package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization/generated"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

const managedPolicyType = "data-residency"

type policyResourceModel struct {
	OrganizationID types.String `tfsdk:"organization_id"`
	PolicyID       types.String `tfsdk:"policy_id"`
	Data           types.Object `tfsdk:"data"`
	CreateTimeout  types.String `tfsdk:"create_timeout"`
	UpdateTimeout  types.String `tfsdk:"update_timeout"`
	DeleteTimeout  types.String `tfsdk:"delete_timeout"`
}
type policyDataModel struct {
	ID         types.String `tfsdk:"id"`
	Type       types.String `tfsdk:"type"`
	Attributes types.Object `tfsdk:"attributes"`
}
type policyAttributesModel struct {
	Type      types.String `tfsdk:"type"`
	Name      types.String `tfsdk:"name"`
	Status    types.String `tfsdk:"status"`
	Rule      types.Object `tfsdk:"rule"`
	Resources types.Set    `tfsdk:"resources"`
}
type policyDesired struct {
	Name   string
	Status string
	Realms []string
}

func policyPlanValues(ctx context.Context, model policyResourceModel, allowUnknown bool) (policyDesired, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	var desired policyDesired
	if model.Data.IsUnknown() && allowUnknown {
		return desired, diagnostics
	}
	if model.Data.IsNull() || model.Data.IsUnknown() {
		diagnostics.AddError("Invalid policy", "data must be known and non-null.")
		return desired, diagnostics
	}
	var data policyDataModel
	diagnostics.Append(model.Data.As(ctx, &data, basetypes.ObjectAsOptions{})...)
	if !data.Type.IsNull() && !data.Type.IsUnknown() && data.Type.ValueString() != "policy" {
		diagnostics.AddError("Invalid policy", "data.type must be policy.")
	}
	if data.Attributes.IsUnknown() && allowUnknown {
		return desired, diagnostics
	}
	if data.Attributes.IsNull() || data.Attributes.IsUnknown() {
		diagnostics.AddError("Invalid policy", "data.attributes must be known and non-null.")
		return desired, diagnostics
	}
	var attributes policyAttributesModel
	diagnostics.Append(data.Attributes.As(ctx, &attributes, basetypes.ObjectAsOptions{})...)
	diagnostics.Append(validatePolicyAttributes(attributes, allowUnknown)...)
	realms, ds := policyRuleRealms(ctx, attributes.Rule, allowUnknown)
	diagnostics.Append(ds...)
	desired.Name = attributes.Name.ValueString()
	desired.Status = attributes.Status.ValueString()
	desired.Realms = realms
	return desired, diagnostics
}

func validatePolicyAttributes(attributes policyAttributesModel, allowUnknown bool) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if !attributes.Type.IsUnknown() && attributes.Type.ValueString() != managedPolicyType {
		diagnostics.AddError("Unsupported policy type", "Only data-residency policies without associated resources can be managed. Use the policy data source to read other types.")
	}
	if attributes.Name.IsNull() || (!allowUnknown && attributes.Name.IsUnknown()) {
		diagnostics.AddError("Invalid policy", "name must be known and non-null; an empty string is allowed.")
	}
	if !attributes.Status.IsUnknown() && attributes.Status.ValueString() != "enabled" && attributes.Status.ValueString() != "disabled" {
		diagnostics.AddError("Invalid policy", "status must be enabled or disabled.")
	}
	if !attributes.Resources.IsNull() && !attributes.Resources.IsUnknown() && len(attributes.Resources.Elements()) > 0 {
		diagnostics.AddError("Unsupported policy associations", "resources must be empty. Resource association lifecycle has not yet been verified.")
	}
	if !allowUnknown && (attributes.Type.IsUnknown() || attributes.Status.IsUnknown() || attributes.Resources.IsUnknown()) {
		diagnostics.AddError("Invalid policy", "Policy attributes must be known before a mutation.")
	}
	return diagnostics
}

func policyRuleRealms(ctx context.Context, rule types.Object, allowUnknown bool) ([]string, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	var result []string
	if rule.IsUnknown() && allowUnknown {
		return result, diagnostics
	}
	if rule.IsNull() || rule.IsUnknown() {
		diagnostics.AddError("Invalid policy", "rule must be known and non-null.")
		return result, diagnostics
	}
	realms := rule.Attributes()["in"].(types.Set)
	if !realms.IsUnknown() && (realms.IsNull() || len(realms.Elements()) == 0) {
		diagnostics.AddError("Invalid policy rule", "rule.in must contain at least one realm.")
	}
	if !realms.IsUnknown() {
		for _, v := range realms.Elements() {
			if v.IsNull() {
				diagnostics.AddError("Invalid policy rule", "rule.in must not contain null elements.")
			}
			if v.IsUnknown() && allowUnknown {
				return result, diagnostics
			}
		}
		diagnostics.Append(realms.ElementsAs(ctx, &result, false)...)
	} else if !allowUnknown {
		diagnostics.AddError("Invalid policy rule", "rule.in must be known before a mutation.")
	}
	slices.Sort(result)
	return result, diagnostics
}

