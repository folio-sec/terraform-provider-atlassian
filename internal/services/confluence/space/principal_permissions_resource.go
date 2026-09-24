package space

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/validation"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var _ resource.Resource = &principalPermissionsResource{}
var _ resource.ResourceWithIdentity = &principalPermissionsResource{}
var _ resource.ResourceWithImportState = &principalPermissionsResource{}
var _ resource.ResourceWithValidateConfig = &principalPermissionsResource{}

type principalPermissionsService interface {
	GetSpaceByID(context.Context, string) (Space, error)
	GetSpacePermissionsAssignments(context.Context, string) ([]PermissionAssignment, error)
	GetSpaceRoleAssignments(context.Context, string, *Principal) ([]SpaceRoleAssignment, error)
	AddPermissionToSpace(context.Context, string, Principal, PermissionOperation) (string, error)
	RemovePermission(context.Context, string, string) error
}

// These lists are the single definition of each attribute's rules. The schema
// applies them to configuration and validatePermissionsIdentity applies the
// same instances to identity values.
var (
	// The numeric pattern already excludes a blank value.
	permissionsSpaceIDValidators      = []validator.String{stringvalidator.RegexMatches(numericIDPattern, "must be a numeric string")}
	permissionsSpaceKeyValidators     = []validator.String{validation.NonBlank}
	permissionsPrincipalTypeValidator = []validator.String{stringvalidator.OneOf("user", "group")}
	permissionsPrincipalIDValidators  = []validator.String{validation.NonBlank}
)

type principalPermissionsResource struct{ client principalPermissionsService }

type permissionReconciliation struct {
	resource     *principalPermissionsResource
	ctx          context.Context
	identity     principalPermissionsIdentity
	spaceKey     string
	principal    Principal
	desired      map[string]PermissionOperation
	current      map[string]PermissionAssignment
	mutationSent bool
}

type principalPermissionsState struct {
	ID         types.String `tfsdk:"id"`
	SpaceID    types.String `tfsdk:"space_id"`
	SpaceKey   types.String `tfsdk:"space_key"`
	Principal  types.Object `tfsdk:"principal"`
	Operations types.Set    `tfsdk:"operations"`
}

type principalPermissionsIdentity struct {
	SpaceID       types.String `tfsdk:"space_id"`
	PrincipalType types.String `tfsdk:"principal_type"`
	PrincipalID   types.String `tfsdk:"principal_id"`
}

var writablePermissionOperations = map[string]struct{}{
	"manage_content/space": {}, "administer/space": {}, "manage_templates/space": {},
	"manage_look_and_feel/space": {}, "manage_users/space": {}, "restrict_content/space": {},
	"export/space": {}, "manage_nonlicensed_users/space": {}, "manage_public_links/space": {},
	"manage_guest_users/space": {}, "delete_space/space": {}, "archive_space/space": {},
	"delete/page": {}, "delete/space": {}, "archive/page": {}, "delete/comment": {},
	"delete/blogpost": {}, "delete/attachment": {}, "create/page": {}, "update/page": {},
	"create/comment": {}, "create/blogpost": {}, "update/blogpost": {},
	"create/attachment": {}, "export_content/space": {}, "read/space": {},
}

var companionProducers = map[string]struct{}{
	"create/comment": {}, "create/page": {}, "update/blogpost": {}, "administer/space": {},
}

// NewPrincipalPermissionsResource returns a complete Custom access set for one
// principal. This aggregate shape is intentional: v1 producer operations can
// create or delete companion assignment objects, so one resource per grant
// would create overlapping ownership.
func NewPrincipalPermissionsResource() resource.Resource {
	return &principalPermissionsResource{}
}

func (r *principalPermissionsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_confluence_space_principal_permissions"
}

