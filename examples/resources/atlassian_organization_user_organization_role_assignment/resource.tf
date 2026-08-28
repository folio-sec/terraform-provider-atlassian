data "atlassian_organization_users" "office_it" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  emails          = ["office-it@example.com"]
}

resource "atlassian_organization_user_organization_role_assignment" "office_it" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  account_id      = one(data.atlassian_organization_users.office_it.users).account_id
  role            = "atlassian/org-admin"
}
