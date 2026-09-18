package space

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type fakeSpaceRoles struct {
	t *testing.T

	mu        sync.Mutex
	bodies    map[string][]byte
	deleted   bool
	staleByID bool
}

func newFakeSpaceRoles(t *testing.T) (*fakeSpaceRoles, *httptest.Server) {
	t.Helper()
	fake := &fakeSpaceRoles{t: t, bodies: map[string][]byte{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return fake, server
}

func (f *fakeSpaceRoles) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Fatalf("read request body: %v", err)
	}
	f.mu.Lock()
	f.bodies[r.Method+" "+r.URL.Path] = body
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	const role = `{"id":"role-1","type":"CUSTOM","name":"Editors","description":"Can edit","spacePermissions":["read/space"]}`
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/wiki/api/v2/space-roles":
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(role))
	case r.Method == http.MethodGet && r.URL.Path == "/wiki/api/v2/space-roles":
		f.mu.Lock()
		deleted := f.deleted
		f.mu.Unlock()
		if deleted {
			_, _ = w.Write([]byte(`{"results":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[` + role + `]}`))
	case r.Method == http.MethodGet && r.URL.Path == "/wiki/api/v2/space-roles/role-1":
		f.mu.Lock()
		deleted := f.deleted
		staleByID := f.staleByID
		f.mu.Unlock()
		if deleted && !staleByID {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[{"status":404,"code":"NOT_FOUND","title":"not found"}]}`))
			return
		}
		_, _ = w.Write([]byte(role))
	case r.Method == http.MethodPut && r.URL.Path == "/wiki/api/v2/space-roles/role-1":
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"role-1","type":"CUSTOM","name":"Editors","description":"Can edit","taskId":"task-1"}`))
	case r.Method == http.MethodDelete && r.URL.Path == "/wiki/api/v2/space-roles/role-1":
		f.mu.Lock()
		f.deleted = true
		f.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"taskId":"task-2"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[{"status":404,"code":"NOT_FOUND","title":"not found"}]}`))
	}
}

func (f *fakeSpaceRoles) body(method, path string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.bodies[method+" "+path]...)
}

func TestSpaceRoleServiceCRUD(t *testing.T) {
	t.Parallel()
	fake, server := newFakeSpaceRoles(t)
	service := NewService(newTestClient(t, server))

	created, err := service.CreateSpaceRole(context.Background(), SpaceRoleWriteRequest{
		Name: "Editors", Description: "Can edit", PermissionIDs: []string{"read/space"},
	})
	if err != nil {
		t.Fatalf("CreateSpaceRole() error = %v", err)
	}
	if created.ID != "role-1" || created.Type != "CUSTOM" {
		t.Fatalf("created role = %#v", created)
	}
	assertJSONBody(t, fake.body(http.MethodPost, "/wiki/api/v2/space-roles"), map[string]any{
		"name": "Editors", "description": "Can edit", "spacePermissions": []any{"read/space"},
	})

	got, err := service.GetSpaceRoleByID(context.Background(), "role-1")
	if err != nil {
		t.Fatalf("GetSpaceRoleByID() error = %v", err)
	}
	if !reflect.DeepEqual(got.PermissionIDs, []string{"read/space"}) {
		t.Fatalf("permission ids = %v", got.PermissionIDs)
	}

	anonymous := "role-anonymous"
	guest := "role-guest"
	updateTaskID, err := service.UpdateSpaceRole(context.Background(), "role-1", SpaceRoleWriteRequest{
		Name: "Editors", Description: "Can edit", PermissionIDs: []string{"read/space"},
		AnonymousReassignmentRoleID: &anonymous, GuestReassignmentRoleID: &guest,
	})
	if err != nil {
		t.Fatalf("UpdateSpaceRole() error = %v", err)
	}
	assertJSONBody(t, fake.body(http.MethodPut, "/wiki/api/v2/space-roles/role-1"), map[string]any{
		"name": "Editors", "description": "Can edit", "spacePermissions": []any{"read/space"},
		"anonymousReassignmentRoleId": "role-anonymous", "guestReassignmentRoleId": "role-guest",
	})
	if updateTaskID != "task-1" {
		t.Fatalf("UpdateSpaceRole() task id = %q, want task-1", updateTaskID)
	}

	taskID, err := service.DeleteSpaceRole(context.Background(), "role-1")
	if err != nil {
		t.Fatalf("DeleteSpaceRole() error = %v", err)
	}
	if taskID != "task-2" {
		t.Fatalf("DeleteSpaceRole() task id = %q, want task-2", taskID)
	}
	_, err = service.GetSpaceRoleByID(context.Background(), "role-1")
	if !confluence.IsNotFound(err) {
		t.Fatalf("GetSpaceRoleByID() after delete error = %v, want Confluence not found", err)
	}
}

func TestCreateSpaceRoleReturnsPartialIdentityFromInvalidSuccessResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"role-partial","type":"CUSTOM"}`))
	}))
	t.Cleanup(server.Close)

	created, err := NewService(newTestClient(t, server)).CreateSpaceRole(context.Background(), SpaceRoleWriteRequest{
		Name: "Viewers", Description: "Can view", PermissionIDs: []string{"read/space"},
	})
	if err == nil {
		t.Fatal("CreateSpaceRole() error = nil, want invalid success response")
	}
	if created.ID != "role-partial" || created.Type != "CUSTOM" {
		t.Fatalf("CreateSpaceRole() partial result = %#v", created)
	}
}

