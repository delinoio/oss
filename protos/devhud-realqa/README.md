# `@delinoio/devhud-realqa-connect`

This private workspace package owns the versioned `devhud.realqa.v1` source
contract and reproducible Connect artifacts for Go and TypeScript. It is not a
public API, tracker plugin SDK, or publishable package.

- Source: `v1/*.proto`
- Go output: `gen/go/devhud-realqa/v1`
- TypeScript output: `gen/ts/devhud-realqa/v1`
- Compatibility descriptor: `devhud.realqa.v1.binpb`
- Root export: `@delinoio/devhud-realqa-connect`
- Versioned subpath export:
  `@delinoio/devhud-realqa-connect/devhud-realqa/v1/{common,preset,submission,tracker}_pb`

The package is consumed only by the future `servers/devhud-realqa`,
authenticated RealQA code under `apps/devhud`, and `servers/delibase` for the
typed service-authenticated feature-deletion call. DeliDev, arbitrary browser
clients, third-party trackers, and external plugins are not consumers.

Run `pnpm --dir protos/devhud-realqa generate:proto` to regenerate the isolated
descriptor and both language outputs. Run
`pnpm --dir protos/devhud-realqa check:proto` for scoped Buf lint and
compatibility checks, two-pass deterministic generation, package build, and
cross-component write isolation. See `AUTHENTICATION.md` for credential
metadata that deliberately remains outside protobuf messages.

