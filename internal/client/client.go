package client

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/control"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence"
)

// Config contains credentials and shared dependencies for supported Atlassian
// API families. Each family is optional; a family whose credentials are absent
// is left nil on the resulting Client and the data sources and resources that
// need it report that in their own Configure.
type Config struct {
	// AdminAPIKey authenticates the Cloud Admin API families (Organization
	// and Admin Control). Empty leaves those families unconfigured.
	AdminAPIKey string
	// Confluence configures the Confluence Cloud family. Nil leaves it
	// unconfigured.
	Confluence *confluence.Config
	HTTPClient *http.Client
}

// Client composes the API-family-specific services used by the provider. Any
// field may be nil when that family was not configured.
type Client struct {
	Admin        *admin.Client
	Control      *control.Service
	Organization *organization.Service
	Confluence   *confluence.Client
}

// New creates the API family clients whose credentials are present. It does
// not require any particular family: an Admin-only configuration, a
// Confluence-only configuration, and a configuration with both are all valid.
// A configuration with neither is also accepted, so that a provider block that
// relies entirely on environment variables absent in one environment fails at
// the data source or resource that needs the family, with a diagnostic naming
// what to set, rather than at provider configuration.
func New(config Config) (*Client, error) {
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}

	result := &Client{}

	if adminAPIKey := strings.TrimSpace(config.AdminAPIKey); adminAPIKey != "" {
		adminClient, err := admin.New(adminAPIKey, config.HTTPClient)
		if err != nil {
			return nil, fmt.Errorf("configure Admin API client: %w", err)
		}
		organizationClient, err := organization.NewService(adminClient)
		if err != nil {
			return nil, fmt.Errorf("configure Organization API service: %w", err)
		}
		controlClient, err := control.NewService(adminClient)
		if err != nil {
			return nil, fmt.Errorf("configure Admin Control API service: %w", err)
		}
		result.Admin, result.Organization, result.Control = adminClient, organizationClient, controlClient
	}

	if config.Confluence != nil {
		confluenceConfig := *config.Confluence
		if confluenceConfig.HTTPClient == nil {
			confluenceConfig.HTTPClient = config.HTTPClient
		}
		confluenceClient, err := confluence.New(confluenceConfig)
		if err != nil {
			return nil, fmt.Errorf("configure Confluence client: %w", err)
		}
		result.Confluence = confluenceClient
	}

	return result, nil
}