func (r *principalPermissionsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	requiredReplace := func(description string, validators ...validator.String) schema.StringAttribute {
		return schema.StringAttribute{
			Description: description, Required: true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			Validators:    validators,
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the complete Custom access permission set for one Confluence space principal. Operations absent from configuration are revoked, so UI changes appear as drift. Every non-empty set must include `read/space`; remove the resource to revoke all access.\n\nThe resource uses v2 reads by `space_id` and v1 writes by `space_key`, and verifies that they identify the same space before writing. It never converts a role assignment into Custom access.\n\n## Required OAuth scopes\n\n- `read:space:confluence`\n- `read:space.permission:confluence`\n- `write:space.permission:confluence`\n",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "Composite Custom access identifier.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"space_id":  requiredReplace("Numeric-string ID used by v2 reads.", permissionsSpaceIDValidators...),
			"space_key": requiredReplace("Space key used by v1 writes. Must match space_id.", permissionsSpaceKeyValidators...),
			"principal": schema.SingleNestedAttribute{
				Description: "User or group whose complete Custom access set is managed.", Required: true,
				Attributes: map[string]schema.Attribute{
					"type": requiredReplace("Principal type: user or group.", permissionsPrincipalTypeValidator...),
					"id":   requiredReplace("Immutable Atlassian account or group ID.", permissionsPrincipalIDValidators...),
				},
			},
			"operations": schema.SetNestedAttribute{
				Description: "Authoritative, non-empty set of public API operation pairs. `read/space` is required.", Required: true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"key":         schema.StringAttribute{Description: "Operation key.", Required: true},
					"target_type": schema.StringAttribute{Description: "Operation target type.", Required: true},
				}},
			},
		},
	}
}

func (r *principalPermissionsResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	required := func(description string) identityschema.StringAttribute {
		return identityschema.StringAttribute{Description: description, RequiredForImport: true}
	}
	resp.IdentitySchema = identityschema.Schema{Attributes: map[string]identityschema.Attribute{
		"space_id": required("Numeric-string ID of the space."), "principal_type": required("Principal type: user or group."),
		"principal_id": required("Atlassian account or group ID."),
	}}
}

func (r *principalPermissionsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *principalPermissionsResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config principalPermissionsState
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, diagnostics := permissionsIdentityFromState(ctx, config)
	resp.Diagnostics.Append(diagnostics...)
	// The operations set is not a string attribute, so its contents stay here.
	_, diagnostics = operationsFromSet(ctx, config.Operations)
	resp.Diagnostics.Append(diagnostics...)
}

func (r *principalPermissionsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan principalPermissionsState
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Operations.IsNull() || plan.Operations.IsUnknown() {
		resp.Diagnostics.AddError("Unable to create Confluence Custom access", "operations must be known during apply.")
		return
	}
	identity, diagnostics := permissionsIdentityFromState(ctx, plan)
	resp.Diagnostics.Append(diagnostics...)
	desired, diagnostics := operationsFromSet(ctx, plan.Operations)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	principal := permissionsIdentityPrincipal(identity)
	if err := r.verifySpaceKey(ctx, identity.SpaceID.ValueString(), plan.SpaceKey.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to verify Confluence space identity", err.Error())
		return
	}
	roles, err := r.roleAssignments(ctx, identity)
	if err != nil {
		resp.Diagnostics.AddError("Unable to check Confluence role assignment", err.Error())
		return
	}
	if len(roles) > 0 {
		resp.Diagnostics.AddError("Confluence role assignment already exists", "This principal has a role. Remove or transition that assignment explicitly before creating Custom access.")
		return
	}
	all, err := r.client.GetSpacePermissionsAssignments(ctx, identity.SpaceID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to check existing Confluence Custom access", err.Error())
		return
	}
	if len(permissionMapFor(all, principal)) > 0 {
		resp.Diagnostics.AddError("Confluence Custom access already exists", "This principal already has individual permissions. Import them instead of overwriting access Terraform does not own.")
		return
	}

	actual, mutationSent, err := r.reconcile(ctx, identity, plan.SpaceKey.ValueString(), desired)
	if err != nil {
		if mutationSent && (actual == nil || len(actual) > 0) {
			plan.ID = types.StringValue(principalPermissionsID(identity))
			r.setObservedCreateState(ctx, plan, identity, actual, resp)
		}
		resp.Diagnostics.AddError("Unable to create Confluence Custom access", err.Error())
		return
	}
	plan.ID = types.StringValue(principalPermissionsID(identity))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
}

