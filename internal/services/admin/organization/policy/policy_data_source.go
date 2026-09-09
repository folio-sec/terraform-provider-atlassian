package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization/generated"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type policyReader interface {
	GetPolicyById(context.Context, string, string) (generated.PolicyModel, error)
	GetPolicies(context.Context, string, *string) ([]generated.PolicyModel, error)
}

type policyDataSource struct {
	client     policyReader
	collection bool
}
type policyDataSourceModel struct {
	OrganizationID types.String `tfsdk:"organization_id"`
	PolicyID       types.String `tfsdk:"policy_id"`
	Data           types.Object `tfsdk:"data"`
}
type policiesDataSourceModel struct {
	OrganizationID types.String `tfsdk:"organization_id"`
	Type           types.String `tfsdk:"type"`
	Data           types.Set    `tfsdk:"data"`
}

var _ datasource.DataSourceWithValidateConfig = &policyDataSource{}

func NewPolicyDataSource() datasource.DataSource   { return &policyDataSource{} }
func NewPoliciesDataSource() datasource.DataSource { return &policyDataSource{collection: true} }

func (d *policyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_policy"
	if d.collection {
		resp.TypeName = req.ProviderTypeName + "_organization_policies"
	}
}

func policyComputedStrings(names ...string) map[string]schema.Attribute {
	result := map[string]schema.Attribute{}
	for _, name := range names {
		result[name] = schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "API response field `" + name + "`.",
		}
	}
	return result
}

func policyAssociationReadSchema() map[string]schema.Attribute {
	fields := policyComputedStrings("id", "type", "application_status", "created_at", "updated_at")
	for _, name := range []string{"meta", "metadata", "links"} {
		fields[name] = policyJSONSchema(name)
	}
	return fields
}

func policyJSONSchema(name string) schema.StringAttribute {
	// Rule and metadata have heterogeneous JSON shapes, including arrays. A JSON
	// string preserves these shapes while keeping collection element types stable.
	return schema.StringAttribute{
		Computed: true,
		MarkdownDescription: "JSON-encoded API `" + name + "`. Use `jsondecode()` to access its contents; " +
			"object keys are canonicalized and array order is preserved.",
	}
}

func policyReadSchema() map[string]schema.Attribute {
	fields := policyComputedStrings("id", "type")
	attributes := policyComputedStrings("id", "owner_id", "type", "name", "status", "created_at", "updated_at", "query_data")
	attributes["rule"] = policyJSONSchema("rule")
	attributes["metadata"] = policyJSONSchema("metadata")
	attributes["resources"] = schema.SetNestedAttribute{
		Computed:            true,
		MarkdownDescription: "Resources associated with the policy.",
		NestedObject: schema.NestedAttributeObject{
			Attributes: policyAssociationReadSchema(),
		},
	}
	fields["attributes"] = schema.SingleNestedAttribute{
		Computed:            true,
		MarkdownDescription: "Policy attributes returned by the API.",
		Attributes:          attributes,
	}
	fields["links"] = policyJSONSchema("links")
	fields["message"] = policyJSONSchema("message")
	return fields
}

func policyObjectTypes(fields map[string]schema.Attribute) map[string]attr.Type {
	result := map[string]attr.Type{}
	for name, field := range fields {
		result[name] = field.GetType()
	}
	return result
}

func (d *policyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	fields := map[string]schema.Attribute{
		"organization_id": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Organization ID used in the API path.",
		},
	}
	markdownDescription := "Reads one organization policy. Requires `read:policies:admin`.\n\n" +
		"> Rules and metadata are returned as JSON to preserve policy-specific response shapes."
	if d.collection {
		markdownDescription = "Reads all pages of organization policies. Requires `read:policies:admin`.\n\n" +
			"> Results are represented as a set for every cardinality, including zero or one result."
		fields["type"] = schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "API policy type filter. Undocumented type values are passed through.",
		}
		fields["data"] = schema.SetNestedAttribute{
			Computed:            true,
			MarkdownDescription: "Matching policies.",
			NestedObject: schema.NestedAttributeObject{
				Attributes: policyReadSchema(),
			},
		}
	} else {
		fields["policy_id"] = schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Policy ID used in the API path.",
		}
		fields["data"] = schema.SingleNestedAttribute{
			Computed:            true,
			MarkdownDescription: "Policy returned by the API.",
			Attributes:          policyReadSchema(),
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: markdownDescription,
		Attributes:          fields,
	}
}

