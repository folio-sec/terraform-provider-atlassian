package space

import (
	"context"
	"testing"

	"github.com/folio-sec/terraform-provider-atlassian/internal/validation"

	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
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
		case resourceschema.SetNestedAttribute:
			for _, elementPath := range setElementPaths(ctx, config, attributePath) {
				diagnostics.Append(resourceSchemaStringDiagnostics(ctx, config, typed.NestedObject.Attributes, &elementPath)...)
			}
		case resourceschema.ListNestedAttribute:
			for _, elementPath := range listElementPaths(ctx, config, attributePath) {
				diagnostics.Append(resourceSchemaStringDiagnostics(ctx, config, typed.NestedObject.Attributes, &elementPath)...)
			}
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
		case datasourceschema.SetNestedAttribute:
			for _, elementPath := range setElementPaths(ctx, config, attributePath) {
				diagnostics.Append(datasourceSchemaStringDiagnostics(ctx, config, typed.NestedObject.Attributes, &elementPath)...)
			}
		case datasourceschema.ListNestedAttribute:
			for _, elementPath := range listElementPaths(ctx, config, attributePath) {
				diagnostics.Append(datasourceSchemaStringDiagnostics(ctx, config, typed.NestedObject.Attributes, &elementPath)...)
			}
		}
	}
	return diagnostics
}

// setElementPaths and listElementPaths return the path of every configured
// element of a nested collection, so the walk reaches rules declared on the
// attributes inside it. A null or unknown collection has no elements to check.
func setElementPaths(ctx context.Context, config tfsdk.Config, attribute path.Path) []path.Path {
	var set types.Set
	if diagnostics := config.GetAttribute(ctx, attribute, &set); diagnostics.HasError() || set.IsNull() || set.IsUnknown() {
		return nil
	}
	paths := make([]path.Path, 0, len(set.Elements()))
	for _, element := range set.Elements() {
		paths = append(paths, attribute.AtSetValue(element))
	}
	return paths
}

func listElementPaths(ctx context.Context, config tfsdk.Config, attribute path.Path) []path.Path {
	var list types.List
	if diagnostics := config.GetAttribute(ctx, attribute, &list); diagnostics.HasError() || list.IsNull() || list.IsUnknown() {
		return nil
	}
	paths := make([]path.Path, 0, len(list.Elements()))
	for index := range list.Elements() {
		paths = append(paths, attribute.AtListIndex(index))
	}
	return paths
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
	return validation.RunString(ctx, attribute, value, validators)
}

// The resources below carry every configuration rule on their schema
// attributes: their ValidateConfig is gone, and the identity functions read
// the shared validator lists directly, so nothing else notices if an
// attribute stops referencing them. These tests read the attribute out of the
// schema the resource actually builds.

func schemaStringAttribute(t *testing.T, attributes map[string]resourceschema.Attribute, name string) resourceschema.StringAttribute {
	t.Helper()
	attribute, ok := attributes[name].(resourceschema.StringAttribute)
	if !ok {
		t.Fatalf("%s attribute type = %T, want schema.StringAttribute", name, attributes[name])
	}
	return attribute
}

func rejectsThrough(t *testing.T, attribute resourceschema.StringAttribute, name, value string) bool {
	t.Helper()
	return validation.RunString(context.Background(), path.Root(name), types.StringValue(value), attribute.Validators).HasError()
}

func TestRoleAssignmentSchemaCarriesItsRules(t *testing.T) {
	t.Parallel()
	response := &resource.SchemaResponse{}
	(&roleAssignmentResource{}).Schema(context.Background(), resource.SchemaRequest{}, response)
	attributes := response.Schema.Attributes
	principal, ok := attributes["principal"].(resourceschema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("principal attribute type = %T", attributes["principal"])
	}

	for _, testCase := range []struct {
		name      string
		attribute resourceschema.StringAttribute
		value     string
		wantError bool
	}{
		{name: "numeric space_id", attribute: schemaStringAttribute(t, attributes, "space_id"), value: "12345"},
		{name: "non-numeric space_id", attribute: schemaStringAttribute(t, attributes, "space_id"), value: "abc", wantError: true},
		{name: "blank space_id", attribute: schemaStringAttribute(t, attributes, "space_id"), value: " ", wantError: true},
		{name: "role_id", attribute: schemaStringAttribute(t, attributes, "role_id"), value: "role-1"},
		{name: "blank role_id", attribute: schemaStringAttribute(t, attributes, "role_id"), value: "  ", wantError: true},
		{name: "GROUP principal", attribute: schemaStringAttribute(t, principal.Attributes, "principal_type"), value: "GROUP"},
		{name: "USER principal", attribute: schemaStringAttribute(t, principal.Attributes, "principal_type"), value: "USER"},
		{name: "ACCESS_CLASS principal", attribute: schemaStringAttribute(t, principal.Attributes, "principal_type"), value: "ACCESS_CLASS", wantError: true},
		{name: "blank principal_id", attribute: schemaStringAttribute(t, principal.Attributes, "principal_id"), value: " ", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := rejectsThrough(t, testCase.attribute, "attribute", testCase.value); got != testCase.wantError {
				t.Fatalf("rejected = %t, want %t", got, testCase.wantError)
			}
		})
	}
}

