package space

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// spaceRoleNameMaxCharacters is the longest name createSpaceRole accepts.
// The limit is documented nowhere: maxLength does not occur in the Confluence
// v2 specification, in the live upstream copy of it, or in the REST reference.
// It was measured on 2026-09-17 by binary search over createSpaceRole against
// a disposable tenant: 25 characters were accepted and 26 were rejected with
// "Role name exceeds maximum length". A name of 25 multibyte characters (75
// bytes) was accepted, so the bound counts characters rather than bytes.
// Atlassian could raise it, in which case this check would reject a name the
// API would now take.
const spaceRoleNameMaxCharacters = 25

// spaceRoleNonBlank rejects a value that is present but holds only
// whitespace, which the API would take and store verbatim.
var spaceRoleNonBlank = stringvalidator.RegexMatches(regexp.MustCompile(`\S`), "must not be empty")

var _ resource.Resource = &spaceRoleResource{}
var _ resource.ResourceWithIdentity = &spaceRoleResource{}
var _ resource.ResourceWithImportState = &spaceRoleResource{}

type spaceRoleResource struct{ client *Service }

type spaceRoleResourceModel struct {
	ID                          types.String `tfsdk:"id"`
	Name                        types.String `tfsdk:"name"`
	Description                 types.String `tfsdk:"description"`
	SpacePermissions            types.Set    `tfsdk:"space_permissions"`
	Type                        types.String `tfsdk:"type"`
	AnonymousReassignmentRoleID types.String `tfsdk:"anonymous_reassignment_role_id"`
	GuestReassignmentRoleID     types.String `tfsdk:"guest_reassignment_role_id"`
}

type spaceRoleIdentity struct {
	ID types.String `tfsdk:"id"`
}

// NewRoleResource returns the tenant-wide Confluence space-role resource.
func NewRoleResource() resource.Resource { return &spaceRoleResource{} }

func (r *spaceRoleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_confluence_space_role"
}

func (r *spaceRoleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	preserve := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a tenant-wide Confluence space role. Roles created through this resource have type CUSTOM. The role can then be assigned within a space using `atlassian_confluence_space_role_assignment`. Updates and deletes are asynchronous in Confluence; the provider tracks their tasks and verifies the observable final state before completing.\n\nConfluence allows at most 10 custom roles per tenant.\n\n## Required OAuth scopes\n\n- Read: `read:space.permission:confluence`\n- Create: `write:configuration:confluence`\n- Update and delete: `write:configuration:confluence`, `read:space.permission:confluence`, `read:confluence-space.summary`\n",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Tenant-specific space role ID.", Computed: true,
				PlanModifiers: preserve,
			},
			"name": schema.StringAttribute{
				Description: "Name of the space role. Confluence accepts at most 25 characters.",
				Required:    true,
				Validators: []validator.String{
					spaceRoleNonBlank,
					// UTF8LengthAtMost counts characters; LengthAtMost counts
					// bytes and would reject a name the API accepts.
					stringvalidator.UTF8LengthAtMost(spaceRoleNameMaxCharacters),
				},
			},
			"description": schema.StringAttribute{
				Description: "Description of the space role.",
				Required:    true,
				Validators:  []validator.String{spaceRoleNonBlank},
			},
			"space_permissions": schema.SetAttribute{
				Description: "IDs of the space permissions included in the role, such as `read/space`.",
				Required:    true, ElementType: types.StringType,
			},
			"type": schema.StringAttribute{
				Description: "Role type returned by Confluence. Roles created through this resource are CUSTOM.",
				Computed:    true, PlanModifiers: preserve,
			},
			"anonymous_reassignment_role_id": schema.StringAttribute{
				Description: "Update-only API field, so it cannot be set while the role is being created. Set it to the role ID that anonymous assignments should move to when an update would give this role permissions anonymous access may not hold. Confluence rejects such an update with `400 Role is currently held by anonymous and new permissions cannot be assigned to anonymous access` unless this value is set, and ignores it for updates that create no such conflict. Confluence does not return the value, so the provider preserves the configured one in state.",
				Optional:    true,
				Validators:  []validator.String{spaceRoleNonBlank},
			},
			"guest_reassignment_role_id": schema.StringAttribute{
				Description: "Update-only API field, so it cannot be set while the role is being created. Set it to the role ID that guest assignments should move to when an update would give this role permissions guests may not hold. This mirrors `anonymous_reassignment_role_id`, whose behaviour was measured against a live tenant; the guest case is assumed to work the same way and has not been verified, because it needs a guest account. Confluence does not return the value, so the provider preserves the configured one in state.",
				Optional:    true,
				Validators:  []validator.String{spaceRoleNonBlank},
			},
		},
	}
}

