data "atlassian_organization_groups" "example" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  group_names     = ["jira-users"]
}
