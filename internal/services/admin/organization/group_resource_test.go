package organization

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	organizationclient "github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type fakeGroupClient struct {
	create func(context.Context, string, string, string, *string) error
	get    func(context.Context, string, string, string) (organizationclient.Group, error)
	search func(context.Context, string, string, organizationclient.SearchGroupsRequest) ([]organizationclient.Group, error)
	delete func(context.Context, string, string, string) error
}

func (f *fakeGroupClient) CreateGroup(ctx context.Context, organizationID, directoryID, name string, description *string) error {
	return f.create(ctx, organizationID, directoryID, name, description)
}

func (f *fakeGroupClient) GetGroup(ctx context.Context, organizationID, directoryID, groupID string) (organizationclient.Group, error) {
	return f.get(ctx, organizationID, directoryID, groupID)
}

func (f *fakeGroupClient) SearchGroups(ctx context.Context, organizationID, directoryID string, request organizationclient.SearchGroupsRequest) ([]organizationclient.Group, error) {
	return f.search(ctx, organizationID, directoryID, request)
}

func (f *fakeGroupClient) DeleteGroup(ctx context.Context, organizationID, directoryID, groupID string) error {
	return f.delete(ctx, organizationID, directoryID, groupID)
}

func TestGroupResourceMetadataSchemaAndIdentity(t *testing.T) {
	t.Parallel()

	subject := NewGroupResource()
	var metadata resource.MetadataResponse
	subject.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "atlassian"}, &metadata)
	if metadata.TypeName != "atlassian_organization_group" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}
	var schemaResponse resource.SchemaResponse
	subject.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatal(schemaResponse.Diagnostics)
	}
	for _, name := range []string{"organization_id", "directory_id", "name", "description"} {
		attribute, ok := schemaResponse.Schema.Attributes[name].(resourceschema.StringAttribute)
		if !ok || len(attribute.PlanModifiers) == 0 {
			t.Errorf("%s does not require replacement", name)
		}
	}
	description := schemaResponse.Schema.Attributes["description"].(resourceschema.StringAttribute)
	if !description.Optional || description.Computed {
		t.Errorf("description must be optional and non-computed, got Optional=%t Computed=%t", description.Optional, description.Computed)
	}
	var modifierResponse planmodifier.StringResponse
	description.PlanModifiers[0].PlanModifyString(context.Background(), planmodifier.StringRequest{
		State:       tfsdk.State{Raw: tftypes.NewValue(tftypes.String, "Managed by Terraform")},
		Plan:        tfsdk.Plan{Raw: tftypes.NewValue(tftypes.String, "planned resource")},
		StateValue:  types.StringValue("Managed by Terraform"),
		PlanValue:   types.StringNull(),
		ConfigValue: types.StringNull(),
	}, &modifierResponse)
	if !modifierResponse.RequiresReplace {
		t.Error("removing description does not require replacement")
	}
	for _, name := range []string{"id", "group_id", "external_synced", "managed_by", "management_access"} {
		if attribute := schemaResponse.Schema.Attributes[name]; attribute == nil || !attribute.IsComputed() {
			t.Errorf("%s is not computed", name)
		}
	}

	var identityResponse resource.IdentitySchemaResponse
	subject.(resource.ResourceWithIdentity).IdentitySchema(context.Background(), resource.IdentitySchemaRequest{}, &identityResponse)
	for _, name := range []string{"organization_id", "directory_id", "group_id"} {
		if attribute := identityResponse.IdentitySchema.Attributes[name]; attribute == nil || !attribute.IsRequiredForImport() {
			t.Errorf("identity %s is not required", name)
		}
	}
}

