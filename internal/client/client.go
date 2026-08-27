package client

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization"
)

// Config contains credentials and shared dependencies for supported Atlassian
// API families. Add family-specific configuration when that client is added.
type Config struct {
	AdminAPIKey string
	HTTPClient  *http.Client
}

// Client composes the API-family-specific services used by the provider.
type Client struct {
	Admin        *admin.Client
	Organization *organization.Service
}

// New validates the configured credentials and creates the available API
// family clients and services.
func New(config Config) (*Client, error) {
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}

	adminAPIKey := strings.TrimSpace(config.AdminAPIKey)
	if adminAPIKey == "" {
		return nil, fmt.Errorf("admin_api_key must be configured")
	}

	adminClient, err := admin.New(adminAPIKey, config.HTTPClient)
	if err != nil {
		return nil, fmt.Errorf("configure Admin API client: %w", err)
	}
	organizationClient, err := organization.NewService(adminClient)
	if err != nil {
		return nil, fmt.Errorf("configure Organization API service: %w", err)
	}

	return &Client{Admin: adminClient, Organization: organizationClient}, nil
}
