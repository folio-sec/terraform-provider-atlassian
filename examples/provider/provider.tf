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

  # Confluence types. Configure exactly one of service_account and basic_auth;
  # the credential values fall back to ATLASSIAN_CLIENT_ID /
  # ATLASSIAN_CLIENT_SECRET or ATLASSIAN_EMAIL / ATLASSIAN_API_TOKEN.
  site_url = "https://example.atlassian.net"

  service_account = {
    # cloud_id is discovered from site_url when omitted.
  }
}
