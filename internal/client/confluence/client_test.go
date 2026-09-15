package confluence

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testCloudID = "a7c408f1-ec5f-410e-8f27-62dbefebe6b6"
	testEmail   = "user@example.com"
	testToken   = "api-token"
	testSecret  = "very-secret-value"
	// The service account's own API token, distinct from testToken so a test
	// cannot pass by sending basic auth's credential.
	testServiceAccountToken = "service-account-api-token"
)

// fakeAtlassian stands in for the site, the gateway and the token endpoint at
// once, so a single httptest server can observe every request a client makes.
type fakeAtlassian struct {
	t *testing.T

	mu       sync.Mutex
	requests []recordedRequest

	tokenExchanges atomic.Int32
	tokenExpiresIn int64
	tokenStatus    int
	tokenBody      string

	tenantInfoCalls atomic.Int32
	tenantInfoFail  bool
	tenantInfoDelay time.Duration

	// apiResponses is consumed in order by API (non-token, non-tenant) calls;
	// when exhausted the server answers 200 with an empty list.
	apiResponses []apiResponse
}

type recordedRequest struct {
	method, path, authorization string
}

type apiResponse struct {
	status  int
	body    string
	headers map[string]string
}

func newFakeAtlassian(t *testing.T) (*fakeAtlassian, *httptest.Server) {
	t.Helper()
	f := &fakeAtlassian{t: t, tokenExpiresIn: 3600, tokenStatus: http.StatusOK}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	return f, server
}

