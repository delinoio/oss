# PR #1193 picker and primary-checkout review repair

Recorded on 2026-09-30 on macOS arm64 after explicitly invoking the repair-pr workflow for [PR #1193](https://github.com/delinoio/oss/pull/1193). This is independent of the [implementation record](repository-folder-registration.md) and [first maintenance repair](pr-1193-first-repair.md); their source-backed outcomes and qualifications remain unchanged.

## Problems and revisions

The starting head was `9527dd371de7c5764f3db1ca1602a4eb3a4cb7de`. Main advanced to `faa7fbee6`, adding GitHub Integrations presentation and Windows Go CI sharding. Merge commit `9322a02b86bbbed47165ba200d71d8b4530d6328` preserves both repository-registration and GitHub-profile style blocks, including each independent responsive-media closure. No shared Settings controller was replaced.

[Picker-stage feedback](https://github.com/delinoio/oss/pull/1193#discussion_r4142296899) correctly identified that native `busy` and `invalid-evidence` failures were presented as failed Worker verification before any path was accepted. Commit `2757c78669390962567f93f54a9bbd57872aabce` separates picker and Worker stages, gives wait/reselect/access/retry guidance, and preserves existing confirmation/options when a replacement picker fails. Tests assert that picker failures initiate no proof, status, inspection or save request, make no newly-retained-folder claim, and allow retry.

[Primary-checkout feedback](https://github.com/delinoio/oss/pull/1193#discussion_r4142296916) correctly identified that additional-checkout editing could remove the confirmed primary checkout while retaining its inferred metadata. Commit `dfe950083f9d9b99d0ab7acc64beb81e53cf44d3` binds readiness to the exact inspected machine/canonical root and prevents removing that checkout during creation, with Change folder guidance. Tests cover retaining/removing an additional checkout, exact saved checkout/metadata bytes and unchanged removal behavior in existing repository editing. The source-scoped AGENTS.md and desktop contract document both repaired invariants.

## Executed verification

- Merge verification: `pnpm --filter @delinoio/delidev-api-client build`; then from `apps/delidev`, `pnpm exec vitest run src/repository-registration.test.tsx src/integrations.test.tsx --maxWorkers=1 --testTimeout=15000`, `pnpm typecheck` and `pnpm build` passed. The two suites passed 39 tests.
- Picker repair: registration tests with one worker and a 15-second bound passed 22 tests; frontend typecheck passed before the separate commit.
- Primary-checkout repair: registration and Settings tests with the same bound passed 51 tests; frontend typecheck passed before the separate commit.
- Required full frontend command from `apps/delidev`: `GOMODCACHE=/private/tmp/issue-1142-go-mod GOCACHE=/private/tmp/issue-1142-go-build GOMAXPROCS=2 GOFLAGS=-p=2 VITEST_MAX_WORKERS=2 pnpm test` passed completely against `dfe950083f9d9b99d0ab7acc64beb81e53cf44d3`: 89 files / 1,074 tests, 8 package dry-run tests, 16 desktop-launch/asset tests, widget fixture checks and production build. The command retained its normal five-second unit-test timeout. These isolated disposable Go caches avoid the shared-cache failures recorded previously.
- `pnpm ci:contracts` passed 111 tests and `pnpm ci:workflows` passed after the main merge. `git diff --check` passed. Generated repository-owned dist directories were removed after verification; no generated binaries were added.

## Limits and publication

The current full frontend success does not rewrite the earlier failed broad runs. Required root Rust validation previously failed on unchanged binpm temporary-path and clibox DNS-classification assertions. This repair introduces no Rust source changes and claims no new root Rust success. Prior focused Rust/CEF compile and protocol-binding evidence remains qualified in the historical records.

Native picker/trusted-main/saved/foreign-window authorization, keyboard/focus and 960×640/200% zoom acceptance remain unverified independently on macOS, Windows and Linux. Component, compile, widget and package-fixture checks do not establish those real runtime outcomes. No real account, publication or OS acceptance is inferred.

Before publication, GitHub reported only a passing Cloudflare Pages check for the prior head and no failing checks; complete Actions CI evidence was unavailable. The repair workflow pushes once after all local commits, preserves Closes #1142, and resolves only the two repaired Codex threads after push succeeds. Fresh CI/review results belong to the published head and subsequent scheduled maintenance; the PR is never merged or set to auto-merge by this workflow.
