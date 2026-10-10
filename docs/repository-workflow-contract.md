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

DeliDev's frontend `checks` matrix entry retains its common affected checks and
runs `scripts/ci/run-delidev-browser.mjs` for the existing automated QA browser,
Worker model layout and command-menu layout entrypoints. The runner installs exact
Playwright 1.58.2 and its matching Chromium only under `RUNNER_TEMP`; the browser
package and download cache are temporary host tooling, not product dependencies.
The helper configures both existing host-module interfaces and selects bundled
Chromium. Missing tooling, empty acceptance records or unconfirmed original QA
cleanup fail the check; no entry is skipped or replaced with a passing placeholder.

The QA browser uses two independently paired temporary loopback servers/Workers
and owned synthetic Git folders. It runs ordinary metadata/Settings mutations,
Git inspection, explicit server/Worker lifecycle and revocation-isolation checks.
Worker model layout uses synthetic markup with product CSS. Command-menu layout
uses the existing settings entry with in-memory Connect fixtures. No check admits
hosted accounts, AI execution, external repository mutation, a native application
or installed-platform acceptance.

`DELIDEV_QA_SCREENSHOTS=disabled` suppresses both success and failure captures in
the QA browser without changing geometry, focus, lifecycle or cleanup assertions.
Its default `enabled` retains the existing explicit QA workflow. Hosted checks
always force `disabled` and upload only revision-bound structured JSONL results,
assertion/case coverage and original cleanup classification. Raw child output,
exceptions, tokens, private environment files and image files are not uploaded.
Browser evidence remains separate from native CEF, real account, native IME and
operating-system zoom acceptance. The helper retains failed evidence and returns
nonzero when any required fixture fails.

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

CI and release jobs that consume these assets enable `actions/checkout` LFS downloading before building. In particular, the API image build must receive the actual removal PNG before Docker copies its context, and desktop builds must receive the font and DeliDev app-icon source before compilation or packaging. Validate migrated files against their original SHA-256 values, inspect committed pointer blobs, and run `git lfs fsck` before publishing a branch. The originating change migrated the six font/ROAM/removal assets with a new commit without rewriting historical commits or tags; their older Git blobs remain in history. The originating change migrated its AURA textures throughout its own PR history before merging that policy, as recorded in the React Forge validation evidence. `async-commit-hook` intentionally rejects LFS repositories, so this repository is no longer a supported execution source for that tool; its existing product contract is unchanged.

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
freshness resolve the installed `@bufbuild/buf` package manifest `bin.buf` entry
through a shared bounded resolver, reject missing, malformed or escaping entries,
and launch its contained Node entry through `process.execPath`
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

The Ubuntu workspace Rust Clippy and test jobs install WebKitGTK 4.1 development files when DevHud or the DeliDev desktop host is selected, before compiling those hosts. Its package supplies the JavaScriptCoreGTK 4.1 pkg-config metadata required by the resolved all-features graph. The originating change's Clippy job failed at this native prerequisite before linting source; the CI prerequisite repair does not claim a Rust source change.

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

Disposable cache-policy fixtures set their subprocess event explicitly. Ordinary push/PR cold/warm restoration must not inherit a manual parent run's fresh-execution policy. Runner fixtures check literal invocation, forced/unforced scopes and failure propagation across push, PR and manual events. The real formatter fixture retains push cold/warm restoration and invalidation, and additionally requires fresh manual success and formatting-failure receipts despite a warmed result.

Validate local cold/warm restoration in an isolated workspace, then mutate source, dependency, tool pin and build options and prove invalidation. Prove native/freshness failures still fail a warm run. Hosted acceptance requires an actual successful main cache write followed by a comparable PR remote hit; record source revisions, completion time, summed runner minutes and hit ratio in the PR and CI artifacts. A local hit or first unseeded PR cannot establish remote performance improvement.

Every pnpm install uses `--frozen-lockfile --ignore-scripts` and runs only once per selected Node workspace job. Local setup actions restore pnpm and Go caches scoped by OS, architecture, tool version, and lockfile. Explicit final save steps write only after successful main validation; Rust caches use the same main-only policy with failure caching disabled. Rustfmt has no Cargo dependency cache; its deterministic formatting result uses Turbo and Node tooling uses the existing pnpm policy. The DevHud desktop matrix restores a dependency-only Rust cache with target caching disabled, retries its locked dependency fetch at most three times with bounded backoff, then runs its frontend contract checks with Cargo offline so a later runner DNS outage cannot invalidate an already-resolved dependency graph. PRs restore eligible main caches but never save branch-scoped caches, so dependency-update PRs may repeat downloads until merged. Native output caches and existing artifact retention policies are not expanded. Go timing metadata has its separate seven-day retention described above.

`setup-ci-go` accepts an optional `cache-scope`, empty by default to preserve existing callers' exact keys. Repository Go tests use `go-test-all` on Linux/macOS; contracts, quality, ach, API, DeliDev schemas/client and each desktop phase use separate producer scopes. Ubuntu PR shards restore the main `go-test-all` producer scope and never save caches; they do not create shard-specific Linux keys. The workspace fallback scope applies only on Windows. This prevents a short contract job from freezing the shared immutable key before a larger compiler job can save its populated cache. Windows Go tests use `go-test-core`, `go-test-server`, `go-test-harness`, `go-test-worker` and `go-test-workspace` scopes, preventing independently populated build caches from competing for the same immutable key. A scoped restore first looks for its OS/architecture/toolchain scope and then the existing shared cache as a first-run fallback. The new workspace runner supplies the optional, default-empty `cache-fallback-scope` to restore the former `go-test-worker` producer between its own scope and the shared fallback; this reuses existing module/build bytes while keeping new saves under `go-test-workspace`. Other callers retain their restore priority and exact primary keys. Successful main jobs alone save their own primary keys; PRs never save. No source-revision key or unbounded per-run cache is introduced.

The PR frontend job runs the complete DevHud test coverage through `ci:check` and `verify:pins`, retaining the script fixtures, deterministic clean desktop/mobile frontend builds, font/dependency isolation, static widget contracts, and immutable CEF checks previously available inside native packaging. The aggregate DevHud `test` task is non-cacheable because these checks exercise clean builds and external contract inputs.

Generated protocol output and package-local frontend output are deterministic cacheable Turbo products. DeliDev validation has three independent planned jobs: `delidev-protocol` checks DeliDev Go bindings and generated freshness through the shared repository-wide schema command; `delidev-client` checks the DeliDev TypeScript client; `delidev-frontend` checks its complete desktop frontend. Screen-only changes select only the desktop job. Client/schema/generated-binding changes select dependent desktop checks, and command changes retain both real-server client and desktop integration. Shared inputs force all eligible jobs. Manual dispatch without a comparison base also forces all eligible jobs; compared manual dispatch forces complete checks for each selected owning job. Allocation metadata does not select client or desktop execution by itself. DevHud-only inputs and TypeScript client checks are excluded from these DeliDev jobs, matching the protocol separation boundary.

The desktop matrix contains exactly `checks`, `tests-1` and `tests-2`, with fail-fast disabled. Checks retain type validation, packaging/launch/widget script fixtures, QA, production build and release-output isolation. Vitest splits the entire file inventory into two disjoint shards. Package tasks declare their client build dependencies through Turbo; integration tasks remain uncached. The planner publishes the exact phase matrix, and CI Result rejects an altered matrix or any failed, canceled, missing or unexpectedly skipped planned job.

