# TaskFlow engine contract

## Scope
`crates/taskflow` owns the `tflow` binary and a reusable library. The approved implementation includes discovery, graph queries, selection, scheduling, local/R2-compatible caching, development sessions, Docker, sharding, and GitHub Actions export. Existing repository workflows, compiler actions, remote execution servers, and public releases are excluded.

## Runtime and Language
Rust using the repository toolchain. Host execution targets macOS, Linux, and Windows on x64/arm64; Docker executes Linux containers. Optional external tools are required only for the corresponding adapter or executor. Do not claim validation on platforms not exercised by conformance CI.

## Users and Operators
Developers run explicit native commands locally or in managed development sessions. CI maintainers configure runner/tool mappings and a trusted cache namespace. Operators own their R2/S3 storage and credentials.

## Interfaces and Contracts
### Configuration
- `taskflow.yml` version 1 requires an explicit project ID; unknown fields and duplicate keys are errors.
- Preserve `command`, `dependsOn`, `input`, and `output`. Arrays execute argv directly; strings use `/bin/sh` on Unix or `cmd.exe` on Windows unless `shell` is explicit.
- Root `workspace.manifests` references native manifests, never a duplicated member registry. Omission discovers supported root manifests. No native workspace means a single project.
- Projects without configuration remain queryable using path-based IDs but have no inferred commands. Canonical directories merge adapter discoveries; distinct directories cannot share an explicit ID.
- Paths are project-relative. Inputs may reference other files inside the workspace. Outputs must remain inside their owning project and cannot overlap another owner's outputs.
- Direct task references must exist. Native dependency selectors select direct neighbors of the requested kinds, skip task-less neighbors with an explanation, and do not traverse task-less intermediates. Task cycles fail validation.
- A service prerequisite requires explicit readiness waiting. `with` activates companions without implying readiness or completion ordering.
- Cache is opt-in. Service, scheduled, external-side-effect, and secret-consuming tasks are uncached. Cacheable tasks explicitly declare inputs, outputs (including an explicitly empty list for checks), relevant environment, and tool identities.

### Discovery and queries
pnpm lockfile metadata, versioned Cargo metadata, and Go workspace/module metadata supply resolved identities and conditions. Preserve aliases, renames, dependency kinds, replacements, and supported target/feature conditions. Unsupported or incomplete relationships are diagnosed; affected selection is conservative and unresolved prerequisite selectors fail closed. Discovery never installs dependencies implicitly. Explicit installation prerequisites can repair unavailable metadata before replanning.

Cargo target conditions remain visible on project edges but only active conditions select task prerequisites. Evaluate them with Cargo's platform parser and rustc cfg output. Explicit `workspace.cargoTarget` selects the compilation target independently of the execution host; otherwise use the matching rustc host target or the selected platform's GNU Linux, Apple Darwin, or Windows MSVC target for x64/arm64. Unavailable target cfg metadata makes selector coverage incomplete.

Keep project relationships separate from task prerequisites and artifact relationships. Queries expose projects/tasks, forward/reverse closure, paths, file ownership, matching inputs, and explanations. Git selection includes both sides of renames and deleted files. Graph generations invalidate obsolete executions after configuration changes.

An explicit `--head` requires `--base` or `--affected` and cannot be combined with `--changed`; comparison endpoints must never be silently ignored in favor of a direct run.

### Execution
`check`, `query`, `plan`, `run`, `start`, `result unchanged`, `cache`, and `ci export` are public commands. Machine output is versioned JSON on stdout; logs go to stderr. Direct, own-input, prerequisite, and schedule causes remain distinct. An unchanged report removes only propagation from its source. Cache reuse and output restoration are execution outcomes, not unconditional claims that dependents are unchanged.

The scheduler deduplicates prerequisites, limits concurrency, coordinates named resources and output ownership, and uses observed durations for critical-path priority. A failed finite task blocks its dependents while independent tasks complete. Unix process groups, Windows Job Objects, and owned Docker containers are reaped before replacement. Cancellation or input/configuration invalidation forbids successful cache publication.

