package organization

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	organizationclient "github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &groupMembershipResource{}
var _ resource.ResourceWithIdentity = &groupMembershipResource{}
var _ resource.ResourceWithImportState = &groupMembershipResource{}
var _ resource.ResourceWithValidateConfig = &groupMembershipResource{}

type groupMembershipResource struct {
	client *organizationclient.Service
}

type groupMembershipResourceModel struct {
	ID             types.String `tfsdk:"id"`
	OrganizationID types.String `tfsdk:"organization_id"`
	DirectoryID    types.String `tfsdk:"directory_id"`
	GroupID        types.String `tfsdk:"group_id"`
	AccountID      types.String `tfsdk:"account_id"`
}

type groupMembershipResourceIdentityModel struct {
	OrganizationID types.String `tfsdk:"organization_id"`
	DirectoryID    types.String `tfsdk:"directory_id"`
	GroupID        types.String `tfsdk:"group_id"`
	AccountID      types.String `tfsdk:"account_id"`
}

func NewGroupMembershipResource() resource.Resource {
	return &groupMembershipResource{}
}

func (r *groupMembershipResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_group_membership"
}

func (r *groupMembershipResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	requiredReplace := func(description string) schema.StringAttribute {
		return schema.StringAttribute{
			Description: description,
			Required:    true,
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(),
			},
		}
	}
	resp.Schema = schema.Schema{
		Description: "Manages a user's membership in an Atlassian organization group.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Description: "Composite group membership identifier.", Computed: true},
			"organization_id": requiredReplace("Atlassian organization ID used in the Organization API path."),
			"directory_id":    requiredReplace("Directory ID containing both the user and group."),
			"group_id":        requiredReplace("Atlassian organization group ID."),
			"account_id":      requiredReplace("Atlassian account ID of the group member."),
		},
	}
}

func (r *groupMembershipResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	requiredString := func(description string) identityschema.StringAttribute {
		return identityschema.StringAttribute{Description: description, RequiredForImport: true}
	}
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"organization_id": requiredString("Atlassian organization ID used in the Organization API path."),
			"directory_id":    requiredString("Directory ID containing both the user and group."),
			"group_id":        requiredString("Atlassian organization group ID."),
			"account_id":      requiredString("Atlassian account ID of the group member."),
		},
	}
}

func (r *groupMembershipResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	atlassianClient, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T", req.ProviderData))
		return
	}
	if atlassianClient.Organization == nil {
		resp.Diagnostics.AddError("Admin API is not configured", "Set admin_api_key or ATLASSIAN_ADMIN_API_KEY to use organization resources.")
		return
	}
	r.client = atlassianClient.Organization
}

func (r *groupMembershipResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config groupMembershipResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateGroupMembership(config.OrganizationID, config.DirectoryID, config.GroupID, config.AccountID)...)
}

func validateGroupMembership(values ...types.String) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	names := []string{"organization_id", "directory_id", "group_id", "account_id"}
	for index, value := range values {
		if !value.IsNull() && !value.IsUnknown() && strings.TrimSpace(value.ValueString()) == "" {
			diagnostics.AddError("Invalid group membership", fmt.Sprintf("%s must not be empty.", names[index]))
		}
	}
	return diagnostics
}

func (r *groupMembershipResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupMembershipResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.AddGroupMembership(ctx, plan.OrganizationID.ValueString(), plan.DirectoryID.ValueString(), plan.GroupID.ValueString(), plan.AccountID.ValueString())
	if err != nil {
		if mutationOutcomeMayBeAmbiguous(err) {
			present, readErr := r.client.HasGroupMembership(ctx, plan.OrganizationID.ValueString(), plan.DirectoryID.ValueString(), plan.GroupID.ValueString(), plan.AccountID.ValueString())
			if readErr == nil && present {
				setGroupMembershipState(ctx, &plan, resp)
				return
			}
			if readErr != nil {
				resp.Diagnostics.AddError("Unable to verify group membership", fmt.Sprintf("The add response was ambiguous and reading the resulting state also failed: %s", readErr))
			}
		}
		resp.Diagnostics.AddError("Unable to add user to group", err.Error())
		return
	}
	setGroupMembershipState(ctx, &plan, resp)
}

