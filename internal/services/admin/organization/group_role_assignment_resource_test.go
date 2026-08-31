package organization

import (
	"context"
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

const testGroupRoleAssignmentID = "org,directory,group,ari:cloud:confluence::site/site-id,atlassian/user"

type fakeGroupRoleAssignmentClient struct {
	assign func(context.Context, string, string, string, string, string) error
	revoke func(context.Context, string, string, string, string, string) error
	has    func(context.Context, string, string, string, string, string) (bool, error)
}

func (f *fakeGroupRoleAssignmentClient) AssignGroupRole(ctx context.Context, organizationID, directoryID, groupID, resourceID, role string) error {
	return f.assign(ctx, organizationID, directoryID, groupID, resourceID, role)
}

func (f *fakeGroupRoleAssignmentClient) RevokeGroupRole(ctx context.Context, organizationID, directoryID, groupID, resourceID, role string) error {
	return f.revoke(ctx, organizationID, directoryID, groupID, resourceID, role)
}

func (f *fakeGroupRoleAssignmentClient) HasGroupRole(ctx context.Context, organizationID, directoryID, groupID, resourceID, role string) (bool, error) {
	return f.has(ctx, organizationID, directoryID, groupID, resourceID, role)
}

func TestGroupRoleAssignmentMetadataAndSchema(t *testing.T) {
	t.Parallel()

	subject := NewGroupRoleAssignmentResource()
	var metadataResponse resource.MetadataResponse
	subject.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "atlassian"}, &metadataResponse)
	if metadataResponse.TypeName != "atlassian_organization_group_role_assignment" {
		t.Fatalf("TypeName = %q", metadataResponse.TypeName)
	}

	var schemaResponse resource.SchemaResponse
	subject.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", schemaResponse.Diagnostics)
	}
	wantAttributes := []string{"id", "organization_id", "directory_id", "group_id", "resource", "role"}
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
	// Destroying the resource removes app access from every member, so the
	// documentation must say so.
	if !strings.Contains(schemaResponse.Schema.MarkdownDescription, "every member") {
		t.Error("schema description does not warn that destroying removes access from every member")
	}
}

func TestGroupRoleAssignmentIdentitySchema(t *testing.T) {
	t.Parallel()

	var response resource.IdentitySchemaResponse
	NewGroupRoleAssignmentResource().(resource.ResourceWithIdentity).IdentitySchema(context.Background(), resource.IdentitySchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("IdentitySchema() diagnostics = %v", response.Diagnostics)
	}
	wantAttributes := []string{"organization_id", "directory_id", "group_id", "resource", "role"}
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

func TestValidateGroupRoleAssignment(t *testing.T) {
	t.Parallel()

	valid := groupRoleAssignmentResourceIdentityModel{
		OrganizationID: types.StringValue("org"),
		DirectoryID:    types.StringValue("directory"),
		GroupID:        types.StringValue("group"),
		Resource:       types.StringValue("ari:cloud:confluence::site/site-id"),
		Role:           types.StringValue("atlassian/user"),
	}

	tests := map[string]struct {
		mutate    func(*groupRoleAssignmentResourceIdentityModel)
		wantError bool
	}{
		"valid":            {},
		"blank group":      {mutate: func(m *groupRoleAssignmentResourceIdentityModel) { m.GroupID = types.StringValue("  ") }, wantError: true},
		"blank role":       {mutate: func(m *groupRoleAssignmentResourceIdentityModel) { m.Role = types.StringValue("") }, wantError: true},
		"resource not ari": {mutate: func(m *groupRoleAssignmentResourceIdentityModel) { m.Resource = types.StringValue("site-id") }, wantError: true},
		// The accepted roles differ from the user endpoint's and are not
		// enumerable client side, so the provider forwards unknown roles and
		// lets the API answer.
		"viewer role is accepted": {mutate: func(m *groupRoleAssignmentResourceIdentityModel) { m.Role = types.StringValue("atlassian/viewer") }},
		"unknown role is accepted": {
			mutate: func(m *groupRoleAssignmentResourceIdentityModel) { m.Role = types.StringValue("atlassian/future-role") },
		},
		"null values are left to Terraform": {
			mutate: func(m *groupRoleAssignmentResourceIdentityModel) { m.Resource = types.StringNull() },
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			values := valid
			if test.mutate != nil {
				test.mutate(&values)
			}
			diagnostics := validateGroupRoleAssignment(values)
			if diagnostics.HasError() != test.wantError {
				t.Fatalf("diagnostics = %v, wantError = %t", diagnostics, test.wantError)
			}
		})
	}
}

