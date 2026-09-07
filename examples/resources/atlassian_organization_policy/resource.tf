resource "atlassian_organization_policy" "example" {
  organization_id = "your-organization-id"

  data = {
    type = "policy"
    attributes = {
      type   = "data-residency"
      name   = "Japan data residency policy"
      status = "disabled"
      rule = {
        in = ["jp"]
      }
      # Only policies without resource associations are currently supported.
      resources = []
    }
  }
}
