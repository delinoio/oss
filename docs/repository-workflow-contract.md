# Repository Workflow Contract

Repository workflows are reviewed as source-backed contracts. Workflow IDs, job IDs, artifact names, permissions, triggers, protected environments, and publication boundaries must match the checked-in YAML and the static workflow tests.

## Continuous integration

`.github/workflows/CI.yml` is a read-only validation workflow. It uses `contents: read` and `pull-requests: read`, does not consume repository secrets, and must not push tags, create or upload releases, submit stores, push OCI images, deploy documentation or infrastructure, promote updater state, or call any mutating release-controller operation. Release workflows and packaging inputs are tested as source and deterministic fixtures only.

CI never builds a signed private candidate and never publishes.

The always-running `changes` job computes the execution plan with `scripts/ci/plan.mjs` and `scripts/ci/job-paths.json`. Domain jobs depend on this plan and use job-level conditions, so unrelated jobs do not allocate runners. `ci-contracts` always validates the workflow and planner. Rules cover Go, Rust, every Node workspace, environment tooling, all implemented DevHud domains, packaging, public documentation, and private-package, public-release, and CEF-review workflows. Runmoor-only release scripts do not select DevHud native packaging.

PRs run affected validation, including the existing three-OS Go/environment checks and dual-architecture OCI checks, while deferring desktop and mobile packaging. Relevant main pushes run the unchanged ten desktop, three iOS, and four Android package matrix entries. Manual dispatch runs every check and native platform regardless of paths. Changes to `CI.yml`, `.github/actions/**`, or `scripts/ci/**` force all checks eligible for the event; they never enable native packaging on a PR. There is no nightly CI schedule.

PR comparisons use the merge-base of the event's base and head commits. Main pushes compare the exact `before..sha` trees, including multi-commit and force pushes. Git diff uses NUL-delimited names and disables rename detection so both old and new owners are selected. Invalid or unavailable revisions fail planning instead of producing an empty change set. Node workspace checks use the committed Turbo binary via `scripts/ci/run-affected.mjs`, invoking its installed Node entry point with the current Node executable instead of spawning a platform-specific package-manager shim or shell. The runner preserves literal task/filter arguments and the same `TURBO_SCM_BASE` and `TURBO_SCM_HEAD`. Forced checks and inputs outside the workspace dependency graph omit `--affected`; an otherwise empty affected set is a successful no-op.

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

## Selected CLI release and bot ownership

`release-project.yml` is the manual `Release Project` entrypoint. Its required choices are `project` (`binpm`, `cargo-mono`, `nodeup`, `with-watch`, `derun`, `runmoor`, `clibox`) and `bump` (`patch`, `minor`, `major`, default `patch`). It accepts only `main` in `delinoio/oss`. There is no main-push workspace publisher: Rust libraries remain available through explicit local `cargo mono publish` usage, but are not published by this workflow. DevHud and documentation deployment remain separate.

The private organization-owned GitHub App `delino-release-bot` has only Contents write and implicit Metadata read, no webhook subscriptions or user authorization, and selected installation access to `oss` and `homebrew-tap`. Both the organization and repository main rulesets must allow this app to bypass directly; other rules and actors are preserved. `DELINO_RELEASE_BOT_CLIENT_ID` is an Actions variable and `DELINO_RELEASE_BOT_PRIVATE_KEY` is an Actions secret in `oss`. Neither private keys nor installation tokens belong in files, artifacts, logs, Git URLs, or Git configuration. `actions/create-github-app-token@v3` obtains a fresh repository-scoped token immediately before each write phase and revokes it afterward. The app has no Actions or Administration write permission. CI/run inspection and same-repository release uploads use the built-in token. Registry uploads retain `CARGO_REGISTRY_TOKEN`.

The release workflow serializes its runs without canceling an active release. GitHub's default concurrency queue retains one pending run; a newer pending request replaces an older pending request. Each new run fetches current main, validates exact stable SemVer, and commits only the selected version source plus its Rust lock entry; clibox also updates its private npm source manifest in the same version-only commit. Rust manifests and lock entries must agree. Runmoor uses its existing `Version` constant; Derun uses `cmds/derun/internal/version/version.go`, also consumed by MCP server metadata. Minor and major bumps reset their lower components. Runmoor uses the stable release channel independently of the bump level.

The bot commit journals the repository/run ID, project, bump, and previous version. A rerun finds and validates that exact commit and its complete parent-to-child version-only diff instead of bumping again, even after main advances. Pushes are fast-forward only; conflicts fail without rebasing or force. After the exact commit's main-push `CI.yml` run and `CI Result` job succeed, Rust targets publish with `cargo mono publish --package <project>`. Only then does CI push the single `<project>@v<version>` tag at the recorded commit with the bot token. Go releases use the same path without a registry upload. Already-published crates can resume a missing remote tag, but a conflicting existing tag is always rejected. Tags are never moved or deleted for recovery.

