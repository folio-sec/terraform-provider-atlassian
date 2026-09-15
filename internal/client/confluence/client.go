package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	v1gen "github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence/v1/generated"
	v2gen "github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence/v2/generated"
	"github.com/hashicorp/go-retryablehttp"
)

const maxRetries = 4

type contextKey int

const disableRetryKey contextKey = iota

// WithoutRetry marks a request context so the transport sends the request
// only once, apart from a 429, which the server rejects before applying
// anything. Notably a 401 is NOT retried here; see checkRetry. Use it for
// non-idempotent mutations whose outcome would be ambiguous if the response
// were lost.
func WithoutRetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, disableRetryKey, true)
}

// Rate limiting is deliberately left to retryablehttp's default backoff,
// unlike the Admin transport, which paces every request and honours
// X-RateLimit-Reset across its whole surface. Confluence's throttling
// response was never observed: no request during verification was rate
// limited, so the headers it returns and the reset semantics behind them are
// unknown (plans/confluence-space-verification.json records this). Porting
// Admin's pacing would encode assumptions about a response this provider has
// never seen. Revisit once a real 429 from Confluence has been captured; a
// shared limiter here would also need to pace the two clients together.
//
// newRetryClient builds a retry client over a copy of the caller's transport,
// so per-family settings never mutate http.DefaultClient or a transport shared
// with the Admin family. retryablehttp carries its retry policy on the client
// rather than the request, which is why the two policies this package needs
// are two clients rather than one client and a per-call flag.
func newRetryClient(opts options, httpClient *http.Client, policy retryablehttp.CheckRetry) *retryablehttp.Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	base := *httpClient

	retryClient := retryablehttp.NewClient()
	retryClient.HTTPClient = &base
	retryClient.Logger = nil
	retryClient.RetryMax = maxRetries
	retryClient.RetryWaitMin = opts.retryWaitMin
	retryClient.RetryWaitMax = opts.retryWaitMax
	retryClient.ErrorHandler = retryablehttp.PassthroughErrorHandler
	retryClient.CheckRetry = policy
	return retryClient
}

// checkRetry mirrors the Admin transport's policy. A WithoutRetry request is
// sent once, apart from a 429, which the server rejects before applying
// anything. There is deliberately no carve-out for 401: the token is refreshed
// before every request that is within expiryMargin of expiry, so a 401 from
// expiry needs the token to lapse in the moment between that check and the
// server's, and a 401 from any other cause is not fixed by resending. What a
// 401 does do is discard the cached token, so the next attempt fetches a new
// one -- see checkResponse.
func (c *Client) checkRetry(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if resp != nil && resp.StatusCode == http.StatusUnauthorized {
		// A token the server rejects must not be reused, even when this
		// client's own clock still considers it valid: a credential revoked in
		// the admin console would otherwise fail every later request too.
		// Only API calls reach this policy, so invalidating here cannot
		// re-enter the token exchange that produced the credential.
		c.auth.invalidate()
	}
	if disable, _ := ctx.Value(disableRetryKey).(bool); disable {
		// A rate limited request is rejected before the server applies it, so
		// resending it cannot duplicate a mutation. Every other outcome stays
		// single shot because the mutation may already have taken effect.
		return err == nil && resp != nil && resp.StatusCode == http.StatusTooManyRequests, nil
	}
	retry, policyErr := retryablehttp.DefaultRetryPolicy(ctx, resp, err)
	if policyErr != nil {
		return retry, fmt.Errorf("evaluate retry policy: %w", policyErr)
	}
	return retry, nil
}

// HTTPClient returns the client the generated API clients send through. It
// authenticates nothing itself; authentication is applied by EditRequest so
// that a refreshed token is visible to the retry loop.
func (c *Client) HTTPClient() *http.Client {
	return c.httpClient.StandardClient()
}

// ErrAuthorize marks a failure to authenticate a request. The generated
// clients run request editors before sending, so an error wrapping this one
// means nothing was sent -- which callers of non-idempotent operations need to
// distinguish from an outcome the server may already have applied.
var ErrAuthorize = errors.New("authorize request")

// EditRequest adds authentication and the JSON accept header to a request
// created by a generated client.
func (c *Client) EditRequest(ctx context.Context, req *http.Request) error {
	if err := c.auth.authorize(ctx, req); err != nil {
		return fmt.Errorf("%w: %w", ErrAuthorize, err)
	}
	req.Header.Set("Accept", "application/json")
	return nil
}

