package space

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence"
	v2gen "github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence/v2/generated"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var _ resource.Resource = &spaceResource{}
var _ resource.ResourceWithIdentity = &spaceResource{}
var _ resource.ResourceWithImportState = &spaceResource{}
var _ resource.ResourceWithValidateConfig = &spaceResource{}

type spaceResource struct {
	client *Service
}

// spaceResourceModel is the Terraform-facing model. template_key,
// copy_space_access_configuration, create_private_space and role_assignments
// are accepted by createSpace but never returned by any read operation
// (plans/confluence-space.md, "Write-only create inputs"). Terraform cannot
// express "write-only at create" for an attribute, so Read leaves these four
// exactly as they were in the prior state/plan rather than nulling them.
type spaceResourceModel struct {
	ID                           types.String `tfsdk:"id"`
	Key                          types.String `tfsdk:"key"`
	Alias                        types.String `tfsdk:"alias"`
	Name                         types.String `tfsdk:"name"`
	Description                  types.Object `tfsdk:"description"`
	Type                         types.String `tfsdk:"type"`
	Status                       types.String `tfsdk:"status"`
	HomepageID                   types.String `tfsdk:"homepage_id"`
	TemplateKey                  types.String `tfsdk:"template_key"`
	CopySpaceAccessConfiguration types.String `tfsdk:"copy_space_access_configuration"`
	CreatePrivateSpace           types.Bool   `tfsdk:"create_private_space"`
	RoleAssignments              types.Set    `tfsdk:"role_assignments"`
	AuthorID                     types.String `tfsdk:"author_id"`
	SpaceOwnerID                 types.String `tfsdk:"space_owner_id"`
	CreatedAt                    types.String `tfsdk:"created_at"`
	CurrentActiveAlias           types.String `tfsdk:"current_active_alias"`
}

type roleAssignmentResourceModel struct {
	Principal types.Object `tfsdk:"principal"`
	RoleID    types.String `tfsdk:"role_id"`
}

type principalResourceModel struct {
	PrincipalType types.String `tfsdk:"principal_type"`
	PrincipalID   types.String `tfsdk:"principal_id"`
}

type spaceResourceIdentityModel struct {
	ID types.String `tfsdk:"id"`
}

// NewSpaceResource returns the atlassian_confluence_space resource.
func NewSpaceResource() resource.Resource {
	return &spaceResource{}
}

func (r *spaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_confluence_space"
}

// spaceResourceDescription is the registry page body for this resource. It
// lives outside Schema so the schema itself stays readable.
const spaceResourceDescription = "Manages a Confluence Cloud space.\n\n" +
	"## Required OAuth scopes\n\n" +
	"When the provider authenticates as a service account, its credential must carry the scopes for the " +
	"operations this resource actually performs. A scope is checked when an operation runs, so a resource " +
	"that is only ever read needs the first bullet alone.\n\n" +
	"- Read: `read:space:confluence`\n" +
	"- Create: `write:space:confluence`\n" +
	"- Update: `read:space-details:confluence`, `write:space:confluence`, `write:space.permission:confluence`\n" +
	"- Delete: `delete:space:confluence`, `read:content.metadata:confluence`\n\n" +
	"Managing a space through its whole lifecycle therefore needs all six. Creating one also requires a " +
	"tenant with Role-Based Access Control enabled, which is what the v2 createSpace operation is gated " +
	"behind.\n\n" +
	"Those six are the granular scope names, and they are all a client credentials credential needs. A " +
	"service account API token needs more: update, delete and the delete completion check run against the " +
	"v1 REST API, which an API token reaches only when it also carries the classic scope names. Its scopes " +
	"are fixed when it is created, so grant the whole set then:\n\n" +
	"- `read:space:confluence`\n" +
	"- `write:space:confluence`\n" +
	"- `delete:space:confluence`\n" +
	"- `read:space-details:confluence`\n" +
	"- `write:space.permission:confluence`\n" +
	"- `read:content.metadata:confluence`\n" +
	"- `write:confluence-space` (classic, for v1 update and delete)\n" +
	"- `read:confluence-space.summary` (classic, for the v1 delete completion check)\n\n" +
	"> **Deletion is permanent**\n" +
	"> Deleting this resource deletes the space outright; it does not pass through the trash and cannot be undone.\n\n" +
	"> **Write-only at create**\n" +
	"> `template_key`, `copy_space_access_configuration`, `create_private_space` and `role_assignments` are sent " +
	"only when the space is created. No read operation returns them, so they are never refreshed from the API " +
	"and changing them always replaces the space."