func (f *fakeAtlassian) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, recordedRequest{method: r.Method, path: r.URL.Path, authorization: r.Header.Get("Authorization")})
	f.mu.Unlock()

	switch r.URL.Path {
	case "/oauth/token":
		n := f.tokenExchanges.Add(1)
		if f.tokenStatus != http.StatusOK {
			// auth.atlassian.com answers a rejected exchange with
			// Content-Type: application/json and the RFC 6749 error fields
			// (observed 2026-09-15: 401 {"error":"access_denied",
			// "error_description":"Unauthorized"}). The header is part of the
			// contract here, not decoration: it is what makes those fields
			// readable instead of the raw body being carried into the error.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(f.tokenStatus)
			_, _ = w.Write([]byte(f.tokenBody))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token-" + strconv.Itoa(int(n)),
			"token_type":   "Bearer",
			"expires_in":   f.tokenExpiresIn,
			"scope":        "read:space:confluence write:space:confluence",
		})
	case "/_edge/tenant_info":
		f.tenantInfoCalls.Add(1)
		time.Sleep(f.tenantInfoDelay)
		if f.tenantInfoFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"cloudId":"` + testCloudID + `"}`))
	default:
		f.mu.Lock()
		var next apiResponse
		if len(f.apiResponses) > 0 {
			next, f.apiResponses = f.apiResponses[0], f.apiResponses[1:]
		} else {
			next = apiResponse{status: http.StatusOK, body: `{"results":[]}`}
		}
		f.mu.Unlock()
		for k, v := range next.headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(next.status)
		_, _ = w.Write([]byte(next.body))
	}
}

func (f *fakeAtlassian) apiRequests() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recordedRequest
	for _, r := range f.requests {
		if r.path != "/oauth/token" && r.path != "/_edge/tenant_info" {
			out = append(out, r)
		}
	}
	return out
}

func testOptions(server *httptest.Server) options {
	return options{
		gateway:      server.URL,
		tokenURL:     server.URL + "/oauth/token",
		retryWaitMin: time.Millisecond,
		retryWaitMax: 5 * time.Millisecond,
	}
}

func newBasicClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	c, err := newClient(Config{Mode: AuthBasic, SiteURL: server.URL, Email: testEmail, APIToken: testToken}, testOptions(server))
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	return c
}

func newServiceAccountClient(t *testing.T, server *httptest.Server, cloudID string) *Client {
	t.Helper()
	c, err := newClient(Config{
		Mode: AuthServiceAccount, SiteURL: server.URL, ClientID: "client", ClientSecret: testSecret, CloudID: cloudID,
	}, testOptions(server))
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	return c
}

func newServiceAccountAPITokenClient(t *testing.T, server *httptest.Server, cloudID string) *Client {
	t.Helper()
	c, err := newClient(Config{
		Mode: AuthServiceAccountAPIToken, SiteURL: server.URL, ServiceAccountAPIToken: testServiceAccountToken, CloudID: cloudID,
	}, testOptions(server))
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	return c
}

func getSpaces(t *testing.T, c *Client, ctx context.Context) (int, error) {
	t.Helper()
	v2, err := c.V2(ctx)
	if err != nil {
		return 0, err
	}
	resp, err := v2.GetSpacesWithResponse(ctx, nil)
	if err != nil {
		return 0, err
	}
	return resp.StatusCode(), CheckResponse(http.MethodGet, "/spaces", resp.StatusCode(), resp.Body)
}

func TestBasicAuthSendsHeaderToSitePath(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	c := newBasicClient(t, server)

	if _, err := getSpaces(t, c, context.Background()); err != nil {
		t.Fatalf("GetSpaces error = %v", err)
	}
	reqs := fake.apiRequests()
	if len(reqs) != 1 {
		t.Fatalf("api requests = %d, want 1", len(reqs))
	}
	if reqs[0].path != "/wiki/api/v2/spaces" {
		t.Errorf("path = %q, want /wiki/api/v2/spaces", reqs[0].path)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(testEmail+":"+testToken))
	if reqs[0].authorization != want {
		t.Errorf("Authorization = %q, want %q", reqs[0].authorization, want)
	}
	if fake.tokenExchanges.Load() != 0 || fake.tenantInfoCalls.Load() != 0 {
		t.Error("basic auth must not exchange tokens or discover a cloud id")
	}
}

func TestServiceAccountUsesGatewayPathsAndCachesToken(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	c := newServiceAccountClient(t, server, testCloudID)
	ctx := context.Background()

	if _, err := getSpaces(t, c, ctx); err != nil {
		t.Fatalf("GetSpaces error = %v", err)
	}
	v1, err := c.V1(ctx)
	if err != nil {
		t.Fatalf("V1() error = %v", err)
	}
	if _, err := v1.GetTaskWithResponse(ctx, "1"); err != nil {
		t.Fatalf("GetTask error = %v", err)
	}

	reqs := fake.apiRequests()
	if len(reqs) != 2 {
		t.Fatalf("api requests = %d, want 2", len(reqs))
	}
	if want := "/ex/confluence/" + testCloudID + "/wiki/api/v2/spaces"; reqs[0].path != want {
		t.Errorf("v2 path = %q, want %q", reqs[0].path, want)
	}
	if want := "/ex/confluence/" + testCloudID + "/wiki/rest/api/longtask/1"; reqs[1].path != want {
		t.Errorf("v1 path = %q, want %q", reqs[1].path, want)
	}
	for _, r := range reqs {
		if r.authorization != "Bearer token-1" {
			t.Errorf("Authorization = %q, want Bearer token-1", r.authorization)
		}
	}
	if got := fake.tokenExchanges.Load(); got != 1 {
		t.Errorf("token exchanges = %d, want 1 (cached across requests and versions)", got)
	}
	if fake.tenantInfoCalls.Load() != 0 {
		t.Error("cloud id was given explicitly; discovery must not run")
	}
}

func TestCloudIDDiscoveryIsLazyCachedAndRetriedAfterFailure(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	c := newServiceAccountClient(t, server, "")
	ctx := context.Background()

	if fake.tenantInfoCalls.Load() != 0 {
		t.Fatal("constructing the client must perform no I/O")
	}

	fake.tenantInfoFail = true
	if _, err := getSpaces(t, c, ctx); err == nil || !strings.Contains(err.Error(), "set service_account.cloud_id explicitly") {
		t.Fatalf("expected an actionable discovery error, got %v", err)
	}

	fake.tenantInfoFail = false
	if _, err := getSpaces(t, c, ctx); err != nil {
		t.Fatalf("GetSpaces after discovery recovered: %v", err)
	}
	if _, err := getSpaces(t, c, ctx); err != nil {
		t.Fatalf("second GetSpaces: %v", err)
	}
	// One failed attempt (with retries against the 500) plus exactly one
	// successful discovery; the success is cached for the second call.
	successes := fake.tenantInfoCalls.Load() - int32(maxRetries+1)
	if successes != 1 {
		t.Errorf("tenant_info successful calls = %d, want 1", successes)
	}
	reqs := fake.apiRequests()
	if want := "/ex/confluence/" + testCloudID + "/wiki/api/v2/spaces"; len(reqs) == 0 || reqs[0].path != want {
		t.Errorf("api path = %v, want %q", reqs, want)
	}
}

// TestUnauthorizedIsSurfacedAndDiscardsTheToken pins the replacement for the
// old 401 retry: the request fails once, and the rejected token is dropped so
// the next request fetches a new one. That covers the case the retry could not
// -- a credential the server rejects while this client's clock still thinks it
// is valid, which a resend would have replayed forever.
func TestUnauthorizedIsSurfacedAndDiscardsTheToken(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	c := newServiceAccountClient(t, server, testCloudID)
	ctx := context.Background()

	fake.apiResponses = []apiResponse{{status: http.StatusUnauthorized, body: `{"message":"revoked"}`}}
	status, err := getSpaces(t, c, ctx)
	if err == nil || status != http.StatusUnauthorized {
		t.Fatalf("expected the 401 to surface, got status %d err %v", status, err)
	}
	if got := len(fake.apiRequests()); got != 1 {
		t.Errorf("api requests = %d, want 1: a 401 is not retried", got)
	}

	// The next request must not replay the rejected token.
	if _, err := getSpaces(t, c, ctx); err != nil {
		t.Fatalf("GetSpaces after the 401: %v", err)
	}
	reqs := fake.apiRequests()
	if len(reqs) != 2 || reqs[0].authorization == reqs[1].authorization {
		t.Errorf("second request reused the rejected token: %+v", reqs)
	}
	if got := fake.tokenExchanges.Load(); got != 2 {
		t.Errorf("token exchanges = %d, want 2", got)
	}
}

func TestConcurrentRequestsExchangeTokenOnce(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	c := newServiceAccountClient(t, server, testCloudID)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := getSpaces(t, c, context.Background()); err != nil {
				t.Errorf("GetSpaces error = %v", err)
			}
		}()
	}
	wg.Wait()
	if got := fake.tokenExchanges.Load(); got != 1 {
		t.Errorf("token exchanges = %d, want 1", got)
	}
}

func TestTokenWithinExpiryMarginIsReplaced(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	fake.tokenExpiresIn = 30 // shorter than expiryMargin
	c := newServiceAccountClient(t, server, testCloudID)
	ctx := context.Background()

	for range 2 {
		if _, err := getSpaces(t, c, ctx); err != nil {
			t.Fatalf("GetSpaces error = %v", err)
		}
	}
	if got := fake.tokenExchanges.Load(); got != 2 {
		t.Errorf("token exchanges = %d, want 2: a token inside the expiry margin is not reused", got)
	}
}

func TestWithoutRetrySendsOnceExceptRateLimit(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	c := newBasicClient(t, server)

	fake.apiResponses = []apiResponse{{status: http.StatusInternalServerError}, {status: http.StatusInternalServerError}}
	if _, err := getSpaces(t, c, context.Background()); err != nil {
		t.Fatalf("default policy should retry through two 500s: %v", err)
	}
	if got := len(fake.apiRequests()); got != 3 {
		t.Fatalf("api requests = %d, want 3", got)
	}

	fake.apiResponses = []apiResponse{{status: http.StatusInternalServerError, body: `{"message":"boom"}`}}
	status, err := getSpaces(t, c, WithoutRetry(context.Background()))
	if err == nil || status != http.StatusInternalServerError {
		t.Fatalf("WithoutRetry must surface the 500, got status %d err %v", status, err)
	}
	if got := len(fake.apiRequests()); got != 4 {
		t.Fatalf("api requests = %d, want 4 (single attempt)", got)
	}

	fake.apiResponses = []apiResponse{{status: http.StatusTooManyRequests, headers: map[string]string{"Retry-After": "0"}}}
	if _, err := getSpaces(t, c, WithoutRetry(context.Background())); err != nil {
		t.Fatalf("WithoutRetry must still retry a 429: %v", err)
	}
	if got := len(fake.apiRequests()); got != 6 {
		t.Errorf("api requests = %d, want 6", got)
	}
}

// TestBasicAuthKeepsItsCredentialAfter401 checks that invalidate is a no-op
// for a static credential: discarding it would leave the provider with no way
// to authenticate at all.
func TestBasicAuthDoesNotRetryUnauthorized(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	c := newBasicClient(t, server)

	fake.apiResponses = []apiResponse{{status: http.StatusUnauthorized}}
	if _, err := getSpaces(t, c, context.Background()); err == nil {
		t.Fatal("expected a 401 error")
	}
	if got := len(fake.apiRequests()); got != 1 {
		t.Errorf("api requests = %d, want 1: a 401 is not retried", got)
	}
	if _, err := getSpaces(t, c, context.Background()); err != nil {
		t.Fatalf("basic auth must keep working after a 401: %v", err)
	}
	reqs := fake.apiRequests()
	if len(reqs) != 2 || reqs[0].authorization != reqs[1].authorization {
		t.Errorf("a static credential must survive a 401: %+v", reqs)
	}
}

func TestTokenEndpointErrorIsRedacted(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	fake.tokenStatus = http.StatusUnauthorized
	fake.tokenBody = `{"error":"invalid_client","error_description":"Client authentication failed","echo":"` + testSecret + `"}`
	c := newServiceAccountClient(t, server, testCloudID)

	_, err := getSpaces(t, c, context.Background())
	if err == nil {
		t.Fatal("expected a token error")
	}
	if !strings.Contains(err.Error(), "invalid_client: Client authentication failed") {
		t.Errorf("error should carry the endpoint's documented fields, got %v", err)
	}
	if strings.Contains(err.Error(), testSecret) {
		t.Errorf("error must not echo the response body: %v", err)
	}
	if got := len(fake.apiRequests()); got != 0 {
		t.Errorf("no API request may be sent without a token, got %d", got)
	}
}

func TestCheckResponseShapes(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		status       int
		body         string
		wantContains []string
		wantAbsent   []string
	}{
		"success is nil": {status: 200, body: `{}`},
		"confluence v2 shape": {
			status:       404,
			body:         `{"errors":[{"status":404,"code":"NOT_FOUND","title":"Cannot find a space with id [1]","detail":null}]}`,
			wantContains: []string{"NOT_FOUND Cannot find a space with id [1]"},
			wantAbsent:   []string{"cloud_id"},
		},
		"gateway 404 gets the cloud id hint": {
			status:       404,
			body:         `{"timestamp":"2026-09-14T06:59:19Z","status":404,"error":"Not Found","message":"No message available","path":"/ex/confluence/x/wiki/api/v2/spaces"}`,
			wantContains: []string{"Not Found: No message available", "service_account.cloud_id", "_edge/tenant_info"},
		},
		"admin style shape": {
			status:       400,
			body:         `{"message":"Invalid request data","details":{"field":"name"}}`,
			wantContains: []string{"Invalid request data", `{"field":"name"}`},
		},
		"403 gets the scope hint": {
			status:       403,
			body:         `{"errors":[{"status":403,"code":"FORBIDDEN","title":"Forbidden"}]}`,
			wantContains: []string{"FORBIDDEN Forbidden", "OAuth scopes", "space permissions"},
		},
		"unknown body falls back to raw text": {
			status:       502,
			body:         "<html>bad gateway</html>",
			wantContains: []string{"<html>bad gateway</html>"},
		},
		"empty body": {
			status:       500,
			body:         "",
			wantContains: []string{"Internal Server Error for GET /spaces"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := CheckResponse(http.MethodGet, "/spaces", tt.status, []byte(tt.body))
			if tt.status == 200 {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			var httpErr *HTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode != tt.status {
				t.Fatalf("expected HTTPError %d, got %v", tt.status, err)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q should contain %q", err.Error(), want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(err.Error(), absent) {
					t.Errorf("error %q should not contain %q", err.Error(), absent)
				}
			}
			// A gateway routing 404 is not resource absence: the request never
			// reached Confluence, so it says nothing about the space.
			wantNotFound := tt.status == 404 && !strings.Contains(tt.body, `"path"`)
			if wantNotFound != IsNotFound(err) {
				t.Errorf("IsNotFound = %v for status %d, want %v", IsNotFound(err), tt.status, wantNotFound)
			}
			if httpErr.GatewayRouting != strings.Contains(tt.body, `"path"`) {
				t.Errorf("GatewayRouting = %v for %q", httpErr.GatewayRouting, tt.body)
			}
		})
	}
}

// TestConcurrentCloudIDDiscoveryIsRaceFree drives the path Terraform actually
// takes: several data sources reading at once under a service account whose
// cloud id must still be discovered. Every access to the cached cloud id must
// be synchronized, and discovery must happen once.
func TestConcurrentCloudIDDiscoveryIsRaceFree(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	// Widen the window between the guard read and the cached write so an
	// unsynchronized read is actually caught rather than merely possible.
	fake.tenantInfoDelay = 50 * time.Millisecond
	c := newServiceAccountClient(t, server, "")

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			go func() { _ = c.CloudID() }()
			if _, err := getSpaces(t, c, context.Background()); err != nil {
				t.Errorf("GetSpaces error = %v", err)
			}
			_ = c.CloudID()
		}()
	}
	wg.Wait()
	if got := fake.tenantInfoCalls.Load(); got != 1 {
		t.Errorf("tenant_info calls = %d, want 1", got)
	}
	if got := c.CloudID(); got != testCloudID {
		t.Errorf("CloudID() = %q, want %q", got, testCloudID)
	}
}

// TestWithoutRetryDoesNotResendOn401 is the rule that matters most for a
// non-idempotent mutation: a create marked WithoutRetry must be sent once,
// even when the answer is 401. Authentication is expected to be decided
// before a request is applied, but expectation is not proof, and a resent
// create would produce a second space.
func TestWithoutRetryDoesNotResendOn401(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	c := newServiceAccountClient(t, server, testCloudID)

	fake.apiResponses = []apiResponse{{status: http.StatusUnauthorized, body: `{"message":"expired"}`}}
	if _, err := getSpaces(t, c, WithoutRetry(context.Background())); err == nil {
		t.Fatal("expected the 401 to surface")
	}
	if got := len(fake.apiRequests()); got != 1 {
		t.Errorf("api requests = %d, want 1: a non-idempotent mutation is never resent", got)
	}
}

// TestNestedRequestsRetryUnderWithoutRetry checks the other half of that rule:
// the cloud id lookup and the token exchange are idempotent and are not the
// mutation, so they must still be retried even when the caller's context
// carries WithoutRetry.
func TestNestedRequestsRetryUnderWithoutRetry(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	fake.tenantInfoFail = true
	c := newServiceAccountClient(t, server, "")

	_, _ = getSpaces(t, c, WithoutRetry(context.Background()))
	if got := fake.tenantInfoCalls.Load(); got < 2 {
		t.Errorf("tenant_info calls = %d, want more than 1: discovery is idempotent and must still retry", got)
	}
}

// TestTokenEndpoint401DoesNotDeadlock guards the reason the token exchange
// runs on a separate retry client. When it shared the API client, a 401 from
// the token endpoint reached a policy that invalidates the authenticator,
// which deadlocked against the lock the exchange itself was holding. The
// split makes that unreachable; this test keeps it that way.
func TestTokenEndpoint401DoesNotDeadlock(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	fake.tokenStatus = http.StatusUnauthorized
	fake.tokenBody = `{"error":"invalid_client"}`
	c := newServiceAccountClient(t, server, testCloudID)

	done := make(chan error, 1)
	go func() {
		_, err := getSpaces(t, c, context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "invalid_client") {
			t.Fatalf("error = %v, want the token endpoint's own error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the token exchange deadlocked against authenticator invalidation")
	}
}

// TestGatewayRoutingIsNotResourceAbsence states the distinction plainly,
// because two call sites act on it: Read removes a resource from state when
// it is not found, and Delete accepts a 404 as already deleted. A cloud id
// that does not identify the site produces a routing 404 on every request, so
// conflating the two would let a refresh forget a live space and let a
// destroy record one as deleted while it is still there.
func TestGatewayRoutingIsNotResourceAbsence(t *testing.T) {
	t.Parallel()

	gateway := CheckResponse(http.MethodGet, "/spaces/1", http.StatusNotFound,
		[]byte(`{"timestamp":"t","status":404,"error":"Not Found","message":"No message available","path":"/ex/confluence/x/wiki/api/v2/spaces/1"}`))
	confluence := CheckResponse(http.MethodGet, "/spaces/1", http.StatusNotFound,
		[]byte(`{"errors":[{"status":404,"code":"NOT_FOUND","title":"Cannot find a space with id [1]"}]}`))

	if IsNotFound(gateway) {
		t.Error("a gateway routing 404 must not read as resource absence")
	}
	var gatewayErr *HTTPError
	if !errors.As(gateway, &gatewayErr) || !gatewayErr.GatewayRouting {
		t.Error("a gateway routing 404 must be identifiable as such")
	}
	if !IsNotFound(confluence) {
		t.Error("a Confluence 404 must read as resource absence")
	}
	var confluenceErr *HTTPError
	if !errors.As(confluence, &confluenceErr) || confluenceErr.GatewayRouting {
		t.Error("a Confluence 404 is not a routing failure")
	}
}

// TestServiceAccountAPITokenUsesGatewayWithoutExchange covers the mode's whole
// contract: the configured token is sent as it is, to the gateway route the
// client credentials mode uses, and no token endpoint is involved at all.
func TestServiceAccountAPITokenUsesGatewayWithoutExchange(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	c := newServiceAccountAPITokenClient(t, server, testCloudID)
	ctx := context.Background()

	if _, err := getSpaces(t, c, ctx); err != nil {
		t.Fatalf("GetSpaces error = %v", err)
	}
	v1, err := c.V1(ctx)
	if err != nil {
		t.Fatalf("V1() error = %v", err)
	}
	if _, err := v1.GetTaskWithResponse(ctx, "1"); err != nil {
		t.Fatalf("GetTask error = %v", err)
	}

	reqs := fake.apiRequests()
	if len(reqs) != 2 {
		t.Fatalf("api requests = %d, want 2", len(reqs))
	}
	if want := "/ex/confluence/" + testCloudID + "/wiki/api/v2/spaces"; reqs[0].path != want {
		t.Errorf("v2 path = %q, want %q", reqs[0].path, want)
	}
	if want := "/ex/confluence/" + testCloudID + "/wiki/rest/api/longtask/1"; reqs[1].path != want {
		t.Errorf("v1 path = %q, want %q", reqs[1].path, want)
	}
	for _, r := range reqs {
		if r.authorization != "Bearer "+testServiceAccountToken {
			t.Errorf("Authorization = %q, want the configured token as a bearer", r.authorization)
		}
	}
	if fake.tokenExchanges.Load() != 0 {
		t.Error("an API token must not be exchanged for another token")
	}
	if fake.tenantInfoCalls.Load() != 0 {
		t.Error("cloud id was given explicitly; discovery must not run")
	}
}

// TestServiceAccountAPITokenDiscoversCloudID checks the half of the gateway
// behavior the token itself cannot provide: /oauth/token/accessible-resources
// refuses an API token, so the site's own tenant_info stays the only source.
func TestServiceAccountAPITokenDiscoversCloudID(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	c := newServiceAccountAPITokenClient(t, server, "")

	if _, err := getSpaces(t, c, context.Background()); err != nil {
		t.Fatalf("GetSpaces error = %v", err)
	}
	if got := fake.tenantInfoCalls.Load(); got != 1 {
		t.Errorf("tenant_info calls = %d, want 1", got)
	}
	if got := c.CloudID(); got != testCloudID {
		t.Errorf("CloudID() = %q, want %q", got, testCloudID)
	}
	if got := fake.apiRequests()[0].path; got != "/ex/confluence/"+testCloudID+"/wiki/api/v2/spaces" {
		t.Errorf("path = %q, want the discovered cloud id in the gateway route", got)
	}
}

// TestServiceAccountAPITokenKeepsItsCredentialAfter401 states that invalidate
// is a no-op here for the same reason it is under basic auth: the token is the
// one the provider was configured with, so discarding it would leave nothing
// to authenticate with.
func TestServiceAccountAPITokenKeepsItsCredentialAfter401(t *testing.T) {
	t.Parallel()
	fake, server := newFakeAtlassian(t)
	c := newServiceAccountAPITokenClient(t, server, testCloudID)

	fake.apiResponses = []apiResponse{{status: http.StatusUnauthorized}}
	if _, err := getSpaces(t, c, context.Background()); err == nil {
		t.Fatal("expected the 401 to surface")
	}
	if _, err := getSpaces(t, c, context.Background()); err != nil {
		t.Fatalf("the credential must survive a 401: %v", err)
	}
	reqs := fake.apiRequests()
	if len(reqs) != 2 || reqs[0].authorization != reqs[1].authorization {
		t.Errorf("a static credential must survive a 401: %+v", reqs)
	}
	if fake.tokenExchanges.Load() != 0 {
		t.Error("a 401 must not trigger a token exchange in this mode")
	}
}
