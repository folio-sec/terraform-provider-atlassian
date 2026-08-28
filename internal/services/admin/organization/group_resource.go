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

var _ resource.Resource = &groupResource{}
var _ resource.ResourceWithIdentity = &groupResource{}
var _ resource.ResourceWithImportState = &groupResource{}
var _ resource.ResourceWithValidateConfig = &groupResource{}

const (
	defaultGroupResolutionTimeout = 35 * time.Second
	defaultGroupResolutionInitial = 500 * time.Millisecond
	defaultGroupResolutionMaximum = 3 * time.Second
)

type organizationGroupClient interface {
	CreateGroup(context.Context, string, string, string, *string) error
	GetGroup(context.Context, string, string, string) (organizationclient.Group, error)
	SearchGroups(context.Context, string, string, organizationclient.SearchGroupsRequest) ([]organizationclient.Group, error)
	DeleteGroup(context.Context, string, string, string) error
}

type groupResource struct {
	client                 organizationGroupClient
	resolutionTimeout      time.Duration
	resolutionInitialDelay time.Duration
	resolutionMaximumDelay time.Duration
}

type groupResourceModel struct {
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

type groupResourceIdentityModel struct {
	OrganizationID types.String `tfsdk:"organization_id"`
	DirectoryID    types.String `tfsdk:"directory_id"`
	GroupID        types.String `tfsdk:"group_id"`
}

func NewGroupResource() resource.Resource {
	return &groupResource{
		resolutionTimeout:      defaultGroupResolutionTimeout,
		resolutionInitialDelay: defaultGroupResolutionInitial,
		resolutionMaximumDelay: defaultGroupResolutionMaximum,
	}
}

func (r *groupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_group"
}

func (r *groupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	requiresReplace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	computedBool := func(description string) schema.BoolAttribute {
		return schema.BoolAttribute{Description: description, Computed: true}
	}
	resp.Schema = schema.Schema{
		Description: "Manages an Atlassian organization group. Atlassian does not provide an update operation, so changing the name or description replaces the group. Group creation is eventually consistent; the provider waits up to 35 seconds to resolve the created group ID. Importing a group makes Terraform responsible for deleting it; use the organization group data source when only lookup is needed. Built-in and product access groups may be managed by Atlassian and non-deletable.",
		Attributes: map[string]schema.Attribute{
			"id":       schema.StringAttribute{Description: "Terraform resource ID, equal to the Atlassian group ID.", Computed: true},
			"group_id": schema.StringAttribute{Description: "Unique Atlassian group ID.", Computed: true},
			"organization_id": schema.StringAttribute{
				Description:   "Atlassian organization ID used in the Organization API path.",
				Required:      true,
				PlanModifiers: requiresReplace,
			},
			"directory_id": schema.StringAttribute{
				Description:   "Directory containing the group.",
				Required:      true,
				PlanModifiers: requiresReplace,
			},
			"name": schema.StringAttribute{
				Description:   "Group name. Changing it replaces the group because the API has no update operation.",
				Required:      true,
				PlanModifiers: requiresReplace,
			},
			"description": schema.StringAttribute{
				Description:   "Group description. Changing it replaces the group because the API has no update operation.",
				Optional:      true,
				PlanModifiers: requiresReplace,
			},
			"external_synced": computedBool("Whether the group is synchronized from an identity provider."),
			"managed_by":      schema.StringAttribute{Description: "How the group is managed.", Computed: true},
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

func (r *groupResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	requiredString := func(description string) identityschema.StringAttribute {
		return identityschema.StringAttribute{Description: description, RequiredForImport: true}
	}
	resp.IdentitySchema = identityschema.Schema{Attributes: map[string]identityschema.Attribute{
		"organization_id": requiredString("Atlassian organization ID used in the Organization API path."),
		"directory_id":    requiredString("Directory containing the group."),
		"group_id":        requiredString("Unique Atlassian group ID."),
	}}
}

func (r *groupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *groupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config groupResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateGroupValues(config.OrganizationID, config.DirectoryID, types.StringNull(), config.Name)...)
}

func validateGroupValues(organizationID, directoryID, groupID, name types.String) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	values := []struct {
		name  string
		value types.String
	}{
		{"organization_id", organizationID},
		{"directory_id", directoryID},
		{"group_id", groupID},
		{"name", name},
	}
	for _, item := range values {
		if !item.value.IsNull() && !item.value.IsUnknown() && strings.TrimSpace(item.value.ValueString()) == "" {
			diagnostics.AddError("Invalid organization group", fmt.Sprintf("%s must not be empty.", item.name))
		}
	}
	return diagnostics
}

func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if existing, err := r.resolveExactGroup(ctx, plan.OrganizationID.ValueString(), plan.DirectoryID.ValueString(), plan.Name.ValueString()); err == nil {
		resp.Diagnostics.AddError("Organization group already exists", fmt.Sprintf("A group named %q already exists with ID %q. Import it with %s,%s,%s instead of creating it.", plan.Name.ValueString(), existing.ID, plan.OrganizationID.ValueString(), plan.DirectoryID.ValueString(), existing.ID))
		return
	} else if !errors.Is(err, errNoExactGroup) {
		resp.Diagnostics.AddError("Unable to check for an existing organization group", err.Error())
		return
	}

	var description *string
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		value := plan.Description.ValueString()
		description = &value
	}
	createErr := r.client.CreateGroup(ctx, plan.OrganizationID.ValueString(), plan.DirectoryID.ValueString(), plan.Name.ValueString(), description)
	if createErr != nil {
		if !mutationOutcomeMayBeAmbiguous(createErr) {
			resp.Diagnostics.AddError("Unable to create organization group", createErr.Error())
			return
		}
		group, resolveErr := r.waitForExactGroup(ctx, plan.OrganizationID.ValueString(), plan.DirectoryID.ValueString(), plan.Name.ValueString())
		if resolveErr == nil {
			setGroupState(ctx, &plan, group, &resp.Diagnostics)
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			resp.Diagnostics.Append(resp.Identity.Set(ctx, groupIdentity(plan))...)
			return
		}
		resp.Diagnostics.AddError("Unable to verify organization group creation", fmt.Sprintf("The create response was ambiguous and exact-name verification failed: %s. Original create error: %s. The group may exist and require import.", resolveErr, createErr))
		return
	}
	group, resolveErr := r.waitForExactGroup(ctx, plan.OrganizationID.ValueString(), plan.DirectoryID.ValueString(), plan.Name.ValueString())
	if resolveErr != nil {
		resp.Diagnostics.AddError("Unable to resolve created organization group", fmt.Sprintf("%s. Atlassian accepted the create request, so the group may exist and require import.", resolveErr))
		return
	}
	setGroupState(ctx, &plan, group, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, groupIdentity(plan))...)
}

