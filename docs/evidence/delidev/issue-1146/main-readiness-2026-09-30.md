# OpenRouter OAuth readiness after the provider-picker merge

Issue: [#1146](https://github.com/delinoio/oss/issues/1146).
Inspected main: `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`, freshly fetched on
2026-09-30. Preparatory branch: `kdy1/delidev-openrouter-oauth`; main was merged
without conflicts at `3351e513e1b7d818a344907834038a1d12fcd54a`.

## Current implementation and prerequisite

The direct-action provider picker, issue #1145, is now closed after its merged
implementation. Main includes its provider action buttons and preserves all four
provider-inventory capability gates. That previously reported prerequisite is
satisfied; the earlier foundation record remains historical evidence.

The existing OAuth code on this preparatory branch still consists only of exact
authorization-code/callback validation, managed OpenRouter eligibility, S256
authorization construction and a bounded single-call exchange helper. A search
for `OpenRouterOAuthEligible`, `NewOpenRouterAuthorization`,
`ExchangeOpenRouterCode`, `ValidateOAuthCode` and `ValidateOAuthCallback` confirms
that no product coordinator calls them. No OAuth RPC, CLI command, durable attempt
table, native callback authority or waiting/completion UI is implemented. The
issue remains open; this is not completion evidence.

The [structure contract](../../../cmds-delidev-structure-contract.md) requires
new protocol allocations to be established on main before dependent feature
branches use them. Its migration sequence also requires a complete preceding
implementation and forbids empty migrations. Main still executes schema 24 and
reserves 25/26/27 for the replacements of PRs #1108/#1115/#1117. No OAuth allocation
or migration reservation exists. Claiming 25 locally would collide; implementing
28 now would skip three unimplemented migrations.

## Concrete reservation proposal awaiting an ordering decision

These numbers are proposals, not established reservations or runtime support:

| Shared declaration | Proposed addition | Number |
| --- | --- | --- |
| `ProviderInventoryCapability` | `PROVIDER_INVENTORY_CAPABILITY_OAUTH_PKCE` | 5 |
| `ProviderInventoryEntry` | `connection_methods` | 9 |

`connection_methods` would use a new closed provider-owned enum with unspecified,
API-key, OAuth-PKCE and keyless values. Only the eligible saved managed OpenRouter
entry could include OAuth-PKCE; the independent capability does not replace any
of the existing four account-flow gates. New OAuth request/response/state types
would remain owned by `account.proto`; no existing RPC or field meaning changes.

Two migration sequences can satisfy the contract after review and establishment
on main:

| Ordering | OAuth | Grok accounting (#1108) | Claude accounting (#1115) | Request diagnostics (#1117) |
| --- | --- | --- | --- | --- |
| Prioritize OAuth | 25 | 26 | 27 | 28 |
| Preserve existing priority | 28 | 25 | 26 | 27 |

Prioritizing OAuth preserves the accounting composition dependencies and all
original branch-version evidence, but changes the current planned feature order.
The reservation validator currently requires increasing PR numbers; that
incidental chronology would need to become unique reservation ownership and
preceding-dependency validation, with explicit issue/PR identity support. It
must still reject duplicates, version gaps and a dependency placed after its
consumer. This would be a scoped contract/AGENTS/ledger/test change before the
OAuth implementation, not permission to activate a no-op migration.

Preserving priority requires all three preceding implementations before the
OAuth migration can execute. Neither proposal has been applied. The ordering
question was presented to the owner in this chat; implementation dependent on
that decision remains pending.

## Executed verification

From `cmds/delidev-cli`, after merging the inspected main:

- `go test -timeout=2m ./internal/domain ./internal/providers`: passed.
- `go test -race -timeout=2m ./internal/domain ./internal/providers`: passed.
- `go vet ./internal/domain ./internal/providers`: passed.

No new frontend or Rust source was authored in this readiness pass. Full Go,
frontend, native, protocol-generation, migration/restart/cancellation and real
OpenRouter checks were not rerun. The earlier full-suite failures remain recorded
in [the foundation record](README.md); focused success does not establish that
they are resolved or pre-existing. No user credentials or real provider exchange
were used. No completion PR or PR-maintenance heartbeat exists.
