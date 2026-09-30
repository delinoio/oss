# Issue #1095: managed Codex subscription validation

The replacement implementation starts from main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` and adapts the unmerged implementation from [closed PR #1124](https://github.com/delinoio/oss/pull/1124) to the current service-specific schemas, CLI/server file ownership, Worker capability allocation and independent evidence structure. The previous branch observations are preserved separately in [historical evidence](previous-pr-1124.md).

All authentication fixtures use synthetic JWT/token material, temporary private runtime state and controlled native subprocesses. No user login or real provider account is imported.

## Checks executed on the replacement

- `go test ./cmds/delidev-cli/... -run 'Subscription|Managed|Bundle' -count=1`: passed, including protected Connect/SQLite lifecycle, concurrent leases, cancellation/revocation, bundle rotation, lost write-back, native process fixtures and execution authentication cleanup.
- `go vet ./cmds/delidev-cli/...`: passed.
- API client `pnpm test`: 44 tests passed. The first run hit the integration fixture's two-minute Go build deadline during concurrent machine compilation; the warmed-cache retry passed all four test files.
- API client `pnpm typecheck`: passed.
- `pnpm proto:generate` and `pnpm proto:lint`: passed after applying Buf's canonical import order. Full protocol compatibility/freshness validation is pending.
- `go fmt ./...`: passed after explicitly generating both Go embedded app asset prerequisites.
- Required full `go test -race ./cmds/delidev-cli/...`: running; no full-suite pass is claimed at this checkpoint.

## Acceptance limits

These fixtures establish deterministic protocol and ownership behavior, not real subscription OAuth, hosted inference, installed-Codex subscription execution, desktop login controls, full uncertain-lease recovery, native Windows/Linux runtime acceptance or release readiness. Complete issue #964 remains independent and unfinished.
