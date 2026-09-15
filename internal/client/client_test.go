package client

import (
	"net/http"
	"testing"

	"github.com/folio-sec/terraform-provider-atlassian/internal/client/confluence"
)

func TestNew(t *testing.T) {
	t.Parallel()

	basic := &confluence.Config{
		Mode:     confluence.AuthBasic,
		SiteURL:  "https://example.atlassian.net",
		Email:    "a@example.com",
		APIToken: "tok",
	}

	tests := map[string]struct {
		config         Config
		wantAdmin      bool
		wantConfluence bool
		wantErr        bool
	}{
		"Admin API key only": {
			config:    Config{AdminAPIKey: "admin-key"},
			wantAdmin: true,
		},
		"Confluence only": {
			config:         Config{Confluence: basic},
			wantConfluence: true,
		},
		"both families": {
			config:         Config{AdminAPIKey: "admin-key", Confluence: basic},
			wantAdmin:      true,
			wantConfluence: true,
		},
		// Neither family is not an error: the data sources and resources that
		// need a family report its absence themselves.
		"no credentials": {config: Config{}},
		"blank Admin API key is treated as absent": {
			config: Config{AdminAPIKey: "   "},
		},
		"inconsistent Confluence config is rejected": {
			config:  Config{Confluence: &confluence.Config{Mode: confluence.AuthBasic}},
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
			if gotAdmin := got.Admin != nil && got.Control != nil && got.Organization != nil; gotAdmin != tt.wantAdmin {
				t.Errorf("Admin family configured = %v, want %v", gotAdmin, tt.wantAdmin)
			}
			if !tt.wantAdmin && (got.Admin != nil || got.Control != nil || got.Organization != nil) {
				t.Error("Admin family partially configured; all three must be nil together")
			}
			if gotConfluence := got.Confluence != nil; gotConfluence != tt.wantConfluence {
				t.Errorf("Confluence family configured = %v, want %v", gotConfluence, tt.wantConfluence)
			}
		})
	}
}
