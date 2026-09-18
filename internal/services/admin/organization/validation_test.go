package organization

import (
	"context"
	"errors"
	"github.com/folio-sec/terraform-provider-atlassian/internal/validation"
	"net/http"
	"testing"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestUserDataSourceSchemaReturnsUsersAsSet(t *testing.T) {
	t.Parallel()

	var response datasource.SchemaResponse
	NewUsersDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", response.Diagnostics)
	}
	if _, exists := response.Schema.Attributes["id"]; exists {
		t.Fatal("schema contains an unexpected synthetic id attribute")
	}
	if _, exists := response.Schema.Attributes["user"]; exists {
		t.Fatal("schema contains the obsolete singular user attribute")
	}
	users, ok := response.Schema.Attributes["users"].(datasourceschema.SetNestedAttribute)
	if !ok {
		t.Fatalf("users attribute type = %T, want schema.SetNestedAttribute", response.Schema.Attributes["users"])
	}
	if !users.Computed {
		t.Fatal("users attribute must be computed")
	}
}

func TestGroupDataSourceSchemaReturnsGroupsAsSet(t *testing.T) {
	t.Parallel()

	var response datasource.SchemaResponse
	NewGroupsDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", response.Diagnostics)
	}
	groups, ok := response.Schema.Attributes["groups"].(datasourceschema.SetNestedAttribute)
	if !ok {
		t.Fatalf("groups attribute type = %T, want schema.SetNestedAttribute", response.Schema.Attributes["groups"])
	}
	if !groups.Computed {
		t.Fatal("groups attribute must be computed")
	}
	if _, exists := response.Schema.Attributes["sort_by"]; exists {
		t.Fatal("schema exposes response ordering control sort_by")
	}
	if _, exists := response.Schema.Attributes["expand"]; exists {
		t.Fatal("schema exposes response shaping control expand")
	}
}

func TestGroupDetailsDataSourceSchema(t *testing.T) {
	t.Parallel()

	var metadata datasource.MetadataResponse
	NewGroupDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "atlassian"}, &metadata)
	if metadata.TypeName != "atlassian_organization_group" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}
	var response datasource.SchemaResponse
	NewGroupDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", response.Diagnostics)
	}
	for _, name := range []string{"organization_id", "directory_id", "group_id"} {
		if attribute := response.Schema.Attributes[name]; attribute == nil || !attribute.IsRequired() {
			t.Errorf("%s is not required", name)
		}
	}
	for _, name := range []string{"id", "name", "description", "external_synced", "managed_by", "management_access"} {
		if attribute := response.Schema.Attributes[name]; attribute == nil || !attribute.IsComputed() {
			t.Errorf("%s is not computed", name)
		}
	}
}

func TestUserDetailsDataSourceSchema(t *testing.T) {
	t.Parallel()

	var metadata datasource.MetadataResponse
	NewUserDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "atlassian"}, &metadata)
	if metadata.TypeName != "atlassian_organization_user" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}
	var response datasource.SchemaResponse
	NewUserDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", response.Diagnostics)
	}
	for _, name := range []string{"organization_id", "directory_id", "account_id"} {
		if attribute := response.Schema.Attributes[name]; attribute == nil || !attribute.IsRequired() {
			t.Errorf("%s is not required", name)
		}
	}
	for _, name := range []string{"id", "name", "email", "deactivated_on", "platform_roles"} {
		if attribute := response.Schema.Attributes[name]; attribute == nil || !attribute.IsComputed() {
			t.Errorf("%s is not computed", name)
		}
	}
}