func (r *principalPermissionsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state principalPermissionsState
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	identity, diagnostics := permissionsIdentityFromState(ctx, state)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.verifySpaceKey(ctx, identity.SpaceID.ValueString(), state.SpaceKey.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to verify Confluence space identity", err.Error())
		return
	}
	principal := permissionsIdentityPrincipal(identity)
	roles, err := r.roleAssignments(ctx, identity)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Confluence role assignment", err.Error())
		return
	}
	if len(roles) > 0 {
		resp.Diagnostics.AddError("Confluence access changed to a role", "This principal now has a role assignment. Remove this Custom access resource from state and manage the role explicitly; the provider will not convert between access models.")
		return
	}
	all, err := r.client.GetSpacePermissionsAssignments(ctx, identity.SpaceID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Confluence Custom access", err.Error())
		return
	}
	current := permissionMapFor(all, principal)
	if len(current) == 0 {
		resp.State.RemoveResource(ctx)
		return
	}
	operations, diagnostics := operationSetValue(ctx, current)
	resp.Diagnostics.Append(diagnostics...)
	state.ID = types.StringValue(principalPermissionsID(identity))
	state.Operations = operations
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
}

func (r *principalPermissionsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan principalPermissionsState
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Operations.IsNull() || plan.Operations.IsUnknown() {
		resp.Diagnostics.AddError("Unable to update Confluence Custom access", "operations must be known during apply.")
		return
	}
	identity, diagnostics := permissionsIdentityFromState(ctx, plan)
	resp.Diagnostics.Append(diagnostics...)
	desired, diagnostics := operationsFromSet(ctx, plan.Operations)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.verifySpaceKey(ctx, identity.SpaceID.ValueString(), plan.SpaceKey.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to verify Confluence space identity", err.Error())
		return
	}
	roles, err := r.roleAssignments(ctx, identity)
	if err != nil {
		resp.Diagnostics.AddError("Unable to check Confluence role assignment", err.Error())
		return
	}
	if len(roles) > 0 {
		resp.Diagnostics.AddError("Confluence access changed to a role", "Refusing to update Custom access because this principal now has a role assignment.")
		return
	}
	actual, _, err := r.reconcile(ctx, identity, plan.SpaceKey.ValueString(), desired)
	if err != nil {
		if actual != nil {
			set, diagnostics := operationSetValue(ctx, actual)
			if diagnostics.HasError() {
				resp.Diagnostics.Append(diagnostics...)
			} else {
				plan.Operations = set
				plan.ID = types.StringValue(principalPermissionsID(identity))
				resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
				resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
			}
		}
		resp.Diagnostics.AddError("Unable to update Confluence Custom access", err.Error())
		return
	}
	plan.ID = types.StringValue(principalPermissionsID(identity))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
}

func (r *principalPermissionsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state principalPermissionsState
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	identity, diagnostics := permissionsIdentityFromState(ctx, state)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.verifySpaceKey(ctx, identity.SpaceID.ValueString(), state.SpaceKey.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to verify Confluence space identity", err.Error())
		return
	}
	roles, err := r.roleAssignments(ctx, identity)
	if err != nil {
		resp.Diagnostics.AddError("Unable to check Confluence role assignment", err.Error())
		return
	}
	if len(roles) > 0 {
		resp.Diagnostics.AddError("Confluence access changed to a role", "Refusing to delete legacy permissions because this principal now has a role assignment.")
		return
	}
	if err := r.deleteAll(ctx, identity, state.SpaceKey.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to delete Confluence Custom access", err.Error())
	}
}

