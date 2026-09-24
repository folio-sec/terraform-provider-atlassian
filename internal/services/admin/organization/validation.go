package organization

import (
	"context"
	"regexp"

	"github.com/folio-sec/terraform-provider-atlassian/internal/validation"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// resourceARI checks the shape shared by every resource-scoped role
// assignment. The set of valid ARIs is not enumerable client side, so only the
// scheme prefix is checked and the API decides the rest.
var resourceARI = stringvalidator.RegexMatches(
	regexp.MustCompile(`^ari:cloud:`),
	"must be an Atlassian cloud resource identifier beginning with ari:cloud:",
)

// knownString reports whether a value is set and can be inspected. Null and
// unknown values are left to Terraform and to the API.
func knownString(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown()
}

// identityStringValidators names each attribute a type validates and the rules
// that apply to it, so the schema and the identity path share one definition.
type identityStringValidators struct {
	attribute  string
	value      types.String
	validators []validator.String
}

func runIdentityStringValidators(ctx context.Context, values ...identityStringValidators) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	for _, item := range values {
		diagnostics.Append(validation.RunString(ctx, path.Root(item.attribute), item.value, item.validators)...)
	}
	return diagnostics
}