Each client/Vitest host builds its own current-checkout Go fixture executable once under runner temporary storage and passes only its absolute file path through `DELIDEV_TEST_BINARY`. The source-build preparation watchdog is five minutes to cover cold module download and SQLite compilation; structured start/failure timing retains the cause. This bound is separate from fixture lifetimes, product deadlines and the 20/45-minute Go package watchdogs. Fixtures share executable bytes while retaining independent processes, credentials, private directories and cleanup. Missing or invalid configured files fail; no cached or downloaded executable substitutes for this build. Ordinary local tests keep their private build fallback. QA retains its independently lifecycle-tracked source build and environment ownership. No user credentials or inference are involved. The ignored administrator and ach UI embeds, native host, desktop installer, mobile, smoke, signing, release, and deployment tasks are explicitly non-cacheable. CI validates schemas and generated freshness; Go formatting, vet, unit, PostgreSQL migration, integration, API, and sweeper behavior; Rust formatting, Clippy, unit, capture, shortcut, IPC, and updater behavior; frontend type, lint, unit, component, accessibility, build, security, and adapter fixtures; exact CEF pins and feasible native architecture builds; extension/native-host/installer packages; SPDX SBOM and provenance; non-root multi-architecture API and migration-bearing sweeper OCI layouts; public routes; and release workflow fixtures.

Manual CI accepts an optional `comparison_base` input. An absent or empty input retains full manual validation. A non-empty input must be an existing, nonzero, lowercase 40-character commit SHA and an ancestor of the exact event head. Invalid syntax, missing commits, ancestry failures and comparison failures stop planning; they cannot become empty affected selections. The planner uses `git diff --name-only --no-renames -z` over the exact base/head pair, preserving both paths of a rename and newline-bearing filenames. Compared manual dispatch uses push ownership rules with manual event eligibility: an owning iOS change or broad CI configuration change selects iOS, while an unrelated desktop change does not. Central configuration and job-rule changes retain their existing force policy.

Every selected compared manual job executes its complete workspace checks. Go retains complete manual platform matrices and full native package suites with `-count=1`; selected Rust jobs retain the full manual workspace inventory and its documented exclusions. Desktop and React Forge retain their full manual matrices. All manual calls through `run-affected.mjs` use Turbo `--force`, so cached task results cannot substitute for fresh validation. Turbo rejects combining `--force` with an explicit `--cache` policy. Fresh manual execution therefore omits that policy and clears remote cache authentication for the child runner; it can write local results but cannot publish branch results through default remote behavior. Dependency and compiler caches remain available. The changes job stores `ci-selection.json` with the validated mode, exact base/head, final jobs, force decisions and selected Rust packages. A finalized selection is planning evidence, not test acceptance. CI Result recomputes the trusted event comparison and owning-path decisions before admitting unselected skips in compared mode. It rejects identity mismatches, missing selected jobs, selected skips, altered matrices, incomplete workspace force decisions and unexpected execution. Default manual validation still requires every domain job.

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

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

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

The September 2026 recovery addresses two observed failures: `runmoor@v0.1.1` was published before uploads and immutable-release enforcement left it without assets; `runmoor@v0.1.2` uploaded all eight assets but failed to rediscover its draft through the tag endpoint. The recovery preserved those historical releases and tags. After the corrected publisher was merged, `Release Project` ran once with `project=runmoor` and `bump=patch` to create the new source-bound release. The downstream release, anonymous macOS downloads, exact-tag Sigstore verification, checksum and packaged version/revision were confirmed separately from coordinator success. This recovery is complete; no further coordinator run is required. Do not rerun the old tag's unchanged publisher or move its tag. An unknown result after the publication PATCH requires inspection by the pinned ID before any retry. The repair was merged in the originating change and the bot subsequently published `runmoor@v0.1.3` at `514ff0a2af4c79a9448b3148c512e416e0b00cf6`; all eight anonymous asset downloads, four exact-tag Sigstore signatures, checksums and native macOS execution passed. The command foundation records the release/run identities and validation limits.

Runmoor Homebrew publication follows successful public archive publication through `release-runmoor-homebrew.yml`. Its read-only verification/native macOS installation gate precedes fresh tap-only bot credentials. It accepts an exact already-published version/revision on `main` for initial publication or recovery, defaults to a no-write dry run, verifies all eight public asset digests and signatures bound to the release source, and refuses a tap downgrade or changed same-version Formula. Recovery does not rerun the version coordinator or alter release assets. The public formula is macOS 14+ ARM64-only; native tests run in ephemeral CI rather than a developer's Homebrew installation. See the Runmoor command foundation for the complete boundary.

## pnport and clibox native and npm distribution

Each pnport native CI and exact-source candidate host repeats identical offline filesystem/compiler workloads with cold and warm caches at least five times. Upload numeric time/memory/disk results under a target/revision/attempt-specific artifact name, separately from native package assembly. A failed or incomplete measurement blocks its host job. Development results do not replace reviewed full conformance, minimum-OS or release-build acceptance and never open publication by themselves.

`pnport` is a selected private Rust distribution target. The unpublished CLI, interception library, Cargo.lock entries and npm source are synchronized at `0.0.0`; the first minor bump is `0.1.0`. The coordinator follows the common prepare/registry/tag/summary path without native validation or Cargo registry credentials/publication. Its tag workflow builds from the exact version commit, runs native execution, TypeScript and installed npm/Yarn PnP tests on all four macOS/glibc Linux hosts for 0.1.0, validates the complete source-bound archive/package set, publishes and confirms four npm native packages before the launcher, then signs and publishes the immutable GitHub Release and prebuilt Homebrew formula. A failed four-target or complete-set gate leaves the version commit and tag intact while blocking every publication phase; recover by rerunning the pnport tag workflow without moving its tag. Its manual default is a credential-free dry run. Runtime gaps from the feature currently keep this gate red; no `0.1.0` publication is permitted until the evidence passes. Windows x64/arm64 remains planned for 0.2.0 with its original full requirements. Both matrices derive from the package-owned registry. A reviewed private `pnportReleaseReady` source gate additionally blocks real publication until minimum-OS, full conformance and benchmark acceptance is complete; preparing a version never enables it. See [pnport distribution](packages-pnport-distribution-contract.md).

`clibox` is a selected Rust release target. Its executable Cargo manifest, Cargo.lock entry, private npm source manifest, executable, and generated packages must agree on the exact version. All six clibox crates have `publish = false`; its five internal companion versions do not participate in product bumps. The coordinator validates the exact source identity without waiting for main CI, then pushes `clibox@v<version>` without a Cargo registry token or crates.io publication and without waiting for npm. It does not publish npm packages itself.

`CI.yml` runs `node-clibox-test` on Linux, macOS, and Windows, with one frozen script-disabled workspace install, `cargo test --locked -p clibox -p clibox-config -p clibox-system -p clibox-transform -p clibox-wait` for unit/process/adapter readiness and configuration-command and transformation conformance, the shared affected planner/Turbo comparison, package fixtures, and temporary npm/pnpm consumer installs exercising JSON file readiness, environment execution, help/version, dotenv list/merge, YAML normalization, and all seven the feature transformations. Each OS also exercises the affected runner against temporary workspaces, including empty selection, forced execution, and failure propagation. External Cargo and release inputs force this workspace's checks. The job participates in `CI Result`. Native integration tasks are not cached; successful-main-only dependency cache rules remain unchanged.

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

## DeliDev QA output ordering

DeliDev QA preparation and its original signal-test children rebuild the API client in the current checkout. Turbo must finish desktop typecheck and release validation before `ci:qa` starts; release validation already follows the frontend build. The combined `test:qa` graph also finishes unit and integration readers first. A dependency on the initial API client build alone does not order a later QA rebuild against its consumers. These edges retain standalone QA preparation, original process admission and joined signal cleanup. Do not infer valid generated declarations from a successful earlier build while another owner can still rewrite its output. Synthetic graph and lifecycle fixtures establish ordering only; actual QA, typecheck, frontend builds and native/platform acceptance remain independent CI or owner validation.

