# Issue #1100: broad Go race validation limits

## Revision and environment

Executed on 2026-09-30 in the isolated issue worktree at
`5fb978a57` (implementation `9993bd318`, base
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7`). Host: macOS arm64,
Go 1.26.8. All commands below ran from the repository root with isolated
fixture state, controlled providers and no user credentials. Other repository
Go and frontend test runs were active on the host. That observation is not
proof that every failure is environmental or pre-existing.

## Broad attempt

`go test -race -p 2 -timeout=20m ./cmds/delidev-cli/...` reported failures in
unchanged CLI, harness discovery, Claude and Codex fixtures. Observed outcomes:

- CLI configuration transfer and workspace readiness fixtures failed; the CLI
  package reported failure after 504.694 seconds.
- Harness discovery failed `TestDiscoveryVerifiesClaudeWithoutGrantingExecution`
  and `TestDiscoveryBoundsAndGrokUpdateSuppression`; package failure after
  86.863 seconds.
- Claude failed API stream authority, bounded probe, protocol cleanup and
  pre-canceled delivery fixtures; package failure after 810.750 seconds.
- Codex reported native handshake, uncertain delivery, continuation and
  response-inspection failures, then reached its 20-minute package deadline
  while `TestInteractionResponseInspectionSerializesAndRejectsUnownedAttempts`
  was running; package failure after 1200.758 seconds.
- API proxy, connections, credentials, domain and forwarding reported passes.

The owned process group was interrupted after these repeated failures while
Grok and native-wire packages were still running. The command exited 1. This
was an incomplete broad attempt: no final results are claimed for the running
or not-yet-executed packages. Other chats' processes were not interrupted.

## Bounded rerun

`GOMAXPROCS=2 go test -race -p 1 -failfast -timeout=10m ./cmds/delidev-cli/...`
completed with exit 1. API proxy passed in 2.675 seconds. CLI failed in
202.494 seconds at `TestCLISessionAcceptanceQueueAndArchive`: a temporary
private workspace did not reach verified dispatch readiness before
`session create --wait` returned `unavailable` / operation timed out.

The failfast command stopped after that package failure. Its log has no
results for the remaining packages, so this does not establish a complete
package-list pass or complete execution of every package. No product behavior,
fixture deadline or unrelated test was changed to conceal the failures.

## Independent issue verification

The complete focused race command covering all four changed Go packages
(domain, store, server and CLI) passed. It includes cleanup gating, immutable
receipt replay, interrupted/altered/missing closure rejection, exact unsigned
counters, mixed Codex/Grok units, rollback/restart, profile/capability
negotiation, historical schema convergence and unknown version-25 rejection.
The exact command, timings, frontend/protocol/build results and unperformed
native/account/platform/release acceptance are retained in
[the implementation evidence](grok-accounting.md).

These successful focused checks do not erase the broad-suite failures or
establish a complete local Go race pass. The complete local frontend pipeline
also remains failed as documented in the implementation evidence.
