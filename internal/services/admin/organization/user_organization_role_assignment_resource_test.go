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

	var readCalls, assignCalls int
	client := &fakeOrganizationRoleAssignmentClient{
		assign: func(_ context.Context, organizationID, accountID, role string) error {
			assignCalls++
			if organizationID != "org" || accountID != "5b361abdf2886739ae9da236" || role != organizationAdminRole {
				t.Errorf("assign arguments = %q, %q, %q", organizationID, accountID, role)
			}
			if readCalls != 1 {
				t.Errorf("assign was sent after %d reads, want the existence check only", readCalls)
			}
			return nil
		},
		has: func(context.Context, string, string, string, string) (bool, error) {
			readCalls++
			// The first read is the existence check, the rest poll for visibility.
			return readCalls >= 4, nil
		},
	}
	subject := newTestUserOrganizationRoleAttachmentResource(client)
	model := testUserOrganizationRoleAssignmentModel()
	request, response := newUserOrganizationRoleAttachmentCreateData(t, subject, model)
	subject.Create(context.Background(), request, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Create() diagnostics = %v", response.Diagnostics)
	}
	if assignCalls != 1 {
		t.Fatalf("assign calls = %d, want 1", assignCalls)
	}
	if readCalls != 4 {
		t.Fatalf("read calls = %d, want 4", readCalls)
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
		"transport error":       errors.New("connection reset after request"),
		"internal server error": &admin.HTTPError{StatusCode: http.StatusInternalServerError},
	}
	for name, mutationErr := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reads := 0
			client := &fakeOrganizationRoleAssignmentClient{
				assign: func(context.Context, string, string, string) error { return mutationErr },
				has: func(context.Context, string, string, string, string) (bool, error) {
					reads++
					// Absent for the existence check, present once the ambiguous
					// mutation is verified.
					return reads > 1, nil
				},
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

func TestUserOrganizationRoleAssignmentCreateRefusesToAdoptExistingAssignment(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		alreadyAssigned bool
		assignErr       error
		wantAssigns     int
	}{
		"existence check finds the assignment": {alreadyAssigned: true},
		"assign reports a conflict":            {assignErr: &admin.HTTPError{StatusCode: http.StatusConflict}, wantAssigns: 1},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assigns := 0
			client := &fakeOrganizationRoleAssignmentClient{
				assign: func(context.Context, string, string, string) error {
					assigns++
					return test.assignErr
				},
				has: func(context.Context, string, string, string, string) (bool, error) {
					return test.alreadyAssigned, nil
				},
			}
			subject := newTestUserOrganizationRoleAttachmentResource(client)
			request, response := newUserOrganizationRoleAttachmentCreateData(t, subject, testUserOrganizationRoleAssignmentModel())
			subject.Create(context.Background(), request, response)
			if !response.Diagnostics.HasError() {
				t.Fatal("Create() adopted an assignment Terraform did not grant")
			}
			if assigns != test.wantAssigns {
				t.Errorf("assign calls = %d, want %d", assigns, test.wantAssigns)
			}
			detail := response.Diagnostics.Errors()[0].Detail()
			if !strings.Contains(detail, "org,directory,5b361abdf2886739ae9da236,atlassian/org-admin") {
				t.Errorf("detail = %q, want the composite import identifier", detail)
			}
			if !response.State.Raw.IsNull() {
				t.Errorf("state = %v, want null for a resource Terraform did not create", response.State.Raw)
			}
		})
	}
}

func TestUserOrganizationRoleAssignmentCreateStopsBeforeAssigningWhenExistenceCheckFails(t *testing.T) {
	t.Parallel()

	client := &fakeOrganizationRoleAssignmentClient{
		assign: func(context.Context, string, string, string) error {
			t.Fatal("assign must not be sent when the existence check fails")
			return nil
		},
		has: func(context.Context, string, string, string, string) (bool, error) {
			return false, &admin.HTTPError{StatusCode: http.StatusForbidden}
		},
	}
	subject := newTestUserOrganizationRoleAttachmentResource(client)
	request, response := newUserOrganizationRoleAttachmentCreateData(t, subject, testUserOrganizationRoleAssignmentModel())
	subject.Create(context.Background(), request, response)
	if !response.Diagnostics.HasError() {
		t.Fatal("Create() diagnostics = nil, want an existence check error")
	}
	if !response.State.Raw.IsNull() {
		t.Errorf("state = %v, want null", response.State.Raw)
	}
}

func TestUserOrganizationRoleAssignmentCreateKeepsPartialStateWhenVerificationFails(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		// has is called with the 1-based read count. The first read is the
		// existence check that runs before the assignment is sent.
		has       func(int) (bool, error)
		wantReads int
	}{
		"assignment never becomes visible": {
			has: func(int) (bool, error) { return false, nil },
		},
		"verification becomes unreadable after assigning": {
			has: func(reads int) (bool, error) {
				if reads == 1 {
					return false, nil
				}
				return false, &admin.HTTPError{StatusCode: http.StatusForbidden}
			},
			wantReads: 2,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reads := 0
			client := &fakeOrganizationRoleAssignmentClient{
				assign: func(context.Context, string, string, string) error { return nil },
				has: func(context.Context, string, string, string, string) (bool, error) {
					reads++
					return test.has(reads)
				},
			}
			subject := newTestUserOrganizationRoleAttachmentResource(client)
			request, response := newUserOrganizationRoleAttachmentCreateData(t, subject, testUserOrganizationRoleAssignmentModel())
			subject.Create(context.Background(), request, response)
			if !response.Diagnostics.HasError() {
				t.Fatal("Create() diagnostics = nil, want a verification error")
			}
			if test.wantReads > 0 && reads != test.wantReads {
				t.Errorf("read calls = %d, want %d", reads, test.wantReads)
			}

			// The assignment may have been granted, so Terraform must keep tracking it.
			var state userOrganizationRoleAssignmentResourceModel
			if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
				t.Fatalf("State.Get() diagnostics = %v", diagnostics)
			}
			if got := state.ID.ValueString(); got != "org,directory,5b361abdf2886739ae9da236,atlassian/org-admin" {
				t.Errorf("id = %q, want the composite assignment identifier", got)
			}
			if got := state.AccountID.ValueString(); got != "5b361abdf2886739ae9da236" {
				t.Errorf("account_id = %q", got)
			}
			var identity userOrganizationRoleAssignmentResourceIdentityModel
			if diagnostics := response.Identity.Get(context.Background(), &identity); diagnostics.HasError() {
				t.Fatalf("Identity.Get() diagnostics = %v", diagnostics)
			}
			if identity.Role.ValueString() != organizationAdminRole {
				t.Errorf("identity role = %q", identity.Role.ValueString())
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
