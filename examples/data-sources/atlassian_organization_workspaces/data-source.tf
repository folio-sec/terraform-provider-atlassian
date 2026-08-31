data "atlassian_organization_workspaces" "example" {
  organization_id = "your-organization-id"

  query = {
    search = "your-site-name"

    fields = [{
      name   = "attributes.type"
      values = ["confluence"]
    }]
  }
}

output "confluence_resource_ari" {
  value = one(data.atlassian_organization_workspaces.example.workspaces).id
}
