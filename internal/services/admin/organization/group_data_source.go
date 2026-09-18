package organization

import (
	"context"
	"fmt"
	"github.com/folio-sec/terraform-provider-atlassian/internal/validation"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	organizationclient "github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &groupsDataSource{}
var _ datasource.DataSourceWithValidateConfig = &groupsDataSource{}

type groupsDataSource struct {
	client *organizationclient.Service
}

type groupsDataSourceModel struct {
	OrganizationID types.String `tfsdk:"organization_id"`
	DirectoryID    types.String `tfsdk:"directory_id"`
	AccountIDs     types.Set    `tfsdk:"account_ids"`
	DirectoryIDs   types.Set    `tfsdk:"directory_ids"`
	RoleIDs        types.Set    `tfsdk:"role_ids"`
	ResourceOwners types.Set    `tfsdk:"resource_owners"`
	ResourceIDs    types.Set    `tfsdk:"resource_ids"`
	SearchTerm     types.String `tfsdk:"search_term"`
	GroupIDs       types.Set    `tfsdk:"group_ids"`
	GroupNames     types.Set    `tfsdk:"group_names"`
	Groups         types.Set    `tfsdk:"groups"`
}

type groupResultModel struct {
	GroupID          types.String `tfsdk:"group_id"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	DirectoryID      types.String `tfsdk:"directory_id"`
	ExternalSynced   types.Bool   `tfsdk:"external_synced"`
	ManagedBy        types.String `tfsdk:"managed_by"`
	ManagementAccess types.Object `tfsdk:"management_access"`
}

type managementAccessResultModel struct {
	Deletable  types.Bool `tfsdk:"deletable"`
	Modifiable types.Bool `tfsdk:"modifiable"`
	Readable   types.Bool `tfsdk:"readable"`
}

func NewGroupsDataSource() datasource.DataSource {
	return &groupsDataSource{}
}

func (d *groupsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_groups"
}

func (d *groupsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	// Each filter carries the count the endpoint accepts.
	filterSet := func(description string, maximum int) schema.SetAttribute {
		return schema.SetAttribute{
			Description: description, Optional: true, ElementType: types.StringType,
			Validators: []validator.Set{setvalidator.SizeBetween(1, maximum), setvalidator.ValueStringsAre(validation.NonBlank)},
		}
	}
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Description: description, Computed: true}
	}
	computedBool := func(description string) schema.BoolAttribute {
		return schema.BoolAttribute{Description: description, Computed: true}
	}

	resp.Schema = schema.Schema{
		Description: "Searches all pages of an Atlassian organization directory and returns every matching group.",
		Attributes: map[string]schema.Attribute{
			"organization_id": schema.StringAttribute{Description: "Atlassian organization ID used in the Organization API path.", Required: true, Validators: groupStringValidators},
			"directory_id":    schema.StringAttribute{Description: "Directory ID used in the Organization API path.", Required: true, Validators: groupStringValidators},
			"account_ids":     filterSet("Account IDs of group members to match. Accepts 1 to 10 values.", 10),
			"directory_ids":   filterSet("Directory IDs to match. Accepts 1 to 10 values.", 10),
			"role_ids":        filterSet("Canonical Atlassian role IDs to match. Accepts 1 to 10 values.", 10),
			"resource_owners": filterSet("Resource type keys to match. Accepts 1 to 10 values.", 10),
			"resource_ids":    filterSet("Resource IDs to match. Accepts 1 to 20 values.", 20),
			"search_term":     schema.StringAttribute{Description: "Free-text group name search. Mutually exclusive with group_names.", Optional: true, Validators: []validator.String{validation.NonBlank}},
			"group_ids":       filterSet("Group IDs to match. Accepts 1 to 10 values and is mutually exclusive with group_names.", 10),
			"group_names":     filterSet("Full group names to match exactly, case-insensitively. Accepts 1 to 100 values and is mutually exclusive with search_term and group_ids.", 100),
			"groups": schema.SetNestedAttribute{
				Description: "All groups matching the configured filters. The set is empty when no groups match.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"group_id":        computedString("Unique group ID."),
					"name":            computedString("Group name."),
					"description":     computedString("Group description."),
					"directory_id":    computedString("Directory containing the group."),
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
				}},
			},
		},
	}
}

func (d *groupsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *groupsDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var config groupsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Each attribute's own rules are schema validators. Only the rules that
	// read two attributes at once stay here.
	if configuredSet(config.GroupNames) {
		if knownString(config.SearchTerm) {
			resp.Diagnostics.AddError("Invalid organization group filters", "group_names and search_term are mutually exclusive.")
		}
		if configuredSet(config.GroupIDs) {
			resp.Diagnostics.AddError("Invalid organization group filters", "group_names and group_ids are mutually exclusive.")
		}
	}
}

func (d *groupsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config groupsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	filters := organizationclient.SearchGroupsRequest{SearchTerm: stringValue(config.SearchTerm)}
	sets := []struct {
		set    types.Set
		target *[]string
	}{
		{config.AccountIDs, &filters.AccountIDs},
		{config.DirectoryIDs, &filters.DirectoryIDs},
		{config.RoleIDs, &filters.RoleIDs},
		{config.ResourceOwners, &filters.ResourceOwners},
		{config.ResourceIDs, &filters.ResourceIDs},
		{config.GroupIDs, &filters.GroupIDs},
		{config.GroupNames, &filters.GroupNames},
	}
	for _, item := range sets {
		if item.set.IsNull() || item.set.IsUnknown() {
			continue
		}
		resp.Diagnostics.Append(item.set.ElementsAs(ctx, item.target, false)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	groups, err := d.client.SearchGroups(ctx, config.OrganizationID.ValueString(), config.DirectoryID.ValueString(), filters)
	if err != nil {
		resp.Diagnostics.AddError("Unable to search organization groups", err.Error())
		return
	}
	results := make([]groupResultModel, 0, len(groups))
	for _, group := range groups {
		managementAccess := types.ObjectNull(managementAccessAttributeTypes())
		if group.ManagementAccess != nil {
			value, diagnostics := types.ObjectValueFrom(ctx, managementAccessAttributeTypes(), managementAccessResultModel{
				Deletable:  nullableBoolValue(group.ManagementAccess.Deletable),
				Modifiable: nullableBoolValue(group.ManagementAccess.Modifiable),
				Readable:   nullableBoolValue(group.ManagementAccess.Readable),
			})
			resp.Diagnostics.Append(diagnostics...)
			managementAccess = value
		}
		results = append(results, groupResultModel{
			GroupID:          types.StringValue(group.ID),
			Name:             nullableStringValue(group.Name),
			Description:      nullableStringValue(group.Description),
			DirectoryID:      nullableStringValue(group.DirectoryID),
			ExternalSynced:   nullableBoolValue(group.ExternalSynced),
			ManagedBy:        nullableStringValue(group.ManagedBy),
			ManagementAccess: managementAccess,
		})
	}
	if resp.Diagnostics.HasError() {
		return
	}
	groupSet, diagnostics := types.SetValueFrom(ctx, types.ObjectType{AttrTypes: groupResultAttributeTypes()}, results)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	config.Groups = groupSet
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func configuredSet(value types.Set) bool {
	return !value.IsNull() && !value.IsUnknown()
}

func managementAccessAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"deletable":  types.BoolType,
		"modifiable": types.BoolType,
		"readable":   types.BoolType,
	}
}

func groupResultAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"group_id":          types.StringType,
		"name":              types.StringType,
		"description":       types.StringType,
		"directory_id":      types.StringType,
		"external_synced":   types.BoolType,
		"managed_by":        types.StringType,
		"management_access": types.ObjectType{AttrTypes: managementAccessAttributeTypes()},
	}
}
