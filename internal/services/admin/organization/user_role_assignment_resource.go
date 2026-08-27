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

var _ resource.Resource = &userRoleAssignmentResource{}
var _ resource.ResourceWithIdentity = &userRoleAssignmentResource{}
var _ resource.ResourceWithImportState = &userRoleAssignmentResource{}
var _ resource.ResourceWithValidateConfig = &userRoleAssignmentResource{}

type userRoleAssignmentResource struct {
	client *organizationclient.Service
}

type userRoleAssignmentResourceModel struct {
	ID             types.String `tfsdk:"id"`
	OrganizationID types.String `tfsdk:"organization_id"`
	DirectoryID    types.String `tfsdk:"directory_id"`
	AccountID      types.String `tfsdk:"account_id"`
	Resource       types.String `tfsdk:"resource"`
	Role           types.String `tfsdk:"role"`
}

type userRoleAssignmentResourceIdentityModel struct {
	OrganizationID types.String `tfsdk:"organization_id"`
	DirectoryID    types.String `tfsdk:"directory_id"`
	AccountID      types.String `tfsdk:"account_id"`
	Resource       types.String `tfsdk:"resource"`
	Role           types.String `tfsdk:"role"`
}

type userRoleAssignmentValues struct {
	OrganizationID types.String
	DirectoryID    types.String
	AccountID      types.String
	Resource       types.String
	Role           types.String
}

func NewUserRoleAssignmentResource() resource.Resource {
	return &userRoleAssignmentResource{}
}

func (r *userRoleAssignmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_user_role_assignment"
}

func (r *userRoleAssignmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	requiredReplace := func(description string) schema.StringAttribute {
		return schema.StringAttribute{
			Description: description,
			Required:    true,
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(),
			},
		}
	}
	resourceAttribute := requiredReplace("Application resource ARI beginning with ari:cloud:, such as ari:cloud:jira::site/<site-id>.")
	resourceAttribute.MarkdownDescription = fmt.Sprintf("Application resource ARI beginning with %s, such as %s.", markdownARI("ari:cloud:"), markdownARI("ari:cloud:jira::site/<site-id>"))
	roleAttribute := requiredReplace("Atlassian application role. Organization-level atlassian/org-admin is not supported by this resource.")
	roleAttribute.MarkdownDescription = "Atlassian application role. Organization-level `atlassian/org-admin` is not supported by this resource."

	resp.Schema = schema.Schema{
		Description: "Assigns an application resource and platform role directly to an organization user.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Description: "Composite assignment identifier.", Computed: true},
			"organization_id": requiredReplace("Atlassian organization ID used in the Organization API path."),
			"directory_id":    requiredReplace("Directory ID used to read the assignment."),
			"account_id":      requiredReplace("Atlassian account ID."),
			"resource":        resourceAttribute,
			"role":            roleAttribute,
		},
	}
}

func (r *userRoleAssignmentResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	requiredString := func(description string) identityschema.StringAttribute {
		return identityschema.StringAttribute{
			Description:       description,
			RequiredForImport: true,
		}
	}
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"organization_id": requiredString("Atlassian organization ID used in the Organization API path."),
			"directory_id":    requiredString("Directory ID used to read the assignment."),
			"account_id":      requiredString("Atlassian account ID."),
			"resource":        requiredString("Application resource ARI."),
			"role":            requiredString("Atlassian application role."),
		},
	}
}

func (r *userRoleAssignmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *userRoleAssignmentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config userRoleAssignmentResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateUserRoleAssignmentValues(userRoleAssignmentValues{
		OrganizationID: config.OrganizationID,
		DirectoryID:    config.DirectoryID,
		AccountID:      config.AccountID,
		Resource:       config.Resource,
		Role:           config.Role,
	})...)
}

func validateUserRoleAssignmentValues(values userRoleAssignmentValues) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	attributes := []struct {
		name  string
		value types.String
	}{
		{"organization_id", values.OrganizationID},
		{"directory_id", values.DirectoryID},
		{"account_id", values.AccountID},
		{"resource", values.Resource},
		{"role", values.Role},
	}
	for _, item := range attributes {
		if !item.value.IsNull() && !item.value.IsUnknown() && strings.TrimSpace(item.value.ValueString()) == "" {
			diagnostics.AddError("Invalid user role assignment", fmt.Sprintf("%s must not be empty.", item.name))
		}
	}
	if !values.Resource.IsNull() && !values.Resource.IsUnknown() && !strings.HasPrefix(values.Resource.ValueString(), "ari:cloud:") {
		diagnostics.AddError("Invalid user role assignment", "resource must be an Atlassian cloud resource identifier beginning with ari:cloud:.")
	}
	if !values.Role.IsNull() && !values.Role.IsUnknown() {
		allowedRoles := stringSet("atlassian/user", "atlassian/user-access-admin", "atlassian/admin", "atlassian/guest", "atlassian/contributor", "atlassian/customer", "atlassian/basic", "atlassian/stakeholder", "atlassian/site-admin")
		if _, allowed := allowedRoles[values.Role.ValueString()]; !allowed {
			diagnostics.AddError("Invalid user role assignment", fmt.Sprintf("%q is not a supported application role.", values.Role.ValueString()))
		}
	}
	return diagnostics
}

