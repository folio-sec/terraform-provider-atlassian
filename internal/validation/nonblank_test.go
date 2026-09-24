package validation

import (
	"context"
	"strings"
	"testing"
	"unicode"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func rejects(value types.String) bool {
	response := &validator.StringResponse{}
	NonBlank.ValidateString(context.Background(), validator.StringRequest{
		Path: path.Root("attribute"), ConfigValue: value,
	}, response)
	return response.Diagnostics.HasError()
}

// TestNonBlankMatchesTrimSpace is the point of implementing this validator
// rather than reaching for RegexMatches: the characters below are blank to
// strings.TrimSpace but are matched by RE2's \\S, so a regex spelling accepted
// them. The loop asserts the equivalence across every White_Space code point
// rather than a chosen sample.
func TestNonBlankMatchesTrimSpace(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"\v", "\u0085", "\u00a0", "\u1680", "\u2000", "\u2003", "\u2028", "\u2029",
		"\u202f", "\u205f", "\u3000", " ", "\t", "\n", "", "  \u3000  ",
	} {
		if !rejects(types.StringValue(value)) {
			t.Errorf("value %q is blank to strings.TrimSpace but was accepted", value)
		}
	}

	for r := rune(0); r <= unicode.MaxRune; r++ {
		if !unicode.IsSpace(r) {
			continue
		}
		value := string(r)
		if strings.TrimSpace(value) != "" {
			t.Fatalf("test premise broken: %q is not blank to TrimSpace", value)
		}
		if !rejects(types.StringValue(value)) {
			t.Errorf("White_Space rune %U was accepted", r)
		}
	}

	for _, value := range []string{"x", "  x  ", "\u3000x", "0"} {
		if rejects(types.StringValue(value)) {
			t.Errorf("value %q holds a non-space character but was rejected", value)
		}
	}

	if rejects(types.StringNull()) {
		t.Error("a null value must be left to Terraform")
	}
	if rejects(types.StringUnknown()) {
		t.Error("an unknown value must be left to Terraform")
	}
}
