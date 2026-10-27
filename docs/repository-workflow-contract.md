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

Repository workflows are reviewed as source-backed contracts. Workflow IDs, job IDs, artifact names, permissions, triggers, protected environments, and publication boundaries must match the checked-in YAML and the static workflow tests.

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

No current repository assets require LFS tracking. Future repository assets at or above the 512 KiB threshold require an exact-path LFS entry and hydration in every build, test or packaging job that consumes them. Imported assets retain their source and license records beside the files. Attribute changes force every event-eligible CI job; pull requests still defer native packaging.

CI and release jobs that consume these assets enable `actions/checkout` LFS downloading before building. Validate migrated files against their original SHA-256 values, inspect committed pointer blobs, and run `git lfs fsck` before publishing a branch. The originating change migrated the six font/ROAM/removal assets with a new commit without rewriting historical commits or tags; their older Git blobs remain in history. `async-commit-hook` intentionally rejects LFS repositories, so this repository is no longer a supported execution source for that tool; its existing product contract is unchanged.

The schema-only `scripts/check-proto-breaking.sh` command scopes
`GIT_LFS_SKIP_SMUDGE=1` to `pnpm exec buf breaking`. Buf creates a temporary local
Git clone of the baseline; CI's pointer-only protocol checkout does not contain
LFS payloads for that clone to fetch. Schemas and Buf configuration remain normal
Git files, and incompatible schema changes must still fail comparison. This
setting does not apply to asset-consuming build, test, or packaging commands.
The CI regression uses a temporary repository with an unavailable LFS object and
the installed Buf/Git LFS tools to verify both compatible and breaking schemas.

Every CI step whose Turbo graph reaches `ci:proto:breaking` supplies
`PROTO_BASELINE`. Push events compare against `github.event.before`, so
all commits in a push are checked even when `origin/main` already points to the
pushed revision. Manual main runs use `HEAD^`; PR and manual non-main runs use
`origin/main`. This includes async-commit-hook
validation, including the manually dispatched async-commit-hook release
workflow. The release workflow uses `HEAD^`; all of these jobs fetch full
history, and the breaking leaf retains its existing environment pass-through.
A revision without prior Buf configuration establishes the initial baseline. Retired and transferred schemas, including `bundlewhy.v1`, are excluded from the baseline projection; checks remain active for owned packages.
The baseline projection also permits only the forward Go package migration for `async_commit_hook/v1/ach.proto` from `github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1;achv1` to `github.com/delinoio/oss/protos/async_commit_hook/v1;achv1`. It applies only when the baseline and current descriptors match those exact paths and the `async_commit_hook.v1` package. Field, service and other Go package compatibility checks remain enabled. Remove this migration projection when supported baselines no longer contain the old Go path.
Workflow graph assertions and a temporary Git fixture cover the event rules,
compatible additions, deleted fields and multi-commit pushes without
downloading unrelated LFS assets.

### pnport Windows development checks

The separate `pnport-windows.yml` workflow runs on pnport-related pull requests
and supports manual dispatch. It derives native Windows x64/arm64 hosts from the
package-owned development matrix before allocation and reuses
`scripts/ci/pnport-windows.ps1` for source and installed-development-archive
validation. The two targets run independently with read-only contents access,
no secrets and no publication. Logs record revision, target and development-only
acceptance. PR coverage includes pnport, shared fspy implementations, package
tooling, workflow/validator inputs and shared dependency/toolchain inputs.
The affected paths include the checked-in root `rust-toolchain` selector.
This is an explicit exception for the separate Windows development workflow;
`CI.yml` retains its PR/manual native exclusions and affected selection.
It does not establish complete Windows release or minimum-OS acceptance.

A failed Windows integration command can replay only the Node fixture under a
native debugger for diagnostics. `PNPORT_WINDOWS_DEBUGGER` is a test-only switch;
ordinary source and installed-archive validation never enables it. The replay
retains the original failure regardless of its own result, has a bounded wait,
and records module-relative code locations and repository symbols only. It
never uploads process memory, native payloads or crash dumps. Symbol lookup uses
the actual loaded image path, including for installed archives. Missing or
unusable symbols produce an explicit unavailable result without falling back to
the debug build image. Hook recursion
and child-creation flags use OS-managed TLS with preserved Win32 last-error
values; thread and process teardown cannot re-enter runtime state through
image-based guard storage.

### Pinned repository utilities

The root `clibox-prebuilt` dev dependency aliases the published `@delino/clibox@0.1.6` package. Its exact launcher and optional native packages are integrity-pinned in `pnpm-lock.yaml`; the private `packages/clibox` workspace is not an executable dependency. Ordinary `pnpm install` installs the prebuilt without Rust compilation. Run repository utility scripts from the repository root; they invoke `pnpm exec clibox` directly. There is no additional repository launcher or runtime download.

Development-port recovery guidance retains `env run`, as supported by the pinned 0.1.6 prebuilt. The source CLI's newer `run env` spelling does not change that installed binary's interface.

`.github/actions/setup-clibox` prepares Node/pnpm and installs only the root workspace with `--frozen-lockfile --ignore-scripts`. Jobs with an existing workspace install use `install: 'false'` to verify the same executable without a second install. `working-directory` supports a separate immutable tooling checkout for historical release recovery. The action checks `pnpm exec clibox --version` against the root dependency pin and logs the installed package path. Linux/macOS/Windows repository-tool fixtures run through `pnpm ci:tools`.

Compatible shell hashing, Base64, literal template replacement, timestamp formatting, and clipboard work use clibox. Preserve checksum record compatibility, private output permissions, and secret-free argv/logs. Public standalone installers, minimal Rust toolchain bootstraps, and provenance timestamps captured before tooling installation retain their existing tools. In-process Node data handling and signing, structured parsing, port binding checks, database health, nonempty-file readiness, and child lifecycle checks retain their existing stronger contracts.

The shared checksum generator keeps sorted recursive paths, GNU filename escaping, and the existing two-space text marker; it computes digests with clibox and writes the compatible relative filename records explicitly, independent of pnpm’s working directory. Template values use literal replacement. Signing inputs pass through stdin, and shell umasks or platform file permissions remain authoritative.

### Validation workflow

Manual CI runs affected validation only and exposes no full-validation input.
It uses PR job eligibility and one Ubuntu Go shard, including when shared
configuration forces all eligible checks. PRs, main pushes and manual CI allocate
no macOS runners. Manual runs and PRs exclude native packaging; selected main
pushes retain Linux/Windows native checks. iOS build jobs are removed from this
CI workflow. Owning release platform gates remain unchanged.

For manual branch runs, the planner resolves `origin/main` to one commit SHA and
compares its merge-base with the exact execution head. It publishes that main
SHA for Rust consumers rather than resolving the moving branch again. Main
manual runs compare the head with its first parent. Missing refs, parents,
merge-bases or invalid SHAs fail planning. Rust, Go and Turbo selectors consume
the exact comparison; being a manual event alone never forces full validation.
The planner records `affected` as its execution mode and rejects obsolete `full`
requests. The existing per-ref concurrency group cancels superseded executions.

`.github/workflows/CI.yml` is a read-only validation workflow. It uses `contents: read` and `pull-requests: read`, does not consume repository secrets, and must not push tags, create or upload releases, submit stores, push OCI images, deploy documentation or infrastructure, promote updater state, or call any mutating release-controller operation. Release workflows and packaging inputs are tested as source and deterministic fixtures only.

The `ci-contracts` matrix runs workflow syntax and the full Node contract suite on
Linux, plus the uncached `ci:proto:launcher` fixtures on native Windows.
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

An empty `rust_packages` JSON array selection disables both generic Rust jobs in the final
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
test-only pnport prerequisite through `dependsOn`; the leaf
calls the existing Rust adapter with the original package list and locked Cargo
flags. This preserves package selection without preparing unrelated desktop
inputs. The published CLI selection fixture also runs as an uncached Turbo task
in `changes`, after the pinned binary is prepared and before final planning.

The generic Rust test job installs `liblzma-dev` before Cargo when `binpm` or `nodeup` is selected. Their `xz2` dependency links liblzma; a restored Cargo cache may retain dynamic-link metadata even when a fresh dependency build could choose a bundled library. Keep this dependency explicit so selected CLI tests do not depend on desktop application prerequisites.

The generic test job builds pnport and its injection companion only when `pnport` is selected. Go setup remains required for pnport static-child fixtures and uses the pinned `go.mod` version.

CI never builds a signed private candidate and never publishes.

Changes to the shared checksum generator select `release-contracts`, which runs the portable release fixtures on affected PRs and main pushes.

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

The central planner publishes `go_test_matrix` from the committed native matrix inventory before runner allocation. PRs allocate one Ubuntu core runner. Main uses the Linux all-package runner and Windows core runner. Affected manual runs retain PR eligibility.

| Event/input                               | Ubuntu                                       | Windows                    | macOS                |
| ----------------------------------------- | -------------------------------------------- | -------------------------- | -------------------- |
| Ordinary PR or affected manual            | Affected packages in the core shard          | No runners allocated       | No runners allocated |
| Shared-input or forced PR/affected manual | Complete package inventory in the core shard | No runners allocated       | No runners allocated |
| Ordinary main push                        | Complete package inventory                   | Affected packages          | No runners allocated |
| Shared-input or forced main push          | Complete package inventory                   | Complete package inventory | No runners allocated |

