# Confluence Cloud REST API v1 specification

`upstream.json` is the published Confluence Cloud REST API v1 OpenAPI document
from:

https://dac-static.atlassian.com/cloud/confluence/swagger.v3.json

Do not edit generated clients directly. Update the vendored specification or
Overlay, then run `make generate/api-client/confluence-v1`.

This client exists only to cover the space lifecycle operations that v2 does
not expose. v2 has `createSpace`, `getSpaces` and `getSpaceById` but no update
and no delete; v1 has `updateSpace` and `deleteSpace` but no space read at all.
A managed space resource therefore has to span both versions. Keep
`include-operation-ids` limited to `updateSpace`, `deleteSpace` and `getTask`
so the generated surface stays small, and keep every read on v2.

The two versions share a host and credentials but nothing else: v1 is keyed by
`spaceKey` while v2 is keyed by the numeric string space id, and the request
and response schemas are unrelated. The Overlay removes the document's
scheme-relative `//your-domain.atlassian.net` server because the handwritten
client supplies the base URL, which differs per authentication mode.

`deleteSpace` returns `202` with a long-running task. Poll `getTask` using the
task's `id`; the `links.status` URL in the response omits the `/wiki` prefix
and 404s in both authentication modes. See
`plans/confluence-space-verification.json`.
