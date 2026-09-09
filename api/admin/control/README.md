# Atlassian Admin Control API specification

`upstream.json` is the published Atlassian Admin Control OpenAPI document from:

https://dac-static.atlassian.com/cloud/admin/control/swagger.v3.json

Do not edit generated clients directly. Update the vendored specification or
Overlay, then run `make generate/api-client`.

Atlassian marks the public policy operations used here as deprecated and states
that deprecated Control APIs may be temporarily disabled. No public replacement
is currently documented. Keep the deprecation markers in generated code and
limit suppressions to the handwritten calls that intentionally wrap this API.
