# Account-switch PR maintenance on main d1f83cec

## Revisions and preceding observations

This pass started from published PR #1218 head
`d6d211596268c3eecef9d6c70eab2b695d2bf2eb` and merged freshly fetched main
`d1f83cecee4e0c50ea094335392cf68845f85739` on 2026-09-30.
Main adds Windows OpenCode global-root support (#1223) and original-process
OpenCode event-stream reconciliation (#1229). GitHub reported a conflict while
the preceding head's CI and Codex reviews were still pending. No passing new-head
CI, completed review or human approval is inferred from those observations.

## Conflict composition

Only `docs/cmds-delidev-harness-contract.md` conflicted. Preserve the complete
Codex portable full-history account boundary and the complete Windows OpenCode
General Chat root profile in separate sections. A direct comparison against
both merge parents verified that each section is retained verbatim. Main's
OpenCode event reconciliation, root isolation, checkpoint compatibility, scoped
instructions and independent issue #1204/#1205 evidence are retained.

The explicit account-switch gate remains Codex API-only. OpenCode root and
event-stream support does not widen account-switch eligibility or replace its
exact predecessor history, terminal-state and cleanup requirements. This repair
adds no protocol number, migration, product policy or generated-source edit.

## Executed verification

- From the repository root, `GOMAXPROCS=2 go test -race -p 1 -timeout 5m
  ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/apiproxy
  ./cmds/delidev-cli/internal/cli
  -run 'Test(AccountSwitch|StoppedAccountSwitch|HistoryObservation|SwitchedHistory|FullNativeHistory|CLISwitch|OpenCodeFirstDispatch)'
  -count=1` passed: server 61.748 seconds, apiproxy 1.449 seconds,
  CLI 1.729 seconds. This includes the account-switch regression group and
  deterministic first-dispatch checks for the imported OpenCode behavior.
- `GOMAXPROCS=2 go test -race -p 1 -timeout 5m
  ./cmds/delidev-cli/internal/harness/opencode
  ./cmds/delidev-cli/internal/worker
  -run '^Test(EventReconciliation|GlobalRoot|HistoricalV1Checkpoint|OpenCodeGeneralRoot|OpenCodeControlsJoinOnce|OpenCodeUnsupportedControl)'
  -count=1` passed: harness 8.050 seconds, Worker 2.263 seconds. These are
  deterministic fixtures, not installed Windows or hosted-account acceptance.
- `GOMAXPROCS=2 go vet ./cmds/delidev-cli/...` passed.
- `node --test scripts/ci/delidev-structure.test.mjs
  scripts/ci/delidev-proto.test.mjs scripts/ci/proto-breaking.test.mjs` passed
  all seven checks.
- Complete `pnpm proto:check` passed formatting/lint, breaking comparison and
  forced Turbo generation with no tracked or untracked generated-source drift.
- `pnpm --filter devhud-admin build:embedded` passed real embedded-asset
  preparation for the root Go formatting hook.
- `git diff --check` and `git diff --cached --check` passed after resolving
  the document content.

## Continuing limits

The merge has no source delta from the preceding published head in
`apps/delidev`, the DeliDev API client or DeliDev schemas/generated Go sources.
The complete frontend pass remains recorded at its executed source revision in
[the preceding maintenance evidence](maintenance-main-65eca334-2026-09-30.md).
It is not rerun or represented as a fresh frontend execution in this pass.

Earlier incomplete/failed broad local Go runs remain recorded independently.
Installed Codex A-to-B acceptance remains unverified after the two recorded
initialization failures; this pass does not repeat those unchanged attempts.
No installed-native, hosted-account, subscription, platform-release or billing
evidence is added. Repository-owned generated `dist` outputs are removed after
validation and commits. The final push requires new-head CI and review evidence.

## Committed-source result

Merge commit `6f756e5fa2d5a816f6e844d0a319563f34fb4b8c` has the two exact
parents recorded above. Root Lefthook Go formatting passed and the worktree was
clean after the merge. The final repair inventory contained no Codex review
threads, but the preceding head had developed a macOS Go failure and consequent
CI Result failure. The separate [CI investigation](macos-grok-probe-ci-2026-09-30.md)
records that failure and the bounded local repetitions without claiming CI repair.