func TestValidateOrganizationUserIdentifiers(t *testing.T) {
	t.Parallel()

	if diagnostics := validateOrganizationUserIdentifiers(context.Background(), types.StringValue("org"), types.StringValue("directory"), types.StringValue("712020:account")); diagnostics.HasError() {
		t.Fatalf("valid identifiers returned diagnostics: %v", diagnostics)
	}
	for _, values := range [][3]types.String{
		{types.StringValue(" "), types.StringValue("directory"), types.StringValue("account")},
		{types.StringValue("org"), types.StringValue(""), types.StringValue("account")},
		{types.StringValue("org"), types.StringValue("directory"), types.StringValue("\t")},
	} {
		if diagnostics := validateOrganizationUserIdentifiers(context.Background(), values[0], values[1], values[2]); !diagnostics.HasError() {
			t.Fatalf("invalid identifiers %#v returned no diagnostics", values)
		}
	}
	if diagnostics := validateOrganizationUserIdentifiers(context.Background(), types.StringUnknown(), types.StringUnknown(), types.StringUnknown()); diagnostics.HasError() {
		t.Fatalf("unknown identifiers returned diagnostics: %v", diagnostics)
	}
}

func TestCollectionDataSourcesUsePluralTypeNames(t *testing.T) {
	t.Parallel()

	for want, subject := range map[string]datasource.DataSource{
		"atlassian_organization_groups": NewGroupsDataSource(),
		"atlassian_organization_users":  NewUsersDataSource(),
	} {
		var response datasource.MetadataResponse
		subject.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "atlassian"}, &response)
		if response.TypeName != want {
			t.Errorf("type name = %q, want %q", response.TypeName, want)
		}
	}
}

func TestUserRoleAssignmentIdentitySchema(t *testing.T) {
	t.Parallel()

	var response resource.IdentitySchemaResponse
	NewUserRoleAssignmentResource().(resource.ResourceWithIdentity).IdentitySchema(context.Background(), resource.IdentitySchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("IdentitySchema() diagnostics = %v", response.Diagnostics)
	}
	wantAttributes := []string{"organization_id", "directory_id", "account_id", "resource", "role"}
	if len(response.IdentitySchema.Attributes) != len(wantAttributes) {
		t.Fatalf("identity attribute count = %d, want %d", len(response.IdentitySchema.Attributes), len(wantAttributes))
	}
	for _, name := range wantAttributes {
		attribute, exists := response.IdentitySchema.Attributes[name]
		if !exists {
			t.Errorf("identity schema is missing %q", name)
			continue
		}
		if !attribute.IsRequiredForImport() {
			t.Errorf("identity attribute %q must be required for import", name)
		}
	}
}

func TestGroupMembershipIdentitySchema(t *testing.T) {
	t.Parallel()

	var response resource.IdentitySchemaResponse
	NewGroupMembershipResource().(resource.ResourceWithIdentity).IdentitySchema(context.Background(), resource.IdentitySchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", response.Diagnostics)
	}
	wantAttributes := []string{"organization_id", "directory_id", "group_id", "account_id"}
	if len(response.IdentitySchema.Attributes) != len(wantAttributes) {
		t.Fatalf("identity attribute count = %d, want %d", len(response.IdentitySchema.Attributes), len(wantAttributes))
	}
	for _, name := range wantAttributes {
		attribute, exists := response.IdentitySchema.Attributes[name]
		if !exists {
			t.Errorf("identity schema is missing %q", name)
			continue
		}
		if !attribute.IsRequiredForImport() {
			t.Errorf("identity attribute %q must be required for import", name)
		}
	}
}

func TestParseGroupMembershipImportID(t *testing.T) {
	t.Parallel()

	identity, err := parseGroupMembershipImportID(" org , directory , group , 712020:account ")
	if err != nil {
		t.Fatalf("parseGroupMembershipImportID() error = %v", err)
	}
	if got := identity.GroupID.ValueString(); got != "group" {
		t.Errorf("group_id = %q, want %q", got, "group")
	}
	if got := identity.AccountID.ValueString(); got != "712020:account" {
		t.Errorf("account_id = %q, want %q", got, "712020:account")
	}

	invalid := []string{
		"org,directory,group",
		"org,directory,,account",
	}
	for _, id := range invalid {
		if _, err := parseGroupMembershipImportID(id); err == nil {
			t.Errorf("parseGroupMembershipImportID(%q) returned no error", id)
		}
	}
}

