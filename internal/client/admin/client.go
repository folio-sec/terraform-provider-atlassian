package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/hashicorp/go-retryablehttp"
)

const defaultBaseURL = "https://api.atlassian.com"

// Client is the shared authenticated transport for Atlassian Cloud Admin API
// families. Typed Organization and User Management services are built on top
// of this transport.
type Client struct {
	baseURL    *url.URL
	apiKey     string
	httpClient *retryablehttp.Client
}

type disableRetryContextKey struct{}

// WithoutRetry marks a request context so the shared retry transport sends the
// request only once, apart from rate limiting. Use this for non-idempotent
// mutations whose outcome would be ambiguous if their response were lost.
func WithoutRetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, disableRetryContextKey{}, true)
}

// HTTPError represents a non-2xx response from an Atlassian API.
type HTTPError struct {
	StatusCode int
	Method     string
	URL        string
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("Atlassian API returned %s for %s %s", http.StatusText(e.StatusCode), e.Method, e.URL)
	}
	return fmt.Sprintf("Atlassian API returned %s for %s %s: %s", http.StatusText(e.StatusCode), e.Method, e.URL, e.Body)
}

// New creates a Cloud Admin API transport using an organization API key.
func New(apiKey string, httpClient *http.Client) (*Client, error) {
	baseURL, err := url.Parse(defaultBaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Admin API base URL: %w", err)
	}
	return NewWithBaseURL(baseURL, apiKey, httpClient)
}

// NewWithBaseURL creates a transport with a custom base URL. It is exported to
// support deterministic consumers and tests without changing global state.
func NewWithBaseURL(baseURL *url.URL, apiKey string, httpClient *http.Client) (*Client, error) {
	if baseURL == nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("admin API base URL must be absolute")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("admin API key must be configured")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	baseURLCopy := *baseURL
	baseURLCopy.Path = strings.TrimRight(baseURLCopy.Path, "/")
	retryClient := retryablehttp.NewClient()
	// Copy the caller's client so installing shared throttling does not mutate
	// http.DefaultClient or a transport used by another provider configuration.
	limitedClient := *httpClient
	transport := httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	limitedClient.Transport = &rateLimitedTransport{base: transport}
	retryClient.HTTPClient = &limitedClient
	retryClient.Logger = nil
	retryClient.RetryMax = 4
	retryClient.ErrorHandler = retryablehttp.PassthroughErrorHandler
	retryClient.Backoff = sharedRateLimitBackoff
	retryClient.CheckRetry = func(ctx context.Context, response *http.Response, err error) (bool, error) {
		if errors.Is(err, errRateLimitWait) {
			return false, err
		}
		if disableRetry, _ := ctx.Value(disableRetryContextKey{}).(bool); disableRetry {
			// A rate limited request is rejected before the server applies it, so
			// resending it cannot duplicate a mutation. Any other outcome stays
			// single shot because the mutation may already have taken effect.
			if err == nil && response != nil && response.StatusCode == http.StatusTooManyRequests {
				return true, nil
			}
			return false, nil
		}
		return retryablehttp.DefaultRetryPolicy(ctx, response, err)
	}

	return &Client{baseURL: &baseURLCopy, apiKey: apiKey, httpClient: retryClient}, nil
}

// HTTPClient returns a standard net/http facade backed by the shared
// retryable transport. Generated API clients use this without taking ownership
// of authentication or retry policy.
func (c *Client) HTTPClient() *http.Client {
	return c.httpClient.StandardClient()
}

// BaseURL returns the configured Admin API endpoint with path appended.
func (c *Client) BaseURL(path string) string {
	requestURL := *c.baseURL
	requestURL.Path = strings.TrimRight(c.baseURL.Path, "/") + "/" + strings.Trim(path, "/")
	return strings.TrimRight(requestURL.String(), "/")
}

// EditRequest adds the shared Admin API authentication and media headers to a
// request created by a generated client.
func (c *Client) EditRequest(_ context.Context, req *http.Request) error {
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	return nil
}
