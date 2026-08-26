package client

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization"
)

// Config contains credentials for the independent Atlassian API families.
// Site credentials are retained for future Jira and Confluence services, while
// AdminAPIKey authenticates Cloud Admin APIs hosted at api.atlassian.com.
type Config struct {
	SiteURL     string
	Email       string
	APIToken    string
	AdminAPIKey string
	HTTPClient  *http.Client
}

// SiteConfig contains the shared configuration for product APIs.
type SiteConfig struct {
	BaseURL  *url.URL
	Email    string
	APIToken string
}

// Client composes the API-family-specific services used by the provider.
type Client struct {
	Site         *SiteConfig
	Admin        *admin.Client
	Organization *organization.Service
}

// New validates the configured credential sets and creates the available API
// services. Product and Admin credentials can be configured independently.
func New(config Config) (*Client, error) {
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}

	client := &Client{}

	siteURL := strings.TrimSpace(config.SiteURL)
	email := strings.TrimSpace(config.Email)
	apiToken := strings.TrimSpace(config.APIToken)
	if siteURL != "" || email != "" || apiToken != "" {
		if siteURL == "" || email == "" || apiToken == "" {
			return nil, fmt.Errorf("site_url, email, and api_token must be configured together")
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

		client.Site = &SiteConfig{BaseURL: baseURL, Email: email, APIToken: apiToken}
	}

	adminAPIKey := strings.TrimSpace(config.AdminAPIKey)
	if adminAPIKey != "" {
		adminClient, err := admin.New(adminAPIKey, config.HTTPClient)
		if err != nil {
			return nil, fmt.Errorf("configure Admin API client: %w", err)
		}
		organizationClient, err := organization.NewService(adminClient)
		if err != nil {
			return nil, fmt.Errorf("configure Organization API service: %w", err)
		}
		client.Admin = adminClient
		client.Organization = organizationClient
	}

	if client.Site == nil && client.Admin == nil {
		return nil, fmt.Errorf("at least one credential set must be configured")
	}

	return client, nil
}
