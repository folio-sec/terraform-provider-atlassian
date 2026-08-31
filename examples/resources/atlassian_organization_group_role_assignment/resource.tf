data "atlassian_organization_workspaces" "example" {
  organization_id = "your-organization-id"
  search          = "your-site-name"
}

resource "atlassian_organization_group_role_assignment" "confluence" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  group_id        = "your-group-id"
  resource        = one([for workspace in data.atlassian_organization_workspaces.example.workspaces : workspace.id if workspace.type_key == "confluence"])
  role            = "atlassian/user"
}
