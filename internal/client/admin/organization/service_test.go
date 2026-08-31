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

func TestGetUserUsesGeneratedV2Endpoint(t *testing.T) {
	t.Parallel()

	service := newTestService(t, func(r *http.Request) *http.Response {
		if r.Method != http.MethodGet || r.URL.Path != "/admin/v2/orgs/org/directories/directory/users/712020:account" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer admin-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		return jsonResponse(r, http.StatusOK, `{"data":{"accountId":"712020:account","name":"Example User","email":"user@example.com","status":"active","managementSource":"invited","platformRoles":["atlassian/org-admin"],"deactivatedOn":null}}`)
	})

	user, err := service.GetUser(context.Background(), "org", "directory", "712020:account")
	if err != nil {
		t.Fatal(err)
	}
	if user.AccountID != "712020:account" || user.Email == nil || *user.Email != "user@example.com" {
		t.Fatalf("user = %#v", user)
	}
	if user.PlatformRoles == nil || !reflect.DeepEqual(*user.PlatformRoles, []string{"atlassian/org-admin"}) {
		t.Fatalf("platform roles = %#v", user.PlatformRoles)
	}
	if user.DeactivatedOn != nil {
		t.Fatalf("deactivated on = %#v, want nil", user.DeactivatedOn)
	}
}

func TestGetUserRejectsInvalidSuccessResponse(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"missing data":       `{}`,
		"missing account ID": `{"data":{"name":"Example User"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			service := newTestService(t, func(r *http.Request) *http.Response {
				return jsonResponse(r, http.StatusOK, body)
			})
			if _, err := service.GetUser(context.Background(), "org", "directory", "account"); err == nil {
				t.Fatal("GetUser() error = nil")
			}
		})
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

func TestGetGroupRejectsMissingName(t *testing.T) {
	t.Parallel()

	service := newTestService(t, func(r *http.Request) *http.Response {
		return jsonResponse(r, http.StatusOK, `{"data":{"id":"group","directoryId":"directory"}}`)
	})
	_, err := service.GetGroup(context.Background(), "org", "directory", "group")
	if err == nil || !strings.Contains(err.Error(), "group without name") {
		t.Fatalf("GetGroup() error = %v, want missing name error", err)
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

func TestGroupRoleMutationsUseExactEndpointsAndBodies(t *testing.T) {
	t.Parallel()

	type call struct {
		method string
		path   string
		body   map[string]any
		status int
	}
	var calls []call
	service := newTestService(t, func(r *http.Request) *http.Response {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		// Assign answers 200 and revoke answers 204; asserting the exact code
		// keeps a copied 204 check from silently failing every assignment.
		status := http.StatusOK
		if strings.HasSuffix(r.URL.Path, "/revoke") {
			status = http.StatusNoContent
		}
		calls = append(calls, call{method: r.Method, path: r.URL.Path, body: body, status: status})
		return jsonResponse(r, status, "")
	})

	if err := service.AssignGroupRole(context.Background(), "org", "directory", "group", "ari:cloud:confluence::site/site-id", "atlassian/user"); err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeGroupRole(context.Background(), "org", "directory", "group", "ari:cloud:confluence::site/site-id", "atlassian/user"); err != nil {
		t.Fatal(err)
	}

	want := []call{
		{
			method: http.MethodPost,
			path:   "/admin/v2/orgs/org/directories/directory/groups/group/role-assignments/assign",
			body:   map[string]any{"resourceId": "ari:cloud:confluence::site/site-id", "roleId": "atlassian/user"},
			status: http.StatusOK,
		},
		{
			method: http.MethodPost,
			path:   "/admin/v2/orgs/org/directories/directory/groups/group/role-assignments/revoke",
			body:   map[string]any{"resourceId": "ari:cloud:confluence::site/site-id", "roleId": "atlassian/user"},
			status: http.StatusNoContent,
		},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %#v, want %#v", calls, want)
	}
}

func TestGroupRoleMutationsDoNotRetry(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*Service) error{
		"assign": func(s *Service) error {
			return s.AssignGroupRole(context.Background(), "org", "directory", "group", "resource", "atlassian/user")
		},
		"revoke": func(s *Service) error {
			return s.RevokeGroupRole(context.Background(), "org", "directory", "group", "resource", "atlassian/user")
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			service := newTestService(t, func(r *http.Request) *http.Response {
				calls.Add(1)
				return jsonResponse(r, http.StatusInternalServerError, `{"message":"temporary failure"}`)
			})
			if err := mutate(service); err == nil {
				t.Fatal("mutation error = nil")
			}
			if calls.Load() != 1 {
				t.Fatalf("calls = %d, want 1", calls.Load())
			}
		})
	}
}

func TestHasGroupRoleFollowsAllPages(t *testing.T) {
	t.Parallel()

	var cursors []string
	service := newTestService(t, func(r *http.Request) *http.Response {
		query := r.URL.Query()
		cursors = append(cursors, query.Get("cursor"))
		if query.Get("cursor") == "" {
			// The first request narrows the search server side. A later cursor
			// request must carry the cursor alone, because the endpoint
			// discards every other parameter once a cursor is present.
			if got := query.Get("resourceIds"); got != "resource" {
				t.Errorf("resourceIds = %q, want %q", got, "resource")
			}
			if got := query.Get("roleIds"); got != "atlassian/user" {
				t.Errorf("roleIds = %q, want %q", got, "atlassian/user")
			}
			if got := query.Get("limit"); got == "" {
				t.Error("limit is not set on the first request")
			}
			return jsonResponse(r, http.StatusOK, `{"data":[{"resourceId":"other","roles":["atlassian/user"]}],"links":{"next":"page-2"}}`)
		}
		if got := query.Get("resourceIds"); got != "" {
			t.Errorf("resourceIds = %q on a cursor request, want none", got)
		}
		return jsonResponse(r, http.StatusOK, `{"data":[{"resourceId":"resource","roles":["atlassian/admin","atlassian/user"]}],"links":{}}`)
	})

	present, err := service.HasGroupRole(context.Background(), "org", "directory", "group", "resource", "atlassian/user")
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Fatal("HasGroupRole() = false, want true")
	}
	if !reflect.DeepEqual(cursors, []string{"", "page-2"}) {
		t.Errorf("cursors = %#v", cursors)
	}
}

func TestHasGroupRoleIgnoresDefaultRoleAndOtherResources(t *testing.T) {
	t.Parallel()

	// defaultRole reports what the resource grants by default and is
	// independent of what the group holds, so it must not count as an
	// assignment. A matching role on a different resource must not count
	// either.
	cases := map[string]string{
		"default role only":  `{"data":[{"resourceId":"resource","defaultRole":"atlassian/user","roles":[]}],"links":{}}`,
		"different resource": `{"data":[{"resourceId":"other","roles":["atlassian/user"]}],"links":{}}`,
		"different role":     `{"data":[{"resourceId":"resource","roles":["atlassian/admin"]}],"links":{}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			service := newTestService(t, func(r *http.Request) *http.Response {
				return jsonResponse(r, http.StatusOK, body)
			})
			present, err := service.HasGroupRole(context.Background(), "org", "directory", "group", "resource", "atlassian/user")
			if err != nil {
				t.Fatal(err)
			}
			if present {
				t.Fatal("HasGroupRole() = true, want false")
			}
		})
	}
}

