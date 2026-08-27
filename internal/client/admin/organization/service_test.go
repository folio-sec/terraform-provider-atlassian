package organization

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
)

func TestSearchUsersFollowsAllPages(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	handler := func(r *http.Request) *http.Response {
		if r.URL.Path != "/admin/v2/orgs/org/directories/directory/users/search" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer admin-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		call := calls.Add(1)
		if call == 1 {
			if request["limit"] != float64(100) || request["cursor"] != nil {
				t.Errorf("first request = %#v", request)
			}
			return jsonResponse(r, http.StatusOK, `{"data":[{"accountId":"12345678-1234-1234-1234-123456789012","email":"one@example.com","managementSource":null}],"links":{"next":"next-page"}}`)
		}
		if request["cursor"] != "next-page" || request["limit"] != float64(100) {
			t.Errorf("second request = %#v", request)
		}
		return jsonResponse(r, http.StatusOK, `{"data":[{"accountId":"22345678-1234-1234-1234-123456789012","email":"two@example.com"}],"links":{}}`)
	}

	service := newTestService(t, handler)
	users, err := service.SearchUsers(context.Background(), "org", "directory", SearchUsersRequest{Emails: []string{"one@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{users[0].AccountID, users[1].AccountID}; !reflect.DeepEqual(got, []string{"12345678-1234-1234-1234-123456789012", "22345678-1234-1234-1234-123456789012"}) {
		t.Fatalf("accounts = %#v", got)
	}
	if users[0].ManagementSource != nil {
		t.Fatalf("management source = %#v", users[0].ManagementSource)
	}
	if users[0].Email == nil || *users[0].Email != "one@example.com" {
		t.Fatalf("email = %#v", users[0].Email)
	}
	if users[1].Email == nil || *users[1].Email != "two@example.com" {
		t.Fatalf("email = %#v", users[1].Email)
	}
	if users[1].MFAEnabled != nil {
		t.Fatalf("MFA enabled = %#v, want nil for an omitted API field", users[1].MFAEnabled)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestSearchGroupsFollowsAllPages(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	handler := func(r *http.Request) *http.Response {
		if r.URL.Path != "/admin/v2/orgs/org/directories/directory/groups/search" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		call := calls.Add(1)
		if call == 1 {
			if request["limit"] != float64(100) || request["cursor"] != nil {
				t.Errorf("first request = %#v", request)
			}
			if got := request["groupNames"]; !reflect.DeepEqual(got, []any{"jira-users"}) {
				t.Errorf("groupNames = %#v", got)
			}
			return jsonResponse(r, http.StatusOK, `{"data":[{"id":"group-1","name":"jira-users","managementAccess":{"deletable":false,"modifiable":true,"readable":true}}],"links":{"next":"next-page"}}`)
		}
		if request["cursor"] != "next-page" || request["limit"] != float64(100) {
			t.Errorf("second request = %#v", request)
		}
		return jsonResponse(r, http.StatusOK, `{"data":[{"id":"group-2","name":"confluence-users"}],"links":{}}`)
	}

	service := newTestService(t, handler)
	groups, err := service.SearchGroups(context.Background(), "org", "directory", SearchGroupsRequest{GroupNames: []string{"jira-users"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{groups[0].ID, groups[1].ID}; !reflect.DeepEqual(got, []string{"group-1", "group-2"}) {
		t.Fatalf("groups = %#v", got)
	}
	if groups[0].ManagementAccess == nil || groups[0].ManagementAccess.Modifiable == nil || !*groups[0].ManagementAccess.Modifiable {
		t.Fatalf("management access = %#v", groups[0].ManagementAccess)
	}
	if groups[1].ManagementAccess != nil {
		t.Fatalf("management access = %#v, want nil", groups[1].ManagementAccess)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestHasGroupMembershipFiltersByAccountAndGroup(t *testing.T) {
	t.Parallel()

	handler := func(r *http.Request) *http.Response {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if got := request["accountIds"]; !reflect.DeepEqual(got, []any{"712020:account"}) {
			t.Errorf("accountIds = %#v", got)
		}
		if got := request["groupIds"]; !reflect.DeepEqual(got, []any{"group"}) {
			t.Errorf("groupIds = %#v", got)
		}
		return jsonResponse(r, http.StatusOK, `{"data":[{"accountId":"712020:account"}],"links":{}}`)
	}

	service := newTestService(t, handler)
	present, err := service.HasGroupMembership(context.Background(), "org", "directory", "group", "712020:account")
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Fatal("HasGroupMembership() = false, want true")
	}
}

func TestGroupMembershipMutationsUseV2EndpointsWithoutRetry(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	handler := func(r *http.Request) *http.Response {
		calls.Add(1)
		switch r.Method {
		case http.MethodPost:
			if r.URL.Path != "/admin/v2/orgs/org/directories/directory/groups/group/memberships" {
				t.Errorf("add path = %q", r.URL.Path)
			}
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request["accountId"] != "712020:account" {
				t.Errorf("add request = %#v", request)
			}
		case http.MethodDelete:
			if r.URL.Path != "/admin/v2/orgs/org/directories/directory/groups/group/memberships/712020:account" {
				t.Errorf("remove path = %q", r.URL.Path)
			}
		default:
			t.Errorf("method = %q", r.Method)
		}
		return jsonResponse(r, http.StatusNoContent, "")
	}

	service := newTestService(t, handler)
	if err := service.AddGroupMembership(context.Background(), "org", "directory", "group", "712020:account"); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveGroupMembership(context.Background(), "org", "directory", "group", "712020:account"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestAddGroupMembershipDoesNotRetryMutation(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	handler := func(r *http.Request) *http.Response {
		calls.Add(1)
		return jsonResponse(r, http.StatusInternalServerError, `{"message":"temporary failure"}`)
	}

	service := newTestService(t, handler)
	err := service.AddGroupMembership(context.Background(), "org", "directory", "group", "account")
	if err == nil {
		t.Fatal("AddGroupMembership() error = nil")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestHasDirectUserRoleIgnoresInheritedRole(t *testing.T) {
	t.Parallel()

	handler := func(r *http.Request) *http.Response {
		if got := r.URL.Query().Get("resourceIds"); got != "resource" {
			t.Errorf("resourceIds = %q", got)
		}
		return jsonResponse(r, http.StatusOK, `{"data":[{"resourceId":"resource","roleAssignments":[{"role":"atlassian/user","roleAssignmentMethods":["group_direct"]},{"role":"atlassian/admin","roleAssignmentMethods":["direct"]}]}],"links":{}}`)
	}

	service := newTestService(t, handler)
	present, err := service.HasDirectUserRole(context.Background(), "org", "directory", "account", "resource", "atlassian/user")
	if err != nil {
		t.Fatal(err)
	}
	if present {
		t.Fatal("HasDirectUserRole() = true for inherited role")
	}
}

func TestAssignUserRoleDoesNotRetryMutation(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	handler := func(r *http.Request) *http.Response {
		calls.Add(1)
		return jsonResponse(r, http.StatusInternalServerError, `{"message":"temporary failure"}`)
	}

	service := newTestService(t, handler)
	err := service.AssignUserRole(context.Background(), "org", "12345678-1234-1234-1234-123456789012", "resource", "atlassian/user")
	if err == nil {
		t.Fatal("AssignUserRole() error = nil")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

type roundTripFunc func(*http.Request) *http.Response

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request), nil
}

func newTestService(t *testing.T, handler func(*http.Request) *http.Response) *Service {
	t.Helper()
	baseURL, err := url.Parse("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Transport: roundTripFunc(handler)}
	transport, err := admin.NewWithBaseURL(baseURL, "admin-key", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(transport)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func jsonResponse(request *http.Request, statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}
