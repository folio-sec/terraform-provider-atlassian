package control

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/control/generated"
)

type roundTripFunc func(*http.Request) *http.Response

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request), nil
}

func testService(t *testing.T, handler func(*http.Request) *http.Response) *Service {
	t.Helper()
	baseURL, err := url.Parse("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	transport, err := admin.NewWithBaseURL(baseURL, "key", &http.Client{Transport: roundTripFunc(handler)})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(transport)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func response(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

const policyJSON = `{"data":{"id":"draft-id","type":"policy","attributes":{"type":"data-security","name":"draft","status":"draft","rule":{"export":{"effect":"allow"}},"metadata":{"policyCoverageLevel":"ORG","description":"temporary"}}}}`

func TestDataSecurityPolicyOperationsUseControlPaths(t *testing.T) {
	t.Parallel()

	requests := []struct {
		method string
		path   string
		status int
		body   string
	}{
		{http.MethodPost, "/admin/control/v2/orgs/org/policies", http.StatusAccepted, policyJSON},
		{http.MethodGet, "/admin/control/v2/orgs/org/policies/draft-id", http.StatusOK, policyJSON},
		{http.MethodPut, "/admin/control/v2/orgs/org/policies/draft-id", http.StatusAccepted, policyJSON},
		{http.MethodDelete, "/admin/control/v1/orgs/org/policies/draft-id", http.StatusAccepted, ""},
	}
	index := 0
	service := testService(t, func(request *http.Request) *http.Response {
		want := requests[index]
		index++
		if request.Method != want.method || request.URL.Path != want.path {
			t.Fatalf("request = %s %s, want %s %s", request.Method, request.URL.Path, want.method, want.path)
		}
		if request.Header.Get("Authorization") != "Bearer key" {
			t.Fatal("missing authorization header")
		}
		return response(request, want.status, want.body)
	})
	policy, err := service.CreateDataSecurityPolicy(context.Background(), "org", generated.CreateDataSecurityPolicyJSONRequestBody{}) //nolint:staticcheck // No public replacement exists.
	if err != nil || policy.Id != "draft-id" {
		t.Fatalf("create policy = %#v, error = %v", policy, err)
	}
	if _, err := service.GetDataSecurityPolicy(context.Background(), "org", "draft-id"); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateDataSecurityPolicy(context.Background(), "org", "draft-id", generated.UpdateDataSecurityPolicyJSONRequestBody{}); err != nil { //nolint:staticcheck // No public replacement exists.
		t.Fatal(err)
	}
	if err := service.DeleteDataSecurityPolicy(context.Background(), "org", "draft-id"); err != nil {
		t.Fatal(err)
	}
	if index != len(requests) {
		t.Fatalf("requests = %d, want %d", index, len(requests))
	}
}

func TestDataSecurityPolicyPagination(t *testing.T) {
	t.Parallel()

	calls := 0
	service := testService(t, func(request *http.Request) *http.Response {
		calls++
		if request.URL.Query().Get("type") != "data-security" {
			t.Fatal("missing fixed data-security filter")
		}
		if calls == 1 {
			return response(request, http.StatusOK, `{"data":[{"id":"one","type":"policy","attributes":{"type":"data-security","name":"one","status":"draft","rule":{"export":{"effect":"allow"}},"metadata":{"policyCoverageLevel":"ORG"}}}],"links":{"next":"https://api.atlassian.com/admin/control/v2/orgs/org/policies?cursor=opaque"}}`)
		}
		if request.URL.Query().Get("cursor") != "opaque" {
			t.Fatal("missing cursor")
		}
		return response(request, http.StatusOK, `{"data":[{"id":"two","type":"policy","attributes":{"type":"data-security","name":"two","status":"deleted","rule":{"export":{"effect":"allow"}},"metadata":{"policyCoverageLevel":"ORG"}}}],"meta":{"next":null}}`)
	})
	policies, err := service.GetDataSecurityPolicies(context.Background(), "org")
	if err != nil || len(policies) != 2 || calls != 2 {
		t.Fatalf("policies = %#v, calls = %d, error = %v", policies, calls, err)
	}
}

func TestDataSecurityMutationsAreNotReplayed(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			calls := 0
			service := testService(t, func(request *http.Request) *http.Response {
				calls++
				return response(request, http.StatusServiceUnavailable, `{}`)
			})
			var err error
			switch method {
			case http.MethodPost:
				_, err = service.CreateDataSecurityPolicy(context.Background(), "org", generated.CreateDataSecurityPolicyJSONRequestBody{}) //nolint:staticcheck // No public replacement exists.
			case http.MethodPut:
				err = service.UpdateDataSecurityPolicy(context.Background(), "org", "id", generated.UpdateDataSecurityPolicyJSONRequestBody{}) //nolint:staticcheck // No public replacement exists.
			case http.MethodDelete:
				err = service.DeleteDataSecurityPolicy(context.Background(), "org", "id")
			}
			if err == nil || calls != 1 {
				t.Fatalf("calls = %d, error = %v", calls, err)
			}
		})
	}
}

func TestDataSecurityPaginationRejectsForeignLink(t *testing.T) {
	t.Parallel()

	service := testService(t, func(request *http.Request) *http.Response {
		return response(request, http.StatusOK, `{"data":[],"links":{"next":"https://example.invalid/admin/control/v2/orgs/org/policies?cursor=x"}}`)
	})
	if _, err := service.GetDataSecurityPolicies(context.Background(), "org"); err == nil {
		t.Fatal("accepted foreign pagination link")
	}
}
