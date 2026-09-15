package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestProviderMetadata(t *testing.T) {
	t.Parallel()

	var resp provider.MetadataResponse
	New("test")().Metadata(context.Background(), provider.MetadataRequest{}, &resp)

	if resp.TypeName != "atlassian" {
		t.Fatalf("TypeName = %q, want %q", resp.TypeName, "atlassian")
	}
	if resp.Version != "test" {
		t.Fatalf("Version = %q, want %q", resp.Version, "test")
	}
}

func TestProviderSchema(t *testing.T) {
	t.Parallel()

	var resp provider.SchemaResponse
	New("test")().Schema(context.Background(), provider.SchemaRequest{}, &resp)

	if got := len(resp.Schema.Attributes); got != 4 {
		t.Fatalf("schema attribute count = %d, want 4", got)
	}
	for _, name := range []string{"admin_api_key", "site_url", "basic_auth", "service_account"} {
		if _, ok := resp.Schema.Attributes[name]; !ok {
			t.Errorf("schema attribute %s is missing", name)
		}
	}
	if !resp.Schema.Attributes["admin_api_key"].IsSensitive() {
		t.Error("admin_api_key must be sensitive")
	}

	basic, ok := resp.Schema.Attributes["basic_auth"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("basic_auth is %T, want SingleNestedAttribute", resp.Schema.Attributes["basic_auth"])
	}
	if !basic.Attributes["api_token"].IsSensitive() {
		t.Error("basic_auth.api_token must be sensitive")
	}
	if basic.Attributes["email"].IsSensitive() {
		t.Error("basic_auth.email must not be sensitive")
	}

	account, ok := resp.Schema.Attributes["service_account"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("service_account is %T, want SingleNestedAttribute", resp.Schema.Attributes["service_account"])
	}
	if !account.Attributes["client_secret"].IsSensitive() {
		t.Error("service_account.client_secret must be sensitive")
	}
	if account.Attributes["client_id"].IsSensitive() {
		t.Error("service_account.client_id must not be sensitive")
	}
	if _, ok := account.Attributes["cloud_id"]; !ok {
		t.Error("service_account.cloud_id is missing")
	}
}

// providerConfig builds a tfsdk.Config against the provider schema. Attributes
// not listed in values are null. Nested blocks are given as maps of their own
// attributes; a nil map leaves the block null.
func providerConfig(t *testing.T, values map[string]any) tfsdk.Config {
	t.Helper()
	ctx := context.Background()
	var resp provider.SchemaResponse
	New("test")().Schema(ctx, provider.SchemaRequest{}, &resp)
	objectType, ok := resp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatalf("schema type is %T, want tftypes.Object", resp.Schema.Type().TerraformType(ctx))
	}

	raw := map[string]tftypes.Value{}
	for name, attributeType := range objectType.AttributeTypes {
		value, given := values[name]
		if !given {
			raw[name] = tftypes.NewValue(attributeType, nil)
			continue
		}
		raw[name] = toValue(t, attributeType, value)
	}
	return tfsdk.Config{Schema: resp.Schema, Raw: tftypes.NewValue(objectType, raw)}
}

// unknown marks a test value as unknown in the configuration.
type unknown struct{}

func toValue(t *testing.T, attributeType tftypes.Type, value any) tftypes.Value {
	t.Helper()
	switch v := value.(type) {
	case unknown:
		return tftypes.NewValue(attributeType, tftypes.UnknownValue)
	case string:
		return tftypes.NewValue(attributeType, v)
	case map[string]any:
		nested, ok := attributeType.(tftypes.Object)
		if !ok {
			t.Fatalf("attribute type %v does not accept a nested map", attributeType)
		}
		raw := map[string]tftypes.Value{}
		for name, fieldType := range nested.AttributeTypes {
			field, given := v[name]
			if !given {
				raw[name] = tftypes.NewValue(fieldType, nil)
				continue
			}
			raw[name] = toValue(t, fieldType, field)
		}
		return tftypes.NewValue(nested, raw)
	default:
		t.Fatalf("unsupported test value %T", value)
		return tftypes.Value{}
	}
}

func validateConfig(t *testing.T, values map[string]any) diag.Diagnostics {
	t.Helper()
	p, ok := New("test")().(provider.ProviderWithValidateConfig)
	if !ok {
		t.Fatal("provider does not implement ProviderWithValidateConfig")
	}
	var resp provider.ValidateConfigResponse
	p.ValidateConfig(context.Background(), provider.ValidateConfigRequest{Config: providerConfig(t, values)}, &resp)
	return resp.Diagnostics
}

