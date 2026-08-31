package organization

import (
	"context"
	"fmt"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	organizationclient "github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &workspacesDataSource{}
var _ datasource.DataSourceWithValidateConfig = &workspacesDataSource{}

type workspacesDataSource struct {
	client *organizationclient.Service
}

type workspacesDataSourceModel struct {
	OrganizationID types.String         `tfsdk:"organization_id"`
	Query          *workspaceQueryModel `tfsdk:"query"`
	Workspaces     types.Set            `tfsdk:"workspaces"`
}

// workspaceQueryModel mirrors the operands of the endpoint's query union. Every
// operand narrows which workspaces match; when more than one is configured the
// provider combines them with the union's and operator.
type workspaceQueryModel struct {
	Search   types.String `tfsdk:"search"`
	Fields   types.List   `tfsdk:"fields"`
	Features types.Set    `tfsdk:"features"`
	Policies types.Set    `tfsdk:"policies"`
}

type workspaceQueryFieldModel struct {
	Name   types.String `tfsdk:"name"`
	Values types.Set    `tfsdk:"values"`
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
			"query": schema.SingleNestedAttribute{
				Description: "Filters narrowing which workspaces match. Configuring more than one filter returns only workspaces matching all of them.",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"search": schema.StringAttribute{
						Description: "Free-text search matching part of a workspace name or URL.",
						Optional:    true,
					},
					"features": schema.SetAttribute{
						Description: "Feature keys the workspace must contain.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"policies": schema.SetAttribute{
						Description: "Policy IDs the workspace must contain.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"fields": schema.ListNestedAttribute{
						Description: "Field filters. Each entry matches workspaces whose named field holds one of the listed values.",
						Optional:    true,
						NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
							"name": schema.StringAttribute{
								Description: "Field name to match, such as attributes.type.",
								Required:    true,
							},
							"values": schema.SetAttribute{
								Description: "Values the field may hold. A workspace matches when the field holds any of them.",
								Required:    true,
								ElementType: types.StringType,
							},
						}},
					},
				},
			},
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
	resp.Diagnostics.Append(validateNonEmpty(summary, namedValue{"organization_id", config.OrganizationID})...)
	if config.Query == nil {
		return
	}
	resp.Diagnostics.Append(validateNonEmpty(summary, namedValue{"query.search", config.Query.Search})...)

	fields, diagnostics := workspaceQueryFields(ctx, config.Query)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	for index, field := range fields {
		// An empty values list makes the field operand a no-op server side,
		// which silently widens the result set instead of narrowing it.
		if len(field.Values) == 0 {
			resp.Diagnostics.AddError(summary, fmt.Sprintf("query.fields[%d].values must not be empty.", index))
		}
		if strings.TrimSpace(field.Name) == "" {
			resp.Diagnostics.AddError(summary, fmt.Sprintf("query.fields[%d].name must not be empty.", index))
		}
	}
}

// workspaceQueryFields converts the configured field operands, treating a null
// or unknown list as no operands at all.
func workspaceQueryFields(ctx context.Context, query *workspaceQueryModel) ([]organizationclient.WorkspaceField, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	if query == nil || query.Fields.IsNull() || query.Fields.IsUnknown() {
		return nil, diagnostics
	}
	var models []workspaceQueryFieldModel
	diagnostics.Append(query.Fields.ElementsAs(ctx, &models, false)...)
	if diagnostics.HasError() {
		return nil, diagnostics
	}
	fields := make([]organizationclient.WorkspaceField, 0, len(models))
	for _, model := range models {
		var values []string
		if !model.Values.IsNull() && !model.Values.IsUnknown() {
			diagnostics.Append(model.Values.ElementsAs(ctx, &values, false)...)
		}
		fields = append(fields, organizationclient.WorkspaceField{Name: model.Name.ValueString(), Values: values})
	}
	return fields, diagnostics
}

func (d *workspacesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config workspacesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	filters, diagnostics := workspaceQueryRequest(ctx, config.Query)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaces, err := d.client.QueryWorkspaces(ctx, config.OrganizationID.ValueString(), filters)
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

// workspaceQueryRequest converts the configured query into service filters. A
// null query means no operands, which returns every workspace in the
// organization.
func workspaceQueryRequest(ctx context.Context, query *workspaceQueryModel) (organizationclient.QueryWorkspacesRequest, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	if query == nil {
		return organizationclient.QueryWorkspacesRequest{}, diagnostics
	}
	fields, fieldDiagnostics := workspaceQueryFields(ctx, query)
	diagnostics.Append(fieldDiagnostics...)

	request := organizationclient.QueryWorkspacesRequest{
		Search: query.Search.ValueString(),
		Fields: fields,
	}
	for _, item := range []struct {
		set    types.Set
		target *[]string
	}{
		{query.Features, &request.Features},
		{query.Policies, &request.Policies},
	} {
		if item.set.IsNull() || item.set.IsUnknown() {
			continue
		}
		diagnostics.Append(item.set.ElementsAs(ctx, item.target, false)...)
	}
	return request, diagnostics
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
