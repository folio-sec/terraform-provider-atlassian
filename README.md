# Terraform Provider Atlassian

Terraform provider for managing Atlassian Cloud resources across the Cloud
Admin and Confluence Cloud APIs.

## Documentation

See the generated [provider documentation](docs/index.md) for the provider
schema and for how each API family is authenticated. Every data source and
resource has its own page under [`docs/data-sources`](docs/data-sources) and
[`docs/resources`](docs/resources):

- **Cloud Admin** — organization groups, users, policies, workspaces, group
  membership, and role assignments, plus the Admin Control API
  [`atlassian_data_security_policy`](docs/resources/data_security_policy.md)
  resource.
- **Confluence Cloud** — [spaces](docs/resources/confluence_space.md) and the
  space data sources.

Atlassian also publishes documentation for the
[Organizations REST API](https://developer.atlassian.com/cloud/admin/organization/rest/)
and the Confluence Cloud REST API, both
[v2](https://developer.atlassian.com/cloud/confluence/rest/v2/) and
[v1](https://developer.atlassian.com/cloud/confluence/rest/v1/), which the
provider calls for different operations.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads)
- [Go](https://go.dev/doc/install)

## Building the Provider

1. Clone the repository.
2. Enter the repository directory.
3. Build the provider:

```sh
make build
```

The provider binary is written to `bin/terraform-provider-atlassian`.

## Using the Provider

Declare the provider source and configure credentials through environment
variables:

```hcl
terraform {
  required_providers {
    atlassian = {
      source = "folio-sec/atlassian"
    }
  }
}

provider "atlassian" {}
```

### Authentication

The provider covers more than one Atlassian API family, and each family has its
own credential. Configure only the families you use. Do not put secrets
directly in Terraform configuration.

Cloud Admin types authenticate with an Atlassian organization API key:

```sh
export ATLASSIAN_ADMIN_API_KEY="..."
```

Confluence types authenticate against a site in one of three ways: as a service
account with OAuth 2.0 client credentials, as a service account with an API
token issued to it, or as an Atlassian account over HTTP basic auth. Configure
exactly one.

```sh
export ATLASSIAN_SITE_URL="https://example.atlassian.net"

# Service account with client credentials.
export ATLASSIAN_SERVICE_ACCOUNT_CLIENT_ID="..."
export ATLASSIAN_SERVICE_ACCOUNT_CLIENT_SECRET="..."

# Or a service account API token.
export ATLASSIAN_SERVICE_ACCOUNT_API_TOKEN="..."

# Or basic auth as an Atlassian account.
export ATLASSIAN_EMAIL="user@example.com"
export ATLASSIAN_API_TOKEN="..."
```

A service account credential carries at most 50 scopes, and the scopes of an
API token are fixed when it is created. See the
[provider documentation](docs/index.md) for which scopes each type needs, for
`cloud_id` discovery, and for splitting scopes across provider aliases.

## Developing the Provider

Run the local checks and regenerate clients and documentation with:

```sh
make lint
go test -race ./...
make generate
git diff --check
```

`make generate` regenerates both the OpenAPI clients and the documentation.
`make generate/api-client` covers every vendored API surface — the Organization,
Admin Control, and Confluence v1 and v2 specifications under `api/` — and
per-surface targets such as `make generate/api-client/organization` regenerate
one of them. Generated clients are never edited by hand. A weekly GitHub Actions
workflow refreshes the upstream specifications and opens a pull request when
they change; local Overlay corrections stay separate from the untouched upstream
documents.

`make generate/docs` alone regenerates the files under `docs/` after a change to
provider schemas, examples, or templates.

## Debugging the Provider

Start the provider in Terraform Plugin Framework debug mode:

```sh
go run . -debug
```

Use the emitted `TF_REATTACH_PROVIDERS` value when running Terraform from
another terminal. Never enable verbose logging with real credentials in shared
or public environments.

## Release

The repository uses [tagpr](https://github.com/Songmu/tagpr) for version pull
requests and [GoReleaser](https://goreleaser.com/) for signed Terraform Registry
release artifacts. Run `make release/check` after changing release
configuration.

## Contribution

See [CONTRIBUTING.md](CONTRIBUTING.md). Every commit must include a DCO
sign-off.
