package organization

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// namedValue pairs a schema attribute name with its configured value so
// validation diagnostics can name the attribute that is at fault.
type namedValue struct {
	name  string
	value types.String
}

// knownString reports whether a value is set and can be inspected. Null and
// unknown values are left to Terraform and to the API.
func knownString(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown()
}

// validateNonEmpty reports every attribute that is set to blank or whitespace.
// The API rejects these with an opaque 400, so catching them during validation
// keeps the error next to the offending attribute.
func validateNonEmpty(summary string, values ...namedValue) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	for _, item := range values {
		if knownString(item.value) && strings.TrimSpace(item.value.ValueString()) == "" {
			diagnostics.AddError(summary, fmt.Sprintf("%s must not be empty.", item.name))
		}
	}
	return diagnostics
}

// validateResourceARI checks the shape shared by every resource-scoped role
// assignment. The set of valid ARIs is not enumerable client side, so only the
// scheme prefix is checked and the API decides the rest.
func validateResourceARI(summary string, resource types.String) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if knownString(resource) && !strings.HasPrefix(resource.ValueString(), "ari:cloud:") {
		diagnostics.AddError(summary, "resource must be an Atlassian cloud resource identifier beginning with ari:cloud:.")
	}
	return diagnostics
}
