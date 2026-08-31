# Repository guidance

Keep this file limited to project-specific rules that are not obvious from the
code. Update it when the same implementation or review mistake recurs.

## Language

- Write commit messages and repository artifacts in English by default. Use
  another language only when the artifact's intended audience or an external
  requirement makes it necessary.

## Contribution workflow

- Sign off every commit for Developer Certificate of Origin (DCO) compliance,
  for example by using `git commit --signoff`.
- When opening a pull request, follow and complete the repository's pull
  request template.

## Architecture

- Separate clients and Terraform services by Atlassian API surface when their
  endpoints, authentication requirements, schemas, or lifecycle semantics
  differ. Share transports only for behavior that is actually common.
- Mirror API boundaries under `internal/client` and `internal/services`. Go file
  and type names inside an API-specific directory do not need to repeat that
  directory's prefix.
- Provider configuration should contain credentials and other global context
  required to authenticate or initialize API clients. Identifiers used only in
  individual request paths belong to the relevant data source or resource.
- Use the generated OpenAPI client for request and response types. Keep
  pagination, Terraform-facing models, retry decisions, and lifecycle behavior
  in the handwritten service layer.

## Terraform modeling

- Stay faithful to the API by default. Data sources and resources should mirror
  the shape and vocabulary of the operation they wrap: name service methods
  after the operation ID, keep request attributes nested the way the request
  body nests them, and name attributes after the API fields they map to rather
  than inventing friendlier synonyms. Prefer exposing an operand group as it is
  defined over flattening one member of it out, so the remaining members can be
  added later without reshaping the schema.
- Depart from the API only for a stated reason, and record that reason in a
  comment next to the code that departs. Legitimate reasons include Terraform
  requirements the API cannot express, response-shaping controls that must stay
  internal, and specification text contradicted by an observed response.
- When an API operation returns a collection, expose it consistently as a list
  or set for every cardinality, including zero or one result. Use a set when
  ordering has no semantic meaning, and follow API pagination internally unless
  pagination itself is part of the Terraform use case.
- Validate import identity values with the same rules as resource
  configuration. Keep string-ID import for `terraform import` CLI compatibility
  even though Terraform 1.12+ resource identity is preferred.
- Keep each example under `examples/data-sources` and `examples/resources` to
  the block being documented, because the file is rendered verbatim into the
  registry page for that one type. A data source example declares the `data`
  block and nothing else; a resource example declares the `resource` block plus
  only the `data` blocks its arguments actually reference. Leave out `output`,
  `variable`, `provider`, and `terraform` blocks; `examples/provider` is the
  only place provider configuration belongs.

## Organization API behavior

- `atlassian_organization_users` must expose API filter names where they affect
  which users match. Do not expose response-shaping or ordering options such as
  `expand` or `sort_by`.
- The user role assignment resource uses the resource-scoped application-role
  grant and revoke endpoints. `atlassian/org-admin` uses separate
  organization-level endpoints with different lifecycle semantics, so it must
  not be managed by this resource.
- Use `github.com/hashicorp/go-retryablehttp` for Admin API transport retries.
  Do not automatically retry mutations unless the operation is proven
  idempotent; verify ambiguous outcomes with a read when possible.

## Generated files and verification

- Never edit generated API clients by hand. Use the generator target for the
  relevant API surface; for the Organization API, run `make generate/api-client`
  after changing the vendored specification, Overlay, or generator configuration.
- Keep the write-enabled OpenAPI update workflow limited to downloading,
  generating, and opening a pull request. Run generated code and tests only in
  the pull request CI job with read-only repository permissions. Pin third-party
  GitHub Actions to full commit SHAs.
- After changing provider schemas, examples, or documentation templates, run
  `make generate/docs` instead of editing generated files only.
- When adding or updating a direct Go dependency, use its latest stable
  compatible version, run `go mod tidy`, and update generator version references
  when applicable.
- Manage development CLI versions with `aqua.yaml`; do not install unpinned tool
  versions in CI. Before handing off Go changes, run `make lint`,
  `go test -race ./...`, and `git diff --check`.
- Releases are prepared by tagpr and published from its draft GitHub Release by
  GoReleaser. Keep Terraform Registry artifacts and checksum signing in the
  automated release workflow rather than creating or replacing them manually.
  Run `make release/check` after changing release configuration.