func (r *spaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	requiresReplaceString := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	preserveString := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	// UseStateForUnknown on the computed-only attributes: without it every
	// update plan shows author_id, space_owner_id, created_at and
	// current_active_alias as "known after apply", which was observed during
	// the live acceptance run and makes a one-attribute rename unreadable.
	// None of them can change as a result of an update this resource issues,
	// and Read still refreshes them if they change outside Terraform.
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Description: description, Computed: true, PlanModifiers: preserveString}
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: spaceResourceDescription,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "Numeric-string ID of the space.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"key": schema.StringAttribute{
				Description:   "Key of the space. Exactly one of key or alias is required. Immutable: changing it replaces the space.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: append(append([]planmodifier.String{}, requiresReplaceString...), preserveString...),
			},
			"alias": schema.StringAttribute{
				Description:   "Alias for the space in page URLs, used as the space's identifier when key is not set. Exactly one of key or alias is required. Immutable: changing it replaces the space. Maximum 255 alphanumeric characters.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: append(append([]planmodifier.String{}, requiresReplaceString...), preserveString...),
			},
			"name": schema.StringAttribute{
				Description: "Name of the space. Maximum 200 characters.",
				Required:    true,
			},
			"description": schema.SingleNestedAttribute{
				// Optional+Computed with UseStateForUnknown, not just Optional:
				// removing this block from configuration cannot clear the
				// description through v1 updateSpace (unverified against the
				// live API; see updateRequestFromDiff), so Read always observes
				// the description still present. Modeling it as plain Optional
				// would make that a permanent "inconsistent result after apply"
				// error; Computed lets an omitted block keep its prior state
				// value and produce no diff instead.
				Description: "Space description. Only the plain representation is accepted on write. Removing this " +
					"block from configuration does not clear the description on the API; the prior value is kept.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.UseStateForUnknown(),
				},
				Attributes: map[string]schema.Attribute{
					"value": schema.StringAttribute{Description: "Description text.", Required: true},
					"representation": schema.StringAttribute{
						Description: "Representation of value. Only plain is accepted.",
						Optional:    true,
						Computed:    true,
						Default:     stringdefault.StaticString("plain"),
					},
				},
			},
			"type": schema.StringAttribute{
				Description:   "Type of the space. Server-assigned at creation; updatable afterward, though valid transitions are undocumented.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: preserveString,
			},
			"status": schema.StringAttribute{
				Description:   "Status of the space: current or archived. Updating this attribute is how a space is archived or unarchived. trashed cannot be set from configuration.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: preserveString,
			},
			"homepage_id": schema.StringAttribute{
				Description:   "ID of the space's homepage. Server-assigned at creation; updatable afterward.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: preserveString,
			},
			"template_key": schema.StringAttribute{
				Description:   "Key of the template used to create the space. Write-only at create; see the resource description.",
				Optional:      true,
				PlanModifiers: requiresReplaceString,
			},
			"copy_space_access_configuration": schema.StringAttribute{
				Description:   "ID of the space to copy the access configuration from. Write-only at create; see the resource description.",
				Optional:      true,
				PlanModifiers: requiresReplaceString,
			},
			"create_private_space": schema.BoolAttribute{
				Description:   "Whether to create the space as private. Write-only at create; see the resource description.",
				Optional:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"role_assignments": schema.SetNestedAttribute{
				Description:   "Role assignments seeded at creation. Write-only at create; see the resource description.",
				Optional:      true,
				PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplace()},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"principal": schema.SingleNestedAttribute{
						Description: "The principal the role is assigned to.",
						Required:    true,
						Attributes: map[string]schema.Attribute{
							"principal_type": schema.StringAttribute{Description: "One of USER, GROUP, ACCESS_CLASS.", Required: true},
							"principal_id":   schema.StringAttribute{Description: "ID of the principal.", Required: true},
						},
					},
					"role_id": schema.StringAttribute{Description: "Role to assign to the principal.", Required: true},
				}},
			},
			"author_id":            computedString("Account ID of the user who created the space."),
			"space_owner_id":       computedString("Account ID of the user who owns the space. Absent on some responses."),
			"created_at":           computedString("Date and time the space was created, RFC 3339."),
			"current_active_alias": computedString("Currently active alias for the space."),
		},
	}
}