## Workflow integration

- The five legacy CLI release workflows use `scripts/release/legacy-cli-release.mjs` before Homebrew credentials or writes. Follow the repository workflow contract: reuse only a complete public inventory with exact retained bytes and source-bound signatures; never sign, delete, replace, upload or edit on that path. Reject conflicting/incomplete public inventories. Fresh and owned partial drafts upload only missing assets and require complete signed readback before publication. Keep unsigned dry runs, exact tag/source checks and downstream job ordering.

- DeliDev checks use separate `delidev-protocol`, `delidev-client` and `delidev-frontend` jobs. Select schemas/generated freshness, client and dependent desktop checks by their inputs, exclude DevHud-only inputs, and require all planned results through `CI Result`. Shared `pnpm proto:check` remains repository-wide; follow `repository-workflow-contract.md`.

- Every workflow step reaching the shared protocol breaking check must pass `DEVHUD_PROTO_BASELINE`: push events use `github.event.before`, manual main runs use `HEAD^`, and PR/manual non-main runs use `origin/main`; the manually dispatched async-commit-hook release workflow uses `HEAD^`. Preserve full-history checkout and the Turbo leaf's environment pass-through, including indirect async-commit-hook and Go-binding paths. Keep schema-only LFS handling and no-prior-schema behavior unchanged.

- The `async-commit-hook` job owns shared protocol freshness for DevHud schemas, Go bindings and `packages/devhud-api-client/src/gen/**` on PRs and main pushes. Preserve DevHud consumer selection, exclude DevHud-only inputs from DeliDev jobs, and run the uncached `ci:proto:fresh` leaf through `ci:proto:check` even with a warm cache. Handwritten DevHud client changes do not select this shared protocol owner.

- Generic Rust CI uses the single verified prebuilt cargo-mono selection and exact comparison/fallback policy in `repository-workflow-contract.md`. Validate the published executable with offline live fixtures before planning; no source fallback or binary cache. Preserve final package/job agreement, pnport runtime ownership, scene exclusions, conditional native preparation, full manual/shared-input checks and independent project validation.

- Formatting-only configuration changes retain the separate `rust-fmt` job. That job installs the pinned formatter and runs its real cold/warm cache regression through an uncached CI workspace task; ordinary contract jobs need no formatter installation.

- The owner-authorized 2026-10-05 repair release in `project-pnport.md` permits exactly stable 0.1.2 after the failed 0.1.1 candidate is repaired and all retained four-native candidate gates pass. Keep 0.1.1 and published tags/bytes immutable. Require `pnportReleaseReady: true` and exact `pnportReleaseVersion: "0.1.2"`; preserve the earlier disclosed initialization/SIGHUP, clibox watch and full-acceptance limits without claiming fixes or skipped passes. No new candidate failure is waived. Keep the feature and Windows 0.2.0 acceptance open; preserve final-tag dry-run, fresh native validation, integrity/signing, native-before-launcher and immutable-retry gates. Later versions need separate reviewed authorization.

- Each pnport native CI/candidate host must pass installed hidden-cache native conformance and the prepared, offline default-config Vitest cache suite in inline/split forms before evidence recording. Linux includes dynamic/static children. Keep all four macOS/glibc Linux targets and existing native/benchmark/publication gates.

- pnport tag pushes perform credential-free dry runs only. Actual publication requires an explicit `Release pnport` dispatch at the same final tag with `dry_run=false` after its successful dry run. Before publication outputs/write authority, verify the latest exact-tag/revision first-party push run and its complete successful native/assembly jobs with all publication jobs skipped; missing, failed, pending or untrusted records block. All npm/GitHub/Homebrew jobs require the manual event and retain complete fresh native verification.

- The owner-authorized 2026-10-04 amendment in `project-pnport.md` permits exactly stable 0.1.0 publication with the recorded macOS initialization/SIGHUP failures, separate root clibox watch failure and full-acceptance review deferred. This version-specific exception takes precedence over earlier full-acceptance prerequisites; it does not establish a cause fix or passing skipped checks. Require `pnportReleaseReady: true` plus an exact `pnportReleaseVersion` match; version coordination preserves both fields. Retain all final four-native candidate execution/install/TypeScript/benchmark, integrity, signing, native-before-launcher and immutable-retry gates. New failures still block publication. Keep open, preserve Windows 0.2.0 and immutable 0.1.0-next.1, and disclose unresolved user-facing limits. Remove the stable unreleased notice only after verified publication.

- pnport 0.1.0 native CI and exact-tag release matrices derive from `packages/pnport/scripts/native-matrix.mjs` and its package-owned four macOS/glibc Linux targets. Windows x64/arm64 is deferred to 0.2.0. Preserve full native and installed-package gates, credential-free dry runs, exact-source identity and immutable publication. Stable publication also requires the reviewed private `pnportReleaseReady` source gate; version preparation never enables it. Experimental `0.1.0-next.N` publication requires the exact reviewed `pnportPreviewVersion` declaration instead, with the same four-host package gates. Keep npm next separate from latest, signed GitHub artifacts marked prerelease and never latest, and Homebrew skipped. Discover recovery drafts through every releases-list page, pin the numeric ID, and reject duplicate same-tag releases before writing or publishing. All five pnport npm latest tags now point to stable 0.1.2. The temporary first-name bootstrap exception is removed: preview inspection rejects every non-stable latest, including `0.0.0-stage`, and preserves stable latest plus the immutable next channel. Do not delete historical placeholder versions or mutate published package bytes.

- Each pnport native CI/candidate host must repeat the offline cold/warm benchmark protocol after installed-consumer and TypeScript conformance. Upload only the numeric `benchmark.json` under a target/revision/attempt-specific artifact identity, separately from native package assembly. A failed or incomplete measurement blocks its native job; passing measurements do not replace full/minimum-OS acceptance or enable publication.

- pnport 0.1.0 requires macOS 15+; use macOS 15 Intel and Apple Silicon native runners and `MACOSX_DEPLOYMENT_TARGET=15.0` for its own jobs. Do not change other projects' minimum operating systems.

- Each pnport native CI/candidate host must run the default-parallel process lifecycle suite with `PNPORT_TEST_BINARY` set to the absolute packaged native executable after archive installation, before recording native evidence or benchmarking. Source/debug process tests alone do not validate the shipped owner helpers and adjacent injection library; failures block native acceptance.

- The same installed binary must pass `native_conformance` for ZIP library constructors, nested loading/fork callbacks, direct package bins, mmap, read-only data and actual native source-file/directory watch events in inline/split form; Linux also runs a fully static watch child. Record both installed suite gates in assembled native evidence before benchmarking or publication. These fixtures do not replace full process ownership or minimum-OS acceptance.

- pnport candidates additionally repeat the internally concurrent fork/child-callback control ten times after the complete native suite, without retrying a failure or relaxing its cleanup deadline. This bounds investigation of native host-kernel ordering races; repeats do not replace the complete parallel installed suite.

