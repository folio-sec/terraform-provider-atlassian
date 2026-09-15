package confluence

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
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

// bearerAuthenticator authenticates with a token the provider was configured
// with, rather than one this client obtained. A service account API token is
// such a credential: it is sent as a bearer token to the gateway and carries
// the scopes chosen when it was created.
type bearerAuthenticator struct {
	token string
}

func (a *bearerAuthenticator) authorize(_ context.Context, req *http.Request) error {
	req.Header.Set("Authorization", "Bearer "+a.token)
	return nil
}

// invalidate does nothing, for the same reason basicAuthenticator's does not:
// the credential is static, so discarding it would only lose the one the
// provider was configured with.
func (a *bearerAuthenticator) invalidate() {}

// clientCredentialsAuthenticator authenticates as a service account through
// the OAuth 2.0 client credentials grant. Atlassian issues no refresh token,
// so a new access token is obtained by repeating the exchange.
//
// The client credentials grant itself is golang.org/x/oauth2/clientcredentials;
// only the caching around it is local, because this client must be able to
// discard a token the server has rejected. That is what invalidate does and
// what checkRetry calls on a 401; no oauth2.TokenSource exposes it, and
// oauth2.ReuseTokenSource decides by expiry alone, so a credential revoked in
// the admin console would keep being replayed until its hour was up. Caching
// here also keeps the request's context on the exchange, which
// Config.TokenSource(ctx) would fix at construction instead.
//
// AuthStyleInParams sends the credentials in the request body, which is what
// this provider has always done. Measured against auth.atlassian.com on
// 2026-09-15 with a real service account credential: a form-encoded body, a
// JSON body, and form plus HTTP Basic client authentication all returned 200
// with token_type=Bearer and expires_in=3600. The endpoint accepts all three,
// so the library's encoding is not a departure from what was verified.
type clientCredentialsAuthenticator struct {
	config     clientcredentials.Config
	httpClient *http.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
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
	if a.token != "" && time.Now().Add(expiryMargin).Before(a.expiresAt) {
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
// The client secret never appears in a returned error: a token endpoint error
// is reported through its documented RFC 6749 fields, and any other failure is
// reported without the response body, which oauth2.RetrieveError would
// otherwise include verbatim.
func (a *clientCredentialsAuthenticator) exchangeLocked(ctx context.Context) (string, error) {
	// httpClient here is the helper client: it always applies the default
	// retry policy, so the exchange still retries a transient failure even
	// when the caller's context marks a mutation WithoutRetry, and it never
	// reaches the policy that invalidates this authenticator -- which would
	// deadlock against the lock held here.
	token, err := a.config.Token(context.WithValue(ctx, oauth2.HTTPClient, a.httpClient))
	if err != nil {
		return "", exchangeError(err)
	}
	if token.AccessToken == "" {
		return "", fmt.Errorf("token response did not include an access token")
	}
	a.token = token.AccessToken
	// A zero Expiry means oauth2 saw no expires_in. oauth2 reads that as a
	// token that never expires; the check in current reads it as one that is
	// already past its margin, so the next request exchanges again. Erring
	// towards an extra exchange is the safe direction, and the endpoint has
	// always returned expires_in=3600 in practice.
	a.expiresAt = token.Expiry
	return a.token, nil
}

// exchangeError reduces a failed exchange to what is safe to surface. An
// endpoint that answered in the RFC 6749 error shape is reported by its error
// code and description; anything else keeps its status and drops its body,
// because a body this client did not parse may contain the request it echoed.
func exchangeError(err error) error {
	var retrieveErr *oauth2.RetrieveError
	if !errors.As(err, &retrieveErr) {
		return fmt.Errorf("request service account token: %w", err)
	}
	status := ""
	if retrieveErr.Response != nil {
		status = http.StatusText(retrieveErr.Response.StatusCode)
	}
	detail := strings.TrimSpace(strings.Trim(retrieveErr.ErrorCode+": "+retrieveErr.ErrorDescription, ": "))
	if detail == "" {
		detail = "no error details returned"
	}
	return fmt.Errorf("service account token request returned %s: %s", status, detail)
}
