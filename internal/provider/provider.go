package provider

import (
	"context"
	"fmt"
	"os"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	organizationservice "github.com/folio-sec/terraform-provider-atlassian/internal/services/admin/organization"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-name atlassian --provider-dir ../..
//go:generate go run ../cmd/postprocess-docs -docs-dir ../../docs

var _ provider.Provider = &AtlassianProvider{}

type AtlassianProvider struct {
	version string
}

type providerModel struct {
	AdminAPIKey types.String `tfsdk:"admin_api_key"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &AtlassianProvider{version: version}
	}
}

func (p *AtlassianProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "atlassian"
	resp.Version = p.version
}

func (p *AtlassianProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage Atlassian Cloud resources.",
		Attributes: map[string]schema.Attribute{
			"admin_api_key": schema.StringAttribute{
				Description: "Atlassian organization API key used for Cloud Admin APIs. May also be set with ATLASSIAN_ADMIN_API_KEY.",
				Optional:    true,
				Sensitive:   true,
			},
		},
	}
}
func (p *AtlassianProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if config.AdminAPIKey.IsUnknown() {
		return
	}

	adminAPIKey := configuredValue(config.AdminAPIKey, "ATLASSIAN_ADMIN_API_KEY")

	atlassianClient, err := client.New(client.Config{
		AdminAPIKey: adminAPIKey,
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to configure Atlassian client", fmt.Sprintf("Invalid provider configuration: %s", err))
		return
	}

	resp.DataSourceData = atlassianClient
	resp.ResourceData = atlassianClient
}

func (p *AtlassianProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		organizationservice.NewUserRoleAssignmentResource,
	}
}

func (p *AtlassianProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		organizationservice.NewUserDataSource,
	}
}

func configuredValue(value types.String, environmentVariable string) string {
	if !value.IsNull() {
		return value.ValueString()
	}
	return os.Getenv(environmentVariable)
}