PR and affected manual Go results establish Linux coverage only. Windows regressions are checked on main. macOS acceptance remains a local or owning release validation requirement. Every selected package runs its complete tests; no test-name filters or separate core-test inventory are introduced. `scripts/ci/go-test.mjs --shard all` preserves the complete `go test -count=1 -json -timeout=20m ./...` suite for selected Ubuntu main jobs and forced full validation on eligible hosts. Affected Ubuntu PR/manual and Windows main execution uses the exact planned base/head. Shared Go dependencies and other shared inputs retain the affected selector's conservative full-inventory fallback. Ubuntu PR and affected manual shards use the same five disjoint package owners as Windows. Each shard runs complete selected packages with `-count=1 -json -timeout=20m`, at Go's default compiler and package parallelism, without a separate precompilation phase. Main Ubuntu jobs retain `--shard all`. Each Windows runner discovers its native `go list ./...` inventory and passes its selected package paths to `go test -count=1 -json -p=1` with a 20-minute package watchdog. All runners hydrate LFS assets and generate the real ach UI embed before testing. The matrix retains `fail-fast: false`, the existing affected selection, and the required `CI Result` aggregate.

The `core` shard owns all remaining Go packages, including async-commit-hook, Runmoor and protocols. Retired DeliDev server, harness, Worker and workspace shards are removed.

The core shard covers the discovered inventory exactly once; newly added and native-only packages require no allowlist update. Invalid selectors, empty inventories, empty shards, discovery errors, compilation errors and test failures fail validation. The runner uses literal argument arrays without a shell, preserves build diagnostics, bounded failure diagnostics and nonzero statuses, and logs shard membership, package counts, compile time, test time and total elapsed seconds.

Affected Go validation on Ubuntu PR/manual runs and Windows main pushes discovers the native `go list -mod=readonly -test -json ./...` inventory after generating the real ach UI embed. Only original package records own affected selections and shards; synthetic `.test` binaries and `ForTest` records are excluded. The affected runner retains production, internal-test and external-test imports and resolved `EmbedFiles`, `TestEmbedFiles` and `XTestEmbedFiles`. Production changes include reverse dependents; test-only and nested testdata changes select their owner. Explicit command-to-test edges cover Runmoor native and ach integration fixtures that build commands in subprocesses. ach UI/client inputs also seed their real embed owners. Exact `AGENTS.md` files are policy metadata and do not seed Go selection, including additions, deletions and moves. Resolved production/internal-test/external-test embeds remain authoritative; deleting or moving an embedded instruction file retains the conservative fallback. Other deletions, moves and unknown resources expand to the owning command/server domain; an unknown or missing domain expands to the full inventory. Shared Go/CI inputs force full selection. Missing comparisons, malformed inventories and discovery failures fail instead of producing an empty selection. An empty affected shard is a logged successful no-op; full partitioned shards still reject empty inventories. Go Quality uses the same selection and checks tracked formatting without rewriting files before vet. Every Go host runs real temporary Git/Go fixtures for native OS file selection and subprocess result-cache regressions. A private Go cache is seeded with a successful integration result, then a command-only change must fail fresh All and Core execution. Repeated valid runs must execute `TestMain`, and warm compile-only calls must reuse compiled objects.

Linux CLI deadline/response-wait fixtures and the isolated server restore-capacity, terminal-close, large-compaction and queued-title-budget fixtures overlap through top-level `t.Parallel()` calls. Their private directories, servers, credentials and cleanup remain independent; scenario order, real wait durations, assertions and product deadlines remain unchanged. Windows retains its existing scheduling. Complete package execution and the existing package watchdogs remain required.

Every actual execution through the central Go runner includes `-count=1` in both full and affected modes, for All and every Ubuntu/Windows shard. Turbo's `cache: false` alone does not disable Go's successful test-result cache restored in `GOCACHE`. A subprocess consumer's test binary can remain unchanged when its command source changes, so result reuse can skip `TestMain` and hide a regression. `-count=1` forces execution while retaining compiled-object reuse. Discovery, compile-only arguments, shard watchdogs and successful-main-only cache saves remain unchanged.

Windows first runs `go test -c -o NUL` for the selected packages at Go's default compiler parallelism, populating the build cache without executing any test binary or `TestMain`. The null output avoids collisions between packages with identical names and retains no generated test executables. Only after that phase succeeds does the complete `go test -count=1 -json -p=1` run begin with its shard-owned package watchdog. Compiler work and test fixtures never overlap across these phases. This keeps cold-cache compilation out of the serial test bottleneck without changing native test scheduling. Remove precompilation if native cold-cache measurements show no net saving.

Go execution streams `-json` events into the runner without retaining the raw stream. Each host logs top-level test starts and package outcomes, then ranks the twenty slowest completed top-level tests. Parent timings include subtests; dynamic subtest names are omitted to avoid copying native or fixture content into records. `.turbo/go-test/<shard>.json` records the source revision, invoked commands, compile/test/total wall times, exit status, package cache markers and all top-level test names, results and durations. Cached test durations describe the cached execution, not the current runner. Successful output is suppressed; build diagnostics and the last 1 MiB of failure diagnostics per top-level test remain in the console, with truncation reported explicitly. Successful-test buffers are discarded when their outcomes arrive so they cannot evict failed or incomplete diagnostics. Raw output is never written to timing artifacts. Malformed timing streams and incomplete successful runs fail validation. The workflow adds a timing summary and preserves only these JSON reports for seven days on success or failure under an OS/shard/revision/attempt-specific artifact name. Empty affected shards emit an empty successful report. Windows timing and cache comparisons remain required before claiming a shorter critical path; separating workspace from Worker does not by itself shorten a single slow test.

Native Git/PowerShell and durable SQLite fixtures on hosted Windows outgrew Go's default 10-minute package budget, and concurrent package suites starved bounded Worker and harness protocols. Independent runners shorten the critical path while retaining serial package execution within each Windows runner and each package's normal test parallelism. Remove `-p=1` only when full native Windows evidence supports concurrent package suites. This scheduling and bounded watchdog do not introduce a timeout for `ach` commands or change product concurrency limits. Bulk scheduler-only queue setup uses one durable transaction so it measures the worker scenario instead of thousands of independent disk flushes.

The independent workspace shard owns durable workspace cleanup and recovery regressions, including bounded journal compaction across fresh recovery owners. The workspace and Worker shards retain their 45-minute package watchdogs. The workspace budget covers the aggregate fixture runtime; every original or recovery operation retains the production five-minute deadline and the bounded recovery-attempt limit. The explicit Windows workspace investigation uses the same 45-minute package watchdog within a 60-minute job. Reassess these larger fixture budgets after native timing evidence permits reduction.