func (r *spaceRoleResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{Attributes: map[string]identityschema.Attribute{
		"id": identityschema.StringAttribute{Description: "Tenant-specific space role ID.", RequiredForImport: true},
	}}
}

func (r *spaceRoleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *spaceRoleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan spaceRoleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// createSpaceRole accepts only name, description, and spacePermissions;
	// the reassignment ids belong to updateSpaceRole, and a role that has just
	// been created has no anonymous or guest assignments to migrate. Reject
	// them rather than dropping them silently, because schema validation runs
	// before Terraform knows whether this is a create or an update.
	if reassignmentRequested(plan) {
		resp.Diagnostics.AddError(
			"Confluence space role reassignment is update-only",
			"anonymous_reassignment_role_id and guest_reassignment_role_id are accepted only by the Confluence space role update operation, so they cannot be set while the role is being created. Create the role without them, then add them in a later change.",
		)
		return
	}
	write, diagnostics := spaceRoleWriteRequest(ctx, plan, nil)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	recordCreatedIdentity := func(role SpaceRole) {
		partial := plan
		partial.ID = types.StringValue(role.ID)
		if role.Type != "" {
			partial.Type = types.StringValue(role.Type)
		} else if partial.Type.IsUnknown() {
			partial.Type = types.StringNull()
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)
		resp.Diagnostics.Append(resp.Identity.Set(ctx, &spaceRoleIdentity{ID: partial.ID})...)
	}
	created, err := r.client.CreateSpaceRole(ctx, write)
	if err != nil {
		if created.ID != "" {
			recordCreatedIdentity(created)
		}
		summary := "Unable to create Confluence space role"
		detail := err.Error()
		if created.ID != "" {
			summary = "Unable to read created Confluence space role"
			detail += ". Terraform recorded the role ID returned by Confluence in state so the role is not orphaned."
		} else if mutationOutcomeMayBeAmbiguous(err) {
			summary = "Unable to confirm Confluence space role creation"
			detail += ". The role may or may not have been created. If it exists, import it by its role ID; a name lookup is not safe evidence of ownership."
		}
		resp.Diagnostics.AddError(summary, detail)
		return
	}
	state, diagnostics := spaceRoleState(ctx, plan, created)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		recordCreatedIdentity(created)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &spaceRoleIdentity{ID: state.ID})...)
}

func (r *spaceRoleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state spaceRoleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.client.GetSpaceRoleByID(ctx, state.ID.ValueString())
	if err != nil {
		if confluence.IsNotFound(err) {
			absent, verifyErr := r.spaceRoleAbsent(ctx, state.ID.ValueString())
			if verifyErr != nil {
				resp.Diagnostics.AddError("Unable to verify Confluence space role absence", verifyErr.Error())
				return
			}
			if absent {
				resp.State.RemoveResource(ctx)
				return
			}
		}
		resp.Diagnostics.AddError("Unable to read Confluence space role", err.Error())
		return
	}
	// A deleted role was seen once during live testing to remain readable by
	// id as a 200 response with an empty permission set after the complete
	// catalogue had stopped returning it. That observation is not recorded in
	// a committed artefact, so treat it as unverified. Requiring both an empty
	// permission set and catalogue absence is correct either way: if the
	// tombstone is real, refresh detects the deletion; if it is not, a role
	// that genuinely holds no permissions stays in state because the
	// catalogue still lists it.
	if len(current.PermissionIDs) == 0 {
		absent, verifyErr := r.spaceRoleAbsent(ctx, state.ID.ValueString())
		if verifyErr != nil {
			resp.Diagnostics.AddError("Unable to verify Confluence space role absence", verifyErr.Error())
			return
		}
		if absent {
			resp.State.RemoveResource(ctx)
			return
		}
	}
	updated, diagnostics := spaceRoleState(ctx, state, current)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &spaceRoleIdentity{ID: updated.ID})...)
}

func (r *spaceRoleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state spaceRoleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	write, diagnostics := spaceRoleWriteRequest(ctx, plan, &state)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	taskID, mutationErr := r.client.UpdateSpaceRole(ctx, id, write)
	if mutationErr != nil && !mutationOutcomeMayBeAmbiguous(mutationErr) {
		resp.Diagnostics.AddError("Unable to update Confluence space role", mutationErr.Error())
		return
	}
	if mutationErr != nil && (write.AnonymousReassignmentRoleID != nil || write.GuestReassignmentRoleID != nil) {
		resp.Diagnostics.AddError(
			"Unable to confirm Confluence space role reassignment",
			fmt.Sprintf("The mutation response was ambiguous (%s), and Confluence does not expose the reassignment result through a role read.", mutationErr),
		)
		return
	}
	if taskID != "" {
		if err := r.client.waitForTask(ctx, taskID); err != nil {
			resp.Diagnostics.AddError("Unable to confirm Confluence space role update", err.Error())
			return
		}
	}
	current, err := r.waitForSpaceRole(ctx, id, &write)
	if err != nil {
		detail := err.Error()
		if mutationErr != nil {
			detail = fmt.Sprintf("The mutation response was ambiguous (%s), and the desired role could not be confirmed: %s", mutationErr, err)
		}
		resp.Diagnostics.AddError("Unable to confirm Confluence space role update", detail)
		return
	}
	updated, diagnostics := spaceRoleState(ctx, plan, current)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &spaceRoleIdentity{ID: updated.ID})...)
}

