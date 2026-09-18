package space

import (
	"context"
	"fmt"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &roleDataSource{}
var _ datasource.DataSourceWithValidateConfig = &roleDataSource{}

type roleDataSource struct{ client *Service }

type roleDataSourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	Type             types.String `tfsdk:"type"`
	SpacePermissions types.Set    `tfsdk:"space_permissions"`
}

// NewRoleDataSource returns one tenant-wide Confluence space role by ID.
func NewRoleDataSource() datasource.DataSource { return &roleDataSource{} }

func (d *roleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_confluence_space_role"
}

func (d *roleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Description: description, Computed: true}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one tenant-wide Confluence space role by ID.\n\n## Required OAuth scopes\n\n- `read:space.permission:confluence`\n",
		Attributes: map[string]schema.Attribute{
			"id":                schema.StringAttribute{Description: "Tenant-specific space role ID.", Required: true},
			"name":              computedString("Name of the space role."),
			"description":       computedString("Description of the space role."),
			"type":              computedString("Role type returned by Confluence."),
			"space_permissions": schema.SetAttribute{Description: "Space permission IDs included in the role.", Computed: true, ElementType: types.StringType},
		},
	}
}

func (d *roleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	atlassianClient, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T", req.ProviderData))
		return
	}
	if atlassianClient.Confluence == nil {
		resp.Diagnostics.AddError("Confluence is not configured", "Set site_url plus basic_auth or service_account to use Confluence data sources.")
		return
	}
	d.client = NewService(atlassianClient.Confluence)
}

func (d *roleDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var config roleDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateNonEmpty("Invalid Confluence space role lookup", namedValue{"id", config.ID})...)
}

func (d *roleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config roleDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	role, err := d.client.GetSpaceRoleByID(ctx, config.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Confluence space role", err.Error())
		return
	}
	permissionIDs, diagnostics := types.SetValueFrom(ctx, types.StringType, role.PermissionIDs)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	state := roleDataSourceModel{
		ID: types.StringValue(role.ID), Name: types.StringValue(role.Name), Description: types.StringValue(role.Description),
		Type: types.StringValue(role.Type), SpacePermissions: permissionIDs,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
