package organization

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	organizationclient "github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization"
	"github.com/folio-sec/terraform-provider-atlassian/internal/docs"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	defaultGroupRolePollTimeout = 35 * time.Second
	defaultGroupRolePollInitial = 500 * time.Millisecond
	defaultGroupRolePollMaximum = 3 * time.Second
)

var _ resource.Resource = &groupRoleAssignmentResource{}
var _ resource.ResourceWithIdentity = &groupRoleAssignmentResource{}
var _ resource.ResourceWithImportState = &groupRoleAssignmentResource{}
var _ resource.ResourceWithValidateConfig = &groupRoleAssignmentResource{}

type groupRoleAssignmentClient interface {
	AssignGroupRole(context.Context, string, string, string, string, string) error
	RevokeGroupRole(context.Context, string, string, string, string, string) error
	HasGroupRole(context.Context, string, string, string, string, string) (bool, error)
}

type groupRoleAssignmentResource struct {
	client              groupRoleAssignmentClient
	pollTimeout         time.Duration
	pollInitialInterval time.Duration
	pollMaximumInterval time.Duration
}

type groupRoleAssignmentResourceModel struct {
	ID             types.String `tfsdk:"id"`
	OrganizationID types.String `tfsdk:"organization_id"`
	DirectoryID    types.String `tfsdk:"directory_id"`
	GroupID        types.String `tfsdk:"group_id"`
	Resource       types.String `tfsdk:"resource"`
	Role           types.String `tfsdk:"role"`
}

type groupRoleAssignmentResourceIdentityModel struct {
	OrganizationID types.String `tfsdk:"organization_id"`
	DirectoryID    types.String `tfsdk:"directory_id"`
	GroupID        types.String `tfsdk:"group_id"`
	Resource       types.String `tfsdk:"resource"`
	Role           types.String `tfsdk:"role"`
}

func NewGroupRoleAssignmentResource() resource.Resource {
	return &groupRoleAssignmentResource{
		pollTimeout:         defaultGroupRolePollTimeout,
		pollInitialInterval: defaultGroupRolePollInitial,
		pollMaximumInterval: defaultGroupRolePollMaximum,
	}
}

func (r *groupRoleAssignmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_group_role_assignment"
}

func (r *groupRoleAssignmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
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
		MarkdownDescription: docs.Description(
			"Assigns an application resource and role to an Atlassian organization group, granting every member of the group that app access.",
			docs.Callout{Sigil: docs.Warning, Label: "Warning", Text: `Destroying this resource revokes the role from the group and removes app
				access from every member. A member keeps access only if another group grants the same app.`},
			docs.Callout{Sigil: docs.Warning, Label: "Note", Text: `Role assignments are eventually consistent. After assigning or revoking a
				role the provider polls the group role assignment API for up to 35 seconds to confirm the result.`},
			docs.Callout{Sigil: docs.Info, Label: "Tip", Text: `A role that the group already holds cannot be created. Import the existing
				assignment instead; the error message reports the import identifier to use.`},
			docs.Callout{Sigil: docs.Info, Label: "Tip", Text: `Use the organization workspaces data source to look up the resource ARI for
				an app instance.`},
		),
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Description: "Composite group role assignment identifier.", Computed: true},
			"organization_id": requiredReplace("Atlassian organization ID used in the Organization API path."),
			"directory_id":    requiredReplace("Directory ID that contains the group."),
			"group_id":        requiredReplace("Immutable Atlassian group ID."),
			"resource":        requiredReplace("Application resource ARI beginning with ari:cloud:, such as ari:cloud:confluence::site/<site-id>."),
			"role":            requiredReplace("Atlassian application role to assign to the group, such as atlassian/user."),
		},
	}
}

func (r *groupRoleAssignmentResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	requiredString := func(description string) identityschema.StringAttribute {
		return identityschema.StringAttribute{Description: description, RequiredForImport: true}
	}
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"organization_id": requiredString("Atlassian organization ID used in the Organization API path."),
			"directory_id":    requiredString("Directory ID that contains the group."),
			"group_id":        requiredString("Immutable Atlassian group ID."),
			"resource":        requiredString("Application resource ARI."),
			"role":            requiredString("Atlassian application role."),
		},
	}
}

func (r *groupRoleAssignmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *groupRoleAssignmentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config groupRoleAssignmentResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateGroupRoleAssignment(groupRoleAssignmentResourceIdentityModel{
		OrganizationID: config.OrganizationID,
		DirectoryID:    config.DirectoryID,
		GroupID:        config.GroupID,
		Resource:       config.Resource,
		Role:           config.Role,
	})...)
}

