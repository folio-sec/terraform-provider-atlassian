package policy

import (
	"errors"
	"net/http"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

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
