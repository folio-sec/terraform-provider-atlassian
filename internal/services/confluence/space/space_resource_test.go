package space

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// fakeSpaceWrite is an httptest fake covering the write-path endpoints the
// resource drives: v2 createSpace, v2 getSpaceById, v1 updateSpace, v1
// deleteSpace and v1 getTask.
type fakeSpaceWrite struct {
	t *testing.T

	mu       sync.Mutex
	requests []recordedRequest
	bodies   map[string][]byte

	createStatus int
	createBody   string
	// getSpaceByID maps a space id to its getSpaceById response body.
	getSpaceByID map[string]string
	updateStatus int
	updateBody   string
	deleteStatus int
	deleteBody   string
	taskBody     string

	// tokenStatus, when set to a non-200, makes the OAuth token endpoint fail.
	tokenStatus int
}

func newFakeSpaceWrite(t *testing.T) (*fakeSpaceWrite, *httptest.Server) {
	t.Helper()
	f := &fakeSpaceWrite{
		t:            t,
		bodies:       map[string][]byte{},
		getSpaceByID: map[string]string{},
		createStatus: http.StatusCreated,
		updateStatus: http.StatusOK,
		deleteStatus: http.StatusAccepted,
	}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	return f, server
}

func (f *fakeSpaceWrite) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	body := readBody(f.t, r)
	f.requests = append(f.requests, recordedRequest{path: r.URL.Path, query: map[string][]string(r.URL.Query())})
	f.bodies[r.Method+" "+r.URL.Path] = body
	f.mu.Unlock()

	switch {
	case r.URL.Path == "/oauth/token":
		if f.tokenStatus != 0 && f.tokenStatus != http.StatusOK {
			w.WriteHeader(f.tokenStatus)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"t","token_type":"Bearer","expires_in":3600}`))
	case r.Method == http.MethodPost && r.URL.Path == "/wiki/api/v2/spaces":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.createStatus)
		_, _ = w.Write([]byte(f.createBody))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/wiki/api/v2/spaces/"):
		id := strings.TrimPrefix(r.URL.Path, "/wiki/api/v2/spaces/")
		body, ok := f.getSpaceByID[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[{"status":404,"code":"NOT_FOUND","title":"not found"}]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/wiki/rest/api/space/"):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.updateStatus)
		_, _ = w.Write([]byte(f.updateBody))
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/wiki/rest/api/space/"):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.deleteStatus)
		_, _ = w.Write([]byte(f.deleteBody))
	case r.Method == http.MethodGet && r.URL.Path == "/wiki/rest/api/longtask/37814273":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.taskBody))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func readBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return body
}

func (f *fakeSpaceWrite) bodyOf(method, path string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[method+" "+path]
}

func (f *fakeSpaceWrite) requestCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, req := range f.requests {
		if req.path == path {
			n++
		}
	}
	return n
}

func (f *fakeSpaceWrite) requestedPath(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, req := range f.requests {
		if req.path == path {
			return true
		}
	}
	return false
}

// newTestServiceAccountClient points a service-account client at the fake,
// including its token endpoint, so the authenticate-before-send boundary can
// be exercised.
func newTestServiceAccountClient(t *testing.T, server *httptest.Server) *confluence.Client {
	t.Helper()
	c, err := confluence.NewForTest(confluence.Config{
		Mode: confluence.AuthServiceAccount, SiteURL: server.URL,
		ClientID: "id", ClientSecret: "secret", CloudID: "a7c408f1-ec5f-410e-8f27-62dbefebe6b6",
	}, server.URL)
	if err != nil {
		t.Fatalf("confluence.NewForTest() error = %v", err)
	}
	return c
}

func newTestWriteClient(t *testing.T, server *httptest.Server) *confluence.Client {
	t.Helper()
	c, err := confluence.New(confluence.Config{Mode: confluence.AuthBasic, SiteURL: server.URL, Email: "user@example.com", APIToken: "token"})
	if err != nil {
		t.Fatalf("confluence.New() error = %v", err)
	}
	return c
}

// TestCreateSpaceThenReadFillsSpaceOwnerID exercises gate 2's finding: the
// create response lacks spaceOwnerId, which only the following read supplies.
func TestCreateSpaceThenReadFillsSpaceOwnerID(t *testing.T) {
	t.Parallel()
	fake, server := newFakeSpaceWrite(t)
	fake.createBody = `{"id":"10001","key":"DEMO","name":"Demo Space","type":"global","status":"current","authorId":"712020:author","createdAt":"2026-01-01T00:00:00.000Z"}`
	fake.getSpaceByID["10001"] = `{
		"id":"10001","key":"DEMO","name":"Demo Space","type":"global","status":"current",
		"authorId":"712020:author","spaceOwnerId":"712020:owner",
		"createdAt":"2026-01-01T00:00:00.000Z",
		"description":{"plain":{"value":"A demo space.","representation":"plain"}}
	}`
	service := NewService(newTestWriteClient(t, server))

	created, err := service.CreateSpace(context.Background(), CreateSpaceRequest{
		Key:  "DEMO",
		Name: "Demo Space",
		Description: &Description{
			Value:          "A demo space.",
			Representation: "plain",
		},
	})
	if err != nil {
		t.Fatalf("CreateSpace() error = %v", err)
	}
	if created.ID != "10001" || created.Key != "DEMO" {
		t.Fatalf("CreateSpace() result = %#v", created)
	}

	current, err := service.GetSpaceByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetSpaceByID() error = %v", err)
	}
	if current.SpaceOwnerID == nil || *current.SpaceOwnerID != "712020:owner" {
		t.Fatalf("SpaceOwnerID = %#v, want 712020:owner (create response never carries it)", current.SpaceOwnerID)
	}

	var createBody map[string]any
	if err := json.Unmarshal(fake.bodyOf(http.MethodPost, "/wiki/api/v2/spaces"), &createBody); err != nil {
		t.Fatalf("unmarshal create request body: %v", err)
	}
	description, ok := createBody["description"].(map[string]any)
	if !ok {
		t.Fatalf("create request body description = %#v, want a nested {value, representation} object", createBody["description"])
	}
	if description["value"] != "A demo space." || description["representation"] != "plain" {
		t.Fatalf("create request body description = %#v", description)
	}
}

// TestCreateSpaceNotRetriedOn500 asserts createSpace is non-idempotent and
// therefore sent through confluence.WithoutRetry: a 500 must surface after a
// single attempt, never be retried.
func TestCreateSpaceNotRetriedOn500(t *testing.T) {
	t.Parallel()
	fake, server := newFakeSpaceWrite(t)
	fake.createStatus = http.StatusInternalServerError
	fake.createBody = `{"message":"boom"}`
	service := NewService(newTestWriteClient(t, server))

	if _, err := service.CreateSpace(context.Background(), CreateSpaceRequest{Key: "DEMO", Name: "Demo Space"}); err == nil {
		t.Fatal("CreateSpace() error = nil, want the 500 to surface")
	}
	if got := fake.requestCount("/wiki/api/v2/spaces"); got != 1 {
		t.Fatalf("create requests = %d, want 1 (non-idempotent create must never be retried)", got)
	}
}

// TestUpdateSpaceSendsOnlyChangedFields exercises the resource's
// updateRequestFromDiff contract at the service boundary: a request with only
// Name set must produce a PUT body with exactly {"name": ...}, and a tiny
// response body (not the ~80KB real one) must still succeed since UpdateSpace
// never parses the response beyond its status code.
func TestUpdateSpaceSendsOnlyChangedFields(t *testing.T) {
	t.Parallel()
	fake, server := newFakeSpaceWrite(t)
	fake.updateBody = `{}`
	service := NewService(newTestWriteClient(t, server))

	name := "Renamed Space"
	if err := service.UpdateSpace(context.Background(), "DEMO", UpdateSpaceRequest{Name: &name}); err != nil {
		t.Fatalf("UpdateSpace() error = %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(fake.bodyOf(http.MethodPut, "/wiki/rest/api/space/DEMO"), &body); err != nil {
		t.Fatalf("unmarshal update request body: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("update request body = %#v, want exactly {\"name\": ...}", body)
	}
	if body["name"] != name {
		t.Fatalf("update request body name = %#v, want %q", body["name"], name)
	}
}

// TestDeleteSpacePollsTaskByIDNotServerSuppliedLink asserts the delete task
// is polled at /wiki/rest/api/longtask/{id} and that the server-supplied
// links.status (which 404s: it omits the /wiki prefix) is never requested.
func TestDeleteSpacePollsTaskByIDNotServerSuppliedLink(t *testing.T) {
	t.Parallel()
	fake, server := newFakeSpaceWrite(t)
	fake.deleteBody = `{"id":"37814273","links":{"status":"/rest/api/longtask/37814273"}}`
	fake.taskBody = `{"id":"37814273","finished":true,"successful":true}`
	service := NewService(newTestWriteClient(t, server))

	if err := service.DeleteSpace(context.Background(), "DEMO"); err != nil {
		t.Fatalf("DeleteSpace() error = %v", err)
	}
	if !fake.requestedPath("/wiki/rest/api/longtask/37814273") {
		t.Fatal("expected a poll of /wiki/rest/api/longtask/37814273")
	}
	if fake.requestedPath("/rest/api/longtask/37814273") {
		t.Fatal("the server-supplied links.status path must never be requested: it omits the /wiki prefix and 404s")
	}
}

func TestDeleteSpace404IsAlreadyDeleted(t *testing.T) {
	t.Parallel()
	fake, server := newFakeSpaceWrite(t)
	fake.deleteStatus = http.StatusNotFound
	fake.deleteBody = `{"statusCode":404,"message":"No space found"}`
	service := NewService(newTestWriteClient(t, server))

	if err := service.DeleteSpace(context.Background(), "DEMO"); err != nil {
		t.Fatalf("DeleteSpace() error = %v, want nil for an already-deleted space", err)
	}
}

func TestDeleteSpaceTaskFailureReportsMessage(t *testing.T) {
	t.Parallel()
	fake, server := newFakeSpaceWrite(t)
	fake.deleteBody = `{"id":"37814273","links":{"status":"/rest/api/longtask/37814273"}}`
	fake.taskBody = `{"id":"37814273","finished":true,"successful":false,"messages":[{"translation":"could not delete space"}]}`
	service := NewService(newTestWriteClient(t, server))

	err := service.DeleteSpace(context.Background(), "DEMO")
	if err == nil {
		t.Fatal("DeleteSpace() error = nil, want the failed task to surface")
	}
	if !strings.Contains(err.Error(), "could not delete space") {
		t.Fatalf("DeleteSpace() error = %v, want it to contain the task message", err)
	}
}

// --- resource-level: ValidateConfig, ImportState ---

func spaceResourceSchemas(t *testing.T) (resourceschema.Schema, identityschema.Schema) {
	t.Helper()
	ctx := context.Background()
	subject := &spaceResource{}
	var schemaResponse resource.SchemaResponse
	subject.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)
	var identityResponse resource.IdentitySchemaResponse
	subject.IdentitySchema(ctx, resource.IdentitySchemaRequest{}, &identityResponse)
	if schemaResponse.Diagnostics.HasError() || identityResponse.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics = %v, identity diagnostics = %v", schemaResponse.Diagnostics, identityResponse.Diagnostics)
	}
	return schemaResponse.Schema, identityResponse.IdentitySchema
}

func baseSpaceResourceModel() spaceResourceModel {
	return spaceResourceModel{
		ID:                           types.StringUnknown(),
		Key:                          types.StringNull(),
		Alias:                        types.StringNull(),
		Name:                         types.StringValue("Demo Space"),
		Description:                  types.ObjectNull(descriptionAttributeTypes()),
		Type:                         types.StringUnknown(),
		Status:                       types.StringNull(),
		HomepageID:                   types.StringUnknown(),
		TemplateKey:                  types.StringNull(),
		CopySpaceAccessConfiguration: types.StringNull(),
		CreatePrivateSpace:           types.BoolNull(),
		RoleAssignments:              types.SetNull(types.ObjectType{AttrTypes: roleAssignmentAttributeTypes()}),
		AuthorID:                     types.StringUnknown(),
		SpaceOwnerID:                 types.StringUnknown(),
		CreatedAt:                    types.StringUnknown(),
		CurrentActiveAlias:           types.StringUnknown(),
	}
}

func validateSpaceResourceConfig(t *testing.T, model spaceResourceModel) resource.ValidateConfigResponse {
	t.Helper()
	ctx := context.Background()
	schemaValue, _ := spaceResourceSchemas(t)
	state := tfsdk.State{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue}
	if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
		t.Fatalf("build config: %v", diagnostics)
	}
	config := tfsdk.Config{Raw: state.Raw, Schema: schemaValue}
	var resp resource.ValidateConfigResponse
	(&spaceResource{}).ValidateConfig(ctx, resource.ValidateConfigRequest{Config: config}, &resp)
	return resp
}

func TestSpaceResourceValidateConfigIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*spaceResourceModel)
		wantErr bool
	}{
		{name: "key only is valid", mutate: func(m *spaceResourceModel) { m.Key = types.StringValue("DEMO") }},
		{name: "alias only is valid", mutate: func(m *spaceResourceModel) { m.Alias = types.StringValue("demoalias") }},
		{
			name: "both key and alias is an error",
			mutate: func(m *spaceResourceModel) {
				m.Key = types.StringValue("DEMO")
				m.Alias = types.StringValue("demoalias")
			},
			wantErr: true,
		},
		{name: "neither key nor alias is an error", mutate: func(m *spaceResourceModel) {}, wantErr: true},
		{name: "blank key is an error", mutate: func(m *spaceResourceModel) { m.Key = types.StringValue(" ") }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			model := baseSpaceResourceModel()
			tt.mutate(&model)
			resp := validateSpaceResourceConfig(t, model)
			if resp.Diagnostics.HasError() != tt.wantErr {
				t.Fatalf("ValidateConfig() diagnostics = %v, wantErr %v", resp.Diagnostics, tt.wantErr)
			}
		})
	}
}

func TestSpaceResourceValidateConfigDescriptionRepresentation(t *testing.T) {
	t.Parallel()
	model := baseSpaceResourceModel()
	model.Key = types.StringValue("DEMO")
	description, err := descriptionValue(context.Background(), &Description{Value: "hi", Representation: "view"})
	if err != nil {
		t.Fatalf("build description: %v", err)
	}
	model.Description = description

	resp := validateSpaceResourceConfig(t, model)
	if !resp.Diagnostics.HasError() {
		t.Fatal("ValidateConfig() diagnostics = nil, want an error for representation != plain")
	}
}

func TestSpaceResourceValidateConfigStatusTrashedRejected(t *testing.T) {
	t.Parallel()
	model := baseSpaceResourceModel()
	model.Key = types.StringValue("DEMO")
	model.Status = types.StringValue("trashed")

	resp := validateSpaceResourceConfig(t, model)
	if !resp.Diagnostics.HasError() {
		t.Fatal("ValidateConfig() diagnostics = nil, want an error for status = trashed")
	}
}

func TestSpaceResourceValidateConfigRoleAssignmentPrincipalType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	model := baseSpaceResourceModel()
	model.Key = types.StringValue("DEMO")

	principal, diagnostics := types.ObjectValueFrom(ctx, principalAttributeTypes(), principalResourceModel{
		PrincipalType: types.StringValue("NOT_A_TYPE"),
		PrincipalID:   types.StringValue("712020:user"),
	})
	if diagnostics.HasError() {
		t.Fatalf("build principal: %v", diagnostics)
	}
	assignment, diagnostics := types.ObjectValueFrom(ctx, roleAssignmentAttributeTypes(), roleAssignmentResourceModel{
		Principal: principal,
		RoleID:    types.StringValue("admin"),
	})
	if diagnostics.HasError() {
		t.Fatalf("build assignment: %v", diagnostics)
	}
	assignments, diagnostics := types.SetValueFrom(ctx, types.ObjectType{AttrTypes: roleAssignmentAttributeTypes()}, []types.Object{assignment})
	if diagnostics.HasError() {
		t.Fatalf("build role_assignments: %v", diagnostics)
	}
	model.RoleAssignments = assignments

	resp := validateSpaceResourceConfig(t, model)
	if !resp.Diagnostics.HasError() {
		t.Fatal("ValidateConfig() diagnostics = nil, want an error for an invalid principal_type")
	}
}

// --- D1-D4 correctness fixes ---

// TestCreatePrivateSpaceRequiresReplace guards against a regression to D1's
// bug: create_private_space had an empty plan-modifier list even though its
// description says changing it replaces the space.
func TestCreatePrivateSpaceRequiresReplace(t *testing.T) {
	t.Parallel()
	schemaValue, _ := spaceResourceSchemas(t)
	attribute, ok := schemaValue.Attributes["create_private_space"].(resourceschema.BoolAttribute)
	if !ok {
		t.Fatalf("create_private_space attribute = %#v, want a BoolAttribute", schemaValue.Attributes["create_private_space"])
	}
	if len(attribute.PlanModifiers) == 0 {
		t.Fatal("create_private_space has no plan modifiers, want boolplanmodifier.RequiresReplace()")
	}
}

// TestDeleteConfirmsSpaceIsGone exercises D2: Delete must confirm the
// deletion with a read after DeleteSpace returns nil, not just trust the
// finished delete task.
func TestDeleteConfirmsSpaceIsGone(t *testing.T) {
	t.Parallel()

	t.Run("confirmed by 404", func(t *testing.T) {
		t.Parallel()
		fake, server := newFakeSpaceWrite(t)
		fake.deleteBody = `{"id":"37814273","links":{"status":"/rest/api/longtask/37814273"}}`
		fake.taskBody = `{"id":"37814273","finished":true,"successful":true}`
		subject := &spaceResource{client: NewService(newTestWriteClient(t, server))}

		resp := runSpaceDelete(t, subject, "DEMO", "10001")
		if resp.Diagnostics.HasError() {
			t.Fatalf("Delete() diagnostics = %v", resp.Diagnostics)
		}
	})

	t.Run("space still readable after delete task finished is an error", func(t *testing.T) {
		t.Parallel()
		fake, server := newFakeSpaceWrite(t)
		fake.deleteBody = `{"id":"37814273","links":{"status":"/rest/api/longtask/37814273"}}`
		fake.taskBody = `{"id":"37814273","finished":true,"successful":true}`
		fake.getSpaceByID["10001"] = `{"id":"10001","key":"DEMO","name":"Demo Space","type":"global","status":"current","authorId":"712020:author"}`
		subject := &spaceResource{client: NewService(newTestWriteClient(t, server))}

		resp := runSpaceDelete(t, subject, "DEMO", "10001")
		if !resp.Diagnostics.HasError() {
			t.Fatal("Delete() diagnostics = nil, want an error: the space is still readable after the delete task finished")
		}
		if !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "space still readable after delete task finished") {
			t.Fatalf("Delete() diagnostics = %v", resp.Diagnostics)
		}
	})
}

func runSpaceDelete(t *testing.T, subject *spaceResource, key, id string) *resource.DeleteResponse {
	t.Helper()
	ctx := context.Background()
	schemaValue, _ := spaceResourceSchemas(t)
	model := baseSpaceResourceModel()
	model.ID = types.StringValue(id)
	model.Key = types.StringValue(key)
	state := tfsdk.State{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue}
	if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
		t.Fatalf("build state: %v", diagnostics)
	}
	resp := &resource.DeleteResponse{}
	subject.Delete(ctx, resource.DeleteRequest{State: state}, resp)
	return resp
}

// TestAliasFromSpace exercises D3: Read must never store an unknown alias
// (ImportState sets it unknown), and current_active_alias stands in for it
// once known, per gate 3.
func TestAliasFromSpace(t *testing.T) {
	t.Parallel()

	alias := "demo-alias"
	tests := []struct {
		name    string
		base    types.String
		current Space
		want    types.String
	}{
		{
			name:    "unknown base with a known current_active_alias resolves to it",
			base:    types.StringUnknown(),
			current: Space{CurrentActiveAlias: &alias},
			want:    types.StringValue(alias),
		},
		{
			name:    "unknown base with no current_active_alias resolves to null",
			base:    types.StringUnknown(),
			current: Space{},
			want:    types.StringNull(),
		},
		{
			name:    "known base passes through unchanged",
			base:    types.StringValue("prior-alias"),
			current: Space{CurrentActiveAlias: &alias},
			want:    types.StringValue("prior-alias"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := aliasFromSpace(tt.base, tt.current); !got.Equal(tt.want) {
				t.Fatalf("aliasFromSpace() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// TestDescriptionSchemaOptionalComputedUseStateForUnknown exercises D4:
// description must be Optional+Computed with UseStateForUnknown so an
// omitted block keeps the state value, and the schema must still pass the
// framework's own implementation validation.
func TestDescriptionSchemaOptionalComputedUseStateForUnknown(t *testing.T) {
	t.Parallel()
	schemaValue, _ := spaceResourceSchemas(t)

	if diagnostics := schemaValue.ValidateImplementation(context.Background()); diagnostics.HasError() {
		t.Fatalf("Schema.ValidateImplementation() diagnostics = %v", diagnostics)
	}

	attribute, ok := schemaValue.Attributes["description"].(resourceschema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("description attribute = %#v, want a SingleNestedAttribute", schemaValue.Attributes["description"])
	}
	if !attribute.Optional || !attribute.Computed {
		t.Fatalf("description Optional=%v Computed=%v, want both true", attribute.Optional, attribute.Computed)
	}
	if len(attribute.PlanModifiers) != 1 {
		t.Fatalf("description has %d plan modifiers, want exactly one (objectplanmodifier.UseStateForUnknown())", len(attribute.PlanModifiers))
	}
	value, ok := attribute.Attributes["value"].(resourceschema.StringAttribute)
	if !ok {
		t.Fatalf("description.value attribute = %#v, want a StringAttribute", attribute.Attributes["value"])
	}
	if !value.Required {
		t.Fatal("description.value is not Required")
	}
}

func TestSpaceResourceImportState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "numeric id is accepted", id: "10001"},
		{name: "blank id is rejected", id: "  ", wantErr: true},
		{name: "non-numeric id is rejected", id: "DEMO", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			subject := &spaceResource{}
			schemaValue, identityValue := spaceResourceSchemas(t)
			identityType := identityValue.Type().TerraformType(ctx)
			resp := &resource.ImportStateResponse{
				State:    tfsdk.State{Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil), Schema: schemaValue},
				Identity: &tfsdk.ResourceIdentity{Raw: tftypes.NewValue(identityType, nil), Schema: identityValue},
			}
			subject.ImportState(ctx, resource.ImportStateRequest{ID: tt.id}, resp)
			if resp.Diagnostics.HasError() != tt.wantErr {
				t.Fatalf("ImportState(%q) diagnostics = %v, wantErr %v", tt.id, resp.Diagnostics, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			var identity spaceResourceIdentityModel
			resp.Diagnostics.Append(resp.Identity.Get(ctx, &identity)...)
			if identity.ID.ValueString() != strings.TrimSpace(tt.id) {
				t.Fatalf("identity.ID = %q, want %q", identity.ID.ValueString(), tt.id)
			}
		})
	}
}

// TestIsAmbiguousCreateFailure pins which failures may reach the adoption
// path. Adoption binds an existing space to this resource, and a wrongly
// adopted space is permanently deleted on the next destroy, so a failure that
// never reached Atlassian must never get there.
func TestIsAmbiguousCreateFailure(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  error
		want bool
	}{
		"transport failure with no status is ambiguous": {
			err:  fmt.Errorf("create space: %w", errors.New("connection reset by peer")),
			want: true,
		},
		"a 4xx is Atlassian declining the request": {
			err:  fmt.Errorf("create space: %w", &confluence.HTTPError{StatusCode: http.StatusBadRequest}),
			want: false,
		},
		"a 5xx can arrive after the space was committed": {
			err:  fmt.Errorf("create space: %w", &confluence.HTTPError{StatusCode: http.StatusBadGateway}),
			want: true,
		},
		"a gateway failure is equally unsettled": {
			err:  fmt.Errorf("create space: %w", &confluence.HTTPError{StatusCode: http.StatusNotFound, GatewayRouting: true}),
			want: true,
		},
		"an authorize failure never left the client": {
			err:  fmt.Errorf("create space: %w: %w", ErrRequestNotSent, confluence.ErrAuthorize),
			want: false,
		},
		"a local conversion failure never left the client": {
			err:  fmt.Errorf("create space: %w: parse copy_space_access_configuration %q: bad", ErrRequestNotSent, "abc"),
			want: false,
		},
		"a transport that could not be built never left the client": {
			err:  fmt.Errorf("create space: %w: %w", ErrRequestNotSent, errors.New("discover cloud id")),
			want: false,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := isAmbiguousCreateFailure(tt.err); got != tt.want {
				t.Errorf("isAmbiguousCreateFailure(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestCreateSpaceRejectsBadCopySourceWithoutSendingAnything checks the same
// boundary end to end: an unparseable copy_space_access_configuration must
// fail before any HTTP request is made, and must be marked as not sent.
func TestCreateSpaceRejectsBadCopySourceWithoutSendingAnything(t *testing.T) {
	t.Parallel()
	fake, server := newFakeSpaceWrite(t)
	service := NewService(newTestWriteClient(t, server))

	_, err := service.CreateSpace(context.Background(), CreateSpaceRequest{
		Name: "x", Key: "X", CopySpaceAccessConfiguration: "not-a-number",
	})
	if err == nil || !errors.Is(err, ErrRequestNotSent) {
		t.Fatalf("CreateSpace() error = %v, want one wrapping ErrRequestNotSent", err)
	}
	if got := fake.requestCount("/wiki/api/v2/spaces"); got != 0 {
		t.Errorf("createSpace requests = %d, want 0: the value is converted before anything is sent", got)
	}
}

// TestComputedAttributesSurviveAnUpdatePlan guards the live-run finding that
// author_id, space_owner_id, created_at and current_active_alias went "known
// after apply" on every update because they carried no plan modifier.
func TestComputedAttributesSurviveAnUpdatePlan(t *testing.T) {
	t.Parallel()

	schemaValue, _ := spaceResourceSchemas(t)
	for _, name := range []string{"author_id", "space_owner_id", "created_at", "current_active_alias"} {
		attribute, ok := schemaValue.Attributes[name].(resourceschema.StringAttribute)
		if !ok {
			t.Fatalf("%s is %T, want resourceschema.StringAttribute", name, schemaValue.Attributes[name])
		}
		if len(attribute.PlanModifiers) == 0 {
			t.Errorf("%s has no plan modifier; an update plan would show it as known after apply", name)
		}
	}
}

// TestImportHintNamesTheSpace pins what a post-create failure has to tell the
// operator. State is written, so the space is not orphaned -- but Terraform
// taints a resource whose Create errored, and the resulting plan replaces it,
// which for a space means permanent deletion. The diagnostic therefore has to
// name the space and point at untaint, not at another apply.
func TestImportHintNamesTheSpace(t *testing.T) {
	t.Parallel()

	got := importHint("37552130", "Reading the space back failed: boom.")
	for _, want := range []string{"37552130", "tainted", "terraform untaint", "terraform import", "boom"} {
		if !strings.Contains(got, want) {
			t.Errorf("importHint() = %q, want it to contain %q", got, want)
		}
	}
	// The old wording promised the next apply would reconcile the space. It
	// would have replaced it instead, so that promise must not come back.
	for _, forbidden := range []string{"will read and reconcile", "next apply will read"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("importHint() must not promise reconciliation: %q", got)
		}
	}
}

// TestCreateFailuresAfterTheSpaceExistsAllNameTheID walks the failure paths
// that run once createSpace has already succeeded and asserts each one is
// routed through importHint, rather than checking the wording of each.
func TestCreateFailuresAfterTheSpaceExistsAllNameTheID(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("space_resource.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	body := string(source)
	start := strings.Index(body, "func (r *spaceResource) finishCreate(")
	if start < 0 {
		t.Fatal("finishCreate not found")
	}
	end := strings.Index(body[start:], "\n}\n")
	if end < 0 {
		t.Fatal("end of finishCreate not found")
	}
	finishCreate := body[start : start+end]

	addErrors := strings.Count(finishCreate, "resp.Diagnostics.AddError(")
	hints := strings.Count(finishCreate, "importHint(id,")
	if addErrors != hints {
		t.Errorf("finishCreate has %d AddError calls but %d importHint calls; every failure after the space exists must name its id", addErrors, hints)
	}
}

// TestCreateSpaceMarksAuthorizeFailureAsNotSent covers the boundary the
// generated clients create: request editors run before the request is sent,
// so a credential that cannot be obtained fails with nothing dispatched. A
// caller that read that as an unsettled outcome would go looking for a space
// that cannot exist.
func TestCreateSpaceMarksAuthorizeFailureAsNotSent(t *testing.T) {
	t.Parallel()
	fake, server := newFakeSpaceWrite(t)
	fake.tokenStatus = http.StatusUnauthorized
	service := NewService(newTestServiceAccountClient(t, server))

	_, err := service.CreateSpace(context.Background(), CreateSpaceRequest{Name: "n", Key: "K"})
	if err == nil || !errors.Is(err, ErrRequestNotSent) {
		t.Fatalf("CreateSpace() error = %v, want one wrapping ErrRequestNotSent", err)
	}
	if got := fake.requestCount("/wiki/api/v2/spaces"); got != 0 {
		t.Errorf("createSpace requests = %d, want 0", got)
	}
}

// TestCreateSpaceKeepsTheIDWithoutAKey checks that a create response missing
// the key does not cost us the id. The caller reads the space by id and takes
// the key from that read, so discarding the id would leave a space that exists
// but cannot be tracked.
func TestCreateSpaceKeepsTheIDWithoutAKey(t *testing.T) {
	t.Parallel()
	fake, server := newFakeSpaceWrite(t)
	fake.createBody = `{"id":"37552130","name":"n","type":"global","status":"current","authorId":"a"}`
	service := NewService(newTestWriteClient(t, server))

	result, err := service.CreateSpace(context.Background(), CreateSpaceRequest{Name: "n", Key: "K"})
	if err != nil {
		t.Fatalf("CreateSpace() error = %v", err)
	}
	if result.ID != "37552130" {
		t.Errorf("ID = %q, want 37552130", result.ID)
	}
	if result.Key != "" {
		t.Errorf("Key = %q, want empty", result.Key)
	}
}

// TestUpdateSpaceIgnoresAnUndecodableBody is the point of taking the raw
// response: the API commits the update and then returns roughly 80 KB that
// need not fit the generated model. Decoding it would report a committed
// mutation as a failure.
func TestUpdateSpaceIgnoresAnUndecodableBody(t *testing.T) {
	t.Parallel()
	fake, server := newFakeSpaceWrite(t)
	fake.updateBody = `{"permissions": "this is a string where the model expects an array"}`
	service := NewService(newTestWriteClient(t, server))

	name := "renamed"
	if err := service.UpdateSpace(context.Background(), "K", UpdateSpaceRequest{Name: &name}); err != nil {
		t.Fatalf("UpdateSpace() error = %v, want nil: the status is what matters", err)
	}
	if got := fake.requestCount("/wiki/rest/api/space/K"); got != 1 {
		t.Errorf("update requests = %d, want 1", got)
	}
}
