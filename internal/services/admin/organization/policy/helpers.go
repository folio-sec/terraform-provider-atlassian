package policy

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type namedValue struct {
	name  string
	value types.String
}

func validateNonEmpty(summary string, values ...namedValue) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	for _, item := range values {
		if !item.value.IsNull() && !item.value.IsUnknown() && strings.TrimSpace(item.value.ValueString()) == "" {
			diagnostics.AddError(summary, fmt.Sprintf("%s must not be empty.", item.name))
		}
	}
	return diagnostics
}

func nullableStringValue(value *string) types.String {
	if value == nil {
		return types.StringNull()
	}
	return types.StringValue(*value)
}

func mutationOutcomeMayBeAmbiguous(err error) bool {
	var httpErr *admin.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode >= http.StatusInternalServerError
	}
	return true
}

func readOutcomeMayBeTransient(err error) bool {
	var httpErr *admin.HTTPError
	if !errors.As(err, &httpErr) {
		return true
	}
	return httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode >= http.StatusInternalServerError
}
