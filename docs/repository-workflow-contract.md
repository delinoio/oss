# Repository Workflow Contract

## GitHub issue language

Write GitHub issue titles and explanatory body text in English, including new
issues and later edits. This rule applies to every project in this repository.

Preserve exact code, commands, paths, diagnostic messages, search queries, direct
quotations and localized UI strings when their original text is required. Explain
non-English evidence in English so readers can understand it without translating
the surrounding prose.

When translating an existing issue, preserve its requirements, conditions,
uncertainty, validation status, references, attachments and open or closed state.
Translate the prose without treating the translation as implementation or feature
acceptance. Changes to issue content are made on GitHub; the repository PR records
the language policy and the translation scope.

## DeliDev known model catalog

`.github/workflows/delidev-known-models.yml` runs daily at 04:17 Asia/Seoul
(`17 19 * * *` UTC) and supports manual dispatch on main in `delinoio/oss`.
Its public-source collector and fixtures validate all three service inventories
before issuing any write token. Reuse the existing release-bot GitHub App, scoped
to `oss` with contents and pull-requests write permissions only. Checkout keeps no
credential. Raw upstream responses and secret values are excluded from logs.

Use one bot branch and one open review PR. Catalog/date/source-byte-only equality
creates no PR. A candidate model or related metadata change records additions,
changes, removals, source revision/digest, validation commands/results and account,
native/platform limits in the PR and CI summary. An unchanged source revision is
required at publication. Every branch-only commit must belong to the bot and every
changed path to the catalog. A human edit or incomplete ownership inspection stops
publication. Exact old-ref force-with-lease protects against a concurrent human
push; credentials exist only in ephemeral Git configuration environment. Existing
PRs are edited, and no automatic merge or automatic publication retry is enabled.
Runs also inspect the bot branch and open review PR when the candidate already
matches main, closing a stale review instead of leaving obsolete catalog data
open. Immediately before a candidate commit, the publisher re-fetches every
recorded source and compares its exact URL, revision and digest; any source or
main revision drift aborts publication and requires a new collection.
Only a person-merged main catalog is consumed by installed servers. GitHub schedule
delivery is best effort; manual dispatch can recover a missed run. A real workflow
run and App permission acceptance are separate from local fixture validation.


DeliDev source decomposition, independent evidence and stable numeric reservations
follow [the structure contract](cmds-delidev-structure-contract.md). These are source
ownership changes; existing required checks and repository rulesets remain intact.

Repository workflows are reviewed as source-backed contracts. Workflow IDs, job IDs, artifact names, permissions, triggers, protected environments, and publication boundaries must match the checked-in YAML and the static workflow tests.

DeliDev's manual `delidev-native-dry-run.yml` is a credential-free six-target native
packaging workflow governed by `docs/apps-delidev-packaging-contract.md`. It has
read-only repository access and uploads revision-bound verified workflow artifacts
only; it does not sign with production keys, notarize, publish releases or install
updates. Native runtime acceptance remains separate from package verification.

The optional boolean `workspace_fixture_only` manual input defaults to false.
Enabling it skips package planning and assembly and runs only these six closed
Windows regressions, using isolated temporary directories:

- `TestClaimedRemovalPreservesUncapturedWritesDuringUnlink`
- `TestSnapshotCreatePublicationFailureRetainsOriginalRecovery`
- `TestRemovalJournalCapacityCompactionRetainsActiveProofAcrossRestart`
- `TestSnapshotObservationSharesBudgetBeforeHashing`
- `TestSnapshotObservationStopsAtAggregateGitInventory`
- `TestSnapshotAdmissionReservesPrivatePathHeadroom`

 Failure output retains structured closed-stage diagnostics.
Investigation and packaging use separate per-ref concurrency groups and neither
cancels an active run.
This bounded investigation mode preserves read-only access and is separate from
complete required CI, packaging and installed-platform acceptance.

## Renovate scheduling

Root `renovate.json` extends `github>delinoio/renovate-config` and defines this
repository's scheduling policy through local overrides. Ordinary branch
creation and existing branch updates are allowed on Mondays from 00:00 inclusive
to 04:00 exclusive in `Asia/Seoul`, using `schedule: ["* 0-3 * * 1"]` and
`updateNotScheduled: false`. Lock file maintenance uses the same explicit
schedule while retaining its inherited enablement and automerge rules.