func TestValidateGroupValues(t *testing.T) {
	t.Parallel()

	if diagnostics := validateGroupValues(types.StringValue("org"), types.StringValue("directory"), types.StringNull(), types.StringValue("engineering")); diagnostics.HasError() {
		t.Fatalf("valid diagnostics = %v", diagnostics)
	}
	for name, values := range map[string][]types.String{
		"organization": {types.StringValue(" "), types.StringValue("directory"), types.StringNull(), types.StringValue("engineering")},
		"directory":    {types.StringValue("org"), types.StringValue(" "), types.StringNull(), types.StringValue("engineering")},
		"group":        {types.StringValue("org"), types.StringValue("directory"), types.StringValue(" "), types.StringNull()},
		"name":         {types.StringValue("org"), types.StringValue("directory"), types.StringNull(), types.StringValue(" ")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if diagnostics := validateGroupValues(values[0], values[1], values[2], values[3]); !diagnostics.HasError() {
				t.Fatal("validation accepted an empty value")
			}
		})
	}
}

func TestGroupResourceCreateResolvesExactName(t *testing.T) {
	t.Parallel()

	searches := 0
	creates := 0
	client := &fakeGroupClient{
		create: func(_ context.Context, organizationID, directoryID, name string, description *string) error {
			creates++
			if organizationID != "org" || directoryID != "directory" || name != "engineering" || description == nil || *description != "Managed by Terraform" {
				t.Errorf("create arguments = %q, %q, %q, %#v", organizationID, directoryID, name, description)
			}
			return nil
		},
		search: func(_ context.Context, organizationID, directoryID string, request organizationclient.SearchGroupsRequest) ([]organizationclient.Group, error) {
			searches++
			if organizationID != "org" || directoryID != "directory" || len(request.GroupNames) != 1 || request.GroupNames[0] != "engineering" {
				t.Errorf("search arguments = %q, %q, %#v", organizationID, directoryID, request)
			}
			if searches == 1 {
				return nil, nil
			}
			return []organizationclient.Group{testGroup()}, nil
		},
	}
	subject := newTestGroupResource(client)
	request, response := newGroupCreateData(t, subject, testGroupModel())
	subject.Create(context.Background(), request, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Create() diagnostics = %v", response.Diagnostics)
	}
	if creates != 1 || searches != 2 {
		t.Fatalf("creates = %d, searches = %d", creates, searches)
	}
	var state groupResourceModel
	response.Diagnostics.Append(response.State.Get(context.Background(), &state)...)
	if state.ID.ValueString() != "group-1" || state.GroupID.ValueString() != "group-1" || state.ManagedBy.ValueString() != "admins" {
		t.Fatalf("state = %#v", state)
	}
}

func TestGroupResourceCreatePollsUntilExactNameIsVisible(t *testing.T) {
	t.Parallel()

	searches := 0
	client := &fakeGroupClient{
		create: func(context.Context, string, string, string, *string) error { return nil },
		search: func(context.Context, string, string, organizationclient.SearchGroupsRequest) ([]organizationclient.Group, error) {
			searches++
			// The first search is the preflight existence check. The next two
			// model a transient read error and eventual consistency after
			// Atlassian accepts the create.
			if searches == 1 {
				return nil, nil
			}
			if searches == 2 {
				return nil, &admin.HTTPError{StatusCode: http.StatusInternalServerError}
			}
			if searches == 3 {
				return nil, nil
			}
			return []organizationclient.Group{testGroup()}, nil
		},
	}
	subject := newTestGroupResource(client)
	request, response := newGroupCreateData(t, subject, testGroupModel())
	subject.Create(context.Background(), request, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Create() diagnostics = %v", response.Diagnostics)
	}
	if searches != 4 {
		t.Fatalf("searches = %d, want 4", searches)
	}
}