func (r *groupMembershipResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupMembershipResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	present, err := r.client.HasGroupMembership(ctx, state.OrganizationID.ValueString(), state.DirectoryID.ValueString(), state.GroupID.ValueString(), state.AccountID.ValueString())
	if err != nil {
		var httpErr *admin.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read group membership", err.Error())
		return
	}
	if !present {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.Identity.Set(ctx, groupMembershipIdentity(state))...)
}

func (r *groupMembershipResource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {
	// All configurable attributes require replacement.
}

func (r *groupMembershipResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupMembershipResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.RemoveGroupMembership(ctx, state.OrganizationID.ValueString(), state.DirectoryID.ValueString(), state.GroupID.ValueString(), state.AccountID.ValueString())
	if err == nil {
		return
	}
	var httpErr *admin.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
		return
	}
	if mutationOutcomeMayBeAmbiguous(err) {
		present, readErr := r.client.HasGroupMembership(ctx, state.OrganizationID.ValueString(), state.DirectoryID.ValueString(), state.GroupID.ValueString(), state.AccountID.ValueString())
		if readErr == nil && !present {
			return
		}
		if readErr != nil {
			resp.Diagnostics.AddError("Unable to verify group membership removal", fmt.Sprintf("The remove response was ambiguous and reading the resulting state also failed: %s", readErr))
		}
	}
	resp.Diagnostics.AddError("Unable to remove user from group", err.Error())
}

func (r *groupMembershipResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var identity groupMembershipResourceIdentityModel
	switch {
	case req.ID != "":
		parsed, err := parseGroupMembershipImportID(req.ID)
		if err != nil {
			resp.Diagnostics.AddError("Invalid import identifier", err.Error())
			return
		}
		identity = parsed
	case req.Identity != nil:
		resp.Diagnostics.Append(req.Identity.Get(ctx, &identity)...)
		if resp.Diagnostics.HasError() {
			return
		}
	default:
		resp.Diagnostics.AddError("Invalid import identifier", "Expected either a string ID or a resource identity.")
		return
	}

	resp.Diagnostics.Append(validateGroupMembership(identity.OrganizationID, identity.DirectoryID, identity.GroupID, identity.AccountID)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state := groupMembershipResourceModel{
		OrganizationID: identity.OrganizationID,
		DirectoryID:    identity.DirectoryID,
		GroupID:        identity.GroupID,
		AccountID:      identity.AccountID,
	}
	state.ID = types.StringValue(groupMembershipID(state))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
}

func parseGroupMembershipImportID(id string) (groupMembershipResourceIdentityModel, error) {
	parts := strings.Split(id, ",")
	if len(parts) != 4 {
		return groupMembershipResourceIdentityModel{}, errors.New("expected organization_id,directory_id,group_id,account_id")
	}
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if parts[index] == "" {
			return groupMembershipResourceIdentityModel{}, fmt.Errorf("import identifier component %d must not be empty", index+1)
		}
	}
	return groupMembershipResourceIdentityModel{
		OrganizationID: types.StringValue(parts[0]),
		DirectoryID:    types.StringValue(parts[1]),
		GroupID:        types.StringValue(parts[2]),
		AccountID:      types.StringValue(parts[3]),
	}, nil
}

func setGroupMembershipState(ctx context.Context, model *groupMembershipResourceModel, resp *resource.CreateResponse) {
	model.ID = types.StringValue(groupMembershipID(*model))
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, groupMembershipIdentity(*model))...)
}

func groupMembershipIdentity(model groupMembershipResourceModel) *groupMembershipResourceIdentityModel {
	return &groupMembershipResourceIdentityModel{
		OrganizationID: model.OrganizationID,
		DirectoryID:    model.DirectoryID,
		GroupID:        model.GroupID,
		AccountID:      model.AccountID,
	}
}

func groupMembershipID(model groupMembershipResourceModel) string {
	return strings.Join([]string{
		model.OrganizationID.ValueString(),
		model.DirectoryID.ValueString(),
		model.GroupID.ValueString(),
		model.AccountID.ValueString(),
	}, ",")
}