func requireDiagnostic(t *testing.T, diags diag.Diagnostics, severity diag.Severity, wantSubstring string) {
	t.Helper()
	for _, d := range diags {
		if d.Severity() != severity {
			continue
		}
		if strings.Contains(d.Summary(), wantSubstring) || strings.Contains(d.Detail(), wantSubstring) {
			return
		}
	}
	t.Fatalf("no %v diagnostic mentioning %q in %v", severity, wantSubstring, diags)
}

func requireNoErrors(t *testing.T, diags diag.Diagnostics) {
	t.Helper()
	if diags.HasError() {
		t.Fatalf("unexpected error diagnostics: %v", diags)
	}
}

func TestProviderValidateConfigShapes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		values      map[string]any
		wantError   string
		wantWarning string
	}{
		"valid basic auth": {
			values: map[string]any{
				"site_url":   "https://example.atlassian.net",
				"basic_auth": map[string]any{"email": "user@example.com", "api_token": "tok"},
			},
		},
		"valid service account": {
			values: map[string]any{
				"service_account": map[string]any{"client_id": "id", "client_secret": "secret", "cloud_id": "a7c408f1-ec5f-410e-8f27-62dbefebe6b6"},
			},
		},
		"bare host is accepted": {
			values: map[string]any{"site_url": "example.atlassian.net"},
		},
		"site_url with path": {
			values:    map[string]any{"site_url": "https://example.atlassian.net/wiki"},
			wantError: "must not include a path",
		},
		"site_url over http warns": {
			values:      map[string]any{"site_url": "http://localhost:8080"},
			wantWarning: "plain HTTP",
		},
		"blank site_url": {
			values:    map[string]any{"site_url": "   "},
			wantError: "must not be empty",
		},
		"blank api_token": {
			values:    map[string]any{"basic_auth": map[string]any{"email": "user@example.com", "api_token": " "}},
			wantError: "basic_auth.api_token must not be empty",
		},
		"email without domain": {
			values:    map[string]any{"basic_auth": map[string]any{"email": "5b31ef1e7b8c14625a47e880", "api_token": "tok"}},
			wantError: "Invalid email",
		},
		"email without dot in domain": {
			values:    map[string]any{"basic_auth": map[string]any{"email": "user@localhost", "api_token": "tok"}},
			wantError: "Invalid email",
		},
		"cloud_id that is not a uuid": {
			values:    map[string]any{"service_account": map[string]any{"client_id": "id", "client_secret": "s", "cloud_id": "example"}},
			wantError: "Invalid cloud_id",
		},
		"cloud_id in upper case": {
			values:    map[string]any{"service_account": map[string]any{"client_id": "id", "client_secret": "s", "cloud_id": "A7C408F1-EC5F-410E-8F27-62DBEFEBE6B6"}},
			wantError: "Invalid cloud_id",
		},
		"unknown values are skipped": {
			values: map[string]any{
				"site_url":        unknown{},
				"basic_auth":      unknown{},
				"service_account": map[string]any{"client_id": unknown{}, "client_secret": "s", "cloud_id": unknown{}},
			},
		},
		// Presence rules belong to Configure, where the environment is visible.
		"half a block is not a validation error": {
			values: map[string]any{"basic_auth": map[string]any{"email": "user@example.com"}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diags := validateConfig(t, tt.values)
			if tt.wantError != "" {
				requireDiagnostic(t, diags, diag.SeverityError, tt.wantError)
				return
			}
			requireNoErrors(t, diags)
			if tt.wantWarning != "" {
				requireDiagnostic(t, diags, diag.SeverityWarning, tt.wantWarning)
			}
		})
	}
}

func configure(t *testing.T, values map[string]any) (*client.Client, diag.Diagnostics) {
	t.Helper()
	var resp provider.ConfigureResponse
	New("test")().Configure(context.Background(), provider.ConfigureRequest{Config: providerConfig(t, values)}, &resp)
	if resp.DataSourceData == nil {
		return nil, resp.Diagnostics
	}
	c, ok := resp.DataSourceData.(*client.Client)
	if !ok {
		t.Fatalf("DataSourceData is %T, want *client.Client", resp.DataSourceData)
	}
	return c, resp.Diagnostics
}

// clearAtlassianEnv guarantees the shell running the tests does not leak a
// credential into a case. It must not be combined with t.Parallel.
func clearAtlassianEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{envAdminAPIKey, envSiteURL, envEmail, envAPIToken, envClientID, envClientSecret, envCloudID} {
		t.Setenv(name, "")
	}
}

