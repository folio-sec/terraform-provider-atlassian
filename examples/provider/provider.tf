terraform {
  required_providers {
    atlassian = {
      source = "folio-sec/atlassian"
    }
  }
}

provider "atlassian" {
  # Set ATLASSIAN_ADMIN_API_KEY instead of committing an organization API key.
}