func TestGroupResourceCreateRequiresExactlyOnePostCreateMatch(t *testing.T) {
	t.Parallel()

	for name, matches := range map[string][]organizationclient.Group{
		"zero":     nil,
		"multiple": {testGroup(), testGroupWithID("group-2")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			searches := 0
			client := &fakeGroupClient{
				create: func(context.Context, string, string, string, *string) error { return nil },
				search: func(context.Context, string, string, organizationclient.SearchGroupsRequest) ([]organizationclient.Group, error) {
					searches++
					if searches == 1 {
						return nil, nil
					}
					return matches, nil
				},
			}
			subject := newTestGroupResource(client)
			request, response := newGroupCreateData(t, subject, testGroupModel())
			subject.Create(context.Background(), request, response)
			if !response.Diagnostics.HasError() {
				t.Fatal("Create() diagnostics = nil")
			}
			if name == "multiple" && !strings.Contains(response.Diagnostics.Errors()[0].Detail(), "returned 2 groups") {
				t.Errorf("diagnostic = %q", response.Diagnostics.Errors()[0].Detail())
			}
		})
	}
}

func TestGroupResourceCreateVerifiesAmbiguousOutcome(t *testing.T) {
	t.Parallel()

	for name, createErr := range map[string]error{
		"transport": errors.New("connection reset after request"),
		"server":    &admin.HTTPError{StatusCode: http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			searches := 0
			client := &fakeGroupClient{
				create: func(context.Context, string, string, string, *string) error { return createErr },
				search: func(context.Context, string, string, organizationclient.SearchGroupsRequest) ([]organizationclient.Group, error) {
					searches++
					if searches == 1 {
						return nil, nil
					}
					return []organizationclient.Group{testGroup()}, nil
				},
			}
			subject := newTestGroupResource(client)
			request, response := newGroupCreateData(t, subject, testGroupModel())
			subject.Create(context.Background(), request, response)
			if response.Diagnostics.HasError() {
				t.Fatalf("Create() diagnostics = %v", response.Diagnostics)
			}
		})
	}
}

func TestGroupResourceReadRefreshesAndRemoves404(t *testing.T) {
	t.Parallel()

	for name, get := range map[string]func(context.Context, string, string, string) (organizationclient.Group, error){
		"refresh": func(context.Context, string, string, string) (organizationclient.Group, error) {
			group := testGroup()
			updated := "Updated description"
			group.Description = &updated
			return group, nil
		},
		"not found": func(context.Context, string, string, string) (organizationclient.Group, error) {
			return organizationclient.Group{}, &admin.HTTPError{StatusCode: http.StatusNotFound}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			subject := newTestGroupResource(&fakeGroupClient{get: get})
			request, response := newGroupReadData(t, subject, testGroupModel())
			subject.Read(context.Background(), request, response)
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			if name == "not found" {
				if !response.State.Raw.IsNull() {
					t.Fatalf("state = %v, want null", response.State.Raw)
				}
				return
			}
			var state groupResourceModel
			response.Diagnostics.Append(response.State.Get(context.Background(), &state)...)
			if state.Description.ValueString() != "Updated description" {
				t.Errorf("description = %q", state.Description.ValueString())
			}
		})
	}
}

func TestGroupResourceReadPreservesUnconfiguredDescription(t *testing.T) {
	t.Parallel()

	model := testGroupModel()
	model.Description = types.StringNull()
	subject := newTestGroupResource(&fakeGroupClient{get: func(context.Context, string, string, string) (organizationclient.Group, error) {
		return testGroup(), nil
	}})
	request, response := newGroupReadData(t, subject, model)
	subject.Read(context.Background(), request, response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	var state groupResourceModel
	response.Diagnostics.Append(response.State.Get(context.Background(), &state)...)
	if !state.Description.IsNull() {
		t.Errorf("description = %q, want null for unconfigured description", state.Description.ValueString())
	}
}

func TestGroupResourceDeleteBehavior(t *testing.T) {
	t.Parallel()

	for name, deleteErr := range map[string]error{"success": nil, "not found": &admin.HTTPError{StatusCode: http.StatusNotFound}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			deletes := 0
			subject := newTestGroupResource(&fakeGroupClient{delete: func(context.Context, string, string, string) error {
				deletes++
				return deleteErr
			}})
			request, response := newGroupDeleteData(t, subject, testGroupModel())
			subject.Delete(context.Background(), request, response)
			if response.Diagnostics.HasError() || deletes != 1 {
				t.Fatalf("Delete() diagnostics = %v, deletes = %d", response.Diagnostics, deletes)
			}
		})
	}

	model := testGroupModel()
	model.ManagementAccess, _ = types.ObjectValueFrom(context.Background(), managementAccessAttributeTypes(), managementAccessResultModel{
		Deletable: types.BoolValue(false), Modifiable: types.BoolValue(false), Readable: types.BoolValue(true),
	})
	subject := newTestGroupResource(&fakeGroupClient{delete: func(context.Context, string, string, string) error {
		t.Fatal("DeleteGroup must not be called for a known non-deletable group")
		return nil
	}})
	request, response := newGroupDeleteData(t, subject, model)
	subject.Delete(context.Background(), request, response)
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics.Errors()[0].Detail(), "management_access.deletable") {
		t.Fatalf("Delete() diagnostics = %v", response.Diagnostics)
	}
}

