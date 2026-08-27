package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
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

	if got := len(resp.Schema.Attributes); got != 1 {
		t.Fatalf("schema attribute count = %d, want 1", got)
	}
	if _, ok := resp.Schema.Attributes["admin_api_key"]; !ok {
		t.Error("schema attribute admin_api_key is missing")
	}
}

func TestProviderRegistersOrganizationTypes(t *testing.T) {
	t.Parallel()

	p := New("test")()
	if got := len(p.DataSources(context.Background())); got != 3 {
		t.Fatalf("DataSources() length = %d, want 3", got)
	}
	if got := len(p.Resources(context.Background())); got != 4 {
		t.Fatalf("Resources() length = %d, want 4", got)
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
	dataSourceNames := map[string]bool{}
	for _, constructor := range p.DataSources(context.Background()) {
		var response datasource.MetadataResponse
		constructor().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "atlassian"}, &response)
		dataSourceNames[response.TypeName] = true
	}
	for _, name := range []string{"atlassian_organization_group", "atlassian_organization_groups", "atlassian_organization_users"} {
		if !dataSourceNames[name] {
			t.Errorf("data source %q is not registered", name)
		}
	}
}