- Go validation uses the central event-owned `go_test_matrix` under the existing `go-test` job and `CI Result` aggregate. PRs allocate five Ubuntu package shards only, including forced/shared-input validation, and establish Linux coverage only. Main keeps complete Ubuntu suites and native dependency-aware affected packages on macOS and five Windows shards. Shared inputs and forced validation select full suites only on event-eligible hosts; manual runs retain all seven complete suites. `CI Result` rejects missing or altered event matrices. Keep other jobs' OS policies independent and add no periodic schedule. Discover native packages before partitioning; every package belongs to exactly one PR Ubuntu or Windows shard, including newly added packages. Windows first compiles its selected test binaries at default Go compiler parallelism with `-c -o NUL`, without running fixtures; preserve `-p=1` for the subsequent complete test execution. Every execution through the central runner uses `-count=1` in full and affected modes so selected tests and `TestMain` run despite cached successful results; compiled objects remain reusable. Use the repository workflow contract’s 45-minute package watchdog for the Windows Worker and workspace shards and explicit workspace investigation; retain 20 minutes for other Go CI packages. These fixture budgets cannot change production deadlines or recovery-attempt limits. The workspace package and descendants own a separate Windows runner. Preserve metadata-only top-level test timing summaries and seven-day artifacts on success and failure. Windows Go caches remain shard-scoped; Ubuntu PR shards restore main's `go-test-all` cache without saving; Unix tests, contracts, quality, API/ach and DeliDev jobs/phases use separate producer scopes. Unmodified callers retain their default keys and all saves remain successful-main-only. Follow the repository workflow contract for import/test/subprocess/embed edges, conservative fallbacks and logged empty affected shards. Ubuntu shards use default Go parallelism, no precompilation and 20-minute watchdogs. Exact non-embedded `AGENTS.md` metadata does not widen Go selection. Comparison and discovery failures cannot pass.

- The optional DeliDev `workspace_fixture_only` manual input defaults to false. When true, run only the closed Windows workspace regression list declared in the workflow: retained writers, post-publication recovery, bounded journal compaction, shared observation entry/byte limits, aggregate Git inventory, and private-path admission headroom, all against temporary fixtures, skip package planning and assembly, and retain the same read-only credential boundary. Use independent per-ref concurrency groups for fixture investigation and packaging without canceling either. This investigation path never substitutes for complete CI or package/platform acceptance.

- DeliDev uses the one six-target native matrix exported by its package tool. Verify packages before uploading revision-bound workflow artifacts; never count static package inspection as native runtime, production-signing or release acceptance. Update the workflow contract tests with boundary changes and record validation in pull requests, issues and CI logs/artifacts under the root DeliDev validation policy; do not add repository evidence documents.

- clibox Homebrew follows its exact-tag GitHub Release independently of npm enablement. Both native macOS installation/audit gates and signed public-source verification must precede tap-only app-token creation. Publish only the identical tested Formula, then verify public-tap installation on both architectures. Dry runs never enter Homebrew publication; keep the release summary and workflow contracts synchronized.

- DeliDev updater input dry runs use the same six-target native matrix and keyless read-only manual workflow. Export exact twelve signed-inventory filenames and source/version/size/digest provenance only after package checks. Keep production signing readiness and private-key access separate; dry-run artifacts never imply production release or installed-platform acceptance.

- Shared Tauri CLI preparation uses `setup-prebuilt` and execution-host lock selection; restore/verify on native and mobile paths, save only after a successful main job, and retain existing application Cargo caches. Native DeliDev dry runs remain read-only and credential-free.

- DevHud Linux Xvfb smokes must provide an explicit session-local StatusNotifierWatcher fixture for the pinned ksni tray backend, retain Chromium sandbox requirements, and keep fixture-only tray registration distinct from real desktop-panel acceptance. Only AppImage smokes may set APPDIR and APPIMAGE.

- DevHud Windows private packaging checks `$LASTEXITCODE` immediately after SignTool verification and stops before installer execution on failure. Keep the failure diagnostic limited to stable context and numeric status. The read-only `devhud-supply-chain` job retains Ubuntu fixtures and runs native PowerShell failure/success stubs on Windows through an uncached Turbo task; stub results do not establish certificate or signed-package acceptance.

- PR CI validation uses package-owned Turbo leaves and the private `scripts/ci` workspace under `repository-workflow-contract.md`. Preserve complete assertions and native/clean/freshness gates; cache-only OIDC access does not grant release authority. Keep affected selection, development environment allowlists and final generated-dist cleanup intact.

- Load repository-local actions only after checkout. Cache authentication requires the `delinoio/oss` repository, GitHub workflow name `CI` and exact `CI.yml` workflow-ref prefix. Prepare the pinned Rust toolchain before native tasks and retain serial DevHud conformance; export prepared Go cache locations explicitly for Turbo strict environments.

- DeliDev validation separates schema/binding freshness, TypeScript client and desktop jobs. Preserve the exact checks/two-Vitest-shard matrix, Turbo client build dependencies, uncached real-server fixtures and complete CI Result aggregation. A runner-owned source build may share only its executable path; retain private fixture state and QA build ownership.

- The separate Runmoor PR Docker workflow also uses uncached `scripts/ci` Turbo tasks, preserving the explicit Docker opt-in, race coverage, read-only contents authority and successful-main-only dependency-cache saves. It requires no remote-cache token.

- DeliDev release coordinator and independent release workflow external actions use full commit SHAs. Keep macOS signing in a fresh Environment job with only checkout, Node and artifact transfer actions; no package manager/toolchain/build dependency installation precedes credentials. Build jobs have no signing Environment or secret references. Preserve same-run keyless DMG/Worker/CEF notice digest inventory and signed-candidate reuse before rebuild/resign.

- DeliDev mobile beta defaults to both platforms and permits explicit iOS-only execution under the mobile contract. Preserve target-bound candidates, all initial receipts before provider access, exact source provenance and non-canceling global serialization. iOS-only jobs require no Google credentials. Cross-run candidate and receipt downloads use their independently provenance-verified owner run IDs.

- DeliDev mobile `recovery_sha` is permitted only for explicit resume. Pin workflow code to that exact reviewed SHA and retain a separate clean checkout of the original candidate `source_sha`; verify original artifacts and candidate-bound receipts independently. The repair lane cannot package or submit a new candidate and never changes original binary provenance.

## Project requirements

- Agent Worker ordered source routes follow the catalog, desktop, protocol and sessions contracts. Main reservation the originating change owns System capability 36 and SaveAgentWorkerRequest.route_models field 5; preserve capability 35. Keep schema-3 routes exclusive with legacy fields, all models/accounts referenced and saved atomically, per-source routing state updated only with a successful first claim, confirmed-exhaustion-only fallback and observed-recovery preference for later new sessions. Preserve immutable executions, old-client write protection and portable v1/v2/v3 compatibility; add no SQLite migration.

- Go CI package watchdogs follow `repository-workflow-contract.md`: Windows Worker, workspace and explicit DeliDev workspace investigation use 45 minutes for aggregate durable filesystem fixtures; other Go CI package watchdogs remain 20 minutes. Preserve product deadlines and recovery-attempt limits independently of test scheduling.

- After completing each task, update the relevant `AGENTS.md` and `docs/` files in the same change when policies, structure, or contracts changed.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

- Root `pnpm install` must install Lefthook in linked worktrees when the effective `core.hooksPath` resolves to Git's shared common-directory hooks path, preserve Lefthook's protective failure for unrelated custom hook paths, and skip hook installation without blocking app preparation when Git metadata is unavailable.

- Renovate ordinary branch creation, existing branch updates and lock file maintenance are limited to Mondays from 00:00 inclusive to 04:00 exclusive in `Asia/Seoul`. Keep `updateNotScheduled: false` and preserve immediate vulnerability-fix handling, shared presets, automerge and release-age rules. Follow `repository-workflow-contract.md`; repository schedules restrict branch work, not the hosted service's scan cadence.

- Root `go.mod` selects the Go security baseline, currently Go `1.26.8`; keep the API/sweeper Docker build image on that exact version. CI reads the module selector.