func TestDeleteSpaceRoleDoesNotTreatPermissionNotFoundAsAbsence(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[{"status":404,"code":"NOT_FOUND","title":"not permitted"}]}`))
	}))
	t.Cleanup(server.Close)

	_, err := NewService(newTestClient(t, server)).DeleteSpaceRole(context.Background(), "role-1")
	if !confluence.IsNotFound(err) {
		t.Fatalf("DeleteSpaceRole() error = %v, want Confluence not found", err)
	}
}

func assertJSONBody(t *testing.T, body []byte, want map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode request body %q: %v", body, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("request body = %#v, want %#v", got, want)
	}
}

func TestRoleMatchesTreatsPermissionOrderAsUnordered(t *testing.T) {
	t.Parallel()
	role := SpaceRole{Name: "Editors", Description: "Can edit", PermissionIDs: []string{"read/space", "read/comment", "export/content", "delete/own-content", "delete/own-comment"}}
	want := SpaceRoleWriteRequest{Name: "Editors", Description: "Can edit", PermissionIDs: []string{"delete/own-comment", "delete/own-content", "export/content", "read/comment", "read/space"}}
	if !roleMatches(role, want) {
		t.Fatal("roleMatches() = false for the same permission set in a different order")
	}
	want.PermissionIDs = []string{"read/space"}
	if roleMatches(role, want) {
		t.Fatal("roleMatches() = true for different permission sets")
	}
}

func TestSpaceRoleAbsentRequiresCatalogueEvidence(t *testing.T) {
	t.Parallel()
	fake, server := newFakeSpaceRoles(t)
	resource := &spaceRoleResource{client: NewService(newTestClient(t, server))}

	absent, err := resource.spaceRoleAbsent(context.Background(), "role-1")
	if err != nil {
		t.Fatalf("spaceRoleAbsent() error = %v", err)
	}
	if absent {
		t.Fatal("spaceRoleAbsent() = true while the catalogue still contains the role")
	}

	fake.mu.Lock()
	fake.deleted = true
	fake.mu.Unlock()
	absent, err = resource.spaceRoleAbsent(context.Background(), "role-1")
	if err != nil {
		t.Fatalf("spaceRoleAbsent() after delete error = %v", err)
	}
	if !absent {
		t.Fatal("spaceRoleAbsent() = false after the complete catalogue omitted the role")
	}
}

func TestWaitForSpaceRoleDeleteAcceptsCatalogueAbsenceWithStaleByIDRead(t *testing.T) {
	t.Parallel()
	fake, server := newFakeSpaceRoles(t)
	resource := &spaceRoleResource{client: NewService(newTestClient(t, server))}

	fake.mu.Lock()
	fake.deleted = true
	fake.staleByID = true
	fake.mu.Unlock()

	if _, err := resource.waitForSpaceRole(context.Background(), "role-1", nil); err != nil {
		t.Fatalf("waitForSpaceRole() error = %v", err)
	}
}

func TestSpaceRoleWriteRequestSendsConfiguredReassignment(t *testing.T) {
	t.Parallel()
	model := spaceRoleResourceModel{
		Name: types.StringValue("Editors"), Description: types.StringValue("Can edit"),
		SpacePermissions:            types.SetValueMust(types.StringType, []attr.Value{types.StringValue("read/space")}),
		AnonymousReassignmentRoleID: types.StringValue("role-anonymous"),
		GuestReassignmentRoleID:     types.StringNull(),
	}

	created, diagnostics := spaceRoleWriteRequest(context.Background(), model, nil)
	if diagnostics.HasError() {
		t.Fatalf("spaceRoleWriteRequest() diagnostics = %v", diagnostics)
	}
	if created.AnonymousReassignmentRoleID != nil || created.GuestReassignmentRoleID != nil {
		t.Fatal("a create must omit the update-only reassignment ids")
	}

	// An update sends the configured id even when it matches state. A live
	// tenant left the assignment in place for an update whose permission set
	// was unchanged, so re-sending cannot migrate anything on its own.
	unchanged, diagnostics := spaceRoleWriteRequest(context.Background(), model, &model)
	if diagnostics.HasError() {
		t.Fatalf("spaceRoleWriteRequest() diagnostics = %v", diagnostics)
	}
	if unchanged.AnonymousReassignmentRoleID == nil || *unchanged.AnonymousReassignmentRoleID != "role-anonymous" {
		t.Fatalf("a configured reassignment id must be sent, got %#v", unchanged.AnonymousReassignmentRoleID)
	}
	if unchanged.GuestReassignmentRoleID != nil {
		t.Fatal("a null reassignment id must not be sent")
	}

	prior := model
	prior.AnonymousReassignmentRoleID = types.StringNull()
	changed, diagnostics := spaceRoleWriteRequest(context.Background(), model, &prior)
	if diagnostics.HasError() {
		t.Fatalf("spaceRoleWriteRequest() diagnostics = %v", diagnostics)
	}
	if changed.AnonymousReassignmentRoleID == nil || *changed.AnonymousReassignmentRoleID != "role-anonymous" {
		t.Fatalf("a newly configured reassignment id must be sent, got %#v", changed.AnonymousReassignmentRoleID)
	}
}

// TestUpdateSpaceRoleAcceptsSuccessWithoutTaskID pins the observed live
// behaviour: a tenant answered 202 with a null taskId for an update whose
// name, description and permissions were unchanged.
func TestUpdateSpaceRoleAcceptsSuccessWithoutTaskID(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"role-1","type":"CUSTOM","name":"Editors","description":"Can edit","taskId":null}`))
	}))
	t.Cleanup(server.Close)

	taskID, err := NewService(newTestClient(t, server)).UpdateSpaceRole(context.Background(), "role-1", SpaceRoleWriteRequest{
		Name: "Editors", Description: "Can edit", PermissionIDs: []string{"read/space"},
	})
	if err != nil {
		t.Fatalf("UpdateSpaceRole() error = %v, want nil for a 202 without a task id", err)
	}
	if taskID != "" {
		t.Fatalf("UpdateSpaceRole() task id = %q, want empty", taskID)
	}
}

