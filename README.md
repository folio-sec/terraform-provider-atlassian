# Terraform Provider Atlassian

Terraform provider for managing Atlassian Cloud resources.

This repository currently contains the provider skeleton: provider startup,
configuration, shared API client configuration, documentation generation, and
CI. Resources and data sources can be added under `internal/`.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads)
- [Go](https://go.dev/doc/install)

## Development

```sh
make build
make test
make generate
```

The provider accepts `site_url`, `email`, and `api_token`. The corresponding
`ATLASSIAN_SITE_URL`, `ATLASSIAN_EMAIL`, and `ATLASSIAN_API_TOKEN` environment
variables can be used instead.

## Contribution

See [CONTRIBUTING.md](CONTRIBUTING.md).
