resource "atlassian_data_security_policy" "example" {
  organization_id = "your-organization-id"

  data = {
    type = "policy"
    attributes = {
      type   = "data-security"
      name   = "Organization export default"
      status = "draft"
      rule = {
        export = {
          effect = "allow"
        }
      }
      metadata = {
        policy_coverage_level = "ORG"
        description           = "Allow exports by default for organization policy overrides."
      }
    }
  }
}
