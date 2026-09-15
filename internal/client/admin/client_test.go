package admin

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// send drives a request through the shared transport the way the generated
// clients do: EditRequest applies authentication, the retryable client sends.
func send(t *testing.T, client *Client, ctx context.Context, method, path string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, client.BaseURL(path), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.EditRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	resp, err := client.HTTPClient().Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	return resp, err
}

func TestClientAuthenticatesAndRetries(t *testing.T) {
	t.Parallel()

	attempts := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if attempts == 1 {
			return response(request, http.StatusServiceUnavailable, ""), nil
		}
		return response(request, http.StatusOK, `{"value":"ok"}`), nil
	})}
	baseURL, _ := url.Parse("https://api.example.test")
	client, err := NewWithBaseURL(baseURL, "secret", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	client.httpClient.RetryWaitMin = time.Millisecond
	client.httpClient.RetryWaitMax = time.Millisecond

	resp, err := send(t, client, context.Background(), http.MethodGet, "v1/test")
	if err != nil {
		t.Fatalf("send() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d", resp.StatusCode)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestClientWithoutRetry(t *testing.T) {
	t.Parallel()

	attempts := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		return response(request, http.StatusServiceUnavailable, ""), nil
	})}
	baseURL, _ := url.Parse("https://api.example.test")
	client, err := NewWithBaseURL(baseURL, "secret", httpClient)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := send(t, client, WithoutRetry(context.Background()), http.MethodPost, "admin/v1/test")
	if err != nil {
		t.Fatalf("send() error = %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("StatusCode = %d", resp.StatusCode)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestClientWithoutRetryStillRetriesRateLimits(t *testing.T) {
	t.Parallel()

	attempts := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			// Retry-After keeps the retry immediate instead of waiting out the
			// default backoff.
			rateLimited := response(request, http.StatusTooManyRequests, "")
			rateLimited.Header.Set("Retry-After", "0")
			return rateLimited, nil
		}
		return response(request, http.StatusNoContent, ""), nil
	})}
	baseURL, _ := url.Parse("https://api.example.test")
	client, err := NewWithBaseURL(baseURL, "secret", httpClient)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := send(t, client, WithoutRetry(context.Background()), http.MethodPost, "admin/v1/test")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("StatusCode = %d", resp.StatusCode)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func response(request *http.Request, statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

// TestHTTPErrorMessage covers the error the service layer builds from a
// generated client's response; the transport no longer constructs one itself.
func TestHTTPErrorMessage(t *testing.T) {
	t.Parallel()

	withBody := &HTTPError{StatusCode: http.StatusBadRequest, Method: http.MethodGet, URL: "https://api.example.test/v1/test", Body: `{"errors":[{"detail":"bad request"}]}`}
	if got, want := withBody.Error(), `Atlassian API returned Bad Request for GET https://api.example.test/v1/test: {"errors":[{"detail":"bad request"}]}`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	withoutBody := &HTTPError{StatusCode: http.StatusNotFound, Method: http.MethodDelete, URL: "https://api.example.test/v1/test"}
	if got, want := withoutBody.Error(), "Atlassian API returned Not Found for DELETE https://api.example.test/v1/test"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