func TestParseAssignmentImportID(t *testing.T) {
	t.Parallel()

	identity, err := parseAssignmentImportID(" org , directory , account , ari:cloud:jira::site/site , atlassian/user ")
	if err != nil {
		t.Fatalf("parseAssignmentImportID() error = %v", err)
	}
	if got := identity.OrganizationID.ValueString(); got != "org" {
		t.Errorf("organization_id = %q, want %q", got, "org")
	}
	if got := identity.Resource.ValueString(); got != "ari:cloud:jira::site/site" {
		t.Errorf("resource = %q, want %q", got, "ari:cloud:jira::site/site")
	}
	if got := identity.Role.ValueString(); got != "atlassian/user" {
		t.Errorf("role = %q, want %q", got, "atlassian/user")
	}

	invalid := []string{
		"org,directory,account,resource",
		"org,directory,,resource,role",
	}
	for _, id := range invalid {
		if _, err := parseAssignmentImportID(id); err == nil {
			t.Errorf("parseAssignmentImportID(%q) returned no error", id)
		}
	}
}

func TestUserRoleAssignmentImportStateByStringID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	subject := &userRoleAssignmentResource{}
	requestIdentity, response := newUserRoleAssignmentImportData(t, ctx, subject)
	subject.ImportState(ctx, resource.ImportStateRequest{
		ID:       "org,directory,12345678-1234-1234-1234-123456789012,ari:cloud:jira::site/site,atlassian/user",
		Identity: requestIdentity,
	}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("ImportState() diagnostics = %v", response.Diagnostics)
	}

	var state userRoleAssignmentResourceModel
	response.Diagnostics.Append(response.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		t.Fatalf("State.Get() diagnostics = %v", response.Diagnostics)
	}
	if got := state.OrganizationID.ValueString(); got != "org" {
		t.Errorf("organization_id = %q, want %q", got, "org")
	}
	if got := state.Role.ValueString(); got != "atlassian/user" {
		t.Errorf("role = %q, want %q", got, "atlassian/user")
	}
}

func TestUserRoleAssignmentImportStateByIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	subject := &userRoleAssignmentResource{}
	requestIdentity, response := newUserRoleAssignmentImportData(t, ctx, subject)
	identity := userRoleAssignmentResourceIdentityModel{
		OrganizationID: types.StringValue("org"),
		DirectoryID:    types.StringValue("directory"),
		AccountID:      types.StringValue("12345678-1234-1234-1234-123456789012"),
		Resource:       types.StringValue("ari:cloud:jira::site/site"),
		Role:           types.StringValue("atlassian/user"),
	}
	if diagnostics := requestIdentity.Set(ctx, &identity); diagnostics.HasError() {
		t.Fatalf("Identity.Set() diagnostics = %v", diagnostics)
	}
	response.Identity.Raw = requestIdentity.Raw.Copy()

	subject.ImportState(ctx, resource.ImportStateRequest{Identity: requestIdentity}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("ImportState() diagnostics = %v", response.Diagnostics)
	}

	var state userRoleAssignmentResourceModel
	response.Diagnostics.Append(response.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		t.Fatalf("State.Get() diagnostics = %v", response.Diagnostics)
	}
	if got := state.ID.ValueString(); got != "org,directory,12345678-1234-1234-1234-123456789012,ari:cloud:jira::site/site,atlassian/user" {
		t.Errorf("id = %q", got)
	}
}

