# PR #1193 first maintenance repair

Recorded on 2026-09-30 on macOS arm64 after explicitly invoking the repair-pr workflow. This is independent of the original [implementation record](repository-folder-registration.md), which describes its earlier source revisions and validation limits.

## Problems and changes

The initial PR head `263dad6a0639f36f837ab7cfece32165bf5f6593` failed CI's immutable protocol allocation check. Worker capability 3 was already reserved for managed Codex subscriptions and 4 for session terminals in main's allocation ledger; neither reservation advertises implementation. The historical issue proposal's value 3 therefore could not be reused. Metadata now uses the first unreserved value, 5, with the ledger, schema, generated bindings and owning protocol rules updated together. Attachment-response field 3 remains unchanged. No main-established or reserved meaning was renumbered.

The base advanced to `8a626cc8a`, producing a Settings import conflict. Merge commit `25c293747` retains both the repository-registration adapters and the newly merged Agent validation import. Protocol repair commit `3d0a57f726ab643a4f43c32e3e2b07695ba96dfb` contains the separate allocation fix.

## Executed verification

- Generated client build and frontend typecheck passed after the merge. Registration, Settings and real Worker integration passed 3 files / 44 tests with one test worker and a 15-second bound.
- `pnpm ci:contracts` passed all 102 tests, including the previously failing immutable allocation/reservation check. `pnpm ci:workflows` passed.
- `pnpm proto:generate`, `pnpm proto:lint`, `pnpm proto:breaking` and post-commit `pnpm proto:fresh` passed with isolated Go caches. Bindings reproduce from source without drift.
- The focused domain/CLI/server/Worker/workspace Go command from the original record passed against the merged schema and capability 5. Real CLI/Worker/Git observations, capability echo, null/map validation and atomic repository publication remain covered.
- Required `GOMODCACHE=/private/tmp/issue-1142-go-mod GOCACHE=/private/tmp/issue-1142-go-build GOMAXPROCS=2 GOFLAGS=-p=2 VITEST_MAX_WORKERS=2 pnpm test` was executed from `apps/delidev`: 87 files passed, 2 failed; 1045 tests passed, 4 failed. The failures were five-second timeouts in two existing App tests and two newly merged Agent disposal tests. Isolated App and Agent runs with one worker and a 15-second bound passed all 37 and 19 tests respectively. The broad command remains failed; focused success does not replace that outcome.
- The merged production frontend build passed. This repair changes protocol allocation, generated bindings, contracts and import composition, with no new native Rust source changes. Earlier root Rust failures, native compile/library outcomes and all native picker/authorization/focus/geometry gaps remain as recorded in the original evidence; no new platform acceptance is claimed.
- All generated repository-owned dist output was removed after verification. The issue worktree is committed independently of the user's pre-existing main-checkout changes. Repairs are pushed once after local validation; GitHub CI/review evidence must be assessed for that new head.
