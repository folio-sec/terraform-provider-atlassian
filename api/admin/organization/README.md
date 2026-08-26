# Atlassian Organization OpenAPI

`upstream.json` is an unmodified copy of Atlassian's published Organization
API specification:

https://dac-static.atlassian.com/cloud/admin/organization/swagger.v3.json

`overlay.yaml` contains local corrections required to load the upstream
document as valid OpenAPI. Keep corrections in the Overlay so upstream changes
remain reviewable.

The generator intentionally includes only the operations currently used by the
provider. Run the following command after changing the specification, Overlay,
or generator configuration:

```sh
make generate/api-client
```
