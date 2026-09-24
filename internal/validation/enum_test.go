package validation

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// testEnum mirrors the shape oapi-codegen generates for a string enum.
type testEnum string

func (e testEnum) Valid() bool {
	switch e {
	case "alpha", "beta":
		return true
	default:
		return false
	}
}

func TestEnumDefersToValid(t *testing.T) {
	t.Parallel()
	enum := Enum[testEnum]()
	for _, testCase := range []struct {
		name      string
		value     types.String
		wantError bool
	}{
		{name: "declared value", value: types.StringValue("alpha")},
		{name: "another declared value", value: types.StringValue("beta")},
		{name: "undeclared value", value: types.StringValue("gamma"), wantError: true},
		{name: "case differs", value: types.StringValue("Alpha"), wantError: true},
		{name: "blank", value: types.StringValue(""), wantError: true},
		{name: "null is left to Terraform", value: types.StringNull()},
		{name: "unknown is left to Terraform", value: types.StringUnknown()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			response := &validator.StringResponse{}
			enum.ValidateString(context.Background(), validator.StringRequest{
				Path: path.Root("attribute"), ConfigValue: testCase.value,
			}, response)
			if response.Diagnostics.HasError() != testCase.wantError {
				t.Fatalf("rejected = %t, want %t (%v)", response.Diagnostics.HasError(), testCase.wantError, response.Diagnostics)
			}
		})
	}
}