func (r *principalPermissionsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	identity, ok := parsePermissionsImport(ctx, req, resp)
	if !ok {
		return
	}
	space, err := r.client.GetSpaceByID(ctx, identity.SpaceID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to import Confluence Custom access", err.Error())
		return
	}
	principal := permissionsIdentityPrincipal(identity)
	roles, err := r.roleAssignments(ctx, identity)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import Confluence Custom access", err.Error())
		return
	}
	if len(roles) > 0 {
		resp.Diagnostics.AddError("Unable to import Confluence Custom access", "The principal has a role assignment; import it with the role-assignment resource instead.")
		return
	}
	all, err := r.client.GetSpacePermissionsAssignments(ctx, identity.SpaceID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to import Confluence Custom access", err.Error())
		return
	}
	current := permissionMapFor(all, principal)
	if len(current) == 0 {
		resp.Diagnostics.AddError("Unable to import Confluence Custom access", "The principal has no Custom access on this space.")
		return
	}
	principalObject, diagnostics := customPrincipalValue(ctx, principal)
	resp.Diagnostics.Append(diagnostics...)
	operations, diagnostics := operationSetValue(ctx, current)
	resp.Diagnostics.Append(diagnostics...)
	state := principalPermissionsState{
		ID: types.StringValue(principalPermissionsID(identity)), SpaceID: identity.SpaceID,
		SpaceKey: types.StringValue(space.Key), Principal: principalObject, Operations: operations,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
}

func (r *principalPermissionsResource) verifySpaceKey(ctx context.Context, spaceID, spaceKey string) error {
	space, err := r.client.GetSpaceByID(ctx, spaceID)
	if err != nil {
		return fmt.Errorf("read space %s: %w", spaceID, err)
	}
	if space.Key != spaceKey {
		return fmt.Errorf("space_id %s resolves to key %q, not configured space_key %q", spaceID, space.Key, spaceKey)
	}
	return nil
}

func (r *principalPermissionsResource) roleAssignments(ctx context.Context, identity principalPermissionsIdentity) ([]SpaceRoleAssignment, error) {
	principal := permissionsIdentityPrincipal(identity)
	principal.Type = strings.ToUpper(principal.Type)
	roles, err := r.client.GetSpaceRoleAssignments(ctx, identity.SpaceID.ValueString(), &principal)
	if err != nil {
		return nil, fmt.Errorf("read role assignment: %w", err)
	}
	return roles, nil
}

func (r *principalPermissionsResource) reconcile(ctx context.Context, identity principalPermissionsIdentity, spaceKey string, desired map[string]PermissionOperation) (map[string]PermissionAssignment, bool, error) {
	reconciliation := &permissionReconciliation{
		resource: r, ctx: ctx, identity: identity, spaceKey: spaceKey,
		principal: permissionsIdentityPrincipal(identity), desired: desired,
	}
	err := reconciliation.read()
	if err != nil {
		return nil, false, err
	}
	if needsAdministrationRemoval(reconciliation.current, desired) {
		if err := r.requireOtherAdministrator(ctx, identity.SpaceID.ValueString(), reconciliation.principal); err != nil {
			return reconciliation.current, false, err
		}
	}
	steps := []func() error{
		func() error { return reconciliation.removeUndesired(true) },
		func() error { return reconciliation.removeUndesired(false) },
		reconciliation.addRead,
		reconciliation.addDesired,
		func() error { return reconciliation.removeUndesired(false) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return reconciliation.current, reconciliation.mutationSent, err
		}
	}
	if !sameOperationSet(reconciliation.current, desired) {
		return reconciliation.current, reconciliation.mutationSent, fmt.Errorf("final permission set differs from configuration: have %v, want %v", sortedOperationKeys(reconciliation.current), sortedDesiredKeys(desired))
	}
	return reconciliation.current, reconciliation.mutationSent, nil
}

func (p *permissionReconciliation) read() error {
	all, err := p.resource.client.GetSpacePermissionsAssignments(p.ctx, p.identity.SpaceID.ValueString())
	if err != nil {
		return fmt.Errorf("read current permission assignments: %w", err)
	}
	p.current = permissionMapFor(all, p.principal)
	return nil
}

