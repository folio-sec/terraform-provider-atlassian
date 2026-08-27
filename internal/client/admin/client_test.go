package admin

import (
	"context"
	"errors"
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

func TestClientDoAuthenticatesAndRetries(t *testing.T) {
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

	var result struct {
		Value string `json:"value"`
	}
	if err := client.Do(context.Background(), http.MethodGet, "v1/test", url.Values{"a": []string{"b"}}, nil, &result); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if result.Value != "ok" {
		t.Fatalf("Value = %q", result.Value)
	}
}

func TestClientDoReturnsHTTPError(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusBadRequest, `{"errors":[{"detail":"bad request"}]}`), nil
	})}
	baseURL, _ := url.Parse("https://api.example.test")
	client, err := NewWithBaseURL(baseURL, "secret", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	err = client.Do(context.Background(), http.MethodGet, "v1/test", nil, nil, nil)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("StatusCode = %d", httpErr.StatusCode)
	}
}

func TestClientDoWithoutRetry(t *testing.T) {
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

	err = client.DoWithoutRetry(context.Background(), http.MethodPost, "admin/v1/test", nil, map[string]string{"value": "test"}, nil)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %v, want *HTTPError", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestClientDoWithoutRetryStillRetriesRateLimits(t *testing.T) {
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

	if err := client.DoWithoutRetry(context.Background(), http.MethodPost, "admin/v1/test", nil, map[string]string{"value": "test"}, nil); err != nil {
		t.Fatal(err)
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
