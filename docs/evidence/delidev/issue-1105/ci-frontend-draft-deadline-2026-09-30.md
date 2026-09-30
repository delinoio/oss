# CI Settings draft lifecycle deadline repair

## Current-head failure

At the 2026-09-30 maintenance check, PR #1213 head
`276b659bc4ac3958a2736fda293bc61c3aa0d7d0` was open and mergeable. Its CI run
`36713480096`, job `109880710211` (DevHud Protocol and Client), failed in the
DeliDev frontend stage, after protocol checks and client checks succeeded.
The runner checked out GitHub's test merge
`ff9d1319af6afd3bb17aac337be51a8d71d64b8f`, combining that PR head with main
`d1f83cecee4e0c50ea094335392cf68845f85739`.

The frontend run passed 1,269 of 1,270 tests in 98 of 99 files. The remaining
`App.test.tsx` case, `discards notification and import drafts on close without
saving`, exceeded its existing five-second deadline, reporting 5,155 ms. Vitest
completed in 142.21 s with exit 1; the subsequent packaging/launcher/widget/build
stages of that command were not reached. This is a frontend fixture deadline,
not evidence of a protocol or generated-binding failure.

Linux Go and Go Quality checks passed at the initial inspection, while macOS and
the four Windows Go shards were still running. No review threads or submitted
reviews were present; Codex code and security reviews were running on this head
and its reaction was eyes. None of these pending observations proves approval
or full CI acceptance.

## Repair and retained coverage

The failed case combined two independent Settings draft families and their
separate close/reopen/navigation sequences under one test deadline. Each now
has a fresh fixture and its own complete lifecycle test. Notification coverage
still changes the preference draft, closes Settings, enters Repositories through
Pull requests, verifies the editor was discarded and verifies the original
preference on reopening. Import coverage still edits incomplete JSON, closes
Settings, enters Repositories through Pull requests, verifies that targeted
destination and verifies an empty import draft on reopening. Both independently
assert that configuration was not saved.

All original behavior assertions and both navigation paths remain covered.
No production code, per-test deadline, global timeout, skipped-test rule or
arbitrary wait was changed. Splitting independent families avoids accumulating
their unrelated navigation in one deadline; it does not establish the cause of
every earlier App deadline failure or the separate native Go failures.

## Local verification

The unmodified single-case control at head `276b659b` passed in 1,873 ms using
`pnpm exec vitest run src/App.test.tsx -t
'discards notification and import drafts on close without saving'
--reporter=verbose --maxWorkers=2` from `apps/delidev`, after the required API
client build. This establishes local behavior, not an explanation of runner
timing.

After the split, `pnpm exec vitest run src/App.test.tsx -t
'discards (a notification|an import) draft' --reporter=verbose --maxWorkers=2`
passed both cases: notification 1,213 ms, import 637 ms. The 43 other tests were
outside this focused command's selection and were subsequently included in the
full run.

The required unmodified `pnpm test` then completed with exit 0 on the same
worktree, with parent `276b659b` and `App.test.tsx` blob
`f8166a32e18c064122da37ae36c7f9930910dd03`. All 1,271 tests passed in 99 files
(Vitest 36.10 s), together with API client build, frontend type checking, eight
packaging fixtures, sixteen launcher/LFS fixtures, Swift widget fixtures and
production Rsbuild. No source edits occurred during these checks. Generated
frontend/client `dist` directories were removed afterwards, and
`git diff --check` passed.

## Limits

The CI exit-1 result above remains an actual failure on the old head; local
success is not a rerun or acceptance on that runner. The repair commit requires
fresh CI and Codex review. No full native Go suite was rerun, no native failure
cause was inferred, and all prior complete native and frontend results remain
in their independent records. This test-only repair changes no ownership,
policy, wire or database contract. `Closes #1105` remains in the existing PR;
maintenance continues without merging it or enabling auto-merge.
