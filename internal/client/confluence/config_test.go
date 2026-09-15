package confluence

import (
	"net/url"
	"strings"
	"testing"
)

func TestParseSiteURL(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in      string
		want    string
		wantErr string
	}{
		"https url":                {in: "https://example.atlassian.net", want: "https://example.atlassian.net"},
		"bare host":                {in: "example.atlassian.net", want: "https://example.atlassian.net"},
		"trailing slash":           {in: "https://example.atlassian.net/", want: "https://example.atlassian.net"},
		"http is allowed":          {in: "http://localhost:8080", want: "http://localhost:8080"},
		"surrounding whitespace":   {in: "  https://example.atlassian.net  ", want: "https://example.atlassian.net"},
		"empty":                    {in: "   ", wantErr: "must not be empty"},
		"path is rejected":         {in: "https://example.atlassian.net/wiki", wantErr: "must not include a path"},
		"query is rejected":        {in: "https://example.atlassian.net?x=1", wantErr: "query or fragment"},
		"fragment is rejected":     {in: "https://example.atlassian.net#top", wantErr: "query or fragment"},
		"user info is rejected":    {in: "https://user:pw@example.atlassian.net", wantErr: "user information"},
		"other scheme":             {in: "ftp://example.atlassian.net", wantErr: "scheme must be https or http"},
		"missing host with scheme": {in: "https://", wantErr: "must include a host"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseSiteURL(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseSiteURL(%q) error = %v, want containing %q", tt.in, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSiteURL(%q) error = %v", tt.in, err)
			}
			if got.String() != tt.want {
				t.Fatalf("ParseSiteURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		config  Config
		wantErr string
	}{
		"basic auth": {
			config: Config{Mode: AuthBasic, SiteURL: "https://example.atlassian.net", Email: "a@example.com", APIToken: "tok"},
		},
		"basic auth without site": {
			config:  Config{Mode: AuthBasic, Email: "a@example.com", APIToken: "tok"},
			wantErr: "requires site_url",
		},
		"basic auth without token": {
			config:  Config{Mode: AuthBasic, SiteURL: "https://example.atlassian.net", Email: "a@example.com"},
			wantErr: "both email and api_token",
		},
		"service account with cloud id only": {
			config: Config{Mode: AuthServiceAccount, ClientID: "id", ClientSecret: "secret", CloudID: "a7c408f1-ec5f-410e-8f27-62dbefebe6b6"},
		},
		"service account with site only": {
			config: Config{Mode: AuthServiceAccount, ClientID: "id", ClientSecret: "secret", SiteURL: "https://example.atlassian.net"},
		},
		"service account with neither": {
			config:  Config{Mode: AuthServiceAccount, ClientID: "id", ClientSecret: "secret"},
			wantErr: "requires cloud_id, or site_url",
		},
		"service account without secret": {
			config:  Config{Mode: AuthServiceAccount, ClientID: "id", CloudID: "x"},
			wantErr: "both client_id and client_secret",
		},
		"service account api token with cloud id": {
			config: Config{Mode: AuthServiceAccountAPIToken, ServiceAccountAPIToken: "token", CloudID: "a7c408f1-ec5f-410e-8f27-62dbefebe6b6"},
		},
		"service account api token without a token": {
			config:  Config{Mode: AuthServiceAccountAPIToken, CloudID: "a7c408f1-ec5f-410e-8f27-62dbefebe6b6"},
			wantErr: "requires api_token",
		},
		"service account api token with neither cloud id nor site": {
			config:  Config{Mode: AuthServiceAccountAPIToken, ServiceAccountAPIToken: "token"},
			wantErr: "requires cloud_id, or site_url",
		},
		"invalid site url is rejected": {
			config:  Config{Mode: AuthBasic, SiteURL: "https://example.atlassian.net/wiki", Email: "a@example.com", APIToken: "tok"},
			wantErr: "must not include a path",
		},
		"mode unset": {
			config:  Config{SiteURL: "https://example.atlassian.net"},
			wantErr: "mode is not set",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := New(tt.config)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("New() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if got.Mode() != tt.config.Mode {
				t.Fatalf("Mode() = %v, want %v", got.Mode(), tt.config.Mode)
			}
		})
	}
}

// TestBaseURLs pins the joining behavior of the generated clients. They
// resolve operation paths as relative references ("./spaces"), so each row
// checks the URL a generated request would actually hit.
func TestBaseURLs(t *testing.T) {
	t.Parallel()

	const cloudID = "a7c408f1-ec5f-410e-8f27-62dbefebe6b6"
	site, _ := url.Parse("https://example.atlassian.net")

	tests := map[string]struct {
		mode   AuthMode
		wantV1 string
		wantV2 string
	}{
		"basic auth": {
			mode:   AuthBasic,
			wantV1: "https://example.atlassian.net/wiki/rest/api/longtask/1",
			wantV2: "https://example.atlassian.net/wiki/api/v2/spaces",
		},
		"service account": {
			mode:   AuthServiceAccount,
			wantV1: "https://api.atlassian.com/ex/confluence/" + cloudID + "/wiki/rest/api/longtask/1",
			wantV2: "https://api.atlassian.com/ex/confluence/" + cloudID + "/wiki/api/v2/spaces",
		},
		"service account api token": {
			mode:   AuthServiceAccountAPIToken,
			wantV1: "https://api.atlassian.com/ex/confluence/" + cloudID + "/wiki/rest/api/longtask/1",
			wantV2: "https://api.atlassian.com/ex/confluence/" + cloudID + "/wiki/api/v2/spaces",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			prefix, err := Prefix(tt.mode, site, cloudID)
			if err != nil {
				t.Fatalf("Prefix() error = %v", err)
			}
			v1, v2 := BaseURLs(prefix)
			if !strings.HasSuffix(v1.Path, "/") || !strings.HasSuffix(v2.Path, "/") {
				t.Fatalf("base URLs must end in a trailing slash: v1=%q v2=%q", v1, v2)
			}
			// The v1 operation path carries the full /wiki/rest/api prefix; the
			// v2 operation path is bare. This is exactly what oapi-codegen emits.
			if got := resolveLikeGeneratedClient(v1, "/wiki/rest/api/longtask/1"); got != tt.wantV1 {
				t.Errorf("v1 resolved = %q, want %q", got, tt.wantV1)
			}
			if got := resolveLikeGeneratedClient(v2, "/spaces"); got != tt.wantV2 {
				t.Errorf("v2 resolved = %q, want %q", got, tt.wantV2)
			}
		})
	}
}

func TestPrefixErrors(t *testing.T) {
	t.Parallel()

	if _, err := Prefix(AuthBasic, nil, ""); err == nil {
		t.Error("Prefix(AuthBasic, nil) returned no error")
	}
	if _, err := Prefix(AuthServiceAccount, nil, ""); err == nil {
		t.Error("Prefix(AuthServiceAccount, no cloud id) returned no error")
	}
	if _, err := Prefix(AuthServiceAccountAPIToken, nil, ""); err == nil {
		t.Error("Prefix(AuthServiceAccountAPIToken, no cloud id) returned no error")
	}
	if _, err := Prefix(AuthMode(0), nil, ""); err == nil {
		t.Error("Prefix(unknown mode) returned no error")
	}
}

// resolveLikeGeneratedClient mirrors the URL construction in oapi-codegen's
// generated New*Request functions.
func resolveLikeGeneratedClient(server *url.URL, operationPath string) string {
	if operationPath[0] == '/' {
		operationPath = "." + operationPath
	}
	resolved, err := server.Parse(operationPath)
	if err != nil {
		return "error: " + err.Error()
	}
	return resolved.String()
}
