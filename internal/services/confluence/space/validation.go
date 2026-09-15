package space

import (
	"fmt"
	"strings"

	v2gen "github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence/v2/generated"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// knownString reports whether a value is set and can be inspected. Null and
// unknown values are left to Terraform and to the API, mirroring
// internal/services/admin/organization/validation.go.
func knownString(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown()
}

// namedValue pairs a schema attribute name with its configured value so
// validation diagnostics can name the attribute that is at fault.
type namedValue struct {
	name  string
	value types.String
}

// validateNonEmpty reports every attribute that is set to blank or whitespace.
func validateNonEmpty(summary string, values ...namedValue) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	for _, item := range values {
		if knownString(item.value) && strings.TrimSpace(item.value.ValueString()) == "" {
			diagnostics.AddError(summary, fmt.Sprintf("%s must not be empty.", item.name))
		}
	}
	return diagnostics
}

// validateSpaceType reports an error when a configured type filter is not one
// of the values getSpaces accepts, reusing the generated enum's own Valid
// method rather than duplicating the value list.
func validateSpaceType(summary string, value types.String) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if knownString(value) && strings.TrimSpace(value.ValueString()) != "" {
		if !v2gen.GetSpacesParamsType(value.ValueString()).Valid() {
			diagnostics.AddError(summary, fmt.Sprintf("type %q is not a known space type.", value.ValueString()))
		}
	}
	return diagnostics
}

// validateSpaceStatus reports an error when a configured status filter is not
// one of the values getSpaces accepts.
func validateSpaceStatus(summary string, value types.String) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if knownString(value) && strings.TrimSpace(value.ValueString()) != "" {
		if !v2gen.GetSpacesParamsStatus(value.ValueString()).Valid() {
			diagnostics.AddError(summary, fmt.Sprintf("status %q is not a known space status.", value.ValueString()))
		}
	}
	return diagnostics
}
