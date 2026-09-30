# Projects settings PR maintenance at 17:00 KST

Date: 2026-09-30. PR: https://github.com/delinoio/oss/pull/1191. Published head inspected: `36c130a33548615f21f4f300a2ef6668917d0452`. Merged base: `126641a6dcf274420c8c5800a6e88079f0e19990` (`origin/main`). The commit containing this record owns the tested conflict resolution.

## Conflict resolution

The three conflicts involved desktop/source ownership instructions and the shared configuration form. Preserve the Projects-only class alongside the new Agent Worker form class and its Agent-only invalid-control disclosure handler. Keep the Agent subtitle/footer, GitHub Integrations presentation and every existing scoped ownership requirement. Project fields, revision guards, exact retries and opening disposal are unchanged by this repair. No new contract or native geometry change is introduced.

## Validation

The required `pnpm test` from `apps/delidev` passed generated-client preparation, TypeScript checking, all 1,065 tests in 89 files, eight bundle dry-run tests, 16 asset/desktop-launch tests, widget fixture checks and the production Rsbuild build. The full run includes the Projects, Agent Worker and Integrations suites.

The run used temporary single-worker Vitest settings, 30-second test and 90-second default hook timeouts, plus `GOMAXPROCS=2`, retaining explicit test timeout overrides. The committed Vitest configuration was restored. Repository-owned generated `dist` directories were removed before committing, and `git diff --check` passed. These bounds are validation context, not product response-time guarantees.

No native CEF/platform smoke, keyboard-containment acceptance or actual 200% zoom run was performed. Earlier browser fixture evidence remains qualified in `projects-settings.md`; this merge validation adds no native or browser visual acceptance claim.

## GitHub observations

At the initial inventory, the PR remained open and conflicting. Its Cloudflare check passed for the inspected published head, and Codex code/security review was running. No unresolved non-outdated Codex review threads or failing checks were reported. A repair push invalidates that prior-head check and review evidence; maintenance must assess the new head separately.