Repository-wide quality CI is defined in `.github/workflows/CI.yml`. Go validation uses an event-owned `go_test_matrix` under the existing `go-test` job: PRs allocate only Ubuntu, including forced and shared-input checks; main pushes retain complete Ubuntu suites and native dependency-aware affected selection on macOS and five Windows shards; manual runs retain complete suites on all seven runners. Shared inputs and forced validation select complete suites only on the hosts eligible for that event. PR Go results establish Linux coverage only; Windows/macOS regressions are checked on main or manual runs. `CI Result` must reject a missing or altered event matrix. Test imports, subprocess command consumers and real UI embed owners must remain in the selection graph. Unknown/deleted inputs expand conservatively; discovery/comparison errors fail and empty affected shards are logged successful no-ops. Windows partitions the native `go list ./...` inventory exactly once, precompiles selected test binaries without running them at default compiler parallelism, then preserves `-p=1` for complete test execution per runner. Every central Go test execution uses `-count=1` in full and affected modes so selected tests and `TestMain` run even when successful results are cached; compiled-object caches remain reusable. The workspace package and descendants run on their own Windows runner. Timing summaries and seven-day artifacts retain source revision, commands, phase durations and top-level test metadata without raw output or dynamic subtest names. Shard-scoped caches are saved only after successful main validation. The Windows Worker and workspace shards and explicit workspace investigation use a 45-minute per-package watchdog; all other Go CI packages retain 20 minutes. These are fixture scheduling limits, not product command timeouts. Follow `repository-workflow-contract.md` for shard ownership and measurement.

Coverage expectations:

- `go-quality`: generates and validates the ignored administrator and ach UI bundles, then checks formatting without rewriting sources and runs `go vet` on the selected native packages on Ubuntu. Main/manual runs retain the full suite.

- `go-test`: generates and validates the ignored administrator and ach UI bundles on every allocated runner, then runs the complete tests of selected packages through `scripts/ci/go-test.mjs`. PRs use Ubuntu only; main uses full Ubuntu and affected macOS/Windows packages; shared/forced inputs select full suites on eligible hosts and manual runs use all seven runners.

- `rust-fmt`: runs `cargo fmt --all --check`.

- `rust-clippy`: prepares the DeliDev typed client, frontend and Go sidecar plus WebKitGTK 4.1 development prerequisites, then runs `cargo clippy --workspace --all-targets --all-features -- -D warnings`.

- `rust-test`: prepares the DeliDev typed client, frontend and Go sidecar plus WebKitGTK 4.1 development prerequisites, builds pnport and its injection companion with `cargo build --locked -p pnport -p pnport-preload`, then runs `cargo test --workspace --all-targets`.

- `node-public-docs-test`: runs `pnpm install --frozen-lockfile --ignore-scripts` and `pnpm --filter public-docs test`, covering the root and all seven project content sections.

- `node-clibox-test`: runs `cargo test --locked -p clibox -p clibox-config -p clibox-fspy -p clibox-system -p clibox-transform -p clibox-wait` for native utility/configuration/process/adapter and file-access behavior, including isolated Windows Ctrl+C/Ctrl+Break readiness cancellation and Node launcher result preservation. Native CLI consumer installation and launcher/distribution tests run on Linux, macOS, and Windows, selected by shared CI planning and required by `CI Result`.

- `node-pnport-test`: checks launcher and package contracts, version synchronization, immutable artifacts, installer rollback, and fail-closed release publication on affected PRs and main pushes.

- `pnport-native`: on affected main pushes and manual CI dispatch, runs the four macOS/glibc Linux x64/arm64 native targets for 0.1.0, installed npm/Yarn PnP consumers, TypeScript conformance, archive packaging, and direct-installer smoke. Each installed binary must also pass native hidden-cache conformance and the prepared offline default-config Vitest suite in inline/split form before benchmarking. PRs skip this native matrix; the pnport tag workflow independently requires the same four targets before publication. Windows x64/arm64 retains the complete acceptance requirements for 0.2.0.

- `node-public-docs-test`: runs `pnpm install --frozen-lockfile --ignore-scripts` and `pnpm --filter public-docs test`.

- `forge-test` and `forge-render`: validate the three private Forge crates on Linux/macOS/Windows, official stdio MCP interoperability, and mandatory Linux LibreOffice/Poppler rendering. Both follow central change planning and remain required in `CI Result`; optional local renderers do not make the selected render job optional.

- `react-forge` follows central change planning and validates four Windows/Linux hosts on affected PRs and main pushes, adding both Darwin hosts for manual CI and release. The shared host script covers non-scene native engines, installed CLI/MCP, package tests, Office/PDF rendering and benchmarks. Scene engine tests, React scene tests, interoperability, Blender visual evidence and product renders remain local acceptance work; generic Rust CI excludes the scene engines from tests/Clippy.

- `ci-contracts`: validates workflow syntax and the repository CI contract with the checked-in Go `actionlint` tool and Node fixtures on Linux. Its macOS/Windows rows run the uncached installed-Buf launcher fixtures through `ci:proto:launcher`; Linux covers those fixtures in the full contract suite.

- `async-commit-hook`: follows the central change plan, runs Go race tests, local UI/docs/client tests, protocol freshness and release fixtures, and builds all six unsigned target archives. Shared setup actions restore caches; only successful main validation saves them.

- `devhud-frontend`, `devhud-extension`, and `devhud-admin`: run package-local type, lint, unit, component, accessibility, and deterministic frontend/package builds.

- DeliDev CI separates `delidev-protocol` schema/Go-binding freshness, `delidev-client` TypeScript validation and `delidev-frontend` desktop validation. Screen-only changes do not select schema/client checks; command changes retain real-server integration. Desktop checks use exactly one checks phase plus two complete Vitest shards, with fail-fast disabled and all results required by `CI Result`.

- `devhud-api`: runs package-local Go format, vet, unit, PostgreSQL migration, integration, API, and sweeper conformance.

- `devhud-rust-conformance`: runs package-local capture, shortcut, IPC, updater, and native-host protocol tests in addition to the repository Rust baseline.

- `devhud-security`: runs credential, redaction, logout, deletion, restore, direct GitHub/R2, and agent adapter fixtures.

- `devhud-desktop`: validates the exact CEF pin and feasible macOS, Windows, and Ubuntu x64/arm64 native packages, installer/native-host lifecycle, and Linux X11 smoke.

- `devhud-mobile-contracts`, `devhud-ios-simulator`, and `devhud-android-emulator`: validate iOS/Android app and widget generation and production/simulator/emulator builds.

- `devhud-oci`: builds both API and sweeper OCI layouts for amd64/arm64 and validates non-root execution, embedded migrations, and SPDX SBOMs without pushing.

- `devhud-supply-chain`: validates installer, Native Messaging host, extension ZIP, updater/key-rotation signature, SBOM, and provenance fixtures on Ubuntu, plus native PowerShell SignTool failure/success stubs on Windows. These stubs do not establish certificate or signed-package acceptance.

- `devhud-release-contracts`: runs every top-level `scripts/release/*.test.mjs` fixture, including cross-project release tests, alongside deterministic static/dry DevHud candidate, identity, configuration, signing/preflight, review, rollback, and redaction contracts without exercising publication. Changes to those test files, committed data under `scripts/release/fixtures/`, `scripts/release/linux-packages.mjs`, `scripts/release/linux-packages/**`, or `packaging/linux/**` select this job on PRs and main pushes. Keep native Linux package assembly skipped on PRs and require the portable fixture result through `CI Result`.

- `ci-result`: retains the `CI Result` status and checks every dependency against the exact `changes` plan; failed/cancelled jobs, missing dependencies, and unexpected skips or execution fail the aggregate.

- The DevHud release-contract job also validates the internal operations runbook, repository workflow contract, and read-only CEF review workflow through `scripts/release/devhud-operations.test.mjs`.

Change-scoped execution rules:

