package organization

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type fakeOrganizationRoleAssignmentClient struct {
	assign func(context.Context, string, string, string) error
	revoke func(context.Context, string, string, string) error
	has    func(context.Context, string, string, string, string) (bool, error)
}

func (f *fakeOrganizationRoleAssignmentClient) AssignOrganizationRole(ctx context.Context, organizationID, accountID, role string) error {
	return f.assign(ctx, organizationID, accountID, role)
}

func (f *fakeOrganizationRoleAssignmentClient) RevokeOrganizationRole(ctx context.Context, organizationID, accountID, role string) error {
	return f.revoke(ctx, organizationID, accountID, role)
}

func (f *fakeOrganizationRoleAssignmentClient) HasDirectOrganizationRole(ctx context.Context, organizationID, directoryID, accountID, role string) (bool, error) {
	return f.has(ctx, organizationID, directoryID, accountID, role)
}

func TestUserOrganizationRoleAssignmentMetadataAndSchema(t *testing.T) {
	t.Parallel()

	subject := NewUserOrganizationRoleAssignmentResource()
	var metadataResponse resource.MetadataResponse
	subject.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "atlassian"}, &metadataResponse)
	if metadataResponse.TypeName != "atlassian_organization_user_organization_role_assignment" {
		t.Fatalf("TypeName = %q", metadataResponse.TypeName)
	}

	var schemaResponse resource.SchemaResponse
	subject.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", schemaResponse.Diagnostics)
	}
	wantAttributes := []string{"id", "organization_id", "directory_id", "account_id", "role"}
	if len(schemaResponse.Schema.Attributes) != len(wantAttributes) {
		t.Fatalf("attribute count = %d, want %d", len(schemaResponse.Schema.Attributes), len(wantAttributes))
	}
	for _, name := range wantAttributes {
		attribute, exists := schemaResponse.Schema.Attributes[name]
		if !exists {
			t.Errorf("attribute %q is missing", name)
			continue
		}
		if name == "id" {
			if !attribute.IsComputed() {
				t.Errorf("id is not computed")
			}
			continue
		}
		if !attribute.IsRequired() {
			t.Errorf("attribute %q is not required", name)
		}
		stringAttribute, ok := attribute.(resourceschema.StringAttribute)
		if !ok || len(stringAttribute.PlanModifiers) == 0 {
			t.Errorf("attribute %q does not require replacement", name)
		}
	}
}

func TestUserOrganizationRoleAssignmentIdentitySchema(t *testing.T) {
	t.Parallel()

	var response resource.IdentitySchemaResponse
	NewUserOrganizationRoleAssignmentResource().(resource.ResourceWithIdentity).IdentitySchema(context.Background(), resource.IdentitySchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("IdentitySchema() diagnostics = %v", response.Diagnostics)
	}
	wantAttributes := []string{"organization_id", "directory_id", "account_id", "role"}
	if len(response.IdentitySchema.Attributes) != len(wantAttributes) {
		t.Fatalf("identity attribute count = %d, want %d", len(response.IdentitySchema.Attributes), len(wantAttributes))
	}
	for _, name := range wantAttributes {
		attribute, exists := response.IdentitySchema.Attributes[name]
		if !exists || !attribute.IsRequiredForImport() {
			t.Errorf("identity attribute %q is missing or not required for import", name)
		}
	}
}

func TestValidateUserOrganizationRoleAssignment(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		organizationID types.String
		directoryID    types.String
		accountID      types.String
		role           types.String
		wantError      bool
	}{
		"legacy account ID": {
			organizationID: types.StringValue("org"),
			directoryID:    types.StringValue("directory"),
			accountID:      types.StringValue("5b361abdf2886739ae9da236"),
			role:           types.StringValue(organizationAdminRole),
		},
		"712020 account ID": {
			organizationID: types.StringValue("org"),
			directoryID:    types.StringValue("directory"),
			accountID:      types.StringValue("712020:12345678-1234-1234-1234-123456789012"),
			role:           types.StringValue(organizationAdminRole),
		},
		"unsupported role": {
			organizationID: types.StringValue("org"),
			directoryID:    types.StringValue("directory"),
			accountID:      types.StringValue("account"),
			role:           types.StringValue("atlassian/admin"),
			wantError:      true,
		},
		"empty account ID": {
			organizationID: types.StringValue("org"),
			directoryID:    types.StringValue("directory"),
			accountID:      types.StringValue(" "),
			role:           types.StringValue(organizationAdminRole),
			wantError:      true,
		},
		"unknown values": {
			organizationID: types.StringUnknown(),
			directoryID:    types.StringUnknown(),
			accountID:      types.StringUnknown(),
			role:           types.StringUnknown(),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diagnostics := validateUserOrganizationRoleAssignment(test.organizationID, test.directoryID, test.accountID, test.role)
			if diagnostics.HasError() != test.wantError {
				t.Fatalf("HasError() = %t, want %t; diagnostics = %v", diagnostics.HasError(), test.wantError, diagnostics)
			}
		})
	}
}

