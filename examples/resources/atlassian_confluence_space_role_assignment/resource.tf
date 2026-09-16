resource "atlassian_confluence_space_role_assignment" "example" {
  space_id = "123456"

  principal = {
    principal_type = "GROUP"
    principal_id   = "00000000-0000-0000-0000-000000000000"
  }

  role_id = "11111111-1111-1111-1111-111111111111"
}
