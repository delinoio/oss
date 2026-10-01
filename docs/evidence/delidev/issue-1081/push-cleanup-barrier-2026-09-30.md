# Manual push proof: preceding native-owner cleanup

Date: 2026-09-30. Base: `5bd51fab`. Implementation revision: the enclosing commit.
Review: [join descendants before push verification](https://github.com/delinoio/oss/pull/1227#discussion_r4144945586),
thread `PRRT_kwDORRAKg86niwv9`.

The original ordering closed the native client/bridge, observed Git for push
proof, and only then reconciled native descendants in lease Close. Client exit
alone did not prove that surviving local helpers had stopped changing Git.

PRGitTool now retains its original lease and independently reconciles that
lease's original native process owner before its first push-proof observation.
The active claim and workspace lock remain held. Missing or changed process
evidence returns uncertain proof; final lease Close repeats the ownership check
before recording cleanup/releasing the lock. Native-client and bridge closure
still precede verification, and no new execution or push can be replayed.

`GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/workspace -run 'ExecutionLease|ExecutionWorkspaceClaim' -count=1 -timeout=10m`
passed in 3.142 seconds. The new regression proves that native reconciliation
keeps the claim active and preparation excluded, and refuses a missing original
process index. Existing lease checks include owned process cleanup and uncertain
cleanup preservation. Broader PR Git fixtures belong to the final repair
validation; this focused result alone is not live Codex/escaped-helper acceptance.

No protocol, migration, frontend or Rust source changed. The separate executable/
configuration verification race remains unresolved. Resolve only this handled
thread after the final repair push and its required focused validation succeed.
