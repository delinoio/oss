# DevHud Deck private Connect contract

`v1/*.proto` is the authoritative `devhud.deck.v1` source contract. It defines
exactly `DeckViewService`, `DeckIntegrationService`, and `DeckDeviceService`.
Generated Go and protobuf-es v2 TypeScript artifacts under `gen/` are checked
in as reproducible derived views.

The Go packages are:

- `github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1`
- `github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1/deckv1connect`

The private workspace TypeScript package is
`@delinoio/devhud-deck-connect`. It exports all v1 messages and `GenService`
descriptors from its root and exposes
`@delinoio/devhud-deck-connect/devhud-deck/v1/*_pb` subpaths. It is private
workspace infrastructure, not a published package or public plugin/API SDK.
Only authenticated Deck code in `apps/devhud`, the
`DeckIntegrationService`-only DeliDev settings client, the planned Deck server,
and delibase's typed lifecycle delivery may consume it.

## Contract invariants

- Persisted identifiers and replay keys use canonical UUID v7 wrappers.
- Lists use opaque cursors. The descriptor fixes 50 personal views, 250 views
  per organization, and 500 pull-request results per view; result pages report
  truncation explicitly.
- `Revision` contains both a monotonic value and opaque ETag. Existing
  synchronized mutations carry an expected revision.
- A view's raw GitHub search is authoritative. Typed builder clauses cover
  owners/repositories, authors, assignees, individual/team reviewers, labels,
  state/draft, base/head, review decision, checks, and updated ranges while
  retaining unrecognized raw clauses.
- The view registry contains only `GITHUB_PULL_REQUESTS`. Update input has no
  owner or kind field, may update the view notification preference, and there
  is no transfer, copy, plugin, or remote-UI RPC.
- Pull-request mutations are a closed typed union. Merge requires explicit
  confirmation. List results expose individual/team reviewer identities for
  grouping, current assignees and labels for removal actions, open/closed/merged
  lifecycle state independently from draft state, supported mutations,
  available merge methods, and the revision required by mutations; comments,
  approvals, and change requests are not Deck actions.
- Shortcut bindings use closed modifier/key enums shared with the DevHud native
  registry. Device writes carry request-only shortcut configurations; effective
  conflict state and synchronized revisions are server-authored response state.
- Initial device registration omits `expected_revision`; lease renewal carries
  the current device revision so a stale renewal cannot overwrite newer mutable
  device configuration.
- Widget actions are client behavior and cannot carry a mutation. Push payloads
  contain only opaque event identifiers. Device writes carry widget
  configuration only; widget snapshots and synchronized state are server-owned.
- Stable non-OK responses carry `ErrorDetail`; clients switch on `ErrorReason`,
  not the display-safe message.

See [AUTHENTICATION.md](AUTHENTICATION.md) for the Deck bearer, memory-only
forwarded delibase bearer, device cleanup grant, and lifecycle M2M boundaries.

## Generation and validation

From the repository root after `pnpm install`:

```sh
pnpm --dir protos/devhud-deck generate:proto
pnpm --dir protos/devhud-deck check:proto
go test ./protos/devhud-deck/...
go vet ./protos/devhud-deck/...
pnpm --filter @delinoio/devhud-deck-connect typecheck
```

The root `pnpm generate:proto` and `pnpm check:proto` commands include Deck in
their fixed component order. Generation owns only this component's `gen/go`,
`gen/ts`, `devhud.deck.v1.binpb`, and TypeScript `dist`. The compatibility
check uses only the immutable Deck v1 descriptor baseline and regenerates twice
to reject nondeterministic or stale output. It does not publish the package,
deploy an API, activate the catalog meter, or register a GitHub App.
