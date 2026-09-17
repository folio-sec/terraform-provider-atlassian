package space

import (
	"context"
	"fmt"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var _ resource.Resource = &roleAssignmentResource{}
var _ resource.ResourceWithIdentity = &roleAssignmentResource{}
var _ resource.ResourceWithImportState = &roleAssignmentResource{}
var _ resource.ResourceWithValidateConfig = &roleAssignmentResource{}

type roleAssignmentResource struct{ client *Service }

type roleAssignmentState struct {
	ID        types.String `tfsdk:"id"`
	SpaceID   types.String `tfsdk:"space_id"`
	Principal types.Object `tfsdk:"principal"`
	RoleID    types.String `tfsdk:"role_id"`
}

type roleAssignmentIdentity struct {
	SpaceID       types.String `tfsdk:"space_id"`
	PrincipalType types.String `tfsdk:"principal_type"`
	PrincipalID   types.String `tfsdk:"principal_id"`
}

// NewRoleAssignmentResource returns a principal-scoped Confluence space role.
func NewRoleAssignmentResource() resource.Resource { return &roleAssignmentResource{} }

func (r *roleAssignmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_confluence_space_role_assignment"
}

func (r *roleAssignmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	requiredReplace := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Description: description, Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one Confluence space role assignment for a `GROUP` or `USER` principal. A principal can hold at most one role per space, so changing `role_id` updates this resource in place. Custom access is a separate server state and is never overwritten or adopted.\n\n## Required OAuth scopes\n\n- `read:space:confluence`\n- `read:space.permission:confluence`\n- `write:space.permission:confluence`\n",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "Composite assignment identifier.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"space_id": requiredReplace("Numeric-string ID of the space."),
			"principal": schema.SingleNestedAttribute{
				Description: "Principal that receives the role.", Required: true,
				Attributes: map[string]schema.Attribute{
					"principal_type": requiredReplace("Principal type: GROUP or USER."),
					"principal_id":   requiredReplace("Immutable Atlassian group or account ID."),
				},
			},
			"role_id": schema.StringAttribute{Description: "Tenant-specific space role ID.", Required: true},
		},
	}
}

func (r *roleAssignmentResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	required := func(description string) identityschema.StringAttribute {
		return identityschema.StringAttribute{Description: description, RequiredForImport: true}
	}
	resp.IdentitySchema = identityschema.Schema{Attributes: map[string]identityschema.Attribute{
		"space_id":       required("Numeric-string ID of the space."),
		"principal_type": required("Principal type: GROUP or USER."),
		"principal_id":   required("Atlassian group or account ID."),
	}}
}

func (r *roleAssignmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	atlassianClient, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T", req.ProviderData))
		return
	}
	if atlassianClient.Confluence == nil {
		resp.Diagnostics.AddError("Confluence is not configured", "Set site_url plus basic_auth or service_account to use Confluence resources.")
		return
	}
	r.client = NewService(atlassianClient.Confluence)
}

func (r *roleAssignmentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config roleAssignmentState
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, diagnostics := roleIdentityFromState(ctx, config)
	resp.Diagnostics.Append(diagnostics...)
	resp.Diagnostics.Append(validateNonEmpty("Invalid Confluence space role assignment", namedValue{"space_id", config.SpaceID}, namedValue{"role_id", config.RoleID})...)
	if knownString(config.SpaceID) && !numericIDPattern.MatchString(config.SpaceID.ValueString()) {
		resp.Diagnostics.AddError("Invalid Confluence space role assignment", "space_id must be a numeric string.")
	}
}