func TestGroupResourceDeletePermissionDiagnostic(t *testing.T) {
	t.Parallel()

	subject := newTestGroupResource(&fakeGroupClient{delete: func(context.Context, string, string, string) error {
		return &admin.HTTPError{StatusCode: http.StatusForbidden, Body: `{"message":"group cannot be deleted"}`}
	}})
	request, response := newGroupDeleteData(t, subject, testGroupModel())
	subject.Delete(context.Background(), request, response)
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics.Errors()[0].Detail(), "management_access.deletable") {
		t.Fatalf("Delete() diagnostics = %v", response.Diagnostics)
	}
}

func TestGroupResourceImportByStringAndIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	subject := &groupResource{}
	for name, configure := range map[string]func(*tfsdk.ResourceIdentity) resource.ImportStateRequest{
		"string": func(identity *tfsdk.ResourceIdentity) resource.ImportStateRequest {
			return resource.ImportStateRequest{ID: " org , directory , group-1 ", Identity: identity}
		},
		"identity": func(identity *tfsdk.ResourceIdentity) resource.ImportStateRequest {
			value := groupResourceIdentityModel{OrganizationID: types.StringValue("org"), DirectoryID: types.StringValue("directory"), GroupID: types.StringValue("group-1")}
			if diagnostics := identity.Set(ctx, &value); diagnostics.HasError() {
				t.Fatal(diagnostics)
			}
			return resource.ImportStateRequest{Identity: identity}
		},
	} {
		t.Run(name, func(t *testing.T) {
			identity, response := newGroupImportData(t, subject)
			request := configure(identity)
			if request.ID == "" {
				response.Identity.Raw = identity.Raw.Copy()
			}
			subject.ImportState(ctx, request, response)
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			var state groupResourceModel
			response.Diagnostics.Append(response.State.Get(ctx, &state)...)
			if state.ID.ValueString() != "group-1" || state.GroupID.ValueString() != "group-1" {
				t.Fatalf("state = %#v", state)
			}
		})
	}
	for _, id := range []string{"org,directory", "org,,group"} {
		if _, err := parseGroupImportID(id); err == nil {
			t.Errorf("parseGroupImportID(%q) error = nil", id)
		}
	}
}

func testGroup() organizationclient.Group {
	name := "engineering"
	description := "Managed by Terraform"
	directoryID := "directory"
	externalSynced := false
	managedBy := "admins"
	deletable, modifiable, readable := true, true, true
	return organizationclient.Group{
		ID: "group-1", Name: &name, Description: &description, DirectoryID: &directoryID,
		ExternalSynced: &externalSynced, ManagedBy: &managedBy,
		ManagementAccess: &organizationclient.ManagementAccess{Deletable: &deletable, Modifiable: &modifiable, Readable: &readable},
	}
}

func testGroupWithID(id string) organizationclient.Group {
	group := testGroup()
	group.ID = id
	return group
}

