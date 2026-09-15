package space

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence"
)

// fakeConfluence is a minimal httptest fake covering only what the space
// service touches: GET /wiki/api/v2/spaces and /wiki/api/v2/spaces/{id}.
type fakeConfluence struct {
	t *testing.T

	mu       sync.Mutex
	requests []recordedRequest

	// pages is consumed in order by requests to /wiki/api/v2/spaces; the last
	// entry repeats once exhausted.
	pages []string
	// spaceByID maps a space id to the getSpaceById response body, or to a
	// (status, body) pair recorded in notFound for a space that should 404.
	spaceByID map[string]string
	notFound  map[string]bool
}

type recordedRequest struct {
	path  string
	query map[string][]string
}

func newFakeConfluence(t *testing.T) (*fakeConfluence, *httptest.Server) {
	t.Helper()
	f := &fakeConfluence{t: t, spaceByID: map[string]string{}, notFound: map[string]bool{}}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	return f, server
}

func (f *fakeConfluence) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, recordedRequest{path: r.URL.Path, query: map[string][]string(r.URL.Query())})
	f.mu.Unlock()

	switch {
	case r.URL.Path == "/wiki/api/v2/spaces":
		f.mu.Lock()
		var page string
		if len(f.pages) > 0 {
			page, f.pages = f.pages[0], f.pages[1:]
		} else {
			page = `{"results":[]}`
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(page))
	case strings.HasPrefix(r.URL.Path, "/wiki/api/v2/spaces/"):
		id := strings.TrimPrefix(r.URL.Path, "/wiki/api/v2/spaces/")
		if f.notFound[id] {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[{"status":404,"code":"NOT_FOUND","title":"Cannot find a space with id [` + id + `]"}]}`))
			return
		}
		body, ok := f.spaceByID[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeConfluence) requestsTo(path string) []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recordedRequest
	for _, req := range f.requests {
		if req.path == path {
			out = append(out, req)
		}
	}
	return out
}

func newTestClient(t *testing.T, server *httptest.Server) *confluence.Client {
	t.Helper()
	c, err := confluence.New(confluence.Config{Mode: confluence.AuthBasic, SiteURL: server.URL, Email: "user@example.com", APIToken: "token"})
	if err != nil {
		t.Fatalf("confluence.New() error = %v", err)
	}
	return c
}

func TestGetSpaceByIDAlwaysSendsDescriptionFormatAndNoIncludes(t *testing.T) {
	t.Parallel()
	fake, server := newFakeConfluence(t)
	fake.spaceByID["10001"] = `{
		"id":"10001","key":"DEMO","name":"Demo Space","type":"global","status":"current",
		"authorId":"712020:author","spaceOwnerId":"712020:owner","homepageId":"20001",
		"createdAt":"2026-01-01T00:00:00.000Z","currentActiveAlias":"demo-alias",
		"description":{"plain":{"value":"A demo space.","representation":"plain"}}
	}`
	service := NewService(newTestClient(t, server))

	got, err := service.GetSpaceByID(context.Background(), "10001")
	if err != nil {
		t.Fatalf("GetSpaceByID() error = %v", err)
	}
	if got.ID != "10001" || got.Key != "DEMO" || got.Name != "Demo Space" {
		t.Fatalf("space = %#v", got)
	}
	if got.SpaceOwnerID == nil || *got.SpaceOwnerID != "712020:owner" {
		t.Fatalf("space owner id = %#v", got.SpaceOwnerID)
	}
	if got.CurrentActiveAlias == nil || *got.CurrentActiveAlias != "demo-alias" {
		t.Fatalf("current active alias = %#v, want demo-alias (the Overlay adds it to SpaceSingle, despite upstream omitting it)", got.CurrentActiveAlias)
	}
	if got.Description == nil || got.Description.Value != "A demo space." || got.Description.Representation != "plain" {
		t.Fatalf("description = %#v", got.Description)
	}

	requests := fake.requestsTo("/wiki/api/v2/spaces/10001")
	if len(requests) != 1 {
		t.Fatalf("requests to getSpaceById = %d, want 1", len(requests))
	}
	if got := requests[0].query["description-format"]; len(got) != 1 || got[0] != "plain" {
		t.Errorf("description-format = %v, want [plain]", got)
	}
	assertNoIncludeParams(t, requests[0], 0)
}

// assertNoIncludeParams fails the test if the request carries any include-*
// parameter. Each one converts into another OAuth scope a service account
// credential must be granted, and the 50-scope cap makes minimizing that
// spend a design constraint the space read path must never violate.
func assertNoIncludeParams(t *testing.T, req recordedRequest, page int) {
	t.Helper()
	for key := range req.query {
		if strings.HasPrefix(key, "include-") {
			t.Errorf("page %d: request carried %s, which converts into an OAuth scope the space read path must never require", page, key)
		}
	}
}

func TestGetSpaceByIDNotFound(t *testing.T) {
	t.Parallel()
	_, server := newFakeConfluence(t)
	service := NewService(newTestClient(t, server))

	_, err := service.GetSpaceByID(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatal("GetSpaceByID() error = nil, want a not-found error")
	}
	if !confluence.IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false, want true", err)
	}
}

func TestGetSpacesFollowsCursorAcrossPages(t *testing.T) {
	t.Parallel()
	fake, server := newFakeConfluence(t)
	fake.pages = []string{
		`{"results":[{"id":"1","key":"A","name":"A","type":"global","status":"current","authorId":"acc-1"}],
		  "_links":{"base":"` + server.URL + `","next":"/wiki/api/v2/spaces?cursor=page-2&limit=250"}}`,
		`{"results":[{"id":"2","key":"B","name":"B","type":"global","status":"current","authorId":"acc-1"}]}`,
	}
	service := NewService(newTestClient(t, server))

	got, err := service.GetSpaces(context.Background(), GetSpacesFilters{})
	if err != nil {
		t.Fatalf("GetSpaces() error = %v", err)
	}
	if len(got) != 2 || got[0].ID != "1" || got[1].ID != "2" {
		t.Fatalf("spaces = %#v", got)
	}

	requests := fake.requestsTo("/wiki/api/v2/spaces")
	if len(requests) != 2 {
		t.Fatalf("requests to getSpaces = %d, want 2", len(requests))
	}
	for i, req := range requests {
		if got := req.query["description-format"]; len(got) != 1 || got[0] != "plain" {
			t.Errorf("page %d: description-format = %v, want [plain]", i, got)
		}
		assertNoIncludeParams(t, req, i)
	}
	if got := requests[1].query["cursor"]; len(got) != 1 || got[0] != "page-2" {
		t.Errorf("second request cursor = %v, want [page-2]; the cursor must be extracted from _links.next, never followed as a URL", got)
	}
}

func TestGetSpacesFilterConversions(t *testing.T) {
	t.Parallel()
	fake, server := newFakeConfluence(t)
	service := NewService(newTestClient(t, server))

	_, err := service.GetSpaces(context.Background(), GetSpacesFilters{
		IDs:            []string{"10001", "10002"},
		Keys:           []string{"DEMO"},
		Type:           "global",
		Status:         "current",
		Labels:         []string{"important"},
		FavoritedBy:    "712020:user",
		NotFavoritedBy: "712020:other",
	})
	if err != nil {
		t.Fatalf("GetSpaces() error = %v", err)
	}

	requests := fake.requestsTo("/wiki/api/v2/spaces")
	if len(requests) != 1 {
		t.Fatalf("requests to getSpaces = %d, want 1", len(requests))
	}
	query := requests[0].query
	if got := query["ids"]; len(got) != 2 || got[0] != "10001" || got[1] != "10002" {
		t.Errorf("ids = %v, want [10001 10002]", got)
	}
	if got := query["keys"]; len(got) != 1 || got[0] != "DEMO" {
		t.Errorf("keys = %v, want [DEMO]", got)
	}
	if got := query["type"]; len(got) != 1 || got[0] != "global" {
		t.Errorf("type = %v, want [global]", got)
	}
	if got := query["status"]; len(got) != 1 || got[0] != "current" {
		t.Errorf("status = %v, want [current]", got)
	}
	if got := query["favorited-by"]; len(got) != 1 || got[0] != "712020:user" {
		t.Errorf("favorited-by = %v, want [712020:user]", got)
	}
	if got := query["not-favorited-by"]; len(got) != 1 || got[0] != "712020:other" {
		t.Errorf("not-favorited-by = %v, want [712020:other]", got)
	}
	assertNoIncludeParams(t, requests[0], 0)
}

// TestGetSpacesRejectsCursorlessNextLink guards against silently truncating a
// result: a next link the provider cannot follow is a broken API contract,
// not the end of the pages.
func TestGetSpacesRejectsCursorlessNextLink(t *testing.T) {
	t.Parallel()
	fake, server := newFakeConfluence(t)
	fake.pages = []string{`{"results":[],"_links":{"next":"/wiki/api/v2/spaces?limit=250"}}`}
	service := NewService(newTestClient(t, server))

	if _, err := service.GetSpaces(context.Background(), GetSpacesFilters{}); err == nil ||
		!strings.Contains(err.Error(), "carries no cursor") {
		t.Fatalf("GetSpaces() error = %v, want one naming the missing cursor", err)
	}
}

// TestGetSpaceByKeyRechecksTheKey covers the reason this helper exists:
// whether the keys= filter matches exactly or by prefix was never established
// against a live site, so the results are filtered again here. Taking the
// filter's first result would bind the wrong space -- and in the resource's
// adoption path, delete it on the next destroy.
func TestGetSpaceByKeyRechecksTheKey(t *testing.T) {
	t.Parallel()

	space := func(id, key string) string {
		return `{"id":"` + id + `","key":"` + key + `","name":"n","type":"global","status":"current","authorId":"a"}`
	}
	tests := map[string]struct {
		page    string
		wantKey string
		wantErr string
	}{
		"exact match": {
			page:    `{"results":[` + space("1", "DEMO") + `]}`,
			wantKey: "DEMO",
		},
		"filter also returned a prefix match": {
			page:    `{"results":[` + space("1", "DEMOLITION") + `,` + space("2", "DEMO") + `]}`,
			wantKey: "DEMO",
		},
		"only a prefix match is not a match": {
			page:    `{"results":[` + space("1", "DEMOLITION") + `]}`,
			wantErr: "not found",
		},
		"no results": {
			page:    `{"results":[]}`,
			wantErr: "not found",
		},
		"two exact matches are refused rather than guessed": {
			page:    `{"results":[` + space("1", "DEMO") + `,` + space("2", "DEMO") + `]}`,
			wantErr: "2 Confluence spaces returned",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake, server := newFakeConfluence(t)
			fake.pages = []string{tt.page}
			got, err := NewService(newTestClient(t, server)).GetSpaceByKey(context.Background(), "DEMO")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("GetSpaceByKey() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetSpaceByKey() error = %v", err)
			}
			if got.Key != tt.wantKey {
				t.Errorf("Key = %q, want %q", got.Key, tt.wantKey)
			}
		})
	}
}
