package policy

import (
	"context"
	"fmt"
	"time"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization/generated"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type policyWriter interface {
	policyReader
	CreatePolicy(context.Context, string, generated.CreatePolicyJSONRequestBody) (generated.PolicyModel, error)
	UpdatePolicy(context.Context, string, string, generated.UpdatePolicyJSONRequestBody) error
	DeletePolicy(context.Context, string, string) error
}

type policyResource struct {
	client       policyWriter
	pollInterval time.Duration
}

type policyIdentity struct {
	OrganizationID types.String `tfsdk:"organization_id"`
	PolicyID       types.String `tfsdk:"policy_id"`
}

var _ resource.ResourceWithIdentity = &policyResource{}
var _ resource.ResourceWithImportState = &policyResource{}
var _ resource.ResourceWithValidateConfig = &policyResource{}

func NewPolicyResource() resource.Resource {
	return &policyResource{}
}

func (r *policyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_policy"
}

func (r *policyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	fields := map[string]schema.Attribute{
		"organization_id": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Organization ID used in the API path.",
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(),
			},
		},
		"policy_id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Policy ID, also returned in `data.id`.",
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			},
		},
		"data": policyResourceDataSchema(),
	}

	// Terraform needs bounded lifecycle waits; these controls are not API fields.
	for _, name := range []string{"create_timeout", "update_timeout", "delete_timeout"} {
		fields[name] = schema.StringAttribute{
			Optional:            true,
			Computed:            true,
			Default:             stringdefault.StaticString("2m"),
			MarkdownDescription: "Positive Go duration for this operation's API request and convergence checks. Defaults to `2m`.",
		}
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an Atlassian organization policy. Requires `read:policies:admin`, `write:policies:admin`, and `delete:policies:admin`.\n\n" +
			"> **Supported scope**\n" +
			"> Only `data-residency` policies without resource associations are supported for creation, import, and management. " +
			"Use the organization policy data sources to read IP allowlists and other policy types.\n\n" +
			"A policy's type is specified by `data.attributes.type`; `data.type` is always `policy`.",
		Attributes: fields,
	}
}

func (r *policyResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"organization_id": identityschema.StringAttribute{
				RequiredForImport: true,
				Description:       "Organization ID.",
			},
			"policy_id": identityschema.StringAttribute{
				RequiredForImport: true,
				Description:       "Policy ID.",
			},
		},
	}
}

func (r *policyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	configured, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T", req.ProviderData))
		return
	}
	if configured.Organization == nil {
		resp.Diagnostics.AddError("Admin API is not configured", "Set admin_api_key or ATLASSIAN_ADMIN_API_KEY.")
		return
	}

	r.client = configured.Organization
}

func (r *policyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var model policyResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(validateNonEmpty("Invalid policy", namedValue{"organization_id", model.OrganizationID})...)
	_, diagnostics := policyPlanValues(ctx, model, true)
	resp.Diagnostics.Append(diagnostics...)
	for name, value := range map[string]types.String{
		"create_timeout": model.CreateTimeout,
		"update_timeout": model.UpdateTimeout,
		"delete_timeout": model.DeleteTimeout,
	} {
		if value.IsUnknown() {
			continue
		}
		if _, err := policyTimeout(value); err != nil {
			resp.Diagnostics.AddError("Invalid policy timeout", name+": "+err.Error())
		}
	}
}