func (p *permissionReconciliation) remove(key string) error {
	assignment, exists := p.current[key]
	if !exists {
		return nil
	}
	mutationErr := p.resource.client.RemovePermission(p.ctx, p.spaceKey, assignment.ID)
	if !errors.Is(mutationErr, ErrRequestNotSent) {
		p.mutationSent = true
	}
	readErr := p.read()
	if readErr == nil {
		if _, remains := p.current[key]; !remains {
			return nil
		}
	}
	if mutationErr != nil {
		return fmt.Errorf("remove %s: %w; follow-up read: %v", key, mutationErr, readErr)
	}
	if readErr != nil {
		return fmt.Errorf("read after removing %s: %w", key, readErr)
	}
	return fmt.Errorf("remove %s returned success but the assignment remains", key)
}

func (p *permissionReconciliation) add(key string) error {
	_, mutationErr := p.resource.client.AddPermissionToSpace(p.ctx, p.spaceKey, p.principal, p.desired[key])
	if !errors.Is(mutationErr, ErrRequestNotSent) {
		p.mutationSent = true
	}
	readErr := p.read()
	if readErr == nil {
		if _, exists := p.current[key]; exists {
			return nil
		}
	}
	if mutationErr != nil {
		return fmt.Errorf("add %s: %w; follow-up read: %v", key, mutationErr, readErr)
	}
	if readErr != nil {
		return fmt.Errorf("read after adding %s: %w", key, readErr)
	}
	return fmt.Errorf("add %s returned success but the assignment is absent", key)
}

func (p *permissionReconciliation) removeUndesired(producersOnly bool) error {
	for _, key := range sortedOperationKeys(p.current) {
		_, producer := companionProducers[key]
		_, wanted := p.desired[key]
		if key == "read/space" || wanted || producersOnly != producer {
			continue
		}
		if err := p.remove(key); err != nil {
			return err
		}
	}
	return nil
}

func (p *permissionReconciliation) addRead() error {
	if _, wanted := p.desired["read/space"]; !wanted {
		return nil
	}
	if _, exists := p.current["read/space"]; exists {
		return nil
	}
	return p.add("read/space")
}

func (p *permissionReconciliation) addDesired() error {
	for _, key := range sortedDesiredKeys(p.desired) {
		if key == "read/space" {
			continue
		}
		if _, exists := p.current[key]; exists {
			continue
		}
		if err := p.add(key); err != nil {
			return err
		}
	}
	return nil
}

func (r *principalPermissionsResource) deleteAll(ctx context.Context, identity principalPermissionsIdentity, spaceKey string) error {
	principal := permissionsIdentityPrincipal(identity)
	all, err := r.client.GetSpacePermissionsAssignments(ctx, identity.SpaceID.ValueString())
	if err != nil {
		return fmt.Errorf("read current permission assignments: %w", err)
	}
	current := permissionMapFor(all, principal)
	if len(current) == 0 {
		return nil
	}
	if needsAdministrationRemoval(current, map[string]PermissionOperation{}) {
		if err := r.requireOtherAdministrator(ctx, identity.SpaceID.ValueString(), principal); err != nil {
			return err
		}
	}
	for {
		key := ""
		for _, candidate := range sortedOperationKeys(current) {
			if candidate != "read/space" {
				key = candidate
				break
			}
		}
		if key == "" {
			break
		}
		if err := r.client.RemovePermission(ctx, spaceKey, current[key].ID); err != nil {
			return fmt.Errorf("remove %s: %w", key, err)
		}
		all, err = r.client.GetSpacePermissionsAssignments(ctx, identity.SpaceID.ValueString())
		if err != nil {
			return fmt.Errorf("read after removing %s: %w", key, err)
		}
		next := permissionMapFor(all, principal)
		if remaining, exists := next[key]; exists && remaining.ID == current[key].ID {
			return fmt.Errorf("remove %s returned success but the assignment remains", key)
		}
		current = next
	}
	if read, exists := current["read/space"]; exists {
		// A settled success is enough here. Removing read may remove the caller's
		// own access, making a verification read unauthorized.
		if err := r.client.RemovePermission(ctx, spaceKey, read.ID); err != nil {
			return fmt.Errorf("remove read/space: %w", err)
		}
	}
	return nil
}