The tag triggers the existing project release workflow asynchronously, preserving its signing identity, platform matrix, assets, and Homebrew behavior. Homebrew writes use a fresh tap-only app token and the bot commit identity. The coordinator waits only for the exact main-push CI Result before registry publication and the single tag push; it does not poll or fail on downstream release completion. There is no implicit redispatch, tag replacement, or public asset overwrite by the coordinator. Failed CI leaves the version commit in main without publishing; resume the same run after repairing/rerunning its exact validation, or start an intentional new version release. A failed downstream tag workflow is rerun from its own Actions page and does not require retrying the coordinator when the tag push succeeded. Every run reports version, commit/tag identity, and coordinator phase results; downstream status belongs to the tag-triggered workflow.

Source version tests must not pin the current Runmoor or Derun release literal. Release tests use temporary Git repositories and injected API responses, never real tag, registry, or deployment mutations. Validate with `pnpm ci:workflows`, `pnpm ci:contracts`, and the project release tests. Initial app setup and validation do not dispatch a release.

## Runmoor validation and release

`CI.yml` validates `apps/runmoor-docs` in `node-runmoor-docs-test`, using the shared change plan, one frozen install with `--ignore-scripts`, and the same exact Turbo comparison as other documentation jobs. The job is required by `ci-result` and only builds and validates documentation; it never deploys. Public Runmoor routes are owned by the standalone app, while `public-docs` validates their removal and links to `https://runmoor.delino.io`.

`.github/workflows/runmoor.yml` is a separate read-only `contents: read` validation workflow, gated to Runmoor source, release scripts/workflows, shared Go module changes, and its shared Go setup action. It runs race tests and explicitly enabled local Docker integration without GitHub credentials or job assignment. Its per-ref concurrency group cancels superseded runs; caches follow the successful-main-only save policy. Real Tart integration remains operator opt-in. Existing CI aggregation, application development commands and fixed ports are unchanged.

`.github/workflows/release-runmoor.yml` accepts exact `runmoor@v<MAJOR.MINOR.PATCH>` tags and manual version/dry-run dispatch. Source version and revision must match the release plan; publication permits main or the exact tag only. Plan, build and package jobs use read-only contents authority and produce the three native platform archives plus SHA256SUMS. Dry runs cannot request OIDC, sign, create tags/releases, or upload public assets. Only the explicitly guarded publish job obtains `contents: write` and `id-token: write`, inspects any existing stable draft by exact revision, rejects unexpected or mismatched existing assets, downloads and cryptographically verifies a complete signed candidate and reuses those signature bundles when the deterministic unsigned artifacts match, or completes a positively owned partial draft with the missing assets. It signs the exact archives/checksum file only when reuse is not possible, verifies the exact workflow certificate identity and issuer, rejects conflicting existing tags or existing public releases, validates the complete signed asset names, sizes and SHA-256 digests after draft completion, reapplies the committed canonical release notes immediately before publication, and then publishes a stable release with overwrite disabled. No macOS/Xcode images or placeholder signatures are shipped.

Run `node --test scripts/release/runmoor.test.mjs` for deterministic archive, identity, checksum, signature-verifier-double and workflow isolation checks. Cross-build with `node scripts/release/runmoor.mjs build --version 0.1.0 --revision <40-hex-commit> --ref <git-ref> --mode dry-run --output <temporary-directory>`, then its `checksums` and `verify` commands. These commands do not need credentials or signing tools. Repository-wide release fixtures that use Debian packaging and GNU tar run on Linux. Follow `docs/cmds-runmoor-foundation.md` for runtime verification and compatibility limits.

## Runlens independent validation and release

`crates/runlens` owns its independent lockfile and nightly-2026-08-02 toolchain.
The root workspace excludes it; `lefthook.yml` runs its product formatter with an
explicit hook root, alongside the existing root formatter. Its native workflow
`.github/workflows/runlens.yml` runs product fmt/Clippy, tests, release compilation,
archive construction, and installed native execution on six native OS/architecture
runners. Linux tests include a real static child fixture. No cross compile result
counts as platform evidence. Dedicated `runlens-macos-13`,
`runlens-windows-10-22h2`, and `runlens-ubuntu-22.04` self-hosted labels, paired with
X64 or ARM64, select the minimum-OS dispatch matrix. The workflow records actual
OS identity and never substitutes a runner label for evidence.
Both PR and main-push path filters include the prebuilt formula template
`packaging/homebrew/templates/runlens.rb.tmpl` and its shared renderer
`scripts/release/update-homebrew.sh`, as well as Runlens source and release inputs.

`scripts/release/runlens.py` owns the six-archive inventory, reproducible archive
headers, license aggregation, SHA256 manifest, native evidence, and prebuilt tap
rendering. `.github/workflows/release-runlens.yml` is manually dispatched. The
default dry run has read-only permissions, never signs or accesses publication
credentials, builds native archives, and records blockers without claiming release
readiness. Publication requires its exact `runlens@vX.Y.Z` tag and a successful
manual native-validation workflow at the same commit, with every archive digest
bound to execution and minimum-OS evidence. A missing platform or OS proof prevents
the protected `runlens-release` signing environment from being reached.
The validation job has only `contents: read` and `actions: read`; the latter is
required to inspect and download evidence from the explicitly selected native
workflow run. It has no publication, signing, or secret authority.