func (r *roleAssignmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan roleAssignmentState
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	identity, diagnostics := roleIdentityFromState(ctx, plan)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !knownString(plan.RoleID) {
		resp.Diagnostics.AddError("Unable to create Confluence space role assignment", "role_id must be known during apply.")
		return
	}
	principal := identityPrincipal(identity)
	existing, err := r.singleRole(ctx, identity)
	if err != nil {
		resp.Diagnostics.AddError("Unable to check existing Confluence space access", err.Error())
		return
	}
	if existing != nil {
		resp.Diagnostics.AddError("Confluence space role assignment already exists", "This principal already has a role on the space. Import the existing assignment instead of overwriting access Terraform does not own.")
		return
	}
	permissions, err := r.client.GetSpacePermissionsAssignments(ctx, identity.SpaceID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to check existing Confluence Custom access", err.Error())
		return
	}
	if len(permissionMapFor(permissions, principal)) > 0 {
		resp.Diagnostics.AddError("Confluence Custom access already exists", "This principal has individual permissions and no role. Remove or transition that access explicitly before creating a role assignment.")
		return
	}

	plan.ID = types.StringValue(roleAssignmentID(identity))
	roleID := plan.RoleID.ValueString()
	mutationErr := r.client.SetSpaceRoleAssignment(ctx, identity.SpaceID.ValueString(), principal, &roleID)
	if mutationErr != nil && !mutationOutcomeMayBeAmbiguous(mutationErr) {
		resp.Diagnostics.AddError("Unable to create Confluence space role assignment", mutationErr.Error())
		return
	}
	actual, readErr := r.singleRole(ctx, identity)
	if readErr == nil && actual != nil && actual.RoleID == roleID {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
		return
	}
	// A failed verification read leaves the outcome unknown. If the read found
	// a different role, retain that observed value. A successful absence read
	// proves there is no object to preserve in state.
	if readErr != nil {
		plan.RoleID = types.StringUnknown()
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
	} else if actual != nil {
		plan.RoleID = types.StringValue(actual.RoleID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
	}
	if mutationErr != nil {
		resp.Diagnostics.AddError("Unable to verify Confluence space role assignment", fmt.Sprintf("The mutation response was ambiguous (%s), and the desired role could not be confirmed: %v", mutationErr, readErr))
		return
	}
	if readErr != nil {
		resp.Diagnostics.AddError("Unable to verify Confluence space role assignment", readErr.Error())
		return
	}
	resp.Diagnostics.AddError("Unable to verify Confluence space role assignment", "Atlassian accepted the request but the filtered read did not return the requested role.")
}

func (r *roleAssignmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state roleAssignmentState
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	identity, diagnostics := roleIdentityFromState(ctx, state)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	actual, err := r.singleRole(ctx, identity)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Confluence space role assignment", err.Error())
		return
	}
	if actual == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(roleAssignmentID(identity))
	state.RoleID = types.StringValue(actual.RoleID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
}

func (r *roleAssignmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan roleAssignmentState
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	identity, diagnostics := roleIdentityFromState(ctx, plan)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !knownString(plan.RoleID) {
		resp.Diagnostics.AddError("Unable to update Confluence space role assignment", "role_id must be known during apply.")
		return
	}
	principal := identityPrincipal(identity)
	existing, err := r.singleRole(ctx, identity)
	if err != nil {
		resp.Diagnostics.AddError("Unable to check existing Confluence space role assignment", err.Error())
		return
	}
	if existing == nil {
		permissions, err := r.client.GetSpacePermissionsAssignments(ctx, identity.SpaceID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Unable to check existing Confluence Custom access", err.Error())
			return
		}
		if len(permissionMapFor(permissions, principal)) > 0 {
			resp.Diagnostics.AddError("Confluence access changed to Custom access", "Refusing to update the role because this principal now has individual permissions.")
			return
		}
	}
	roleID := plan.RoleID.ValueString()
	err = r.client.SetSpaceRoleAssignment(ctx, identity.SpaceID.ValueString(), principal, &roleID)
	if err != nil && !mutationOutcomeMayBeAmbiguous(err) {
		resp.Diagnostics.AddError("Unable to update Confluence space role assignment", err.Error())
		return
	}
	actual, readErr := r.singleRole(ctx, identity)
	if readErr == nil && actual != nil && actual.RoleID == roleID {
		plan.ID = types.StringValue(roleAssignmentID(identity))
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
		return
	}
	switch {
	case err != nil:
		resp.Diagnostics.AddError("Unable to verify Confluence space role assignment update", fmt.Sprintf("The mutation response was ambiguous (%s), and the desired role could not be confirmed: %v", err, readErr))
	case readErr != nil:
		resp.Diagnostics.AddError("Unable to verify Confluence space role assignment update", readErr.Error())
	default:
		resp.Diagnostics.AddError("Unable to verify Confluence space role assignment update", "The filtered read did not return the requested role.")
	}
}

func (r *roleAssignmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state roleAssignmentState
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	identity, diagnostics := roleIdentityFromState(ctx, state)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.SetSpaceRoleAssignment(ctx, identity.SpaceID.ValueString(), identityPrincipal(identity), nil)
	if err != nil && !mutationOutcomeMayBeAmbiguous(err) {
		resp.Diagnostics.AddError("Unable to delete Confluence space role assignment", err.Error())
		return
	}
	actual, readErr := r.singleRole(ctx, identity)
	if readErr == nil && actual == nil {
		return
	}
	switch {
	case err != nil:
		resp.Diagnostics.AddError("Unable to verify Confluence space role assignment deletion", fmt.Sprintf("The mutation response was ambiguous (%s), and absence could not be confirmed: %v", err, readErr))
	case readErr != nil:
		resp.Diagnostics.AddError("Unable to verify Confluence space role assignment deletion", readErr.Error())
	default:
		resp.Diagnostics.AddError("Unable to verify Confluence space role assignment deletion", "The filtered read still returned a role assignment.")
	}
}

func (r *roleAssignmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	identity, ok := parseRoleAssignmentImport(ctx, req, resp)
	if !ok {
		return
	}
	actual, err := r.singleRole(ctx, identity)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import Confluence space role assignment", err.Error())
		return
	}
	if actual == nil {
		resp.Diagnostics.AddError("Unable to import Confluence space role assignment", "No role assignment exists for this space and principal.")
		return
	}
	principal, diagnostics := principalValue(ctx, actual.Principal)
	resp.Diagnostics.Append(diagnostics...)
	state := roleAssignmentState{
		ID: types.StringValue(roleAssignmentID(identity)), SpaceID: identity.SpaceID,
		Principal: principal, RoleID: types.StringValue(actual.RoleID),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
}

func (r *roleAssignmentResource) singleRole(ctx context.Context, identity roleAssignmentIdentity) (*SpaceRoleAssignment, error) {
	principal := identityPrincipal(identity)
	rows, err := r.client.GetSpaceRoleAssignments(ctx, identity.SpaceID.ValueString(), &principal)
	if err != nil {
		return nil, fmt.Errorf("read role assignment: %w", err)
	}
	if len(rows) > 1 {
		return nil, fmt.Errorf("API returned %d role assignments for one principal", len(rows))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

func roleIdentityFromState(ctx context.Context, state roleAssignmentState) (roleAssignmentIdentity, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	var principal principalResourceModel
	if state.Principal.IsNull() || state.Principal.IsUnknown() {
		return roleAssignmentIdentity{}, diagnostics
	}
	diagnostics.Append(state.Principal.As(ctx, &principal, basetypes.ObjectAsOptions{})...)
	identity := roleAssignmentIdentity{SpaceID: state.SpaceID, PrincipalType: principal.PrincipalType, PrincipalID: principal.PrincipalID}
	diagnostics.Append(validateRoleAssignmentIdentity(identity)...)
	return identity, diagnostics
}

func validateRoleAssignmentIdentity(identity roleAssignmentIdentity) diag.Diagnostics {
	diagnostics := validateNonEmpty("Invalid Confluence space role assignment",
		namedValue{"space_id", identity.SpaceID}, namedValue{"principal.principal_type", identity.PrincipalType}, namedValue{"principal.principal_id", identity.PrincipalID})
	if knownString(identity.SpaceID) && !numericIDPattern.MatchString(identity.SpaceID.ValueString()) {
		diagnostics.AddError("Invalid Confluence space role assignment", "space_id must be a numeric string.")
	}
	if knownString(identity.PrincipalType) {
		value := identity.PrincipalType.ValueString()
		if value != "GROUP" && value != "USER" {
			diagnostics.AddError("Invalid Confluence space role assignment", fmt.Sprintf("principal.principal_type must be GROUP or USER, got %q.", value))
		}
	}
	return diagnostics
}

func parseRoleAssignmentImport(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) (roleAssignmentIdentity, bool) {
	var identity roleAssignmentIdentity
	ok := parsePrincipalImport(ctx, req, resp, &identity, func(parts [3]string) roleAssignmentIdentity {
		return roleAssignmentIdentity{SpaceID: types.StringValue(parts[0]), PrincipalType: types.StringValue(parts[1]), PrincipalID: types.StringValue(parts[2])}
	}, validateRoleAssignmentIdentity)
	return identity, ok
}

func roleAssignmentID(identity roleAssignmentIdentity) string {
	return strings.Join([]string{identity.SpaceID.ValueString(), identity.PrincipalType.ValueString(), identity.PrincipalID.ValueString()}, ",")
}

func identityPrincipal(identity roleAssignmentIdentity) Principal {
	return Principal{Type: identity.PrincipalType.ValueString(), ID: identity.PrincipalID.ValueString()}
}

func principalValue(ctx context.Context, principal Principal) (types.Object, diag.Diagnostics) {
	return types.ObjectValueFrom(ctx, principalAttributeTypes(), principalResourceModel{
		PrincipalType: types.StringValue(principal.Type), PrincipalID: types.StringValue(principal.ID),
	})
}
