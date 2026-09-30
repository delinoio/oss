# DeliDev conflict reset investigation and rollout

## Scope and evidence

The owner approved exactly the 22 PRs in `pr-snapshot.json`, in `delinoio/oss`.
Baseline: `12b33a2accafe55b39da9bae2fba1acf310b265b` (2026-09-30).
The snapshot preserves each URL, title, head SHA, branch, linked issue, changed files,
main conflicts, and the 55 pairwise comparisons of individually mergeable heads.
It is an immutable closure allowlist; later PRs are excluded.

Eleven heads conflicted with main. Of the remaining eleven, 50 of 55 pairs conflicted;
47 pairs conflicted in the shared evidence ledger and 22 pairs conflicted in code.
All 22 edited that ledger. Twenty-one edited the project index, nineteen the CLI
parent instructions, sixteen the app parent instructions, fourteen the central
Settings integration suite, and thirteen the shared protocol/generated surfaces.

`identifier-collisions.json` records conflicting original assignments and their
replacement reservations. `protos/delidev/allocations.json` retains every main
assignment plus 36 pending member reservations allocated in ascending PR order.
No reserved feature is advertised by the server. Three different branches used DB
version 25; the separate migration ledger reserves 25/26/27 without executing them.

## Content preservation

`document-relocations.json` records 800 source blocks with baseline source lines,
destination lines at this refactor, and SHA-256 hashes. The opt-in verbatim audit
passed after relocation. Future legitimate policy edits need not retain old hashes.

The original evidence ledger remains an exact 849,413-byte suffix after its archival
notice, with SHA-256 `2dffb289e9859905c5c93dbce68e611f61c69088da6c84cb563d8c2077a7514c`.
Original headings and anchors are retained. New evidence is issue-local; adding a run
alone requires no project-index or ancestor-instruction change.

## Replacement workflow

1. Branch from current main after the structural PR has merged. Use the preserved
   PR diff and linked issue as requirements/evidence, not as a branch to merge wholesale.
2. Put feature evidence under its linked `docs/evidence/delidev/issue-<number>/`
   directory. Update a domain contract or scoped AGENTS only when its contract or policy
   changes. Keep the project index for ownership, links, and cross-domain invariants.
3. Use the service-specific proto and feature-specific Settings integration file.
   Retain explicit capability detection; reservations do not imply implementation.
   Reconcile source definitions, then regenerate bindings and compatibility facades.
4. Adopt the reserved numeric assignment even when the old branch used another value.
   Establish new shared allocations on main before parallel branches use them.
5. Implement DB changes in the reserved order: #1108, #1115, then #1117. The two
   accounting changes must compose shared tables and usage attribution. A changed
   product order requires a reviewed reservation update before implementation, not
   no-op migrations. Never open old branch version-25 state as if its number identified
   its layout; retain the original and follow explicit recovery.
6. Review genuine dependencies: account switching/subscriptions and proxy authority;
   session fork/deletion/terminal/subagent ownership and cancellation; workspace snapshots
   and backup restore; accounting and diagnostic attribution; PR CI evaluation and
   unified activity. Independent files cannot make contradictory semantics independent.

## Local validation

- `pnpm proto:check`: passed, including unchanged FILE policy against explicitly
  relocated baseline declarations and reproducible generated output.
- Descriptor graph comparison: all 320 original declarations retain their contents.
  Added tests prove FILE still rejects field type changes and removals after relocation.
- API client build/typecheck and tests: passed (43 tests), including legacy generated
  module and Connect Query imports.
- DeliDev frontend `pnpm test`: passed, 84 Vitest files / 960 tests plus preparation,
  native-contract/widget fixtures and production web build. This is fixture evidence,
  not new native-device or installed-harness acceptance.
- Storage tests: passed, including all 27 frozen layouts, fresh/upgrade DDL equality,
  existing backfills, backup/rollback failures and byte-preserved unknown v25 rejection.
- CLI/server tests: passed after dispatch/authentication/lifecycle separation.
- `pnpm ci:contracts`: passed; final count is recorded in the rollout validation update.
- Full Go tests, race, vet and hosted PR checks are required before rollout; their final
  results are recorded separately. Rust implementation was not modified.

`merge-experiment.json` records the disposable two-branch experiment. Provider and
schedule feature branches each add separate evidence, a service RPC, integration
coverage, and generated Go/TypeScript bindings. Their changed-file sets do not overlap;
Git merge succeeds, Buf lint passes, and regeneration after merge is clean. Reproduce
with `node scripts/delidev/verify-independent-changes.mjs`.

## Rollout boundary

GitHub rulesets, required CI checks, and merge-queue policy are unchanged. Merge the
structural PR only after existing checks and review requirements pass. Verify main,
then re-read and close only still-open PRs in the frozen allowlist. Preserve branches
and linked issues. Record the resulting main commit and closure results separately.