- Generic Rust tests and Clippy consume one final `rust_packages` selection from the central changes job through the verified first-party cargo-mono 0.6.9 Linux x64 binary; never build the selector from source in CI. Include transitive manifest dependents and the explicit pnport-preload runtime test owner. Shared/forced/manual inputs, unsupported exact comparisons and changed PR merge graphs retain full validation. Empty selection disables both jobs and must agree with CI Result. Preserve scene exclusions, rustfmt, dedicated native/release gates, conditional desktop/sidecar/companion preparation and successful-main-only caches. Follow `repository-workflow-contract.md`; keep the Tauri recipe lock independent.

- A single `changes` job selects domain jobs before runner allocation using `scripts/ci/job-paths.json` and `scripts/ci/plan.mjs`. `ci-contracts` always runs. Go tests allocate Ubuntu only on PRs and retain all three operating systems on main/manual runs through the event matrix. Environment checks retain all three operating systems when selected.

- PRs run affected validation, including OCI checks, but never allocate the two Linux CLI package, four pnport native, ten desktop, three iOS, or four Android package entries. Relevant main pushes run Linux CLI, pnport native, eight Windows/Linux DevHud desktop entries, four Android entries, and four Windows/Linux React Forge hosts; they skip Mac desktop, iOS native, and React Forge Darwin rows. Manual dispatch runs every check and platform. The signed DevHud candidate requires both Mac desktop packages, signed iOS arm64, and arm64/x64 simulators; the React Forge tag release requires both Darwin rows through the shared CI validation command. There is no nightly CI schedule.

- PR comparisons use the base/head merge-base; main comparisons use the exact `before..sha` trees, including all commits in the push. Missing or invalid comparisons fail. Deleted and renamed files select both affected owners.

- Node workspace jobs use `scripts/ci/run-affected.mjs` to invoke the installed Turbo Node entry point directly with `turbo run <task> --affected --filter <workspace>` arguments, without a shell or package-manager shim, and with the planner's exact `TURBO_SCM_BASE` and `TURBO_SCM_HEAD`. External inputs and forced runs omit `--affected`; an otherwise empty affected set is a successful no-op.

- Because `public-docs` builds seven project content roots directly, changes under `apps/public-docs/docs/{async-commit-hook,binpm,nodeup,runmoor,clibox,pnport,react-forge}` select and force `node-public-docs-test`. Changes to `apps-react-forge-docs-foundation.md` alone also force that job.

- Central path rules cover Go, Rust, every Node workspace, repository environment tooling, DevHud domains, packaging, public docs, and package/release/review workflows. Runmoor-only release scripts do not select DevHud native packaging; shared DevHud packaging inputs still do.

- The PR frontend job runs the complete DevHud test command, including native-script fixtures, clean desktop/mobile frontend output validation, static mobile/widget contracts, and immutable CEF pins. Its aggregate `test` task is non-cacheable because it validates consecutive clean builds and external contract inputs.

- Protocol generation and package-local frontend outputs are deterministic and cacheable; the ignored administrator and ach UI embeds, native package, mobile, smoke, signing, release, and deployment tasks remain non-cacheable.

- DeliDev client and desktop CI build a current-checkout Go fixture executable once per client/Vitest host. Share only executable bytes through the absolute `DELIDEV_TEST_BINARY` test input; keep processes, credentials, state and cleanup private to each fixture. Local tests retain private builds, QA retains its lifecycle-tracked build, and all integration stays uncached. Go compiler cache producers use separate job/phase scopes and successful-main-only saves; no external remote cache is added.

- Changes to `.github/workflows/CI.yml`, `.github/actions/**`, or central planning/result logic force every check eligible for that event. Changes to `scripts/ci/job-paths.json` compare the previous and current rules and force only changed eligible jobs. PRs still exclude native packaging; `workflow_dispatch` without `comparison_base` runs all domain jobs regardless of changed paths. A non-empty `comparison_base` selects changed owning paths under the same rules as a push while retaining manual event eligibility and complete manual platform matrices.

- CI installs always use the frozen pnpm lockfile with `--ignore-scripts`. Shared pnpm/Go setup actions restore caches scoped by OS, architecture, tool version, and lockfile; only successful main jobs save them. Rust compilation caches likewise save only on successful main jobs; the DevHud desktop matrix must cache dependencies only with target caching disabled, and rustfmt has no dependency cache. PRs may restore main caches but never create branch-scoped caches. The Runmoor workflow uses the same Go cache policy and cancels superseded executions on the same ref.

- CI is read-only: it does not consume release secrets, push tags or images, create releases, upload stores, deploy services/docs, or mutate updater/controller state.

- When build or test commands change in project contracts, update this section and `.github/workflows/CI.yml` in the same commit.

Release automation baseline:

- CLI release orchestration is owned by `repository-workflow-contract.md` and the manual `Release Project` workflow: only binpm, cargo-mono, nodeup, with-watch, derun, runmoor, clibox, pnport, async-commit-hook, react-forge, and delidev are selectable. Do not restore main-push workspace publishing. Version commits and individual release-tag pushes use the repository-scoped `delino-release-bot` GitHub App; Homebrew uses a separate tap-scoped token. Preserve exact release-source validation, version-only run-ID recovery, non-forced pushes, and existing signed artifact workflows. Keep bot keys/tokens out of files, artifacts, logs, Git URLs, and configuration.

- Trigger contract: `release-project.yml` accepts only manual `main` runs in `delinoio/oss`, with closed `project` and `bump` choices. All selectable projects proceed from version preparation through registry validation/publication and the exact tag push without inspecting or waiting for main CI. Main CI runs independently; pending, failed, or canceled CI does not block release orchestration. For Rust targets that publish to crates.io, registry publication must still succeed before the tag push.

- The coordinator ends after the verified release-tag push. Tag-triggered project release workflows, including DeliDev, run asynchronously; downstream release failures are repaired from that workflow's Actions page and do not require retrying the coordinator when the tag push succeeded. async-commit-hook is preparation-only: its tag never starts publication, and its summary directs maintainers to the separate manual release workflow.

- Legacy binpm, cargo-mono, nodeup, with-watch and derun publishers follow `repository-workflow-contract.md`: verify exact retained unsigned bytes and source-bound Sigstore bundles before signing or writing. Reuse complete public releases with no release/signing mutations so downstream tap recovery can proceed; reject incomplete or conflicting public state. Fresh publication and explicitly source-owned partial drafts require complete signed readback before publication, missing-asset-only uploads, paginated unique discovery and pinned release IDs. Dry runs remain unsigned and nonpublishing; never move tags or automatically redispatch for recovery.

- Publish command contract: `cargo run --locked -p cargo-mono -- publish --package "$RELEASE_PROJECT"` for Rust CLI targets other than clibox and pnport; Go, clibox, and pnport targets validate the release source without a registry upload.

- Authentication contract: checkout disables persisted credentials, read-only release-source inspection uses the built-in token, and fresh `delino-release-bot` installation tokens perform source/tag and Homebrew writes with separate repository scopes. Configuration is `DELINO_RELEASE_BOT_CLIENT_ID` (Actions variable), `DELINO_RELEASE_BOT_PRIVATE_KEY` (Actions secret), and `CARGO_REGISTRY_TOKEN` (Rust upload secret). No PAT is required by these workflows.

- `release-cargo-mono` is defined in `.github/workflows/release-cargo-mono.yml`.

- Trigger contract: runs on tag push `cargo-mono@v*` and supports `workflow_dispatch` (`version`, `dry_run`).

