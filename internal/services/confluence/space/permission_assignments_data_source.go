package space

import (
	"context"
	"fmt"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &permissionAssignmentsDataSource{}

type permissionAssignmentsDataSource struct{ client *Service }

type permissionAssignmentsDataSourceModel struct {
	SpaceID     types.String `tfsdk:"space_id"`
	Assignments types.Set    `tfsdk:"assignments"`
}

type permissionAssignmentModel struct {
	ID        types.String `tfsdk:"id"`
	Principal types.Object `tfsdk:"principal"`
	Operation types.Object `tfsdk:"operation"`
}

type accessPrincipalModel struct {
	Type types.String `tfsdk:"type"`
	ID   types.String `tfsdk:"id"`
}

type permissionOperationModel struct {
	Key        types.String `tfsdk:"key"`
	TargetType types.String `tfsdk:"target_type"`
}

// NewPermissionAssignmentsDataSource returns the complete legacy permission
// assignment read for one Confluence space.
func NewPermissionAssignmentsDataSource() datasource.DataSource {
	return &permissionAssignmentsDataSource{}
}

func (d *permissionAssignmentsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_confluence_space_permission_assignments"
}

func (d *permissionAssignmentsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Description: description, Computed: true}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads every legacy permission assignment for one Confluence space. The result is an unordered set and includes Custom access, role-expanded permissions, and access-class principals returned by the API.\n\n## Required OAuth scopes\n\n- `read:space:confluence`\n",
		Attributes: map[string]schema.Attribute{
			"space_id": schema.StringAttribute{
				Description: "Numeric-string ID of the space.", Required: true,
				// The numeric pattern already excludes a blank value.
				Validators: []validator.String{stringvalidator.RegexMatches(numericIDPattern, "must be a numeric string")},
			},
			"assignments": schema.SetNestedAttribute{
				Description: "Complete permission assignments returned for the space.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id": computedString("ID of the permission assignment."),
					"principal": schema.SingleNestedAttribute{Computed: true, Attributes: map[string]schema.Attribute{
						"type": computedString("Principal type returned by the API."),
						"id":   computedString("Principal ID."),
					}},
					"operation": schema.SingleNestedAttribute{Computed: true, Attributes: map[string]schema.Attribute{
						"key":         computedString("API operation key."),
						"target_type": computedString("API operation target type."),
					}},
				}},
			},
		},
	}
}

func (d *permissionAssignmentsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *permissionAssignmentsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config permissionAssignmentsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	assignments, err := d.client.GetSpacePermissionsAssignments(ctx, config.SpaceID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Confluence space permission assignments", err.Error())
		return
	}
	rows := make([]permissionAssignmentModel, 0, len(assignments))
	for _, assignment := range assignments {
		principal, diagnostics := types.ObjectValueFrom(ctx, accessPrincipalAttributeTypes(), accessPrincipalModel{
			Type: types.StringValue(assignment.Principal.Type), ID: types.StringValue(assignment.Principal.ID),
		})
		resp.Diagnostics.Append(diagnostics...)
		operation, diagnostics := types.ObjectValueFrom(ctx, permissionOperationAttributeTypes(), permissionOperationModel{
			Key: types.StringValue(assignment.Operation.Key), TargetType: types.StringValue(assignment.Operation.TargetType),
		})
		resp.Diagnostics.Append(diagnostics...)
		rows = append(rows, permissionAssignmentModel{ID: types.StringValue(assignment.ID), Principal: principal, Operation: operation})
	}
	if resp.Diagnostics.HasError() {
		return
	}
	set, diagnostics := types.SetValueFrom(ctx, types.ObjectType{AttrTypes: permissionAssignmentAttributeTypes()}, rows)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	config.Assignments = set
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func accessPrincipalAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{"type": types.StringType, "id": types.StringType}
}

func permissionOperationAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{"key": types.StringType, "target_type": types.StringType}
}

func permissionAssignmentAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"id":        types.StringType,
		"principal": types.ObjectType{AttrTypes: accessPrincipalAttributeTypes()},
		"operation": types.ObjectType{AttrTypes: permissionOperationAttributeTypes()},
	}
}
