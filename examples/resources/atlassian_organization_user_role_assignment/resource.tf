data "atlassian_organization_users" "example" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  emails          = ["user@example.com"]
}

resource "atlassian_organization_user_role_assignment" "jira" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  account_id      = one(data.atlassian_organization_users.example.users).account_id
  resource        = "ari:cloud:jira::site/your-site-id"
  role            = "atlassian/user"
}
