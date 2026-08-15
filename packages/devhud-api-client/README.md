# @delinoio/devhud-api-client

Generated `devhud.v1` Protobuf service descriptors and Connect Query method
exports for the DevHud and DevHud Admin SPAs.

The package is generated from `protos/devhud/v1`. Run `pnpm proto:generate`
from the repository root after changing a schema. Application code owns the
Connect transport, authentication headers, React Query client, persistence,
and redacted logging.

Import service/message descriptors from the package root or from stable
`@delinoio/devhud-api-client/devhud/v1/*` subpaths. Read the response
correlation ID with `getDevHudCorrelationId`; the package never logs it or any
request data automatically.
