data "atlassian_organization_policy" "example" {
  organization_id = "your-organization-id"
  policy_id       = "your-policy-id"

  # Response rules and metadata are JSON strings; use jsondecode() to inspect them.
}
