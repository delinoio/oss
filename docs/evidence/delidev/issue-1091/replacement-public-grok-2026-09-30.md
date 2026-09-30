# Public Grok replacement validation, 2026-09-30

Issue: [#1091](https://github.com/delinoio/oss/issues/1091). Implementation commit:
`9fc8cdb7e57cbc6c92cd5abaf4904e12b36981c1`. The initial freshly fetched base was
`ad0e3e9a29cb3d8375ab5d168bb160c35a023250`.

## Source and scope

The issue remained open and its public interaction gap was present on that base.
The earlier [#1125](https://github.com/delinoio/oss/pull/1125) and
[#1199](https://github.com/delinoio/oss/pull/1199) implementations were closed,
unmerged work retained during the DeliDev structural reconciliation. This change
adapts the latter's retained head `3fe3dc12cbbc9b5657dc47d8025e0a0bf23cb184` to
the scoped current owners. The original
[validation record](public-grok-interactions.md) is preserved as historical
evidence; its checks are not newly executed checks of this replacement.

The public composition retains Grok Build `1.0.41` original Read/Write,
remembered edit permission, questions, native Plan artifacts/revisions and native
transitions. Typed ordered Worker publication, independently checked server
journals, existing authenticated Connect responses and equivalent CLI documents
share the original native request and current authorization. Publication receipt
retries have no native sender. Delivery, native result acceptance, root terminal,
process cleanup and workspace cleanup remain separate.

This replacement also corrects retained review findings: the desktop accepts the
full native 32-question/64-option and UTF-8/aggregate bounds, accepts the server's
null generic-question field, preserves native integer request spellings outside
signed 64-bit range and `-0` through a Grok-only decimal identity variant, and
focuses the sole Write/Plan decision selector. Regression cases cover original
numeric digest/claim/receipt identity and completed remembered-Write provenance.
No protobuf field, enum number or migration version is allocated here.

## Executed checks

Checks ran on macOS arm64 with temporary private fixture state, scripted native
fixture processes and no user/provider credentials. Go commands used
`GOCACHE=/tmp/oss-1091-go-cache` and bounded compiler parallelism. Root dependency
installation succeeded, installed linked-worktree hooks, and relevant consumed
LFS assets were hydrated. Root administrator and async-commit-hook embedded
outputs were explicitly built for repository-wide Go tooling.

| Check | Actual result before main reconciliation |
| --- | --- |
| `pnpm --filter @delinoio/delidev-api-client build`; desktop `pnpm typecheck` | Passed, including a typecheck after the final draft-enum changes. |
| Root `pnpm proto:check` | Passed lint, breaking validation and forced reproducible generation; no generated drift. |
| Desktop `pnpm exec vitest run src/native-grok-interactions.test.tsx` | All 17 tests passed, including server-null shape, full native question bounds, UTF-8 limits, lexical numeric IDs and Plan focus. |
| Root focused `go test -race -p 2` for domain, Grok harness, Worker, server and CLI, with `-run 'Grok\|PublicFirstInput\|PublicToolJournal\|PublicNumericRequest' -count=1` | Domain, Worker and CLI passed. Harness reported four original first-input initialization/cleanup deadline failures; server reported the unsupported repository dispatch fixture's context deadline. The mixed/Plan/numeric projection and public response cases reported no failures. This combined command exited unsuccessfully. |
| Grok harness `go test -p 1 ... -run '^TestPublicFirstInputRetainsOriginalToolsAndTextClosure$' -count=1`, then the same with `-race` | Both passed all controlled Write allow/reject, native Plan and existing plain-text closure scenarios with unchanged source deadlines. |
| Server `go test -race -p 1 ... -run 'TestGrokPublic(Remembered\|Numeric)' -count=1` | Passed completed session-edit provenance, foreign provenance rollback, numeric request/claim identity and exact response receipt replay. |
| Root `go vet -p 1 ./cmds/delidev-cli/...` | Passed. |
| Required desktop `pnpm test` | Executed; unit phase failed with 24 failed/72 passed files and 72 failed/1,169 passed/12 skipped tests. Timing and fixture-build failures prevented its later packaging phases from running. This is not a passing script result. |
| Desktop serial unit run, `pnpm exec vitest run --maxWorkers=1 --no-file-parallelism` | 94 files/1,244 tests passed; two files/nine tests failed only their existing five- or fifteen-second timeouts. |
| Serial rerun of `src/App.test.tsx` and `src/settings.test.tsx` | Both files and all 93 tests passed with unchanged source deadlines. Together with the serial unit run this covers all 96 files/1,253 tests; it is not a single passing full-script run. |
| Desktop `pnpm test:bundle-dry-run`, `pnpm test:desktop-launch`, `pnpm test:widget`, `pnpm build` | Passed independently: eight bundle checks, 16 launch checks, widget fixtures and production build. |

The required root `go test -race -p 1 ./cmds/delidev-cli/...` was executed on the
replacement before main reconciliation. CLI failed
`TestCLISessionAcceptanceQueueAndArchive` while reading a prepared workspace file;
the unchanged-deadline focused rerun failed during workspace preparation. Claude
failed `TestCheckpointRestoresOnlyOriginalClosedEvidence` at the original process
control socket deadline. Codex, domain, API proxy, connections, credentials,
forwarding and common harness packages passed. The run was interrupted during the
Grok package to reconcile newer main; the remaining packages are not validated by
this incomplete run. It is not a passing full-suite result.

The unchanged-deadline focused rerun of
`TestGrokFirstDispatchRetainsUnsupportedSelectionsWithoutClaiming` also failed its
repository fixture during workspace preparation, before the unsupported dispatch
assertion. These failures remain visible. Process inspection observed several
other concurrent native suites and compiler jobs; host contention is a plausible
cause of deadline failures, not proof that every failure is unrelated to this
change.

## Current-main reconciliation

Merge `7515e63480a051b4ec1568feb0cd96b6e1079912` incorporates freshly fetched main
`574c1a92c957fc741a723ff8123888dad32a2194`, including issue #1100 native accounting
and the shared compaction reservations. Five conflicts affected only appended
scoped instructions and the protocol contract; both independent original-tool and
native-accounting requirements were preserved. Generated sources remain derived
from the reconciled schemas. No new reservation or migration is introduced by
#1091.

The new negotiated accounting unit still requires a first-text closed-history
terminal. Tool/Plan aggregate display does not supply that history or create a
GrokClosedInput unit. Successful tools can retain their original completion and
independent cleanup while accounting remains absent under its existing gate.
Final checks on this integrated revision passed:

- Root `go test -race -p 2` across domain, Grok harness, Worker, server, CLI and
  store, selecting `Test(GrokPublic|GrokInitialPlanTerminal|GrokOriginalReply|GrokExtraReceipt|GrokResponses|GrokDecimal|GrokExact|PublicToolJournal|PublicNumeric|PublicFirstInput|GrokAccounting|NativeAccounting|MixedCodexGrok|CLIGrok|AccountingMigration)`
  with `-count=1`: all six packages passed. This includes original tools/Plan
  publication and response races, first-input subprocess fixtures, numeric and
  remembered-write boundaries, original reply/receipt separation, existing
  first-text accounting acceptance/exclusions and migration preservation.
- Root `go vet -p 1 ./cmds/delidev-cli/...`: passed.
- Root `pnpm proto:check`: passed lint, main-baseline breaking checks and forced
  reproducible generation without generated drift.
- Rebuilt API client, desktop typecheck, six focused frontend files and all 137
  tests: passed. Files cover native Grok interactions/text, execution
  configuration, negotiated Grok accounting, Usage and Inbox.
- Desktop production build: passed.

These focused integrated passes do not replace a passing full race/unit script,
and do not remove the broader failures recorded above.

## Acceptance limits

Read in this pinned native profile has an automatic permission cycle without a
client allow/deny request; no unsupported Read denial control is invented.
Initial/current native modes and original Plan artifact ownership remain
distinct. Native Plan uses its own approval transition without a synthetic common
gate. Tool terminals do not borrow first-text closed-history or continuation
authority. Richer Stop without its separately verified terminal retains recovery.

Repository execution, continuation, unverified families, real hosted-account
inference, other-platform native acceptance, releases and the full issue #964
completion boundary are not established by these controlled fixtures. No actual
installed Grok/provider acceptance is claimed from fixture subprocess results.
Generated repository-owned `dist` output is removed after validation and is never
tracked.