func newUserRoleAssignmentImportData(t *testing.T, ctx context.Context, subject *userRoleAssignmentResource) (*tfsdk.ResourceIdentity, *resource.ImportStateResponse) {
	t.Helper()

	var schemaResponse resource.SchemaResponse
	subject.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", schemaResponse.Diagnostics)
	}
	var identitySchemaResponse resource.IdentitySchemaResponse
	subject.IdentitySchema(ctx, resource.IdentitySchemaRequest{}, &identitySchemaResponse)
	if identitySchemaResponse.Diagnostics.HasError() {
		t.Fatalf("IdentitySchema() diagnostics = %v", identitySchemaResponse.Diagnostics)
	}

	identityType := identitySchemaResponse.IdentitySchema.Type().TerraformType(ctx)
	requestIdentity := &tfsdk.ResourceIdentity{
		Raw:    tftypes.NewValue(identityType, nil),
		Schema: identitySchemaResponse.IdentitySchema,
	}
	return requestIdentity, &resource.ImportStateResponse{
		State: tfsdk.State{
			Raw:    tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(ctx), nil),
			Schema: schemaResponse.Schema,
		},
		Identity: &tfsdk.ResourceIdentity{
			Raw:    tftypes.NewValue(identityType, nil),
			Schema: identitySchemaResponse.IdentitySchema,
		},
	}
}

func TestValidateUserRoleAssignmentValues(t *testing.T) {
	t.Parallel()

	valid := userRoleAssignmentValues{
		OrganizationID: types.StringValue("org"),
		DirectoryID:    types.StringValue("directory"),
		AccountID:      types.StringValue("account"),
		Resource:       types.StringValue("ari:cloud:jira::site/site"),
		Role:           types.StringValue("atlassian/user"),
	}
	tests := map[string]struct {
		values     userRoleAssignmentValues
		wantErrors bool
	}{
		"valid": {
			values: valid,
		},
		"empty identity attribute": {
			values: func() userRoleAssignmentValues {
				values := valid
				values.DirectoryID = types.StringValue(" ")
				return values
			}(),
			wantErrors: true,
		},
		"invalid resource": {
			values: func() userRoleAssignmentValues {
				values := valid
				values.Resource = types.StringValue("jira-site")
				return values
			}(),
			wantErrors: true,
		},
		"unsupported role": {
			values: func() userRoleAssignmentValues {
				values := valid
				values.Role = types.StringValue("atlassian/org-admin")
				return values
			}(),
			wantErrors: true,
		},
		"unknown values": {
			values: userRoleAssignmentValues{
				OrganizationID: types.StringUnknown(),
				DirectoryID:    types.StringUnknown(),
				AccountID:      types.StringUnknown(),
				Resource:       types.StringUnknown(),
				Role:           types.StringUnknown(),
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diagnostics := validateUserRoleAssignmentValues(context.Background(), test.values)
			if diagnostics.HasError() != test.wantErrors {
				t.Fatalf("HasError() = %t, want %t; diagnostics = %v", diagnostics.HasError(), test.wantErrors, diagnostics)
			}
		})
	}
}

func TestNullableStringValue(t *testing.T) {
	t.Parallel()

	if got := nullableStringValue(nil); !got.IsNull() {
		t.Fatalf("nullableStringValue(nil) = %v, want null", got)
	}
	value := "invited"
	if got := nullableStringValue(&value); got.ValueString() != value {
		t.Fatalf("nullableStringValue() = %q", got.ValueString())
	}
}

// TestUserFilterSetValidators runs the validators the users data source
// schema declares, so removing one from an attribute fails the test.
func TestUserFilterSetValidators(t *testing.T) {
	t.Parallel()

	var response datasource.SchemaResponse
	NewUsersDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &response)
	statusAttribute, ok := response.Schema.Attributes["status"].(datasourceschema.SetAttribute)
	if !ok {
		t.Fatalf("status attribute type = %T, want schema.SetAttribute", response.Schema.Attributes["status"])
	}

	tests := map[string]struct {
		value      types.Set
		wantErrors bool
	}{
		"supported value": {
			value: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("active")}),
		},
		"empty": {
			value:      types.SetValueMust(types.StringType, nil),
			wantErrors: true,
		},
		"too many": {
			value: types.SetValueMust(types.StringType, []attr.Value{
				types.StringValue("active"), types.StringValue("suspended"), types.StringValue("not_invited"),
				types.StringValue("deactivated"), types.StringValue("for_deletion"),
			}),
			wantErrors: true,
		},
		"unsupported": {
			value:      types.SetValueMust(types.StringType, []attr.Value{types.StringValue("invalid")}),
			wantErrors: true,
		},
		"unknown element": {
			value: types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()}),
		},
		"null": {value: types.SetNull(types.StringType)},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diagnostics := runSetValidators(context.Background(), path.Root("status"), test.value, statusAttribute.Validators)
			if diagnostics.HasError() != test.wantErrors {
				t.Fatalf("HasError() = %t, want %t; diagnostics = %v", diagnostics.HasError(), test.wantErrors, diagnostics)
			}
		})
	}
}

