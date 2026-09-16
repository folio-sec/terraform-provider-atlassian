package space

import (
	"context"
	"fmt"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &rolesDataSource{}

type rolesDataSource struct{ client *Service }

type rolesDataSourceModel struct {
	Roles types.Set `tfsdk:"roles"`
}

type roleModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	Type          types.String `tfsdk:"type"`
	PermissionIDs types.Set    `tfsdk:"permission_ids"`
}

// NewRolesDataSource returns the tenant's Confluence space-role catalogue.
func NewRolesDataSource() datasource.DataSource { return &rolesDataSource{} }

func (d *rolesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_confluence_space_roles"
}

func (d *rolesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Description: description, Computed: true}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads the tenant-specific Confluence space-role catalogue. Use role IDs in resource configuration; names are localized. Catalogue membership does not guarantee that a role is assignable to every space.\n\n## Required OAuth scopes\n\n- `read:space.permission:confluence`\n",
		Attributes: map[string]schema.Attribute{
			"roles": schema.SetNestedAttribute{
				Description: "Every available space role in the tenant.", Computed: true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":             computedString("Tenant-specific role ID."),
					"name":           computedString("Localized role name."),
					"description":    computedString("Localized role description."),
					"type":           computedString("Role type returned by the API."),
					"permission_ids": schema.SetAttribute{Description: "Permission IDs included in the role.", Computed: true, ElementType: types.StringType},
				}},
			},
		},
	}
}

func (d *rolesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *rolesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	roles, err := d.client.GetAvailableSpaceRoles(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Confluence space roles", err.Error())
		return
	}
	rows := make([]roleModel, 0, len(roles))
	for _, role := range roles {
		permissions, diagnostics := types.SetValueFrom(ctx, types.StringType, role.PermissionIDs)
		resp.Diagnostics.Append(diagnostics...)
		rows = append(rows, roleModel{
			ID: types.StringValue(role.ID), Name: types.StringValue(role.Name), Description: types.StringValue(role.Description),
			Type: types.StringValue(role.Type), PermissionIDs: permissions,
		})
	}
	if resp.Diagnostics.HasError() {
		return
	}
	set, diagnostics := types.SetValueFrom(ctx, types.ObjectType{AttrTypes: roleAttributeTypes()}, rows)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &rolesDataSourceModel{Roles: set})...)
}

func roleAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"id": types.StringType, "name": types.StringType, "description": types.StringType,
		"type": types.StringType, "permission_ids": types.SetType{ElemType: types.StringType},
	}
}
