# Issue #1088 replacement: current-main integration

## Reconciled sources

The terminal/deletion integration commit is
`a35c81771adb68200fd961f05c1ab9799a09df4c`. It was merged with main at
`574c1a92c957fc741a723ff8123888dad32a2194`, including verified Grok accounting
and shared compaction reservations, in
`97719d1031a4699437b3d68005995cabea190697`.

Both native-accounting capability 4 and terminal capability 14 remain advertised.
The service-specific schemas regenerated the Go and TypeScript bindings; no
serialized generated merge side was retained. Main's accounting migration 25
and its independent layout marker remain unchanged. Terminals require no new
migration. Compaction reservations remain planned and do not activate dispatch.

Historical evidence and the preceding replacement-deletion record retain their
original results and qualifications.

## Completed verification

- Regeneration: `pnpm proto:generate` passed.
- Repository contracts: `pnpm ci:contracts` passed all 113 tests.
- Desktop: regenerated the required client dist, then `pnpm typecheck` passed;
  focused terminal and Grok accounting tests passed, two files / seven tests.
- Native macOS arm64: the original descendant-close fixture passed without
  changes to production cleanup or test deadlines (1.29 seconds). A subsequent
  race-enabled process/workspace terminal pass also passed: process 10.315
  seconds; workspace 76.597 seconds, covering General Chat, Local and
  multi-repository Worktree original-primary selection and replaced-root refusal.
  These reruns do not erase the earlier failures or establish their root cause.
- The prior cross-compilation run finished successfully for Linux amd64/arm64
  and Windows amd64/arm64, CLI plus process-test binaries. Windows arm64 completed
  after the preceding evidence commit. No foreign binary was executed; this
  does not establish native Windows/Linux acceptance.
- The combined race-enabled store terminal/deletion/accounting selection passed
  in 146.057 seconds. Its server portion remains in progress at this record.
- `git diff --check` passed.

## Unsuccessful or pending verification

An initial desktop typecheck after removal of generated dist failed with missing
client imports. Building the client explicitly corrected this setup failure;
final typecheck and focused tests passed as recorded above.

The first merged-source API-client suite passed its 41 unit tests but its three
Connect integration tests were skipped when the temporary Go server build hit
its 120-second limit. The suite failed. A bounded Go-parallelism rerun is pending.

Root Go vet, the combined focused server pass, full desktop `pnpm test` with
`VITEST_MAX_WORKERS=1`, and post-commit `pnpm proto:check` are pending at this
record. Earlier broad Go/frontend failures and interruptions remain documented
in `replacement-deletion-integration-2026-09-30.md`; this record does not claim
passing complete suites. Later results must be recorded independently.

Real-account, physical remote-Worker, native Windows/Linux, native desktop visual
and release acceptance remain unperformed. The desktop is a bounded text/control
view without full-screen VT emulation. Generated repository-owned dist directories
must be removed after consumers finish and before final delivery.
