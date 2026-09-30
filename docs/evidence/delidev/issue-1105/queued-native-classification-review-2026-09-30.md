# Queued native classification review repair

## Finding and scope

Codex thread `PRRT_kwDORRAKg86nhvNl` on PR #1213 head
`276b659bc4ac3958a2736fda293bc61c3aa0d7d0` reported that the frontend accepted
a queued terminal-failure label after the referenced native check changed to
success. The finding is valid: provenance/App checks and aggregate priority
alone did not recompute an ordinary queued requirement's state or reason.
The finding arrived in the final repair inventory after the separately
committed frontend deadline repair `bd1b3e8a2b947c2b66c0ac0d590747a5285d28cd`.
It is handled in a distinct commit before the repair workflow's single push.

The frontend now independently recomputes every non-workflow ALLGREEN
requirement from the complete selected entry rollup, following the Go domain's
ordered classification. It binds exact context/native-requiredness, required
App, native CheckRun/status state, original Actions merge-group provenance,
duplicate same-App ambiguity and the complete ordered result-ID list. Missing
queue results remain Unknown with their original no-matching-result reason.
An explicit zero App remains unverified without borrowing a result. Existing
workflow recomputation and ordinary non-queue behavior are preserved.

A successful native check cannot retain a terminal-failure row or headline;
pending/future native states cannot borrow a prior failure; a matching result
cannot be omitted or reordered to manufacture an assessment. The scoped
frontend instructions and integration contract record this validation
obligation. This read-side validation grants no handling or execution authority
and adds no protocol, database, native or GitHub mutation behavior.

## Verification

On parent `bd1b3e8a`, with decoder blob
`6b306f6c5dd92053cd4673436fe798c1e525bb34` and regression blob
`625fcc2ae700d301fff6bd428f6e96b43ed0ce8c`:

- The API client build and frontend type checking passed.
- From `apps/delidev`, `pnpm exec vitest run src/github-ci.test.tsx
  src/github-ci-profiles.test.tsx src/github-workflows.test.tsx
  src/pr-problems.test.tsx --maxWorkers=2` passed all 32 tests in four files.
  Regressions exercise stale labels on native success/neutral/skipped, pending
  and unknown states, duplicate App evidence and exact order, commit statuses
  with and without an App restriction, missing/optional/wrong-App/zero-App
  results and unverified Actions events, alongside mixed workflow/queue and
  retained historical evidence.
- The required unmodified `pnpm test` completed with exit 0: all 1,275 tests
  passed in 99 files (Vitest 27.98 s), plus client build/type checking, eight
  packaging fixtures, sixteen launcher/LFS fixtures, Swift widget fixtures and
  production Rsbuild. The earlier 1,271-test full pass belongs to the preceding
  deadline-only commit and is retained in its independent record.
- `git diff --check` passed. Generated frontend and client `dist` directories
  were removed after verification. No source changed during the recorded runs.

The final inventory on old remote head `276b659b` reported this one unresolved
Codex thread and two failed CI checks. The aggregate job `109884726269` explicitly
reported `devhud-protocol: expected success, got failure`; it is dependent on
the frontend deadline failure recorded in `ci-frontend-draft-deadline-2026-09-30.md`,
not a separate repair cause. No extra final-status polling is performed in this
one-shot repair. The addressed review thread is resolved only after pushing the
two separate repair commits successfully.

## Limits

Fresh CI and Codex acceptance on the pushed head remain pending for the next
scheduled maintenance pass. Local fixture/build success is not runner or native
product/platform acceptance. No full native Go run was started or repeated;
the earlier complete failures and qualifications remain in their original
records. The existing PR and `Closes #1105` are retained; maintenance does not
merge the PR, enable auto-merge or create another PR or automation.