func TestHasGroupRoleRejectsRepeatedCursor(t *testing.T) {
	t.Parallel()

	service := newTestService(t, func(r *http.Request) *http.Response {
		return jsonResponse(r, http.StatusOK, `{"data":[],"links":{"next":"same"}}`)
	})
	_, err := service.HasGroupRole(context.Background(), "org", "directory", "group", "resource", "atlassian/user")
	if err == nil || !strings.Contains(err.Error(), "repeated pagination cursor") {
		t.Fatalf("HasGroupRole() error = %v, want repeated cursor error", err)
	}
}

func TestQueryWorkspacesFollowsPagesWithCursorOnlyBodies(t *testing.T) {
	t.Parallel()

	// The response advertises links.next as a URL but returns a cursor, and the
	// API rejects a cursor sent alongside any other property, so follow-up
	// requests must carry the cursor and nothing else.
	var bodies []map[string]any
	service := newTestService(t, func(r *http.Request) *http.Response {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
		if _, paged := body["cursor"]; !paged {
			return jsonResponse(r, http.StatusOK, `{"data":[{"id":"ari:cloud:confluence::site/site-id","type":"Confluence","attributes":{"name":"folio-sec","typeKey":"confluence","status":"online"}}],"links":{"next":"page-2"}}`)
		}
		return jsonResponse(r, http.StatusOK, `{"data":[{"id":"ari:cloud:jira-software::site/site-id","attributes":{"typeKey":"jira-software"}}],"links":{"next":null}}`)
	})

	workspaces, err := service.QueryWorkspaces(context.Background(), "org", QueryWorkspacesRequest{Search: "folio-sec"})
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != 2 {
		t.Fatalf("workspaces length = %d, want 2", len(workspaces))
	}
	if workspaces[0].ID != "ari:cloud:confluence::site/site-id" {
		t.Errorf("workspaces[0].ID = %q", workspaces[0].ID)
	}
	if workspaces[0].TypeKey == nil || *workspaces[0].TypeKey != "confluence" {
		t.Errorf("workspaces[0].TypeKey = %v", workspaces[0].TypeKey)
	}
	if workspaces[0].Status == nil || *workspaces[0].Status != "online" {
		t.Errorf("workspaces[0].Status = %v", workspaces[0].Status)
	}

	if len(bodies) != 2 {
		t.Fatalf("request count = %d, want 2", len(bodies))
	}
	if _, searched := bodies[0]["query"]; !searched {
		t.Errorf("first request body = %#v, want a query", bodies[0])
	}
	if want := map[string]any{"cursor": "page-2"}; !reflect.DeepEqual(bodies[1], want) {
		t.Errorf("second request body = %#v, want %#v", bodies[1], want)
	}
}

