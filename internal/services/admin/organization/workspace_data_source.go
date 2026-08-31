package organization

import (
	"context"
	"fmt"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	organizationclient "github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &workspacesDataSource{}
var _ datasource.DataSourceWithValidateConfig = &workspacesDataSource{}

type workspacesDataSource struct {
	client *organizationclient.Service
}

type workspacesDataSourceModel struct {
	OrganizationID types.String `tfsdk:"organization_id"`
	Search         types.String `tfsdk:"search"`
	Workspaces     types.Set    `tfsdk:"workspaces"`
}

type workspaceResultModel struct {
	ID      types.String `tfsdk:"id"`
	TypeKey types.String `tfsdk:"type_key"`
	Name    types.String `tfsdk:"name"`
	Type    types.String `tfsdk:"type"`
	Status  types.String `tfsdk:"status"`
}

func NewWorkspacesDataSource() datasource.DataSource {
	return &workspacesDataSource{}
}

func (d *workspacesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_workspaces"
}

func (d *workspacesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Description: description, Computed: true}
	}
	resp.Schema = schema.Schema{
		Description: "Queries all pages of an Atlassian organization and returns every matching workspace. A workspace is a single app instance, and its ID is the resource ARI that role assignments refer to.",
		Attributes: map[string]schema.Attribute{
			"organization_id": schema.StringAttribute{Description: "Atlassian organization ID used in the Organization API path.", Required: true},
			"search":          schema.StringAttribute{Description: "Free-text search matching part of a workspace name or URL.", Optional: true},
			"workspaces": schema.SetNestedAttribute{
				Description: "All workspaces matching the configured filters. The set is empty when no workspaces match.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":       computedString("Resource ARI of the workspace, such as ari:cloud:confluence::site/<site-id>."),
					"type_key": computedString("Machine-readable app key, such as confluence or jira-software."),
					"name":     computedString("Workspace name."),
					"type":     computedString("Display name of the app. Use type_key for comparisons."),
					"status":   computedString("Workspace status: online, offline, or deprecated."),
				}},
			},
		},
	}
}

func (d *workspacesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	atlassianClient, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T", req.ProviderData))
		return
	}
	if atlassianClient.Organization == nil {
		resp.Diagnostics.AddError("Admin API is not configured", "Set admin_api_key or ATLASSIAN_ADMIN_API_KEY to use organization data sources.")
		return
	}
	d.client = atlassianClient.Organization
}

func (d *workspacesDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var config workspacesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	const summary = "Invalid organization workspace configuration"
	resp.Diagnostics.Append(validateNonEmpty(summary,
		namedValue{"organization_id", config.OrganizationID},
		namedValue{"search", config.Search},
	)...)
}

func (d *workspacesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config workspacesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaces, err := d.client.QueryWorkspaces(ctx, config.OrganizationID.ValueString(), config.Search.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to query organization workspaces", err.Error())
		return
	}
	results := make([]workspaceResultModel, 0, len(workspaces))
	for _, workspace := range workspaces {
		results = append(results, workspaceResultModel{
			ID:      types.StringValue(workspace.ID),
			TypeKey: nullableStringValue(workspace.TypeKey),
			Name:    nullableStringValue(workspace.Name),
			Type:    nullableStringValue(workspace.Type),
			Status:  nullableStringValue(workspace.Status),
		})
	}
	workspaceSet, diagnostics := types.SetValueFrom(ctx, types.ObjectType{AttrTypes: workspaceResultAttributeTypes()}, results)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	config.Workspaces = workspaceSet
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func workspaceResultAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"id":       types.StringType,
		"type_key": types.StringType,
		"name":     types.StringType,
		"type":     types.StringType,
		"status":   types.StringType,
	}
}
