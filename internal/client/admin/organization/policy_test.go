package organization

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin"
	"github.com/folio-sec/terraform-provider-atlassian/internal/client/admin/organization/generated"
)

func TestPolicyPagination(t *testing.T) {
	for _, next := range []string{`"meta":{"next":"opaque"}`, `"links":{"next":"https://api.atlassian.com/admin/v1/orgs/org/policies?cursor=opaque"}`} {
		t.Run(next, func(t *testing.T) {
			calls := 0
			service := newTestService(t, func(r *http.Request) *http.Response {
				calls++
				if r.URL.Path != "/admin/v1/orgs/org/policies" || r.URL.Query().Get("type") != "hipaa" {
					t.Errorf("unexpected URL: %s", r.URL)
				}
				if calls == 1 {
					return jsonResponse(r, 200, `{"data":[{"id":"one","type":"policy","attributes":{"type":"hipaa","rule":[]}}],`+next+`}`)
				}
				if calls > 2 || r.URL.Query().Get("cursor") != "opaque" {
					t.Fatalf("unexpected page: %s", r.URL)
				}
				return jsonResponse(r, 200, `{"data":[{"id":"two","type":"policy","attributes":{"type":"hipaa","rule":{"large":9007199254740993}}}],"meta":{"next":null}}`)
			})
			filter := "hipaa"
			result, err := service.GetPolicies(context.Background(), "org", &filter)
			if err != nil || len(result) != 2 || calls != 2 {
				t.Fatalf("result=%v calls=%d error=%v", result, calls, err)
			}
			if string(*result[1].Attributes.Rule) != `{"large":9007199254740993}` {
				t.Fatal("rule lost precision")
			}
		})
	}
}

func TestPolicyInvalidPages(t *testing.T) {
	for name, body := range map[string]string{
		"missing data": `{}`, "invalid data": `{"data":[{"id":""}]}`,
		"foreign host":     `{"data":[],"links":{"next":"https://evil.test/v1/orgs/org/policies?cursor=x"}}`,
		"foreign org":      `{"data":[],"links":{"next":"/v1/orgs/other/policies?cursor=x"}}`,
		"missing cursor":   `{"data":[],"links":{"next":"/v1/orgs/org/policies"}}`,
		"duplicate cursor": `{"data":[],"meta":{"next":"same"}}`,
		"duplicate ID":     `{"data":[{"id":"same","type":"policy","attributes":{"type":"hipaa"}}],"meta":{"next":"same"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			s := newTestService(t, func(r *http.Request) *http.Response {
				calls++
				if calls > 2 {
					t.Fatal("pagination failed to stop")
				}
				return jsonResponse(r, 200, body)
			})
			if _, err := s.GetPolicies(context.Background(), "org", nil); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	s := newTestService(t, func(r *http.Request) *http.Response { return jsonResponse(r, 200, `{"data":[]}`) })
	if result, err := s.GetPolicies(context.Background(), "org", nil); err != nil || result == nil || len(result) != 0 {
		t.Fatalf("empty page: %v, %v", result, err)
	}
}

func TestPolicyHTTPAndResponseValidation(t *testing.T) {
	for _, body := range []string{"", "not JSON", `{"errors":[{"code":"ADMIN-404-5"}]}`} {
		s := newTestService(t, func(r *http.Request) *http.Response { return jsonResponse(r, 404, body) })
		_, err := s.GetPolicyById(context.Background(), "org", "id")
		var httpErr *admin.HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != 404 {
			t.Fatalf("404 lost: %v", err)
		}
	}
	for _, body := range []string{`{}`, `{"data":{"id":"other","type":"policy","attributes":{"type":"data-residency"}}}`} {
		s := newTestService(t, func(r *http.Request) *http.Response { return jsonResponse(r, 200, body) })
		if _, err := s.GetPolicyById(context.Background(), "org", "id"); err == nil {
			t.Fatal("expected malformed response error")
		}
	}
	// Creation must preserve an allocated ID even when the response lacks its type.
	policy, err := decodePolicy([]byte(`{"data":{"id":"allocated"}}`), "")
	if err == nil || policy.Id != "allocated" {
		t.Fatalf("partial identity lost: %v %v", policy, err)
	}
}

func TestPolicyMutationsAreNotReplayed(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			calls := 0
			s := newTestService(t, func(r *http.Request) *http.Response {
				calls++
				if r.Method != method || !strings.HasPrefix(r.URL.Path, "/admin/v1/orgs/org/policies") {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				return jsonResponse(r, 503, `{}`)
			})
			var err error
			switch method {
			case http.MethodPost:
				_, err = s.CreatePolicy(context.Background(), "org", generated.CreatePolicyJSONRequestBody{})
			case http.MethodPut:
				err = s.UpdatePolicy(context.Background(), "org", "id", generated.UpdatePolicyJSONRequestBody{})
			case http.MethodDelete:
				err = s.DeletePolicy(context.Background(), "org", "id")
			}
			if err == nil || calls != 1 {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
		})
	}
}
