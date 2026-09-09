package datasecuritypolicy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/control/generated"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const defaultTimeout = "2m"

type policyClient interface {
	GetDataSecurityPolicy(context.Context, string, string) (generated.ModelsDataSecurityPolicy, error)
	CreateDataSecurityPolicy(context.Context, string, generated.CreateDataSecurityPolicyJSONRequestBody) (generated.ModelsDataSecurityPolicy, error) //nolint:staticcheck // No public replacement exists.
	UpdateDataSecurityPolicy(context.Context, string, string, generated.UpdateDataSecurityPolicyJSONRequestBody) error                               //nolint:staticcheck // No public replacement exists.
	DeleteDataSecurityPolicy(context.Context, string, string) error
}

type dataSecurityPolicyResource struct {
	client       policyClient
	pollInterval time.Duration
}

type resourceModel struct {
	OrganizationID types.String `tfsdk:"organization_id"`
	PolicyID       types.String `tfsdk:"policy_id"`
	Data           types.Object `tfsdk:"data"`
	CreateTimeout  types.String `tfsdk:"create_timeout"`
	UpdateTimeout  types.String `tfsdk:"update_timeout"`
	DeleteTimeout  types.String `tfsdk:"delete_timeout"`
}

type identityModel struct {
	OrganizationID types.String `tfsdk:"organization_id"`
	PolicyID       types.String `tfsdk:"policy_id"`
}

type dataModel struct {
	ID         types.String `tfsdk:"id"`
	Type       types.String `tfsdk:"type"`
	Attributes types.Object `tfsdk:"attributes"`
}

type attributesModel struct {
	Type     types.String `tfsdk:"type"`
	Name     types.String `tfsdk:"name"`
	Status   types.String `tfsdk:"status"`
	Rule     types.Object `tfsdk:"rule"`
	Metadata types.Object `tfsdk:"metadata"`
}

type ruleModel struct {
	Export types.Object `tfsdk:"export"`
}

type effectModel struct {
	Effect types.String `tfsdk:"effect"`
}

type metadataModel struct {
	PolicyCoverageLevel types.String `tfsdk:"policy_coverage_level"`
	Description         types.String `tfsdk:"description"`
}

type desiredPolicy struct {
	Name        string
	Status      string
	Effect      string
	Coverage    string
	Description string
}

var _ resource.ResourceWithIdentity = &dataSecurityPolicyResource{}
var _ resource.ResourceWithImportState = &dataSecurityPolicyResource{}
var _ resource.ResourceWithValidateConfig = &dataSecurityPolicyResource{}

func NewDataSecurityPolicyResource() resource.Resource {
	return &dataSecurityPolicyResource{}
}

func (r *dataSecurityPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_data_security_policy"
}

func (r *dataSecurityPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an unpublished Atlassian data-security policy draft through the Admin Control API.\n\n" +
			"> **Supported scope**\n" +
			"> Only the verified `ORG`-level `export` rule is supported. Publishing and resource associations are not managed.\n\n" +
			"> **Deprecated API**\n" +
			"> Atlassian marks the underlying public policy operations deprecated and may temporarily disable them. " +
			"No public replacement is currently documented.",
		Attributes: map[string]schema.Attribute{
			"organization_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Organization ID used in the API path.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"policy_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Policy ID returned by the API.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"data":           policyDataSchema(),
			"create_timeout": timeoutSchema("create"),
			"update_timeout": timeoutSchema("update"),
			"delete_timeout": timeoutSchema("delete"),
		},
	}
}

func timeoutSchema(operation string) schema.StringAttribute {
	return schema.StringAttribute{
		Optional:            true,
		Computed:            true,
		Default:             stringdefault.StaticString(defaultTimeout),
		MarkdownDescription: "Positive Go duration for the " + operation + " request and convergence checks. Defaults to `2m`.",
	}
}

func policyDataSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required:            true,
		MarkdownDescription: "Data-security policy request data.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Policy ID returned by the API.",
			},
			"type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("policy"),
				MarkdownDescription: "API object type; must be `policy`.",
			},
			"attributes": dataSecurityAttributesSchema(),
		},
	}
}

func dataSecurityAttributesSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required:            true,
		MarkdownDescription: "Data-security policy attributes.",
		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Policy type; must be `data-security`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Policy draft name.",
			},
			"status": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("draft"),
				MarkdownDescription: "Policy status. Only `draft` is managed because publication is a separate bulk operation.",
			},
			"rule":     dataSecurityRuleSchema(),
			"metadata": dataSecurityMetadataSchema(),
		},
	}
}

func dataSecurityRuleSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required:            true,
		MarkdownDescription: "Policy rules. Only `export` has a verified lifecycle.",
		Attributes: map[string]schema.Attribute{
			"export": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Confluence page export rule.",
				Attributes: map[string]schema.Attribute{
					"effect": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Rule effect: `allow` or `block`.",
					},
				},
			},
		},
	}
}

func dataSecurityMetadataSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required:            true,
		MarkdownDescription: "Writable policy metadata.",
		Attributes: map[string]schema.Attribute{
			"policy_coverage_level": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Policy coverage. Only `ORG` is supported by this verified resource.",
			},
			"description": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Policy description.",
			},
		},
	}
}

func (r *dataSecurityPolicyResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
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

func (r *dataSecurityPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	configured, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T", req.ProviderData))
		return
	}
	if configured.Control == nil {
		resp.Diagnostics.AddError("Admin Control API is not configured", "Set admin_api_key or ATLASSIAN_ADMIN_API_KEY.")
		return
	}
	r.client = configured.Control
}

func (r *dataSecurityPolicyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var model resourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if model.OrganizationID.IsNull() || (!model.OrganizationID.IsUnknown() && strings.TrimSpace(model.OrganizationID.ValueString()) == "") {
		resp.Diagnostics.AddError("Invalid data-security policy", "organization_id must be nonblank.")
	}
	_, diagnostics := planValues(ctx, model, true)
	resp.Diagnostics.Append(diagnostics...)
	for name, value := range map[string]types.String{
		"create_timeout": model.CreateTimeout,
		"update_timeout": model.UpdateTimeout,
		"delete_timeout": model.DeleteTimeout,
	} {
		if value.IsUnknown() {
			continue
		}
		if _, err := parseTimeout(value); err != nil {
			resp.Diagnostics.AddError("Invalid data-security policy timeout", name+": "+err.Error())
		}
	}
}
