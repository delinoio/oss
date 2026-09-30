# Issue #1137 Settings merge maintenance — 2026-09-30

This pass begins at PR head `dcc0709e1c35decdc71526cc2c838ec7b3fc50b8`
and merges main revision `82020a7ef2916534f3342aa5208f43d2a051b052`.

Conflicts are limited to app ownership instructions and the Settings props /
Agent Worker row boundary. Both ownership policies are retained. Main's Agent
Worker row, Projects grouping, AI Subscription presentation and their complete
contracts remain intact; the issue #1137 connectionSettings prop, closed Doctor
title and separately persistent native Connection panel remain composed with
Settings. Settings-opening-local disposal continues to govern the newly merged
category workflows, while the native Connection controller remains owned by its
existing persistent sibling boundary.

API client generation succeeded. Focused Vitest verification used one worker
and a 15-second per-test bound across settings, doctor, desktop, configuration
integration and workspace integration suites. All 93 tests across five files
passed. Full frontend and startup regression results will be recorded below.

There were no unresolved Codex threads or failing checks at the initial PR
inventory. The four previously handled startup findings retain their individual
commits and evidence in `review-maintenance-2026-09-30.md`. Existing unrelated
broad-suite failures and actual native UI/platform/service acceptance gaps remain
visible in that record and the prior implementation/main-merge records.

## Completed validation

Validation source is merge commit `9430f44f832c30f59a1c96e98c3827410c804d4d`;
its second parent is the exact main revision recorded above.

- `VITEST_MAX_WORKERS=1 pnpm test` in `apps/delidev` passed API-client generation,
  TypeScript checking, all 1,176 tests across 92 files, packaging dry-run checks,
  launcher/assets checks, widget fixture/build checks and the production build.
- `go test -race -p 2 ./cmds/delidev-cli/internal/cli
  ./cmds/delidev-cli/internal/userservice -run
  'TestDesktopLaunch|TestDesktopPairing|TestLaunchAdmission|TestAutomaticStartup|TestStartup|TestDetachedStartupReadiness|TestCLILocalClientPairing|TestDesktopRecovery'
  -count=1` passed both packages (90.963s and 3.431s), retaining all four prior
  startup-review repairs on the new main source.
- The merge changes no Rust source. Prior native library, real-sidecar fixture
  and CEF compilation evidence remains recorded separately; this pass does not
  promote component/build results to actual rendered native acceptance.
- Repository-owned generated `dist` output was removed after validation. Prior
  unrelated broad-suite failures and native UI/platform/service gaps are retained.

No new product policy or ownership boundary is introduced by this composition:
existing category-opening disposal and the persistent native Connection-panel
exception remain governed by their owning instructions and contracts.