func TestPrincipalPermissionsSchemaCarriesItsRules(t *testing.T) {
	t.Parallel()
	response := &resource.SchemaResponse{}
	(&principalPermissionsResource{}).Schema(context.Background(), resource.SchemaRequest{}, response)
	attributes := response.Schema.Attributes
	principal, ok := attributes["principal"].(resourceschema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("principal attribute type = %T", attributes["principal"])
	}

	for _, testCase := range []struct {
		name      string
		attribute resourceschema.StringAttribute
		value     string
		wantError bool
	}{
		{name: "numeric space_id", attribute: schemaStringAttribute(t, attributes, "space_id"), value: "12345"},
		{name: "non-numeric space_id", attribute: schemaStringAttribute(t, attributes, "space_id"), value: "KEY", wantError: true},
		{name: "space_key", attribute: schemaStringAttribute(t, attributes, "space_key"), value: "DEMO"},
		{name: "blank space_key", attribute: schemaStringAttribute(t, attributes, "space_key"), value: " ", wantError: true},
		{name: "user principal", attribute: schemaStringAttribute(t, principal.Attributes, "type"), value: "user"},
		{name: "group principal", attribute: schemaStringAttribute(t, principal.Attributes, "type"), value: "group"},
		{name: "uppercase principal", attribute: schemaStringAttribute(t, principal.Attributes, "type"), value: "USER", wantError: true},
		{name: "blank principal id", attribute: schemaStringAttribute(t, principal.Attributes, "id"), value: " ", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := rejectsThrough(t, testCase.attribute, "attribute", testCase.value); got != testCase.wantError {
				t.Fatalf("rejected = %t, want %t", got, testCase.wantError)
			}
		})
	}
}

// importDiagnosticPaths runs ImportState for subject and returns the attribute
// path of every error diagnostic, so a test can check that an invalid import
// is reported against the identity schema rather than resource state.
func importDiagnosticPaths(t *testing.T, subject resource.ResourceWithImportState, identity any, id string) []path.Path {
	t.Helper()
	ctx := context.Background()
	withIdentity, ok := subject.(resource.ResourceWithIdentity)
	if !ok {
		t.Fatalf("%T does not implement resource identity", subject)
	}
	var schemaResponse resource.SchemaResponse
	subject.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)
	var identityResponse resource.IdentitySchemaResponse
	withIdentity.IdentitySchema(ctx, resource.IdentitySchemaRequest{}, &identityResponse)
	identityType := identityResponse.IdentitySchema.Type().TerraformType(ctx)

	request := resource.ImportStateRequest{ID: id}
	if identity != nil {
		requestIdentity := &tfsdk.ResourceIdentity{Raw: tftypes.NewValue(identityType, nil), Schema: identityResponse.IdentitySchema}
		if diagnostics := requestIdentity.Set(ctx, identity); diagnostics.HasError() {
			t.Fatalf("build identity: %v", diagnostics)
		}
		request.Identity = requestIdentity
	}
	response := &resource.ImportStateResponse{
		State:    tfsdk.State{Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(ctx), nil), Schema: schemaResponse.Schema},
		Identity: &tfsdk.ResourceIdentity{Raw: tftypes.NewValue(identityType, nil), Schema: identityResponse.IdentitySchema},
	}
	subject.ImportState(ctx, request, response)

	var paths []path.Path
	for _, diagnostic := range response.Diagnostics.Errors() {
		if withPath, ok := diagnostic.(diag.DiagnosticWithPath); ok {
			paths = append(paths, withPath.Path())
		}
	}
	return paths
}

// The principal-scoped resources nest the principal in state, but their import
// identities are flat, so an invalid import has to name the flat attribute.
func TestPrincipalImportsReportAtIdentityPaths(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name     string
		subject  resource.ResourceWithImportState
		identity any
		id       string
	}{
		{
			name:     "role assignment identity",
			subject:  &roleAssignmentResource{},
			identity: &roleAssignmentIdentity{SpaceID: types.StringValue("123"), PrincipalType: types.StringValue("OTHER"), PrincipalID: types.StringValue("abc")},
		},
		{name: "role assignment string id", subject: &roleAssignmentResource{}, id: "123,OTHER,abc"},
		{
			name:     "custom access identity",
			subject:  &principalPermissionsResource{},
			identity: &principalPermissionsIdentity{SpaceID: types.StringValue("123"), PrincipalType: types.StringValue("USER"), PrincipalID: types.StringValue("abc")},
		},
		{name: "custom access string id", subject: &principalPermissionsResource{}, id: "123,USER,abc"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			paths := importDiagnosticPaths(t, testCase.subject, testCase.identity, testCase.id)
			want := path.Root("principal_type")
			if len(paths) != 1 || !paths[0].Equal(want) {
				t.Fatalf("error paths = %v, want exactly %s", paths, want)
			}
		})
	}
}
