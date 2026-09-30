# Validation of the pinned workflow and ALLGREEN merge

## Immutable source

The checks below completed against merge commit
`8ccae6fa077a9a7ada95c03b411e6ab5c68d8358`, whose parents are PR head
`14010a90fb0e99503031649c1ccfde75a299d87e` and main
`d1f83cecee4e0c50ea094335392cf68845f85739`. No source changes occurred during
these checks. The subsequent evidence-only commit records these results without
changing the tested runtime source. Focused conflict-resolution checks and the
two corrected fixture attempts are recorded in
`main-workflow-reconciliation-2026-09-30.md`.

## Completed checks

From `apps/delidev`, the required unmodified `pnpm test` command completed with
exit 0. Every stage in its standard chain passed:

- Generated API client build and frontend type checking.
- Vitest: 99 files and all 1,270 tests passed in 40.09 s.
- Native packaging dry-run fixtures: all eight passed.
- Desktop launcher and LFS preparation fixtures: all sixteen passed.
- Swift widget fixtures: exact values, currencies, isolation, stale/closure,
  masking, corruption and private storage passed.
- Production Rsbuild completed successfully.

These are one complete current frontend run, not a union of passing subsets.
The earlier seven-failure and one-deadline full attempts remain preserved in
`merge-validation-and-ci-2026-09-30.md`; the current pass does not erase them or
establish their causes.

From the repository root:

- `GOMAXPROCS=2 go vet ./cmds/delidev-cli/...` completed with exit 0.
- `pnpm proto:check` completed with exit 0: formatting/lint, breaking comparison
  and forced generation with no tracked or untracked generated-binding drift.
  Incoming shared reservations are retained from main; this repair introduces
  no new wire declaration or runtime migration.
- `GOMAXPROCS=2 go test -race -p=1 -timeout=3m
  ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli
  -run 'RepositoryQuery|PRProblemsRPC|PRProblemCollectionFamilies|CLIPRProblem|CLIRemediation'`
  completed with exit 0. The server package passed in 4.191 s and the CLI package
  passed in 1.902 s. This covers authenticated
  repository read lifetime and original problem/remediation identities.
- `git diff --check` passed. Generated `apps/delidev/dist` and
  `packages/delidev-api-client/dist` were removed after validation; no generated
  `dist` content is tracked.

The complete domain and GitHub adapter race packages, four focused frontend
files, independent-change verification and six structure/protocol contract
checks also passed on the source tree recorded by the merge commit, as detailed
in the separate reconciliation record.

## Limits

No full native Go suite was started or repeated. The original completed exit-1
race run is already retained in `original-full-go-race-complete.md`: 15 packages
passed, seven failed, two had no tests and five reached the 20-minute deadline;
its source checkout changed while packages were pending. These results do not
validate this immutable merge tree. The earlier macOS local-review Git-launch
failure and isolated storage-setup failure remain unexplained; neither is
claimed fixed by this merge or the current frontend pass.

The new workflow and queue provenance behavior is verified with controlled
fixtures. Real queued-account, full native product/platform, imported OpenCode
native behavior, installed package and release acceptance were not performed in
this maintenance pass. Frontend packaging/widget fixtures remain distinct from
that acceptance. The pushed head still requires fresh GitHub CI and Codex review;
old-head deployment success or completed code-review processing is not current
acceptance. PR #1213 stays open with `Closes #1105`; maintenance never merges it
or enables auto-merge.