// validateGroupRoleAssignment checks only what the provider can decide without
// calling the API. The role is deliberately not checked against a list: the
// accepted roles differ from the user endpoint's and exist only as prose in the
// specification, so a local copy would reject roles the API accepts. An
// unsupported role is answered with a clear 400.
func validateGroupRoleAssignment(values groupRoleAssignmentResourceIdentityModel) diag.Diagnostics {
	const summary = "Invalid group role assignment"
	diagnostics := validateNonEmpty(summary,
		namedValue{"organization_id", values.OrganizationID},
		namedValue{"directory_id", values.DirectoryID},
		namedValue{"group_id", values.GroupID},
		namedValue{"resource", values.Resource},
		namedValue{"role", values.Role},
	)
	diagnostics.Append(validateResourceARI(summary, values.Resource)...)
	return diagnostics
}

func (r *groupRoleAssignmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupRoleAssignmentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The assign endpoint is an upsert, so check for an existing assignment
	// first. Without this check Terraform would adopt access it did not grant
	// and revoke it on destroy, removing the app from every group member.
	present, err := r.client.HasGroupRole(ctx, plan.OrganizationID.ValueString(), plan.DirectoryID.ValueString(), plan.GroupID.ValueString(), plan.Resource.ValueString(), plan.Role.ValueString())
	switch {
	case err != nil && !hasHTTPStatus(err, http.StatusNotFound):
		resp.Diagnostics.AddError("Unable to check for an existing group role assignment", err.Error())
		return
	case present:
		resp.Diagnostics.Append(existingGroupRoleAssignmentError(plan))
		return
	}

	mutationErr := r.client.AssignGroupRole(ctx, plan.OrganizationID.ValueString(), plan.DirectoryID.ValueString(), plan.GroupID.ValueString(), plan.Resource.ValueString(), plan.Role.ValueString())
	if mutationErr != nil {
		// This endpoint answers 409 when the app has no seats left, which is a
		// definite failure rather than a sign the role is already assigned.
		if hasHTTPStatus(mutationErr, http.StatusConflict) {
			resp.Diagnostics.AddError("App user limit exceeded", fmt.Sprintf("Assigning %s on %s would exceed the user limit for the app. Free a seat or increase the subscription, then try again.\n\n%s", plan.Role.ValueString(), plan.Resource.ValueString(), mutationErr))
			return
		}
		if !mutationOutcomeMayBeAmbiguous(mutationErr) {
			resp.Diagnostics.AddError("Unable to assign group role", mutationErr.Error())
			return
		}
	}

	// The assignment request has been sent, so record state before verifying it.
	// Terraform persists the response state even alongside error diagnostics and
	// marks the resource tainted, which keeps possibly granted access tracked
	// instead of leaving it behind with nothing in state.
	plan.ID = types.StringValue(groupRoleAssignmentID(plan))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, groupRoleAssignmentIdentity(plan))...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.waitForGroupRole(ctx, plan, true); err != nil {
		if mutationErr != nil {
			resp.Diagnostics.AddError("Unable to verify group role assignment", fmt.Sprintf("The assignment response was ambiguous (%s), and the resulting state could not be verified: %s", mutationErr, err))
		} else {
			resp.Diagnostics.AddError("Unable to verify group role assignment", err.Error())
		}
		return
	}
}

func (r *groupRoleAssignmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupRoleAssignmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	present, err := r.client.HasGroupRole(ctx, state.OrganizationID.ValueString(), state.DirectoryID.ValueString(), state.GroupID.ValueString(), state.Resource.ValueString(), state.Role.ValueString())
	switch {
	case hasHTTPStatus(err, http.StatusNotFound):
		resp.State.RemoveResource(ctx)
	case err != nil:
		resp.Diagnostics.AddError("Unable to read group role assignment", err.Error())
	case !present:
		resp.State.RemoveResource(ctx)
	default:
		resp.Diagnostics.Append(resp.Identity.Set(ctx, groupRoleAssignmentIdentity(state))...)
	}
}