func supportedPolicy(policy generated.PolicyModel) (policyDesired, error) {
	a := policy.Attributes
	if policy.Type != "policy" || a.Type != managedPolicyType {
		return policyDesired{}, fmt.Errorf("unsupported policy type %q; only isolated data-residency policies can be managed", a.Type)
	}
	if a.Resources == nil || len(*a.Resources) != 0 {
		return policyDesired{}, fmt.Errorf("policy associations must be explicitly empty; associated policies are not supported")
	}
	if a.Name == nil || a.Status == nil || (*a.Status != "enabled" && *a.Status != "disabled") || a.Rule == nil {
		return policyDesired{}, fmt.Errorf("policy has missing or unsupported writable attributes")
	}
	decoder := json.NewDecoder(bytes.NewReader(*a.Rule))
	decoder.DisallowUnknownFields()
	var rule generated.AllowIfContainedRule
	if err := decoder.Decode(&rule); err != nil {
		return policyDesired{}, fmt.Errorf("unsupported policy rule: %w", err)
	}
	if err := checkPolicyRuleElements(*a.Rule); err != nil {
		return policyDesired{}, err
	}
	if len(rule.In) == 0 {
		return policyDesired{}, fmt.Errorf("policy rule.in must not be empty")
	}
	slices.Sort(rule.In)
	return policyDesired{Name: *a.Name, Status: *a.Status, Realms: slices.Compact(rule.In)}, nil
}

// encoding/json turns null string-array elements into empty strings. Refuse that
// lossy conversion before adopting an API rule into Terraform state.
func checkPolicyRuleElements(raw json.RawMessage) error {
	var fields map[string][]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("decode rule elements: %w", err)
	}
	for _, value := range fields["in"] {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("policy rule.in contains a null element")
		}
	}
	return nil
}

func policyDesiredMatches(actual, expected policyDesired) bool {
	return actual.Name == expected.Name && actual.Status == expected.Status && slices.Equal(actual.Realms, expected.Realms)
}

func policyCreateRequest(desired policyDesired) (generated.CreatePolicyJSONRequestBody, error) {
	data := generated.PolicyCreateModel{Type: "policy"}
	data.Attributes.Type = managedPolicyType
	data.Attributes.Name = &desired.Name
	status := generated.PolicyCreateModelAttributesStatus(desired.Status)
	data.Attributes.Status = &status
	resources := []generated.ResourceInput{}
	data.Attributes.Resources = &resources
	rule := generated.PolicyCreateModel_Attributes_Rule{}
	if err := rule.FromAllowIfContainedRule(generated.AllowIfContainedRule{In: desired.Realms}); err != nil {
		return generated.CreatePolicyJSONRequestBody{}, fmt.Errorf("encode policy rule: %w", err)
	}
	data.Attributes.Rule = &rule
	return generated.CreatePolicyJSONRequestBody{Data: &data}, nil
}

func policyUpdateRequest(id string, desired policyDesired) (generated.UpdatePolicyJSONRequestBody, error) {
	data := generated.PolicyUpdateModel{Id: &id, Type: "policy"}
	data.Attributes.Type = managedPolicyType
	data.Attributes.Name = &desired.Name
	status := generated.PolicyUpdateModelAttributesStatus(desired.Status)
	data.Attributes.Status = &status
	resources := []generated.ResourceInput{}
	data.Attributes.Resources = &resources
	rule := generated.PolicyUpdateModel_Attributes_Rule{}
	if err := rule.FromAllowIfContainedRule(generated.AllowIfContainedRule{In: desired.Realms}); err != nil {
		return generated.UpdatePolicyJSONRequestBody{}, fmt.Errorf("encode policy rule: %w", err)
	}
	data.Attributes.Rule = &rule
	return generated.UpdatePolicyJSONRequestBody{Data: &data}, nil
}

func setPolicyResourceData(model *policyResourceModel, id string, desired policyDesired, diagnostics *diag.Diagnostics) {
	dataTypes := policyResourceDataTypes()
	attributeTypes := dataTypes["attributes"].(types.ObjectType).AttrTypes
	ruleTypes := attributeTypes["rule"].(types.ObjectType).AttrTypes
	realms := make([]attr.Value, 0, len(desired.Realms))
	for _, realm := range desired.Realms {
		realms = append(realms, types.StringValue(realm))
	}
	realmSet, ds := types.SetValue(types.StringType, realms)
	diagnostics.Append(ds...)
	rule, ds := types.ObjectValue(ruleTypes, map[string]attr.Value{"in": realmSet})
	diagnostics.Append(ds...)
	resources, ds := types.SetValue(attributeTypes["resources"].(types.SetType).ElemType, nil)
	diagnostics.Append(ds...)
	attributes, ds := types.ObjectValue(attributeTypes, map[string]attr.Value{"type": types.StringValue(managedPolicyType), "name": types.StringValue(desired.Name), "status": types.StringValue(desired.Status), "rule": rule, "resources": resources})
	diagnostics.Append(ds...)
	model.Data, ds = types.ObjectValue(dataTypes, map[string]attr.Value{"id": types.StringValue(id), "type": types.StringValue("policy"), "attributes": attributes})
	diagnostics.Append(ds...)
	model.PolicyID = types.StringValue(id)
}

func parsePolicyImportID(id string) (string, string, error) {
	parts := strings.Split(id, ",")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("expected organization_id,policy_id")
	}
	// Do not normalize identity strings differently from resource configuration.
	if strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("organization_id and policy_id must not be blank")
	}
	return parts[0], parts[1], nil
}
