# Confluence Cloud REST API v2 specification

`upstream.json` is the published Confluence Cloud REST API v2 OpenAPI document
from:

https://dac-static.atlassian.com/cloud/confluence/openapi-v2.v3.json

Do not edit generated clients directly. Update the vendored specification or
Overlay, then run `make generate/api-client/confluence-v2`.

This API is per-site, not per-organization: it does not accept the organization
API key used by the Admin API clients. It authenticates either with basic auth
(a user's email and API token, against the site domain) or with a service
account's OAuth 2.0 client credentials (a bearer token, against
`https://api.atlassian.com/ex/confluence/{cloudId}`). Because the base URL
differs between those two modes, the Overlay removes the document's templated
`servers` entry and the handwritten client supplies the base URL instead.

The generator includes only the space operations the provider uses. Space
update and delete do not exist in v2 and are taken from v1; see
`api/confluence/v1/README.md`.

Verified API behavior that this document gets wrong, with the requests that
established it, is recorded in `plans/confluence-space-verification.json`.
Most importantly, every space id the API returns is a string while the
`getSpaceById` path parameter is typed `integer`; the Overlay retypes the
parameter so identifiers stay strings end to end.
