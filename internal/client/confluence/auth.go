package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// defaultOAuthEndpoint is where the client credentials grant is exchanged.
const defaultOAuthEndpoint = "https://auth.atlassian.com/oauth/token"

// expiryMargin is how long before a token's expiry the client stops using it.
// Tokens live an hour; refreshing a minute early makes a 401 from expiry rare
// rather than routine.
const expiryMargin = time.Minute

// authenticator adds credentials to outgoing requests. Both modes implement
// it so that the service layer is identical regardless of how the provider was
// configured.
type authenticator interface {
	// authorize sets the Authorization header for a request.
	authorize(ctx context.Context, req *http.Request) error
	// invalidate discards any cached credential after the server rejected it,
	// so the next request obtains a fresh one instead of replaying a
	// credential this client still believes is valid. Modes holding a static
	// credential do nothing.
	invalidate()
}

// basicAuthenticator authenticates as an Atlassian account with an API token.
type basicAuthenticator struct {
	email, apiToken string
}

func (a *basicAuthenticator) authorize(_ context.Context, req *http.Request) error {
	req.SetBasicAuth(a.email, a.apiToken)
	return nil
}

// invalidate does nothing: an API token is static, so discarding it would
// only lose the credential the provider was configured with.
func (a *basicAuthenticator) invalidate() {}

// clientCredentialsAuthenticator authenticates as a service account through
// the OAuth 2.0 client credentials grant. Atlassian issues no refresh token,
// so a new access token is obtained by repeating the exchange.
type clientCredentialsAuthenticator struct {
	clientID, clientSecret string
	tokenURL               *url.URL
	httpClient             *http.Client
	now                    func() time.Time

	mu        sync.Mutex
	token     string
	expiresAt time.Time
	scopes    []string
	exchanges int
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
}

type tokenErrorResponse struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

func (a *clientCredentialsAuthenticator) authorize(ctx context.Context, req *http.Request) error {
	token, err := a.current(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

// current returns a token that is not within expiryMargin of expiring,
// exchanging for a new one under the lock when necessary.
func (a *clientCredentialsAuthenticator) current(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && a.now().Add(expiryMargin).Before(a.expiresAt) {
		return a.token, nil
	}
	return a.exchangeLocked(ctx)
}

// invalidate drops the cached token so the next authorize exchanges a new
// one. It is called after the server rejects a token, which can happen while
// this client's own expiry clock still considers it valid -- a credential
// revoked in the admin console, or clock skew against the token endpoint.
func (a *clientCredentialsAuthenticator) invalidate() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.token = ""
	a.expiresAt = time.Time{}
}

// exchangeLocked performs the client credentials grant. The caller holds mu.
// The client secret never appears in a returned error, and the endpoint's
// response body is reduced to its documented error fields before it does.
func (a *clientCredentialsAuthenticator) exchangeLocked(ctx context.Context) (string, error) {
	body, err := json.Marshal(map[string]string{
		"grant_type":    "client_credentials",
		"client_id":     a.clientID,
		"client_secret": a.clientSecret,
	})
	if err != nil {
		return "", fmt.Errorf("encode token request: %w", err)
	}
	// httpClient here is the helper client: it always applies the default
	// retry policy, so the exchange still retries a transient failure even
	// when the caller's context marks a mutation WithoutRetry, and it never
	// reaches the policy that invalidates this authenticator -- which would
	// deadlock against the lock held here.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.tokenURL.String(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request service account token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}
	a.exchanges++

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var problem tokenErrorResponse
		_ = json.Unmarshal(raw, &problem)
		detail := strings.TrimSpace(strings.Join([]string{problem.Error, problem.Description}, ": "))
		if detail == ":" {
			detail = "no error details returned"
		}
		return "", fmt.Errorf("service account token request returned %s: %s", http.StatusText(resp.StatusCode), strings.Trim(detail, ": "))
	}

	var token tokenResponse
	if err := json.Unmarshal(raw, &token); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if token.AccessToken == "" {
		return "", fmt.Errorf("token response did not include an access token")
	}
	a.token = token.AccessToken
	a.expiresAt = a.now().Add(time.Duration(token.ExpiresIn) * time.Second)
	a.scopes = nil
	if token.Scope != "" {
		a.scopes = strings.Fields(token.Scope)
	}
	return a.token, nil
}

// GrantedScopes returns the scopes the token endpoint reported for the
// service account credential, or nil when none is known: before the first
// exchange, under basic auth, or when the endpoint omitted the field. It is
// advisory only; nothing in the transport depends on it.
func (c *Client) GrantedScopes() []string {
	a, ok := c.auth.(*clientCredentialsAuthenticator)
	if !ok {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.scopes == nil {
		return nil
	}
	return append([]string(nil), a.scopes...)
}
