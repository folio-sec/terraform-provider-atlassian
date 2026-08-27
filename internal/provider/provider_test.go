package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
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
	if got := len(p.DataSources(context.Background())); got != 1 {
		t.Fatalf("DataSources() length = %d, want 1", got)
	}
	if got := len(p.Resources(context.Background())); got != 2 {
		t.Fatalf("Resources() length = %d, want 2", got)
	}
}
