# Terminal review round six validation

This pass repairs PR #1226 from `964d2981e1e81f071707b01d204f0027d79a8d77`.
Current runtime source: `fa9eed2fa`; final fixture source: `c958dd177`.
The complete Go race command began before the final protobuf-clone-only fixture
correction; that correction separately passed its focused test and root vet.

## Independent repairs

- `97904a909`: missing shutdown observations conservatively mark possible output
  loss, preserving positive original pre-native/joined creation evidence.
- `6b223b7b3`: exact committed terminal report receipts can acknowledge lost
  responses after replacement/purge under current same-device/machine authority;
  absent receipts grant no new report/native work. Only closed ownership metadata
  survives receipt redaction; bounded journal scans advance through a backlog.
- `7fa457179`: input focus returns after pending input/resize controls, while
  metadata refresh does not steal focus or replay controls.
- `fa9eed2fa`: every public terminal Resource omits pending input bytes, while
  authenticated original Worker dispatch/claims retain exact input.
- `c958dd177`: clone protobuf fixture messages rather than copying message state.

Each repair has its own committed focused evidence record. Historical ledgers
and qualifications are preserved. No new protocol number or migration was added.

## Executed checks

- Focused shutdown/replacement/journal/retirement/output-loss race checks passed.
- Focused committed-receipt, ownership, purge allowlist and uncertain-close
  reconciliation race checks passed.
- New public-input projection RPC/server race checks passed; related server
  terminal/deletion race checks passed, 114.630s.
- API client build and all five test files / 46 tests passed.
- Desktop typecheck and all seven terminal component tests passed.
- `pnpm proto:check` passed lint, breaking and fresh-generation checks.
- `pnpm ci:contracts`: all 113 tests passed.
- Root `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` passed after the documented
  protobuf lock-copy fixture correction. The initial warnings remain recorded.
- Windows amd64 Worker test cross-compilation passed; no native Windows execution.
- Required LFS icon was hydrated; desktop asset preparation passed.
- Packaging dry-run tests: 8 passed. Launcher/assets: 16 passed. Swift widget
  fixtures and production build passed independently after the unit failure.
- Generated app/client `dist` output was removed with exact-path guards; a whole
  worktree scan found no repository-owned `dist` directories.

## Full suites and remaining limits

Required `GOMAXPROCS=2 pnpm test` in `apps/delidev` failed in its unit phase:
101 files, 27 failed / 74 passed; 1,286 tests, 111 failed / 1,171 passed / 4 skipped,
332.03s. Failures span App, Settings, subscription/device/controller/navigation,
PR and other component/Connect integration fixtures. Four integration setup
failures skipped their tests after the bounded Go build deadline. Fifty-nine
failure labels also appeared in the prior PR run; this is historical observation,
not proof of root cause or a controlled current-main comparison. No test deadlines,
assertions, cleanup gates or fixture restrictions were weakened.

The complete root command
`GOMAXPROCS=2 go test -race -p 2 -timeout=20m ./cmds/delidev-cli/...`
finished on 2026-10-01 UTC with exit 1: 18 packages passed (including cached
unchanged packages), six failed, and the root package had no tests.

| Failed package | Result |
| --- | --- |
| `internal/cli` | Failed, 389.019s |
| `internal/harness` | Failed, 227.770s |
| `internal/harness/codex` | Failed, 593.846s |
| `internal/harness/grok` | Failed, 1201.035s |
| `internal/server` | Failed, 1201.694s |
| `internal/workspace` | Failed, 1200.820s |

Grok, server and workspace each reached the unchanged 20-minute package
watchdog. Their currently running fixtures at the watchdog were respectively
`TestCreationRetainsOriginalClaimsAndNeverRepeatsUncertainty`,
`TestAccountSwitchCannotAlterSessionPendingPermanentDeletion` and
`TestPRStartupIdentityFailureRetainsCheckingAndBlocksNewAttempt`. These are
observations at timeout, not attribution of the total package duration or cause.

CLI pairing/repository inspection and session acceptance timed out; the pairing
fixture also reported that its Worker did not stop. Harness discovery failed
six classifications/isolation fixtures. Codex permission acceptance and uncertain
approval delivery failed. Workspace additionally failed
`TestWorkspaceDiffUnbornAndBoundedResults`. No controlled current-main comparison
or confirmed failure cause was established, and no native deadline, assertion,
cleanup gate or fixture restriction was weakened.

The full storage package passed in 829.853s and the full Worker package passed
in 538.989s. Claude passed in 501.461s, OpenCode in 74.389s and common RPC in
2.013s. The focused server terminal/deletion result remains separate from its
failed full package. Both broad-suite failures are retained; this record does
not claim complete local validation success.

Current-head CI and Codex review evidence before this pass belonged to the old
published `964d2981...` head; a new push invalidates it. Review/CI/merge readiness
remain separate. The revocation-cleanup authority decision is still unanswered;
its review thread remains unresolved. This pass changes no revoked-credential,
cross-device cleanup or original native-work authority.

Controlled fixture and cross-build results do not establish real-account,
physical remote Worker, native Windows/Linux, native desktop visual, no-echo shell
or release acceptance. Keep maintenance active, without merging or enabling
an automatic merge.
