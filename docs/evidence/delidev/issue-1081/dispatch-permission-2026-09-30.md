# Manual PR fix immutable write permission

Date: 2026-09-30. Base: `daa3213565c5884ec1fd0664382970cb21a06feb`.
Implementation revision: the enclosing commit of this record.
Review: [bound remediation job permission](https://github.com/delinoio/oss/pull/1227#discussion_r4144945644),
thread `PRRT_kwDORRAKg86niwwj`.

The immutable assignment validator now independently requires explicit
workspace-write or full-access permission for Codex Execute remediation. A queued
fix whose Agent becomes read-only or default before dispatch cannot acquire the
Git bridge merely because its initial request was accepted with write permission.
Ordinary assignments retain their existing permission behavior. Documented
full-access support is unchanged; this guard does not fix the separate native
Git verification-to-execution race.

`GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/domain -run 'PRFix' -count=1 -timeout=10m`
passed in 1.694 seconds. The new regression starts with a valid write assignment,
changes the Agent revision/permission and recomputes its exact configuration
digest. Workspace-write/full-access remain admissible; read-only/default fail
with Unsupported before native work. Removing remediation preserves ordinary
validation for all four permissions. The first fixture compile failed because
its permission type was misspelled; it was corrected to the existing
`PermissionMode` enum before the passing run.

Required embedded assets were regenerated for compilation/commit hooks. No
protocol, migration, frontend or Rust source changed. This fixture result grants
no live-account/native/platform acceptance or full-suite pass. The existing P1
scope decision remains pending; resolve only this handled thread after the final
repair push succeeds.