Task-reported unchanged uses an execution-specific result file, not magic stdout. Accept it only for successful, current executions. External effects remain eligible after upstream unchanged results.

### Development sessions
Subscribe before activation. Default file debounce is 200ms; default overlap is `queue` for files and `skip` for schedules. Queue coalesces without losing independent causes; restart cancels and reaps first. Output changes cannot self-trigger their producer but remain visible to consumers. HMR services do not restart because companions run.

Shared companions are reference-counted by live owners; prerequisite and initial companion execution are deduplicated. Finite check failures retain subscriptions; server/readiness failure ends the session. Shutdown removes subscriptions, timers, queued runs, and process trees. Invalid configuration suspends new work until corrected.

Intervals use monotonic time. Cron uses five fields, IANA zones, UTC by default, no catch-up bursts, and once per repeated local wall-clock minute. `every` and `cron` are mutually exclusive. Configuration reads and one-shot runs never activate subscriptions.

Cron weekdays use 0 or 7 for Sunday, 1–6 for Monday–Saturday, and named weekdays. Restricted day-of-month and day-of-week fields form a union. Interval and cron decision functions accept explicit times so missed ticks and DST can be tested without sleeping.

### Shards and CI
Go top-level tests, Rust libtest items (plus one doctest unit), and Vitest/Jest files are supported inventories. Generic adapters exchange versioned JSON inventory, selected-ID files, and results. Assignment is deterministic and optionally duration-balanced. Missing, duplicate, failed, and cancelled units prevent a false aggregate success.

Explicit shard selection is validated before any prerequisite executes: a nonempty pending plan must include a sharded task, and all pending sharded tasks must declare the requested count. Empty affected CI units and already supplied prerequisite receipts require no shard execution.

CI export creates platform/dependency/shard jobs, preserves execution causes, transfers declared artifacts and receipts, provisions pinned tools and TaskFlow source, validates plan compatibility, and aggregates every result. Tasks needing a shared environment are grouped. Development subscriptions are inactive in CI. Untrusted PRs receive neither secrets nor remote cache access. External effects are not skipped merely because upstream artifacts are unchanged.

## Storage
Ignored workspace-local `.taskflow` contains execution records, content-addressed cache data, temporary staging, locks, and masked logs. SHA-256 keys include configuration, content/deletions, native metadata, declared environment/tool identities, platform/image identity, and prerequisite results. Validate required output existence and contents; restore missing outputs or execute. A lockfile never proves installed outputs exist.

R2/S3 stores the same validated cache format. Configure endpoint, bucket, namespace, access mode, and environment references for credentials. Cache transport failure falls back to execution with diagnostics. Restore validates digests, paths, ownership, and link containment before replacing outputs. Native incremental caches are separate shared resources, not implicitly exported artifacts.

## Security
Default dotenv precedence: CLI > task > inherited > project dotenv > root dotenv. Loading can be disabled. Cacheable commands use a declared environment. Cache transport credentials never enter task environments. Secrets disable caching and are masked in live, persisted, and replayed logs, including across byte chunks. Explicit `--show-secrets` affects current live output only; stored output remains masked. Remote entries require trusted writers; untrusted CI cannot read or write the namespace.

Environment names follow host semantics at every boundary: case-insensitive ordinal comparison on Windows, case-sensitive comparison on Unix. This applies to precedence, secret scope/redaction, cache fingerprints, OS lookup retention, internal execution variables, and remote credential exclusion.

## Logging
Use `tracing` for task IDs, causes, outcomes, durations, cache decisions, and cleanup. Never log secret values or credential-bearing URLs. Preserve parseable JSON stdout and documented color opt-out. Persisted logs are always masked.

## Build and Test
Run `cargo test -p taskflow`, root `cargo test`, formatting and Clippy. Run native adapter fixtures, session/process conformance, virtual-clock tests, S3 transport fixtures, and platform/Docker CI. Map all 26 issue scenarios to evidence. Validate generated workflows with actionlint and public documentation with its package-local `pnpm test`. Generate required ignored outputs before dependent builds and remove generated `dist` directories from the final worktree.

