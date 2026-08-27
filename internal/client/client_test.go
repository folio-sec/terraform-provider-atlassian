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
		"Admin API key": {
			config: Config{AdminAPIKey: "admin-key"},
		},
		"missing Admin API key": {wantErr: true},
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
			if got.Admin == nil || got.Organization == nil {
				t.Fatal("Admin API services are nil")
			}
		})
	}
}
