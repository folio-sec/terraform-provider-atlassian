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
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const organizationAdminRole = "atlassian/org-admin"

const (
	defaultOrganizationRolePollTimeout = 35 * time.Second
	defaultOrganizationRolePollInitial = 500 * time.Millisecond
	defaultOrganizationRolePollMaximum = 3 * time.Second
)

var _ resource.Resource = &userOrganizationRoleAssignmentResource{}
var _ resource.ResourceWithIdentity = &userOrganizationRoleAssignmentResource{}
var _ resource.ResourceWithImportState = &userOrganizationRoleAssignmentResource{}
var _ resource.ResourceWithValidateConfig = &userOrganizationRoleAssignmentResource{}

type organizationRoleAssignmentClient interface {
	AssignOrganizationRole(context.Context, string, string, string) error
	RevokeOrganizationRole(context.Context, string, string, string) error
	HasDirectOrganizationRole(context.Context, string, string, string, string) (bool, error)
}

type userOrganizationRoleAssignmentResource struct {
	client              organizationRoleAssignmentClient
	pollTimeout         time.Duration
	pollInitialInterval time.Duration
	pollMaximumInterval time.Duration
}

type userOrganizationRoleAssignmentResourceModel struct {
	ID             types.String `tfsdk:"id"`
	OrganizationID types.String `tfsdk:"organization_id"`
	DirectoryID    types.String `tfsdk:"directory_id"`
	AccountID      types.String `tfsdk:"account_id"`
	Role           types.String `tfsdk:"role"`
}

type userOrganizationRoleAssignmentResourceIdentityModel struct {
	OrganizationID types.String `tfsdk:"organization_id"`
	DirectoryID    types.String `tfsdk:"directory_id"`
	AccountID      types.String `tfsdk:"account_id"`
	Role           types.String `tfsdk:"role"`
}

func NewUserOrganizationRoleAssignmentResource() resource.Resource {
	return &userOrganizationRoleAssignmentResource{
		pollTimeout:         defaultOrganizationRolePollTimeout,
		pollInitialInterval: defaultOrganizationRolePollInitial,
		pollMaximumInterval: defaultOrganizationRolePollMaximum,
	}
}

func (r *userOrganizationRoleAssignmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_user_organization_role_assignment"
}

func (r *userOrganizationRoleAssignmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
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
		Description: "Assigns an organization-level role directly to an Atlassian organization user.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Description: "Composite organization-level role assignment identifier.", Computed: true},
			"organization_id": requiredReplace("Atlassian organization ID used in the Organization API path."),
			"directory_id":    requiredReplace("Directory ID used to read the user's current role assignments."),
			"account_id":      requiredReplace("Opaque Atlassian account ID."),
			"role":            requiredReplace("Atlassian organization-level role. Currently only atlassian/org-admin is supported."),
		},
	}
}

func (r *userOrganizationRoleAssignmentResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	requiredString := func(description string) identityschema.StringAttribute {
		return identityschema.StringAttribute{Description: description, RequiredForImport: true}
	}
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"organization_id": requiredString("Atlassian organization ID used in the Organization API path."),
			"directory_id":    requiredString("Directory ID used to read the user's current role assignments."),
			"account_id":      requiredString("Opaque Atlassian account ID."),
			"role":            requiredString("Atlassian organization-level role."),
		},
	}
}

func (r *userOrganizationRoleAssignmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *userOrganizationRoleAssignmentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config userOrganizationRoleAssignmentResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateUserOrganizationRoleAssignment(config.OrganizationID, config.DirectoryID, config.AccountID, config.Role)...)
}

func validateUserOrganizationRoleAssignment(organizationID, directoryID, accountID, role types.String) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	values := []struct {
		name  string
		value types.String
	}{
		{"organization_id", organizationID},
		{"directory_id", directoryID},
		{"account_id", accountID},
		{"role", role},
	}
	for _, item := range values {
		if !item.value.IsNull() && !item.value.IsUnknown() && strings.TrimSpace(item.value.ValueString()) == "" {
			diagnostics.AddError("Invalid organization role assignment", fmt.Sprintf("%s must not be empty.", item.name))
		}
	}
	if !role.IsNull() && !role.IsUnknown() && role.ValueString() != organizationAdminRole {
		diagnostics.AddError("Invalid organization role assignment", fmt.Sprintf("role must be %q.", organizationAdminRole))
	}
	return diagnostics
}

func (r *userOrganizationRoleAssignmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userOrganizationRoleAssignmentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	mutationErr := r.client.AssignOrganizationRole(ctx, plan.OrganizationID.ValueString(), plan.AccountID.ValueString(), plan.Role.ValueString())
	if mutationErr != nil && !organizationRoleMutationNeedsVerification(mutationErr) {
		resp.Diagnostics.AddError("Unable to assign organization-level role", mutationErr.Error())
		return
	}
	if err := r.waitForDirectRole(ctx, plan, true); err != nil {
		if mutationErr != nil {
			resp.Diagnostics.AddError("Unable to verify organization-level role assignment", fmt.Sprintf("The assignment response was ambiguous (%s), and the resulting state could not be verified: %s", mutationErr, err))
		} else {
			resp.Diagnostics.AddError("Unable to verify organization-level role assignment", err.Error())
		}
		return
	}

	plan.ID = types.StringValue(userOrganizationRoleAssignmentID(plan))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, userOrganizationRoleAssignmentIdentity(plan))...)
}

func (r *userOrganizationRoleAssignmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userOrganizationRoleAssignmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	present, err := r.client.HasDirectOrganizationRole(ctx, state.OrganizationID.ValueString(), state.DirectoryID.ValueString(), state.AccountID.ValueString(), state.Role.ValueString())
	switch {
	case hasHTTPStatus(err, http.StatusNotFound):
		resp.State.RemoveResource(ctx)
	case err != nil:
		resp.Diagnostics.AddError("Unable to read organization-level role assignment", err.Error())
	case !present:
		resp.State.RemoveResource(ctx)
	default:
		resp.Diagnostics.Append(resp.Identity.Set(ctx, userOrganizationRoleAssignmentIdentity(state))...)
	}
}

