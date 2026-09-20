# Repository Workflow Contract

Repository workflows are reviewed as source-backed contracts. Workflow IDs, job IDs, artifact names, permissions, triggers, protected environments, and publication boundaries must match the checked-in YAML and the static workflow tests.

## Continuous integration

`.github/workflows/CI.yml` is a read-only validation workflow. It uses `contents: read` and `pull-requests: read`, does not consume repository secrets, and must not push tags, create or upload releases, submit stores, push OCI images, deploy documentation or infrastructure, promote updater state, or call any mutating release-controller operation. Release workflows and packaging inputs are tested as source and deterministic fixtures only.

CI never builds a signed private candidate and never publishes.

The `async-commit-hook` CI job follows the central change plan and validates the Go runner with race detection, its app/client and generated protocol, release fixtures, and all six CGO-free target archives. Its path rule includes the owned source, installers, release workflow and packaging, documentation contracts, and shared Go/Node/protocol inputs; unrelated changes skip it. It uses the shared cache policy and participates in the exact `ci-result` inventory. Its temporary archives remain unsigned and unpublished. The separate manually dispatched `release-async-commit-hook.yml` owns signing, GitHub Release, Homebrew and Pages publication; its default dry run cannot enter publication jobs. See [the release contract](cmds-async-commit-hook-release-contract.md).

The always-running `changes` job computes the execution plan with `scripts/ci/plan.mjs` and `scripts/ci/job-paths.json`. Domain jobs depend on this plan and use job-level conditions, so unrelated jobs do not allocate runners. `ci-contracts` always validates the workflow and planner. Rules cover Go, Rust, every Node workspace, environment tooling, all implemented DevHud domains, packaging, public documentation, and private-package, public-release, and CEF-review workflows. Runmoor-only release scripts do not select DevHud native packaging.

PRs run affected validation, including the existing three-OS Go/environment checks and dual-architecture OCI checks, while deferring desktop and mobile packaging. Relevant main pushes run the unchanged ten desktop, three iOS, and four Android package matrix entries. Manual dispatch runs every check and native platform regardless of paths. Changes to `CI.yml`, `.github/actions/**`, or `scripts/ci/**` force all checks eligible for the event; they never enable native packaging on a PR. There is no nightly CI schedule.

PR comparisons use the merge-base of the event's base and head commits. Main pushes compare the exact `before..sha` trees, including multi-commit and force pushes. Git diff uses NUL-delimited names and disables rename detection so both old and new owners are selected. Invalid or unavailable revisions fail planning instead of producing an empty change set. Node workspace checks use the committed Turbo binary via `scripts/ci/run-affected.mjs` with the same `TURBO_SCM_BASE` and `TURBO_SCM_HEAD`. Forced checks and inputs outside the workspace dependency graph omit `--affected`; an otherwise empty affected set is a successful no-op.

Every pnpm install uses `--frozen-lockfile --ignore-scripts` and runs only once per selected Node workspace job. Local setup actions restore pnpm and Go caches scoped by OS, architecture, tool version, and lockfile. Explicit final save steps write only after successful main validation; Rust caches use the same main-only policy with failure caching disabled. Rustfmt has no dependency cache. PRs restore eligible main caches but never save branch-scoped caches, so dependency-update PRs may repeat downloads until merged. Native output caches and artifact retention policies are not expanded.

The PR frontend job runs the complete DevHud test command and `verify:pins`, retaining the script fixtures, deterministic clean desktop/mobile frontend builds, font/dependency isolation, static widget contracts, and immutable CEF checks previously available inside native packaging. The aggregate DevHud `test` task is non-cacheable because these checks exercise clean builds and external contract inputs.

Generated protocol output and package-local frontend output are deterministic cacheable Turbo products. The ignored administrator bundle, native host, desktop installer, mobile, smoke, signing, release, and deployment tasks are explicitly non-cacheable. CI validates schemas and generated freshness; Go formatting, vet, unit, PostgreSQL migration, integration, API, and sweeper behavior; Rust formatting, Clippy, unit, capture, shortcut, IPC, and updater behavior; frontend type, lint, unit, component, accessibility, build, security, and adapter fixtures; exact CEF pins and feasible native architecture builds; extension/native-host/installer packages; SPDX SBOM and provenance; non-root multi-architecture API and migration-bearing sweeper OCI layouts; public routes; and release workflow fixtures.

The `ci-result` job retains the `CI Result` name and always evaluates every required dependency. Planning and CI contracts must succeed. Every planned job must succeed and every unselected job must be skipped; failures, cancellations, missing jobs, invalid plans, and unexpected skips or execution fail the aggregate. The changes job publishes a structured decision log and a job-selection table in its run summary.

