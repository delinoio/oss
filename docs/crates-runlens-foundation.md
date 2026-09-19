# Runlens runtime contract

## Scope
`crates/runlens`, its independent workspace, vendored engine, fixtures, installers, and release validation implement Runlens. The complete issue requirements below are preserved as the source baseline; implementation status must be supported by tests, never inferred from this specification.

## Runtime and Language
Rust 2024, nightly-2026-08-02, independent Cargo workspace and lockfile. Version 0.1.0, publish=false. Root Cargo explicitly excludes this workspace.

## Users and Operators
Developers, OSS maintainers, and CI maintainers investigating finite noninteractive commands.

## Interfaces and Contracts
The following normative issue snapshot preserves all selected capabilities, acceptance criteria, exclusions, and platform requirements.

#### Summary

Implement **Runlens**, a local and CI command diagnostics CLI that explains filesystem dependencies, execution differences, and command side effects.

The first release, **0.1.0**, includes all nine capabilities:

1. Compare executions.
2. Audit declared cache inputs and outputs.
3. Produce execution receipts.
4. Verify execution in a clean checkout.
5. Check execution policies and baseline regressions.
6. Explain file usage across explicitly supplied reports.
7. Compare outputs across repeated clean executions.
8. Identify potential conflicts between commands.
9. Export shareable JSON and offline HTML reports.

Runlens targets developers, OSS maintainers, and CI maintainers. It observes finite, noninteractive commands and their supported child processes. It does not manage a task graph, operate a service, or provide a security sandbox.

**Execution history is not retained by default.** Users save a report only by supplying `--save <path>`. Comparisons and queries consume explicitly supplied report files. Reports contain metadata only, never file contents, environment variable values, or captured command output.

Version 0.1.0 is a regular release with the agreed correctness, platform validation, and compatibility requirements. Its version number does not waive those requirements.

#### Evidence