func TestReassignmentRequested(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name      string
		anonymous types.String
		guest     types.String
		want      bool
	}{
		{name: "both null", anonymous: types.StringNull(), guest: types.StringNull()},
		{name: "both blank", anonymous: types.StringValue("  "), guest: types.StringValue("")},
		{name: "anonymous set", anonymous: types.StringValue("role-anonymous"), guest: types.StringNull(), want: true},
		{name: "guest set", anonymous: types.StringNull(), guest: types.StringValue("role-guest"), want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got := reassignmentRequested(spaceRoleResourceModel{
				AnonymousReassignmentRoleID: testCase.anonymous, GuestReassignmentRoleID: testCase.guest,
			})
			if got != testCase.want {
				t.Fatalf("reassignmentRequested() = %t, want %t", got, testCase.want)
			}
		})
	}
}

// TestSpaceRoleSchemaNameValidators runs the validators the schema actually
// carries, so removing one from the attribute fails the test.
func TestSpaceRoleSchemaNameValidators(t *testing.T) {
	t.Parallel()
	response := &resource.SchemaResponse{}
	(&spaceRoleResource{}).Schema(context.Background(), resource.SchemaRequest{}, response)
	attribute, ok := response.Schema.Attributes["name"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("name attribute type = %T, want schema.StringAttribute", response.Schema.Attributes["name"])
	}

	for _, testCase := range []struct {
		name      string
		value     string
		wantError bool
	}{
		{name: "the measured maximum", value: strings.Repeat("a", spaceRoleNameMaxCharacters)},
		// 25 multibyte characters are 75 bytes; a byte-counting validator
		// would reject this name although the API accepted it.
		{name: "the measured maximum in multibyte characters", value: strings.Repeat("テ", spaceRoleNameMaxCharacters)},
		{name: "one character too many", value: strings.Repeat("a", spaceRoleNameMaxCharacters+1), wantError: true},
		{name: "whitespace only", value: "   ", wantError: true},
		{name: "empty", value: "", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var diagnostics diag.Diagnostics
			for _, nameValidator := range attribute.Validators {
				result := &validator.StringResponse{}
				nameValidator.ValidateString(context.Background(), validator.StringRequest{
					Path: path.Root("name"), ConfigValue: types.StringValue(testCase.value),
				}, result)
				diagnostics.Append(result.Diagnostics...)
			}
			if diagnostics.HasError() != testCase.wantError {
				t.Fatalf("validators rejected = %t, want %t (%v)", diagnostics.HasError(), testCase.wantError, diagnostics)
			}
		})
	}
}
