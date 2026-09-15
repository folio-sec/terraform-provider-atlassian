terraform {
  required_providers {
    atlassian = {
      source = "folio-sec/atlassian"
    }
  }
}

provider "atlassian" {
  # Cloud Admin API types. Set ATLASSIAN_ADMIN_API_KEY instead of committing an
  # organization API key.

  # Confluence types. Configure exactly one of service_account and basic_auth.
  # A service account authenticates with either client_id + client_secret or
  # an api_token issued to it. Every service_account attribute falls back to an
  # ATLASSIAN_SERVICE_ACCOUNT_ variable (CLIENT_ID, CLIENT_SECRET, API_TOKEN,
  # CLOUD_ID); basic_auth falls back to ATLASSIAN_EMAIL / ATLASSIAN_API_TOKEN.
  site_url = "https://example.atlassian.net"

  service_account = {
    # cloud_id is discovered from site_url when omitted.
  }
}