// Prefix resolves the part of every URL that precedes "/wiki". Under a
// service account it may need one unauthenticated request to discover the
// cloud id; the result is cached, a failure is not.
func (c *Client) Prefix(ctx context.Context) (*url.URL, error) {
	cloudID, err := c.resolvedCloudID(ctx)
	if err != nil {
		return nil, err
	}
	prefix, err := Prefix(c.mode, c.site, cloudID)
	if err != nil {
		return nil, err
	}
	if c.mode == AuthServiceAccount {
		gateway, err := url.Parse(c.options.gateway)
		if err != nil {
			return nil, fmt.Errorf("parse gateway URL: %w", err)
		}
		prefix.Scheme, prefix.Host = gateway.Scheme, gateway.Host
	}
	return prefix, nil
}

// resolvedCloudID returns the cloud id, discovering it on first use. Every
// read of c.cloudID goes through here so that it is never read outside c.mu:
// Terraform reads data sources concurrently, so several goroutines reach this
// before the id is known. Discovery holds the lock across its request, which
// serializes the concurrent callers onto a single lookup -- the intent -- at
// the cost of blocking them while it runs.
func (c *Client) resolvedCloudID(ctx context.Context) (string, error) {
	if c.mode != AuthServiceAccount {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cloudID == "" {
		if err := c.discoverCloudIDLocked(ctx); err != nil {
			return "", err
		}
	}
	return c.cloudID, nil
}

// discoverCloudIDLocked reads the site's cloud id from _edge/tenant_info,
// which needs no authentication. The caller holds mu.
func (c *Client) discoverCloudIDLocked(ctx context.Context) error {
	if c.cloudID != "" {
		return nil
	}
	endpoint := c.site.JoinPath("/_edge/tenant_info")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create cloud id discovery request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.helperClient.StandardClient().Do(req)
	if err != nil {
		return fmt.Errorf("discover cloud id from %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return fmt.Errorf("read cloud id discovery response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("discover cloud id from %s: returned %s; set service_account.cloud_id explicitly", endpoint, http.StatusText(resp.StatusCode))
	}
	var payload struct {
		CloudID string `json:"cloudId"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || strings.TrimSpace(payload.CloudID) == "" {
		return fmt.Errorf("discover cloud id from %s: response did not include cloudId; set service_account.cloud_id explicitly", endpoint)
	}
	c.cloudID = strings.TrimSpace(payload.CloudID)
	return nil
}

// V1 returns the generated v1 client, building it on first use once the base
// URL is known.
func (c *Client) V1(ctx context.Context) (*v1gen.ClientWithResponses, error) {
	c.mu.Lock()
	if c.v1 != nil {
		defer c.mu.Unlock()
		return c.v1, nil
	}
	c.mu.Unlock()

	prefix, err := c.Prefix(ctx)
	if err != nil {
		return nil, err
	}
	v1Base, _ := BaseURLs(prefix)
	built, err := v1gen.NewClientWithResponses(v1Base.String(), v1gen.WithHTTPClient(c.HTTPClient()), v1gen.WithRequestEditorFn(c.EditRequest))
	if err != nil {
		return nil, fmt.Errorf("configure Confluence v1 client: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.v1 == nil {
		c.v1 = built
	}
	return c.v1, nil
}

// V2 returns the generated v2 client, building it on first use once the base
// URL is known.
func (c *Client) V2(ctx context.Context) (*v2gen.ClientWithResponses, error) {
	c.mu.Lock()
	if c.v2 != nil {
		defer c.mu.Unlock()
		return c.v2, nil
	}
	c.mu.Unlock()

	prefix, err := c.Prefix(ctx)
	if err != nil {
		return nil, err
	}
	_, v2Base := BaseURLs(prefix)
	built, err := v2gen.NewClientWithResponses(v2Base.String(), v2gen.WithHTTPClient(c.HTTPClient()), v2gen.WithRequestEditorFn(c.EditRequest))
	if err != nil {
		return nil, fmt.Errorf("configure Confluence v2 client: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.v2 == nil {
		c.v2 = built
	}
	return c.v2, nil
}
