# Terraform Provider Atlassian

Terraform provider for managing Atlassian Cloud resources.

The first supported workflow finds an existing organization user and manages a
direct application role assignment for that user. Invitations are intentionally
outside the provider's scope.

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

data "atlassian_organization_user" "example" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  emails           = ["user@example.com"]
}

resource "atlassian_organization_user_role_assignment" "jira" {
  organization_id = "your-organization-id"
  directory_id    = "your-directory-id"
  account_id      = one(data.atlassian_organization_user.example.users).account_id
  resource        = "ari:cloud:jira::site/your-site-id"
  role            = "atlassian/user"
}
```

## Contribution

See [CONTRIBUTING.md](CONTRIBUTING.md).
