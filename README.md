# Terraform Provider Atlassian

Terraform provider for managing Atlassian Cloud resources.

Supported organization workflows manage groups, find existing users and
groups, and manage either a user's group membership or a direct role
assignment. Invitations are intentionally outside the provider's scope.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads)
- [Go](https://go.dev/doc/install)

## Development

```sh
make build
make test
make generate
```

The Organization API client is generated from the vendored Atlassian OpenAPI
specification under `api/admin/organization`. `make generate/api-client`
regenerates only that client; the weekly GitHub Actions workflow refreshes the
upstream specification and opens a pull request when it changes. Local Overlay
corrections remain separate from the untouched upstream document.

Organization resources use an Atlassian organization API key supplied with
`admin_api_key` or `ATLASSIAN_ADMIN_API_KEY`.

```hcl
provider "atlassian" {
  # Prefer setting ATLASSIAN_ADMIN_API_KEY in the environment.
}

data "atlassian_organization_users" "example" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  emails           = ["user@example.com"]
}

data "atlassian_organization_groups" "jira_users" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  group_names     = ["jira-users"]
}

resource "atlassian_organization_group_membership" "example" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  group_id        = one(data.atlassian_organization_groups.jira_users.groups).group_id
  account_id      = one(data.atlassian_organization_users.example.users).account_id
}

resource "atlassian_organization_user_role_assignment" "jira" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  account_id      = one(data.atlassian_organization_users.example.users).account_id
  resource        = "ari:cloud:jira::site/your-site-id"
  role            = "atlassian/user"
}
```

## API rate limiting

Each provider client spaces Admin API requests at least 200 milliseconds apart
(including retries). This is a conservative local pacing policy, not an
Atlassian quota. Valid `X-RateLimit-Remaining` and `X-RateLimit-Reset` headers
can slow requests further; an exhausted budget pauses subsequent requests
until reset.

A `429` response pauses all subsequent requests on that client, including
requests to other Admin API endpoints. The provider honors `Retry-After` and
exhausted-budget reset deadlines, and otherwise uses the reset header or
exponential backoff with jitter. Requests already in flight cannot be recalled.
Separate provider configurations and Terraform processes do not share the
limiter; coordinate concurrent runs that use the same server quota.

A request fails if a single wait for permission to send exceeds five minutes,
or earlier if its context is cancelled. Server deadlines are not shortened to
force a retry. The existing limit of four retries remains in effect. Mutations
are retried only after rate limiting; ambiguous failures retain the existing
read-back verification behavior. Large plans and applies may take longer as
requests are paced, without changing membership state or caching reads.

## Contribution

See [CONTRIBUTING.md](CONTRIBUTING.md).
