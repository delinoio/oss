# Public Grok maintenance root validation, 2026-09-30

Issue: [#1091](https://github.com/delinoio/oss/issues/1091). PR:
[#1230](https://github.com/delinoio/oss/pull/1230). This new record preserves the
historical replacement and reconciliation evidence rather than rewriting it.

The first maintenance pass merged main `d1f83cecee4e0c50ea094335392cf68845f85739`
as `088ccf702c65c4537cf3d87baa094e0b5a16e97e`, preserving both Grok and OpenCode
contract/instruction blocks. It then repaired the independently observed Windows
server CI Plan locator failure in `7c7c43495`; that change and its focused final
checks are documented in [the foreign-locator record](maintenance-foreign-plan-locators-2026-09-30.md).

Root broad validation started on the merge before the path repair:

```sh
GOCACHE=/tmp/oss-1091-go-cache GOMAXPROCS=4 go test -race -p 2 -timeout=20m ./cmds/delidev-cli/...
```

The complete Grok and server test binaries were compiled before the path repair.
Their results cover that merged baseline; the separate final path-policy tests
cover the changed reducer. Scripted native fixtures used temporary private state
and no user credentials. Source fixture deadlines were unchanged. The broad
command was allowed to complete rather than interrupted for another main merge.

The broad command exited unsuccessfully. Twenty tested packages passed;
`internal/cli` and `internal/workspace` failed. Complete native Grok
(1,135.190 seconds), server (1,112.916 seconds), store (793.765 seconds) and Worker
(747.746 seconds) packages passed, along with the other native harnesses and
remaining tested packages. The complete workspace package failed in 720.333
seconds at `TestPRWorkspaceMatchPreservesMismatchesAndDistinguishesUnknownAccess`:
its final clean matching observation returned `recovery_required` rather than
matching evidence. No package hit the command's 20-minute ceiling.

After the path repair, the unchanged-deadline isolated race rerun of
`TestPRWorkspaceMatchPreservesMismatchesAndDistinguishesUnknownAccess` passed in
27.697 seconds. This rerun does not make the failed broad command a passing
result.

`TestCLISessionAcceptanceQueueAndArchive` failed in the broad run at a prepared
workspace Git-diff read during review-comment creation. Structured fixture logs
recorded `workspace_read_failed` with `recovery_required`, followed by joined
Worker/server shutdown. After the path repair, the unchanged-deadline isolated
race rerun also failed at a later workspace read before its stale-review
assertion (77.813 seconds for the package). These are not passing results and no
cause is asserted merely because the original-head macOS CI passed. Process
inspection observed several other concurrent native suites on this host; that
observation is not proof that host contention explains every failure.

Final root `go vet -p 2 ./cmds/delidev-cli/...` passed after the committed path
repair. Hook embedded administrator and async-commit-hook outputs were generated
explicitly and their builds passed. Repository-owned generated `dist`
directories were removed before completion; none are tracked. No Rust or frontend
source changed during this maintenance pass, so the earlier frontend checks and
limitations remain in their independent replacement record.

The original PR head's Go CI passed Ubuntu, macOS and Windows core/harness/Worker
shards; Windows server and the dependent CI result failed on the Plan locator
bug. This evidence does not transfer CI or review approval to the new head. New
head checks, real hosted-account/native-platform acceptance, continuation,
release and full issue #964 acceptance remain separate.

The repair pass's final inventory also found a newly arrived unresolved
[Codex security review](https://github.com/delinoio/oss/pull/1230#discussion_r4144405319)
(`PRRT_kwDORRAKg86nhbPq`) about provider-directed automatic Reads outside the
assigned workspace. This pass did not establish confinement, disprove the
finding, or resolve it. The registered maintenance must assess it separately;
passing protocol/lifecycle tests do not establish filesystem confinement. The
Windows path repair does not address this security finding. No review approval
or merge readiness is claimed.