func (d *policyDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T", req.ProviderData))
		return
	}
	if c.Organization == nil {
		resp.Diagnostics.AddError("Admin API is not configured", "Set admin_api_key or ATLASSIAN_ADMIN_API_KEY.")
		return
	}
	d.client = c.Organization
}

func (d *policyDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	if d.collection {
		var config policiesDataSourceModel
		resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
		resp.Diagnostics.Append(validateNonEmpty("Invalid policy lookup", namedValue{"organization_id", config.OrganizationID}, namedValue{"type", config.Type})...)
		return
	}
	var config policyDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(validateNonEmpty("Invalid policy lookup", namedValue{"organization_id", config.OrganizationID}, namedValue{"policy_id", config.PolicyID})...)
}

func (d *policyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.collection {
		d.readCollection(ctx, req, resp)
		return
	}
	var config policyDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := d.client.GetPolicyById(ctx, config.OrganizationID.ValueString(), config.PolicyID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read policy", err.Error())
		return
	}
	config.Data = policyReadValue(policy, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
	}
}

func (d *policyDataSource) readCollection(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config policiesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var filter *string
	if !config.Type.IsNull() {
		value := config.Type.ValueString()
		filter = &value
	}
	policies, err := d.client.GetPolicies(ctx, config.OrganizationID.ValueString(), filter)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list policies", err.Error())
		return
	}
	values := make([]attr.Value, 0, len(policies))
	for _, policy := range policies {
		values = append(values, policyReadValue(policy, &resp.Diagnostics))
	}
	set, diagnostics := types.SetValue(types.ObjectType{AttrTypes: policyObjectTypes(policyReadSchema())}, values)
	resp.Diagnostics.Append(diagnostics...)
	config.Data = set
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
	}
}

func policyJSONValue(raw *json.RawMessage, diagnostics *diag.Diagnostics) types.String {
	if raw == nil || strings.TrimSpace(string(*raw)) == "null" {
		return types.StringNull()
	}
	decoder := json.NewDecoder(bytes.NewReader(*raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		diagnostics.AddError("Invalid policy JSON", err.Error())
		return types.StringNull()
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		diagnostics.AddError("Invalid policy JSON", err.Error())
		return types.StringNull()
	}
	return types.StringValue(string(encoded))
}

func policyReadValue(policy generated.PolicyModel, diagnostics *diag.Diagnostics) types.Object {
	a := policy.Attributes
	resources := make([]attr.Value, 0)
	if a.Resources != nil {
		for _, r := range *a.Resources {
			value, ds := types.ObjectValue(policyObjectTypes(policyAssociationReadSchema()), map[string]attr.Value{
				"id": types.StringValue(r.Id), "type": nullableStringValue(r.Type), "application_status": types.StringValue(r.ApplicationStatus),
				"created_at": nullableStringValue(r.CreatedAt), "updated_at": nullableStringValue(r.UpdatedAt),
				"meta": policyJSONValue(r.Meta, diagnostics), "metadata": policyJSONValue(r.Metadata, diagnostics), "links": policyJSONValue(r.Links, diagnostics),
			})
			diagnostics.Append(ds...)
			resources = append(resources, value)
		}
	}
	resourceSet, ds := types.SetValue(types.ObjectType{AttrTypes: policyObjectTypes(policyAssociationReadSchema())}, resources)
	diagnostics.Append(ds...)
	attributeSchema := policyReadSchema()["attributes"].(schema.SingleNestedAttribute)
	attributes, ds := types.ObjectValue(policyObjectTypes(attributeSchema.Attributes), map[string]attr.Value{
		"id": nullableStringValue(a.Id), "owner_id": nullableStringValue(a.OwnerId), "type": types.StringValue(a.Type), "name": nullableStringValue(a.Name),
		"status": nullableStringValue(a.Status), "created_at": nullableStringValue(a.CreatedAt), "updated_at": nullableStringValue(a.UpdatedAt),
		"query_data": nullableStringValue(a.QueryData), "rule": policyJSONValue(a.Rule, diagnostics), "metadata": policyJSONValue(a.Metadata, diagnostics), "resources": resourceSet,
	})
	diagnostics.Append(ds...)
	value, ds := types.ObjectValue(policyObjectTypes(policyReadSchema()), map[string]attr.Value{"id": types.StringValue(policy.Id), "type": types.StringValue(string(policy.Type)), "attributes": attributes, "links": policyJSONValue(policy.Links, diagnostics), "message": policyJSONValue(policy.Message, diagnostics)})
	diagnostics.Append(ds...)
	return value
}
