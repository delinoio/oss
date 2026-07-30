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

Repository issue-definition types are generated from `common.proto`. The
`preset_pb` subpath continues to re-export them for source compatibility, while
tracker-only consumers can import `common_pb` and `tracker_pb` without loading
preset or submission descriptors.

The package is consumed by the future `servers/devhud-realqa`, authenticated
RealQA code under `apps/devhud`, `servers/delibase` for the typed
service-authenticated feature-deletion call, and DeliDev only through the
common-message and tracker subpaths for its bounded settings UI. Arbitrary
browser clients, third-party trackers, and external plugins are not consumers.

Run `pnpm --dir protos/devhud-realqa generate:proto` to regenerate the isolated
descriptor and both language outputs. Run
`pnpm --dir protos/devhud-realqa check:proto` for scoped Buf lint and
compatibility checks, two-pass deterministic generation, package build, and
cross-component write isolation. See `AUTHENTICATION.md` for credential
metadata that deliberately remains outside protobuf messages.