func TestGroupRoleAssignmentCreateAssignsAndRecordsIdentity(t *testing.T) {
	t.Parallel()

	var assigned []string
	client := &fakeGroupRoleAssignmentClient{
		assign: func(_ context.Context, organizationID, directoryID, groupID, resourceID, role string) error {
			assigned = []string{organizationID, directoryID, groupID, resourceID, role}
			return nil
		},
		has: func(context.Context, string, string, string, string, string) (bool, error) {
			return len(assigned) > 0, nil
		},
	}
	subject := newTestGroupRoleAssignmentResource(client)
	request, response := newGroupRoleAssignmentCreateData(t, subject, testGroupRoleAssignmentModel())
	subject.Create(context.Background(), request, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Create() diagnostics = %v", response.Diagnostics)
	}

	want := []string{"org", "directory", "group", "ari:cloud:confluence::site/site-id", "atlassian/user"}
	if strings.Join(assigned, "|") != strings.Join(want, "|") {
		t.Errorf("assign arguments = %#v, want %#v", assigned, want)
	}

	ctx := context.Background()
	var state groupRoleAssignmentResourceModel
	response.Diagnostics.Append(response.State.Get(ctx, &state)...)
	if state.ID.ValueString() != testGroupRoleAssignmentID {
		t.Errorf("id = %q, want %q", state.ID.ValueString(), testGroupRoleAssignmentID)
	}
	var identity groupRoleAssignmentResourceIdentityModel
	response.Diagnostics.Append(response.Identity.Get(ctx, &identity)...)
	if identity.GroupID.ValueString() != "group" || identity.Role.ValueString() != "atlassian/user" {
		t.Errorf("identity = %#v", identity)
	}
}

func TestGroupRoleAssignmentCreateRefusesToAdoptExistingAssignment(t *testing.T) {
	t.Parallel()

	client := &fakeGroupRoleAssignmentClient{
		assign: func(context.Context, string, string, string, string, string) error {
			t.Fatal("assign must not be sent when the group already holds the role")
			return nil
		},
		has: func(context.Context, string, string, string, string, string) (bool, error) { return true, nil },
	}
	subject := newTestGroupRoleAssignmentResource(client)
	request, response := newGroupRoleAssignmentCreateData(t, subject, testGroupRoleAssignmentModel())
	subject.Create(context.Background(), request, response)
	if !response.Diagnostics.HasError() {
		t.Fatal("Create() adopted an assignment Terraform did not grant")
	}
	detail := response.Diagnostics.Errors()[0].Detail()
	if !strings.Contains(detail, testGroupRoleAssignmentID) {
		t.Errorf("detail = %q, want the composite import identifier", detail)
	}
	if !response.State.Raw.IsNull() {
		t.Errorf("state = %v, want null for a resource Terraform did not create", response.State.Raw)
	}
}

func TestGroupRoleAssignmentCreateReportsConflictAsUserLimit(t *testing.T) {
	t.Parallel()

	// Unlike the organization-level endpoint, this endpoint answers 409 when
	// the app has no seats left. Reporting it as an existing assignment would
	// send the operator to import something that was never assigned.
	client := &fakeGroupRoleAssignmentClient{
		assign: func(context.Context, string, string, string, string, string) error {
			return &admin.HTTPError{StatusCode: http.StatusConflict}
		},
		has: func(context.Context, string, string, string, string, string) (bool, error) { return false, nil },
	}
	subject := newTestGroupRoleAssignmentResource(client)
	request, response := newGroupRoleAssignmentCreateData(t, subject, testGroupRoleAssignmentModel())
	subject.Create(context.Background(), request, response)
	if !response.Diagnostics.HasError() {
		t.Fatal("Create() diagnostics = nil, want a user limit error")
	}
	summary := response.Diagnostics.Errors()[0].Summary()
	if summary != "App user limit exceeded" {
		t.Errorf("summary = %q, want the user limit error", summary)
	}
	if detail := response.Diagnostics.Errors()[0].Detail(); strings.Contains(detail, "terraform import") {
		t.Errorf("detail = %q, must not suggest importing a seat exhaustion failure", detail)
	}
}