func testGroupModel() groupResourceModel {
	return groupResourceModel{
		ID: types.StringValue("group-1"), OrganizationID: types.StringValue("org"), DirectoryID: types.StringValue("directory"),
		GroupID: types.StringValue("group-1"), Name: types.StringValue("engineering"), Description: types.StringValue("Managed by Terraform"),
		ExternalSynced: types.BoolNull(), ManagedBy: types.StringNull(), ManagementAccess: types.ObjectNull(managementAccessAttributeTypes()),
	}
}

func newGroupCreateData(t *testing.T, subject *groupResource, model groupResourceModel) (resource.CreateRequest, *resource.CreateResponse) {
	t.Helper()
	schemaValue, identityValue := groupSchemas(t, subject)
	ctx := context.Background()
	plan := tfsdk.Plan{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue}
	model.ID = types.StringUnknown()
	model.GroupID = types.StringUnknown()
	if diagnostics := plan.Set(ctx, &model); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	identityType := identityValue.Type().TerraformType(ctx)
	return resource.CreateRequest{Plan: plan}, &resource.CreateResponse{
		State:    tfsdk.State{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue},
		Identity: &tfsdk.ResourceIdentity{Raw: tftypes.NewValue(identityType, nil), Schema: identityValue},
	}
}

func newGroupReadData(t *testing.T, subject *groupResource, model groupResourceModel) (resource.ReadRequest, *resource.ReadResponse) {
	t.Helper()
	schemaValue, identityValue := groupSchemas(t, subject)
	ctx := context.Background()
	state := tfsdk.State{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue}
	if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	identityType := identityValue.Type().TerraformType(ctx)
	return resource.ReadRequest{State: state}, &resource.ReadResponse{State: state, Identity: &tfsdk.ResourceIdentity{Raw: tftypes.NewValue(identityType, nil), Schema: identityValue}}
}

func newGroupDeleteData(t *testing.T, subject *groupResource, model groupResourceModel) (resource.DeleteRequest, *resource.DeleteResponse) {
	t.Helper()
	schemaValue, _ := groupSchemas(t, subject)
	ctx := context.Background()
	state := tfsdk.State{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue}
	if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	return resource.DeleteRequest{State: state}, &resource.DeleteResponse{}
}

func newGroupImportData(t *testing.T, subject *groupResource) (*tfsdk.ResourceIdentity, *resource.ImportStateResponse) {
	t.Helper()
	schemaValue, identityValue := groupSchemas(t, subject)
	ctx := context.Background()
	identityType := identityValue.Type().TerraformType(ctx)
	identity := &tfsdk.ResourceIdentity{Raw: tftypes.NewValue(identityType, nil), Schema: identityValue}
	return identity, &resource.ImportStateResponse{
		State:    tfsdk.State{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue},
		Identity: &tfsdk.ResourceIdentity{Raw: tftypes.NewValue(identityType, nil), Schema: identityValue},
	}
}

func groupSchemas(t *testing.T, subject *groupResource) (resourceschema.Schema, identityschema.Schema) {
	t.Helper()
	ctx := context.Background()
	var schemaResponse resource.SchemaResponse
	subject.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)
	var identityResponse resource.IdentitySchemaResponse
	subject.IdentitySchema(ctx, resource.IdentitySchemaRequest{}, &identityResponse)
	if schemaResponse.Diagnostics.HasError() || identityResponse.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics = %v, identity diagnostics = %v", schemaResponse.Diagnostics, identityResponse.Diagnostics)
	}
	return schemaResponse.Schema, identityResponse.IdentitySchema
}

func newTestGroupResource(client organizationGroupClient) *groupResource {
	return &groupResource{
		client:                 client,
		resolutionTimeout:      100 * time.Millisecond,
		resolutionInitialDelay: time.Millisecond,
		resolutionMaximumDelay: 2 * time.Millisecond,
	}
}
