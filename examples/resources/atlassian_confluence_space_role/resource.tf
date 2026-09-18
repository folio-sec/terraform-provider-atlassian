resource "atlassian_confluence_space_role" "example" {
  name        = "Content viewers"
  description = "Can view space content"

  space_permissions = [
    "read/space",
  ]
}
