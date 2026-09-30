# Issue #1093 replacement after the structural reset

Branch: `kdy1/issue-1093-native-compaction`. Inspected and freshly fetched base:
`ad0e3e9a2` (`origin/main`), 2026-09-30, on macOS arm64 with Go 1.26.8,
Node 24.20.0 and pnpm 10.26.2.

PR #1195 was closed without merge during the structural reset. Its original
implementation and review repairs are preserved in this replacement, including
the historical [validation record](native-context-manual-compaction.md). Historical
passes and acceptance gaps are not new validation of this branch.

The replacement preserves current main's permanent session deletion, failed Claude
Resume semantics, independent forwarding cleanup, service-specific protobuf owners
and compatibility exports. No existing shared protobuf member receives a new
number, and there is no SQL migration. The new capability enum is exclusive to
the session service. Generated sources were regenerated from the composed schema.

## Permanent deletion composition

Claimed compaction assignments contribute their original action UUID to the
immutable Worker deletion plan. The action runtime and retained checkpoint join
the original job/process/workspace ownership and share initial removal plus
completed-proof replay inventory. Unrelated checkpoints remain untouched, and
restored replacements remain pending. Existing plans omit the additive action
field and retain their original bytes/digests.

Authenticated server regression verifies that deletion keeps the claimed action
and owning Worker, requests cancellation, waits for positive cleanup and rejects
another compaction. Worker regression verifies actual action-runtime/checkpoint
removal, unrelated checkpoint preservation and restored-copy rejection.

## Executed verification

- `pnpm install --frozen-lockfile`: passed, including linked-worktree Lefthook
  installation. No dependency or lockfile change.
- `pnpm proto:lint` and `pnpm proto:breaking`: passed against the fetched main.
- `go vet ./cmds/delidev-cli/...`: passed.
- API-client typecheck and all 44 package tests: passed.
- Focused domain/Worker/CLI race checks passed. The initial server build found
  a test-only enum spelling error; it was corrected before the final run.
- Final server/Worker race run:
  `go test -race -p 2 ./cmds/delidev-cli/internal/server
  ./cmds/delidev-cli/internal/worker -run
  'Compaction|SessionDeletionCompletedProof' -count=1`: passed
  (178.019 s / 12.911 s).

The complete `GOMAXPROCS=2 go test -race -p 2 -timeout=20m
./cmds/delidev-cli/...` run and pinned-native acceptance attempt are still in
progress at this implementation checkpoint. Their final results will be retained
in a separate validation record. No full-suite or actual native acceptance pass
is claimed here.

All product fixtures use disposable state and controlled providers. No user
credential, real account, hosted inference, other-platform, release or complete
issue-964 acceptance is claimed. Frontend and Rust implementation are unchanged.