func TestQueryWorkspacesRejectsWorkspaceWithoutID(t *testing.T) {
	t.Parallel()

	service := newTestService(t, func(r *http.Request) *http.Response {
		return jsonResponse(r, http.StatusOK, `{"data":[{"attributes":{"typeKey":"confluence"}}],"links":{}}`)
	})
	_, err := service.QueryWorkspaces(context.Background(), "org", QueryWorkspacesRequest{})
	if err == nil || !strings.Contains(err.Error(), "without id") {
		t.Fatalf("QueryWorkspaces() error = %v, want missing id error", err)
	}
}

func TestGroupRoleMutationsAcceptAnySuccessStatus(t *testing.T) {
	t.Parallel()

	// The documented codes are 200 for assign and 204 for revoke, but the same
	// specification misdescribes this endpoint family's pagination links, and
	// rejecting an undocumented 2xx would leave a granted role untracked.
	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			service := newTestService(t, func(r *http.Request) *http.Response {
				return jsonResponse(r, status, "")
			})
			if err := service.AssignGroupRole(context.Background(), "org", "directory", "group", "resource", "atlassian/user"); err != nil {
				t.Errorf("AssignGroupRole() error = %v", err)
			}
			if err := service.RevokeGroupRole(context.Background(), "org", "directory", "group", "resource", "atlassian/user"); err != nil {
				t.Errorf("RevokeGroupRole() error = %v", err)
			}
		})
	}
}

func TestGroupRoleMutationsRejectErrorStatuses(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			service := newTestService(t, func(r *http.Request) *http.Response {
				return jsonResponse(r, status, `{"message":"nope"}`)
			})
			err := service.AssignGroupRole(context.Background(), "org", "directory", "group", "resource", "atlassian/user")
			var httpErr *admin.HTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode != status {
				t.Fatalf("AssignGroupRole() error = %v, want HTTPError with status %d", err, status)
			}
		})
	}
}

func TestQueryWorkspacesBuildsQueryOperands(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		filters QueryWorkspacesRequest
		want    any
	}{
		"no operands sends no query": {
			filters: QueryWorkspacesRequest{},
			want:    nil,
		},
		// A lone operand is a valid query on its own, so it is not wrapped in a
		// one-element conjunction.
		"single operand is sent bare": {
			filters: QueryWorkspacesRequest{Search: "folio-sec"},
			want:    map[string]any{"searchWorkspaces": "folio-sec"},
		},
		"field operand": {
			filters: QueryWorkspacesRequest{Fields: []WorkspaceField{{Name: "attributes.type", Values: []string{"confluence"}}}},
			want:    map[string]any{"field": map[string]any{"name": "attributes.type", "values": []any{"confluence"}}},
		},
		"feature filter": {
			filters: QueryWorkspacesRequest{Features: []string{"feature-a"}},
			want:    map[string]any{"features": []any{"feature-a"}},
		},
		"several operands are combined with and": {
			filters: QueryWorkspacesRequest{
				Search:   "folio-sec",
				Fields:   []WorkspaceField{{Name: "attributes.type", Values: []string{"confluence", "jira-software"}}},
				Features: []string{"feature-a"},
			},
			want: map[string]any{"and": []any{
				map[string]any{"searchWorkspaces": "folio-sec"},
				map[string]any{"field": map[string]any{"name": "attributes.type", "values": []any{"confluence", "jira-software"}}},
				map[string]any{"features": []any{"feature-a"}},
			}},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var body map[string]any
			service := newTestService(t, func(r *http.Request) *http.Response {
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				return jsonResponse(r, http.StatusOK, `{"data":[],"links":{}}`)
			})
			if _, err := service.QueryWorkspaces(context.Background(), "org", test.filters); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(body["query"], test.want) {
				t.Errorf("query = %#v, want %#v", body["query"], test.want)
			}
		})
	}
}
