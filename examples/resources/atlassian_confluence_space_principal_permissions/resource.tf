resource "atlassian_confluence_space_principal_permissions" "example" {
  space_id  = "123456"
  space_key = "ENG"

  principal = {
    type = "group"
    id   = "00000000-0000-0000-0000-000000000000"
  }

  operations = [
    { key = "read", target_type = "space" },
    { key = "create", target_type = "page" },
    { key = "update", target_type = "page" },
  ]
}