func TestUserOrganizationRoleAttachmentCreatePollsUntilVisible(t *testing.T) {
	t.Parallel()

	var readCalls int
	client := &fakeOrganizationRoleAssignmentClient{
		assign: func(_ context.Context, organizationID, accountID, role string) error {
			if organizationID != "org" || accountID != "5b361abdf2886739ae9da236" || role != organizationAdminRole {
				t.Errorf("assign arguments = %q, %q, %q", organizationID, accountID, role)
			}
			return nil
		},
		has: func(context.Context, string, string, string, string) (bool, error) {
			readCalls++
			return readCalls >= 3, nil
		},
	}
	subject := newTestUserOrganizationRoleAttachmentResource(client)
	model := testUserOrganizationRoleAssignmentModel()
	request, response := newUserOrganizationRoleAttachmentCreateData(t, subject, model)
	subject.Create(context.Background(), request, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Create() diagnostics = %v", response.Diagnostics)
	}
	if readCalls != 3 {
		t.Fatalf("read calls = %d, want 3", readCalls)
	}
	var state userOrganizationRoleAssignmentResourceModel
	response.Diagnostics.Append(response.State.Get(context.Background(), &state)...)
	if response.Diagnostics.HasError() {
		t.Fatalf("State.Get() diagnostics = %v", response.Diagnostics)
	}
	if got := state.ID.ValueString(); got != "org,directory,5b361abdf2886739ae9da236,atlassian/org-admin" {
		t.Errorf("id = %q", got)
	}
}

func TestUserOrganizationRoleAttachmentCreateVerifiesAmbiguousOutcomes(t *testing.T) {
	t.Parallel()

	tests := map[string]error{
		"transport error": errors.New("connection reset after request"),
		"conflict":        &admin.HTTPError{StatusCode: http.StatusConflict},
	}
	for name, mutationErr := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := &fakeOrganizationRoleAssignmentClient{
				assign: func(context.Context, string, string, string) error { return mutationErr },
				has:    func(context.Context, string, string, string, string) (bool, error) { return true, nil },
			}
			subject := newTestUserOrganizationRoleAttachmentResource(client)
			request, response := newUserOrganizationRoleAttachmentCreateData(t, subject, testUserOrganizationRoleAssignmentModel())
			subject.Create(context.Background(), request, response)
			if response.Diagnostics.HasError() {
				t.Fatalf("Create() diagnostics = %v", response.Diagnostics)
			}
		})
	}
}

func TestUserOrganizationRoleAttachmentDeletePollsAndVerifiesAmbiguousOutcome(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		revokeErr error
		states    []bool
	}{
		"eventual consistency": {states: []bool{true, true, false}},
		"ambiguous transport":  {revokeErr: errors.New("connection reset after request"), states: []bool{false}},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			readCalls := 0
			client := &fakeOrganizationRoleAssignmentClient{
				revoke: func(context.Context, string, string, string) error { return test.revokeErr },
				has: func(context.Context, string, string, string, string) (bool, error) {
					state := test.states[readCalls]
					readCalls++
					return state, nil
				},
			}
			subject := newTestUserOrganizationRoleAttachmentResource(client)
			request, response := newUserOrganizationRoleAttachmentDeleteData(t, subject, testUserOrganizationRoleAssignmentModel())
			subject.Delete(context.Background(), request, response)
			if response.Diagnostics.HasError() {
				t.Fatalf("Delete() diagnostics = %v", response.Diagnostics)
			}
			if readCalls != len(test.states) {
				t.Fatalf("read calls = %d, want %d", readCalls, len(test.states))
			}
		})
	}
}