func (r *spaceRoleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state spaceRoleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	taskID, mutationErr := r.client.DeleteSpaceRole(ctx, id)
	if confluence.IsNotFound(mutationErr) {
		absent, verifyErr := r.spaceRoleAbsent(ctx, id)
		if verifyErr != nil {
			resp.Diagnostics.AddError("Unable to verify Confluence space role absence", verifyErr.Error())
			return
		}
		if absent {
			return
		}
	}
	if mutationErr != nil && !mutationOutcomeMayBeAmbiguous(mutationErr) {
		resp.Diagnostics.AddError("Unable to delete Confluence space role", mutationErr.Error())
		return
	}
	var taskErr error
	if taskID != "" {
		taskErr = r.client.waitForTask(ctx, taskID)
	}
	_, err := r.waitForSpaceRole(ctx, id, nil)
	if err != nil {
		detail := err.Error()
		if mutationErr != nil {
			detail = fmt.Sprintf("The mutation response was ambiguous (%s), and deletion could not be confirmed: %s", mutationErr, err)
		} else if taskErr != nil {
			detail = fmt.Sprintf("The deletion task did not complete successfully (%s), and deletion could not be confirmed: %s", taskErr, err)
		}
		resp.Diagnostics.AddError("Unable to confirm Confluence space role deletion", detail)
	}
}

func (r *spaceRoleResource) waitForSpaceRole(ctx context.Context, id string, want *SpaceRoleWriteRequest) (SpaceRole, error) {
	pollCtx, cancel := context.WithTimeout(ctx, taskPollTimeout)
	defer cancel()
	ticker := time.NewTicker(taskPollInterval)
	defer ticker.Stop()
	var unreadable error
	for {
		if want == nil {
			// Absence from the complete catalogue is the only evidence that
			// settles deletion, because the by-id endpoint can still answer
			// for a role the catalogue has dropped. Polling it as well could
			// not confirm anything the catalogue has not already confirmed.
			absent, err := r.spaceRoleAbsent(pollCtx, id)
			if err != nil {
				return SpaceRole{}, err
			}
			if absent {
				return SpaceRole{}, nil
			}
		} else {
			current, err := r.client.GetSpaceRoleByID(pollCtx, id)
			switch {
			case err == nil:
				if roleMatches(current, *want) {
					return current, nil
				}
			case confluence.IsNotFound(err):
				absent, verifyErr := r.spaceRoleAbsent(pollCtx, id)
				if verifyErr != nil {
					return SpaceRole{}, verifyErr
				}
				if absent {
					return SpaceRole{}, fmt.Errorf("space role %s no longer exists", id)
				}
				// The catalogue still lists the role, so getSpaceRolesById
				// returned its documented "no permission to view" 404 rather
				// than absence. Keep polling in case the update instead left
				// a transient state, but carry the error so a timeout reports
				// it instead of looking like slow convergence.
				unreadable = err
			default:
				return SpaceRole{}, err
			}
		}
		select {
		case <-pollCtx.Done():
			if want == nil {
				return SpaceRole{}, fmt.Errorf("timed out after %s waiting for role %s to be deleted", taskPollTimeout, id)
			}
			if unreadable != nil {
				return SpaceRole{}, fmt.Errorf("timed out after %s waiting for role %s to match the requested definition; the role remained in the catalogue but was not readable: %w", taskPollTimeout, id, unreadable)
			}
			return SpaceRole{}, fmt.Errorf("timed out after %s waiting for role %s to match the requested definition", taskPollTimeout, id)
		case <-ticker.C:
		}
	}
}

// spaceRoleAbsent disambiguates getSpaceRolesById's documented 404: that
// status can mean either absence or a lack of permission to view the role.
// The unfiltered catalogue is independent evidence; only absence from the
// complete catalogue permits Terraform to remove the resource from state.
func (r *spaceRoleResource) spaceRoleAbsent(ctx context.Context, id string) (bool, error) {
	roles, err := r.client.GetAvailableSpaceRoles(ctx)
	if err != nil {
		return false, fmt.Errorf("read role catalogue after GET /space-roles/%s returned not found: %w", id, err)
	}
	for _, role := range roles {
		if role.ID == id {
			return false, nil
		}
	}
	return true, nil
}

