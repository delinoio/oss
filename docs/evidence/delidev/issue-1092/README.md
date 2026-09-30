# Issue #1092: same-account native Codex session forks

## Implementation

Replacement of the unmerged PR #1126 on inspected main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`. The implementation retains native `thread/fork`, immutable account/configuration, complete independent workspace snapshots, exact request receipts, source reservations, atomic child publication and paused child continuation. Source-backed fork contracts are retained; historical evidence remains frozen.

The replacement uses the current session/system proto owners, allocation-ledger capability number 13, CLI session dispatch and server status ownership. Generated service/client bindings preserve compatibility exports. No migration or credential-bearing user state is consumed.

## Validation

Validation is in progress. Results and evidence limits will be recorded before publication.
