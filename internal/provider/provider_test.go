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

	for _, name := range []string{"site_url", "email", "api_token"} {
		if _, ok := resp.Schema.Attributes[name]; !ok {
			t.Errorf("schema attribute %q is missing", name)
		}
	}
}