func (r *userRoleAssignmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userRoleAssignmentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Role.ValueString() == "atlassian/org-admin" {
		resp.Diagnostics.AddError("Unsupported organization-level role", "This resource manages application role assignments and does not support atlassian/org-admin.")
		return
	}
	if err := r.client.AssignUserRole(ctx, plan.OrganizationID.ValueString(), plan.AccountID.ValueString(), plan.Resource.ValueString(), plan.Role.ValueString()); err != nil {
		if mutationOutcomeMayBeAmbiguous(err) {
			present, readErr := r.client.HasDirectUserRole(ctx, plan.OrganizationID.ValueString(), plan.DirectoryID.ValueString(), plan.AccountID.ValueString(), plan.Resource.ValueString(), plan.Role.ValueString())
			if readErr == nil && present {
				plan.ID = types.StringValue(assignmentID(plan))
				resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
				resp.Diagnostics.Append(resp.Identity.Set(ctx, assignmentIdentity(plan))...)
				return
			}
			if readErr != nil {
				resp.Diagnostics.AddError("Unable to verify organization user role assignment", fmt.Sprintf("The assignment response was ambiguous and reading the resulting state also failed: %s", readErr))
			}
		}
		resp.Diagnostics.AddError("Unable to assign organization user role", err.Error())
		return
	}
	plan.ID = types.StringValue(assignmentID(plan))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, assignmentIdentity(plan))...)
}

func (r *userRoleAssignmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userRoleAssignmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	present, err := r.client.HasDirectUserRole(ctx, state.OrganizationID.ValueString(), state.DirectoryID.ValueString(), state.AccountID.ValueString(), state.Resource.ValueString(), state.Role.ValueString())
	if err != nil {
		var httpErr *admin.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read organization user role assignment", err.Error())
		return
	}
	if !present {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.Identity.Set(ctx, assignmentIdentity(state))...)
}

func (r *userRoleAssignmentResource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {
	// All configurable attributes require replacement.
}

func (r *userRoleAssignmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userRoleAssignmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.RevokeUserRole(ctx, state.OrganizationID.ValueString(), state.AccountID.ValueString(), state.Resource.ValueString(), state.Role.ValueString()); err != nil {
		var httpErr *admin.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return
		}
		if mutationOutcomeMayBeAmbiguous(err) {
			present, readErr := r.client.HasDirectUserRole(ctx, state.OrganizationID.ValueString(), state.DirectoryID.ValueString(), state.AccountID.ValueString(), state.Resource.ValueString(), state.Role.ValueString())
			if readErr == nil && !present {
				return
			}
			if readErr != nil {
				resp.Diagnostics.AddError("Unable to verify organization user role revocation", fmt.Sprintf("The revocation response was ambiguous and reading the resulting state also failed: %s", readErr))
			}
		}
		resp.Diagnostics.AddError("Unable to revoke organization user role", err.Error())
	}
}

func (r *userRoleAssignmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var identity userRoleAssignmentResourceIdentityModel
	switch {
	case req.ID != "":
		parsed, err := parseAssignmentImportID(req.ID)
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

	resp.Diagnostics.Append(validateUserRoleAssignmentValues(userRoleAssignmentValues(identity))...)
	if resp.Diagnostics.HasError() {
		return
	}
	state := userRoleAssignmentResourceModel{
		OrganizationID: identity.OrganizationID,
		DirectoryID:    identity.DirectoryID,
		AccountID:      identity.AccountID,
		Resource:       identity.Resource,
		Role:           identity.Role,
	}
	state.ID = types.StringValue(assignmentID(state))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
}

func parseAssignmentImportID(id string) (userRoleAssignmentResourceIdentityModel, error) {
	parts := strings.Split(id, ",")
	if len(parts) != 5 {
		return userRoleAssignmentResourceIdentityModel{}, errors.New("expected organization_id,directory_id,account_id,resource,role")
	}
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if parts[index] == "" {
			return userRoleAssignmentResourceIdentityModel{}, fmt.Errorf("import identifier component %d must not be empty", index+1)
		}
	}
	return userRoleAssignmentResourceIdentityModel{
		OrganizationID: types.StringValue(parts[0]),
		DirectoryID:    types.StringValue(parts[1]),
		AccountID:      types.StringValue(parts[2]),
		Resource:       types.StringValue(parts[3]),
		Role:           types.StringValue(parts[4]),
	}, nil
}

func assignmentIdentity(model userRoleAssignmentResourceModel) *userRoleAssignmentResourceIdentityModel {
	return &userRoleAssignmentResourceIdentityModel{
		OrganizationID: model.OrganizationID,
		DirectoryID:    model.DirectoryID,
		AccountID:      model.AccountID,
		Resource:       model.Resource,
		Role:           model.Role,
	}
}

func assignmentID(model userRoleAssignmentResourceModel) string {
	return strings.Join([]string{
		model.OrganizationID.ValueString(),
		model.DirectoryID.ValueString(),
		model.AccountID.ValueString(),
		model.Resource.ValueString(),
		model.Role.ValueString(),
	}, ",")
}

func mutationOutcomeMayBeAmbiguous(err error) bool {
	var httpErr *admin.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode >= http.StatusInternalServerError
	}
	return true
}
