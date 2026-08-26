package client

import (
	"net/http"
	"testing"
)

func TestNew(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		config  Config
		wantErr bool
	}{
		"site credentials": {
			config: Config{SiteURL: "https://example.atlassian.net/", Email: "admin@example.com", APIToken: "token"},
		},
		"Admin API key": {
			config: Config{AdminAPIKey: "admin-key"},
		},
		"both credential sets": {
			config: Config{SiteURL: "https://example.atlassian.net", Email: "admin@example.com", APIToken: "token", AdminAPIKey: "admin-key"},
		},
		"missing all credentials": {wantErr: true},
		"partial site credentials": {
			config:  Config{SiteURL: "https://example.atlassian.net", Email: "admin@example.com"},
			wantErr: true,
		},
		"non HTTPS site URL": {
			config:  Config{SiteURL: "http://example.atlassian.net", Email: "admin@example.com", APIToken: "token"},
			wantErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tt.config.HTTPClient = &http.Client{}
			got, err := New(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Fatal("New() returned no error")
				}
				return
			}
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if tt.config.SiteURL != "" && got.Site == nil {
				t.Fatal("Site is nil")
			}
			if tt.config.AdminAPIKey != "" && (got.Admin == nil || got.Organization == nil) {
				t.Fatal("Admin API services are nil")
			}
		})
	}
}