## Dependencies and Integrations
Use existing native commands as units of work. pnpm metadata requires lockfile-query support (10.23+); Cargo metadata format is version 1; Go module/workspace identities remain native. The project does not change protected DevHud dependencies or existing workflow ownership.

## Change Triggers
Configuration, result protocol, cache format, adapter coverage, lifecycle, and CI changes require synchronized schema, tests, public guidance, and appropriate AGENTS rules. Support claims follow actual compatibility evidence.

## References
- [Project index](project-taskflow.md)
- [Repository defaults](repository-defaults.md)
- [Domain template](domain-template.md)
- [Issue #898](https://github.com/delinoio/oss/issues/898)

## Version 1 compatibility boundaries
- CLI `--os` and `--arch` fill undeclared task platform components; explicit task platforms win. Host execution rejects a different platform. Docker image digests and Linux architecture are explicit, and tool identities are probed inside that image. Docker tasks receive a generation-bound POSIX result helper through `TFLOW_BIN`; reporting requires `/bin/sh`, `mv`, and `rm` in the image.
- Artifact graph edges describe conservative positive glob-prefix overlap, independently of prerequisite ordering. Exact input queries apply ordered positive/negative globs. New or deleted native manifest names conservatively invalidate selection even when no longer present in the discovered graph.
- Cache and CI artifact output roots are exact files/directories or complete `directory/**` trees. Partial wildcard ownership remains available to local uncached tasks, but is rejected before CI export or artifact capture/restoration. Snapshots are bounded to 512 MiB encoded transfer size. Relative output symlinks must stay inside owned projects; input directory symlinks require explicit underlying paths.
- Metadata subprocesses have a 120-second deadline and 64 MiB output limit. Cargo uses locked, offline format-1 metadata before installation, with membership-only fallback. Go pins `GOWORK` to the selected native workspace, or `off` for a module, and disables module proxy, checksum database, VCS, and toolchain downloads during discovery; explicit installation owns fetching dependencies.
- Native sharding requires argv commands. Go selection/output overrides and custom Rust harnesses require the generic protocol. Go subtests remain with their top-level parent; JS inventories partition files. Rust doctests are one separate unit when selected by the Cargo command. Native flags supported by each adapter are validated; unsupported combinations fail instead of inventing coverage.
- Cached suites retain inventory plus per-shard accounting. Local complete-suite runs can use measured duration history; independent CI shards use identical declared inventory duration values, avoiding runner-local history divergence.
- CI source provisioning uses the exact `ci.revision` from `delinoio/oss`; it must already contain TaskFlow and be accessible to the runner. Rust is exact or date-pinned; Node, pnpm, and Go versions use exact three-component versions. Runner labels are operator-provided and validated for every selected platform.
- Generated Actions workflows and adjacent `.taskflow.json` blueprints are both committed by the consuming project. Native manifests and configuration bytes bind exported execution structure; changing them requires regeneration. Each result bundle binds its complete plan and unit, validates all selected receipts and required artifacts, and accounts for all shard indices before success.
- Selected task secrets and remote credential environment references map to identically named GitHub secrets only in trusted non-PR jobs. PR and `pull_request_target` execution omit those values and disable remote access. No release or deployment authority is granted by the repository conformance jobs.
- Remote cache publication stages an unreachable content-addressed object, then rechecks inputs/cancellation before committing its entry manifest. Interrupted object uploads never become cache hits. Restore validates all contents before staging and retains rollback state if replacement recovery fails.
- Query JSON masks designated values as well as command logs. `--show-secrets` applies only to live task output. Internal diagnostics pass through the same designation mask before persistence.
- Public source installation (`cargo build --release --locked -p taskflow --bin tflow` in a checkout) and the standard Cargo binary output location are supported user workflows. The crate remains `publish = false`; no public binary release is implied.

Service task timeouts cover process startup through readiness and continued service execution. Expiry is a service failure that tears down the session; explicit owner cancellation remains a normal shutdown.
