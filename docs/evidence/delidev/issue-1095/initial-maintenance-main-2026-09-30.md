# Issue #1095: initial PR maintenance reconciliation

This independent record covers the initial maintenance pass for
[PR #1233](https://github.com/delinoio/oss/pull/1233), following the replacement
implementation evidence in `replacement-2026-09-30.md`. Historical evidence is
preserved.

## Reconciled revisions

- Published feature head: `66417f1c69970692041f4a30503d6a500040973a`.
- Fetched and merged `main`: `d1f83cecee4e0c50ea094335392cf68845f85739`.
- The five documentation/instruction conflicts retain both the managed Codex
  subscription requirements and the incoming OpenCode, workflow-proof, and
  protocol-reservation requirements. No protocol numbers or migration versions
  were reassigned.
- Generated bindings were regenerated from the reconciled sources without drift.

## Focused validation before the merge commit

On macOS arm64, using Go 1.26.8 and bounded `GOMAXPROCS=4`:

```sh
go test -race -p1 -timeout15m \
  ./cmds/delidev-cli/internal/server \
  ./cmds/delidev-cli/internal/worker \
  ./cmds/delidev-cli/internal/harness/codex \
  ./cmds/delidev-cli/internal/harness/opencode \
  ./cmds/delidev-cli/internal/integrations/github \
  ./cmds/delidev-cli/internal/domain \
  -run 'Subscription|Managed|Bundle|SessionDeletion|PermanentDeletion|OpenCode|Reconciliation|WorkspaceRoot|GlobalRoot|PinnedWorkflow' \
  -count1
```

All six packages passed. Server and Worker checks include the managed lease and
subscription lifecycle fixtures; the additional packages cover the overlapping
incoming OpenCode and pinned-workflow changes. This is a focused run, not a
complete package-suite pass.

`pnpm proto:generate`, the six allocation/structure tests in
`scripts/ci/delidev-proto.test.mjs` and
`scripts/ci/delidev-structure.test.mjs`, and `git diff --check` passed. Embedded
DevHud administrator and async-commit-hook assets were generated for the root
Go formatting hook; these ignored build outputs are not distribution evidence.

## Limits

The full-race failures and the narrowly proven baseline CLI failure recorded in
`replacement-2026-09-30.md` remain visible. This merge validation does not
supersede them. It adds no real OAuth, user-credential, inference, native-platform
release, or provider-revocation evidence. Issue #964's complete desktop scope
remains separate.
