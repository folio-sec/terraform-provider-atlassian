package organization

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// nonBlank rejects a value that is set to blank or whitespace. The API answers
// those with an opaque 400, so catching them during validation keeps the error
// next to the offending attribute.
var nonBlank = stringvalidator.RegexMatches(regexp.MustCompile(`\S`), "must not be empty")

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

// runStringValidators and runSetValidators apply an attribute's own validators
// to a value the framework will not validate for us. Terraform runs attribute
// validators against configuration only, so import identity values would
// otherwise need a second, hand-written copy of the same rules; AGENTS.md
// requires identity to be validated by the same rules as configuration, and
// sharing the validator list is the only way to keep one source of truth.
//
// Only self-contained validators may go in these lists. The replayed request
// carries a value and a path but no configuration, so a validator that
// resolves other attributes -- ConflictsWith, ExactlyOneOf, AlsoRequires --
// dereferences a nil config and panics rather than returning a diagnostic.
// Rules that need to read a second attribute stay hand-written.
func runStringValidators(ctx context.Context, attribute path.Path, value types.String, validators []validator.String) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	for _, item := range validators {
		response := &validator.StringResponse{}
		item.ValidateString(ctx, validator.StringRequest{Path: attribute, ConfigValue: value}, response)
		diagnostics.Append(response.Diagnostics...)
	}
	return diagnostics
}

func runSetValidators(ctx context.Context, attribute path.Path, value types.Set, validators []validator.Set) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	for _, item := range validators {
		response := &validator.SetResponse{}
		item.ValidateSet(ctx, validator.SetRequest{Path: attribute, ConfigValue: value}, response)
		diagnostics.Append(response.Diagnostics...)
	}
	return diagnostics
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
		diagnostics.Append(runStringValidators(ctx, path.Root(item.attribute), item.value, item.validators)...)
	}
	return diagnostics
}
