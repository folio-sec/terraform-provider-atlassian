package validation

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// RunString and RunSet apply an attribute's own validators to a value the
// framework will not validate for us. Terraform runs attribute validators
// against configuration only, so import identity values would otherwise need a
// second, hand-written copy of the same rules; AGENTS.md requires identity to
// be validated by the same rules as configuration, and sharing the validator
// list is the only way to keep one source of truth.
//
// Only self-contained validators may be replayed. The request carries a value
// and a path but no configuration, so a validator that resolves other
// attributes -- ConflictsWith, ExactlyOneOf, AlsoRequires -- dereferences a nil
// config and panics rather than returning a diagnostic. Rules that need to read
// a second attribute stay hand-written.
func RunString(ctx context.Context, attribute path.Path, value types.String, validators []validator.String) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	for _, item := range validators {
		response := &validator.StringResponse{}
		item.ValidateString(ctx, validator.StringRequest{Path: attribute, ConfigValue: value}, response)
		diagnostics.Append(response.Diagnostics...)
	}
	return diagnostics
}

func RunSet(ctx context.Context, attribute path.Path, value types.Set, validators []validator.Set) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	for _, item := range validators {
		response := &validator.SetResponse{}
		item.ValidateSet(ctx, validator.SetRequest{Path: attribute, ConfigValue: value}, response)
		diagnostics.Append(response.Diagnostics...)
	}
	return diagnostics
}
