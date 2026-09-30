# Main restore reconciliation, 2026-10-01

Issue: [#1091](https://github.com/delinoio/oss/issues/1091). PR:
[#1230](https://github.com/delinoio/oss/pull/1230). The date uses Asia/Seoul;
the initiating heartbeat was `2026-09-30T14:55:40.466Z`.

Final protocol breaking validation on the two local review repairs at
`78affe3e5` failed against newly advanced main because the branch lacked the
managed database restore declarations merged in PR #1222. This was an actual
baseline mismatch; no older baseline was substituted. Merge main
`9efb1917e0127a9223cee0969877238ab37c0e1d` into the issue branch without rebasing.
Keep the original proposal-byte and exact request-ID fixes in their separate
commits, preserve all historical evidence and retain the main restore
implementation, reservations, schema and generated declarations.

The only textual conflicts were the server and store `AGENTS.md` files.
Both original Grok ownership/publication rules and main's managed restore,
lifecycle, actor, erasure and recovery rules are retained. Automatic documentation
merges retain both project cross-domain invariants and both protocol contracts.
Bindings were regenerated from the reconciled schemas without hand edits and
matched the merge index exactly.

The earlier root race invocation on `78affe3e5` was deliberately interrupted
before source reconciliation (exit 143). Its only completed package result was
cached apiproxy success; it does not establish full-suite validation. Its owned
Go/test processes terminated before the merge, and the process supervisor
exited. Other worktrees' processes were left untouched. A final root run on the
reconciled source and its actual outcome are recorded separately.

Focused verification uses private SQLite/native fixtures without user credentials
or inference, `GOCACHE=/tmp/oss-1091-go-cache` and `GOMAXPROCS=4`:

```sh
go test -race -p 2 -timeout=20m ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/harness/grok ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/cli -run 'Test(PublicProposal|GrokInteractionPreservesExact|GrokPublic|BackupRestore|Restore|StatusPreservesForwardingAndRestore|ConcurrentBackupRestores|BackupRestoreCLI)'
```

All five packages passed: domain 1.787 seconds, Grok harness 2.582 seconds,
server 173.259 seconds, store 241.891 seconds and CLI 10.712 seconds. These
include original-byte digest and exact-ID admission, positive native response
paths, restore/account-switch quarantine and original actor/current deletion
checks. Protocol generation, formatting/lint, breaking and generated-source
freshness passed after the merge. API-client tests passed all five files and
46 tests. The full desktop `pnpm test` passed, including type checking, unit
fixtures, bundle/launch/assets fixtures, widget checks and production build.
The tracked desktop icon was hydrated before this validation.

The previous automatic outside-workspace Read finding remains unresolved pending
the previously requested policy decision. Neither the two review fixes nor this
base reconciliation establishes filesystem confinement, real-account/platform
acceptance or approval. New-head CI and Codex review results remain separate.
Generated repository-owned `dist` directories are removed after final tests and
normal commit hooks; none are tracked.