var errNoExactGroup = errors.New("no exact-name group match")
var errMultipleExactGroups = errors.New("multiple exact-name group matches")

func (r *groupResource) resolveExactGroup(ctx context.Context, organizationID, directoryID, name string) (organizationclient.Group, error) {
	groups, err := r.client.SearchGroups(ctx, organizationID, directoryID, organizationclient.SearchGroupsRequest{GroupNames: []string{name}})
	if err != nil {
		return organizationclient.Group{}, fmt.Errorf("search for exact group name %q: %w", name, err)
	}
	matches := make([]organizationclient.Group, 0, len(groups))
	for _, group := range groups {
		if group.Name == nil || !strings.EqualFold(*group.Name, name) {
			continue
		}
		if group.DirectoryID != nil && *group.DirectoryID != directoryID {
			continue
		}
		matches = append(matches, group)
	}
	switch len(matches) {
	case 0:
		return organizationclient.Group{}, fmt.Errorf("%w for %q in directory %q", errNoExactGroup, name, directoryID)
	case 1:
		return matches[0], nil
	default:
		return organizationclient.Group{}, fmt.Errorf("%w: search for %q in directory %q returned %d groups; refusing to select one", errMultipleExactGroups, name, directoryID, len(matches))
	}
}

func (r *groupResource) waitForExactGroup(ctx context.Context, organizationID, directoryID, name string) (organizationclient.Group, error) {
	timeout := r.resolutionTimeout
	if timeout <= 0 {
		timeout = defaultGroupResolutionTimeout
	}
	interval := r.resolutionInitialDelay
	if interval <= 0 {
		interval = defaultGroupResolutionInitial
	}
	maximumInterval := r.resolutionMaximumDelay
	if maximumInterval <= 0 {
		maximumInterval = defaultGroupResolutionMaximum
	}

	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastErr error
	for {
		group, err := r.resolveExactGroup(pollCtx, organizationID, directoryID, name)
		if err == nil {
			return group, nil
		}
		lastErr = err
		if errors.Is(err, errMultipleExactGroups) || (!errors.Is(err, errNoExactGroup) && !readOutcomeMayBeTransient(err)) {
			return organizationclient.Group{}, err
		}

		timer := time.NewTimer(interval)
		select {
		case <-pollCtx.Done():
			timer.Stop()
			return organizationclient.Group{}, fmt.Errorf("timed out after %s resolving exact group name: %w", timeout, lastErr)
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

func (r *groupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	group, err := r.client.GetGroup(ctx, state.OrganizationID.ValueString(), state.DirectoryID.ValueString(), state.GroupID.ValueString())
	if err != nil {
		var httpErr *admin.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read organization group", err.Error())
		return
	}
	setGroupState(ctx, &state, group, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, groupIdentity(state))...)
}

func (r *groupResource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {
	// All configurable attributes require replacement because the API has no update operation.
}

func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if groupIsKnownNonDeletable(state.ManagementAccess) {
		resp.Diagnostics.AddError("Organization group is not deletable", "Atlassian reports management_access.deletable as false. Built-in groups such as site-admins, administrators, and product access groups may be managed by Atlassian and cannot be deleted. Remove the resource from Terraform state if the group should remain.")
		return
	}
	err := r.client.DeleteGroup(ctx, state.OrganizationID.ValueString(), state.DirectoryID.ValueString(), state.GroupID.ValueString())
	if err == nil {
		return
	}
	var httpErr *admin.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
		return
	}
	if mutationOutcomeMayBeAmbiguous(err) {
		_, readErr := r.client.GetGroup(ctx, state.OrganizationID.ValueString(), state.DirectoryID.ValueString(), state.GroupID.ValueString())
		if errors.As(readErr, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return
		}
		if readErr != nil {
			resp.Diagnostics.AddError("Unable to verify organization group deletion", fmt.Sprintf("The delete response was ambiguous and reading the group also failed: %s. Original delete error: %s", readErr, err))
			return
		}
	}
	if errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusForbidden || httpErr.StatusCode == http.StatusBadRequest || httpErr.StatusCode == http.StatusConflict) {
		resp.Diagnostics.AddError("Atlassian refused to delete organization group", fmt.Sprintf("The group may be built-in or managed by Atlassian with management_access.deletable set to false: %s", err))
		return
	}
	resp.Diagnostics.AddError("Unable to delete organization group", err.Error())
}

