data "atlassian_organization_workspaces" "confluence" {
  organization_id = "your-organization-id"

  query = {
    search = "your-site-name"

    fields = [{
      name   = "attributes.type"
      values = ["confluence"]
    }]
  }
}

resource "atlassian_organization_group_role_assignment" "confluence" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  group_id        = "your-group-id"
  resource        = one(data.atlassian_organization_workspaces.confluence.workspaces).id
  role            = "atlassian/user"
}