Only the signing job receives OIDC write permission. Its keyless Sigstore identity
is the release workflow at the exact tag; checksum manifests and each archive are
signed separately. All six minimum-OS runners then install authenticated archives
with the production installers. Only after those jobs pass does the publication
job receive contents-write permission and the tap credential. It creates or resumes
a draft bound to the exact release tag and commit, fills missing assets without
replacing existing ones, verifies uploaded sizes and SHA-256 digests, and then
updates the Homebrew tap before making the verified draft public. Tap failures
leave the release private; the final publication step requires successful tap
completion and rechecks the draft's identity, exact asset names, uploaded state,
sizes, and SHA-256 digests. It re-downloads and authenticates every retained
Sigstore bundle against the exact local payload/tag, re-lists the inventory after
authentication to reject replaced asset IDs, then checks identity again before
publication. This final gate never repairs or overwrites assets. The verification and publication requests are separate, so this gate minimizes
the interval and refuses detected concurrent edits; release writers must remain
exclusive through the final publication request. Published releases, drafts for other
commits, unexpected assets, and mismatched payloads are refused. A retry authenticates
retained Sigstore bundles against the exact payload and tag, preserving valid
original signatures even when a new signing attempt produces different bundle bytes.
It resumes interrupted uploads and idempotently repeats a completed tap update
after a final-publication failure; invalid retained assets require operator inspection.
Homebrew uses the existing `delinoio/homebrew-tap` prebuilt formula path.

The six native installer fixtures deliberately replace only cosign authentication
with a failing/succeeding double to test install rejection, tampering, atomic
replacement, and real execution from the installed binary without public release
side effects. POSIX installers reject symlink, directory, and other non-regular
destinations before staging and recheck before replacement; rejected destinations
retain their original contents. They are explicitly not signed-install evidence. The subsequent
protected release installation matrix uses real cosign verification. PR work does
not create release tags, releases, tap commits, or documentation deployments.

## clibox Cargo and npm distribution

`clibox` is a selected Rust release target. Its Cargo manifest, Cargo.lock entry, private npm source manifest, executable, and generated packages must agree on the exact version. The coordinator publishes crates.io only after exact-commit main CI succeeds, then pushes `clibox@v<version>` without waiting for npm. It does not publish npm packages itself.

`CI.yml` runs `node-clibox-test` on Linux, macOS, and Windows, with one frozen script-disabled workspace install, the shared affected planner/Turbo comparison, package fixtures, and temporary npm/pnpm consumer installs. Each OS also exercises the affected runner against temporary workspaces, including empty selection, forced execution, and failure propagation. External Cargo and release inputs force this workspace's checks. The job participates in `CI Result`. Native integration tasks are not cached; successful-main-only dependency cache rules remain unchanged.

`release-clibox.yml` builds eight native binaries (macOS and Windows MSVC x64/arm64; Linux glibc/musl x64/arm64). GNU builds use Ubuntu 22.04 x64 and Ubuntu 24.04 arm64 runners; the corresponding glibc baselines are 2.35 and 2.39. Both musl binaries use the pinned Rust toolchain's `rust-lld` and self-contained runtime objects, execute on the native build hosts, and run npm/pnpm consumer smoke tests in Alpine. Windows uses the MSVC toolchain. macOS binaries use the deployment baseline of the repository-pinned Rust target. Each build checks executable version and creates a platform npm tarball. The assembly job adds the launcher, verifies exactly nine source-commit/version-bound tarballs, and uploads `clibox-npm-<version>-<revision>` with 30-day retention. Native intermediate artifacts are named `clibox-native-<suffix>`. There is no Homebrew or public GitHub Release asset publication.

Manual dispatch defaults to dry-run and may validate a development ref. Non-dry-run publication requires the exact first-party version tag. Build/assembly/dry-run jobs have read-only contents permissions and no signing or registry credentials. Only the guarded npm publish job has `id-token: write`; it requires `CLIBOX_NPM_PUBLISH_ENABLED=true`, a matching already-published crates.io version, and the exact tag/source commit. It uses Node.js 24, explicitly installs pinned npm 11.6.2 before validating the 11.5.1+ OIDC requirement, and publishes with OIDC and provenance. All platform packages are published and their npm registry integrity confirmed before the launcher. Existing identical tarballs are reused; any conflicting remote integrity fails before new writes. Rerun only failed jobs after partial publication so the original complete artifact is reused. Expired/missing artifacts require maintainer recovery; never overwrite an npm version or move its source tag.

All nine packages require a Trusted Publisher permitting publication from `delinoio/oss` and `release-clibox.yml`. Setting `CLIBOX_NPM_PUBLISH_ENABLED=false` or leaving it unset disables publication while retaining validated artifacts. Publisher configuration and retry requirements are in [the npm distribution contract](packages-clibox-distribution-contract.md). No setup or validation command dispatches a workflow or publishes a registry version.

Runlens native validation reads the checked-out Cargo package version with the release helper once and passes that validated stable version to both packaging and native evidence. The workflow and release fixtures do not pin artifact commands to the initial 0.1.0 version.
