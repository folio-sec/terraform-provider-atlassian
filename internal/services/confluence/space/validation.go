package space

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// runStringValidators applies an attribute's own validators to a value the
// framework will not validate for us. Terraform runs attribute validators
// against configuration only, so import identity values would otherwise need a
// second, hand-written copy of the same rules; AGENTS.md requires identity to
// be validated by the same rules as configuration, and sharing the validator
// list is the only way to keep one source of truth.
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

// knownString reports whether a value is set and can be inspected. Null and
// unknown values are left to Terraform and to the API, mirroring
// internal/services/admin/organization/validation.go.
func knownString(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown()
}

func principalImportIDParts(id string) ([3]string, error) {
	parts := strings.Split(id, ",")
	if len(parts) != 3 {
		return [3]string{}, fmt.Errorf("expected space_id,principal_type,principal_id")
	}
	return [3]string{
		strings.TrimSpace(parts[0]),
		strings.TrimSpace(parts[1]),
		strings.TrimSpace(parts[2]),
	}, nil
}

func parsePrincipalImport[Identity any](
	ctx context.Context,
	req resource.ImportStateRequest,
	resp *resource.ImportStateResponse,
	identity *Identity,
	fromParts func([3]string) Identity,
	validate func(Identity) diag.Diagnostics,
) bool {
	switch {
	case req.ID != "":
		parts, err := principalImportIDParts(req.ID)
		if err != nil {
			resp.Diagnostics.AddError("Invalid import identifier", err.Error())
			return false
		}
		*identity = fromParts(parts)
	case req.Identity != nil:
		resp.Diagnostics.Append(req.Identity.Get(ctx, identity)...)
	default:
		resp.Diagnostics.AddError("Invalid import identifier", "Expected either a string ID or a resource identity.")
		return false
	}
	resp.Diagnostics.Append(validate(*identity)...)
	return !resp.Diagnostics.HasError()
}
