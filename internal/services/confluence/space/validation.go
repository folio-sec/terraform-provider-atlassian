package space

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"

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

// principalPaths names where each part of a principal-scoped identity lives.
// The resources nest the principal in an object in state, while their import
// identity schemas are flat, so a diagnostic has to use the layout of whichever
// value is being validated or it points at an attribute that does not exist.
type principalPaths struct {
	spaceID, principalType, principalID path.Path
}

// importIdentityPaths is the flat layout both principal-scoped identity schemas
// share. A string import ID splits into the same three parts, so it reports at
// these paths too.
var importIdentityPaths = principalPaths{
	spaceID:       path.Root("space_id"),
	principalType: path.Root("principal_type"),
	principalID:   path.Root("principal_id"),
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
