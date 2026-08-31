import {
  to = atlassian_organization_group_role_assignment.confluence

  identity = {
    organization_id = "your-organization-id"
    directory_id    = "your-directory-id"
    group_id        = "your-group-id"
    resource        = "ari:cloud:confluence::site/your-site-id"
    role            = "atlassian/user"
  }
}
