# ALLGREEN and pinned workflow main reconciliation

## Source and scope

On 2026-09-30, PR #1213 at `14010a90fb0e99503031649c1ccfde75a299d87e`
reported new merge conflicts. This repair merges main
`d1f83cecee4e0c50ea094335392cf68845f85739`, including pinned required workflows
from #1219, Activity presentation, Runner Device terminology, shared protocol
reservations and OpenCode changes. It preserves both parents and the existing
ALLGREEN and Grok accounting ownership. No branch, issue, PR, automation, runtime
migration or protocol declaration was created for this repair.

The four textual conflicts were in the frontend instructions, CI decoder, Go CI
model and integration contract. Resolution preserves both additive evidence
families and the incoming Activity instructions. ALLGREEN still evaluates only
the original entry head with complete queue membership and original
`merge_group` provenance. Pinned workflows still evaluate only the independently
verified PR test merge; a queued pinned requirement remains Unknown without
erasing ordinary entry status-check failures or borrowing test-merge jobs.

Semantic reconciliation also preserves the final CI read after the final rules
read. It rechecks any retained optional workflow proof there; a complete changed
inventory conflicts, while optional unavailability drops the whole workflow
family and still requires identical complete ordinary evidence. The existing
per-enrichment deadline and joined cancellation remain in effect. Frontend
headline validation recognizes the supported queue rule only with evaluable
entry evidence and rejects an unexplained Unknown source for that entry.

## Focused validation before the merge commit

These checks used the resolved worktree with the two parents above and the
reconciliation changes; the final merge commit records that source tree.

- `GOMAXPROCS=2 go test -race -p=1 -timeout=3m
  ./cmds/delidev-cli/internal/domain
  ./cmds/delidev-cli/internal/integrations/github` passed: domain initially
  1.972 s and reused unchanged on the final attempt; the complete GitHub adapter
  package passed in 10.766 s. Coverage includes current test-merge workflow
  outcomes, final source drift, final optional deadline/join behavior, ALLGREEN
  queue brackets and mixed queue/workflow requirements.
- The first Go attempt failed because the incoming workflow fixture moved the
  CheckRun to the test merge while leaving its nested original workflow suite
  commit at the PR head. The fixture now supplies both original commit fields.
  A second attempt exposed the new final-source-drift fixture's overly narrow
  source-read assertion; that negative fixture now accepts its deliberately
  different immutable source SHA. The final complete package run passed without
  weakening production provenance or timeouts.
- `pnpm --filter @delinoio/delidev-api-client build` and `pnpm typecheck` passed.
  From `apps/delidev`, `pnpm exec vitest run src/github-ci.test.tsx
  src/github-ci-profiles.test.tsx src/github-workflows.test.tsx
  src/pr-problems.test.tsx --maxWorkers=2` passed all 28 tests in four files.
- `node scripts/delidev/verify-independent-changes.mjs` passed and reported
  reproducible generated bindings with no shared changed files.
- `node --test scripts/ci/delidev-structure.test.mjs
  scripts/ci/delidev-proto.test.mjs` passed all six tests, including the new
  reservations already established on main. `git diff --check` passed.

## Limits and maintenance state

These are focused merge checks, not full desktop/native acceptance. Further
required validation is recorded separately against the committed merge tree.
The earlier full Go and frontend failures remain in their original independent
records; this repair does not replace them with a passing full-suite claim or
rerun the full native Go suite.

At the start of this pass, head `14010a90` had only a successful Cloudflare Pages
check in the check inventory and no unresolved Codex review threads. The Codex
summary reported code review completed and security review still running; it
did not establish acceptance. These observations apply to that old head only.
The eventual pushed merge requires fresh CI and review evidence on a later
scheduled pass. PR #1213 remains open, with `Closes #1105` preserved; merging and
auto-merge remain outside this maintenance task.