func TestUserOrganizationRoleAttachmentDeleteSurfacesProtectedRoleError(t *testing.T) {
	t.Parallel()

	client := &fakeOrganizationRoleAssignmentClient{
		revoke: func(context.Context, string, string, string) error {
			return &admin.HTTPError{StatusCode: http.StatusBadRequest, Body: `{"message":"last organization administrator"}`}
		},
		has: func(context.Context, string, string, string, string) (bool, error) {
			t.Fatal("read must not be called for a definitive API error")
			return false, nil
		},
	}
	subject := newTestUserOrganizationRoleAttachmentResource(client)
	request, response := newUserOrganizationRoleAttachmentDeleteData(t, subject, testUserOrganizationRoleAssignmentModel())
	subject.Delete(context.Background(), request, response)
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics.Errors()[0].Detail(), "last organization administrator") {
		t.Fatalf("Delete() diagnostics = %v", response.Diagnostics)
	}
}

func TestUserOrganizationRoleAttachmentReadRemovesAbsentResource(t *testing.T) {
	t.Parallel()

	client := &fakeOrganizationRoleAssignmentClient{
		has: func(context.Context, string, string, string, string) (bool, error) { return false, nil },
	}
	subject := newTestUserOrganizationRoleAttachmentResource(client)
	request, response := newUserOrganizationRoleAttachmentReadData(t, subject, testUserOrganizationRoleAssignmentModel())
	subject.Read(context.Background(), request, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Read() diagnostics = %v", response.Diagnostics)
	}
	if !response.State.Raw.IsNull() {
		t.Fatalf("state = %v, want null", response.State.Raw)
	}
}

func TestUserOrganizationRoleAttachmentImportParsingAndValidation(t *testing.T) {
	t.Parallel()

	identity, err := parseUserOrganizationRoleAssignmentImportID(" org , directory , 712020:account , atlassian/org-admin ")
	if err != nil {
		t.Fatal(err)
	}
	if identity.AccountID.ValueString() != "712020:account" || identity.Role.ValueString() != organizationAdminRole {
		t.Fatalf("identity = %#v", identity)
	}
	invalid := []string{
		"org,directory,account",
		"org,directory,,atlassian/org-admin",
	}
	for _, id := range invalid {
		if _, err := parseUserOrganizationRoleAssignmentImportID(id); err == nil {
			t.Errorf("parseUserOrganizationRoleAssignmentImportID(%q) error = nil", id)
		}
	}

	ctx := context.Background()
	subject := NewUserOrganizationRoleAssignmentResource().(*userOrganizationRoleAssignmentResource)
	requestIdentity, response := newUserOrganizationRoleAttachmentImportData(t, ctx, subject)
	subject.ImportState(ctx, resource.ImportStateRequest{
		ID:       "org,directory,5b361abdf2886739ae9da236,atlassian/org-admin",
		Identity: requestIdentity,
	}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("ImportState() diagnostics = %v", response.Diagnostics)
	}

	_, invalidResponse := newUserOrganizationRoleAttachmentImportData(t, ctx, subject)
	subject.ImportState(ctx, resource.ImportStateRequest{
		ID:       "org,directory,account,atlassian/admin",
		Identity: requestIdentity,
	}, invalidResponse)
	if !invalidResponse.Diagnostics.HasError() {
		t.Fatal("ImportState() accepted an unsupported role")
	}
}

func TestUserOrganizationRoleAttachmentImportByIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	subject := NewUserOrganizationRoleAssignmentResource().(*userOrganizationRoleAssignmentResource)
	requestIdentity, response := newUserOrganizationRoleAttachmentImportData(t, ctx, subject)
	identity := userOrganizationRoleAssignmentResourceIdentityModel{
		OrganizationID: types.StringValue("org"),
		DirectoryID:    types.StringValue("directory"),
		AccountID:      types.StringValue("712020:account"),
		Role:           types.StringValue(organizationAdminRole),
	}
	if diagnostics := requestIdentity.Set(ctx, &identity); diagnostics.HasError() {
		t.Fatalf("Identity.Set() diagnostics = %v", diagnostics)
	}
	response.Identity.Raw = requestIdentity.Raw.Copy()
	subject.ImportState(ctx, resource.ImportStateRequest{Identity: requestIdentity}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("ImportState() diagnostics = %v", response.Diagnostics)
	}
	var state userOrganizationRoleAssignmentResourceModel
	response.Diagnostics.Append(response.State.Get(ctx, &state)...)
	if state.ID.ValueString() != "org,directory,712020:account,atlassian/org-admin" {
		t.Errorf("id = %q", state.ID.ValueString())
	}
}

