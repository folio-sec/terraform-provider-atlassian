package space

import (
	"context"
	"fmt"
	"strings"

	v2gen "github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence/v2/generated"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
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