func TestMutationOutcomeMayBeAmbiguous(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		want bool
	}{
		{err: &admin.HTTPError{StatusCode: http.StatusInternalServerError}, want: true},
		{err: &admin.HTTPError{StatusCode: http.StatusTooManyRequests}, want: false},
		{err: errors.New("connection reset"), want: true},
	}
	for _, test := range tests {
		if got := mutationOutcomeMayBeAmbiguous(test.err); got != test.want {
			t.Errorf("mutationOutcomeMayBeAmbiguous(%v) = %t, want %t", test.err, got, test.want)
		}
	}
}

func TestWorkspacesDataSourceSchemaReturnsWorkspacesAsSet(t *testing.T) {
	t.Parallel()

	var response datasource.SchemaResponse
	NewWorkspacesDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", response.Diagnostics)
	}
	workspaces, ok := response.Schema.Attributes["workspaces"].(datasourceschema.SetNestedAttribute)
	if !ok {
		t.Fatalf("workspaces attribute type = %T, want schema.SetNestedAttribute", response.Schema.Attributes["workspaces"])
	}
	if !workspaces.Computed {
		t.Fatal("workspaces attribute must be computed")
	}
	// The workspace ID is the resource ARI that role assignments refer to, so
	// it must be exposed rather than only accepted as a filter.
	for _, name := range []string{"id", "type_key", "name", "type", "status"} {
		if _, exists := workspaces.NestedObject.Attributes[name]; !exists {
			t.Errorf("workspace attribute %q is missing", name)
		}
	}
	if !response.Schema.Attributes["organization_id"].IsRequired() {
		t.Error("organization_id must be required")
	}
	// The endpoint takes its filters under a single query property, and the
	// provider mirrors that shape rather than flattening one operand out of it.
	query, ok := response.Schema.Attributes["query"].(datasourceschema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("query attribute type = %T, want schema.SingleNestedAttribute", response.Schema.Attributes["query"])
	}
	if !query.IsOptional() {
		t.Error("query must be optional")
	}
	for _, name := range []string{"search", "fields", "features"} {
		if _, exists := query.Attributes[name]; !exists {
			t.Errorf("query operand %q is missing", name)
		}
	}
	// The endpoint rejects a policies operand as an invalid request body even
	// when it carries a policy ID read back from the organization's own policy
	// list, so exposing it would ship an attribute that always fails.
	if _, exists := query.Attributes["policies"]; exists {
		t.Error("query exposes the policies operand, which the endpoint rejects")
	}
	if _, exists := response.Schema.Attributes["search"]; exists {
		t.Error("search must live under query, not at the top level")
	}
	// sort and limit shape the response rather than which workspaces match, so
	// they stay internal to the provider.
	for _, name := range []string{"sort", "limit", "cursor"} {
		if _, exists := response.Schema.Attributes[name]; exists {
			t.Errorf("schema exposes response-shaping attribute %q", name)
		}
		if _, exists := query.Attributes[name]; exists {
			t.Errorf("query exposes response-shaping attribute %q", name)
		}
	}
}