func TestProviderConfigureCombinations(t *testing.T) {
	const site = "https://example.atlassian.net"

	tests := map[string]struct {
		values         map[string]any
		env            map[string]string
		wantAdmin      bool
		wantConfluence bool
		wantNoData     bool
		wantError      []string
	}{
		"admin only": {
			values:    map[string]any{"admin_api_key": "key"},
			wantAdmin: true,
		},
		"admin from environment": {
			env:       map[string]string{envAdminAPIKey: "key"},
			wantAdmin: true,
		},
		"basic auth from configuration": {
			values:         map[string]any{"site_url": site, "basic_auth": map[string]any{"email": "u@example.com", "api_token": "t"}},
			wantConfluence: true,
		},
		"basic auth entirely from environment, no block written": {
			env:            map[string]string{envSiteURL: site, envEmail: "u@example.com", envAPIToken: "t"},
			wantConfluence: true,
		},
		"basic auth mixing configuration and environment": {
			values:         map[string]any{"basic_auth": map[string]any{"email": "u@example.com"}},
			env:            map[string]string{envSiteURL: site, envAPIToken: "t"},
			wantConfluence: true,
		},
		"service account with cloud id, no site": {
			values:         map[string]any{"service_account": map[string]any{"client_id": "i", "client_secret": "s", "cloud_id": "a7c408f1-ec5f-410e-8f27-62dbefebe6b6"}},
			wantConfluence: true,
		},
		"service account with site for discovery": {
			values:         map[string]any{"site_url": site, "service_account": map[string]any{"client_id": "i", "client_secret": "s"}},
			wantConfluence: true,
		},
		"both families": {
			values:         map[string]any{"admin_api_key": "key", "site_url": site, "basic_auth": map[string]any{"email": "u@example.com", "api_token": "t"}},
			wantAdmin:      true,
			wantConfluence: true,
		},
		"nothing configured is not an error": {},
		"both credential types, with sources named": {
			values:    map[string]any{"site_url": site, "service_account": map[string]any{"client_id": "i", "client_secret": "s"}},
			env:       map[string]string{envEmail: "u@example.com", envAPIToken: "t"},
			wantError: []string{"Conflicting Confluence credentials", "environment variable " + envEmail, "service_account.client_id from configuration"},
		},
		"email without token names the missing attribute and its variable": {
			values:    map[string]any{"site_url": site, "basic_auth": map[string]any{"email": "u@example.com"}},
			wantError: []string{"basic_auth.api_token is not", envAPIToken},
		},
		"secret without client id": {
			env:       map[string]string{envClientSecret: "s", envCloudID: "a7c408f1-ec5f-410e-8f27-62dbefebe6b6"},
			wantError: []string{"service_account.client_id is not", envClientID},
		},
		"basic auth without site": {
			values:    map[string]any{"basic_auth": map[string]any{"email": "u@example.com", "api_token": "t"}},
			wantError: []string{"Missing site_url"},
		},
		"service account without site or cloud id": {
			values:    map[string]any{"service_account": map[string]any{"client_id": "i", "client_secret": "s"}},
			wantError: []string{"Missing site identity"},
		},
		"stray cloud id alone does not select service account": {
			values:         map[string]any{"site_url": site, "basic_auth": map[string]any{"email": "u@example.com", "api_token": "t"}},
			env:            map[string]string{envCloudID: "a7c408f1-ec5f-410e-8f27-62dbefebe6b6"},
			wantConfluence: true,
		},
		"invalid site from environment is still rejected": {
			env:       map[string]string{envSiteURL: "https://example.atlassian.net/wiki", envEmail: "u@example.com", envAPIToken: "t"},
			wantError: []string{"must not include a path"},
		},
		"unknown top level value leaves provider data unset": {
			values:     map[string]any{"admin_api_key": unknown{}},
			wantNoData: true,
		},
		"unknown nested value leaves provider data unset": {
			values:     map[string]any{"site_url": site, "basic_auth": map[string]any{"email": "u@example.com", "api_token": unknown{}}},
			wantNoData: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearAtlassianEnv(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			c, diags := configure(t, tt.values)
			if len(tt.wantError) > 0 {
				for _, want := range tt.wantError {
					requireDiagnostic(t, diags, diag.SeverityError, want)
				}
				if c != nil {
					t.Fatal("provider data was set despite an error")
				}
				return
			}
			requireNoErrors(t, diags)
			if tt.wantNoData {
				if c != nil {
					t.Fatal("provider data was set for an unknown configuration")
				}
				return
			}
			if c == nil {
				t.Fatal("provider data was not set")
			}
			if got := c.Organization != nil; got != tt.wantAdmin {
				t.Errorf("Admin family configured = %v, want %v", got, tt.wantAdmin)
			}
			if got := c.Confluence != nil; got != tt.wantConfluence {
				t.Errorf("Confluence family configured = %v, want %v", got, tt.wantConfluence)
			}
		})
	}
}