func TestGroupRoleAssignmentCreateStopsBeforeAssigningWhenExistenceCheckFails(t *testing.T) {
	t.Parallel()

	client := &fakeGroupRoleAssignmentClient{
		assign: func(context.Context, string, string, string, string, string) error {
			t.Fatal("assign must not be sent when the existence check fails")
			return nil
		},
		has: func(context.Context, string, string, string, string, string) (bool, error) {
			return false, &admin.HTTPError{StatusCode: http.StatusForbidden}
		},
	}
	subject := newTestGroupRoleAssignmentResource(client)
	request, response := newGroupRoleAssignmentCreateData(t, subject, testGroupRoleAssignmentModel())
	subject.Create(context.Background(), request, response)
	if !response.Diagnostics.HasError() {
		t.Fatal("Create() diagnostics = nil, want an existence check error")
	}
	if !response.State.Raw.IsNull() {
		t.Errorf("state = %v, want null", response.State.Raw)
	}
}

func TestGroupRoleAssignmentCreateKeepsStateWhenVerificationFails(t *testing.T) {
	t.Parallel()

	reads := 0
	client := &fakeGroupRoleAssignmentClient{
		assign: func(context.Context, string, string, string, string, string) error { return nil },
		has: func(context.Context, string, string, string, string, string) (bool, error) {
			reads++
			// Never becomes visible, so verification times out after the
			// assignment has already been sent.
			return false, nil
		},
	}
	subject := newTestGroupRoleAssignmentResource(client)
	request, response := newGroupRoleAssignmentCreateData(t, subject, testGroupRoleAssignmentModel())
	subject.Create(context.Background(), request, response)
	if !response.Diagnostics.HasError() {
		t.Fatal("Create() diagnostics = nil, want a verification error")
	}
	if response.State.Raw.IsNull() {
		t.Error("state = null, want the possibly granted assignment tracked as tainted")
	}
	if reads < 2 {
		t.Errorf("reads = %d, want the existence check plus verification polling", reads)
	}
}

func TestGroupRoleAssignmentReadRemovesMissingAssignment(t *testing.T) {
	t.Parallel()

	tests := map[string]func(context.Context, string, string, string, string, string) (bool, error){
		"assignment is absent": func(context.Context, string, string, string, string, string) (bool, error) {
			return false, nil
		},
		"group is gone": func(context.Context, string, string, string, string, string) (bool, error) {
			return false, &admin.HTTPError{StatusCode: http.StatusNotFound}
		},
	}
	for name, has := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			subject := newTestGroupRoleAssignmentResource(&fakeGroupRoleAssignmentClient{has: has})
			request, response := newGroupRoleAssignmentReadData(t, subject, testGroupRoleAssignmentModel())
			subject.Read(context.Background(), request, response)
			if response.Diagnostics.HasError() {
				t.Fatalf("Read() diagnostics = %v", response.Diagnostics)
			}
			if !response.State.Raw.IsNull() {
				t.Error("Read() kept state for an assignment that no longer exists")
			}
		})
	}
}