func (r *groupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var identity groupResourceIdentityModel
	switch {
	case req.ID != "":
		parsed, err := parseGroupImportID(req.ID)
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
	resp.Diagnostics.Append(validateGroupValues(identity.OrganizationID, identity.DirectoryID, identity.GroupID, types.StringNull())...)
	if resp.Diagnostics.HasError() {
		return
	}
	state := groupResourceModel{
		ID:               identity.GroupID,
		OrganizationID:   identity.OrganizationID,
		DirectoryID:      identity.DirectoryID,
		GroupID:          identity.GroupID,
		Name:             types.StringUnknown(),
		Description:      types.StringUnknown(),
		ExternalSynced:   types.BoolNull(),
		ManagedBy:        types.StringNull(),
		ManagementAccess: types.ObjectNull(managementAccessAttributeTypes()),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &identity)...)
}

func parseGroupImportID(id string) (groupResourceIdentityModel, error) {
	parts := strings.Split(id, ",")
	if len(parts) != 3 {
		return groupResourceIdentityModel{}, errors.New("expected organization_id,directory_id,group_id")
	}
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if parts[index] == "" {
			return groupResourceIdentityModel{}, fmt.Errorf("import identifier component %d must not be empty", index+1)
		}
	}
	return groupResourceIdentityModel{
		OrganizationID: types.StringValue(parts[0]),
		DirectoryID:    types.StringValue(parts[1]),
		GroupID:        types.StringValue(parts[2]),
	}, nil
}

func setGroupState(ctx context.Context, state *groupResourceModel, group organizationclient.Group, diagnostics *diag.Diagnostics) {
	// description is optional-only so removing it from configuration produces
	// a replacement plan. Keep it null when unconfigured; an unknown value is
	// used during import and by the lookup data source to request the API value.
	readDescription := !state.Description.IsNull()
	state.ID = types.StringValue(group.ID)
	state.GroupID = types.StringValue(group.ID)
	state.Name = nullableStringValue(group.Name)
	if readDescription {
		state.Description = nullableStringValue(group.Description)
	} else {
		state.Description = types.StringNull()
	}
	state.ExternalSynced = nullableBoolValue(group.ExternalSynced)
	state.ManagedBy = nullableStringValue(group.ManagedBy)
	state.ManagementAccess = types.ObjectNull(managementAccessAttributeTypes())
	if group.ManagementAccess != nil {
		value, objectDiagnostics := types.ObjectValueFrom(ctx, managementAccessAttributeTypes(), managementAccessResultModel{
			Deletable:  nullableBoolValue(group.ManagementAccess.Deletable),
			Modifiable: nullableBoolValue(group.ManagementAccess.Modifiable),
			Readable:   nullableBoolValue(group.ManagementAccess.Readable),
		})
		diagnostics.Append(objectDiagnostics...)
		state.ManagementAccess = value
	}
}

func groupIsKnownNonDeletable(access types.Object) bool {
	if access.IsNull() || access.IsUnknown() {
		return false
	}
	value, ok := access.Attributes()["deletable"].(types.Bool)
	return ok && !value.IsNull() && !value.IsUnknown() && !value.ValueBool()
}

func groupIdentity(model groupResourceModel) *groupResourceIdentityModel {
	return &groupResourceIdentityModel{
		OrganizationID: model.OrganizationID,
		DirectoryID:    model.DirectoryID,
		GroupID:        model.GroupID,
	}
}