func roleMatches(role SpaceRole, want SpaceRoleWriteRequest) bool {
	if role.Name != want.Name || role.Description != want.Description || len(role.PermissionIDs) != len(want.PermissionIDs) {
		return false
	}
	actual := make(map[string]struct{}, len(role.PermissionIDs))
	for _, id := range role.PermissionIDs {
		actual[id] = struct{}{}
	}
	for _, id := range want.PermissionIDs {
		if _, ok := actual[id]; !ok {
			return false
		}
	}
	return true
}

// reassignmentRequested reports whether either update-only reassignment id
// carries a value, which Create rejects.
func reassignmentRequested(model spaceRoleResourceModel) bool {
	return setNonBlank(model.AnonymousReassignmentRoleID) || setNonBlank(model.GuestReassignmentRoleID)
}

// spaceRoleWriteRequest builds the write body for model. A nil prior means a
// create, which omits the update-only reassignment ids; otherwise prior is the
// current state and the ids are sent as configured.
//
// Live measurement settled what the reassignment ids do. Adding a permission
// anonymous access may not hold, to a role anonymous access currently holds,
// is rejected with 400 "Role is currently held by anonymous and new
// permissions cannot be assigned to anonymous access". The same update
// carrying anonymousReassignmentRoleId succeeds and moves the anonymous
// assignment to the named role. Updates that create no such conflict ignore
// the value: an unchanged definition, a description-only change and an added
// export/content each left the assignment where it was.
//
// So the id is a precondition for a class of update rather than an action of
// its own, which is why it is sent whenever configured. Sending it only when
// it changed, as an earlier revision did, would have failed exactly the
// updates that need it. Confluence never returns these fields, so a read
// cannot confirm a migration that did happen.
func spaceRoleWriteRequest(ctx context.Context, model spaceRoleResourceModel, prior *spaceRoleResourceModel) (SpaceRoleWriteRequest, diag.Diagnostics) {
	var permissionIDs []string
	diagnostics := model.SpacePermissions.ElementsAs(ctx, &permissionIDs, false)
	if diagnostics.HasError() {
		return SpaceRoleWriteRequest{}, diagnostics
	}
	result := SpaceRoleWriteRequest{Name: model.Name.ValueString(), Description: model.Description.ValueString(), PermissionIDs: permissionIDs}
	if prior == nil {
		return result, nil
	}
	if setNonBlank(model.AnonymousReassignmentRoleID) {
		value := model.AnonymousReassignmentRoleID.ValueString()
		result.AnonymousReassignmentRoleID = &value
	}
	if setNonBlank(model.GuestReassignmentRoleID) {
		value := model.GuestReassignmentRoleID.ValueString()
		result.GuestReassignmentRoleID = &value
	}
	return result, nil
}

func spaceRoleState(ctx context.Context, base spaceRoleResourceModel, role SpaceRole) (spaceRoleResourceModel, diag.Diagnostics) {
	permissionIDs, diagnostics := types.SetValueFrom(ctx, types.StringType, role.PermissionIDs)
	if diagnostics.HasError() {
		return spaceRoleResourceModel{}, diagnostics
	}
	return spaceRoleResourceModel{
		ID: types.StringValue(role.ID), Name: types.StringValue(role.Name), Description: types.StringValue(role.Description),
		SpacePermissions: permissionIDs, Type: types.StringValue(role.Type),
		AnonymousReassignmentRoleID: base.AnonymousReassignmentRoleID,
		GuestReassignmentRoleID:     base.GuestReassignmentRoleID,
	}, nil
}

func (r *spaceRoleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var id string
	switch {
	case req.ID != "":
		id = strings.TrimSpace(req.ID)
	case req.Identity != nil:
		var identity spaceRoleIdentity
		resp.Diagnostics.Append(req.Identity.Get(ctx, &identity)...)
		if resp.Diagnostics.HasError() {
			return
		}
		id = strings.TrimSpace(identity.ID.ValueString())
	}
	if id == "" {
		resp.Diagnostics.AddError("Invalid import identifier", "Expected a non-empty Confluence space role ID.")
		return
	}
	state := spaceRoleResourceModel{
		ID: types.StringValue(id), Name: types.StringUnknown(), Description: types.StringUnknown(),
		SpacePermissions: types.SetUnknown(types.StringType), Type: types.StringUnknown(),
		AnonymousReassignmentRoleID: types.StringNull(), GuestReassignmentRoleID: types.StringNull(),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &spaceRoleIdentity{ID: types.StringValue(id)})...)
}