func newTestUserOrganizationRoleAttachmentResource(client organizationRoleAssignmentClient) *userOrganizationRoleAssignmentResource {
	return &userOrganizationRoleAssignmentResource{
		client:              client,
		pollTimeout:         100 * time.Millisecond,
		pollInitialInterval: time.Millisecond,
		pollMaximumInterval: 2 * time.Millisecond,
	}
}

func testUserOrganizationRoleAssignmentModel() userOrganizationRoleAssignmentResourceModel {
	return userOrganizationRoleAssignmentResourceModel{
		OrganizationID: types.StringValue("org"),
		DirectoryID:    types.StringValue("directory"),
		AccountID:      types.StringValue("5b361abdf2886739ae9da236"),
		Role:           types.StringValue(organizationAdminRole),
	}
}

func newUserOrganizationRoleAttachmentCreateData(t *testing.T, subject *userOrganizationRoleAssignmentResource, model userOrganizationRoleAssignmentResourceModel) (resource.CreateRequest, *resource.CreateResponse) {
	t.Helper()
	ctx := context.Background()
	schemaValue, identitySchemaValue := userOrganizationRoleAttachmentSchemas(t, ctx, subject)
	plan := tfsdk.Plan{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue}
	if diagnostics := plan.Set(ctx, &model); diagnostics.HasError() {
		t.Fatalf("Plan.Set() diagnostics = %v", diagnostics)
	}
	identityType := identitySchemaValue.Type().TerraformType(ctx)
	return resource.CreateRequest{Plan: plan}, &resource.CreateResponse{
		State:    tfsdk.State{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue},
		Identity: &tfsdk.ResourceIdentity{Raw: tftypes.NewValue(identityType, nil), Schema: identitySchemaValue},
	}
}

func newUserOrganizationRoleAttachmentDeleteData(t *testing.T, subject *userOrganizationRoleAssignmentResource, model userOrganizationRoleAssignmentResourceModel) (resource.DeleteRequest, *resource.DeleteResponse) {
	t.Helper()
	ctx := context.Background()
	schemaValue, _ := userOrganizationRoleAttachmentSchemas(t, ctx, subject)
	state := tfsdk.State{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue}
	model.ID = types.StringValue(userOrganizationRoleAssignmentID(model))
	if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
		t.Fatalf("State.Set() diagnostics = %v", diagnostics)
	}
	return resource.DeleteRequest{State: state}, &resource.DeleteResponse{}
}

func newUserOrganizationRoleAttachmentReadData(t *testing.T, subject *userOrganizationRoleAssignmentResource, model userOrganizationRoleAssignmentResourceModel) (resource.ReadRequest, *resource.ReadResponse) {
	t.Helper()
	ctx := context.Background()
	schemaValue, identitySchemaValue := userOrganizationRoleAttachmentSchemas(t, ctx, subject)
	state := tfsdk.State{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue}
	model.ID = types.StringValue(userOrganizationRoleAssignmentID(model))
	if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
		t.Fatalf("State.Set() diagnostics = %v", diagnostics)
	}
	identityType := identitySchemaValue.Type().TerraformType(ctx)
	return resource.ReadRequest{State: state}, &resource.ReadResponse{
		State:    state,
		Identity: &tfsdk.ResourceIdentity{Raw: tftypes.NewValue(identityType, nil), Schema: identitySchemaValue},
	}
}

func newUserOrganizationRoleAttachmentImportData(t *testing.T, ctx context.Context, subject *userOrganizationRoleAssignmentResource) (*tfsdk.ResourceIdentity, *resource.ImportStateResponse) {
	t.Helper()
	schemaValue, identitySchemaValue := userOrganizationRoleAttachmentSchemas(t, ctx, subject)
	identityType := identitySchemaValue.Type().TerraformType(ctx)
	requestIdentity := &tfsdk.ResourceIdentity{Raw: tftypes.NewValue(identityType, nil), Schema: identitySchemaValue}
	return requestIdentity, &resource.ImportStateResponse{
		State:    tfsdk.State{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue},
		Identity: &tfsdk.ResourceIdentity{Raw: tftypes.NewValue(identityType, nil), Schema: identitySchemaValue},
	}
}

func userOrganizationRoleAttachmentSchemas(t *testing.T, ctx context.Context, subject *userOrganizationRoleAssignmentResource) (resourceschema.Schema, resourceIdentitySchema) {
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
	return schemaResponse.Schema, identitySchemaResponse.IdentitySchema
}

type resourceIdentitySchema = identityschema.Schema