func (r *groupRoleAssignmentResource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {
	// All configurable attributes require replacement.
}

func (r *groupRoleAssignmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupRoleAssignmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	mutationErr := r.client.RevokeGroupRole(ctx, state.OrganizationID.ValueString(), state.DirectoryID.ValueString(), state.GroupID.ValueString(), state.Resource.ValueString(), state.Role.ValueString())
	if mutationErr != nil && !hasHTTPStatus(mutationErr, http.StatusNotFound) && !mutationOutcomeMayBeAmbiguous(mutationErr) {
		resp.Diagnostics.AddError("Unable to revoke group role", mutationErr.Error())
		return
	}
	if err := r.waitForGroupRole(ctx, state, false); err != nil {
		if mutationErr != nil {
			resp.Diagnostics.AddError("Unable to verify group role revocation", fmt.Sprintf("The revocation response was ambiguous (%s), and the resulting state could not be verified: %s", mutationErr, err))
		} else {
			resp.Diagnostics.AddError("Unable to verify group role revocation", err.Error())
		}
	}
}

func (r *groupRoleAssignmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	identity, ok := groupRoleAssignmentImportIdentity(ctx, req, resp)
	if !ok {
		return
	}
	resp.Diagnostics.Append(validateGroupRoleAssignment(identity)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state := groupRoleAssignmentResourceModel{
		OrganizationID: identity.OrganizationID,
		DirectoryID:    identity.DirectoryID,
		GroupID:        identity.GroupID,
		Resource:       identity.Resource,
		Role:           identity.Role,
	}
	state.ID = types.StringValue(groupRoleAssignmentID(state))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
}

func groupRoleAssignmentImportIdentity(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) (groupRoleAssignmentResourceIdentityModel, bool) {
	if req.ID != "" {
		identity, err := parseGroupRoleAssignmentImportID(req.ID)
		if err != nil {
			resp.Diagnostics.AddError("Invalid import identifier", err.Error())
			return groupRoleAssignmentResourceIdentityModel{}, false
		}
		return identity, true
	}
	if req.Identity == nil {
		resp.Diagnostics.AddError("Invalid import identifier", "Expected either a string ID or a resource identity.")
		return groupRoleAssignmentResourceIdentityModel{}, false
	}
	var identity groupRoleAssignmentResourceIdentityModel
	resp.Diagnostics.Append(req.Identity.Get(ctx, &identity)...)
	if resp.Diagnostics.HasError() {
		return groupRoleAssignmentResourceIdentityModel{}, false
	}
	return identity, true
}

func (r *groupRoleAssignmentResource) waitForGroupRole(ctx context.Context, model groupRoleAssignmentResourceModel, wantPresent bool) error {
	pollTimeout := r.pollTimeout
	if pollTimeout <= 0 {
		pollTimeout = defaultGroupRolePollTimeout
	}
	interval := r.pollInitialInterval
	if interval <= 0 {
		interval = defaultGroupRolePollInitial
	}
	maximumInterval := r.pollMaximumInterval
	if maximumInterval <= 0 {
		maximumInterval = defaultGroupRolePollMaximum
	}

	pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	var lastErr error
	for {
		present, err := r.client.HasGroupRole(pollCtx, model.OrganizationID.ValueString(), model.DirectoryID.ValueString(), model.GroupID.ValueString(), model.Resource.ValueString(), model.Role.ValueString())
		if err == nil {
			lastErr = nil
			if present == wantPresent {
				return nil
			}
		} else {
			lastErr = err
			var httpErr *admin.HTTPError
			if !wantPresent && errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
				return nil
			}
			if !readOutcomeMayBeTransient(err) {
				return fmt.Errorf("check group role state: %w", err)
			}
		}

		timer := time.NewTimer(interval)
		select {
		case <-pollCtx.Done():
			timer.Stop()
			if lastErr != nil {
				return fmt.Errorf("timed out after %s waiting for group role state: %w", pollTimeout, lastErr)
			}
			return fmt.Errorf("timed out after %s waiting for group role to become %s", pollTimeout, map[bool]string{true: "present", false: "absent"}[wantPresent])
		case <-timer.C:
		}

		if interval < maximumInterval {
			interval *= 2
			if interval > maximumInterval {
				interval = maximumInterval
			}
		}
	}
}

func existingGroupRoleAssignmentError(model groupRoleAssignmentResourceModel) diag.Diagnostic {
	return diag.NewErrorDiagnostic(
		"Group role is already assigned",
		fmt.Sprintf("%s is already assigned to group %s on %s. To manage the existing assignment with Terraform it needs to be imported into the state, for example:\n\n  terraform import <resource address> %q",
			model.Role.ValueString(), model.GroupID.ValueString(), model.Resource.ValueString(), groupRoleAssignmentID(model)),
	)
}

func parseGroupRoleAssignmentImportID(id string) (groupRoleAssignmentResourceIdentityModel, error) {
	parts := strings.Split(id, ",")
	if len(parts) != 5 {
		return groupRoleAssignmentResourceIdentityModel{}, errors.New("expected organization_id,directory_id,group_id,resource,role")
	}
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if parts[index] == "" {
			return groupRoleAssignmentResourceIdentityModel{}, fmt.Errorf("import identifier component %d must not be empty", index+1)
		}
	}
	return groupRoleAssignmentResourceIdentityModel{
		OrganizationID: types.StringValue(parts[0]),
		DirectoryID:    types.StringValue(parts[1]),
		GroupID:        types.StringValue(parts[2]),
		Resource:       types.StringValue(parts[3]),
		Role:           types.StringValue(parts[4]),
	}, nil
}

func groupRoleAssignmentIdentity(model groupRoleAssignmentResourceModel) *groupRoleAssignmentResourceIdentityModel {
	return &groupRoleAssignmentResourceIdentityModel{
		OrganizationID: model.OrganizationID,
		DirectoryID:    model.DirectoryID,
		GroupID:        model.GroupID,
		Resource:       model.Resource,
		Role:           model.Role,
	}
}

func groupRoleAssignmentID(model groupRoleAssignmentResourceModel) string {
	return strings.Join([]string{
		model.OrganizationID.ValueString(),
		model.DirectoryID.ValueString(),
		model.GroupID.ValueString(),
		model.Resource.ValueString(),
		model.Role.ValueString(),
	}, ",")
}

var _ groupRoleAssignmentClient = (*organizationclient.Service)(nil)