// TestAdminTypesReportMissingFamily makes the existing per-type nil guards
// live: with a Confluence-only configuration every Admin data source and
// resource must report the missing family instead of dereferencing nil.
func TestAdminTypesReportMissingFamily(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	confluenceOnly := &client.Client{}
	p := New("test")()

	for _, constructor := range p.DataSources(ctx) {
		ds := constructor()
		var meta datasource.MetadataResponse
		ds.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "atlassian"}, &meta)
		configurable, ok := ds.(datasource.DataSourceWithConfigure)
		if !ok {
			continue
		}
		var resp datasource.ConfigureResponse
		configurable.Configure(ctx, datasource.ConfigureRequest{ProviderData: confluenceOnly}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Errorf("%s configured without the Admin family and reported nothing", meta.TypeName)
		}
	}
	for _, constructor := range p.Resources(ctx) {
		r := constructor()
		var meta resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "atlassian"}, &meta)
		configurable, ok := r.(resource.ResourceWithConfigure)
		if !ok {
			continue
		}
		var resp resource.ConfigureResponse
		configurable.Configure(ctx, resource.ConfigureRequest{ProviderData: confluenceOnly}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Errorf("%s configured without the Admin family and reported nothing", meta.TypeName)
		}
	}
}

func TestProviderRegistersOrganizationTypes(t *testing.T) {
	t.Parallel()

	p := New("test")()
	if got := len(p.DataSources(context.Background())); got != 9 {
		t.Fatalf("DataSources() length = %d, want 9", got)
	}
	if got := len(p.Resources(context.Background())); got != 8 {
		t.Fatalf("Resources() length = %d, want 8", got)
	}
	resourceNames := map[string]bool{}
	for _, constructor := range p.Resources(context.Background()) {
		var response resource.MetadataResponse
		constructor().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "atlassian"}, &response)
		resourceNames[response.TypeName] = true
	}
	if !resourceNames["atlassian_organization_user_organization_role_assignment"] {
		t.Error("organization-level user role assignment resource is not registered")
	}
	if !resourceNames["atlassian_organization_group"] {
		t.Error("organization group resource is not registered")
	}
	if !resourceNames["atlassian_organization_group_role_assignment"] {
		t.Error("organization group role assignment resource is not registered")
	}
	if !resourceNames["atlassian_organization_policy"] {
		t.Error("organization policy resource is not registered")
	}
	if !resourceNames["atlassian_data_security_policy"] {
		t.Error("data security policy resource is not registered")
	}
	if !resourceNames["atlassian_confluence_space"] {
		t.Error("confluence space resource is not registered")
	}
	dataSourceNames := map[string]bool{}
	for _, constructor := range p.DataSources(context.Background()) {
		var response datasource.MetadataResponse
		constructor().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "atlassian"}, &response)
		dataSourceNames[response.TypeName] = true
	}
	for _, name := range []string{"atlassian_organization_policy", "atlassian_organization_policies", "atlassian_organization_group", "atlassian_organization_groups", "atlassian_organization_user", "atlassian_organization_users", "atlassian_organization_workspaces", "atlassian_confluence_space", "atlassian_confluence_spaces"} {
		if !dataSourceNames[name] {
			t.Errorf("data source %q is not registered", name)
		}
	}
}

// TestProviderValidateConfigReportsEveryProblem guards against validation
// stopping at the first error: a configuration with an unrelated mistake in
// site_url and in each nested block must report all of them in one pass, so a
// user fixes them together rather than one replan at a time.
func TestProviderValidateConfigReportsEveryProblem(t *testing.T) {
	t.Parallel()

	diags := validateConfig(t, map[string]any{
		"site_url":        "https://example.atlassian.net/wiki",
		"basic_auth":      map[string]any{"email": "not-an-email", "api_token": "tok"},
		"service_account": map[string]any{"client_id": "id", "client_secret": "s", "cloud_id": "not-a-uuid"},
	})
	requireDiagnostic(t, diags, diag.SeverityError, "must not include a path")
	requireDiagnostic(t, diags, diag.SeverityError, "Invalid email")
	requireDiagnostic(t, diags, diag.SeverityError, "Invalid cloud_id")
}