func (r *userOrganizationRoleAssignmentResource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {
	// All configurable attributes require replacement.
}

func (r *userOrganizationRoleAssignmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userOrganizationRoleAssignmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	mutationErr := r.client.RevokeOrganizationRole(ctx, state.OrganizationID.ValueString(), state.AccountID.ValueString(), state.Role.ValueString())
	if mutationErr != nil {
		var httpErr *admin.HTTPError
		if !errors.As(mutationErr, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
			if !organizationRoleMutationNeedsVerification(mutationErr) {
				resp.Diagnostics.AddError("Unable to revoke organization-level role", mutationErr.Error())
				return
			}
		}
	}
	if err := r.waitForDirectRole(ctx, state, false); err != nil {
		if mutationErr != nil {
			resp.Diagnostics.AddError("Unable to verify organization-level role revocation", fmt.Sprintf("The revocation response was ambiguous (%s), and the resulting state could not be verified: %s", mutationErr, err))
		} else {
			resp.Diagnostics.AddError("Unable to verify organization-level role revocation", err.Error())
		}
	}
}

func (r *userOrganizationRoleAssignmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	identity, ok := userOrganizationRoleAssignmentImportIdentity(ctx, req, resp)
	if !ok {
		return
	}
	resp.Diagnostics.Append(validateUserOrganizationRoleAssignment(identity.OrganizationID, identity.DirectoryID, identity.AccountID, identity.Role)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state := userOrganizationRoleAssignmentResourceModel{
		OrganizationID: identity.OrganizationID,
		DirectoryID:    identity.DirectoryID,
		AccountID:      identity.AccountID,
		Role:           identity.Role,
	}
	state.ID = types.StringValue(userOrganizationRoleAssignmentID(state))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
}

func userOrganizationRoleAssignmentImportIdentity(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) (userOrganizationRoleAssignmentResourceIdentityModel, bool) {
	if req.ID != "" {
		identity, err := parseUserOrganizationRoleAssignmentImportID(req.ID)
		if err != nil {
			resp.Diagnostics.AddError("Invalid import identifier", err.Error())
			return userOrganizationRoleAssignmentResourceIdentityModel{}, false
		}
		return identity, true
	}
	if req.Identity == nil {
		resp.Diagnostics.AddError("Invalid import identifier", "Expected either a string ID or a resource identity.")
		return userOrganizationRoleAssignmentResourceIdentityModel{}, false
	}
	var identity userOrganizationRoleAssignmentResourceIdentityModel
	resp.Diagnostics.Append(req.Identity.Get(ctx, &identity)...)
	if resp.Diagnostics.HasError() {
		return userOrganizationRoleAssignmentResourceIdentityModel{}, false
	}
	return identity, true
}

func (r *userOrganizationRoleAssignmentResource) waitForDirectRole(ctx context.Context, model userOrganizationRoleAssignmentResourceModel, wantPresent bool) error {
	pollTimeout := r.pollTimeout
	if pollTimeout <= 0 {
		pollTimeout = defaultOrganizationRolePollTimeout
	}
	interval := r.pollInitialInterval
	if interval <= 0 {
		interval = defaultOrganizationRolePollInitial
	}
	maximumInterval := r.pollMaximumInterval
	if maximumInterval <= 0 {
		maximumInterval = defaultOrganizationRolePollMaximum
	}

	pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	var lastErr error
	for {
		present, err := r.client.HasDirectOrganizationRole(pollCtx, model.OrganizationID.ValueString(), model.DirectoryID.ValueString(), model.AccountID.ValueString(), model.Role.ValueString())
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
				return fmt.Errorf("check direct organization role state: %w", err)
			}
		}

		timer := time.NewTimer(interval)
		select {
		case <-pollCtx.Done():
			timer.Stop()
			if lastErr != nil {
				return fmt.Errorf("timed out after %s waiting for direct role state: %w", pollTimeout, lastErr)
			}
			return fmt.Errorf("timed out after %s waiting for direct role to become %s", pollTimeout, map[bool]string{true: "present", false: "absent"}[wantPresent])
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

func readOutcomeMayBeTransient(err error) bool {
	var httpErr *admin.HTTPError
	if !errors.As(err, &httpErr) {
		return true
	}
	return httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode >= http.StatusInternalServerError
}

func organizationRoleMutationNeedsVerification(err error) bool {
	var httpErr *admin.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusConflict || httpErr.StatusCode >= http.StatusInternalServerError
	}
	return true
}

func hasHTTPStatus(err error, statusCode int) bool {
	var httpErr *admin.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == statusCode
}

func parseUserOrganizationRoleAssignmentImportID(id string) (userOrganizationRoleAssignmentResourceIdentityModel, error) {
	parts := strings.Split(id, ",")
	if len(parts) != 4 {
		return userOrganizationRoleAssignmentResourceIdentityModel{}, errors.New("expected organization_id,directory_id,account_id,role")
	}
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if parts[index] == "" {
			return userOrganizationRoleAssignmentResourceIdentityModel{}, fmt.Errorf("import identifier component %d must not be empty", index+1)
		}
	}
	return userOrganizationRoleAssignmentResourceIdentityModel{
		OrganizationID: types.StringValue(parts[0]),
		DirectoryID:    types.StringValue(parts[1]),
		AccountID:      types.StringValue(parts[2]),
		Role:           types.StringValue(parts[3]),
	}, nil
}

func userOrganizationRoleAssignmentIdentity(model userOrganizationRoleAssignmentResourceModel) *userOrganizationRoleAssignmentResourceIdentityModel {
	return &userOrganizationRoleAssignmentResourceIdentityModel{
		OrganizationID: model.OrganizationID,
		DirectoryID:    model.DirectoryID,
		AccountID:      model.AccountID,
		Role:           model.Role,
	}
}

func userOrganizationRoleAssignmentID(model userOrganizationRoleAssignmentResourceModel) string {
	return strings.Join([]string{
		model.OrganizationID.ValueString(),
		model.DirectoryID.ValueString(),
		model.AccountID.ValueString(),
		model.Role.ValueString(),
	}, ",")
}

var _ organizationRoleAssignmentClient = (*organizationclient.Service)(nil)
