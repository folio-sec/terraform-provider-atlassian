data "atlassian_organization_policies" "example" {
  organization_id = "your-organization-id"
  # Optional API filter. Omit it to retrieve all policy types.
  type = "data-residency"

  # Response rules and metadata are JSON strings; use jsondecode() to inspect them.
}
