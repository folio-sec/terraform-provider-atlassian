data "atlassian_organization_user" "example" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  emails          = ["user@example.com"]
}

output "account_id" {
  value = one(data.atlassian_organization_user.example.users).account_id
}
