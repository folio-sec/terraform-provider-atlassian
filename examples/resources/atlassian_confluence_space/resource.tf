resource "atlassian_confluence_space" "example" {
  key  = "DEMO"
  name = "Demo Space"

  description = {
    value = "Managed by Terraform"
  }
}
