package client

import (
	"net/http"
	"testing"
)

func TestNew(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		siteURL  string
		email    string
		apiToken string
		wantErr  bool
	}{
		"valid":            {siteURL: "https://example.atlassian.net/", email: "admin@example.com", apiToken: "token"},
		"missing site URL": {email: "admin@example.com", apiToken: "token", wantErr: true},
		"non HTTPS URL":    {siteURL: "http://example.atlassian.net", email: "admin@example.com", apiToken: "token", wantErr: true},
		"missing email":    {siteURL: "https://example.atlassian.net", apiToken: "token", wantErr: true},
		"missing token":    {siteURL: "https://example.atlassian.net", email: "admin@example.com", wantErr: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := New(tt.siteURL, tt.email, tt.apiToken, &http.Client{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("New() returned no error")
				}
				return
			}
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if got.BaseURL.String() != "https://example.atlassian.net" {
				t.Fatalf("BaseURL = %q", got.BaseURL.String())
			}
		})
	}
}