func (r *spaceResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"id": identityschema.StringAttribute{Description: "Numeric-string ID of the space.", RequiredForImport: true},
		},
	}
}

func (r *spaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

const spaceStatusInvalid = "Invalid Confluence space configuration"

var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,255}$`)

func (r *spaceResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config spaceResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(validateSpaceIdentity(config)...)
	resp.Diagnostics.Append(validateSpaceConfigStatus(config)...)
	resp.Diagnostics.Append(validateSpaceConfigDescription(ctx, config)...)
	resp.Diagnostics.Append(validateSpaceConfigRoleAssignments(ctx, config)...)
	resp.Diagnostics.Append(validateSpaceConfigCopySource(config)...)
}

// validateSpaceConfigCopySource rejects a copy_space_access_configuration that
// is not a space id. The attribute is a string because space ids are opaque
// strings everywhere in this provider, but createSpace types it as an integer,
// so it is converted just before the request. Catching a bad value here keeps
// that conversion from failing during apply, where the error would arrive
// after the plan was approved.
func validateSpaceConfigCopySource(config spaceResourceModel) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if !setNonBlank(config.CopySpaceAccessConfiguration) {
		return diagnostics
	}
	value := strings.TrimSpace(config.CopySpaceAccessConfiguration.ValueString())
	if !numericIDPattern.MatchString(value) {
		diagnostics.AddError(spaceStatusInvalid,
			fmt.Sprintf("copy_space_access_configuration must be a numeric space id, got %q.", value))
	}
	return diagnostics
}

// validateSpaceIdentity checks the key/alias exactly-one-of and alias's shape.
func validateSpaceIdentity(config spaceResourceModel) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	diagnostics.Append(validateNonEmpty(spaceStatusInvalid, namedValue{"key", config.Key}, namedValue{"alias", config.Alias})...)
	keySet := setNonBlank(config.Key)
	aliasSet := setNonBlank(config.Alias)
	switch {
	case keySet && aliasSet:
		diagnostics.AddError(spaceStatusInvalid, "key and alias are mutually exclusive; set exactly one.")
	case !keySet && !aliasSet && !config.Key.IsUnknown() && !config.Alias.IsUnknown():
		diagnostics.AddError(spaceStatusInvalid, "exactly one of key or alias must be set.")
	}
	if aliasSet && !aliasPattern.MatchString(config.Alias.ValueString()) {
		diagnostics.AddError(spaceStatusInvalid, "alias must be 1-255 alphanumeric characters.")
	}
	return diagnostics
}

// validateSpaceConfigStatus rejects any status other than current or archived.
func validateSpaceConfigStatus(config spaceResourceModel) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if knownString(config.Status) && strings.TrimSpace(config.Status.ValueString()) != "" {
		status := config.Status.ValueString()
		if status != "current" && status != "archived" {
			diagnostics.AddError(spaceStatusInvalid, fmt.Sprintf("status must be current or archived; trashed cannot be set from configuration, got %q.", status))
		}
	}
	return diagnostics
}

// validateSpaceConfigDescription rejects a representation other than plain.
func validateSpaceConfigDescription(ctx context.Context, config spaceResourceModel) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if config.Description.IsNull() || config.Description.IsUnknown() {
		return diagnostics
	}
	var description descriptionModel
	diagnostics.Append(config.Description.As(ctx, &description, basetypes.ObjectAsOptions{})...)
	if knownString(description.Representation) && strings.TrimSpace(description.Representation.ValueString()) != "" {
		if description.Representation.ValueString() != "plain" {
			diagnostics.AddError(spaceStatusInvalid, fmt.Sprintf("description.representation must be plain, got %q.", description.Representation.ValueString()))
		}
	}
	return diagnostics
}

// validateSpaceConfigRoleAssignments rejects an unrecognized principal_type.
func validateSpaceConfigRoleAssignments(ctx context.Context, config spaceResourceModel) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if config.RoleAssignments.IsNull() || config.RoleAssignments.IsUnknown() {
		return diagnostics
	}
	var assignments []roleAssignmentResourceModel
	diagnostics.Append(config.RoleAssignments.ElementsAs(ctx, &assignments, false)...)
	for _, assignment := range assignments {
		if assignment.Principal.IsNull() || assignment.Principal.IsUnknown() {
			continue
		}
		var principal principalResourceModel
		diagnostics.Append(assignment.Principal.As(ctx, &principal, basetypes.ObjectAsOptions{})...)
		if knownString(principal.PrincipalType) && strings.TrimSpace(principal.PrincipalType.ValueString()) != "" {
			if !v2gen.PrincipalType(principal.PrincipalType.ValueString()).Valid() {
				diagnostics.AddError(spaceStatusInvalid, fmt.Sprintf("role_assignments principal_type must be USER, GROUP, or ACCESS_CLASS, got %q.", principal.PrincipalType.ValueString()))
			}
		}
	}
	return diagnostics
}

// isAmbiguousCreateFailure reports whether createSpace's failure leaves it
// unknown whether the space was created. The answer only changes how the
// failure is reported -- nothing is looked up or adopted either way -- but an
// operator told "this may have been created" checks, and one told it failed
// does not, so the classification has to be right:
//
//   - A definite HTTP status: the API told us what happened.
//   - ErrRequestNotSent: the request never left the client, so nothing can
//     have been created. A local conversion failure such as an unparseable
//     copy_space_access_configuration lands here; without this check a plain
//     configuration typo would send the resource hunting for a space to adopt.
//
// What remains is a transport-level failure that never resolved to a status.
func isAmbiguousCreateFailure(err error) bool {
	if errors.Is(err, ErrRequestNotSent) {
		return false
	}
	var httpErr *confluence.HTTPError
	if !errors.As(err, &httpErr) {
		// No status at all: the request may have been applied before the
		// transport failed.
		return true
	}
	// A 4xx is Atlassian declining the request, so nothing was created. A 5xx
	// or a gateway failure can arrive after Confluence has already committed
	// the space, so the outcome is not settled by the status alone.
	return httpErr.StatusCode >= http.StatusInternalServerError || httpErr.GatewayRouting
}

func (r *spaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan spaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	description, diagnostics := descriptionFromModel(ctx, plan.Description)
	resp.Diagnostics.Append(diagnostics...)
	roleAssignments, diagnostics := roleAssignmentsFromModel(ctx, plan.RoleAssignments)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq := CreateSpaceRequest{
		Key:                          plan.Key.ValueString(),
		Alias:                        plan.Alias.ValueString(),
		Name:                         plan.Name.ValueString(),
		Description:                  description,
		TemplateKey:                  plan.TemplateKey.ValueString(),
		CopySpaceAccessConfiguration: plan.CopySpaceAccessConfiguration.ValueString(),
		RoleAssignments:              roleAssignments,
	}
	if !plan.CreatePrivateSpace.IsNull() && !plan.CreatePrivateSpace.IsUnknown() {
		value := plan.CreatePrivateSpace.ValueBool()
		createReq.CreatePrivateSpace = &value
	}

	result, err := r.client.CreateSpace(ctx, createReq)
	if err != nil {
		summary := "Unable to create Confluence space"
		detail := err.Error()
		if isAmbiguousCreateFailure(err) {
			// The request may or may not have reached Atlassian, and without
			// an id there is nothing to record. Deliberately no lookup-and-
			// adopt: a space carrying this key proves only that the key is
			// taken, not that this request created it, and adopting the wrong
			// one deletes it permanently on the next destroy.
			summary = "Unable to confirm Confluence space creation"
			detail = fmt.Sprintf("%s. The space may or may not have been created. Check whether a space with key %q exists; if it does and it is yours, adopt it with: terraform import atlassian_confluence_space.<name> <its numeric id>",
				err, createKeyOrAlias(plan))
		}
		resp.Diagnostics.AddError(summary, detail)
		return
	}

	r.finishCreate(ctx, plan, result.ID, result.Key, resp)
}

// createKeyOrAlias names whichever identifier the configuration supplied, for
// an error that has to describe a space it could not read back.
func createKeyOrAlias(plan spaceResourceModel) string {
	if key := plan.Key.ValueString(); key != "" {
		return key
	}
	return plan.Alias.ValueString()
}

// finishCreate re-reads the space before writing state: gate 2
// (plans/confluence-space-verification.json) showed spaceOwnerId is absent
// from the create response and present only on read.
//
// Every failure below happens after the space exists, and each one records
// the id in state before reporting. Terraform persists state even alongside
// an error diagnostic, precisely so a resource whose creation succeeded but
// whose follow-up call failed is not orphaned; the next apply then reads and
// reconciles it instead of the operator importing it by hand.
func (r *spaceResource) finishCreate(ctx context.Context, plan spaceResourceModel, id, key string, resp *resource.CreateResponse) {
	// Record the plan's own values alongside the id, not the id alone. The
	// framework starts the create response state as null, so writing one
	// attribute leaves the rest null -- including create-only inputs such as
	// create_private_space and role_assignments, which no read can recover.
	// Once null in state they differ from configuration, and RequiresReplace
	// would schedule a replacement that deletes the space.
	failWithID := func(summary, detail string) {
		partial := plan
		partial.ID = types.StringValue(id)
		if key != "" {
			partial.Key = types.StringValue(key)
		}
		for _, unresolved := range []*types.String{
			&partial.AuthorID, &partial.SpaceOwnerID, &partial.CreatedAt, &partial.CurrentActiveAlias,
		} {
			if unresolved.IsUnknown() {
				*unresolved = types.StringNull()
			}
		}
		for _, optional := range []*types.String{&partial.Alias, &partial.Type, &partial.Status, &partial.HomepageID} {
			if optional.IsUnknown() {
				*optional = types.StringNull()
			}
		}
		if partial.Description.IsUnknown() {
			partial.Description = types.ObjectNull(descriptionAttributeTypes())
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)
		resp.Diagnostics.Append(resp.Identity.Set(ctx, &spaceResourceIdentityModel{ID: types.StringValue(id)})...)
		resp.Diagnostics.AddError(summary, importHint(id, detail))
	}

	current, err := r.client.GetSpaceByID(ctx, id)
	if err != nil {
		failWithID("Unable to resolve created Confluence space",
			fmt.Sprintf("Atlassian accepted the create request but reading the space back failed: %s.", err))
		return
	}

	// type, status and homepage_id are not accepted by createSpace at all;
	// the server assigns them. If the plan configured a value that differs
	// from what create produced, reconcile it with one v1 updateSpace call
	// before the first read. This path is best-effort: plans/confluence-space.md
	// leaves "homepage update through v1 updateSpace" unverified.
	if updateReq, changed := reconciliationRequest(plan, current); changed {
		if err := r.client.UpdateSpace(ctx, current.Key, updateReq); err != nil {
			failWithID("Confluence space was created but could not be fully configured",
				fmt.Sprintf("Applying type, status or homepage_id after create failed: %s.", err))
			return
		}
		current, err = r.client.GetSpaceByID(ctx, id)
		if err != nil {
			failWithID("Unable to read Confluence space after reconciling create",
				fmt.Sprintf("The space was created and configured, but reading it back failed: %s.", err))
			return
		}
	}

	state, diagnostics := stateFromSpace(ctx, plan, current)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &spaceResourceIdentityModel{ID: state.ID})...)
}

// importHint states what happened, names the space, and says what to do about
// it. Recording state keeps the space from being orphaned, but Terraform marks
// a resource whose Create returned an error as tainted, and a refresh does not
// clear that: the next plan proposes replacement, whose destroy deletes the
// space permanently and without a trash. So the advice has to be untaint, not
// "apply again".
func importHint(id, detail string) string {
	return fmt.Sprintf("%s The space exists with id %s and has been recorded in state, but Terraform has marked this resource tainted, so the next plan proposes replacing it -- which would delete the space permanently. To keep it, run: terraform untaint atlassian_confluence_space.<name> and then apply again. If the state was lost instead, adopt the space with: terraform import atlassian_confluence_space.<name> %s", detail, id, id)
}

// reconciliationRequest reports the fields Create must send through
// updateSpace because createSpace does not accept them at all.
func reconciliationRequest(plan spaceResourceModel, actual Space) (UpdateSpaceRequest, bool) {
	var req UpdateSpaceRequest
	changed := false
	if knownString(plan.Type) && plan.Type.ValueString() != actual.Type {
		value := plan.Type.ValueString()
		req.Type = &value
		changed = true
	}
	if knownString(plan.Status) && plan.Status.ValueString() != actual.Status {
		value := plan.Status.ValueString()
		req.Status = &value
		changed = true
	}
	actualHomepage := ""
	if actual.HomepageID != nil {
		actualHomepage = *actual.HomepageID
	}
	if knownString(plan.HomepageID) && plan.HomepageID.ValueString() != actualHomepage {
		value := plan.HomepageID.ValueString()
		req.HomepageID = &value
		changed = true
	}
	return req, changed
}

func (r *spaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state spaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetSpaceByID(ctx, state.ID.ValueString())
	if err != nil {
		if confluence.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read Confluence space", err.Error())
		return
	}
	// A deleted space leaves no tombstone (verified: getSpaceById 404s
	// outright), so trashed is not the deletion signal there -- but the enum
	// can still carry it, and a space Read observes as trashed is gone from
	// this resource's perspective regardless of how it got there.
	if current.Status == "trashed" {
		resp.State.RemoveResource(ctx)
		return
	}

	updated, diagnostics := stateFromSpace(ctx, state, current)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &spaceResourceIdentityModel{ID: updated.ID})...)
}

func (r *spaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state spaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	key := strings.TrimSpace(state.Key.ValueString())
	if key == "" {
		resp.Diagnostics.AddError("Cannot update Confluence space", "key is empty in state; refusing to send a request to /wiki/rest/api/space/. This should not happen outside a corrupted state file.")
		return
	}

	updateReq, changed, diagnostics := updateRequestFromDiff(ctx, state, plan)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	if changed {
		if err := r.client.UpdateSpace(ctx, key, updateReq); err != nil {
			resp.Diagnostics.AddError("Unable to update Confluence space", err.Error())
			return
		}
	}

	current, err := r.client.GetSpaceByID(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read Confluence space after update", err.Error())
		return
	}
	updated, diagnostics := stateFromSpace(ctx, plan, current)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &spaceResourceIdentityModel{ID: updated.ID})...)
}

// updateRequestFromDiff sends only the fields that changed between the prior
// state and the plan: v1 updateSpace is a genuine partial update (verified: a
// body containing only name preserved the description), so resending
// unchanged fields is unnecessary. Clearing an existing description is not
// attempted: doing so through updateSpace's nullable field is unverified
// against the live API, so a plan that removes description is a no-op on the
// API side (a stated, deliberate limitation).
func updateRequestFromDiff(ctx context.Context, state, plan spaceResourceModel) (UpdateSpaceRequest, bool, diag.Diagnostics) {
	var req UpdateSpaceRequest
	var diagnostics diag.Diagnostics
	changed := false

	if plan.Name.ValueString() != state.Name.ValueString() {
		value := plan.Name.ValueString()
		req.Name = &value
		changed = true
	}

	planDescription, descDiags := descriptionFromModel(ctx, plan.Description)
	diagnostics.Append(descDiags...)
	stateDescription, descDiags := descriptionFromModel(ctx, state.Description)
	diagnostics.Append(descDiags...)
	if planDescription != nil && (stateDescription == nil || *planDescription != *stateDescription) {
		req.Description = planDescription
		changed = true
	}

	if plan.HomepageID.ValueString() != state.HomepageID.ValueString() {
		value := plan.HomepageID.ValueString()
		req.HomepageID = &value
		changed = true
	}
	if plan.Type.ValueString() != state.Type.ValueString() {
		value := plan.Type.ValueString()
		req.Type = &value
		changed = true
	}
	if plan.Status.ValueString() != state.Status.ValueString() {
		value := plan.Status.ValueString()
		req.Status = &value
		changed = true
	}

	return req, changed, diagnostics
}

func (r *spaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state spaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	key := strings.TrimSpace(state.Key.ValueString())
	if key == "" {
		resp.Diagnostics.AddError("Cannot delete Confluence space", "key is empty in state; refusing to send a request to /wiki/rest/api/space/. This should not happen outside a corrupted state file.")
		return
	}
	if err := r.client.DeleteSpace(ctx, key); err != nil {
		resp.Diagnostics.AddError("Unable to delete Confluence space", err.Error())
		return
	}

	// DeleteSpace returns nil once its delete task reports finished (or the
	// delete call itself already 404'd), but a finished task is not proof the
	// space is gone: confirm with a read, per CLAUDE.md's "verify ambiguous
	// outcomes with a read" rule. Deletion leaves no tombstone (plans/
	// confluence-space.md, "Read"), so the expected outcome is a 404; trashed
	// is accepted too in case the enum ever carries it for a delete.
	current, err := r.client.GetSpaceByID(ctx, state.ID.ValueString())
	if err != nil {
		if confluence.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Unable to confirm Confluence space deletion", err.Error())
		return
	}
	if current.Status != "trashed" {
		resp.Diagnostics.AddError("Unable to confirm Confluence space deletion",
			"space still readable after delete task finished.")
	}
}

var numericIDPattern = regexp.MustCompile(`^[0-9]+$`)

func (r *spaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var id string
	switch {
	case req.ID != "":
		id = strings.TrimSpace(req.ID)
	case req.Identity != nil:
		var identity spaceResourceIdentityModel
		resp.Diagnostics.Append(req.Identity.Get(ctx, &identity)...)
		if resp.Diagnostics.HasError() {
			return
		}
		id = strings.TrimSpace(identity.ID.ValueString())
	default:
		resp.Diagnostics.AddError("Invalid import identifier", "Expected either a string ID or a resource identity.")
		return
	}
	if id == "" || !numericIDPattern.MatchString(id) {
		resp.Diagnostics.AddError("Invalid import identifier", "Expected the numeric-string Confluence space id.")
		return
	}

	state := spaceResourceModel{
		ID:                           types.StringValue(id),
		Key:                          types.StringUnknown(),
		Alias:                        types.StringUnknown(),
		Name:                         types.StringUnknown(),
		Description:                  types.ObjectUnknown(descriptionAttributeTypes()),
		Type:                         types.StringUnknown(),
		Status:                       types.StringUnknown(),
		HomepageID:                   types.StringUnknown(),
		TemplateKey:                  types.StringNull(),
		CopySpaceAccessConfiguration: types.StringNull(),
		CreatePrivateSpace:           types.BoolNull(),
		RoleAssignments:              types.SetNull(types.ObjectType{AttrTypes: roleAssignmentAttributeTypes()}),
		AuthorID:                     types.StringUnknown(),
		SpaceOwnerID:                 types.StringUnknown(),
		CreatedAt:                    types.StringUnknown(),
		CurrentActiveAlias:           types.StringUnknown(),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(resp.Identity.Set(ctx, &spaceResourceIdentityModel{ID: types.StringValue(id)})...)
}

// stateFromSpace builds the new resource state from an API read, preserving
// the four write-only-at-create attributes from prior (base's) values: no
// read operation ever returns them, so Terraform's own record is the only
// source for them once the space exists.
func stateFromSpace(ctx context.Context, base spaceResourceModel, current Space) (spaceResourceModel, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	description, err := descriptionValue(ctx, current.Description)
	if err != nil {
		diagnostics.AddError("Unable to convert Confluence space description", err.Error())
		return spaceResourceModel{}, diagnostics
	}
	state := spaceResourceModel{
		ID:                           types.StringValue(current.ID),
		Key:                          types.StringValue(current.Key),
		Alias:                        aliasFromSpace(base.Alias, current),
		Name:                         types.StringValue(current.Name),
		Description:                  description,
		Type:                         types.StringValue(current.Type),
		Status:                       types.StringValue(current.Status),
		HomepageID:                   nullableStringPointer(current.HomepageID),
		TemplateKey:                  base.TemplateKey,
		CopySpaceAccessConfiguration: base.CopySpaceAccessConfiguration,
		CreatePrivateSpace:           base.CreatePrivateSpace,
		RoleAssignments:              base.RoleAssignments,
		AuthorID:                     types.StringValue(current.AuthorID),
		SpaceOwnerID:                 nullableStringPointer(current.SpaceOwnerID),
		CreatedAt:                    createdAtValue(current.CreatedAt),
		CurrentActiveAlias:           nullableStringPointer(current.CurrentActiveAlias),
	}
	return state, diagnostics
}

// aliasFromSpace resolves the alias to store in state. alias is only ever
// supplied at create, and no read operation returns it back directly, so
// Read must never store an unknown value for it (which ImportState sets):
// gate 3 verified the alias equals whatever key or alias was given at
// create, so current_active_alias -- which is always returned -- stands in
// for it once it is known. Otherwise alias keeps whatever value the caller
// (plan or prior state) already had.
func aliasFromSpace(base types.String, current Space) types.String {
	if base.IsUnknown() {
		if current.CurrentActiveAlias != nil {
			return types.StringValue(*current.CurrentActiveAlias)
		}
		return types.StringNull()
	}
	return base
}

func descriptionFromModel(ctx context.Context, object types.Object) (*Description, diag.Diagnostics) {
	if object.IsNull() || object.IsUnknown() {
		return nil, nil
	}
	var model descriptionModel
	diagnostics := object.As(ctx, &model, basetypes.ObjectAsOptions{})
	if diagnostics.HasError() {
		return nil, diagnostics
	}
	representation := "plain"
	if knownString(model.Representation) && model.Representation.ValueString() != "" {
		representation = model.Representation.ValueString()
	}
	return &Description{Value: model.Value.ValueString(), Representation: representation}, diagnostics
}

func roleAssignmentsFromModel(ctx context.Context, set types.Set) ([]RoleAssignment, diag.Diagnostics) {
	if set.IsNull() || set.IsUnknown() {
		return nil, nil
	}
	var models []roleAssignmentResourceModel
	diagnostics := set.ElementsAs(ctx, &models, false)
	if diagnostics.HasError() {
		return nil, diagnostics
	}
	assignments := make([]RoleAssignment, 0, len(models))
	for _, model := range models {
		var principal principalResourceModel
		diagnostics.Append(model.Principal.As(ctx, &principal, basetypes.ObjectAsOptions{})...)
		assignments = append(assignments, RoleAssignment{
			PrincipalType: principal.PrincipalType.ValueString(),
			PrincipalID:   principal.PrincipalID.ValueString(),
			RoleID:        model.RoleID.ValueString(),
		})
	}
	return assignments, diagnostics
}

func principalAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"principal_type": types.StringType,
		"principal_id":   types.StringType,
	}
}

func roleAssignmentAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"principal": types.ObjectType{AttrTypes: principalAttributeTypes()},
		"role_id":   types.StringType,
	}
}