Vulnerability-fix updates remain enabled and bypass the ordinary schedule.
Preserve the inherited security labels, immediate PR creation, automerge and
vulnerability-fix strategy, plus ordinary release-age and automerge rules.
The schedule limits branch work; it does not control the hosted Renovate
service's scan cadence or GitHub's already queued automatic merges.
See the official [schedule](https://docs.renovatebot.com/configuration-options/#schedule),
[existing branch update](https://docs.renovatebot.com/configuration-options/#updatenotscheduled)
and [vulnerability alert](https://docs.renovatebot.com/configuration-options/#vulnerabilityalerts)
contracts.

Validate configuration changes with
`npx --yes --package renovate -- renovate-config-validator --strict --no-global renovate.json`.
Inspect resolved presets to confirm that ordinary and lock file maintenance
work share the weekly window and security updates remain exempt. Check the
Monday 00:00 and 03:59 allowed boundaries, Monday 04:00 rejection and rejection
on other weekdays. Configuration validation is not evidence of a hosted bot
run; record that distinction in change summaries.

## Continuous integration

### Git LFS assets

Fourteen tracked assets use exact-path entries in the root `.gitattributes`: the two identical Noto Sans KR fonts used by Forge and DevHud, the three React Forge ROAM example PNGs, the DevHud API removal PNG, the DeliDev app-icon source PNG, and the seven AURA source PNGs. Ten files meet the 512 KiB threshold; the four smaller AURA textures remain in LFS with the rest of that texture set. The fonts share one LFS object. Imported assets retain their source and license records beside the files; the supplied DeliDev icon is repository-owned Apache-2.0 material. Future repository assets at or above this threshold require an explicit LFS entry and a hydrated checkout in every build, test, or packaging job that consumes them. Attribute changes force the CI job planner to select every event-eligible job; pull requests still defer native packaging.

CI and release jobs that consume these assets enable `actions/checkout` LFS downloading before building. In particular, the API image build must receive the actual removal PNG before Docker copies its context, and desktop builds must receive the font and DeliDev app-icon source before compilation or packaging. Validate migrated files against their original SHA-256 values, inspect committed pointer blobs, and run `git lfs fsck` before publishing a branch. PR #990 migrated the six font/ROAM/removal assets with a new commit without rewriting historical commits or tags; their older Git blobs remain in history. PR #988 migrated its AURA textures throughout its own PR history before merging that policy, as recorded in the React Forge validation evidence. `async-commit-hook` intentionally rejects LFS repositories, so this repository is no longer a supported execution source for that tool; its existing product contract is unchanged.

The schema-only `scripts/check-proto-breaking.sh` command scopes
`GIT_LFS_SKIP_SMUDGE=1` to `pnpm exec buf breaking`. Buf creates a temporary local
Git clone of the baseline; CI's pointer-only protocol checkout does not contain
LFS payloads for that clone to fetch. Schemas and Buf configuration remain normal
Git files, and incompatible schema changes must still fail comparison. This
setting does not apply to asset-consuming build, test, or packaging commands.
The CI regression uses a temporary repository with an unavailable LFS object and
the installed Buf/Git LFS tools to verify both compatible and breaking schemas.

Every CI step whose Turbo graph reaches `ci:proto:breaking` supplies
`DEVHUD_PROTO_BASELINE`. Push events compare against `github.event.before`, so
all commits in a push are checked even when `origin/main` already points to the
pushed revision. Manual main runs use `HEAD^`; PR and manual non-main runs use
`origin/main`. This includes both DeliDev protocol steps and async-commit-hook
validation, including the manually dispatched async-commit-hook release
workflow. The release workflow uses `HEAD^`; all of these jobs fetch full
history, and the breaking leaf retains its existing environment pass-through.
A revision without prior DevHud schemas still establishes the v1 baseline.
Workflow graph assertions and a temporary Git fixture cover the event rules,
compatible additions, deleted fields and multi-commit pushes without
downloading unrelated LFS assets.

### Pinned repository utilities

The root `clibox-prebuilt` dev dependency aliases the published `@delino/clibox@0.1.6` package. Its exact launcher and optional native packages are integrity-pinned in `pnpm-lock.yaml`; the private `packages/clibox` workspace is not an executable dependency. Ordinary `pnpm install` installs the prebuilt without Rust compilation. Run repository utility scripts from the repository root; they invoke `pnpm exec clibox` directly. There is no additional repository launcher or runtime download.

Development-port recovery guidance retains `env run`, as supported by the pinned 0.1.6 prebuilt. The source CLI's newer `run env` spelling does not change that installed binary's interface.

`.github/actions/setup-clibox` prepares Node/pnpm and installs only the root workspace with `--frozen-lockfile --ignore-scripts`. Jobs with an existing workspace install use `install: 'false'` to verify the same executable without a second install. `working-directory` supports a separate immutable tooling checkout for historical release recovery. The action checks `pnpm exec clibox --version` against the root dependency pin and logs the installed package path. Linux/macOS/Windows repository-tool fixtures run through `pnpm ci:tools`.

Compatible shell hashing, Base64, literal template replacement, timestamp formatting, and clipboard work use clibox. Preserve checksum record compatibility, private output permissions, and secret-free argv/logs. Public standalone installers, minimal Rust toolchain bootstraps, and provenance timestamps captured before tooling installation retain their existing tools. In-process Node data handling and signing, structured parsing, port binding checks, database health, nonempty-file readiness, and child lifecycle checks retain their existing stronger contracts.

The shared checksum generator keeps sorted recursive paths, GNU filename escaping, and the existing two-space text marker; it computes digests with clibox and writes the compatible relative filename records explicitly, independent of pnpm’s working directory. Template values use literal replacement. Signing inputs pass through stdin, and shell umasks or platform file permissions remain authoritative. In DevHud store recovery, only the Apple row checks out `github.workflow_sha` under `.clibox-tooling` and prepares its pinned utility there, using the step’s `working-directory` for `pnpm exec clibox`; the selected historical release source and artifact identities remain unchanged.

### Validation workflow

`.github/workflows/CI.yml` is a read-only validation workflow. It uses `contents: read` and `pull-requests: read`, does not consume repository secrets, and must not push tags, create or upload releases, submit stores, push OCI images, deploy documentation or infrastructure, promote updater state, or call any mutating release-controller operation. Release workflows and packaging inputs are tested as source and deterministic fixtures only.

The `ci-contracts` matrix runs workflow syntax and the full Node contract suite on
Linux, plus the uncached `ci:proto:launcher` fixtures on native macOS and Windows.
Linux runs the launcher fixtures within the full suite. Protocol lint, format and
freshness launch `node_modules/@bufbuild/buf/bin/buf` through `process.execPath`
with explicit argv, root cwd and `shell: false`. They do not require a standalone
Buf executable or Windows package-manager shim. The fixtures remove Buf from
PATH, use checkout paths with spaces, check literal shell metacharacters and
failure statuses, and run minimal offline generation with tracked/untracked
freshness rejection. Full generation retains the compatibility step after Buf;
the launcher fixture does not establish broader Windows plugin acceptance.

The central `changes` job runs on Ubuntu 24.04 and resolves one Rust package
selection for the generic `rust-test` and `rust-clippy` jobs. It prepares the
first-party `cargo-mono@v0.6.9` Linux x64 release only when the initial path plan
selects Rust validation. The archive size/SHA-256, executable size/SHA-256,
source revision and exact `cargo mono 0.6.9` output are pinned in
`scripts/ci/cargo-mono-prebuilt.mjs`. The verified executable lives in private
runner temporary storage; no binary cache or source-build fallback is used.
This repository utility release is separate from the Tauri producer recipe lock.

The published executable must pass offline temporary-workspace fixtures for
transitive, normal, optional, build, dev and target-specific path dependents, rename owners,
empty selection, and the pnport runtime relationship before repository planning.
Those Linux fixtures also execute the selected test and all-features Clippy
commands. Ordinary `pnpm ci:contracts` skips these two live tests when no verified
executable is supplied; static, injected-installer and Git fixture tests still run.

`scripts/ci/rust-affected.mjs` detects affected packages at the planner's exact
head in a temporary detached worktree. Metadata selection skips LFS smudging
only in that temporary reference, never in compilation checkouts. Cleanup removes
the worktree even after command or JSON failures. Detection includes transitive
manifest path dependents and retains cargo-mono's default AGENTS exclusion.
Rename detection is disabled. The released CLI writes tracing logs to stdout even in JSON mode; only its child environment sets `RUST_LOG=off`, while the adapter retains structured planning logs. The CLI's files and merge-base must match the
planner's exact NUL-separated comparison; unsupported newline/quoted/trimmed
paths and non-linear push ranges select the full validation workspace instead.
Manual runs, forced checks, root Cargo inputs, toolchains, external Rust-job
inputs and PR merge manifest/lock/config differences also select the full
baseline. Download, digest, execution, schema and metadata-lock failures fail
planning; they never authorize a skip. `pnport-preload` additionally selects
`pnport`, because the CLI loads that companion outside Cargo's dependency graph.

The final `rust_packages` JSON array excludes `forge-scene`, `forge-glb` and
`forge-fbx`. An empty selection disables both generic Rust jobs in the final
job plan, and `CI Result` checks package/job consistency. Compilation still uses
the normal PR merge checkout. Tests run `cargo test --locked -p <package> ...
--all-targets`; Clippy runs `cargo clippy --locked -p <package> ... --all-targets
--all-features -- -D warnings`. Required dependency compilation remains Cargo's
responsibility. rustfmt and project-owned native/release validation retain their
existing scopes. Logs and step summaries record comparison and validation
revisions, package names, full-selection reasons, commands and outcomes without
credentials or raw native content.

`scripts/ci/run-rust.mjs` maps that validated selection to an explicit uncached
`@delinoio/ci` Turbo task variant. Each variant declares only the selected
DevHud, DeliDev and test-only pnport prerequisites through `dependsOn`; the leaf
calls the existing Rust adapter with the original package list and locked Cargo
flags. This preserves package selection without preparing unrelated desktop
inputs. The published CLI selection fixture also runs as an uncached Turbo task
in `changes`, after the pinned binary is prepared and before final planning.

The Ubuntu workspace Rust Clippy and test jobs install WebKitGTK 4.1 development files when DevHud or the DeliDev desktop host is selected, before compiling those hosts. Its package supplies the JavaScriptCoreGTK 4.1 pkg-config metadata required by the resolved all-features graph. PR #1041's Clippy job failed at this native prerequisite before linting source; the CI prerequisite repair does not claim a Rust source change.

Those same jobs build the DevHud frontend only when `devhud` is selected, and the DeliDev typed client, frontend and target-specific Go sidecar only when `delidev-desktop` is selected. The generic test job builds pnport and its injection companion only when `pnport` is selected. Go setup is also retained for selected pnport static-child fixtures, so that test does not silently skip its compiler prerequisite. The Go setup uses the repository's pinned `go.mod` version, and generated frontend and sidecar output remains ignored. This preserves Tauri's declared external-binary input without adding a placeholder artifact to source control.

CI never builds a signed private candidate and never publishes.

The read-only `devhud-supply-chain` job uses Ubuntu 22.04 for the existing
installer, SBOM, provenance and updater fixtures and Windows for
`ci:windows-signature`, an uncached Turbo task. Windows prepares the pinned Go
toolchain to build a temporary native SignTool stub, then executes the private
workflow's PowerShell validation prefix. MSI and NSIS fixtures require nonzero
verification status to stop before installation, SBOM, evidence or upload
sentinels; zero status reaches every sentinel. Both rows remain required by
`CI Result`, without release credentials or publication authority. These fixtures
do not establish actual certificate verification or signed-package acceptance.

Changes to the shared checksum generator select the DevHud supply-chain fixture job that exercises it, including on pull requests where desktop packaging is skipped.

Forge uses `forge-test` on Linux, macOS, and Windows for its three private Rust crates, DSL, preservation, state and official MCP-client tests. `forge-render` installs LibreOffice Impress and Poppler on Linux and explicitly runs the normally ignored renderer integration test, retaining PNG/PDF evidence for seven days. Both jobs participate in central path selection and `CI Result`; neither publishes packages or artifacts outside the workflow run.

Both Forge jobs are selected for root `rust-toolchain` changes on pull requests and main pushes. The alternate `rust-toolchain.toml` filename remains covered for a future toolchain configuration migration.

Rustfmt-only changes to `.rustfmt.toml` or `rustfmt.toml` select `rust-fmt` on
pull requests and main pushes. This applies at the root and to nested package or
source-directory overrides, even when a broad package rule would otherwise
select compilation or native packaging. Mixed changes retain their other job
owners. The cacheable `ci:rust:fmt` task hashes both filenames at every repository
depth and excludes generated `.turbo` logs from its broad script inputs so an
unchanged run can reuse its result. Its formatting job also runs the uncached
`ci:rust:fmt-fixture` task with the pinned formatter and locked Turbo. Disposable
offline workspaces verify
cold success, unchanged cache hits, configuration-only hash changes and fresh
formatting failures, plus override addition and removal. Fixture results do not
establish hosted remote-cache performance.

The central planner publishes `go_test_matrix` from the committed native matrix inventory before runner allocation. PRs allocate five independent Ubuntu runners (`core`, `server`, `harness`, `worker` and `workspace`), including shared-input and forced validation. Selected main pushes retain one Ubuntu runner, one macOS runner and five independent Windows runners. Manual dispatch retains all seven runners. This policy applies only to the common `go-test` job; other jobs retain their existing OS policies and no periodic CI schedule is added.

| Event/input | Ubuntu | macOS and Windows |
| --- | --- | --- |
| Ordinary PR | Affected packages across five shards | No runners allocated |
| Shared-input or forced PR | Complete package inventory across five shards | No runners allocated |
| Ordinary main push | Complete package inventory | Affected packages |
| Shared-input or forced main push | Complete package inventory | Complete package inventory |
| Manual dispatch | Complete package inventory | Complete package inventory |

PR Go results establish Linux coverage only. Windows/macOS regressions are checked on main or manual runs. Every selected package runs its complete tests; no test-name filters or separate core-test inventory are introduced. `scripts/ci/go-test.mjs --shard all` preserves the complete `go test -count=1 -json -timeout=20m ./...` suite for selected Ubuntu main jobs, manual dispatch and full validation on eligible hosts. Affected Ubuntu PR and macOS/Windows main execution uses the exact planned base/head. Shared Go dependencies and other shared inputs retain the affected selector's conservative full-inventory fallback. Ubuntu PR shards use the same five disjoint package owners as Windows. Each shard runs complete selected packages with `-count=1 -json -timeout=20m`, at Go's default compiler and package parallelism, without a separate precompilation phase. Main and manual Ubuntu jobs retain `--shard all`. Each Windows runner discovers its native `go list ./...` inventory and passes its selected package paths to `go test -count=1 -json -p=1` with a 20-minute package watchdog for `core`, `server` and `harness`, and a 45-minute package watchdog for `worker` and `workspace`. All runners hydrate LFS assets and generate the real administrator and ach UI embeds before testing. The matrix retains `fail-fast: false`, the existing affected selection, and the required `CI Result` aggregate.

| PR Ubuntu / Windows shard | Package ownership |
| --- | --- |
| `core` | Every package outside `cmds/delidev-cli`, including async-commit-hook, derun, runmoor, DevHud API and protocols |
| `server` | DeliDev `internal/server` and `internal/store`, including descendants |
| `harness` | DeliDev `internal/cli` and `internal/harness`, including descendants |
| `worker` | Every other DeliDev package, including the command root, worker and future packages outside the other shard owners |
| `workspace` | DeliDev `internal/workspace`, including descendants, on an independent runner |

Partition rules match full path segments, so similarly named siblings cannot be mistaken for owned descendants. The five sets cover the discovered inventory exactly once; newly added and native-only packages require no allowlist update. Invalid selectors, empty inventories, empty shards, discovery errors, compilation errors and test failures fail validation. The runner uses literal argument arrays without a shell, preserves build diagnostics, bounded failure diagnostics and nonzero statuses, and logs shard membership, package counts, compile time, test time and total elapsed seconds.

Affected Go validation on Ubuntu PRs and macOS/Windows main pushes discovers the native `go list -mod=readonly -test -json ./...` inventory after generating both real UI embeds. Only original package records own affected selections and shards; synthetic `.test` binaries and `ForTest` records are excluded. The affected runner retains production, internal-test and external-test imports and resolved `EmbedFiles`, `TestEmbedFiles` and `XTestEmbedFiles`. Production changes include reverse dependents; test-only and nested testdata changes select their owner. Explicit command-to-test edges cover DeliDev CLI/server, Runmoor native and ach integration fixtures that build commands in subprocesses. Administrator/client and ach UI/client inputs also seed their real embed owners. Exact `AGENTS.md` files are policy metadata and do not seed Go selection, including additions, deletions and moves. Resolved production/internal-test/external-test embeds remain authoritative; deleting or moving an embedded instruction file retains the conservative fallback. Other deletions, moves and unknown resources expand to the owning command/server domain; an unknown or missing domain expands to the full inventory. Shared Go/CI inputs force full selection. Missing comparisons, malformed inventories and discovery failures fail instead of producing an empty selection. An empty affected shard is a logged successful no-op; full partitioned shards still reject empty inventories. Go Quality uses the same selection and checks tracked formatting without rewriting files before vet. Every Go host runs real temporary Git/Go fixtures for native OS file selection and subprocess result-cache regressions. A private Go cache is seeded with a successful integration result, then a command-only change must fail fresh All and Core execution. Repeated valid runs must execute `TestMain`, and warm compile-only calls must reuse compiled objects.

Every actual execution through the central Go runner includes `-count=1` in both full and affected modes, for All and every Ubuntu/Windows shard. Turbo's `cache: false` alone does not disable Go's successful test-result cache restored in `GOCACHE`. A subprocess consumer's test binary can remain unchanged when its command source changes, so result reuse can skip `TestMain` and hide a regression. `-count=1` forces execution while retaining compiled-object reuse. Discovery, compile-only arguments, shard watchdogs and successful-main-only cache saves remain unchanged.

Windows first runs `go test -c -o NUL` for the selected packages at Go's default compiler parallelism, populating the build cache without executing any test binary or `TestMain`. The null output avoids collisions between packages with identical names and retains no generated test executables. Only after that phase succeeds does the complete `go test -count=1 -json -p=1` run begin with its shard-owned package watchdog. Compiler work and test fixtures never overlap across these phases. This keeps cold-cache compilation out of the serial test bottleneck without changing native test scheduling. Remove precompilation if native cold-cache measurements show no net saving.

Go execution streams `-json` events into the runner without retaining the raw stream. Each host logs top-level test starts and package outcomes, then ranks the twenty slowest completed top-level tests. Parent timings include subtests; dynamic subtest names are omitted to avoid copying native or fixture content into records. `.turbo/go-test/<shard>.json` records the source revision, invoked commands, compile/test/total wall times, exit status, package cache markers and all top-level test names, results and durations. Cached test durations describe the cached execution, not the current runner. Successful output is suppressed; build diagnostics and the last 1 MiB of failure diagnostics per top-level test remain in the console, with truncation reported explicitly. Successful-test buffers are discarded when their outcomes arrive so they cannot evict failed or incomplete diagnostics. Raw output is never written to timing artifacts. Malformed timing streams and incomplete successful runs fail validation. The workflow adds a timing summary and preserves only these JSON reports for seven days on success or failure under an OS/shard/revision/attempt-specific artifact name. Empty affected shards emit an empty successful report. Windows timing and cache comparisons remain required before claiming a shorter critical path; separating workspace from Worker does not by itself shorten a single slow test.

Native Git/PowerShell and durable SQLite fixtures on hosted Windows outgrew Go's default 10-minute package budget, and concurrent package suites starved bounded Worker and harness protocols. Independent runners shorten the critical path while retaining serial package execution within each Windows runner and each package's normal test parallelism. Remove `-p=1` only when full native Windows evidence supports concurrent package suites. This scheduling and bounded watchdog do not introduce a timeout for `ach` commands or change product concurrency limits. Bulk scheduler-only queue setup uses one durable transaction so it measures the worker scenario instead of thousands of independent disk flushes.

The independent workspace shard owns durable workspace cleanup and recovery regressions, including bounded journal compaction across fresh recovery owners. The workspace and Worker shards retain their 45-minute package watchdogs. The workspace budget covers the aggregate fixture runtime; every original or recovery operation retains the production five-minute deadline and the bounded recovery-attempt limit. The explicit Windows workspace investigation uses the same 45-minute package watchdog within a 60-minute job. Reassess these larger fixture budgets after native timing evidence permits reduction.

The September 30 baseline [main run 36677902250](https://github.com/delinoio/oss/actions/runs/36677902250/job/109767014132) spent 27m57s in Windows Go tests and 29m52s in that job; [PR run 36678529123](https://github.com/delinoio/oss/actions/runs/36678529123/job/109768905387) spent 26m53s and 29m45s respectively. Both restored Go caches. Windows validation targets a longest Windows job of at most 15 minutes, including setup; this is a target, not measured improvement. Compare two native runs, cache hits and restored keys, the slowest shard, summed Windows runner minutes and cache storage before claiming the target is met.

The initial four-shard [run 36686158655](https://github.com/delinoio/oss/actions/runs/36686158655) passed the complete CI. Its Windows jobs took 17m46s (`core`), 7m00s (`server`), 6m55s (`harness`) and 9m33s (`worker`), totaling 41m14s of runner time. All four missed both scoped and shared Go caches, whereas the baseline restored a 377 MiB Go cache; these are not identical cache conditions. The 69 native packages were covered once (45/2/7/15). The core suite's async-commit-hook tests took 532.267s, while the full Go step took 1,012s; cold compilation and other package work kept the job above the 15-minute target. This evidence motivated the separate compiler phase. PR cache-save steps remained skipped; main-only scoped cache sizes remain unmeasured before merge.

The `async-commit-hook` CI job follows the central change plan and validates the Go runner with race detection, its local UI, documentation app/client and generated protocol, release fixtures, and all six CGO-free target archives. Its path rule includes the owned source, installers, release workflow and packaging, documentation contracts, and shared Go/Node/protocol inputs; unrelated changes skip it. It uses the shared cache policy and participates in the exact `ci-result` inventory. Its temporary archives remain unsigned and unpublished. The separate manually dispatched `release-async-commit-hook.yml` owns signing, GitHub Release and Homebrew publication; consolidated documentation publication belongs to `public-docs`, and its default dry run cannot enter publication jobs. See [the release contract](cmds-async-commit-hook-release-contract.md).

The `async-commit-hook` job also owns shared protocol freshness for DevHud schemas and Go bindings under `protos/**` and TypeScript output under `packages/devhud-api-client/src/gen/**`. A change confined to that generated TypeScript tree selects the job on PRs and main pushes. Its `ci:proto:check` dependency runs the uncached `ci:proto:fresh` leaf, which regenerates and rejects tracked or untracked drift even with a warm cache. DevHud consumer jobs retain their existing selection, DevHud-only inputs do not select DeliDev jobs, and handwritten DevHud client changes do not select this shared protocol owner.

The always-running `changes` job computes the execution plan with `scripts/ci/plan.mjs` and `scripts/ci/job-paths.json`. Domain jobs depend on this plan and use job-level conditions, so unrelated jobs do not allocate runners. `ci-contracts` always validates the workflow and planner. Rules cover Go, Rust, every Node workspace, environment tooling, all implemented DevHud domains, packaging, public documentation, and private-package, public-release, and CEF-review workflows. Runmoor-only release scripts do not select DevHud native packaging.

Job ownership, configuration forcing and workspace forcing share one dependency-free
dot-aware path matcher. Wildcards include hidden files and directories at every
depth; literal-dot patterns retain their literal dots. Match Git's POSIX paths
without removing filename characters, including whitespace, newlines and literal
backslashes. Preserve single-segment wildcards, zero-or-more-directory globstars
and event eligibility. Planning works before pnpm installation. In particular,
changes limited to `apps/devhud-admin/.env.example` or
`servers/devhud-api/.env.example` select `repository-environment` on both PRs and
main pushes, and hidden descendants of `.github/actions/` force every eligible
job. Hidden inputs inside a job's workspace retain ordinary affected execution;
owned inputs outside that workspace force it. Unrelated hidden paths do not
select jobs.

The `devhud-release-contracts` job runs the complete top-level `scripts/release/*.test.mjs` suite, including release fixtures shared with other projects. Every test fixture in that suite, committed data under `scripts/release/fixtures/`, and the shared `project.mjs`, `runmoor.mjs`, and `update-homebrew.sh` implementations they exercise select this Ubuntu job on PRs and main pushes. Linux package changes under `scripts/release/linux-packages.mjs`, `scripts/release/linux-packages/**`, and `packaging/linux/**` also select this job, including direct and dynamic helper dependencies and the imported tool pins. These portable fixtures exercise interrupted uploads, immutable objects, concurrent publication, signing and public readback in isolation. Their failure must fail the selected job and `CI Result`. Preserve existing clibox consumers and the native `linux-packages` PR skip. Other project-specific release scripts retain their narrower owners. Runmoor and Linux package fixture or implementation changes do not select DevHud desktop or mobile packaging.

Changes to `scripts/release/generate-delidev-updater.mjs` select both
`devhud-release-contracts` for signing/inventory fixtures and `delidev-frontend`
for dependent packaging-script checks on PRs and main pushes. This external
input forces the DeliDev workspace through the affected runner so Turbo cannot
discard its checks. Both consumers must succeed in `CI Result`. The separate
credential-free native dry runs, production signing restrictions and event-based
packaging skips remain unchanged.

The `node-public-docs-test` job owns the consolidated root and subpath publication checks. Its path rule includes `packages/docs-site-switcher/**` and `docs/apps-react-forge-docs-foundation.md`; the latter contract alone forces the job. Its public-docs test boundary runs the shared selector interaction suite before building the assembled site, so changes to the shared navigation cannot bypass documentation CI.

Public-docs owns package-local external installer inputs for cached `build`, `build:frontend`, and `ci:routes` tasks. Each hashes `scripts/install/{nodeup,binpm,async-commit-hook,pnport}.{sh,ps1}`; build overrides use `$TURBO_EXTENDS$` to retain root inputs and leave generated outputs intact. Forced job selection does not bypass cache reads. `scripts/ci/public-docs-cache.test.mjs`, included in `pnpm ci:contracts`, checks all eight independent source mutations, complete cold/warm build and route validation, unchanged output restoration, and stale-asset failure with a frozen producer. It uses disposable source/output and local-only cache storage; hosted cache acceptance and production deployment remain separate.

The `node-pnport-test` job owns pnport launcher, packaging, installer and fail-closed release fixtures on affected PRs and main pushes. The four-host `pnport-native` job runs native execution, TypeScript, installed npm/Yarn PnP consumer, archive and direct-installer checks on macOS/glibc Linux x64/arm64 for 0.1.0 on affected main pushes and manual CI; PRs skip that native matrix. Both CI and release derive their matrices from the package-owned target registry. Release Project does not wait for this CI matrix. The separate pnport tag workflow independently requires its four-host gate before publication. Windows x64/arm64 is deferred to 0.2.0 with the same complete acceptance requirements.

PRs run affected validation, including the existing three-OS Go/environment checks and dual-architecture OCI checks, while deferring Linux CLI, pnport native, desktop, and mobile packaging. Relevant main pushes run two Linux CLI entries, eight Windows/Linux desktop entries, and four Android entries; the DevHud iOS job is skipped. The affected React Forge job runs four Windows/Linux hosts on PRs and main. Manual dispatch runs every check, all ten desktop entries, all three iOS entries, and all six React Forge hosts regardless of paths. The planner publishes event-specific matrices, and `CI Result` validates those matrices and every planned job result. Changes to `CI.yml`, shared setup actions, or central planning and result logic force all checks eligible for the event. A change to `job-paths.json` compares each rule with the comparison-base revision and forces only changed jobs; a missing or invalid comparison fails. React Forge documentation alone never selects its native build, and unrelated package/protocol/server changes do not select DevHud jobs. There is no nightly CI schedule.

PR comparisons use the merge-base of the event's base and head commits. Main pushes compare the exact `before..sha` trees, including multi-commit and force pushes. Git diff uses NUL-delimited names and disables rename detection so both old and new owners are selected. Invalid or unavailable revisions fail planning instead of producing an empty change set. Node workspace checks use the committed Turbo binary via `scripts/ci/run-affected.mjs`, invoking its installed Node entry point with the current Node executable instead of spawning a platform-specific package-manager shim or shell. Changes to this shared runner force every eligible CI job so its callers run against the change. The runner preserves literal task/filter arguments and the same `TURBO_SCM_BASE` and `TURBO_SCM_HEAD`. Forced checks and inputs outside the workspace dependency graph omit `--affected`; an otherwise empty affected set is a successful no-op.

PR validation runs through `turbo run` using the committed common runner. Apps and packages own their validation leaves and dependencies; the private `@delinoio/ci` workspace in `scripts/ci` owns repository-wide Go, Rust, workflow, environment and protocol checks. Actions owns tool installation, job selection, platform matrices, temporary PostgreSQL lifecycle, artifact retention and `CI Result`. Release and deployment workflows retain their existing entrypoints. Main currently has no `vp run` execution layer to remove; Vitest and the imported fspy sources remain required.

Root `turbo.json`, `package.json`, `pnpm-lock.yaml` and `pnpm-workspace.yaml` changes select `rust-fmt`, `forge-test` and `forge-render` on PRs and main pushes through their job-specific path rules. These checks use the shared Node/Turbo runner and retain their complete native commands with `FORCE_RUN: 'true'`. The root inputs do not add global forcing or change event-specific packaging eligibility. `CI Result` requires each selected check to succeed.

DeliDev separates type checking, pure UI/client tests, real Go-server integration, native/widget/QA fixtures and frontend production output. The schema, client and `checks / tests-1 / tests-2` desktop jobs retain main's affected ownership and complete matrix. Client pure tests precede client Go integration; each desktop shard runs its cached pure UI half before its uncached Go integration half. The checks phase retains independent QA build ownership. Each client/Vitest host builds one runner-owned Go fixture executable through an uncached task, sharing bytes while retaining private server/process/credential/data lifetimes. The jsdom worker ceiling remains four. DevHud native conformance installs the pinned Rust toolchain before Turbo and serializes capture, shortcuts, IPC and updater suites against their shared Cargo target directory. Go setup exports its resolved `GOCACHE` and `GOPATH` through task-local pass-through entries so Windows cache discovery does not require general user-profile environment access. DevHud retains every unit/component and script assertion, its four fresh desktop/mobile frontend builds and mobile isolation checks. Administrator embeds are regenerated and validated before Go compilation. Duplicate component/accessibility commands that run the identical suite are represented by one CI leaf; their assertions and package-local commands remain available.

Chrome extension CI orders `test:package → build:test → ci:zip`. The uncached package regression completes both clean builds across time zones before the final build executes or restores cached `dist`, `build` and `artifacts` outputs. ZIP parity validation follows that final build. These tasks must not write their shared output directories concurrently. Controlled cold/warm fixtures pause packaging after artifact removal and verify that neither the final build, cached output restoration nor ZIP validation can proceed; builder and reproducibility failures must fail the aggregate before ZIP validation. Type checks, pure tests and policy checks retain independent execution. The fixture checks explicit scheduler dependencies and its recorded operation sequence; summary wall-clock timestamps do not establish causal ordering, including for cache restoration.

`setup-turbo-cache` exchanges GitHub OIDC identity through the official Vercel action pinned to commit `49d7b1b46ba4c9251e1977986bfe18336feabc8f`. The `delino` team policy matches repository `delinoio/oss` and GitHub's `workflow` name claim `CI`. The shared authentication action additionally checks the exact `delinoio/oss/.github/workflows/CI.yml@` workflow-ref prefix before token exchange; repository variable `TURBO_TEAM=delino` selects the team, with optional `TURBO_OIDC_POLICY` selecting an exact policy. Only those validation jobs receive `id-token: write`. The short-lived token grants Remote Cache access only, is revoked by the action after the job, and is not a deployment or release credential. Authenticated main pushes and main manual runs read and write successful cacheable tasks. Same-repository PRs and non-main manual runs use remote read-only mode. Fork PRs skip authentication. Missing policy/configuration or a failed exchange records local-only mode; Turbo cache transport warnings remain visible while local validation continues. Never retry or suppress a task failure as a cache failure.

Task hashes include source/default workspace inputs, declared external inputs and dependency builds. `CI_CACHE_CONTEXT` supplies OS, architecture, the actual Node version, package-manager pin and Node pin. Go checks, rustfmt and protocol tasks additionally hash their own `CI_GO_CACHE_CONTEXT`, `CI_RUST_CACHE_CONTEXT` or `CI_PROTO_CACHE_CONTEXT`; repository tool pins remain explicit file inputs. Version queries must not install compilers: Go uses `GOTOOLCHAIN=local`, and Rust uses `rustup run` without its opt-in `--install`. JS hashes therefore remain reusable across jobs with different native prerequisites; build selectors such as `TAURI_ENV_PLATFORM` and `TAURI_ENV_TARGET_TRIPLE` are hashed by their owning frontend task. There is no new global environment or pass-through list; development's exact selector/tool/session allowlist is unchanged. Cache type checks, pure JS tests, static contracts, protocol generation and frontend products with explicit generated outputs. Go/Rust execution, database/OS/render/benchmark checks, repeated clean builds, embedded UI generation and protocol freshness remain uncached. Freshness directly regenerates and checks tracked plus untracked generated files on every call. Store Turbo JSON run summaries as workflow artifacts; they report task/cache outcomes without tokens or user state. The disposable rustfmt cache fixture records the exact production formatter command's joined completion, exit status and expected diff before returning its status to Turbo. Every miss must provide fresh completion evidence; a hit must not execute the formatter. Missing forwarded Turbo console output cannot substitute for, or invalidate, that evidence.

The cached `ci:environment:turbo` leaf hashes the root package/Turbo configuration, the workspace inventory, and the DevHud app, administrator, API and API-client manifests/Turbo configurations consumed by its nested development dry run. The API-client configuration input also covers creation of that optional file, and generated Turbo logs are excluded from its script inputs. Its nested development-graph query consumes these exact task-local `$TURBO_ROOT$` inputs outside the CI workspace. A Turbo graph node without a non-empty package `dev` script does not satisfy the required task inventory. Disposable fixtures run the actual checker with local-only caching: unchanged graphs reuse the cache; forbidden package environment overrides, missing development tasks, dependency task/configuration changes, manifest-only package-name changes, removal of a workspace declaration and workspace inventory changes invalidate it. Invalid graphs must execute and fail rather than replay a warmed success, and restoring each graph input must recover the original successful cache entry. Fixtures pass a synthetic canary and check that its value does not appear in output; they use no provider, credentials or live development servers. The environment fixture suite remains uncached.

Validate local cold/warm restoration in an isolated workspace, then mutate source, dependency, tool pin and build options and prove invalidation. Prove native/freshness failures still fail a warm run. Hosted acceptance requires an actual successful main cache write followed by a comparable PR remote hit; record source revisions, completion time, summed runner minutes and hit ratio in the PR and CI artifacts. A local hit or first unseeded PR cannot establish remote performance improvement.

Every pnpm install uses `--frozen-lockfile --ignore-scripts` and runs only once per selected Node workspace job. Local setup actions restore pnpm and Go caches scoped by OS, architecture, tool version, and lockfile. Explicit final save steps write only after successful main validation; Rust caches use the same main-only policy with failure caching disabled. Rustfmt has no Cargo dependency cache; its deterministic formatting result uses Turbo and Node tooling uses the existing pnpm policy. The DevHud desktop matrix restores a dependency-only Rust cache with target caching disabled, retries its locked dependency fetch at most three times with bounded backoff, then runs its frontend contract checks with Cargo offline so a later runner DNS outage cannot invalidate an already-resolved dependency graph. PRs restore eligible main caches but never save branch-scoped caches, so dependency-update PRs may repeat downloads until merged. Native output caches and existing artifact retention policies are not expanded. Go timing metadata has its separate seven-day retention described above.

`setup-ci-go` accepts an optional `cache-scope`, empty by default to preserve existing callers' exact keys. Repository Go tests use `go-test-all` on Linux/macOS; contracts, quality, ach, API, DeliDev schemas/client and each desktop phase use separate producer scopes. Ubuntu PR shards restore the main `go-test-all` producer scope and never save caches; they do not create shard-specific Linux keys. The workspace fallback scope applies only on Windows. This prevents a short contract job from freezing the shared immutable key before a larger compiler job can save its populated cache. Windows Go tests use `go-test-core`, `go-test-server`, `go-test-harness`, `go-test-worker` and `go-test-workspace` scopes, preventing independently populated build caches from competing for the same immutable key. A scoped restore first looks for its OS/architecture/toolchain scope and then the existing shared cache as a first-run fallback. The new workspace runner supplies the optional, default-empty `cache-fallback-scope` to restore the former `go-test-worker` producer between its own scope and the shared fallback; this reuses existing module/build bytes while keeping new saves under `go-test-workspace`. Other callers retain their restore priority and exact primary keys. Successful main jobs alone save their own primary keys; PRs never save. No source-revision key or unbounded per-run cache is introduced.

The PR frontend job runs the complete DevHud test coverage through `ci:check` and `verify:pins`, retaining the script fixtures, deterministic clean desktop/mobile frontend builds, font/dependency isolation, static widget contracts, and immutable CEF checks previously available inside native packaging. The aggregate DevHud `test` task is non-cacheable because these checks exercise clean builds and external contract inputs.

Generated protocol output and package-local frontend output are deterministic cacheable Turbo products. DeliDev validation has three independent planned jobs: `delidev-protocol` checks DeliDev Go bindings and generated freshness through the shared repository-wide schema command; `delidev-client` checks the DeliDev TypeScript client; `delidev-frontend` checks its complete desktop frontend. Screen-only changes select only the desktop job. Client/schema/generated-binding changes select dependent desktop checks, and command changes retain both real-server client and desktop integration. Shared inputs and manual dispatch still force all eligible jobs. Allocation metadata does not select client or desktop execution by itself. DevHud-only inputs and TypeScript client checks are excluded from these DeliDev jobs, matching the protocol separation boundary.

The desktop matrix contains exactly `checks`, `tests-1` and `tests-2`, with fail-fast disabled. Checks retain type validation, packaging/launch/widget script fixtures, QA, production build and release-output isolation. Vitest splits the entire file inventory into two disjoint shards. Package tasks declare their client build dependencies through Turbo; integration tasks remain uncached. The planner publishes the exact phase matrix, and CI Result rejects an altered matrix or any failed, canceled, missing or unexpectedly skipped planned job.

Each client/Vitest host builds its own current-checkout Go fixture executable once under runner temporary storage and passes only its absolute file path through `DELIDEV_TEST_BINARY`. The source-build preparation watchdog is five minutes to cover cold module download and SQLite compilation; structured start/failure timing retains the cause. This bound is separate from fixture lifetimes, product deadlines and the 20/45-minute Go package watchdogs. Fixtures share executable bytes while retaining independent processes, credentials, private directories and cleanup. Missing or invalid configured files fail; no cached or downloaded executable substitutes for this build. Ordinary local tests keep their private build fallback. QA retains its independently lifecycle-tracked source build and environment ownership. No user credentials or inference are involved. The ignored administrator and ach UI embeds, native host, desktop installer, mobile, smoke, signing, release, and deployment tasks are explicitly non-cacheable. CI validates schemas and generated freshness; Go formatting, vet, unit, PostgreSQL migration, integration, API, and sweeper behavior; Rust formatting, Clippy, unit, capture, shortcut, IPC, and updater behavior; frontend type, lint, unit, component, accessibility, build, security, and adapter fixtures; exact CEF pins and feasible native architecture builds; extension/native-host/installer packages; SPDX SBOM and provenance; non-root multi-architecture API and migration-bearing sweeper OCI layouts; public routes; and release workflow fixtures.

The `ci-result` job retains the `CI Result` name and always evaluates every required dependency. Planning and CI contracts must succeed. Every planned job must succeed and every unselected job must be skipped; failures, cancellations, missing jobs, invalid plans, and unexpected skips or execution fail the aggregate. The changes job publishes a structured decision log, the event-owned `go_test_matrix` and a job-selection table in its run summary. `CI Result` requires exact Go matrix agreement with that event even when the Go job is unselected; missing, malformed, empty or altered matrices fail validation.

The Mac native work omitted from ordinary CI is mandatory at its owning release boundary: the signed DevHud private candidate requires both macOS desktop packages, signed iOS arm64, and unsigned arm64/x64 simulator builds before assembly; the React Forge exact-tag release runs its shared native, installed CLI, render, and benchmark validation on both Darwin hosts before candidate assembly or npm publication. Go macOS checks remain on selected main pushes and manual runs; Forge and clibox macOS checks retain their existing ordinary CI policy. Cache limits and artifact retention are unchanged.

For a paid-runner comparison, main run `36053972411` used 169.7 macOS runner minutes, or approximately $10.52 at $0.062 per minute. This is the Mac work shifted out of ordinary CI for an equivalent affected run, not a measured post-change saving. A product release pays for its mandatory Mac validation once. Compare equivalent before/after runs by OS-specific runner minutes and hypothetical bill, and record any manual full-CI run separately. Public-repository hosted compute is currently unbilled.

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

The CEF summary writes committed/upstream revisions, comparison status and counts, and retained/total signal counts with an explicit truncation marker. It must succeed before the report upload, which retains the JSON artifact for 35 days. `scripts/release/devhud-operations.test.mjs` extracts and syntax-checks the actual Node summary heredoc, then executes the workflow summary step against synthetic zero-signal and truncated-signal reports. These fixtures check metadata output, redaction, and report preservation; they do not establish hosted comparison or artifact-upload acceptance.

Changes to DevHud workflows or these contracts must update `docs/apps-devhud-operations-contract.md`, `docs/apps-devhud-support-contract.md`, `docs/project-devhud.md`, the relevant release contract text, `AGENTS.md`, and `scripts/release/devhud-operations.test.mjs` together. `CI.yml` runs the static release/operations contract suite without contacting a controller or publication service.

## Selected CLI release and bot ownership

`release-project.yml` is the manual `Release Project` entrypoint. Its required choices are `project` (`binpm`, `cargo-mono`, `nodeup`, `with-watch`, `derun`, `runmoor`, `clibox`, `pnport`, `async-commit-hook`, `react-forge`, `delidev`) and `bump` (`patch`, `minor`, `major`, pnport-only `next`, default `patch`). It accepts only `main` in `delinoio/oss`. There is no main-push workspace publisher: Rust libraries remain available through explicit local `cargo mono publish` usage, but are not published by this workflow. DevHud and documentation deployment remain separate.

The private organization-owned GitHub App `delino-release-bot` has only Contents write and implicit Metadata read, no webhook subscriptions or user authorization, and selected installation access to `oss` and `homebrew-tap`. Both the organization and repository main rulesets must allow this app to bypass directly; other rules and actors are preserved. `DELINO_RELEASE_BOT_CLIENT_ID` is an Actions variable and `DELINO_RELEASE_BOT_PRIVATE_KEY` is an Actions secret in `oss`. Neither private keys nor installation tokens belong in files, artifacts, logs, Git URLs, or Git configuration. `actions/create-github-app-token@v3` obtains a fresh repository-scoped token immediately before each write phase and revokes it afterward. The app has no Actions or Administration write permission. Release-source inspection and same-repository release uploads use the built-in token. Registry uploads retain `CARGO_REGISTRY_TOKEN`.

The release workflow serializes its runs without canceling an active release. GitHub's default concurrency queue retains one pending run; a newer pending request replaces an older pending request. Each new run fetches current main, validates exact stable SemVer, and commits only the selected version source plus its Rust lock entry; clibox also updates its private npm source manifest in the same version-only commit. pnport updates both private Cargo package versions, both lock entries and its npm source version together. async-commit-hook updates exactly four version fields together: its Go constant, local UI/client manifests, and release metadata. All four must agree before preparation; missing or ambiguous declarations fail before writes, and unrelated bytes remain unchanged. Shell and PowerShell installer defaults independently resolve the highest published stable `async-commit-hook@v<version>` release and are not version-preparation fields, so a prepared but unpublished commit cannot become an install target. Rust manifests and lock entries must agree. Runmoor uses its existing `Version` constant; Derun uses `cmds/derun/internal/version/version.go`, also consumed by MCP server metadata. Minor and major bumps reset their lower components. Runmoor uses the stable release channel independently of the bump level.

The bot commit journals the repository/run ID, project, bump, and previous version. A rerun finds and validates that exact commit and its complete parent-to-child version-only diff instead of bumping again, even after main advances. Pushes are fast-forward only; conflicts fail without rebasing or force. After the prepared commit passes immutable release-source validation, Rust targets other than clibox and pnport publish with `cargo mono publish --package <project>` without waiting for main CI. Only after registry publication succeeds does the coordinator push the single `<project>@v<version>` tag at the recorded commit with the bot token. Go, clibox, and pnport releases use the same path without a registry upload. For clibox, pnport, and async-commit-hook, the registry job retains immutable source validation but skips Rust installation, caching, and publication; the preparation step neither requires nor injects a Cargo registry token. The job still succeeds so its dependent tag phase proceeds normally. Already-published crates can resume a missing remote tag, but a conflicting existing tag is always rejected. Tags are never moved or deleted for recovery.

For binpm, cargo-mono, nodeup, with-watch, derun, runmoor, clibox, and pnport, the tag triggers its owning project release workflow asynchronously, preserving its signing identity, platform matrix, assets, and Homebrew behavior. Those Homebrew writes use a fresh tap-only app token and the bot commit identity. async-commit-hook is preparation-only: its tag triggers no publication workflow, and its successful summary links to the separate manual release with the prepared version and exact commit requirement. That workflow retains its main-only signing identity, dry-run default, protected environment and existing Homebrew credentials. For all nine projects, the coordinator follows `prepare → registry → tag → summary` without querying or waiting for main CI or downstream release completion. The version commit still triggers the independent main-push `CI.yml` workflow; pending, failed, canceled, or missing CI results do not block registry publication or tag creation. Downstream build, test, signing, and publication checks remain enforced by their owning release workflows. There is no implicit redispatch, tag replacement, or public asset overwrite by the coordinator. A failed preparation, registry, or tag phase is repaired and retried through the same coordinator run to reuse any recorded version. A failed downstream tag workflow is rerun from its own Actions page and does not require retrying the coordinator when the tag push succeeded. Every run reports version, commit/tag identity, and preparation/registry/tag results without a CI result or CI run link; downstream status belongs to the owning tag-triggered or separately dispatched workflow. For async-commit-hook, select main and the prepared version explicitly in the separate manual release. Its existing tag check rejects publication if the dispatch commit differs from the prepared tag, including when main advances; historical-source publication and tag movement are not added by this integration.

The five legacy archive publishers (`binpm`, `cargo-mono`, `nodeup`, `with-watch`, and `derun`) use `scripts/release/legacy-cli-release.mjs` before downstream Homebrew work. Changes to this helper select `devhud-release-contracts`, which owns the top-level release fixtures. Generate the sorted unsigned `SHA256SUMS` from the retained build artifacts before any signing or upload. Resolve the remote tag to the exact source revision, enumerate every releases-list page, reject duplicate same-tag releases, and read the selected numeric release ID. A complete public release is reusable only when every expected unsigned asset matches those bytes and every Sigstore bundle verifies the owning workflow, exact tag or main identity, GitHub OIDC issuer, source commit and repository. Validate asset names, unique IDs, uploaded state, sizes and any supplied digests. This path performs no signing, deletion, upload or release edit and allows a failed tap update to resume. Missing, unexpected, duplicate or conflicting public assets and invalid signatures block before tap credentials or work. Public `target_commitish` can be a branch name; the resolved tag remains its source authority.

Fresh publication creates a source-bound stable draft after signing and verification. Recovery may complete only a draft whose `target_commitish` explicitly matches the source commit. Verify its entire existing inventory before signing, preserve valid existing bundles, sign only missing bundles and upload only missing assets. Recheck tag/source and unique release identity before writes, then download and verify the complete signed draft by pinned ID before publishing. Confirm the public state and signatures before downstream work. Unknown mutation outcomes stop the attempt; the next run reconciles remote state without an automatic write retry. Dry runs produce unsigned checksums without network publication or signing. Keep the existing platform and standalone asset inventories, including cargo-mono's legacy unqualified Unix binary. Recovery uses the retained exact artifact bytes; a rebuild with different archive bytes fails even at the same source revision. Missing or expired retained artifacts require maintainer recovery, never public asset replacement or a moved tag.

Source version tests must not pin the current Runmoor or Derun release literal. Release tests use temporary Git repositories and injected API responses, never real tag, registry, or deployment mutations. Validate with `pnpm ci:workflows`, `pnpm ci:contracts`, and the project release tests, including `node --test scripts/release/legacy-cli-release.test.mjs` for immutable public reuse, blocked conflicts, tap retry, fresh/partial drafts and signed readback. These fixtures and static workflow checks do not establish hosted publication or real tap acceptance. Initial app setup and validation do not dispatch a release.

## APT and DNF publication

The seven CLI release workflows call `release-linux-packages.yml` after GitHub Release publication and explicitly inherit secrets so its guarded publisher can resolve the protected environment; other reusable jobs never reference production credentials. Release Project ends after its tag phase; operators must separately await the exact downstream tag workflow, including protected native package publication and public installation verification, before starting the next project. All seven CLIs, including Runmoor and clibox, use stable; preview remains reserved. Follow `repository-linux-packages-contract.md` for exact inputs, package versions, tool pins, signatures and recovery. CI retains the `linux-packages` domain job in `CI Result`; it uses temporary keys and repositories on native amd64/arm64 hosts for relevant main pushes and every manual dispatch. The shared planner classifies it as native packaging, so PRs skip both architectures even for CI configuration changes. `CI Result` accepts that planned skip and still requires success whenever the job is selected. General Linux checks, static package contracts, and release-time packaging validation remain enabled. Only the publisher's global concurrency group uses `queue: max`. The narrowly scoped actionlint compatibility adapter independently validates that exact declaration and leaves all other workflow checks intact.

## Runmoor validation and release

`CI.yml` validates the Runmoor content through `node-public-docs-test`, using the shared change plan, one frozen install with `--ignore-scripts`, and the same exact Turbo comparison as other documentation jobs. The job is required by `ci-result` and only builds and validates documentation; it never deploys. Public Runmoor routes are Markdown-owned by `apps/public-docs/docs/runmoor` and are published below `/runmoor` by the consolidated site.

`.github/workflows/runmoor.yml` is a separate read-only `contents: read` validation workflow, gated to Runmoor source, release scripts/workflows, shared Go module changes, and its shared Go setup action. It runs race tests and explicitly enabled local Docker integration through uncached `@delinoio/ci` Turbo tasks, with no cache authentication or GitHub job assignment. Its path gate additionally covers the shared Node/pnpm/Turbo validation inputs; it prepares the frozen workspace tools and retains run summaries. Its per-ref concurrency group cancels superseded runs; caches follow the successful-main-only save policy. Real Tart integration remains operator opt-in. Existing CI aggregation, application development commands and fixed ports are unchanged.

`.github/workflows/release-runmoor.yml` accepts exact `runmoor@v<MAJOR.MINOR.PATCH>` tags and manual version/dry-run dispatch. Source version and revision must match the release plan; publication permits main or the exact tag only. Plan, build and package jobs use read-only contents authority and produce the three native platform archives plus SHA256SUMS. Dry runs cannot request OIDC, sign, create tags/releases, or upload public assets. Only the explicitly guarded publish job obtains `contents: write` and `id-token: write`, inspects any existing stable draft by exact revision, rejects unexpected or mismatched existing assets, downloads and cryptographically verifies a complete signed candidate and reuses those signature bundles when the deterministic unsigned artifacts match, or completes a positively owned partial draft with the missing assets. It signs the exact archives/checksum file only when reuse is not possible, verifies the exact workflow certificate identity and issuer, rejects conflicting existing tags or existing public releases, validates the complete signed asset names, sizes and SHA-256 digests after draft completion, reapplies the committed canonical release notes immediately before publication, and then publishes a stable release with overwrite disabled. No macOS/Xcode images or placeholder signatures are shipped.

Run `node --test scripts/release/runmoor.test.mjs` for deterministic archive, identity, checksum, signature-verifier-double and workflow isolation checks. Cross-build with `node scripts/release/runmoor.mjs build --version 0.1.0 --revision <40-hex-commit> --ref <git-ref> --mode dry-run --output <temporary-directory>`, then its `checksums` and `verify` commands. These commands do not need credentials or signing tools. Repository-wide release fixtures that use Debian packaging and GNU tar run on Linux. Follow `docs/cmds-runmoor-foundation.md` for runtime verification and compatibility limits.

Runmoor draft lookup enumerates the releases list with pagination: the tag lookup API returns published releases only and cannot locate a draft. Discovery rejects duplicate same-tag records and uncertain responses; later reads and the publication PATCH use the pinned numeric release ID. Source/tag/channel checks and exact eight-asset inventory checks remain mandatory, including a fresh full-list uniqueness check and ID-based read immediately before publication. Complete existing signed drafts retain their verified bundles, partial drafts retain the no-overwrite upload policy, and public releases are never modified. Logs expose stage, tag, release ID and failure classification only.

The September 2026 recovery addresses two observed failures: `runmoor@v0.1.1` was published before uploads and immutable-release enforcement left it without assets; `runmoor@v0.1.2` uploaded all eight assets but failed to rediscover its draft through the tag endpoint. The recovery preserved those historical releases and tags. After the corrected publisher was merged, `Release Project` ran once with `project=runmoor` and `bump=patch` to create the new source-bound release. The downstream release, anonymous macOS downloads, exact-tag Sigstore verification, checksum and packaged version/revision were confirmed separately from coordinator success. This recovery is complete; no further coordinator run is required. Do not rerun the old tag's unchanged publisher or move its tag. An unknown result after the publication PATCH requires inspection by the pinned ID before any retry. The repair was merged in PR #1001 and the bot subsequently published `runmoor@v0.1.3` at `514ff0a2af4c79a9448b3148c512e416e0b00cf6`; all eight anonymous asset downloads, four exact-tag Sigstore signatures, checksums and native macOS execution passed. The command foundation records the release/run identities and validation limits.

Runmoor Homebrew publication follows successful public archive publication through `release-runmoor-homebrew.yml`. Its read-only verification/native macOS installation gate precedes fresh tap-only bot credentials. It accepts an exact already-published version/revision on `main` for initial publication or recovery, defaults to a no-write dry run, verifies all eight public asset digests and signatures bound to the release source, and refuses a tap downgrade or changed same-version Formula. Recovery does not rerun the version coordinator or alter release assets. The public formula is macOS 14+ ARM64-only; native tests run in ephemeral CI rather than a developer's Homebrew installation. See the Runmoor command foundation for the complete boundary.

## pnport and clibox native and npm distribution

Each pnport native CI and exact-source candidate host repeats identical offline filesystem/compiler workloads with cold and warm caches at least five times. Upload numeric time/memory/disk results under a target/revision/attempt-specific artifact name, separately from native package assembly. A failed or incomplete measurement blocks its host job. Development results do not replace reviewed full conformance, minimum-OS or release-build acceptance and never open publication by themselves.

`pnport` is a selected private Rust distribution target. The unpublished CLI, interception library, Cargo.lock entries and npm source are synchronized at `0.0.0`; the first minor bump is `0.1.0`. The coordinator follows the common prepare/registry/tag/summary path without native validation or Cargo registry credentials/publication. Its tag workflow builds from the exact version commit, runs native execution, TypeScript and installed npm/Yarn PnP tests on all four macOS/glibc Linux hosts for 0.1.0, validates the complete source-bound archive/package set, publishes and confirms four npm native packages before the launcher, then signs and publishes the immutable GitHub Release and prebuilt Homebrew formula. A failed four-target or complete-set gate leaves the version commit and tag intact while blocking every publication phase; recover by rerunning the pnport tag workflow without moving its tag. Its manual default is a credential-free dry run. Runtime gaps from issue #958 currently keep this gate red; no `0.1.0` publication is permitted until the evidence passes. Windows x64/arm64 remains planned for 0.2.0 with its original full requirements. Both matrices derive from the package-owned registry. A reviewed private `pnportReleaseReady` source gate additionally blocks real publication until minimum-OS, full conformance and benchmark acceptance is complete; preparing a version never enables it. See [pnport distribution](packages-pnport-distribution-contract.md).

`clibox` is a selected Rust release target. Its executable Cargo manifest, Cargo.lock entry, private npm source manifest, executable, and generated packages must agree on the exact version. All six clibox crates have `publish = false`; its five internal companion versions do not participate in product bumps. The coordinator validates the exact source identity without waiting for main CI, then pushes `clibox@v<version>` without a Cargo registry token or crates.io publication and without waiting for npm. It does not publish npm packages itself.

`CI.yml` runs `node-clibox-test` on Linux, macOS, and Windows, with one frozen script-disabled workspace install, `cargo test --locked -p clibox -p clibox-config -p clibox-system -p clibox-transform -p clibox-wait` for unit/process/adapter readiness and configuration-command and transformation conformance, the shared affected planner/Turbo comparison, package fixtures, and temporary npm/pnpm consumer installs exercising JSON file readiness, environment execution, help/version, dotenv list/merge, YAML normalization, and all seven issue #917 transformations. Each OS also exercises the affected runner against temporary workspaces, including empty selection, forced execution, and failure propagation. External Cargo and release inputs force this workspace's checks. The job participates in `CI Result`. Native integration tasks are not cached; successful-main-only dependency cache rules remain unchanged.

`release-clibox.yml` builds eight native binaries (macOS and Windows MSVC x64/arm64; Linux glibc/musl x64/arm64). GNU builds run inside the shared digest-pinned AlmaLinux 9 image on native x64 and arm64 runners, with glibc 2.34 and baseline CPU/ELF checks. Both musl jobs install `musl-tools` and select target-specific `CC=musl-gcc` for Rustls/ring C compilation; final binaries use the pinned Rust toolchain's `rust-lld` and self-contained runtime objects, execute on the native build hosts, and run npm/pnpm consumer smoke tests in Alpine. Windows uses the MSVC toolchain. macOS binaries use the deployment baseline of the repository-pinned Rust target. Each build runs native unit/process tests for `clibox`, `clibox-config`, `clibox-system`, `clibox-transform`, and `clibox-wait`, checks executable version, and creates a platform npm tarball. Rustls/ring adds no dynamic OpenSSL dependency; Alpine consumer images include `ca-certificates` for OS-trusted HTTPS. The assembly job adds the launcher, verifies exactly nine source-commit/version-bound tarballs, and uploads `clibox-npm-<version>-<revision>` with 30-day retention. Native intermediate artifacts are named `clibox-native-<suffix>`. A separate exact-tag guarded `publish-release` job verifies the full nine-tarball set, copies its two GNU executables into deterministic Linux archives, signs those archives and SHA256SUMS with Sigstore, and publishes through a verified recoverable draft. Existing assets are immutable; verified signatures and complete public releases are reused. The common native workflow follows that GitHub Release. The npm enable flag gates npm only. The same complete npm artifact also supplies deterministic macOS x64/arm64 archives, with full Apache-2.0 terms, NOTICE and the original fspy-family MIT notice. Four archives and SHA256SUMS each have a source-bound Sigstore bundle. After GitHub publication, `homebrew-test` verifies all ten public assets and the source identity and installs/tests/audits the Formula on both native Mac architectures. `homebrew` revalidates both tested Formula artifacts before creating a fresh tap-only release-bot token and publishing the identical Formula. `homebrew-readback` installs/tests the public tap on both architectures. No Homebrew publication depends on npm enablement. Dry runs render the candidate Formula without publication credentials or public installation. Rerun failed downstream jobs using the existing exact tag and retained artifacts; the tap refuses downgrades and different Formula bytes at the same version.

Manual dispatch defaults to dry-run and may validate a development ref. Non-dry-run publication requires the exact first-party version tag. Build/assembly/dry-run jobs have read-only contents permissions and no signing or registry credentials. Only the separately guarded npm `publish` and GitHub `publish-release` jobs have `id-token: write`, for npm provenance and Sigstore signing respectively. The npm job requires `CLIBOX_NPM_PUBLISH_ENABLED=true` and the exact tag/source commit. Neither npm nor GitHub publication queries crates.io; both retain source-version and complete artifact verification. It uses Node.js 24, explicitly installs pinned npm 11.6.2 before validating the 11.5.1+ OIDC requirement, and publishes with OIDC and provenance. All platform packages are published and their npm registry integrity confirmed before the launcher. Existing identical tarballs are reused; any conflicting remote integrity fails before new writes. Rerun only failed jobs after partial publication so the original complete artifact is reused. Expired/missing artifacts require maintainer recovery; never overwrite an npm version or move its source tag.

All nine packages require a Trusted Publisher permitting publication from `delinoio/oss` and `release-clibox.yml`. Setting `CLIBOX_NPM_PUBLISH_ENABLED=false` or leaving it unset disables npm publication while retaining validated artifacts; it does not disable the separately guarded GitHub Release or native package jobs. Publisher configuration and retry requirements are in [the npm distribution contract](packages-clibox-distribution-contract.md). No setup or validation command dispatches a workflow or publishes a registry version.

Repository-wide Go quality/tests and ach-specific compilation must first generate the app-owned ach UI embed using `pnpm --filter async-commit-hook build:embedded`. The root Go checks also retain the existing administrator embed prerequisite. The consolidated `public-docs` build renders the async content under `/async-commit-hook`; the executable release builder regenerates the local UI before cross-compilation.

The scoped Go formatting helper discovers tracked files with `git ls-files -z` and passes original NUL-separated paths as literal `gofmt -l` arguments. Git display quoting and embedded newlines must not omit or split files. Checks leave source bytes unchanged and retain discovery, formatter, missing-scope and empty-inventory failures. `scripts/ci/check-go-format.test.mjs` belongs to the existing central CI contract fixture suite.


### React Forge validation

Source-consuming React Forge CI and release build checkouts enable Git LFS to
hydrate the seven AURA source PNG textures before package/example validation.
Root `.gitattributes` changes select every event-eligible job, including React
Forge validation. AURA uses seven explicit attributes alongside the other
repository assets listed above.

The centrally planned `react-forge` job runs on affected pull requests and main pushes and is required by `CI Result`. Its ordinary four-host/Node 24 boundary follows `docs/packages-react-forge-contract.md`: package-owned uncached native build and integration, installed CLI, native/legacy Forge regressions, test-only LibreOffice/Poppler rendering and benchmarks on Windows/Linux x64/arm64. Manual CI adds both Darwin hosts. It retains evidence for seven days and removes generated package dist. Shared `forge-package` changes also select existing Forge validation/render jobs. PR CI verifies installed packages on each selected host without transferring native tarballs or assembling a complete release candidate. Seven external `0.0.1` npm name reservations preceded the first source release. `release-react-forge.yml` uses the same host validation command on all six native hosts, including mandatory Darwin rendering and benchmarks, and assembles the complete seven-package candidate on exact tags. Release Project prepared `0.1.0`, whose publish job failed before registry writes. The next patch tag, `0.1.1`, carried the publisher fix and became the first functional npm release through seven configured Trusted Publishers with OIDC; all seven registry versions, integrities, provenance markers and `latest` tags were confirmed.

The React Forge host job runs non-scene native, package, installed-consumer and document-render regressions on its selected matrix. It skips the React scene test suite and excludes the scene engines from targeted native gates. Generic workspace Rust test and Clippy jobs exclude `forge-scene`, `forge-glb`, and `forge-fbx`. Scene preparation, Khronos/ufbx interoperability, Blender imports, and product renders remain local acceptance evidence; CI does not schedule scene-specific test/render jobs or retain scene artifacts.

Native Tauri consumers restore/verify the immutable execution-host CLI through `.github/actions/setup-prebuilt`, including iOS/Android generation and credential-free DeliDev packaging. CLI keys bind release, host and both SHA-256 digests; successful main jobs alone save these caches. App Cargo caches remain separate. Root and native-package compilation use `nightly-2026-09-28`. Linux CEF jobs install GTK4, XDG portal/GTK portal and `zenity` alongside existing package prerequisites. Follow [prebuilt dependencies](repository-prebuilt-dependencies-contract.md).

### DeliDev automated release coordination

The shared release planner exports `cargo_publish` from the central project policy.
The coordinator resolves it before configuration validation and version commits,
then uses it to select Cargo registry credentials, Rust setup, caching and
publication. Non-Cargo projects still validate their immutable source and tag.

`Release Project` includes DeliDev stable patch/minor/major preparation and the
exceptional `delidev-v<semver>` namespace. It synchronizes desktop package,
Cargo/Tauri and lock versions, retains exact same-run source recovery, skips Cargo
registry publication through the common `cargo_publish` output and ends after
verified tagging. The tag push starts the dedicated independent release workflow;
the coordinator summary does not report downstream publication. Rerun the original
tag workflow to recover deployment failures without moving the tag.
Unlike the existing manual keyless dry runs, that workflow uses the `delidev-release` Environment for production macOS signing and publishes
an immutable macOS/Linux download-only release with Windows explicitly skipped.
Follow `apps-delidev-packaging-contract.md` and the download-only exception in
`cmds-delidev-updates-contract.md`. No update manifest or partial updater authority
is permitted; complete six-target updater activation requires a new version and
its original production signing/acceptance gates. Source changes do not configure
operational credentials or publish the first release.