The September 30 baseline [main run 36677902250](https://github.com/delinoio/oss/actions/runs/36677902250/job/109767014132) spent 27m57s in Windows Go tests and 29m52s in that job; [PR run 36678529123](https://github.com/delinoio/oss/actions/runs/36678529123/job/109768905387) spent 26m53s and 29m45s respectively. Both restored Go caches. Windows validation targets a longest Windows job of at most 15 minutes, including setup; this is a target, not measured improvement. Compare two native runs, cache hits and restored keys, the slowest shard, summed Windows runner minutes and cache storage before claiming the target is met.

The initial four-shard [run 36686158655](https://github.com/delinoio/oss/actions/runs/36686158655) passed the complete CI. Its Windows jobs took 17m46s (`core`), 7m00s (`server`), 6m55s (`harness`) and 9m33s (`worker`), totaling 41m14s of runner time. All four missed both scoped and shared Go caches, whereas the baseline restored a 377 MiB Go cache; these are not identical cache conditions. The 69 native packages were covered once (45/2/7/15). The core suite's async-commit-hook tests took 532.267s, while the full Go step took 1,012s; cold compilation and other package work kept the job above the 15-minute target. This evidence motivated the separate compiler phase. PR cache-save steps remained skipped; main-only scoped cache sizes remain unmeasured before merge.

The `async-commit-hook` CI job follows the central change plan and validates the Go runner with race detection and `-count=1`, its local UI, documentation app/client and generated protocol, release fixtures, and all six CGO-free target archives. Its app-owned, uncached `ci:embedded` task waits for app `ci:check` before invoking the unchanged `build:embedded` command. The native task waits for `ci:embedded`, and archive packaging waits for the native task, so neither rebuild can clear the frontend output while app checks read it. Direct `build:embedded` and the release builder retain their existing fresh-build behavior. Its path rule includes the owned source, installers, release workflow and packaging, documentation contracts, and shared Go/Node/protocol inputs; unrelated changes skip it. It uses the shared cache policy and participates in the exact `ci-result` inventory. Its temporary archives remain unsigned and unpublished. The separate manually dispatched `release-async-commit-hook.yml` owns signing, GitHub Release and Homebrew publication; consolidated documentation publication belongs to `public-docs`, and its default dry run cannot enter publication jobs. See [the release contract](cmds-async-commit-hook-release-contract.md).

The `async-commit-hook` job also owns shared protocol freshness for owned schemas and Go bindings under `protos/**` and TypeScript output under the owning generated client directories.. Its `ci:proto:check` dependency runs the uncached `ci:proto:fresh` leaf, which regenerates and rejects tracked or untracked drift even with a warm cache.

The always-running `changes` job computes the execution plan with `scripts/ci/plan.mjs` and `scripts/ci/job-paths.json`. Domain jobs depend on this plan and use job-level conditions, so unrelated jobs do not allocate runners. `ci-contracts` always validates the workflow and planner. Rules cover Go, Rust, every Node workspace, packaging, public documentation and the remaining release workflows.

Job ownership, configuration forcing and workspace forcing share one dependency-free
dot-aware path matcher. Wildcards include hidden files and directories at every
depth; literal-dot patterns retain their literal dots. Match Git's POSIX paths
without removing filename characters, including whitespace, newlines and literal
backslashes. Preserve single-segment wildcards, zero-or-more-directory globstars
and event eligibility. Planning works before pnpm installation. In particular, hidden descendants of `.github/actions/` force every eligible
job. Hidden inputs inside a job's workspace retain ordinary affected execution;
owned inputs outside that workspace force it. Unrelated hidden paths do not
select jobs.

The `release-contracts` job runs the complete top-level `scripts/release/*.test.mjs` suite, including release fixtures shared with other projects. Every test fixture in that suite, committed data under `scripts/release/fixtures/`, and the shared `project.mjs`, `runmoor.mjs`, and `update-homebrew.sh` implementations they exercise select this Ubuntu job on PRs and main pushes. Linux package changes under `scripts/release/linux-packages.mjs`, `scripts/release/linux-packages/**`, and `packaging/linux/**` also select this job, including direct and dynamic helper dependencies and the imported tool pins. These portable fixtures exercise interrupted uploads, immutable objects, concurrent publication, signing and public readback in isolation. Their failure must fail the selected job and `CI Result`. Preserve existing clibox consumers and the native `linux-packages` PR skip. Other project-specific release scripts retain their narrower owners.

The `node-labs-docs-test` Ubuntu job builds and validates the independent Labs documentation site through `labs-docs ci:check`. Its central path rule owns the app, project/app contracts, shared Rspress wrapper and toolchain inputs. External inputs force workspace execution; ordinary app edits retain the exact affected comparison. It participates in `CI Result` and never deploys or provisions DNS. See [the Labs app contract](apps-labs-docs-foundation.md).

The `node-public-docs-test` job owns the consolidated root and subpath publication checks. Its path rule includes `packages/docs-site-switcher/**`. Its public-docs test boundary runs the shared selector interaction suite before building the assembled site, so changes to the shared navigation cannot bypass documentation CI.

Public-docs owns package-local external installer inputs for cached `build`, `build:frontend`, and `ci:routes` tasks. Each hashes `scripts/install/{nodeup,binpm,async-commit-hook,pnport}.{sh,ps1}`; build overrides use `$TURBO_EXTENDS$` to retain root inputs and leave generated outputs intact. Forced job selection does not bypass cache reads. `scripts/ci/public-docs-cache.test.mjs`, included in `pnpm ci:contracts`, checks all eight independent source mutations, complete cold/warm build and route validation, unchanged output restoration, and stale-asset failure with a frozen producer. It uses disposable source/output and local-only cache storage; hosted cache acceptance and production deployment remain separate.

The `node-pnport-test` job owns pnport launcher, packaging, installer and fail-closed release fixtures on affected PRs and main pushes. The `pnport-native` job runs native execution, TypeScript, installed npm/Yarn PnP consumer, archive and direct-installer checks on glibc Linux x64/arm64 for 0.1.0 on affected main pushes. PRs and manual CI skip native execution. Release validation retains all four targets. Both CI and release derive their matrices from the package-owned target registry. Release Project does not wait for this CI matrix. The separate pnport tag workflow independently requires its four-host gate before publication. Windows x64/arm64 is deferred to 0.2.0 with the same complete acceptance requirements.

PRs and manual runs use affected validation and defer Linux CLI and pnport native packaging. Selected main pushes retain Linux/Windows native checks. No CI event allocates macOS runners. The planner publishes event-specific matrices, and `CI Result` validates every matrix and planned job result. Changes to shared CI configuration force all event-eligible checks. Changes to `job-paths.json` compare each rule with the comparison-base revision and force only changed jobs. Missing or invalid comparisons fail. There is no full manual mode or nightly CI schedule.

PR comparisons use the merge-base of the event's base and head commits. Main pushes compare the exact `before..sha` trees, including multi-commit and force pushes. Git diff uses NUL-delimited names and disables rename detection so both old and new owners are selected. Invalid or unavailable revisions fail planning instead of producing an empty change set. Node workspace checks use the committed Turbo binary via `scripts/ci/run-affected.mjs`, invoking its installed Node entry point with the current Node executable instead of spawning a platform-specific package-manager shim or shell. Changes to this shared runner force every eligible CI job so its callers run against the change. The runner preserves literal task/filter arguments and the same `TURBO_SCM_BASE` and `TURBO_SCM_HEAD`. Forced checks and inputs outside the workspace dependency graph omit `--affected`; an otherwise empty affected set is a successful no-op.

PR validation runs through `turbo run` using the committed common runner. Apps and packages own their validation leaves and dependencies; the private `@delinoio/ci` workspace in `scripts/ci` owns repository-wide Go, Rust, workflow, environment and protocol checks. Actions owns tool installation, job selection, platform matrices, temporary PostgreSQL lifecycle, artifact retention and `CI Result`. Release and deployment workflows retain their existing entrypoints. Main currently has no `vp run` execution layer to remove. Workspace TypeScript unit and component suites use Rstest; the pnport external-tool cache-conformance fixture still requires Vitest. The imported fspy sources remain required.

Root `turbo.json`, `package.json`, `pnpm-lock.yaml` and `pnpm-workspace.yaml` changes select `rust-fmt` on PRs and main pushes through the job-specific path rules. This check uses the shared Node/Turbo runner and retains its complete native command with `FORCE_RUN: 'true'`. The root inputs do not add global forcing or change event-specific packaging eligibility. `CI Result` requires each selected check to succeed.

Go setup exports resolved `GOCACHE` and `GOPATH` through task-local pass-through entries so Windows cache discovery does not require general user-profile environment access.

Chrome extension CI orders `test:package → build:test → ci:zip`. The uncached package regression completes both clean builds across time zones before the final build executes or restores cached `dist`, `build` and `artifacts` outputs. ZIP parity validation follows that final build. These tasks must not write their shared output directories concurrently. Controlled cold/warm fixtures pause packaging after artifact removal and verify that neither the final build, cached output restoration nor ZIP validation can proceed; builder and reproducibility failures must fail the aggregate before ZIP validation. Type checks, pure tests and policy checks retain independent execution. The fixture checks explicit scheduler dependencies and its recorded operation sequence; summary wall-clock timestamps do not establish causal ordering, including for cache restoration.

`setup-turbo-cache` exchanges GitHub OIDC identity through the official Vercel action pinned to commit `49d7b1b46ba4c9251e1977986bfe18336feabc8f`. The `delino` team policy matches repository `delinoio/oss` and GitHub's `workflow` name claim `CI`. The shared authentication action additionally checks the exact `delinoio/oss/.github/workflows/CI.yml@` workflow-ref prefix before token exchange; repository variable `TURBO_TEAM=delino` selects the team, with optional `TURBO_OIDC_POLICY` selecting an exact policy. Only those validation jobs receive `id-token: write`. The short-lived token grants Remote Cache access only, is revoked by the action after the job, and is not a deployment or release credential. Authenticated main pushes and main manual runs read and write successful cacheable tasks. Same-repository PRs and non-main manual runs use remote read-only mode. Fork PRs skip authentication. Missing policy/configuration or a failed exchange records local-only mode; Turbo cache transport warnings remain visible while local validation continues. Never retry or suppress a task failure as a cache failure.

Task hashes include source/default workspace inputs, declared external inputs and dependency builds. `CI_CACHE_CONTEXT` supplies OS, architecture, the actual Node version, package-manager pin and Node pin. Go checks, rustfmt and protocol tasks additionally hash their own `CI_GO_CACHE_CONTEXT`, `CI_RUST_CACHE_CONTEXT` or `CI_PROTO_CACHE_CONTEXT`; repository tool pins remain explicit file inputs. Version queries must not install compilers: Go uses `GOTOOLCHAIN=local`, and Rust uses `rustup run` without its opt-in `--install`. JS hashes therefore remain reusable across jobs with different native prerequisites; build selectors such as `TAURI_ENV_PLATFORM` and `TAURI_ENV_TARGET_TRIPLE` are hashed by their owning frontend task. There is no new global environment or pass-through list; development's exact selector/tool/session allowlist is unchanged. Cache type checks, pure JS tests, static contracts, protocol generation and frontend products with explicit generated outputs. Go/Rust execution, database/OS/render/benchmark checks, repeated clean builds, embedded UI generation and protocol freshness remain uncached. Freshness directly regenerates and checks tracked plus untracked generated files on every call. Store Turbo JSON run summaries as workflow artifacts; they report task/cache outcomes without tokens or user state. The disposable rustfmt cache fixture records the exact production formatter command's joined completion, exit status and expected diff before returning its status to Turbo. Every miss must provide fresh completion evidence; a hit must not execute the formatter. Missing forwarded Turbo console output cannot substitute for, or invalidate, that evidence.

Validate local cold/warm restoration in an isolated workspace, then mutate source, dependency, tool pin and build options and prove invalidation. Prove native/freshness failures still fail a warm run. Hosted acceptance requires an actual successful main cache write followed by a comparable PR remote hit; record source revisions, completion time, summed runner minutes and hit ratio in the PR and CI artifacts. A local hit or first unseeded PR cannot establish remote performance improvement.

Every pnpm install uses `--frozen-lockfile --ignore-scripts` and runs only once per selected Node workspace job. Local setup actions restore pnpm and Go caches scoped by OS, architecture, tool version, and lockfile. Explicit final save steps write only after successful main validation; Rust caches use the same main-only policy with failure caching disabled. Rustfmt has no Cargo dependency cache; its deterministic formatting result uses Turbo and Node tooling uses the existing pnpm policy. PRs restore eligible main caches but never save branch-scoped caches, so dependency-update PRs may repeat downloads until merged. Native output caches and existing artifact retention policies are not expanded. Go timing metadata has its separate seven-day retention described above.

`setup-ci-go` accepts an optional `cache-scope`, empty by default to preserve existing callers' exact keys. Repository Go tests use `go-test-all` on Linux/macOS; contracts, quality, ach and API use separate producer scopes. Ubuntu PR shards restore the main `go-test-all` producer scope and never save caches; they do not create shard-specific Linux keys. The workspace fallback scope applies only on Windows. This prevents a short contract job from freezing the shared immutable key before a larger compiler job can save its populated cache. Windows Go tests use the `go-test-core` scope. A scoped restore first checks its OS/architecture/toolchain scope and then the existing shared cache. Other callers retain their restore priority and exact primary keys. Successful main jobs alone save their own primary keys; PRs never save. No source-revision key or unbounded per-run cache is introduced.

Generated protocol output and package-local frontend output are deterministic cacheable Turbo products. Existing affected comparison and forced shared-input rules remain in force.

The `ci-result` job retains the `CI Result` name and always evaluates every required dependency. Planning and CI contracts must succeed. Every planned job must succeed and every unselected job must be skipped; failures, cancellations, missing jobs, invalid plans, and unexpected skips or execution fail the aggregate. The changes job publishes a structured decision log, mode, exact comparison SHAs, selected job count, platform matrices and a job-selection table in its run summary. `CI Result` requires exact agreement for every published platform matrix with the event and mode even when its job is unselected; missing, malformed, empty or altered matrices fail validation.

For a paid-runner comparison, main run `36053972411` used 169.7 macOS runner minutes, or approximately $10.52 at $0.062 per minute. This is the Mac work shifted out of ordinary CI for an equivalent affected run, not a measured post-change saving. A product release pays for its mandatory Mac validation once. Compare equivalent before/after runs by OS-specific runner minutes and hypothetical bill, and distinguish local or release platform acceptance from ordinary CI. Public-repository hosted compute is currently unbilled.

For performance comparisons, record workflow completion time, summed per-job execution minutes, selected native matrix entries, and repository cache bytes. Baseline run `35320469091` completed in approximately 48 minutes with 415.5 runner minutes: 266.2 desktop, 117.9 mobile, and 31.4 other. Deferring those 17 entries removes about 92% of that sample's PR runner usage before accounting for retained frontend fixtures and planning overhead; this is a counterfactual estimate, not a measured post-change improvement. Compare equivalent changes and cache states after hosted execution. Public-repository standard hosted compute is free; cache/storage and developer waiting time are separate measurements.

Run the repository CI contracts locally from a clean checkout with:

```sh
pnpm install --frozen-lockfile --ignore-scripts
pnpm ci:workflows
pnpm ci:contracts
pnpm ci:release-fixtures
```

Package-local commands are documented in the applicable workspace README. Use `pnpm ci:affected <task> --affected --filter <workspace> --dry=json` to inspect affected execution without downloading a different Turbo version.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## Selected CLI release and bot ownership

`release-project.yml` is the manual `Release Project` entrypoint. Its required choices are `project` (`binpm`, `cargo-mono`, `nodeup`, `with-watch`, `runmoor`, `clibox`, `pnport`, `async-commit-hook`) and `bump` (`patch`, `minor`, `major`, pnport-only `next`, default `patch`). It accepts only `main` in `delinoio/oss`. Workspace registry publication is owned by the separate main-push `Publish crates` workflow; this manual entrypoint retains selected-project version and remote-tag ownership. Documentation deployment remains separate.

`publish-crates.yml` runs on pushes to `main` in `delinoio/oss`, including merged PRs. It checks out the exact event SHA with full history, hydrated LFS assets and no persisted credentials. It installs the pinned Rust toolchain and prepares cargo-mono through `scripts/ci/cargo-mono-prebuilt.mjs`, reusing its pinned first-party archive/executable SHA-256, size and version checks with no source fallback. The verified binary runs `publish --all`: cargo-mono orders publishable workspace crates by dependencies, skips `publish = false` packages and already-published versions, and uploads remaining versions using `CARGO_REGISTRY_TOKEN`. It does not bump versions or wait for main CI. Missing credentials or publication failures fail the job. The token is scoped to the upload step. Contents authority is read-only; any configured cargo-mono tags remain local and remote tag publication stays manual. Automatic and manual publication share `release-project` concurrency without canceling an active upload; GitHub retains only one pending run, so a newer pending run can replace an older one. Rerunning a failed automatic run reuses its exact source SHA and skips versions already published.

The private organization-owned GitHub App `delino-release-bot` has only Contents write and implicit Metadata read, no webhook subscriptions or user authorization, and selected installation access to `oss` and `homebrew-tap`. Both the organization and repository main rulesets must allow this app to bypass directly; other rules and actors are preserved. `DELINO_RELEASE_BOT_CLIENT_ID` is an Actions variable and `DELINO_RELEASE_BOT_PRIVATE_KEY` is an Actions secret in `oss`. Neither private keys nor installation tokens belong in files, artifacts, logs, Git URLs, or Git configuration. `actions/create-github-app-token@v3` obtains a fresh repository-scoped token immediately before each write phase and revokes it afterward. The app has no Actions or Administration write permission. Release-source inspection and same-repository release uploads use the built-in token. Registry uploads retain `CARGO_REGISTRY_TOKEN`.

The release workflow serializes its runs without canceling an active release. GitHub's default concurrency queue retains one pending run; a newer pending request replaces an older pending request. Each new run fetches current main, validates exact stable SemVer, and commits only the selected version source plus its Rust lock entry; clibox also updates its private npm source manifest in the same version-only commit. pnport updates both private Cargo package versions, both lock entries and its npm source version together. async-commit-hook updates exactly four version fields together: its Go constant, local UI/client manifests, and release metadata. All four must agree before preparation; missing or ambiguous declarations fail before writes, and unrelated bytes remain unchanged. Shell and PowerShell installer defaults independently resolve the highest published stable `async-commit-hook@v<version>` release and are not version-preparation fields, so a prepared but unpublished commit cannot become an install target. Rust manifests and lock entries must agree. Runmoor uses its existing `Version` constant. Minor and major bumps reset their lower components. Runmoor uses the stable release channel independently of the bump level.

The bot commit journals the repository/run ID, project, bump, and previous version. A rerun finds and validates that exact commit and its complete parent-to-child version-only diff instead of bumping again, even after main advances. Pushes are fast-forward only; conflicts fail without rebasing or force. After the prepared commit passes immutable release-source validation, Rust targets other than clibox and pnport publish with `cargo mono publish --package <project>` without waiting for main CI. Only after registry publication succeeds does the coordinator push the single `<project>@v<version>` tag at the recorded commit with the bot token. Go, clibox, and pnport releases use the same path without a registry upload. For clibox, pnport, and async-commit-hook, the registry job retains immutable source validation but skips Rust installation, caching, and publication; the preparation step neither requires nor injects a Cargo registry token. The job still succeeds so its dependent tag phase proceeds normally. Already-published crates can resume a missing remote tag, but a conflicting existing tag is always rejected. Tags are never moved or deleted for recovery.

For binpm, cargo-mono, nodeup, with-watch, runmoor, clibox, and pnport, the tag triggers its owning project release workflow asynchronously, preserving its signing identity, platform matrix, assets, and Homebrew behavior. Those Homebrew writes use a fresh tap-only app token and the bot commit identity. async-commit-hook is preparation-only: its tag triggers no publication workflow, and its successful summary links to the separate manual release with the prepared version and exact commit requirement. That workflow retains its main-only signing identity, dry-run default, protected environment and existing Homebrew credentials. For all eight projects, the coordinator follows `prepare → registry → tag → summary` without querying or waiting for main CI or downstream release completion. The version commit still triggers the independent main-push `CI.yml` workflow; pending, failed, canceled, or missing CI results do not block registry publication or tag creation. Downstream build, test, signing, and publication checks remain enforced by their owning release workflows. There is no implicit redispatch, tag replacement, or public asset overwrite by the coordinator. A failed preparation, registry, or tag phase is repaired and retried through the same coordinator run to reuse any recorded version. A failed downstream tag workflow is rerun from its own Actions page and does not require retrying the coordinator when the tag push succeeded. Every run reports version, commit/tag identity, and preparation/registry/tag results without a CI result or CI run link; downstream status belongs to the owning tag-triggered or separately dispatched workflow. For async-commit-hook, select main and the prepared version explicitly in the separate manual release. Its existing tag check rejects publication if the dispatch commit differs from the prepared tag, including when main advances; historical-source publication and tag movement are not added by this integration.

The four legacy archive publishers (`binpm`, `cargo-mono`, `nodeup`, and `with-watch`) use `scripts/release/legacy-cli-release.mjs` before downstream Homebrew work. Changes to this helper select `release-contracts`, which owns the top-level release fixtures. Generate the sorted unsigned `SHA256SUMS` from the retained build artifacts before any signing or upload. Resolve the remote tag to the exact source revision, enumerate every releases-list page, reject duplicate same-tag releases, and read the selected numeric release ID. A complete public release is reusable only when every expected unsigned asset matches those bytes and every Sigstore bundle verifies the owning workflow, exact tag or main identity, GitHub OIDC issuer, source commit and repository. Validate asset names, unique IDs, uploaded state, sizes and any supplied digests. This path performs no signing, deletion, upload or release edit and allows a failed tap update to resume. Missing, unexpected, duplicate or conflicting public assets and invalid signatures block before tap credentials or work. Public `target_commitish` can be a branch name; the resolved tag remains its source authority.

Fresh publication creates a source-bound stable draft after signing and verification. Recovery may complete only a draft whose `target_commitish` explicitly matches the source commit. Verify its entire existing inventory before signing, preserve valid existing bundles, sign only missing bundles and upload only missing assets. Recheck tag/source and unique release identity before writes, then download and verify the complete signed draft by pinned ID before publishing. Confirm the public state and signatures before downstream work. Unknown mutation outcomes stop the attempt; the next run reconciles remote state without an automatic write retry. Dry runs produce unsigned checksums without network publication or signing. Keep the existing platform and standalone asset inventories, including cargo-mono's legacy unqualified Unix binary. Recovery uses the retained exact artifact bytes; a rebuild with different archive bytes fails even at the same source revision. Missing or expired retained artifacts require maintainer recovery, never public asset replacement or a moved tag.

Source version tests must not pin the current Runmoor release literal. Release tests use temporary Git repositories and injected API responses, never real tag, registry, or deployment mutations. Validate with `pnpm ci:workflows`, `pnpm ci:contracts`, and the project release tests, including `node --test scripts/release/legacy-cli-release.test.mjs` for immutable public reuse, blocked conflicts, tap retry, fresh/partial drafts and signed readback. These fixtures and static workflow checks do not establish hosted publication or real tap acceptance. Initial app setup and validation do not dispatch a release.

## APT and DNF publication

The seven CLI release workflows call `release-linux-packages.yml` after GitHub Release publication and explicitly inherit secrets so its guarded publisher can resolve the protected environment; other reusable jobs never reference production credentials. Release Project ends after its tag phase; operators must separately await the exact downstream tag workflow, including protected native package publication and public installation verification, before starting the next project. All seven CLIs, including Runmoor and clibox, use stable; preview remains reserved. Follow `repository-linux-packages-contract.md` for exact inputs, package versions, tool pins, signatures and recovery. CI retains the `linux-packages` domain job in `CI Result`; it uses temporary keys and repositories on native amd64/arm64 hosts for relevant main pushes and every manual dispatch. The shared planner classifies it as native packaging, so PRs skip both architectures even for CI configuration changes. `CI Result` accepts that planned skip and still requires success whenever the job is selected. General Linux checks, static package contracts, and release-time packaging validation remain enabled. Only the Linux package publisher's job-level concurrency group uses `queue: max`. The actionlint compatibility adapter validates that exact declaration and rejects every other queue declaration.

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

`CI.yml` runs `node-clibox-test` on Linux and Windows, with one frozen script-disabled workspace install, `cargo test --locked -p clibox -p clibox-config -p clibox-system -p clibox-transform -p clibox-wait` for unit/process/adapter readiness and configuration-command and transformation conformance, the shared affected planner/Turbo comparison, package fixtures, and temporary npm/pnpm consumer installs exercising JSON file readiness, environment execution, help/version, dotenv list/merge, YAML normalization, and all seven the feature transformations. Each OS also exercises the affected runner against temporary workspaces, including empty selection, forced execution, and failure propagation. External Cargo and release inputs force this workspace's checks. The job participates in `CI Result`. Native integration tasks are not cached; successful-main-only dependency cache rules remain unchanged.

`release-clibox.yml` builds eight native binaries (macOS and Windows MSVC x64/arm64; Linux glibc/musl x64/arm64). GNU builds run inside the shared digest-pinned AlmaLinux 9 image on native x64 and arm64 runners, with glibc 2.34 and baseline CPU/ELF checks. Both musl jobs install `musl-tools` and select target-specific `CC=musl-gcc` for Rustls/ring C compilation; final binaries use the pinned Rust toolchain's `rust-lld` and self-contained runtime objects, execute on the native build hosts, and run npm/pnpm consumer smoke tests in Alpine. Windows uses the MSVC toolchain. macOS binaries use the deployment baseline of the repository-pinned Rust target. Each build runs native unit/process tests for `clibox`, `clibox-config`, `clibox-system`, `clibox-transform`, and `clibox-wait`, checks executable version, and creates a platform npm tarball. Rustls/ring adds no dynamic OpenSSL dependency; Alpine consumer images include `ca-certificates` for OS-trusted HTTPS. The assembly job adds the launcher, verifies exactly nine source-commit/version-bound tarballs, and uploads `clibox-npm-<version>-<revision>` with 30-day retention. Native intermediate artifacts are named `clibox-native-<suffix>`. A separate exact-tag guarded `publish-release` job verifies the full nine-tarball set, copies its two GNU executables into deterministic Linux archives, signs those archives and SHA256SUMS with Sigstore, and publishes through a verified recoverable draft. Existing assets are immutable; verified signatures and complete public releases are reused. The common native workflow follows that GitHub Release. The npm enable flag gates npm only. The same complete npm artifact also supplies deterministic macOS x64/arm64 archives, with full Apache-2.0 terms, NOTICE and the original fspy-family MIT notice. Four archives and SHA256SUMS each have a source-bound Sigstore bundle. After GitHub publication, `homebrew-test` verifies all ten public assets and the source identity and installs/tests/audits the Formula on both native Mac architectures. `homebrew` revalidates both tested Formula artifacts before creating a fresh tap-only release-bot token and publishing the identical Formula. `homebrew-readback` installs/tests the public tap on both architectures. No Homebrew publication depends on npm enablement. Dry runs render the candidate Formula without publication credentials or public installation. Rerun failed downstream jobs using the existing exact tag and retained artifacts; the tap refuses downgrades and different Formula bytes at the same version.

Manual dispatch defaults to dry-run and may validate a development ref. Non-dry-run publication requires the exact first-party version tag. Build/assembly/dry-run jobs have read-only contents permissions and no signing or registry credentials. Only the separately guarded npm `publish` and GitHub `publish-release` jobs have `id-token: write`, for npm provenance and Sigstore signing respectively. The npm job requires `CLIBOX_NPM_PUBLISH_ENABLED=true` and the exact tag/source commit. Neither npm nor GitHub publication queries crates.io; both retain source-version and complete artifact verification. It uses Node.js 24, explicitly installs pinned npm 11.6.2 before validating the 11.5.1+ OIDC requirement, and publishes with OIDC and provenance. All platform packages are published and their npm registry integrity confirmed before the launcher. Existing identical tarballs are reused; any conflicting remote integrity fails before new writes. Rerun only failed jobs after partial publication so the original complete artifact is reused. Expired/missing artifacts require maintainer recovery; never overwrite an npm version or move its source tag.

All nine packages require a Trusted Publisher permitting publication from `delinoio/oss` and `release-clibox.yml`. Setting `CLIBOX_NPM_PUBLISH_ENABLED=false` or leaving it unset disables npm publication while retaining validated artifacts; it does not disable the separately guarded GitHub Release or native package jobs. Publisher configuration and retry requirements are in [the npm distribution contract](packages-clibox-distribution-contract.md). No setup or validation command dispatches a workflow or publishes a registry version.

Repository-wide Go quality/tests and ach-specific compilation must first generate the app-owned ach UI embed using `pnpm --filter async-commit-hook build:embedded`. The root Go checks also retain the existing administrator embed prerequisite. The consolidated `public-docs` build renders the async content under `/async-commit-hook`; the executable release builder regenerates the local UI before cross-compilation.

The scoped Go formatting helper discovers tracked files with `git ls-files -z` and passes original NUL-separated paths as literal `gofmt -l` arguments. Git display quoting and embedded newlines must not omit or split files. Checks leave source bytes unchanged and retain discovery, formatter, missing-scope and empty-inventory failures. `scripts/ci/check-go-format.test.mjs` belongs to the existing central CI contract fixture suite.

Native Tauri consumers restore/verify the immutable execution-host CLI through `.github/actions/setup-prebuilt`. CLI keys bind release, host and both SHA-256 digests; successful main jobs alone save these caches. App Cargo caches remain separate. Root and native-package compilation use `nightly-2026-09-28`. Linux CEF jobs install GTK4, XDG portal/GTK portal and `zenity` alongside existing package prerequisites. Follow [prebuilt dependencies](repository-prebuilt-dependencies-contract.md).

## Workflow integration

- The five legacy CLI release workflows use `scripts/release/legacy-cli-release.mjs` before Homebrew credentials or writes. Follow the repository workflow contract: reuse only a complete public inventory with exact retained bytes and source-bound signatures; never sign, delete, replace, upload or edit on that path. Reject conflicting/incomplete public inventories. Fresh and owned partial drafts upload only missing assets and require complete signed readback before publication. Keep unsigned dry runs, exact tag/source checks and downstream job ordering.

- Every workflow step reaching the shared protocol breaking check must pass `PROTO_BASELINE`: push events use `github.event.before`, manual main runs use `HEAD^`, and PR/manual non-main runs use `origin/main`; the manually dispatched async-commit-hook release workflow uses `HEAD^`. Preserve full-history checkout and the Turbo leaf's environment pass-through, including indirect async-commit-hook and Go-binding paths. Keep schema-only LFS handling and no-prior-schema behavior unchanged.

- The `async-commit-hook` job owns shared protocol freshness for owned schemas and generated bindings on PRs and main pushes. Keep the freshness leaf uncached and reject tracked or untracked output drift.

- Generic Rust CI uses the single verified prebuilt cargo-mono selection and exact comparison/fallback policy in `repository-workflow-contract.md`. Validate the published executable with offline live fixtures before planning; no source fallback or binary cache. Preserve final package/job agreement, pnport runtime ownership, scene exclusions, conditional native preparation, shared-input full checks and independent project validation.

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

- Go validation uses the central event-owned `go_test_matrix` and `CI Result`. PR and affected manual validation use one Ubuntu core shard. Main Linux validation uses all packages; Windows uses the core shard. macOS remains excluded from these events.

- clibox Homebrew follows its exact-tag GitHub Release independently of npm enablement. Both native macOS installation/audit gates and signed public-source verification must precede tap-only app-token creation. Publish only the identical tested Formula, then verify public-tap installation on both architectures. Dry runs never enter Homebrew publication; keep the release summary and workflow contracts synchronized.

- Shared Tauri CLI preparation uses `setup-prebuilt` and execution-host lock selection; restore/verify on native and mobile paths, save only after a successful main job, and retain existing application Cargo caches.

- PR CI validation uses package-owned Turbo leaves and the private `scripts/ci` workspace under `repository-workflow-contract.md`. Preserve complete assertions and native/clean/freshness gates; cache-only OIDC access does not grant release authority. Keep affected selection, development environment allowlists and final generated-dist cleanup intact.

- Load repository-local actions only after checkout. Cache authentication requires the `delinoio/oss` repository, GitHub workflow name `CI` and exact `CI.yml` workflow-ref prefix. Prepare the pinned Rust toolchain before native tasks; export prepared Go cache locations explicitly for Turbo strict environments.

- The separate Runmoor PR Docker workflow also uses uncached `scripts/ci` Turbo tasks, preserving the explicit Docker opt-in, race coverage, read-only contents authority and successful-main-only dependency-cache saves. Both Go test commands use `-count=1` so successful result-cache entries cannot skip validation of the current subprocess-built command. The Runmoor release build gate uses the same flag for its plain Go tests. Compiled-object and module caches remain reusable. It requires no remote-cache token.

## Project requirements

- Agent Worker ordered source routes follow the catalog, desktop, protocol and sessions contracts. Main reservation the originating change owns System capability 36 and SaveAgentWorkerRequest.route_models field 5; preserve capability 35. Keep schema-3 routes exclusive with legacy fields, all models/accounts referenced and saved atomically, per-source routing state updated only with a successful first claim, confirmed-exhaustion-only fallback and observed-recovery preference for later new sessions. Preserve immutable executions, old-client write protection and portable v1/v2/v3 compatibility; add no SQLite migration.

- Go CI package watchdogs remain independent of product deadlines.

- After completing each task, update the relevant `AGENTS.md` and `docs/` files in the same change when policies, structure, or contracts changed.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

- Root `pnpm install` must install Lefthook in linked worktrees when the effective `core.hooksPath` resolves to Git's shared common-directory hooks path, preserve Lefthook's protective failure for unrelated custom hook paths, and skip hook installation without blocking app preparation when Git metadata is unavailable.

- Renovate ordinary branch creation, existing branch updates and lock file maintenance are limited to Mondays from 00:00 inclusive to 04:00 exclusive in `Asia/Seoul`. Keep `updateNotScheduled: false` and preserve immediate vulnerability-fix handling, shared presets, automerge and release-age rules. Follow `repository-workflow-contract.md`; repository schedules restrict branch work, not the hosted service's scan cadence.

- Root `go.mod` selects the Go security baseline, currently Go `1.26.8`; keep the API/sweeper Docker build image on that exact version. CI reads the module selector.

Repository-wide quality CI is defined in `.github/workflows/CI.yml`. Go validation uses an event-owned `go_test_matrix` under the existing `go-test` job: PRs allocate only Ubuntu, including forced and shared-input checks; main pushes retain complete Ubuntu suites and native dependency-aware affected selection on the Windows core shard; manual runs use affected Ubuntu PR shards. Shared inputs and forced validation select complete suites only on the hosts eligible for that event. PR and manual Go results establish Linux coverage only; Windows regressions are checked on main and macOS acceptance remains local or release work. `CI Result` must reject a missing or altered event matrix. Test imports, subprocess command consumers and real UI embed owners must remain in the selection graph. Unknown/deleted inputs expand conservatively; discovery/comparison errors fail and empty affected shards are logged successful no-ops. Windows partitions the native `go list ./...` inventory exactly once, precompiles selected test binaries without running them at default compiler parallelism, then preserves `-p=1` for complete test execution per runner. Every central Go test execution uses `-count=1` in full and affected modes so selected tests and `TestMain` run even when successful results are cached; compiled-object caches remain reusable. The workspace package and descendants run on their own Windows runner. Timing summaries and seven-day artifacts retain source revision, commands, phase durations and top-level test metadata without raw output or dynamic subtest names. Shard-scoped caches are saved only after successful main validation. The Windows Worker and workspace shards and explicit workspace investigation use a 45-minute per-package watchdog; all other Go CI packages retain 20 minutes. These are fixture scheduling limits, not product command timeouts. Follow `repository-workflow-contract.md` for shard ownership and measurement.

Coverage expectations:

- `go-quality`: generates and validates the ignored administrator and ach UI bundles, then checks formatting without rewriting sources and runs `go vet` on the selected native packages on Ubuntu. Main runs retain the full suite; manual runs select affected packages.

- `go-test`: generates and validates the ignored administrator and ach UI bundles on every allocated runner, then runs the complete tests of selected packages through `scripts/ci/go-test.mjs`. PRs use Ubuntu only; main uses full Ubuntu and affected Windows packages; shared/forced inputs select full suites on eligible hosts and manual runs use affected Ubuntu PR shards.

- `rust-fmt`: runs `cargo fmt --all --check`.

- `rust-clippy`: prepares selected desktop frontends and WebKitGTK 4.1 development prerequisites, then runs `cargo clippy --workspace --all-targets --all-features -- -D warnings`.

- `rust-test`: prepares selected desktop frontends and WebKitGTK 4.1 development prerequisites, builds pnport and its injection companion with `cargo build --locked -p pnport -p pnport-preload`, then runs `cargo test --workspace --all-targets`.

- `node-public-docs-test`: runs `pnpm install --frozen-lockfile --ignore-scripts` and `pnpm --filter public-docs test`, covering the root and all seven project content sections.

- `node-clibox-test`: runs `cargo test --locked -p clibox -p clibox-config -p clibox-fspy -p clibox-system -p clibox-transform -p clibox-wait` for native utility/configuration/process/adapter and file-access behavior, including isolated Windows Ctrl+C/Ctrl+Break readiness cancellation and Node launcher result preservation. Native CLI consumer installation and launcher/distribution tests run on Linux and Windows, selected by shared CI planning and required by `CI Result`.

- `node-pnport-test`: checks launcher and package contracts, version synchronization, immutable artifacts, installer rollback, and fail-closed release publication on affected PRs and main pushes.

- `pnport-native`: on affected main pushes, runs the two glibc Linux x64/arm64 native targets for 0.1.0, installed npm/Yarn PnP consumers, TypeScript conformance, archive packaging, and direct-installer smoke. Each installed binary must also pass native hidden-cache conformance and the prepared offline default-config Vitest suite in inline/split form before benchmarking. PRs skip this native matrix; the pnport tag workflow independently requires the same four targets before publication. The package-owned development matrix also selects native Windows x64/arm64 hosts on those affected main pushes. Windows runs source and installed-development-archive filesystem, process and cache tests through scripts/ci/pnport-windows.ps1; it does not emit complete-release evidence or bypass pending npm/Yarn, PowerShell, watch/loader, benchmark and minimum-OS acceptance. PRs and affected manual runs retain their existing native-job exclusion. Windows x64/arm64 retains the complete acceptance requirements for 0.2.0.

- `node-public-docs-test`: runs `pnpm install --frozen-lockfile --ignore-scripts` and `pnpm --filter public-docs test`.

- `ci-contracts`: validates workflow syntax and the repository CI contract with the checked-in Go `actionlint` tool and Node fixtures on Linux. Its Windows row runs the uncached installed-Buf launcher fixtures through `ci:proto:launcher`; Linux covers those fixtures in the full contract suite.

- `async-commit-hook`: follows the central change plan, runs Go race tests, local UI/docs/client tests, protocol freshness and release fixtures, and builds all six unsigned target archives. Shared setup actions restore caches; only successful main validation saves them.

- `release-contracts`: runs every top-level `scripts/release/*.test.mjs` fixture, including cross-project release tests, without exercising publication. Changes to those test files, committed data under `scripts/release/fixtures/`, `scripts/release/linux-packages.mjs`, `scripts/release/linux-packages/**`, or `packaging/linux/**` select this job on PRs and main pushes. Keep native Linux package assembly skipped on PRs and require the portable fixture result through `CI Result`.

- `ci-result`: retains the `CI Result` status and checks every dependency against the exact `changes` plan; failed/cancelled jobs, missing dependencies, and unexpected skips or execution fail the aggregate.

Change-scoped execution rules:

- Generic Rust tests and Clippy consume one final `rust_packages` selection from the central changes job through the verified first-party cargo-mono 0.6.9 Linux x64 binary; never build the selector from source in CI. Include transitive manifest dependents and the explicit pnport-preload runtime test owner. Shared/forced inputs, unsupported exact comparisons and changed PR merge graphs retain full validation. Empty selection disables both jobs and must agree with CI Result. Preserve scene exclusions, rustfmt, dedicated native/release gates, conditional desktop/sidecar/companion preparation and successful-main-only caches. Follow `repository-workflow-contract.md`; keep the Tauri recipe lock independent.

- A single `changes` job selects domain jobs before runner allocation using `scripts/ci/job-paths.json` and `scripts/ci/plan.mjs`. `ci-contracts` always runs. Go tests allocate Ubuntu only on PRs and affected manual runs, Ubuntu/Windows on main pushes. Environment checks use Linux/Windows.

- PRs and affected manual runs do not allocate Linux CLI or pnport native packaging. No CI event allocates macOS runners. Main retains selected Linux/Windows checks; owning release gates remain authoritative.

- PR comparisons use the base/head merge-base; main comparisons use the exact `before..sha` trees, including all commits in the push. Missing or invalid comparisons fail. Deleted and renamed files select both affected owners.

- Node workspace jobs use `scripts/ci/run-affected.mjs` to invoke the installed Turbo Node entry point directly with `turbo run <task> --affected --filter <workspace>` arguments, without a shell or package-manager shim, and with the planner's exact `TURBO_SCM_BASE` and `TURBO_SCM_HEAD`. External inputs and forced runs omit `--affected`; an otherwise empty affected set is a successful no-op.

- Because `public-docs` builds six project content roots directly, changes under `apps/public-docs/docs/{async-commit-hook,binpm,nodeup,runmoor,clibox,pnport}` select and force `node-public-docs-test`.

- Central path rules cover Go, Rust, every Node workspace, packaging, public docs, and package/release/review workflows.

- Protocol generation and package-local frontend outputs are deterministic and cacheable; the ignored ach UI embed, native package, signing, release, and deployment tasks remain non-cacheable.

- Changes to `.github/workflows/CI.yml`, `.github/actions/**`, or central planning/result logic force every check eligible for that event. Changes to `scripts/ci/job-paths.json` compare the previous and current rules and force only changed eligible jobs. PRs still exclude native packaging; manual dispatch selects affected eligible domain jobs from the exact comparison.

- CI installs always use the frozen pnpm lockfile with `--ignore-scripts`. Shared pnpm/Go setup actions restore caches scoped by OS, architecture, tool version, and lockfile; only successful main jobs save them. Rust compilation caches likewise save only on successful main jobs; rustfmt has no dependency cache. PRs may restore main caches but never create branch-scoped caches. The Runmoor workflow uses the same Go cache policy and cancels superseded executions on the same ref.

- CI is read-only: it does not consume release secrets, push tags or images, create releases, upload stores, deploy services/docs, or mutate updater/controller state.

- When build or test commands change in project contracts, update this section and `.github/workflows/CI.yml` in the same commit.

Release automation baseline:

- CLI release orchestration is owned by `repository-workflow-contract.md` and the manual `Release Project` workflow: only binpm, cargo-mono, nodeup, with-watch, runmoor, clibox, pnport, async-commit-hook are selectable. Main-push workspace registry publishing uses the separate `Publish crates` workflow and shares the manual coordinator’s concurrency group. Version commits and individual release-tag pushes use the repository-scoped `delino-release-bot` GitHub App; Homebrew uses a separate tap-scoped token. Preserve exact release-source validation, version-only run-ID recovery, non-forced pushes, and existing signed artifact workflows. Keep bot keys/tokens out of files, artifacts, logs, Git URLs, and configuration.

- Trigger contract: `release-project.yml` accepts only manual `main` runs in `delinoio/oss`, with closed `project` and `bump` choices. All selectable projects proceed from version preparation through registry validation/publication and the exact tag push without inspecting or waiting for main CI. Main CI runs independently; pending, failed, or canceled CI does not block release orchestration. For Rust targets that publish to crates.io, registry publication must still succeed before the tag push.

- The coordinator ends after the verified release-tag push. Tag-triggered project release workflows, run asynchronously; downstream release failures are repaired from that workflow's Actions page and do not require retrying the coordinator when the tag push succeeded. async-commit-hook is preparation-only: its tag never starts publication, and its summary directs maintainers to the separate manual release workflow.

- Legacy binpm, cargo-mono, nodeup and with-watch publishers follow `repository-workflow-contract.md`: verify exact retained unsigned bytes and source-bound Sigstore bundles before signing or writing. Reuse complete public releases with no release/signing mutations so downstream tap recovery can proceed; reject incomplete or conflicting public state. Fresh publication and explicitly source-owned partial drafts require complete signed readback before publication, missing-asset-only uploads, paginated unique discovery and pinned release IDs. Dry runs remain unsigned and nonpublishing; never move tags or automatically redispatch for recovery.

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

- `release-with-watch` is defined in `.github/workflows/release-with-watch.yml`.

- Trigger contract: runs on tag push `with-watch@v*` and supports `workflow_dispatch` (`version`, `dry_run`).

- Distribution contract: publishes signed multi-OS with-watch release artifacts for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, including standalone prebuilt binaries (`with-watch-<os>-<arch>[.exe]`) and archive assets (`with-watch-<os>-<arch>.tar.gz|zip`), then updates Homebrew (`with-watch`) from GitHub release prebuilt archives (`darwin-amd64`, `darwin-arm64`, `linux-amd64`, `linux-arm64`).

- The CEF maintainer summary must succeed before the metadata report upload, whose retention remains 35 days. Release fixtures must parse the actual summary heredoc and execute the workflow summary step with synthetic zero-signal and truncated-signal reports, checking revision/comparison/signal metadata without exposing raw commit messages, credentials, or native content.

- Project ID `async-commit-hook` owns `cmds/async-commit-hook`, `apps/async-commit-hook`, `apps/public-docs/docs/async-commit-hook`, `protos/async_commit_hook/v1` and `packages/async-commit-hook-api-client`; only executable `ach` is distributed.

- Follow `project-async-commit-hook.md` and its domain contracts. The feature applies with the owner's recorded exclusions of actual six-target machine validation and actual public publication.

- `Release Project` prepares async-commit-hook versions and tags only. Keep the Go version, local UI/docs/client package versions and release metadata synchronized in one version-only commit; installer defaults must resolve the latest published stable release so an automatic Public Docs deployment cannot target an unpublished prepared version. Reject drift and missing or ambiguous declarations before writes. Keep Cargo publication credentials out of this path. Actual publication retains the separate manual main workflow and its exact signing identity and tag/dispatch-commit validation.

- CLI/MCP/Connect share one core, exact-commit latest-compatible-attempt validation and explicit per-run acknowledgements. Never resurrect old successful evidence after pruning.

- State, reports and logs remain local and account-owned; no telemetry. User commands require explicit repository trust. Cancellation must reconcile owned descendants before releasing exclusive scheduling groups.

- Development uses frontend 46308 and local UI/API 46309 with conflict failure; docs development/preview use 46310/46281.

- The daemon and on-demand viewer serve the same embedded UI without pairing. Every RPC requires exact same-origin POST and the API version header. `https://oss.delino.io/async-commit-hook` is documentation-only.

- Run `pnpm --filter async-commit-hook build:embedded` before ach Go compilation or repository-wide Go checks, including commit hooks. The app owns the generated command webassets/dist; never commit or substitute placeholder assets.

- Legacy binpm, cargo-mono, nodeup and with-watch releases bind every checkout and new tag to the validated workflow SHA. Before signing, release upload and Homebrew writes, resolve remote lightweight or bounded annotated tags and all release-by-tag state, reject conflicting, orphaned or uncertain targets, and atomically create plus re-resolve an absent tag before handing off to the release uploader. Preserve manual-main publication, never move tags, and keep development-ref dry runs unsigned with read-only contents authority. Follow `repository-workflow-contract.md`.

- Native package release callers must explicitly inherit secrets so the reusable publisher can resolve its protected `linux-packages` environment. Only the guarded publication job references production credentials; validation and installation jobs remain credential-free. Preserve the environment boundary for manual recovery of already-published release identities.

- APT signing-certificate updates are distributed by the shared `delino-archive-keyring` dependency in both suites. Keep certificate versions immutable, retain historical public signing subkeys, and require a completed 30-day old-signer publication overlap before switching CI subkeys.

- Relevant main pushes, including Rust CLI source, Cargo workspace/configuration and toolchain changes, must select the Linux package CI job so both native architectures retain the AlmaLinux 9 compatibility baseline. Manual CI dispatch and PRs skip this job even when CI configuration changes force all eligible checks; its `CI Result` dependency remains and must match the planned skip. General Linux validation, static package contracts, and release-time packaging checks remain enabled.

## CI task integration

- `@delinoio/ci` owns repository-wide validation; app/package leaves stay with their workspace. Actions owns setup, affected planning, matrices, temporary services, artifacts and `CI Result`.

- Follow `repository-workflow-contract.md`. Route checks through `run-affected.mjs` and publish the central event-owned `go_test_matrix`. PR Go tests allocate five Ubuntu package shards even when forced; main keeps full Ubuntu suites and affected Windows packages; shared inputs and forced validation select full suites only on eligible hosts and manual runs use the five Ubuntu PR shards. PR Go results establish Linux coverage only. Require exact matrix agreement in `CI Result`; preserve exact comparison SHAs, external forcing, complete selected package tests, disjoint PR Ubuntu/Windows package shards and platform-owned 20/45-minute package watchdogs. Ubuntu uses default parallelism without precompilation, restores main's `go-test-all` cache and never saves.

- Use the dependency-free shared dot-aware path matcher for job ownership, configuration forcing and workspace forcing. Include hidden leaves and directories at every wildcard depth, preserve literal-dot patterns and Git path characters, and retain event eligibility. Planning must work before workspace installation.

- Affected Go discovery uses native `go list -mod=readonly -test -json` and retains only original package owners. Match resolved production/internal-test/external-test embed files against every consumer before narrowing testdata ownership; production embeds propagate to callers and test embeds select tests only. Preserve import and subprocess/frontend edges, native checkout identity, discovery failures and conservative deletion/move fallbacks.

- Root `turbo.json`, `package.json`, `pnpm-lock.yaml` and `pnpm-workspace.yaml` changes select `rust-fmt` through its job-specific path rules. Preserve the existing native `FORCE_RUN` commands and event eligibility; these root inputs do not globally force unrelated jobs.

- Preserve the central validated Rust package selection. `run-rust.mjs` selects an explicit uncached Turbo graph with only the selected owners' build prerequisites; it must not expand the Cargo package list or bypass empty-selection rejection.

- Cache deterministic leaves only. Go/Rust execution, DB/OS/render/benchmark, repeated clean builds, embedded generation and protocol freshness run every time. Declare actual outputs and external inputs, plus task-local platform/tool/option hashes.

- Central Go test execution uses `-count=1` in full and affected modes for every shard. Turbo's uncached task does not disable Go's result cache. Keep discovery and compile-only arguments unchanged, retain compiled-object cache reuse, and verify subprocess consumers and repeated execution in disposable fixtures.

- Keep JS hashes independent of native tool availability. Hash installed Go/Rust/Buf versions only in their owning cached tasks; metadata queries must not install compilers.

- Vercel OIDC tokens are short-lived and cache-only. Main writes, first-party PR/manual non-main reads, forks skip auth; failures fall back locally and must not suppress validation failures. Keep development's exact environment boundary unchanged.

- Test cold/warm restoration, invalidation and uncached failure propagation in disposable fixtures. Record hosted main-write/PR-hit and comparable timing in PRs or CI artifacts, not repository evidence files.

- Rustfmt configuration changes select only `rust-fmt`, including nested overrides matched by broad package rules. Hash both supported filenames at every depth in `ci:rust:fmt`; run the real formatter cache fixture uncached in the formatting job. The disposable fixture records the exact production command's completion, status and diff before returning to Turbo; cache hits must create no fresh receipt. Do not accept unrelated nonzero exits or rely on Turbo console forwarding as completion evidence.

- CI task setup must resolve Go cache paths before strict environment filtering and install the pinned Rust toolchain before shared-target native validation.

- Protocol lint, format and freshness resolve the pinned installed `@bufbuild/buf` package manifest `bin.buf` as a bounded, contained Node entry and invoke it through the current Node executable with literal argv and no shell. Keep the launcher fixtures uncached on Linux/Windows; preserve compatibility generation and tracked/untracked freshness rejection.

- Changes to the shared legacy CLI publisher select `release-contracts`, which owns its top-level release fixtures. Keep this input edge in `job-paths.json` and the planner tests when changing release-helper ownership.

- Go timing reports retain source revision, commands, phase times and top-level test metadata only. Keep raw output and dynamic subtest names out of artifacts. Preserve failure status, native affected selection and exhaustive shard coverage when changing instrumentation. Windows workspace tests run on their own runner and use the retained 45-minute fixture watchdog.

- Exact `AGENTS.md` files are policy metadata and do not seed affected Go selection. Resolved production/internal-test/external-test embeds remain authoritative; embedded deletions/moves and other unknown resources retain conservative fallbacks.

- Go formatting inventory uses NUL-separated Git paths and passes literal arguments to the formatter. Preserve source bytes and Git, formatter, missing-scope and empty-inventory failures; cover quoted non-ASCII and newline names in the central contract fixtures.

## Changed source and documentation formatting

The existing Lefthook pre-commit hook runs `node scripts/ci/format-changed.mjs --staged --write` for Rstack-supported staged files and uses `stage_fixed: true` to include formatting fixes in the same commit. The staged selector uses NUL-separated index paths without rename detection, excludes deleted files and symlinks, and requires neither HEAD nor a comparison branch, so it works for initial commits. Rstack keeps ownership of extension formatting and exclusions. Lefthook hides unstaged edits in partially staged files during the hook and restores them afterward; those edits must remain outside the commit. If an unstaged edit conflicts with the formatter output, Lefthook rejects the commit and leaves HEAD, the staged content, and the unstaged edit unchanged, with no stale patch or stash; the pinned Lefthook release must keep this behavior. Formatter failures stop the commit. Rust and Go hooks and the existing root-install hook setup retain their behavior.

CI execution is detected from the `CI` environment variable: unset, empty, or a case-insensitive `false` value is local execution and behaves exactly like unset `CI`, so the hook works in shells that export `CI=false`. Every other value, including `true`, `1` and `0`, is CI execution, which requires the planner comparison and rejects `--staged` and `--all`. The local-only `--staged` option cannot be combined with `--all`, explicit comparison arguments, comparison environment variables, or CI execution. It uses the same formatter runner and configuration as local range formatting and CI. Logs identify staged selection, candidate counts and formatter status without file contents; comparison and validation revisions are null because initial commits have no HEAD. The central formatting fixtures run actual temporary-repository commits to cover auto-staging, partial staging, initial commits, unusual paths, exclusions, deletion-only and unsupported-file commits, and parser failures, including hook runs with `CI=false`; direct CLI fixtures cover the CI value matrix without running the hook.

The `source-format` Ubuntu job checks changed JS/TS (including JSX/TSX and module extensions), CSS, JSON/JSONC/JSON5, YAML and Markdown/MDX files through pinned Rstack CLI `rs fmt --check --no-cache`. The root `rstack.config.ts` owns `define.fmt()` options and ignore patterns. Markdown prose wrapping is preserved and embedded-language formatting is disabled. Generated bindings, generated localization resources and generated guidance, package-manager lockfiles, build output, vendored code, and fixture/testdata directories retain their original bytes. Authored localization JSON, including extension `_locales/*/messages.json`, and ordinary test sources remain eligible. Rust, Go and Protobuf checks retain their existing owners.

PR, main push and affected manual jobs pass the central planner's exact base/head SHAs to the uncached `@delinoio/ci` task. Selection uses NUL-separated Git paths with rename detection disabled, then checks whole files in the validation checkout, including PR merge contents. Deleted files and symlinks are excluded. No candidate files, or only files excluded by Rstack configuration, is a successful no-op. Invalid or missing comparisons, Git failures, formatter startup failures and parser errors fail validation. The final CI result requires a successful selected job. Logs record comparison and validation revisions, candidate counts and exit status without file contents.

For an explicit local repository-wide pass, use `pnpm format -- --all`; this option is rejected in CI. This pass checks tracked files present in the working tree and skips unstaged deletions. Other filesystem errors fail the pass. Imported agent skills retain their upstream formatting.

Root `pnpm format:check` checks and `pnpm format` fixes the same candidates. Local execution defaults to the merge base of `origin/main` and HEAD and includes tracked working changes and untracked non-ignored files. Explicit `--base <sha> --head <sha>` arguments after the Turbo `--` separator select a committed range, as do `TURBO_SCM_BASE` and `TURBO_SCM_HEAD` in CI. CI requires an explicit comparison. Formatter configuration changes do not expand validation to unchanged files; configuration regressions belong to the central contract fixtures.

The formatting job reuses the shared Node setup, frozen script-disabled install, successful-main-only pnpm cache saves, and Turbo summary artifacts. It needs no Rust or Go installation and never allocates a macOS runner.

## Release publication policy

The shared release planner exports `cargo_publish` from the central project policy.
The coordinator resolves it before configuration validation and version commits,
then uses it to select Cargo registry credentials, Rust setup, caching and
publication. Non-Cargo projects still validate their immutable source and tag.

## AIPRouter integration

AIPRouter uses the root `github.com/delinoio/oss` Go module. The module selects toolchain `go1.26.10`, retaining the imported patched toolchain for local commands and CI setup. Root `pnpm dev:aiprouter` selects the API, console and consolidated docs development tasks. The existing affected Go checks include its shared libraries and validation fixtures. `.github/workflows/aiprouter.yml` retains PostgreSQL, race, generated-binding, Compose recovery, distribution and synthetic-load validation on affected PR, main push and manual runs, without publication or macOS runners. Its manual comparison uses the same main/branch baseline rules as the shared workflow. The shared docs workflow builds `/aiprouter`; no separate docs publisher is added. Release staging remains explicit through `scripts/aiprouter/build-release.py`; the npm installer uses the `aiprouter@v<version>` tag namespace in this repository. No release automation or publication authority is added. Record actual results and unperformed checks in PR/CI records.

## labs-site validation

The central `node-labs-site-test` job owns `apps/labs-site/**` and its project/foundation contracts. It uses one frozen script-disabled workspace install and the shared affected runner with the exact planner comparison to run `labs-site test` on Ubuntu. Shared workspace and development-wrapper inputs force validation. The final CI result includes this job. PRs, main pushes, and manual dispatch retain affected selection and existing native platform restrictions. This job validates TypeScript and the static production build; it does not publish to Cloudflare Pages or establish domain, DNS, TLS, or production HTTP acceptance. See [the labs-site foundation](apps-labs-site-foundation.md).

## Labs localization validation

The existing Labs Site and Labs Docs jobs also own `scripts/localization/**` and
`docs/apps-labs-localization-contract.md`. Shared tooling changes select both
jobs through the existing affected planner. App tests run offline auth/generation
fixtures and freshness checks before static builds; cached build and route leaves
include the shared tool inputs. CI does not authenticate with ChatGPT or generate
translations. See the [localization contract](apps-labs-localization-contract.md).