func TestSharedValueValidators(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, value types.String, validators []validator.String) bool {
		t.Helper()
		return runStringValidators(context.Background(), path.Root("attribute"), value, validators).HasError()
	}

	t.Run("non-empty", func(t *testing.T) {
		t.Parallel()
		tests := map[string]struct {
			value     types.String
			wantError bool
		}{
			"set":        {value: types.StringValue("value")},
			"blank":      {value: types.StringValue("   "), wantError: true},
			"empty":      {value: types.StringValue(""), wantError: true},
			"null":       {value: types.StringNull()},
			"unknown":    {value: types.StringUnknown()},
			"whitespace": {value: types.StringValue("\t\n"), wantError: true},
		}
		for name, test := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				if got := run(t, test.value, []validator.String{validation.NonBlank}); got != test.wantError {
					t.Fatalf("rejected = %t, wantError = %t", got, test.wantError)
				}
			})
		}
	})

	t.Run("resource ari", func(t *testing.T) {
		t.Parallel()
		tests := map[string]struct {
			value     types.String
			wantError bool
		}{
			"site ari":      {value: types.StringValue("ari:cloud:confluence::site/site-id")},
			"workspace ari": {value: types.StringValue("ari:cloud:studio::workspace/workspace-id")},
			"bare id":       {value: types.StringValue("site-id"), wantError: true},
			"other scheme":  {value: types.StringValue("ari:server:jira::site/site-id"), wantError: true},
			"null":          {value: types.StringNull()},
		}
		for name, test := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				if got := run(t, test.value, []validator.String{resourceARI}); got != test.wantError {
					t.Fatalf("rejected = %t, wantError = %t", got, test.wantError)
				}
			})
		}
	})
}

// TestWorkspaceQueryFieldValidators runs the validators the workspaces data
// source declares on the operands nested under query.fields.
func TestWorkspaceQueryFieldValidators(t *testing.T) {
	t.Parallel()

	var response datasource.SchemaResponse
	NewWorkspacesDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &response)
	query, ok := response.Schema.Attributes["query"].(datasourceschema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("query attribute type = %T", response.Schema.Attributes["query"])
	}
	fields, ok := query.Attributes["fields"].(datasourceschema.ListNestedAttribute)
	if !ok {
		t.Fatalf("query.fields attribute type = %T", query.Attributes["fields"])
	}
	nameAttribute, ok := fields.NestedObject.Attributes["name"].(datasourceschema.StringAttribute)
	if !ok {
		t.Fatalf("query.fields.name attribute type = %T", fields.NestedObject.Attributes["name"])
	}
	valuesAttribute, ok := fields.NestedObject.Attributes["values"].(datasourceschema.SetAttribute)
	if !ok {
		t.Fatalf("query.fields.values attribute type = %T", fields.NestedObject.Attributes["values"])
	}

	stringSetValue := func(values ...string) types.Set {
		elements := make([]attr.Value, len(values))
		for i, value := range values {
			elements[i] = types.StringValue(value)
		}
		return types.SetValueMust(types.StringType, elements)
	}

	tests := map[string]struct {
		name      types.String
		values    types.Set
		wantError bool
	}{
		"populated": {name: types.StringValue("attributes.type"), values: stringSetValue("confluence")},
		"blank name": {
			name: types.StringValue("  "), values: stringSetValue("confluence"), wantError: true,
		},
		"empty values": {
			name: types.StringValue("attributes.type"), values: stringSetValue(), wantError: true,
		},
		// A filter derived from another object is unknown while validating, and
		// rejecting it here would refuse a configuration that is fine once the
		// value resolves. The service layer checks it again after planning.
		"unknown values": {name: types.StringValue("attributes.type"), values: types.SetUnknown(types.StringType)},
		"unknown name":   {name: types.StringUnknown(), values: stringSetValue("confluence")},
		"null values":    {name: types.StringValue("attributes.type"), values: types.SetNull(types.StringType)},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fieldPath := path.Root("query").AtName("fields").AtListIndex(0)
			diagnostics := runStringValidators(context.Background(), fieldPath.AtName("name"), test.name, nameAttribute.Validators)
			diagnostics.Append(runSetValidators(context.Background(), fieldPath.AtName("values"), test.values, valuesAttribute.Validators)...)
			if diagnostics.HasError() != test.wantError {
				t.Fatalf("diagnostics = %v, wantError = %t", diagnostics, test.wantError)
			}
		})
	}
}