func TestGroupRoleAssignmentReadKeepsPresentAssignment(t *testing.T) {
	t.Parallel()

	client := &fakeGroupRoleAssignmentClient{
		has: func(context.Context, string, string, string, string, string) (bool, error) { return true, nil },
	}
	subject := newTestGroupRoleAssignmentResource(client)
	request, response := newGroupRoleAssignmentReadData(t, subject, testGroupRoleAssignmentModel())
	subject.Read(context.Background(), request, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Read() diagnostics = %v", response.Diagnostics)
	}
	if response.State.Raw.IsNull() {
		t.Fatal("Read() removed a present assignment")
	}
	var identity groupRoleAssignmentResourceIdentityModel
	response.Diagnostics.Append(response.Identity.Get(context.Background(), &identity)...)
	if identity.Resource.ValueString() != "ari:cloud:confluence::site/site-id" {
		t.Errorf("identity resource = %q", identity.Resource.ValueString())
	}
}

func TestGroupRoleAssignmentDeleteToleratesMissingAssignment(t *testing.T) {
	t.Parallel()

	client := &fakeGroupRoleAssignmentClient{
		revoke: func(context.Context, string, string, string, string, string) error {
			return &admin.HTTPError{StatusCode: http.StatusNotFound}
		},
		has: func(context.Context, string, string, string, string, string) (bool, error) { return false, nil },
	}
	subject := newTestGroupRoleAssignmentResource(client)
	request, response := newGroupRoleAssignmentDeleteData(t, subject, testGroupRoleAssignmentModel())
	subject.Delete(context.Background(), request, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Delete() diagnostics = %v", response.Diagnostics)
	}
}

func TestGroupRoleAssignmentDeleteFailsWhenRoleRemains(t *testing.T) {
	t.Parallel()

	client := &fakeGroupRoleAssignmentClient{
		revoke: func(context.Context, string, string, string, string, string) error { return nil },
		has:    func(context.Context, string, string, string, string, string) (bool, error) { return true, nil },
	}
	subject := newTestGroupRoleAssignmentResource(client)
	request, response := newGroupRoleAssignmentDeleteData(t, subject, testGroupRoleAssignmentModel())
	subject.Delete(context.Background(), request, response)
	if !response.Diagnostics.HasError() {
		t.Fatal("Delete() diagnostics = nil, want a verification error while the role is still assigned")
	}
}

func TestGroupRoleAssignmentImportByStringID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	subject := newTestGroupRoleAssignmentResource(&fakeGroupRoleAssignmentClient{})
	_, response := newGroupRoleAssignmentImportData(t, ctx, subject)
	subject.ImportState(ctx, resource.ImportStateRequest{ID: testGroupRoleAssignmentID}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("ImportState() diagnostics = %v", response.Diagnostics)
	}
	var state groupRoleAssignmentResourceModel
	response.Diagnostics.Append(response.State.Get(ctx, &state)...)
	if state.ID.ValueString() != testGroupRoleAssignmentID {
		t.Errorf("id = %q", state.ID.ValueString())
	}
	if state.Resource.ValueString() != "ari:cloud:confluence::site/site-id" {
		t.Errorf("resource = %q, want the ARI to survive its embedded colons", state.Resource.ValueString())
	}
}

func TestGroupRoleAssignmentImportRejectsInvalidIdentifiers(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"too few components": "org,directory,group,ari:cloud:confluence::site/site-id",
		"empty component":    "org,,group,ari:cloud:confluence::site/site-id,atlassian/user",
		// Import identity is validated with the same rules as configuration.
		"resource is not an ari": "org,directory,group,site-id,atlassian/user",
	}
	for name, id := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			subject := newTestGroupRoleAssignmentResource(&fakeGroupRoleAssignmentClient{})
			_, response := newGroupRoleAssignmentImportData(t, ctx, subject)
			subject.ImportState(ctx, resource.ImportStateRequest{ID: id}, response)
			if !response.Diagnostics.HasError() {
				t.Fatalf("ImportState(%q) diagnostics = nil, want an error", id)
			}
		})
	}
}

func TestGroupRoleAssignmentImportByIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	subject := newTestGroupRoleAssignmentResource(&fakeGroupRoleAssignmentClient{})
	requestIdentity, response := newGroupRoleAssignmentImportData(t, ctx, subject)
	identity := groupRoleAssignmentResourceIdentityModel{
		OrganizationID: types.StringValue("org"),
		DirectoryID:    types.StringValue("directory"),
		GroupID:        types.StringValue("group"),
		Resource:       types.StringValue("ari:cloud:confluence::site/site-id"),
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
	var state groupRoleAssignmentResourceModel
	response.Diagnostics.Append(response.State.Get(ctx, &state)...)
	if state.ID.ValueString() != testGroupRoleAssignmentID {
		t.Errorf("id = %q", state.ID.ValueString())
	}
}

func newTestGroupRoleAssignmentResource(client groupRoleAssignmentClient) *groupRoleAssignmentResource {
	return &groupRoleAssignmentResource{
		client:              client,
		pollTimeout:         100 * time.Millisecond,
		pollInitialInterval: time.Millisecond,
		pollMaximumInterval: 2 * time.Millisecond,
	}
}

func testGroupRoleAssignmentModel() groupRoleAssignmentResourceModel {
	return groupRoleAssignmentResourceModel{
		OrganizationID: types.StringValue("org"),
		DirectoryID:    types.StringValue("directory"),
		GroupID:        types.StringValue("group"),
		Resource:       types.StringValue("ari:cloud:confluence::site/site-id"),
		Role:           types.StringValue("atlassian/user"),
	}
}

func newGroupRoleAssignmentCreateData(t *testing.T, subject *groupRoleAssignmentResource, model groupRoleAssignmentResourceModel) (resource.CreateRequest, *resource.CreateResponse) {
	t.Helper()
	ctx := context.Background()
	schemaValue, identitySchemaValue := groupRoleAssignmentSchemas(t, ctx, subject)
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

func newGroupRoleAssignmentReadData(t *testing.T, subject *groupRoleAssignmentResource, model groupRoleAssignmentResourceModel) (resource.ReadRequest, *resource.ReadResponse) {
	t.Helper()
	ctx := context.Background()
	schemaValue, identitySchemaValue := groupRoleAssignmentSchemas(t, ctx, subject)
	state := tfsdk.State{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue}
	model.ID = types.StringValue(groupRoleAssignmentID(model))
	if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
		t.Fatalf("State.Set() diagnostics = %v", diagnostics)
	}
	identityType := identitySchemaValue.Type().TerraformType(ctx)
	return resource.ReadRequest{State: state}, &resource.ReadResponse{
		State:    state,
		Identity: &tfsdk.ResourceIdentity{Raw: tftypes.NewValue(identityType, nil), Schema: identitySchemaValue},
	}
}

func newGroupRoleAssignmentDeleteData(t *testing.T, subject *groupRoleAssignmentResource, model groupRoleAssignmentResourceModel) (resource.DeleteRequest, *resource.DeleteResponse) {
	t.Helper()
	ctx := context.Background()
	schemaValue, _ := groupRoleAssignmentSchemas(t, ctx, subject)
	state := tfsdk.State{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue}
	model.ID = types.StringValue(groupRoleAssignmentID(model))
	if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
		t.Fatalf("State.Set() diagnostics = %v", diagnostics)
	}
	return resource.DeleteRequest{State: state}, &resource.DeleteResponse{}
}

func newGroupRoleAssignmentImportData(t *testing.T, ctx context.Context, subject *groupRoleAssignmentResource) (*tfsdk.ResourceIdentity, *resource.ImportStateResponse) {
	t.Helper()
	schemaValue, identitySchemaValue := groupRoleAssignmentSchemas(t, ctx, subject)
	identityType := identitySchemaValue.Type().TerraformType(ctx)
	requestIdentity := &tfsdk.ResourceIdentity{Raw: tftypes.NewValue(identityType, nil), Schema: identitySchemaValue}
	return requestIdentity, &resource.ImportStateResponse{
		State:    tfsdk.State{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue},
		Identity: &tfsdk.ResourceIdentity{Raw: tftypes.NewValue(identityType, nil), Schema: identitySchemaValue},
	}
}

func groupRoleAssignmentSchemas(t *testing.T, ctx context.Context, subject *groupRoleAssignmentResource) (resourceschema.Schema, identityschema.Schema) {
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