- Distribution contract: publishes signed multi-OS cargo-mono release artifacts to GitHub Releases for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`.

- `release-binpm` is defined in `.github/workflows/release-binpm.yml`.

- Trigger contract: runs on tag push `binpm@v*` and supports `workflow_dispatch` (`version`, `dry_run`).

- Distribution contract: publishes signed multi-OS binpm release artifacts for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, including standalone prebuilt binaries (`binpm-<os>-<arch>[.exe]`) and archive assets (`binpm-<os>-<arch>.tar.gz|zip`), then updates Homebrew (`binpm`) from prebuilt archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.

- `release-nodeup` is defined in `.github/workflows/release-nodeup.yml`.

- Trigger contract: runs on tag push `nodeup@v*` and supports `workflow_dispatch` (`version`, `dry_run`).

- Distribution contract: publishes signed multi-OS nodeup release artifacts for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, including standalone prebuilt binaries (`nodeup-<os>-<arch>[.exe]`) and archive assets (`nodeup-<os>-<arch>.tar.gz|zip`), then updates Homebrew (`nodeup`) from prebuilt archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.

- `release-derun` is defined in `.github/workflows/release-derun.yml`.

- Trigger contract: runs on tag push `derun@v*` and supports `workflow_dispatch` (`version`, `dry_run`).

- Distribution contract: publishes signed multi-OS derun release artifacts and updates Homebrew (`derun`) from GitHub release prebuilt archives (`darwin-amd64`, `darwin-arm64`, `linux-amd64`).

- `release-with-watch` is defined in `.github/workflows/release-with-watch.yml`.

- Trigger contract: runs on tag push `with-watch@v*` and supports `workflow_dispatch` (`version`, `dry_run`).

- Distribution contract: publishes signed multi-OS with-watch release artifacts for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, including standalone prebuilt binaries (`with-watch-<os>-<arch>[.exe]`) and archive assets (`with-watch-<os>-<arch>.tar.gz|zip`), then updates Homebrew (`with-watch`) from GitHub release prebuilt archives (`darwin-amd64`, `darwin-arm64`, `linux-amd64`, `linux-arm64`).

- `release-devhud` is defined in `.github/workflows/release-devhud.yml` and is manual-only with exact `main` version input, an optional exact lowercase ancestor revision for interrupted-release recovery, plus `dry-run` or protected `release` mode. Historical recovery must reuse the retained revision-bound candidate and its original non-secret release-configuration fingerprint, must not rebuild signing output, and must bind every checkout and public boundary to the selected revision and destinations rather than the newer dispatch SHA or environment values. Controller authorization separately binds that newer dispatch SHA to the GitHub OIDC `sha` claim and permits a differing selected revision only after independent ancestor validation.

- `devhud-cef-security-review` is defined in `.github/workflows/devhud-cef-security-review.yml`, runs monthly or by explicit dispatch, and has read-only contents permission. It compares the committed Tauri revision with an immutable upstream `feat/cef` revision, emits only bounded redacted metadata, and may upload its report artifact; it must not mutate source, pins, lockfiles, releases, stores, registries, deployments, alerts, updater state, or GA state. High-risk CEF response is maintainer-owned and must update `apps/devhud/cef-pins.json`, every authoritative Cargo and verifier pin consumer, every matching `Cargo.lock` source entry, compatibility evidence, signed candidate evidence, and release contracts before any updater publication.

- The CEF maintainer summary must succeed before the metadata report upload, whose retention remains 35 days. Release fixtures must parse the actual summary heredoc and execute the workflow summary step with synthetic zero-signal and truncated-signal reports, checking revision/comparison/signal metadata without exposing raw commit messages, credentials, or native content.

- DevHud public release contract: serialize every version through one project-wide release group; retain, discover by exact revision across every attempt of every recovery run, reuse, and revalidate the original complete private signed candidate through the protected review window; fail closed across every documented signing, store, GitHub, Logto, PostgreSQL, R2, asset, registry, docs, and exact-identity provider-neutral deployment boundary; wait on protected review gates; reconcile absent App Store versions, already uploaded exact builds, pending submissions, same-commit retries, interrupted partial store publication, and draft GitHub Release assets without repeating completed mutations; require an existing draft's exact `targetCommitish` and an existing published release's remotely resolved tag to match the selected revision before reconciliation or reuse; validate and reuse an exact already-published immutable GitHub Release without deleting, replacing, or re-uploading assets; block the first publication on the exact docs candidate; bind the deployed `/devhud` page to that candidate; remotely reverify all exact stores, the exact GitHub asset set, the complete extracted evidence archive and expected keyless bundle inventory, every published payload and updater manifest against signed checksums and signatures, and source-bound immutable OCI digests and keyless signatures both before and immediately after final GA approval; require every downstream environment-bound publication, deployment, cleanup, store-publication, and GA job's complete release-variable fingerprint, including the Chrome extension and OCI production push-principal identities, to match the fully validated preflight environment before its first external check or mutation; query every exact store state before cleanup, withdraw every held store submission after any pre-publication failure or cancellation including a failed or timed-out final store-publication gate, and reconcile attempted infrastructure promotion from live controller status before rollback only while no store is public; and serialize infrastructure, all stores, regular GitHub Release, updater, public docs, independent verification, and GA without beta or partial GA.

- DevHud public preflight keeps private updater, desktop, iOS, and Android signing material confined to `devhud-private-build`; the publication environment receives only live release credentials and required dual-use Apple/Chrome identity material, checks Apple submission authority through a read-only API surface, binds the protected public-asset base URL to the controller's exact runtime authority through SHA-256 before promotion, requires the protected Google Play production-release service-account principal and the operator-confirmed OCI production push principal to match their credentials before any network access, classifies terminal App Store `INVALID_BINARY` state as rejected, replaces terminal canceled Apple review submissions with a live draft, and uses immediate/default Chrome publication only after the protected held-review gate. Controller updater input archives normalize ordering, ownership, modes, timestamps, and gzip headers so retries reproduce identical bytes.

- DevHud permission/deployment contract: start with no GitHub permissions, grant job-local read/OIDC/write scopes only where required, and keep the official API host operator-selected behind `servers-devhud-release-controller-contract.md`.

- DevHud Chrome review submission requires a `SUCCEEDED` upload response with the exact selected extension `itemId` and release `crxVersion` before publication. Stop on failed, processing, unknown, missing, or mismatched responses with fixed redacted errors. Preserve explicit operator recovery and the staged 100 percent review options; do not add automatic polling or retry. Follow `apps-devhud-operations-contract.md`.

- Project ID `async-commit-hook` owns `cmds/async-commit-hook`, `apps/async-commit-hook`, `apps/public-docs/docs/async-commit-hook`, `protos/async_commit_hook/v1` and `packages/async-commit-hook-api-client`; only executable `ach` is distributed.

- Follow `project-async-commit-hook.md` and its domain contracts. The feature applies with the owner's recorded exclusions of actual six-target machine validation and actual public publication.

- `Release Project` prepares async-commit-hook versions and tags only. Keep the Go version, local UI/docs/client package versions and release metadata synchronized in one version-only commit; installer defaults must resolve the latest published stable release so an automatic Public Docs deployment cannot target an unpublished prepared version. Reject drift and missing or ambiguous declarations before writes. Keep Cargo publication credentials out of this path. Actual publication retains the separate manual main workflow and its exact signing identity and tag/dispatch-commit validation.

- CLI/MCP/Connect share one core, exact-commit latest-compatible-attempt validation and explicit per-run acknowledgements. Never resurrect old successful evidence after pruning.

- State, reports and logs remain local and account-owned; no telemetry. User commands require explicit repository trust. Cancellation must reconcile owned descendants before releasing exclusive scheduling groups.

- Development uses frontend 46308 and local UI/API 46309 with conflict failure; docs development/preview use 46310/46281. Root DevHud development remains unchanged.

- The daemon and on-demand viewer serve the same embedded UI without pairing. Every RPC requires exact same-origin POST and the API version header. `https://oss.delino.io/async-commit-hook` is documentation-only.

