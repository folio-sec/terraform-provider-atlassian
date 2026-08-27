package organization

import (
	"context"
	"encoding/json"
	"errors"
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

func TestGroupLifecycleUsesGeneratedV2Endpoints(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	service := newTestService(t, func(r *http.Request) *http.Response {
		if r.Header.Get("Authorization") != "Bearer admin-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch calls.Add(1) {
		case 1:
			if r.Method != http.MethodPost || r.URL.Path != "/admin/v2/orgs/org/directories/directory/groups" {
				t.Errorf("create request = %s %s", r.Method, r.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(body, map[string]any{"name": "engineering", "description": "Managed by Terraform"}) {
				t.Errorf("create body = %#v", body)
			}
			return jsonResponse(r, http.StatusCreated, "")
		case 2:
			if r.Method != http.MethodGet || r.URL.Path != "/admin/v2/orgs/org/directories/directory/groups/group" {
				t.Errorf("get request = %s %s", r.Method, r.URL.Path)
			}
			return jsonResponse(r, http.StatusOK, `{"data":{"id":"group","name":"engineering","description":"Managed by Terraform","directoryId":"directory","externalSynced":false,"managedBy":"admins","managementAccess":{"deletable":true,"modifiable":false,"readable":true}}}`)
		case 3:
			if r.Method != http.MethodDelete || r.URL.Path != "/admin/v2/orgs/org/directories/directory/groups/group" {
				t.Errorf("delete request = %s %s", r.Method, r.URL.Path)
			}
			return jsonResponse(r, http.StatusNoContent, "")
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil
		}
	})
	description := "Managed by Terraform"
	if err := service.CreateGroup(context.Background(), "org", "directory", "engineering", &description); err != nil {
		t.Fatal(err)
	}
	group, err := service.GetGroup(context.Background(), "org", "directory", "group")
	if err != nil {
		t.Fatal(err)
	}
	if group.ID != "group" || group.ManagementAccess == nil || group.ManagementAccess.Deletable == nil || !*group.ManagementAccess.Deletable {
		t.Fatalf("group = %#v", group)
	}
	if err := service.DeleteGroup(context.Background(), "org", "directory", "group"); err != nil {
		t.Fatal(err)
	}
}

func TestGroupMutationsDoNotRetry(t *testing.T) {
	t.Parallel()

	for _, operation := range []string{"create", "delete"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			service := newTestService(t, func(r *http.Request) *http.Response {
				calls.Add(1)
				return jsonResponse(r, http.StatusInternalServerError, `{"message":"temporary failure"}`)
			})
			var err error
			if operation == "create" {
				err = service.CreateGroup(context.Background(), "org", "directory", "engineering", nil)
			} else {
				err = service.DeleteGroup(context.Background(), "org", "directory", "group")
			}
			if err == nil {
				t.Fatal("mutation error = nil")
			}
			if calls.Load() != 1 {
				t.Fatalf("calls = %d, want 1", calls.Load())
			}
		})
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

func TestUserRoleMutationsAcceptOpaqueAccountIDs(t *testing.T) {
	t.Parallel()

	// Atlassian issues legacy and 712020-prefixed account IDs, neither of which
	// is a UUID.
	accountIDs := map[string]string{
		"legacy":  "5b361abdf2886739ae9da236",
		"712020":  "712020:12345678-1234-1234-1234-123456789012",
		"bare id": "12345678-1234-1234-1234-123456789012",
	}
	for name, accountID := range accountIDs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var paths []string
			service := newTestService(t, func(r *http.Request) *http.Response {
				paths = append(paths, r.URL.Path)
				return jsonResponse(r, http.StatusNoContent, "")
			})
			if err := service.AssignUserRole(context.Background(), "org", accountID, "ari:cloud:jira::site/site-id", "atlassian/user"); err != nil {
				t.Fatal(err)
			}
			if err := service.RevokeUserRole(context.Background(), "org", accountID, "ari:cloud:jira::site/site-id", "atlassian/user"); err != nil {
				t.Fatal(err)
			}
			want := []string{
				"/admin/v1/orgs/org/users/" + accountID + "/roles/assign",
				"/admin/v1/orgs/org/users/" + accountID + "/roles/revoke",
			}
			if !reflect.DeepEqual(paths, want) {
				t.Errorf("paths = %#v, want %#v", paths, want)
			}
		})
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

func TestOrganizationRoleMutationsUseExactEndpointsAndBodies(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	handler := func(r *http.Request) *http.Response {
		call := calls.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(request, map[string]any{"role": "atlassian/org-admin"}) {
			t.Errorf("request body = %#v", request)
		}
		switch call {
		case 1:
			if r.URL.Path != "/admin/v1/orgs/org/users/5b361abdf2886739ae9da236/role-assignments/assign" {
				t.Errorf("assign path = %q", r.URL.Path)
			}
		case 2:
			if r.URL.Path != "/admin/v1/orgs/org/users/712020:12345678-1234-1234-1234-123456789012/role-assignments/revoke" {
				t.Errorf("revoke path = %q", r.URL.Path)
			}
		default:
			t.Errorf("unexpected call %d", call)
		}
		return jsonResponse(r, http.StatusNoContent, "")
	}

	service := newTestService(t, handler)
	if err := service.AssignOrganizationRole(context.Background(), "org", "5b361abdf2886739ae9da236", "atlassian/org-admin"); err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeOrganizationRole(context.Background(), "org", "712020:12345678-1234-1234-1234-123456789012", "atlassian/org-admin"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestOrganizationRoleMutationsReturnAPIErrorsWithoutRetry(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status int
		call   func(*Service) error
	}{
		"assign": {
			status: http.StatusConflict,
			call: func(service *Service) error {
				return service.AssignOrganizationRole(context.Background(), "org", "account", "atlassian/org-admin")
			},
		},
		"revoke": {
			status: http.StatusBadRequest,
			call: func(service *Service) error {
				return service.RevokeOrganizationRole(context.Background(), "org", "account", "atlassian/org-admin")
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			service := newTestService(t, func(r *http.Request) *http.Response {
				calls.Add(1)
				return jsonResponse(r, test.status, `{"message":"mutation failed"}`)
			})
			err := test.call(service)
			if err == nil {
				t.Fatal("mutation error = nil")
			}
			var httpErr *admin.HTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode != test.status {
				t.Fatalf("error = %v, want HTTP status %d", err, test.status)
			}
			if calls.Load() != 1 {
				t.Fatalf("calls = %d, want 1", calls.Load())
			}
		})
	}
}

func TestHasDirectOrganizationRoleFollowsPagesAndIgnoresInheritedAssignments(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	handler := func(r *http.Request) *http.Response {
		call := calls.Add(1)
		if r.URL.Path != "/admin/v2/orgs/org/directories/directory/users/712020:account/role-assignments" {
			t.Errorf("path = %q", r.URL.Path)
		}
		switch call {
		case 1:
			if got := r.URL.Query()["roleIds"]; !reflect.DeepEqual(got, []string{"atlassian/org-admin"}) {
				t.Errorf("roleIds = %#v", got)
			}
			if got := r.URL.Query().Get("resourceIds"); got != "" {
				t.Errorf("resourceIds = %q, want empty", got)
			}
			return jsonResponse(r, http.StatusOK, `{"data":[{"resourceId":"ignored","roleAssignments":[{"role":"atlassian/org-admin","roleAssignmentMethods":["group_direct","inferred"]}]}],"links":{"next":"next-page"}}`)
		case 2:
			if got := r.URL.Query().Get("cursor"); got != "next-page" {
				t.Errorf("cursor = %q", got)
			}
			if got := r.URL.Query().Get("roleIds"); got != "" {
				t.Errorf("second-page roleIds = %q, want empty", got)
			}
			return jsonResponse(r, http.StatusOK, `{"data":[{"roleAssignments":[{"role":"atlassian/org-admin","roleAssignmentMethods":["direct"]}]}],"links":{}}`)
		default:
			t.Errorf("unexpected call %d", call)
			return jsonResponse(r, http.StatusInternalServerError, "")
		}
	}

	service := newTestService(t, handler)
	present, err := service.HasDirectOrganizationRole(context.Background(), "org", "directory", "712020:account", "atlassian/org-admin")
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Fatal("HasDirectOrganizationRole() = false, want true")
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestHasDirectOrganizationRoleIgnoresGroupAndInferredAssignments(t *testing.T) {
	t.Parallel()

	service := newTestService(t, func(r *http.Request) *http.Response {
		return jsonResponse(r, http.StatusOK, `{"data":[{"roleAssignments":[{"role":"atlassian/org-admin","roleAssignmentMethods":["group_direct"]},{"role":"atlassian/org-admin","roleAssignmentMethods":["inferred"]}]}],"links":{}}`)
	})
	present, err := service.HasDirectOrganizationRole(context.Background(), "org", "directory", "account", "atlassian/org-admin")
	if err != nil {
		t.Fatal(err)
	}
	if present {
		t.Fatal("HasDirectOrganizationRole() = true for inherited assignments")
	}
}

func TestHasDirectOrganizationRoleRejectsRepeatedCursor(t *testing.T) {
	t.Parallel()

	service := newTestService(t, func(r *http.Request) *http.Response {
		return jsonResponse(r, http.StatusOK, `{"data":[],"links":{"next":"repeated"}}`)
	})
	_, err := service.HasDirectOrganizationRole(context.Background(), "org", "directory", "account", "atlassian/org-admin")
	if err == nil || !strings.Contains(err.Error(), `repeated pagination cursor "repeated"`) {
		t.Fatalf("error = %v, want repeated cursor error", err)
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
