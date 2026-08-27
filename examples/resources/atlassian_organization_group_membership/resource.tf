data "atlassian_organization_user" "example" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  emails          = ["user@example.com"]
}

resource "atlassian_organization_group_membership" "example" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  group_id        = "your-group-id"
  account_id      = one(data.atlassian_organization_user.example.users).account_id
}