- Run `pnpm --filter async-commit-hook build:embedded` before ach Go compilation or repository-wide Go checks, including commit hooks. The app owns the generated command webassets/dist; never commit or substitute placeholder assets. Root Go checks also require the existing DevHud administrator embed.

- Legacy binpm, cargo-mono, nodeup, with-watch and derun releases bind every checkout and new tag to the validated workflow SHA. Before signing, release upload and Homebrew writes, resolve remote lightweight or bounded annotated tags and all release-by-tag state, reject conflicting, orphaned or uncertain targets, and atomically create plus re-resolve an absent tag before handing off to the release uploader. Preserve manual-main publication, never move tags, and keep development-ref dry runs unsigned with read-only contents authority. Follow `repository-workflow-contract.md`.

- Native package release callers must explicitly inherit secrets so the reusable publisher can resolve its protected `linux-packages` environment. Only the guarded publication job references production credentials; validation and installation jobs remain credential-free. Preserve the environment boundary for manual recovery of already-published release identities.

- APT signing-certificate updates are distributed by the shared `delino-archive-keyring` dependency in both suites. Keep certificate versions immutable, retain historical public signing subkeys, and require a completed 30-day old-signer publication overlap before switching CI subkeys.

- Relevant main pushes, including Rust CLI source, Cargo workspace/configuration and toolchain changes, must select the Linux package CI job so both native architectures retain the AlmaLinux 9 compatibility baseline. Manual CI dispatch always selects it. PRs skip this job even when CI configuration changes force all eligible checks; its `CI Result` dependency remains and must match the planned skip. General Linux validation, static package contracts, and release-time packaging checks remain enabled.

## CI task integration

- `@delinoio/ci` owns repository-wide validation; app/package leaves stay with their workspace. Actions owns setup, affected planning, matrices, temporary services, artifacts and `CI Result`.

- Follow `repository-workflow-contract.md`. Route checks through `run-affected.mjs` and publish the central event-owned `go_test_matrix`. PR Go tests allocate five Ubuntu package shards even when forced; main keeps full Ubuntu suites and affected macOS/Windows packages; shared inputs and forced validation select full suites only on eligible hosts and manual runs retain all seven complete suites. PR Go results establish Linux coverage only. Require exact matrix agreement in `CI Result`; preserve exact comparison SHAs, external forcing, complete selected package tests, disjoint PR Ubuntu/Windows package shards and platform-owned 20/45-minute package watchdogs. Ubuntu uses default parallelism without precompilation, restores main's `go-test-all` cache and never saves.

- DevHud API client validation in `devhud-frontend` runs the package `typecheck` and `test` tasks together with `FORCE_RUN=true`. Consumer builds do not cover the package test files.

- Use the dependency-free shared dot-aware path matcher for job ownership, configuration forcing and workspace forcing. Include hidden leaves and directories at every wildcard depth, preserve literal-dot patterns and Git path characters, and retain event eligibility. Planning must work before workspace installation.

- Affected Go discovery uses native `go list -mod=readonly -test -json` and retains only original package owners. Match resolved production/internal-test/external-test embed files against every consumer before narrowing testdata ownership; production embeds propagate to callers and test embeds select tests only. Preserve import and subprocess/frontend edges, native checkout identity, discovery failures and conservative deletion/move fallbacks.

- Root `turbo.json`, `package.json`, `pnpm-lock.yaml` and `pnpm-workspace.yaml` changes select `rust-fmt`, `forge-test` and `forge-render` through their job-specific path rules. Preserve the existing native `FORCE_RUN` commands and event eligibility; these root inputs do not globally force unrelated jobs.

- `scripts/release/generate-delidev-updater.mjs` is an external input of `delidev-frontend` and `devhud-release-contracts`. Generator-only PRs and pushes must select both consumers and force the DeliDev workspace checks; retain separate credential-free native dry runs and event-based packaging skips.

- Preserve the central validated Rust package selection. `run-rust.mjs` selects an explicit uncached Turbo graph with only the selected owners' build prerequisites; it must not expand the Cargo package list or bypass empty-selection rejection.

- Cache deterministic leaves only. Go/Rust execution, DB/OS/render/benchmark, repeated clean builds, embedded generation and protocol freshness run every time. Declare actual outputs and external inputs, plus task-local platform/tool/option hashes.

- The cached environment graph checker must hash root/workspace definitions and the DevHud app, administrator, API and API-client manifests/Turbo configurations. Keep `ci:environment:turbo` inputs tied to the exact package graph and workspace files, exclude generated Turbo logs from its script inputs, and preserve the exact development environment allowlist and task inventory. Its disposable fixtures must prove cold/warm reuse, independent invalidation and verifier failure for each graph mutation, including manifest-only package-name changes, and reuse after restoration; keep `ci:environment` uncached.

- Central Go test execution uses `-count=1` in full and affected modes for every shard. Turbo's uncached task does not disable Go's result cache. Keep discovery and compile-only arguments unchanged, retain compiled-object cache reuse, and verify subprocess consumers and repeated execution in disposable fixtures.

- Keep JS hashes independent of native tool availability. Hash installed Go/Rust/Buf versions only in their owning cached tasks; metadata queries must not install compilers.

- Vercel OIDC tokens are short-lived and cache-only. Main writes, first-party PR/manual non-main reads, forks skip auth; failures fall back locally and must not suppress validation failures. Keep development's exact environment boundary unchanged.

- Test cold/warm restoration, invalidation and uncached failure propagation in disposable fixtures. Record hosted main-write/PR-hit and comparable timing in PRs or CI artifacts, not repository evidence files.

- Rustfmt configuration changes select only `rust-fmt`, including nested overrides matched by broad package rules. Hash both supported filenames at every depth in `ci:rust:fmt`; run the real formatter cache fixture uncached in the formatting job. The disposable fixture records the exact production command's completion, status and diff before returning to Turbo; cache hits must create no fresh receipt. Do not accept unrelated nonzero exits or rely on Turbo console forwarding as completion evidence.

- Extension packaging order fixtures prove the declared dependency chain and observed cold/warm operations through a held destructive interval. Turbo wall-clock summary timestamps are timing diagnostics, not causal ordering evidence; preserve cache restoration and every injected failure control.

- CI task setup must resolve Go cache paths before strict environment filtering and install the pinned Rust toolchain before shared-target native validation. Retain the ordered DevHud capture, shortcut, IPC and updater dependency chain.

- Protocol lint, format and freshness resolve the pinned installed `@bufbuild/buf` package manifest `bin.buf` as a bounded, contained Node entry and invoke it through the current Node executable with literal argv and no shell. Keep the launcher fixtures uncached on Linux, macOS and Windows; preserve compatibility generation and tracked/untracked freshness rejection.

- Changes to the shared legacy CLI publisher select `devhud-release-contracts`, which owns its top-level release fixtures. Keep this input edge in `job-paths.json` and the planner tests when changing release-helper ownership.

- Go timing reports retain source revision, commands, phase times and top-level test metadata only. Keep raw output and dynamic subtest names out of artifacts. Preserve failure status, native affected selection and exhaustive shard coverage when changing instrumentation. Windows workspace tests run on their own runner and use the retained 45-minute fixture watchdog.

- Exact `AGENTS.md` files are policy metadata and do not seed affected Go selection. Resolved production/internal-test/external-test embeds remain authoritative; embedded deletions/moves and other unknown resources retain conservative fallbacks.

- Go formatting inventory uses NUL-separated Git paths and passes literal arguments to the formatter. Preserve source bytes and Git, formatter, missing-scope and empty-inventory failures; cover quoted non-ASCII and newline names in the central contract fixtures.

- Runner-owned DeliDev Go fixture preparation uses a five-minute cold-build watchdog, separate from fixture lifetimes, product deadlines and Go package watchdogs. Publish structured start/failure timing; share only the resulting executable path.
