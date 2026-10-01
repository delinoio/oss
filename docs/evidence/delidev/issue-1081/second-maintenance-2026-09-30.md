# Manual PR fixes: second maintenance reconciliation

PR: [#1227](https://github.com/delinoio/oss/pull/1227). Date: 2026-09-30.
This record belongs to its enclosing merge commit, whose parents are the prior
manual-fix head `1a54c184e91db7eb92a9ded33afa17314976f4a0` and main
`d1f83cecee4e0c50ea094335392cf68845f85739`.

## Reconciliation

GitHub reported new conflicts after main merged Windows OpenCode global-root
support (#1223) and original OpenCode event-stream reconciliation (#1229).
The three conflicts were in server/Worker instructions and the session contract.
Retained both independent sets of requirements without removing manual-fix
preflight, sandbox/process ownership or workspace identity rules.

The automatic code merge removes only main's obsolete Windows OpenCode General
Chat exclusion from the shared selection function. Existing account, native
profile, workspace and routing checks remain, together with this branch's PR-fix
assignment/preflight gates. Main's private native root/checkpoint and original
event reconciliation changes remain intact. No protocol allocation, generated
binding or migration changed in this reconciliation.

## Executed verification

- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/harness/opencode ./cmds/delidev-cli/internal/worker -run 'ManualPRFixRPC|PRFix|OpenCodeFirstDispatch|EventReconciliation|GlobalRoot|HistoricalV1' -count=1 -timeout=10m`:
  server, domain and OpenCode passed (13.591, 1.423 and 6.915 seconds). The Worker
  selection compiled but reported no tests to run; it is not Worker test coverage.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/worker -run 'OpenCodeGeneralRoot|OpenCodeControls|OpenCodeUnsupported' -count=1 -timeout=10m`:
  passed the independently selected Worker regressions (1.489 seconds).
- `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...`: passed.
- `GOMAXPROCS=2 go build -p 1 -o /tmp/delidev-1227-second-maintenance-cli ./cmds/delidev-cli`:
  passed; the executable remains outside the repository.
- Required embedded assets were explicitly rebuilt before compilation/commit
  hooks. Their repository-owned generated `dist` directories are removed after
  the commit; dependency directories are preserved.
- `git diff --check`: passed after composing the three conflict regions.

These are focused macOS fixture/build results. They do not establish installed
Windows root behavior, native stream-loss behavior on every platform, a signed
release, live GitHub fix execution or a complete Go suite pass. The prior broad
Go/frontend failures remain in the independent validation records; no frontend
or Rust source changed in this repair, and those suites were not repeated here.

## Review and CI limits

The initial maintenance inventory found no failed check and only the existing
unresolved Codex P1 `PRRT_kwDORRAKg86ng9G8`. Only Cloudflare Pages was reported
as a completed successful check on the prior head; this is not proof of complete
CI or approval. The Git executable/configuration verification-to-execution race
remains unresolved, and the previously requested profile-scope decision has not
arrived. No repeat scope mutation, thread resolution or approval is claimed.
Any repair push invalidates prior-head check evidence. This PR remains blocked
on that native execution boundary; subsequent CI/review observation belongs to
the next scheduled maintenance pass.
