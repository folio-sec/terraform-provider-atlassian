package policy

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestPolicyRuleValidation(t *testing.T) {
	for name, realms := range map[string]types.Set{
		"empty":           types.SetValueMust(types.StringType, nil),
		"null":            types.SetNull(types.StringType),
		"unknown":         types.SetUnknown(types.StringType),
		"unknown element": types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()}),
		"null element":    types.SetValueMust(types.StringType, []attr.Value{types.StringNull()}),
	} {
		t.Run(name, func(t *testing.T) {
			rule := types.ObjectValueMust(map[string]attr.Type{"in": types.SetType{ElemType: types.StringType}}, map[string]attr.Value{"in": realms})
			if _, ds := policyRuleRealms(context.Background(), rule, false); !ds.HasError() {
				t.Fatal("mutation accepted invalid rule")
			}
			_, ds := policyRuleRealms(context.Background(), rule, true)
			if ds.HasError() == (name == "unknown" || name == "unknown element") {
				t.Fatalf("config diagnostics=%v", ds)
			}
		})
	}
	p := testPolicy(t)
	raw := json.RawMessage(`{"in":[null]}`)
	p.Attributes.Rule = &raw
	if _, err := supportedPolicy(p); err == nil {
		t.Fatal("accepted lossy null string conversion")
	}
}

func TestPolicyUnknownConfigAndImportIdentity(t *testing.T) {
	model := policyResourceModel{Data: types.ObjectUnknown(policyResourceDataTypes())}
	if _, ds := policyPlanValues(context.Background(), model, true); ds.HasError() {
		t.Fatal(ds)
	}
	if _, ds := policyPlanValues(context.Background(), model, false); !ds.HasError() {
		t.Fatal("unknown mutation accepted")
	}
	for _, id := range []string{"", "org", "org,", " ,id", "org,id,extra"} {
		if _, _, err := parsePolicyImportID(id); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	if org, id, err := parsePolicyImportID("org,id"); err != nil || org != "org" || id != "id" {
		t.Fatalf("%s %s %v", org, id, err)
	}
	for _, timeout := range []string{"0s", "-1s", "not-a-duration"} {
		if _, err := policyTimeout(types.StringValue(timeout)); err == nil {
			t.Fatalf("accepted %q", timeout)
		}
	}
}
