package organization

import (
	"context"
	"fmt"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	organizationclient "github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &groupDataSource{}
var _ datasource.DataSourceWithValidateConfig = &groupDataSource{}

type groupDataSource struct {
	client *organizationclient.Service
}

type groupDataSourceModel struct {
	ID               types.String `tfsdk:"id"`
	OrganizationID   types.String `tfsdk:"organization_id"`
	DirectoryID      types.String `tfsdk:"directory_id"`
	GroupID          types.String `tfsdk:"group_id"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	ExternalSynced   types.Bool   `tfsdk:"external_synced"`
	ManagedBy        types.String `tfsdk:"managed_by"`
	ManagementAccess types.Object `tfsdk:"management_access"`
}

func NewGroupDataSource() datasource.DataSource {
	return &groupDataSource{}
}

func (d *groupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_group"
}

func (d *groupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Description: description, Computed: true}
	}
	computedBool := func(description string) schema.BoolAttribute {
		return schema.BoolAttribute{Description: description, Computed: true}
	}
	resp.Schema = schema.Schema{
		Description: "Retrieves one Atlassian organization group by its group ID.",
		Attributes: map[string]schema.Attribute{
			"id":              computedString("Data source ID, equal to the Atlassian group ID."),
			"organization_id": schema.StringAttribute{Description: "Atlassian organization ID used in the Organization API path.", Required: true},
			"directory_id":    schema.StringAttribute{Description: "Directory containing the group.", Required: true},
			"group_id":        schema.StringAttribute{Description: "Unique Atlassian group ID.", Required: true},
			"name":            computedString("Group name."),
			"description":     computedString("Group description."),
			"external_synced": computedBool("Whether the group is synchronized from an identity provider."),
			"managed_by":      computedString("How the group is managed."),
			"management_access": schema.SingleNestedAttribute{
				Description: "Operations available to the caller for this group.",
				Computed:    true,
				Attributes: map[string]schema.Attribute{
					"deletable":  computedBool("Whether the group can be deleted."),
					"modifiable": computedBool("Whether the group can be modified."),
					"readable":   computedBool("Whether the group can be read."),
				},
			},
		},
	}
}

func (d *groupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *groupDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var config groupDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateGroupValues(config.OrganizationID, config.DirectoryID, config.GroupID, types.StringNull())...)
}

func (d *groupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config groupDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	group, err := d.client.GetGroup(ctx, config.OrganizationID.ValueString(), config.DirectoryID.ValueString(), config.GroupID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read organization group", err.Error())
		return
	}
	resourceState := groupResourceModel{
		OrganizationID: config.OrganizationID,
		DirectoryID:    config.DirectoryID,
		GroupID:        config.GroupID,
	}
	setGroupState(ctx, &resourceState, group, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	config.ID = resourceState.ID
	config.GroupID = resourceState.GroupID
	config.Name = resourceState.Name
	config.Description = resourceState.Description
	config.ExternalSynced = resourceState.ExternalSynced
	config.ManagedBy = resourceState.ManagedBy
	config.ManagementAccess = resourceState.ManagementAccess
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
