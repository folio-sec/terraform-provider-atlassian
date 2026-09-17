package policy

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// nonBlank rejects a value that is set to blank or whitespace, which the API
// answers with an opaque 400.
var nonBlank = stringvalidator.RegexMatches(regexp.MustCompile(`\S`), "must not be empty")

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