func (r *principalPermissionsResource) requireOtherAdministrator(ctx context.Context, spaceID string, excluded Principal) error {
	all, err := r.client.GetSpacePermissionsAssignments(ctx, spaceID)
	if err != nil {
		return fmt.Errorf("verify another space administrator: %w", err)
	}
	for _, row := range all {
		if !samePrincipal(row.Principal, excluded) && operationKey(row.Operation) == "administer/space" {
			return nil
		}
	}
	return fmt.Errorf("refusing to remove administration access: no other principal with administer/space was found")
}

func (r *principalPermissionsResource) setObservedCreateState(ctx context.Context, plan principalPermissionsState, identity principalPermissionsIdentity, actual map[string]PermissionAssignment, resp *resource.CreateResponse) {
	if set, diagnostics := operationSetValue(ctx, actual); !diagnostics.HasError() && len(actual) > 0 {
		plan.Operations = set
	} else if actual == nil {
		plan.Operations = types.SetUnknown(types.ObjectType{AttrTypes: permissionOperationAttributeTypes()})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
}

func permissionsIdentityFromState(ctx context.Context, state principalPermissionsState) (principalPermissionsIdentity, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	var principal accessPrincipalModel
	if !state.Principal.IsNull() && !state.Principal.IsUnknown() {
		diagnostics.Append(state.Principal.As(ctx, &principal, basetypes.ObjectAsOptions{})...)
	}
	identity := principalPermissionsIdentity{SpaceID: state.SpaceID, PrincipalType: principal.Type, PrincipalID: principal.ID}
	diagnostics.Append(validatePermissionsIdentity(ctx, identity, permissionsStatePaths)...)
	return identity, diagnostics
}

// permissionsStatePaths is where the identity's parts live in resource state.
var permissionsStatePaths = principalPaths{
	spaceID:       path.Root("space_id"),
	principalType: path.Root("principal").AtName("type"),
	principalID:   path.Root("principal").AtName("id"),
}

// validatePermissionsIdentity applies the schema's own attribute validators to
// identity values, which Terraform does not validate for us, reporting at paths
// in the layout the caller is validating.
func validatePermissionsIdentity(ctx context.Context, identity principalPermissionsIdentity, paths principalPaths) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	diagnostics.Append(validation.RunString(ctx, paths.spaceID, identity.SpaceID, permissionsSpaceIDValidators)...)
	diagnostics.Append(validation.RunString(ctx, paths.principalType, identity.PrincipalType, permissionsPrincipalTypeValidator)...)
	diagnostics.Append(validation.RunString(ctx, paths.principalID, identity.PrincipalID, permissionsPrincipalIDValidators)...)
	return diagnostics
}

func operationsFromSet(ctx context.Context, set types.Set) (map[string]PermissionOperation, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	result := map[string]PermissionOperation{}
	if set.IsNull() || set.IsUnknown() {
		return result, diagnostics
	}
	var rows []permissionOperationModel
	diagnostics.Append(set.ElementsAs(ctx, &rows, false)...)
	for _, row := range rows {
		key := operationKey(PermissionOperation{Key: row.Key.ValueString(), TargetType: row.TargetType.ValueString()})
		if _, valid := writablePermissionOperations[key]; !valid {
			diagnostics.AddError("Invalid Confluence Custom access operation", fmt.Sprintf("operation %q is not one of the 26 measured writable key/target_type pairs.", key))
			continue
		}
		result[key] = PermissionOperation{Key: row.Key.ValueString(), TargetType: row.TargetType.ValueString()}
	}
	if len(result) == 0 && !set.IsUnknown() {
		diagnostics.AddError("Invalid Confluence Custom access", "operations must contain at least one entry; remove the resource to revoke all access.")
	} else if _, ok := result["read/space"]; !ok {
		diagnostics.AddError("Invalid Confluence Custom access", "every non-empty operations set must contain read/space.")
	}
	return result, diagnostics
}