- The product request explicitly selected the original three capabilities and all six proposed extensions.
- Repository inspection found no existing Runlens implementation or project contract.
- Searches for `runlens` and `fspy` found no existing issue in `delinoio/oss`.
- The upstream [fspy implementation](https://github.com/voidzero-dev/vite-task/tree/13aa80a0dac698023ce68ba16497b16e5330600b/crates/fspy) supplies filesystem access tracking for commands.
- Its [public access model](https://github.com/voidzero-dev/vite-task/blob/13aa80a0dac698023ce68ba16497b16e5330600b/crates/fspy_shared/src/ipc/mod.rs) records paths and access modes. It does not establish successful content reads, detailed process attribution, or an execution timeline.
- Upstream macOS handling substitutes some system executables. Runlens must disable substitution to preserve the requested command.
- Upstream tracking reports collection overflow, and its collection lifetime is tied to the main child. Runlens must handle these boundaries explicitly.
- [TaskFlow #898](https://github.com/delinoio/oss/issues/898) concerns command orchestration and caching. Runlens remains independently usable and does not require a TaskFlow adapter.
- Repository contracts determine documentation ownership, UUID v7 identifiers, structured logging, public documentation boundaries, and issue formatting.

These are source observations and selected requirements. No implementation, platform certification, or measured performance improvement is claimed yet.

#### Current Gap

Developers need a common way to investigate hidden filesystem dependencies, differences between local and CI execution, unexpected file changes, and output instability.

The missing product layer combines access observations with filesystem snapshots, explicit declarations, and comparable reports. It must distinguish facts from hypotheses and expose incomplete observations instead of treating missing evidence as proof of correctness.

#### Proposed Scope

##### Product capabilities

| Capability | Required behavior |
|---|---|
| Execution comparison | Compare two supplied reports, including accessed paths, observed file states, changes, command outcomes, and environment metadata. Expose missing or incomparable evidence. |
| Cache declaration audit | Compare observed accesses and changes against explicitly declared inputs and outputs. Diagnose undeclared dependencies, uncovered outputs, input/output overlap, and relevant directory or missing-path observations. Do not certify universal cache safety. |
| Execution receipt | Report observed accesses and actual before/after changes separately, including created, modified, deleted, and type-changed paths inside the snapshot scope. Include Git-ignored files. |
| Clean checkout verification | Execute preparation and the command in a temporary checkout. With a baseline report, compare results; without one, verify clean execution and applicable rules without claiming equivalence to the current worktree. |
| Policy and baseline checks | Check declared read/write boundaries, input/output coverage, and newly observed accesses relative to a supplied baseline. Explicit policy violations can fail CI. |
| File explanation | Query supplied reports for commands that accessed or changed a path. Present producer/consumer relationships as observed relationships or candidates, not proven causality. |
| Repeat verification | Run the same command three times by default, using a fresh checkout, HOME, and cache for each run. Compare declared output paths, types, contents through hashes, and applicable executable permissions. Allow an explicit run count; reject missing output declarations. |
| Conflict analysis | Compare supplied command reports for overlapping writes and read/write relationships. Report potential conflicts, not proof that a race occurred. Do not execute commands automatically. |
| Report export | Export metadata as JSON or self-contained, offline HTML. Do not provide ZIP bundles, source attachments, or log attachments. |

Environment differences are visible in comparisons. Reports from different OSes, architectures, or tool versions can be compared using explicit path mappings, but such comparisons cannot establish reproducibility or cache safety.

##### CLI and configuration

Provide these public command groups:

| Command | Purpose |
|---|---|
| `runlens run -- <argv>` | Observe a directly supplied command. |
| `runlens run --command <name>` | Observe a command declared in configuration. |
| `runlens compare <left> <right>` | Compare saved reports. |
| `runlens cache check <report> --command <name>` | Audit a report against declared inputs and outputs. |
| `runlens receipt <report>` | Render an execution receipt. |
| `runlens verify clean <name>` | Verify a configured command in a clean checkout. |
| `runlens policy check <report>` | Check configured policies, optionally against `--baseline <report>`. |
| `runlens explain <path> --report <file>...` | Explain observed file usage. |
| `runlens verify repeat <name> --runs <count>` | Compare outputs across fresh executions. |
| `runlens conflicts --report <file>...` | Identify potential command conflicts. |
| `runlens export <report> --format json\|html --output <path>` | Explicitly write a shareable report. |
| `runlens doctor` | Diagnose platform and tracing prerequisites. |

Use `runlens.toml`, initially at schema version 1. Configuration contains named commands, argv, working directories, inputs, outputs, preparation steps, environment variable names, exclusions, and execution policies.

Direct argv execution must work without configuration. Shell interpretation is explicit; Runlens must not concatenate ordinary argv into a shell command. Loading configuration or inspecting reports must not execute configured commands.

Executing commands preserve their standard output and error streams without capturing them into reports. Runlens diagnostics and human execution summaries use stderr. Saved JSON is written only to the explicitly selected destination. Read-only analysis commands provide machine-readable JSON output without mixing it with progress messages.

Use stable typed errors and distinguish:

- Command failure.
- Invalid arguments, configuration, or report format.
- Unsupported platform or tracing capability.
- Incomplete collection.
- Policy or verification failure.
- Timeout, cancellation, save failure, and cleanup failure.

Keep child status distinct from Runlens status in structured results. Failed or incomplete checks must never produce a successful verification result.

##### Observation and execution correctness

Support finite, noninteractive build, test, and script commands, including supported child processes. PTY sessions, interactive prompts, and background services are excluded.

Preserve the executable and arguments requested by the user. Never silently substitute another shell or utility.

Check available prerequisites before starting. If initialization fails or a known unsupported target is detected, do not start the command. If collection fails after execution starts, allow the command to finish and report incomplete collection and a tool error.

Do not automatically retry commands. Timeouts are explicitly configured. Cancellation and unsupported children remaining after the main command exits receive a five-second termination grace period, followed by forced termination and reaping. Retained evidence must identify incomplete or canceled execution.

Distinguish:

- An access attempt recorded by the tracing backend.
- A filesystem state observed in a snapshot.
- A verified difference between snapshots.
- A dependency, cause, or conflict inferred from those observations.

Do not infer successful content reads, syscall success, event order, or exact process attribution from a path and access mode.

Snapshot the whole worktree, including Git-ignored paths, excluding Git internals, Runlens temporary material, and explicitly configured exclusions. Outside that scope, record access observations without claiming complete before/after coverage.

Handle symlinks, directory membership, missing paths, permission errors, changing files, and platform-specific path semantics explicitly. Missing or unstable evidence remains unknown; it is not an empty file or evidence of no change.

“Complete” collection describes completion within the documented backend and snapshot coverage. It does not prove that every possible program dependency was captured. Environment-variable reads, network activity, clocks, and randomness are outside filesystem observation.

##### Clean and repeated execution

Clean verification runs only in the temporary checkout; it must not rerun the command in the user's current worktree.

Use the selected repository's HEAD commit by default. Clearly report that uncommitted changes were excluded. `--include-working-tree` additionally includes tracked changes and nonignored untracked files.

Use a temporary HOME and caches. Pass only required OS execution context and explicitly selected environment variables. Do not copy credentials or ambient user configuration automatically.

Preparation steps are explicit configuration, execute in order, and must succeed before the target command starts. Runlens must not infer package installation, credential provisioning, or other setup actions.

Temporary checkout isolation is operational isolation, not an OS security boundary. Preparation and target commands retain the host permissions and network access available to them.

Each repetition starts from the same selected source state in a newly prepared environment. Output equality is an observation about those executions, not proof of universal determinism.

##### Data, privacy, and compatibility

Default execution leaves no retained report, history database, search index, or implicit `last` pointer. Report lookup never relies on a hidden per-user history.

`--save <path>` explicitly saves a report. Saved files belong to the user and have no automatic expiration or deletion policy. Publish saved reports atomically, report write failures, and do not silently overwrite an existing file.

Use a single JSON report format, initially schema version 1. Include:

- UUID v7 execution identifiers.
- Runlens and tracing-engine versions.
- Sanitized command identity and arguments.
- OS, architecture, and available source/tool metadata.
- Declared scope and exclusions.
- Access observations and before/after metadata.
- Child and collection outcomes.
- Findings, classifications, evidence references, and limitations.

Store metadata only. Never retain file bodies, stdin, stdout, stderr, terminal transcripts, or environment variable values, including through an opt-in capture feature.

Apply argument and path redaction before report serialization or Runlens logging. Normalize workspace and user-root references for sharing and support explicit redaction rules. Document the remaining sensitivity of metadata; automatic masking must not be described as infallible secret detection.

Reports are untrusted input. Validate versions, types, sizes, and references. Rendering must escape report content and must not execute commands, extract archives, or load external resources.

Preserve CLI and report compatibility within the same product major version. Schema versions are independent of the product version. Reject incompatible report schemas clearly rather than silently reinterpret them. Existing saved reports are never rewritten automatically, including during upgrades or rollback.

##### Resource use and diagnostics

Use private temporary files when collection exceeds a 256 MiB in-memory threshold. Default collection limits are 1 GiB of collected data and one million paths; make the limits configurable.

These limits apply to Runlens collection, not to the disk or memory consumed by the observed build and its temporary checkout.

On collection-limit exhaustion, retain an incomplete classification and never pass verification. Remove owned temporary material after completion, failure, and handled cancellation. Surface cleanup failures with recovery guidance; never clean unrelated user files.

Use structured Rust `tracing` diagnostics with execution IDs, lifecycle stages, counts, elapsed time, and stable error classifications. Keep command output, raw secrets, and environment values out of Runlens logs.

Provide readable progress for scans and preparation, color opt-out, and typed troubleshooting information through `doctor`. No telemetry, remote logging, metrics service, dashboard, or alerting backend is included.

##### Implementation and platform support

Implement Runlens in Rust under `crates/runlens` as an independent Cargo workspace.

Document the explicit exception to the repository's normal workspace membership and default-language rules. Preserve the root Rust toolchain and the protected DevHud dependency graph.

Start from fspy commit:

`13aa80a0dac698023ce68ba16497b16e5330600b`

Use its pinned `nightly-2026-08-02` toolchain within the independent workspace. Include the necessary upstream sources with their original license notices and record the provenance of included dependencies.

Allow narrowly scoped correctness changes needed to disable executable substitution, expose unsupported or incomplete tracking, and enforce the agreed execution lifetime. Record each patch, its justification, tests, and removal conditions. Do not add PTY or service-management extensions.

Required release targets:

| OS | Minimum | Architectures |
|---|---|---|
| macOS | 13 | x64, arm64 |
| Windows | 10 22H2 | x64, arm64 |
| Ubuntu | 22.04 | x64, arm64 |

Linux release artifacts target glibc hosts. Alpine/musl hosts and mixed-architecture execution are excluded. This does not automatically exclude a supported statically linked child executable on a supported glibc host.

Support claims require actual execution evidence on the relevant target and documented backend prerequisites. Cross-compilation or upstream source presence alone does not establish support. Unsupported protected executables and process behaviors must be documented and diagnosed.

##### Distribution, documentation, and support

Publish version `0.1.0` using the exact `runlens@v<MAJOR.MINOR.PATCH>` release identity.

Provide:

- Six platform artifacts through GitHub Releases.
- SHA256 checksums and Sigstore verification material.
- POSIX shell and PowerShell installers.
- Prebuilt Homebrew distribution for supported macOS and Linux architectures through the repository's existing tap workflow.
- Explicit version installation and documented manual update and rollback.

Installation must reject invalid or mismatched release material. Preserve published release immutability. Keep CI validation and release dry runs free of publication side effects; publication credentials belong only to the release boundary.

Apple notarization and Windows Authenticode signing are excluded. Do not describe Sigstore signatures as native OS code signing.

There is no runtime feature flag: this is an independently installed local CLI whose available functionality is determined by its version. Remote flags, cohorts, staged feature activation, and telemetry are not applicable.

Provide English CLI help, errors, reports, README, and a dedicated Rspress documentation app at `apps/runlens-docs`:

- Canonical URL: `https://runlens.delino.io`.
- Hosting: Cloudflare Pages.
- Development: `127.0.0.1:46310`.
- Preview: `127.0.0.1:46272`.
- Development entry point: `pnpm dev:runlens-docs`.
- Port conflicts fail without automatic remapping.

Document installation, configuration, all nine capabilities, report formats, privacy, platform limitations, verification limits, troubleshooting, and manual rollback. Keep HTML reports offline and accessible, using semantic structure and text explanations rather than color alone.

Create the Runlens project and domain contracts before runtime implementation. Update repository/domain AGENTS rules, the documentation catalog, project ownership, and public discovery links in the same implementation change. Keep internal architecture and operational details in `docs/`.

Support uses GitHub issues. Publish reproducible performance benchmarks, but do not promise a numerical performance SLA, support response SLA, or fixed release deadline.

#### Acceptance Criteria

- All nine capabilities are implemented and documented in version 0.1.0.
- Every advertised target passes actual execution and installation validation; unavailable evidence blocks the corresponding support claim and the agreed complete release.
- Ordinary runs retain no execution history. Explicitly saved JSON can be inspected, compared, queried, and exported without running its recorded command.
- Reports contain metadata only and exclude source contents, captured streams, environment values, and unredacted designated secrets.
- Receipts distinguish access attempts from verified file changes and include Git-ignored changes within the declared snapshot scope.
- Cache and policy checks explain violations using concrete evidence. Candidate dependencies and conflicts are not presented as proven causes or races.
- Clean verification preserves the current worktree, uses the selected source policy, and applies only explicit preparation and environment configuration.
- Repeated verification uses fresh environments and compares the configured output contract.
- Incomplete tracking, missing evidence, save failures, and unsupported behavior cannot become successful verification results.
- Cancellation and timeout handling reap owned processes and clean owned temporary material without affecting unrelated processes or files.
- The CLI, configuration, and report format have documented compatibility behavior, including explicit rejection of unsupported versions.
- Release artifacts, installers, Homebrew distribution, public documentation, diagnostics, and rollback instructions are validated.
- Existing repository development commands, root toolchains, protected dependencies, and unrelated release workflows remain functional.

#### Test Scenarios

1. Record direct commands and configured commands with spaces, Unicode arguments, child processes, failed execution, and noninteractive stdin.
2. Exercise real tracing and snapshot fixtures on every supported OS/architecture, including supported static Linux child binaries and known unsupported executables.
3. Compare added, removed, changed, and type-changed paths, Git-ignored files, explicit exclusions, symlinks, directory listings, and missing-path observations.
4. Distinguish read/write attempts from successful content access and actual file changes. Preserve unknown evidence for unreadable or unstable files.
5. Audit undeclared inputs, uncovered outputs, input/output overlap, external accesses, and baseline policy changes.
6. Query multiple supplied reports for a path and verify that inferred producer/consumer relationships and conflicts remain classified as candidates.
7. Verify clean execution from HEAD and with `--include-working-tree`; cover tracked edits, nonignored new files, setup failure, absent credentials, and preservation of the original worktree.
8. Repeat in fresh environments with equal outputs, changed contents, different output sets, changed executable permissions, missing output declarations, and failed runs.
9. Compare different environments with explicit path mappings without issuing a reproducibility or cache-safety pass.
10. Test initialization failure, mid-execution tracing failure, collection overflow, permission loss, timeout, cancellation, lingering children, and cleanup failure.
11. Verify that default runs retain no history and explicit saves are atomic, collision-safe, and independent of automatic retention or migration.
12. Place canary secrets in arguments, environment variables, file contents, and child output. Confirm that prohibited material does not enter reports or Runlens logs.
13. Test malformed, oversized, incompatible, and hostile report input, HTML escaping, offline rendering, keyboard access, and machine-output separation.
14. Validate release identity, artifact selection, checksums, Sigstore verification, installers, Homebrew updates, interrupted installation, and manual rollback.
15. Validate documentation routes, public links, fixed ports, accessibility, and absence of credentials or internal implementation details.
16. Publish repeatable benchmark procedures covering worktree scanning, command overhead, large access sets, memory spill, and resource-limit behavior.

Run focused unit, integration, contract, and native platform tests. Run the repository-required root `cargo test`, the independent Runlens workspace tests using its pinned toolchain, and `pnpm test` from the documentation app. Remove generated repository-owned `dist` directories before completing implementation.

#### Out of Scope

- Default execution-history retention, history databases, implicit last-run lookup, and automatic deletion of saved user reports.
- File contents, environment values, captured command streams, terminal transcripts, binary attachments, and ZIP bundles.
- PTY sessions, interactive prompts, background services, and persistent process supervision.
- Docker management, OS security sandboxing, privileged tracing deployment, and guarantees against hostile workloads.
- Silent executable substitution or a compatibility mode that changes the requested program.
- Automatic cache reuse, output restoration, source modification, command retry, or inferred setup actions.
- Task scheduling, watch mode, per-test selection, and dedicated Turbo or TaskFlow adapters.
- Hosted accounts, team storage, cloud dashboards, remote feature flags, telemetry, or hosted AI analysis.
- Universal tracing coverage, detailed process timelines, network/environment-read tracing, proof of causality, or proof of race occurrence.
- Alpine/musl hosts, mixed-architecture execution, crates.io/npm publication, app stores, automatic self-update, Apple notarization, and Windows Authenticode.
- Numerical performance guarantees, a support SLA, or a fixed release deadline.

## Storage
No implicit history. Only explicit report/export paths persist. Reports contain metadata only. Temporary snapshots, IPC, and spill data are private, bounded, owned by one execution, and removed after handled termination. User-owned reports are never rewritten or automatically expired.

## Security
Observation is not a security sandbox. Redact before serialization/logging; never retain file contents, environment values, or captured streams. Treat reports as untrusted data, validate bounded shapes/references, and escape offline HTML. Source credentials and signing credentials never enter reports or validation jobs.

## Logging
Rust tracing events use execution UUID, lifecycle stage, counts, elapsed time, and stable error codes. User argv, paths before redaction, environment values, and child output never enter tracing events. Progress and human execution summaries use stderr.

## Build and Test
Run root cargo test, then cargo +nightly-2026-08-02 test --locked in crates/runlens. Run independent fmt and Clippy. Native execution/installation evidence is required for each declared OS/architecture, including supported static Linux children. Missing evidence prevents release approval. CI and dry runs never publish.

## Dependencies and Integrations
fspy: voidzero-dev/vite-task at 13aa80a0dac698023ce68ba16497b16e5330600b. Detours: microsoft/Detours at 9764cebcb1a75940e68fa83d6730ffaf0f669401. Preserve original notices and record the exact included dependency closure and local patches. No TaskFlow adapter or root dependency changes.

## Change Triggers
Runtime, report, CLI, source selection, privacy, target, or distribution changes update project-runlens, public guides, AGENTS rules, and regression fixtures together.

## References
- [Project](project-runlens.md)
- [Repository defaults](repository-defaults.md)
- [Issue #907](https://github.com/delinoio/oss/issues/907)

## Implemented observation and validation boundaries

The product crate owns `config`, `platform`, `execute`, `snapshot`, `entries`,
`privacy`, `model`, `report`, `analysis`, and `clean`. The executable is an argv-only
adapter over those modules. Read-only analysis consumes explicit schema-v1 reports
and never resolves or invokes their recorded commands. UUID-v7 strings must use
canonical lowercase spelling. Access evidence records attempted modes; snapshots
and derived change classifications remain separate and the report reader checks
that every supplied change agrees with its referenced before/after states.

Snapshot enumeration includes ignored files and never follows directory symlinks.
Ordered metadata indexes replace unbounded directory sorting. File hashing checks
identity before and after streaming reads and observes cancellation between chunks.
Permission errors, unstable observations, unsupported objects, exhausted collection
budgets, and incomplete opposite snapshots preserve unknown state. Raw backend
collection is private and bounded before target execution starts. Overflow closes
the shared writer while retaining the committed prefix; it cannot certify success.
Temporary execution-directory cleanup failures retain the execution evidence with
a `cleanup-failed` outcome instead of discarding the already observed command.

Local validation from the independent workspace is:

```sh
cargo fmt --package runlens --check
cargo clippy --locked --package runlens --all-targets --features test-support --no-deps -- -D warnings
cargo test --locked --features test-support
```

`--no-deps` restricts the product Clippy policy to product sources; upstream
expectations and its separate lint configuration are not rewritten as product
policy. The test-support feature builds an explicit native fixture executable,
never an installed product command. Native integration tests exercise subprocess
tracing, ignored output receipts, command failure, timeout, cancellation, lingering
children, finite stdin forwarding, clean/repeat source isolation, privacy canaries,
explicit persistence, offline analysis/export, overflow, and protected macOS tools.
Linux native CI additionally compiles and executes a static musl child on the
glibc host to verify seccomp coverage; cross compilation cannot satisfy that test.
