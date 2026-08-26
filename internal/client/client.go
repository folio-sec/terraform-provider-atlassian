package client

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Client contains the shared configuration used by Atlassian resources and
// data sources. Product-specific services can be added here as the provider
// grows.
type Client struct {
	BaseURL    *url.URL
	Email      string
	APIToken   string
	HTTPClient *http.Client
}

// New validates the provider configuration and creates an Atlassian API client.
func New(siteURL, email, apiToken string, httpClient *http.Client) (*Client, error) {
	if strings.TrimSpace(siteURL) == "" {
		return nil, fmt.Errorf("site_url must be configured")
	}

	baseURL, err := url.Parse(strings.TrimRight(siteURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse site_url: %w", err)
	}
	if baseURL.Scheme != "https" || baseURL.Host == "" {
		return nil, fmt.Errorf("site_url must be an absolute HTTPS URL")
	}
	if baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, fmt.Errorf("site_url must not contain a query or fragment")
	}
	if strings.TrimSpace(email) == "" {
		return nil, fmt.Errorf("email must be configured")
	}
	if strings.TrimSpace(apiToken) == "" {
		return nil, fmt.Errorf("api_token must be configured")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &Client{
		BaseURL:    baseURL,
		Email:      email,
		APIToken:   apiToken,
		HTTPClient: httpClient,
	}, nil
}
