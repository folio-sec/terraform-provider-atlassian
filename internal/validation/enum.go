package validation

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/helpers/validatordiag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// generatedEnum is the shape oapi-codegen gives a string enum: a named string
// type with a Valid method listing the values the specification declares.
type generatedEnum interface {
	~string
	Valid() bool
}

// Enum rejects a value that the generated client's own enum does not accept.
//
// It asks the enum's Valid method instead of repeating its values in a
// stringvalidator.OneOf list, because AGENTS.md forbids duplicating a
// generated value list: after the client is regenerated with a new value, a
// copied list would keep rejecting it until someone noticed. The trade-off is
// that the diagnostic cannot name the accepted values, since Valid is a switch
// rather than a list, so an attribute's description should name them where
// that helps the operator.
func Enum[T generatedEnum]() validator.String { return enumValidator[T]{} }

type enumValidator[T generatedEnum] struct{}

func (enumValidator[T]) Description(_ context.Context) string {
	return "must be a value the API accepts"
}

func (v enumValidator[T]) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v enumValidator[T]) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if T(req.ConfigValue.ValueString()).Valid() {
		return
	}
	resp.Diagnostics.Append(validatordiag.InvalidAttributeValueMatchDiagnostic(
		req.Path, v.Description(ctx), req.ConfigValue.ValueString(),
	))
}