For performance comparisons, record workflow completion time, summed per-job execution minutes, selected native matrix entries, and repository cache bytes. Baseline run `35320469091` completed in approximately 48 minutes with 415.5 runner minutes: 266.2 desktop, 117.9 mobile, and 31.4 other. Deferring those 17 entries removes about 92% of that sample's PR runner usage before accounting for retained frontend fixtures and planning overhead; this is a counterfactual estimate, not a measured post-change improvement. Compare equivalent changes and cache states after hosted execution. Public-repository standard hosted compute is free; cache/storage and developer waiting time are separate measurements.

Run the repository CI contracts locally from a clean checkout with:

```sh
pnpm install --frozen-lockfile --ignore-scripts
pnpm ci:workflows
pnpm ci:contracts
pnpm ci:release-fixtures
```

Package-local commands are documented in the applicable workspace README. Use `pnpm ci:affected <task> --affected --filter <workspace> --dry=json` to inspect affected execution without downloading a different Turbo version.

DevHud private packaging is manual-only or explicitly called by `release-devhud.yml`; `plan-only` is credential-free and `signed-private` is protected. Its OCI jobs generate and validate the ignored administrator production bundle before host-side Go tests, while the Docker build independently generates that same contracted structure inside its clean build boundary before compiling API and sweeper from one tree. The monthly `devhud-cef-security-review.yml` workflow is manual or scheduled monthly, has read-only contents permission, and may upload only its bounded metadata report artifact. It must not mutate source, pins, lockfiles, releases, stores, registries, deployments, alerts, or updater state.

Changes to DevHud workflows or these contracts must update `docs/apps-devhud-operations-contract.md`, `docs/apps-devhud-support-contract.md`, `docs/project-devhud.md`, the relevant release contract text, `AGENTS.md`, and `scripts/release/devhud-operations.test.mjs` together. `CI.yml` runs the static release/operations contract suite without contacting a controller or publication service.

## Runmoor preview validation and release

`CI.yml` validates `apps/runmoor-docs` in `node-runmoor-docs-test`, using the shared change plan, one frozen install with `--ignore-scripts`, and the same exact Turbo comparison as other documentation jobs. The job is required by `ci-result` and only builds and validates documentation; it never deploys. Public Runmoor routes are owned by the standalone app, while `public-docs` validates their removal and links to `https://runmoor.delino.io`.

`.github/workflows/runmoor.yml` is a separate read-only `contents: read` validation workflow, gated to Runmoor source, release scripts/workflows, shared Go module changes, and its shared Go setup action. It runs race tests and explicitly enabled local Docker integration without GitHub credentials or job assignment. Its per-ref concurrency group cancels superseded runs; caches follow the successful-main-only save policy. Real Tart integration remains operator opt-in. Existing CI aggregation, application development commands and fixed ports are unchanged.

`.github/workflows/release-runmoor.yml` accepts exact `runmoor@v<MAJOR.MINOR.PATCH>` tags and manual version/dry-run dispatch. Source version and revision must match the release plan; publication permits main or the exact tag only. Plan, build and package jobs use read-only contents authority and produce the three native platform archives plus SHA256SUMS. Dry runs cannot request OIDC, sign, create tags/releases, or upload public assets. Only the explicitly guarded publish job obtains `contents: write` and `id-token: write`, rejects conflicting existing tags or existing public releases, signs the exact archives/checksum file, verifies the exact workflow certificate identity and issuer, and publishes a prerelease with overwrite disabled. No macOS/Xcode images or placeholder signatures are shipped.

Run `node --test scripts/release/runmoor.test.mjs` for deterministic archive, identity, checksum, signature-verifier-double and workflow isolation checks. Cross-build with `node scripts/release/runmoor.mjs build --version 0.1.0 --revision <40-hex-commit> --ref <git-ref> --mode dry-run --output <temporary-directory>`, then its `checksums` and `verify` commands. These commands do not need credentials or signing tools. Repository-wide release fixtures that use Debian packaging and GNU tar run on Linux. Follow `docs/cmds-runmoor-foundation.md` for runtime verification and preview limitations.

The three-OS Go matrix runs `go test -timeout=20m ./...`. Native Git/PowerShell and durable SQLite fixtures on hosted Windows outgrew Go's default 10-minute package budget. This bounded test watchdog does not introduce a timeout for ach commands or increase product concurrency limits. Bulk scheduler-only queue setup uses one durable transaction so it measures the worker scenario instead of thousands of independent disk flushes.
