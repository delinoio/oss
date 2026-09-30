# Manual PR Git: local command ownership

Date: 2026-09-30. Base: `05cb28ab184a54a499d26eebdd41159a79e78c6a`.
Implementation revision: the enclosing commit. Review:
[retain command ownership through the local child](https://github.com/delinoio/oss/pull/1227#discussion_r4144945624),
thread `PRRT_kwDORRAKg86niwwW`.

The bridge previously released its shared command lock after local command
validation, before the sandboxed launcher ran the child. A parallel push could
therefore claim an intermediate HEAD while commit, merge or rebase was active.
The bridge now streams a fresh bounded grant and keeps that request and lock
alive until authenticated completion after child exit. Completion has separate
admission capacity. Foreign or duplicate grants cannot release an owner, and a
missing completion poisons the capability instead of granting another command
or reporting clean bridge closure. The launcher does not retry a lost completion.

The new Unix fixture uses test-owned Git repositories and a pinned wrapper that
holds an actual commit child at a controlled barrier. It proves that a forged
completion is rejected, a parallel push cannot create its one-shot claim,
completion permits exactly the subsequent push of the committed HEAD, and a
launcher disappearing after a grant leaves later commands and closure uncertain.
This proves ordering of supplied local calls. It does not prove independent
descendant cleanup, live Codex sandbox acceptance, Windows native behavior, or
immutable executable/configuration boundaries.

Final focused verification for the three separate review repairs:

- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/workspace -run 'PRGitTool|PRGitBridge|PRLocalGit|ExecutionLease' -count=1 -timeout=15m`
  passed in 132.821 seconds, including the new command-ownership fixture and
  preceding native-owner cleanup barrier.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/worker -run 'ManualPRFixRPC|PRFix|ExecutionCheckpointRequiresExactAssignmentAndConfirmedCleanup' -count=1 -timeout=10m`
  passed: server 5.501 seconds, domain 1.440 seconds, Worker 1.727 seconds.
- `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...` passed.
- `GOMAXPROCS=2 go build -p 1 -o /tmp/delidev-1227-third-cli ./cmds/delidev-cli`
  passed.

Required embedded assets were explicitly generated with
`pnpm --filter @delinoio/devhud-api-client build`,
`pnpm --filter devhud-admin build` and
`pnpm --filter async-commit-hook build:embedded` before Go validation and hooks.
Generated repository-owned `dist` directories must be removed before finishing;
dependency caches are retained. No frontend, Rust, public protocol or migration
source changed in these three fixes. Earlier full local-suite failures and
real-environment evidence limits remain recorded independently; these focused
passes do not establish a full local-suite pass.

The permission and cleanup findings have separate implementation/evidence commits
`5bd51fab101db6f361431c7cd837fa2d7cb82a4d` and
`05cb28ab184a54a499d26eebdd41159a79e78c6a`. The original native Git executable/
configuration verification-to-execution P1 remains unresolved, pending the
previously requested profile/execution-boundary decision. This handshake does
not fix that race. CI success on prior pushed head `daa32135` is historical;
new-head CI and review approval are not established by this record.
