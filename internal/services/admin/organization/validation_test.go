package organization

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
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
			diagnostics := validateUserRoleAssignmentValues(test.values)
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

func TestValidateStringSet(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value      types.Set
		maximum    int
		allowed    map[string]struct{}
		wantErrors bool
	}{
		"valid": {
			value:   types.SetValueMust(types.StringType, []attr.Value{types.StringValue("active")}),
			maximum: 1,
			allowed: stringSet("active"),
		},
		"empty": {
			value:      types.SetValueMust(types.StringType, nil),
			maximum:    1,
			wantErrors: true,
		},
		"too many": {
			value:      types.SetValueMust(types.StringType, []attr.Value{types.StringValue("one"), types.StringValue("two")}),
			maximum:    1,
			wantErrors: true,
		},
		"unsupported": {
			value:      types.SetValueMust(types.StringType, []attr.Value{types.StringValue("invalid")}),
			maximum:    1,
			allowed:    stringSet("active"),
			wantErrors: true,
		},
		"unknown element": {
			value:   types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()}),
			maximum: 1,
			allowed: stringSet("active"),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var response datasource.ValidateConfigResponse
			validateStringSet("test", test.value, test.maximum, test.allowed, &response)
			if response.Diagnostics.HasError() != test.wantErrors {
				t.Fatalf("HasError() = %t, want %t; diagnostics = %v", response.Diagnostics.HasError(), test.wantErrors, response.Diagnostics)
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