func operationSetValue(ctx context.Context, current map[string]PermissionAssignment) (types.Set, diag.Diagnostics) {
	rows := make([]permissionOperationModel, 0, len(current))
	for _, key := range sortedOperationKeys(current) {
		operation := current[key].Operation
		rows = append(rows, permissionOperationModel{Key: types.StringValue(operation.Key), TargetType: types.StringValue(operation.TargetType)})
	}
	return types.SetValueFrom(ctx, types.ObjectType{AttrTypes: permissionOperationAttributeTypes()}, rows)
}

func customPrincipalValue(ctx context.Context, principal Principal) (types.Object, diag.Diagnostics) {
	return types.ObjectValueFrom(ctx, accessPrincipalAttributeTypes(), accessPrincipalModel{Type: types.StringValue(principal.Type), ID: types.StringValue(principal.ID)})
}

func permissionMapFor(rows []PermissionAssignment, principal Principal) map[string]PermissionAssignment {
	result := map[string]PermissionAssignment{}
	for _, row := range rows {
		if samePrincipal(row.Principal, principal) {
			result[operationKey(row.Operation)] = row
		}
	}
	return result
}

func samePrincipal(left, right Principal) bool {
	return left.ID == right.ID && strings.EqualFold(left.Type, right.Type)
}

func operationKey(operation PermissionOperation) string {
	return operation.Key + "/" + operation.TargetType
}

func sortedOperationKeys(current map[string]PermissionAssignment) []string {
	keys := make([]string, 0, len(current))
	for key := range current {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedDesiredKeys(desired map[string]PermissionOperation) []string {
	keys := make([]string, 0, len(desired))
	for key := range desired {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		_, ip := companionProducers[keys[i]]
		_, jp := companionProducers[keys[j]]
		if ip != jp {
			return ip
		}
		return keys[i] < keys[j]
	})
	return keys
}

func sameOperationSet(current map[string]PermissionAssignment, desired map[string]PermissionOperation) bool {
	if len(current) != len(desired) {
		return false
	}
	for key := range desired {
		if _, ok := current[key]; !ok {
			return false
		}
	}
	return true
}

func needsAdministrationRemoval(current map[string]PermissionAssignment, desired map[string]PermissionOperation) bool {
	if _, administer := current["administer/space"]; !administer {
		return false
	}
	if _, keepAdmin := desired["administer/space"]; !keepAdmin {
		return true
	}
	_, keepRead := desired["read/space"]
	return !keepRead
}

func parsePermissionsImport(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) (principalPermissionsIdentity, bool) {
	var identity principalPermissionsIdentity
	ok := parsePrincipalImport(ctx, req, resp, &identity, func(parts [3]string) principalPermissionsIdentity {
		return principalPermissionsIdentity{SpaceID: types.StringValue(parts[0]), PrincipalType: types.StringValue(parts[1]), PrincipalID: types.StringValue(parts[2])}
	}, func(identity principalPermissionsIdentity) diag.Diagnostics {
		return validatePermissionsIdentity(ctx, identity, importIdentityPaths)
	})
	return identity, ok
}

func permissionsIdentityPrincipal(identity principalPermissionsIdentity) Principal {
	return Principal{Type: identity.PrincipalType.ValueString(), ID: identity.PrincipalID.ValueString()}
}

func principalPermissionsID(identity principalPermissionsIdentity) string {
	return strings.Join([]string{identity.SpaceID.ValueString(), identity.PrincipalType.ValueString(), identity.PrincipalID.ValueString()}, ",")
}