// TestResourceSchemasCarryTheirRules reads each attribute out of the schema the
// resource actually builds. The identity validation functions consume the same
// validator lists directly, so without this an attribute could stop
// referencing them and the suite would stay green.
func TestResourceSchemasCarryTheirRules(t *testing.T) {
	t.Parallel()

	stringAttribute := func(t *testing.T, attributes map[string]resourceschema.Attribute, name string) resourceschema.StringAttribute {
		t.Helper()
		attribute, ok := attributes[name].(resourceschema.StringAttribute)
		if !ok {
			t.Fatalf("%s attribute type = %T, want schema.StringAttribute", name, attributes[name])
		}
		return attribute
	}
	schemaOf := func(t *testing.T, subject resource.Resource) map[string]resourceschema.Attribute {
		t.Helper()
		response := &resource.SchemaResponse{}
		subject.Schema(context.Background(), resource.SchemaRequest{}, response)
		if response.Diagnostics.HasError() {
			t.Fatalf("Schema() diagnostics = %v", response.Diagnostics)
		}
		return response.Schema.Attributes
	}

	group := schemaOf(t, NewGroupResource())
	membership := schemaOf(t, NewGroupMembershipResource())
	userRole := schemaOf(t, NewUserRoleAssignmentResource())
	orgRole := schemaOf(t, NewUserOrganizationRoleAssignmentResource())
	groupRole := schemaOf(t, NewGroupRoleAssignmentResource())

	for _, testCase := range []struct {
		name      string
		attribute resourceschema.StringAttribute
		value     string
		wantError bool
	}{
		{name: "group organization_id", attribute: stringAttribute(t, group, "organization_id"), value: "org"},
		{name: "blank group organization_id", attribute: stringAttribute(t, group, "organization_id"), value: " ", wantError: true},
		{name: "blank group name", attribute: stringAttribute(t, group, "name"), value: "  ", wantError: true},
		{name: "blank membership account_id", attribute: stringAttribute(t, membership, "account_id"), value: " ", wantError: true},
		{name: "resource ari", attribute: stringAttribute(t, userRole, "resource"), value: "ari:cloud:jira::site/site-id"},
		{name: "bare resource id", attribute: stringAttribute(t, userRole, "resource"), value: "site-id", wantError: true},
		{name: "supported application role", attribute: stringAttribute(t, userRole, "role"), value: "atlassian/user"},
		{name: "organization role on the user role resource", attribute: stringAttribute(t, userRole, "role"), value: "atlassian/org-admin", wantError: true},
		{name: "organization admin role", attribute: stringAttribute(t, orgRole, "role"), value: organizationAdminRole},
		{name: "application role on the organization role resource", attribute: stringAttribute(t, orgRole, "role"), value: "atlassian/user", wantError: true},
		{name: "group role resource ari", attribute: stringAttribute(t, groupRole, "resource"), value: "ari:cloud:confluence::site/site-id"},
		{name: "group role is not enumerated", attribute: stringAttribute(t, groupRole, "role"), value: "atlassian/anything"},
		{name: "blank group role", attribute: stringAttribute(t, groupRole, "role"), value: " ", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			diagnostics := runStringValidators(context.Background(), path.Root("attribute"),
				types.StringValue(testCase.value), testCase.attribute.Validators)
			if diagnostics.HasError() != testCase.wantError {
				t.Fatalf("rejected = %t, want %t; diagnostics = %v", diagnostics.HasError(), testCase.wantError, diagnostics)
			}
		})
	}
}
