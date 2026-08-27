package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
// request only once. Use this for non-idempotent mutations whose outcome would
// be ambiguous if their response were lost.
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
	retryClient.HTTPClient = httpClient
	retryClient.Logger = nil
	retryClient.RetryMax = 4
	retryClient.ErrorHandler = retryablehttp.PassthroughErrorHandler
	retryClient.CheckRetry = func(ctx context.Context, response *http.Response, err error) (bool, error) {
		if disableRetry, _ := ctx.Value(disableRetryContextKey{}).(bool); disableRetry {
			return false, nil
		}
		return retryablehttp.DefaultRetryPolicy(ctx, response, err)
	}

	return &Client{baseURL: &baseURLCopy, apiKey: apiKey, httpClient: retryClient}, nil
}

// Do sends an authenticated JSON request and decodes a successful response.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, requestBody, responseBody any) error {
	return c.do(ctx, method, path, query, requestBody, responseBody)
}

// DoWithoutRetry sends a request exactly once. It is intended for mutations
// whose result can be ambiguous if a response is lost after the server applies
// the change.
func (c *Client) DoWithoutRetry(ctx context.Context, method, path string, query url.Values, requestBody, responseBody any) error {
	return c.do(WithoutRetry(ctx), method, path, query, requestBody, responseBody)
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

func (c *Client) do(ctx context.Context, method, path string, query url.Values, requestBody, responseBody any) error {
	var body []byte
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		body = encoded
	}

	requestURL := *c.baseURL
	requestURL.Path = strings.TrimRight(c.baseURL.Path, "/") + "/" + strings.TrimLeft(path, "/")
	requestURL.RawQuery = query.Encode()
	req, err := retryablehttp.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		errorBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if readErr != nil {
			return fmt.Errorf("read error response: %w", readErr)
		}
		return &HTTPError{
			StatusCode: resp.StatusCode,
			Method:     method,
			URL:        requestURL.String(),
			Body:       strings.TrimSpace(string(errorBody)),
		}
	}

	if responseBody == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(responseBody); err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	return nil
}
