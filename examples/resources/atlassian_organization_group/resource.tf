resource "atlassian_organization_group" "example" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  name            = "example-group"
  description     = "Managed by Terraform"
}
