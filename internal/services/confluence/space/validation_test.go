package space

import (
	"context"

	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The helpers below let a test assert that Terraform would refuse a
// configuration without depending on whether the rule lives in ValidateConfig
// or in a schema attribute's own validators. The framework runs attribute
// validators inside its own request pipeline, which a unit test cannot enter,
// so the schema is walked here instead.

func resourceSchemaStringDiagnostics(ctx context.Context, config tfsdk.Config, attributes map[string]resourceschema.Attribute, parent *path.Path) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	for name, attribute := range attributes {
		attributePath := attributeChildPath(parent, name)
		switch typed := attribute.(type) {
		case resourceschema.StringAttribute:
			diagnostics.Append(configStringDiagnostics(ctx, config, attributePath, typed.Validators)...)
		case resourceschema.SingleNestedAttribute:
			diagnostics.Append(resourceSchemaStringDiagnostics(ctx, config, typed.Attributes, &attributePath)...)
		}
	}
	return diagnostics
}

func datasourceSchemaStringDiagnostics(ctx context.Context, config tfsdk.Config, attributes map[string]datasourceschema.Attribute, parent *path.Path) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	for name, attribute := range attributes {
		attributePath := attributeChildPath(parent, name)
		switch typed := attribute.(type) {
		case datasourceschema.StringAttribute:
			diagnostics.Append(configStringDiagnostics(ctx, config, attributePath, typed.Validators)...)
		case datasourceschema.SingleNestedAttribute:
			diagnostics.Append(datasourceSchemaStringDiagnostics(ctx, config, typed.Attributes, &attributePath)...)
		}
	}
	return diagnostics
}

func attributeChildPath(parent *path.Path, name string) path.Path {
	if parent == nil {
		return path.Root(name)
	}
	return parent.AtName(name)
}

func configStringDiagnostics(ctx context.Context, config tfsdk.Config, attribute path.Path, validators []validator.String) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if len(validators) == 0 {
		return diagnostics
	}
	var value types.String
	if readDiagnostics := config.GetAttribute(ctx, attribute, &value); readDiagnostics.HasError() {
		return diagnostics
	}
	return runStringValidators(ctx, attribute, value, validators)
}
