# Issue #1105 current-main merge maintenance

## Source and resolution

Merged main `7090de04621ece95b3c2cce8d88fcfcdeadad7cc` into PR #1213's
`kdy1/issue-1105-stable-allgreen` branch, whose prior head was
`193f9c0f82247efec970830ff189af1d71efa701`. Main adds the separately owned
verified Grok accounting implementation from PR #1211.

The three conflicts were additions at the ends of the frontend, domain and
store `AGENTS.md` files. Both parents' independent policies are retained.
A mechanical comparison verified every nonempty instruction line from both
parents remains present. The automatically merged desktop contract preserves
both queue-CI presentation and native-accounting presentation. Reconciled
schemas were regenerated; no generated-file conflict was resolved by choosing
a side. ALLGREEN collection, history and remediation behavior remains scoped
to its existing complete evidence and fresh authorization contracts.

## Executed verification

On the resolved tree, macOS arm64:

- `git diff --check`: passed.
- `go test -race -p=2 ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/integrations/github ./cmds/delidev-cli/internal/store -run 'ALLGREEN|QueueCI|CIQuery|RequiredCI|HistoricalQueue|CIProblem|PRCI|Remediation|Accounting|Migration|Grok'`:
  passed (domain 1.548s, GitHub adapter 2.252s, store 317.383s).
- `pnpm proto:check`: passed formatting/lint, schema breaking comparison against
  main, and forced regeneration/freshness for both language bindings.
- `pnpm test` from `apps/delidev`: failed in Vitest, 90 files and 1,240 tests
  passed; six files and seven tests failed. A two-worker verification and its
  actual result will be retained independently. This attempt did not reach
  packaging, widget and production-build stages. Concurrent frontend work in
  a different checkout and the original Go run were observed; that does not
  prove the cause of every failure.

## CI and native-run limits

The pre-push head's macOS Go job
<https://github.com/delinoio/oss/actions/runs/36700067003/job/109837825931>
failed `TestLocalReviewSubmissionRejectsConcurrentCommentEdit` during workspace
preparation (`local_reviews_test.go:163`, `unavailable: Git could not be launched
on this Worker`). The entire server package failed in 111.149s. The fixture,
workspace Git path and process-launch implementation are unchanged from the
prior main base `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`. The job log does not
retain the underlying native launch failure, so this observation establishes
neither its root cause nor an environmental explanation. No speculative native
launch change or CI-success claim is made.

The original full Go race command remains active and has already reported
CLI, Codex, Grok and OpenCode failures. It was started before this merge; the
checkout changed while later packages were still pending. Its eventual results
cannot establish a complete run against either immutable parent or this merged
tree. They must be retained with this provenance limit in a separate record.
No second full native suite was launched. Earlier baseline-control evidence
applies only to its documented CLI fixture and cannot explain other failures.
New pushed commits require fresh CI and Codex review evidence.
