# PR #1169 Claude child-usage source repair

## Original failure

On 2026-09-30, PR head `69a21ac3ea079d86a4bbc67b7d3f46b3037449c2` failed both [Linux Go CI](https://github.com/delinoio/oss/actions/runs/36681808975/job/109778782900) and [macOS Go CI](https://github.com/delinoio/oss/actions/runs/36681808975/job/109778782889) in `TestClaudeUsageRejectsForeignOrUnpublishedSources/child`. The root usage adapter's unconditional child-tag bypass returned no error for an originally published root report whose parent-tool field was subsequently forged. These two failures have one implementation root cause. The existing rejection test was retained unchanged.

## Repair

The root adapter bypasses publication only after the exact original child-content receipt is acknowledged. It independently matches original session/input/turn and acceptance, retained native event/source, parent tool, observed model and exact validated usage. A foreign child tag or changed acknowledged report blocks the publisher without another usage publication. Legitimate forwarded child usage remains in the child observation and does not enter the root billing ledger.

The new regression rejects changed session, input, turn, acceptance, event, parent, model and usage after an actual child-content publication. The existing positive child test still confirms that the runner's subsequent root-adapter visit adds no usage publication. The owning Worker rule and subagent contract were updated with this exact boundary.

## Executed validation

- `GOMAXPROCS=4 go test -race ./cmds/delidev-cli/internal/worker -run 'TestClaudeUsage|TestSubagentClaude' -count=1`: passed on the repair source, including the previously failing unchanged root-source test, exact root receipt/usage tests and positive/negative child tests.
- `GOMAXPROCS=4 go vet ./cmds/delidev-cli/...`: passed on the repair source.
- Root-hook embed prerequisites `pnpm --filter devhud-admin build:embedded` and `pnpm --filter async-commit-hook build:embedded`: passed. Normal commit hooks remain enabled.
- `git diff --check`: passed.

The already-started aggregate local race run remains separately tracked and has earlier CLI, discovery, Claude and Codex fixture failures. It is not a passing validation of this repair. [Implementation evidence](implementation.md) retains the executed protocol/client/frontend/build results and failed aggregate frontend runs. No frontend, protocol, dependency or Rust source changed in this repair, and no real-account/native-platform or release acceptance is claimed. Fresh CI and review results for the pushed repair must be evaluated independently of the original head.
