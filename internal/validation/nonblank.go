// Package validation holds validators this provider implements itself, for
// rules the framework's validator library does not express.
package validation

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/helpers/validatordiag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// NonBlank rejects a value that is present but holds only whitespace. Every
// Atlassian endpoint this provider writes to stores such a value verbatim and
// answers later requests with an opaque 400, so catching it during validation
// keeps the error next to the attribute.
//
// This is implemented rather than spelled as
// stringvalidator.RegexMatches(regexp.MustCompile(`\S`), ...), because RE2's
// \s is only [\t\n\f\r ]. That regex accepts a vertical tab, a next-line, a
// non-breaking space and an ideographic space, all of which unicode.IsSpace
// and therefore strings.TrimSpace treat as blank — and an ideographic space is
// an ordinary copy-paste artefact from Japanese-language Atlassian
// documentation. Deferring to strings.TrimSpace keeps this equivalent to the
// hand-written checks it replaced by construction, rather than by a character
// class someone has to keep in step with unicode.IsSpace.
var NonBlank validator.String = nonBlankValidator{}

type nonBlankValidator struct{}

func (nonBlankValidator) Description(_ context.Context) string { return "must not be empty" }

func (v nonBlankValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v nonBlankValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if strings.TrimSpace(req.ConfigValue.ValueString()) != "" {
		return
	}
	resp.Diagnostics.Append(validatordiag.InvalidAttributeValueDiagnostic(
		req.Path, v.Description(ctx), req.ConfigValue.ValueString(),
	))
}
