terraform {
  required_providers {
    atlassian = {
      source = "folio-sec/atlassian"
    }
  }
}

provider "atlassian" {
  site_url = "https://example.atlassian.net"
  email    = "admin@example.com"
  # Set ATLASSIAN_API_TOKEN instead of committing a token here.
}
