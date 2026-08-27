import {
  to = atlassian_organization_user_role_assignment.jira

  identity = {
    organization_id = "your-organization-id"
    directory_id    = "your-directory-id"
    account_id      = "your-account-id"
    resource        = "ari:cloud:jira::site/your-site-id"
    role            = "atlassian/user"
  }
}
