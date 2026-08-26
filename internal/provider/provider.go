package provider

import (
	"context"
	"fmt"
	"os"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-name atlassian --provider-dir ../..

var _ provider.Provider = &AtlassianProvider{}

type AtlassianProvider struct {
	version string
}

type providerModel struct {
	SiteURL  types.String `tfsdk:"site_url"`
	Email    types.String `tfsdk:"email"`
	APIToken types.String `tfsdk:"api_token"`
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
			"site_url": schema.StringAttribute{
				Description: "Atlassian Cloud site URL. May also be set with ATLASSIAN_SITE_URL.",
				Optional:    true,
			},
			"email": schema.StringAttribute{
				Description: "Email address used for Atlassian API authentication. May also be set with ATLASSIAN_EMAIL.",
				Optional:    true,
			},
			"api_token": schema.StringAttribute{
				Description: "Atlassian API token. May also be set with ATLASSIAN_API_TOKEN.",
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

	if config.SiteURL.IsUnknown() || config.Email.IsUnknown() || config.APIToken.IsUnknown() {
		return
	}

	siteURL := configuredValue(config.SiteURL, "ATLASSIAN_SITE_URL")
	email := configuredValue(config.Email, "ATLASSIAN_EMAIL")
	apiToken := configuredValue(config.APIToken, "ATLASSIAN_API_TOKEN")

	atlassianClient, err := client.New(siteURL, email, apiToken, nil)
	if err != nil {
		resp.Diagnostics.AddError("Unable to configure Atlassian client", fmt.Sprintf("Invalid provider configuration: %s", err))
		return
	}

	resp.DataSourceData = atlassianClient
	resp.ResourceData = atlassianClient
}

func (p *AtlassianProvider) Resources(_ context.Context) []func() resource.Resource {
	return nil
}

func (p *AtlassianProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}

func configuredValue(value types.String, environmentVariable string) string {
	if !value.IsNull() {
		return value.ValueString()
	}
	return os.Getenv(environmentVariable)
}
