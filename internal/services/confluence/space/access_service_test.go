package space

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestGetSpacePermissionsAssignmentsFollowsEveryPage(t *testing.T) {
	t.Parallel()
	var cursors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/spaces/1/permissions" {
			http.NotFound(w, r)
			return
		}
		cursors = append(cursors, r.URL.Query().Get("cursor"))
		w.Header().Set("Content-Type", "application/json")
		if len(cursors) == 1 {
			_, _ = w.Write([]byte(`{"results":[{"id":"10","principal":{"type":"group","id":"g"},"operation":{"key":"read","targetType":"space"}}],"_links":{"next":"/wiki/api/v2/spaces/1/permissions?cursor=next"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"id":"11","principal":{"type":"access-class","id":"all-licensed-users"},"operation":{"key":"manage_users","targetType":"space"}}]}`))
	}))
	defer server.Close()

	got, err := NewService(newTestClient(t, server)).GetSpacePermissionsAssignments(context.Background(), "1")
	if err != nil {
		t.Fatalf("GetSpacePermissionsAssignments() error = %v", err)
	}
	if len(got) != 2 || got[0].ID != "10" || got[1].Principal.Type != "access-class" || got[1].Operation.Key != "manage_users" {
		t.Fatalf("assignments = %#v", got)
	}
	if !reflect.DeepEqual(cursors, []string{"", "next"}) {
		t.Fatalf("cursors = %v, want [\"\" next]", cursors)
	}
}

func TestSetSpaceRoleAssignmentSendsOneEntryForUpsertAndDelete(t *testing.T) {
	t.Parallel()
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/spaces/1/role-assignments" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(body) != 1 {
			t.Fatalf("body = %#v, want one entry", body)
		}
		bodies = append(bodies, body[0])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"principal":{"principalType":"USER","principalId":"u"}}]}`))
	}))
	defer server.Close()
	service := NewService(newTestClient(t, server))
	role := "role-1"
	if err := service.SetSpaceRoleAssignment(context.Background(), "1", Principal{Type: "USER", ID: "u"}, &role); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := service.SetSpaceRoleAssignment(context.Background(), "1", Principal{Type: "USER", ID: "u"}, nil); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := bodies[0]["roleId"]; got != "role-1" {
		t.Fatalf("upsert roleId = %#v", got)
	}
	if _, exists := bodies[1]["roleId"]; exists {
		t.Fatalf("delete body included roleId: %#v", bodies[1])
	}
}
