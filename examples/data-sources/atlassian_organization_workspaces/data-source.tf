data "atlassian_organization_workspaces" "example" {
  organization_id = "your-organization-id"
  search          = "your-site-name"
}

output "confluence_resource_ari" {
  value = one([for workspace in data.atlassian_organization_workspaces.example.workspaces : workspace.id if workspace.type_key == "confluence"])
}
