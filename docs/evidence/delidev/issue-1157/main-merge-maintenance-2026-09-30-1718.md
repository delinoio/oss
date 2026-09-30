# Projects settings PR maintenance at 17:18 KST

Date: 2026-09-30. PR: https://github.com/delinoio/oss/pull/1191. Published head inspected: `1bab062b908de62d95a90939a8fc8568056c77bb`. Merged base: `2b658e05353a858e328f82a636d8bc49dbccb709` (`origin/main`). The commit containing this record owns the tested conflict resolution.

## Resolution

Only the parent/source AGENTS instruction additions conflicted. Preserve the complete Projects-only requirements and the new home-only sidebar Inbox/Search visibility and focus-handoff requirements. The incoming frontend navigation and Windows CI-sharding implementation merged without manual code changes. No Projects implementation, contract, storage, request or native geometry change was needed.

## Validation

The required package-local `pnpm test` passed generated-client preparation, TypeScript checking, all 1,078 frontend tests in 89 files, eight bundle dry-run tests, 16 asset/desktop-launch tests, widget fixtures and the production Rsbuild build. `pnpm ci:contracts` at the repository root also passed all 111 tests, including the newly merged CI scheduling contracts.

Frontend validation used temporary single-worker Vitest settings, 30-second test and 90-second default hook timeouts, with `GOMAXPROCS=2`; explicit timeout overrides remain. The committed Vitest configuration was restored. `git diff --check` passed, and repository-owned generated `dist` directories were removed before committing.

This run provides component, server-fixture, build and CI-contract evidence. It adds no actual Windows shard-duration measurement, native CEF/platform acceptance, keyboard containment or actual 200% zoom evidence. Earlier visual/native qualifications in `projects-settings.md` remain.

## GitHub observations

Before repair, CI Result and Cloudflare Pages passed on the inspected head, and the Codex summary reported completed code/security review. The repair inventory found no unresolved non-outdated Codex threads or failing checks. The PR remained open and conflicted with the newer base. A repair push invalidates prior-head check and review evidence; the new head requires its own assessment.
