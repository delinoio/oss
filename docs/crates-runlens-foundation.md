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
| `runlens cache check <report> --command <name>` | Audit a report against declared inputs and outputs after matching normalized argv, cwd, and any recorded name; mismatched or masked identities are inconclusive. Direct argv reports may bind when argv and cwd match. This remains an offline operation. |
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

On collection-limit exhaustion, retain an incomplete classification and never pass verification. Cancellation during the after-snapshot or comparison retains both cancelled and incomplete classifications even when the child already exited successfully. Remove owned temporary material after completion, failure, and handled cancellation. Surface cleanup failures with recovery guidance; never clean unrelated user files.

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

Linux doctor and execution preflight require the bounded OS identity to identify
Ubuntu with a numeric `YY.MM` release at least 22.04. Unknown identities, older
releases, and other distributions fail the `minimum-os` check before launching
a target, even when seccomp user notification is available.

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
Unix privacy regressions include non-Unicode environment keys and values in direct
and clean execution. Ambient redaction enumerates OS strings without panicking and
retains textual secret masking for Unicode entries; environment values are never
serialized.
The tracing launchers do not inject the unused `FSPY` marker. An absent variable
stays absent, and supplied values survive in targets and nested children; clean
execution still requires explicit selection. Required tracing coordination is
unchanged and documented separately in the vendored patch ledger.
Sensitive-flag scanning continues through already hidden argv entries, including
consecutive flags and explicitly redacted argument indices. Canary regression
tests cover saved reports and both JSON and HTML exports.
Root normalization requires a path start (including attached alphabetic options
such as `-I/path` and `-L/path`) and an exact root or separator boundary;
similarly prefixed external paths retain their identity for policy, mapping, and
query matching. Embedded path arguments after whitespace, assignment, or quotes
use the same component-boundary rule.
Linux native CI additionally compiles and executes a static musl child on the
glibc host to verify seccomp coverage; cross compilation cannot satisfy that test.

`schema/config-v1.json` and `schema/report-v1.json` are generated from product types
with schemars and committed. `RUNLENS_UPDATE_SCHEMAS=1 cargo test --test schema`
regenerates them; ordinary tests reject drift. The documentation build copies
these inputs to ignored public schema assets. Semantic and bounded-allocation
validation remains required in addition to JSON Schema shape validation.

Git metadata and source-preparation children use the same Unix group / suspended
Windows Job ownership as target execution, without tracer injection.
Selected environment names cannot override isolated HOME/cache variables or Git
configuration/repository controls (`GIT_CONFIG*`, `GIT_DIR`, `GIT_WORK_TREE`). Git
control matching follows Windows case-insensitive environment lookup; Unix keeps
case-sensitive Git names.
Metadata
stdout is capped at 16 MiB during reading, and each Git operation has a 120-second
deadline plus the documented termination grace. Cancellation waits for owned
processes before checkout cleanup. Read-only report commands do not invoke Git.
Disposable metadata indexes share a process-wide memory reservation budget so
retaining multiple clean/repeat executions cannot multiply the 256 MiB threshold.
Deserialized maps use that same reservation before spilling; small report maps
do not each open a SQLite connection. Regression coverage reads the maximum
1,056-execution report with a 128-descriptor Unix reader limit and checks that
aggregate deserialization still spills when the shared memory limit is reached.
Index and early-return directory cleanup failures remain visible through the
stable cleanup-failed exit classification. Report parsing bounds arrays during
deserialization and limits the non-evidence string envelope to 1 MiB; querying up
to 64 explicit reports additionally caps their combined input bytes at 1 GiB.
Conflict analysis permits at most 65,536 target-execution pairs across distinct
supplied reports. It counts the aggregate cross product before path traversal or
finding generation, excludes preparation executions, and rejects excess work as
invalid input (exit 2) without partial output.

## Passive identity and failure boundaries

Execution metadata includes the executable SHA-256 computed without invoking the
target. The inspected file handle stays open through child completion. Before
launch Runlens rechecks platform eligibility, pathname identity, and metadata;
a changed identity stops that launch. Linux executes the inspected descriptor,
Windows locks the canonical executable and ancestor rename chain through process
creation, and macOS verifies the main mapped image's vnode before accepting its
digest. Changed retained metadata or missing/mismatched image evidence clears the
digest and makes collection incomplete. The command retains its argv and streams.
These checks are observation safeguards, not an OS security sandbox. Native
regressions cover replacements both before and after the final pathname check.
Comparison requires known matching executable identity and OS metadata,
equal source revision metadata, and the same working-tree inclusion policy;
redacted arguments cannot establish equivalence. Linux OS metadata includes
`ID:VERSION_ID` from os-release. Missing, malformed, or legacy version-only Linux
identities cannot establish compatibility, even when equal. OS identity parsing
never evaluates shell expressions. Selected environment names do not
establish equality of omitted values. Any selected names make report environment
comparison inconclusive, including repeat-output compatibility; no environment
value or value hash is persisted to work around this privacy boundary.
Known restrictive Mach-O code
signature flags and restricted segments are rejected by a bounded parser before
launch. Windows injection failure preserves the original child execution and
records incomplete evidence. Retained metadata maps share one process-wide memory
threshold, including repeat rounds; private index cleanup failures are surfaced.
Cancellation handlers are installed before owned child work. Native cancellation
fixtures synchronize on actual child readiness rather than assuming startup time.

Offline checks never pass a report with no target execution. Cache and policy
checks apply this guard independently of preparation records, including valid
historical-only reports with no stored verdict.
Windows root masking and scope classification use the native ordinal casing
rules for workspace, home, and temporary roots, preserving component boundaries
and converting UTF-16 match offsets back to UTF-8 without byte-length guesses.
The same root comparison applies to exclusions; Unix roots stay case-sensitive.
Windows creation, overwrite, supersede and delete-on-close options produce write
attempts even with read-only desired access; FILE_OPEN alone remains non-mutating.
Native Windows CI compiles the complete preload unit-test target (including its
temporary-file fixtures) before running the Detours setup-error regression.
Windows file-information updates record source-handle writes before deletion or
metadata mutation. Rename/link destinations and unknown information classes
currently remain unsupported evidence and make collection incomplete; the child
operation still proceeds. Handle-local seek and I/O settings do not mutate files.
Unix collection records directory and link creation, permissions, ownership,
truncation, timestamps, extended attributes and macOS file flags independently of
writable opens. Linux static children use corresponding seccomp syscall coverage.
Direct creation of a parent such as `build` needs an explicit `build` write rule
alongside `build/out/**`; snapshot-only ancestor exceptions do not authorize the
creation attempt. Symlink target strings do not count as reading their targets.
Exclusion comparisons normalize ordinary DOS/UNC and extended Windows prefixes
before extracting a relative path. Windows core fixtures cover both directions
and missing descendants; the native excluded-input fixture prevents an omitted
snapshot input from being certified as a newly created output.
Configured redaction environment names match keys case-insensitively on Windows
and case-sensitively on Unix. Selected values are masked before argv and path
metadata serialization regardless of their original Windows key spelling.
New-access findings require a compatible baseline with complete collection.
Incompatible or incomplete baselines remain inconclusive; independent definite
read/write boundary violations still take precedence.
An excluded directory excludes all descendants from snapshot, membership, and
access coverage, even if the accessed path is missing after execution. A read of
an excluded pre-existing output path remains unknown rather than an inferred
newly generated output.
Symlink targets changed by secret or custom-pattern masking are unknown with
`redacted`, set the redacted-path scope flag, and make collection incomplete.
Identical masking placeholders cannot establish an unchanged symlink target.
Input/output, exclusion, and boundary globs use case-insensitive Windows matching,
including the directory itself for `directory/**`. Offline analysis selects case
semantics from each execution's OS, including when read on a different platform.
Snapshot records and directory names are charged before insertion, including the
last walked record. An entry exceeding its collection budget is not retained as
known evidence: the snapshot is incomplete, and overflowing directory membership
is unknown with `collection-limit`.
Global allow/deny read/write boundaries cover both current preparation and target
executions, including snapshot-proven writes. Target input/output coverage and
baseline new-access comparisons remain target-scoped; imported historical
baseline records are evidence, not new preparation work.
Policy checks retain unknown findings for unsupported accesses, uncovered
workspace paths, and lexical aliases, even with only deny rules configured.
Known absolute external paths remain usable for literal access-boundary checks
without implying snapshot or cache coverage. Parent-component aliases are not
lexically collapsed across possible symlinks: absent trustworthy identity,
unmatched aliases make the policy inconclusive rather than passing.
Imported baseline executions use the `baseline` role for evidence-reference
closure. Before any Git preparation or target launch, the baseline count plus
all planned preparation and target executions must fit the 1,056-execution limit.
This conservative preflight counts every supplied historical execution.
Their historical operational failures affect comparison certainty but
never become errors or cleanup recipients of the current invocation. Current
target and preparation outcomes determine its operational exit status.
Clean/repeat verdicts combine policy and comparison results with failed taking
precedence over inconclusive, which takes precedence over passed; incomplete
baseline evidence cannot erase a definite policy violation.
The schema parser
requires the full derived change map, consistent knowledge/scope flags, valid
executable digests, and a 1 MiB aggregate non-evidence string envelope on both read
and save. Git working-tree inclusion diffs against the initially selected commit,
and copies check cancellation between entries. Windows Git source locators omit
verbatim filesystem prefixes while native filesystem operations retain them.

Explain queries use each execution's recorded OS for path syntax: Windows drive,
UNC, and verbatim paths normalize to recorded slash-separated keys even on a Unix
reader. Unix literal backslashes remain filename bytes. Relative queries still
refer to the workspace.
Conflict findings identify concrete write/access/snapshot evidence, including
ancestor directory reads, while retaining candidate status. Inconclusive
comparisons return exit 4. Uploaded release assets must match both size and
GitHub's SHA-256 digest before a new draft can become public. Vendoring imports
require an explicit empty review destination and cannot overwrite product source.

## Issue acceptance scenario coverage

The numbered scenarios below preserve issue #907's acceptance order. Tests live
in the independent workspace unless otherwise stated; native tests use real
injection and child execution rather than mocked access collections.

| # | Scenario | Executable verification |
| --- | --- | --- |
| 1 | Direct/configured argv, spaces, Unicode, failure, stdin | Native receipt, Unicode argv, finite stdin and failed-command tests |
| 2 | Native platform/architecture, static Linux, protected targets | Six native CI jobs; static C fixture; macOS protected/hardened image tests; minimum-OS release gate |
| 3 | File/type/permission/link/directory/ignored/excluded changes | Core snapshot regression and native receipt/directory conflict tests |
| 4 | Attempts versus changes; unreadable/unstable/unknown | Snapshot knowledge tests, native missing reads and partial collection tests |
| 5 | Cache declarations, boundaries, baseline | Native cache/policy fixture plus conservative coverage and comparison checks |
| 6 | Multi-report producers/consumers/conflicts | Native offline queries and concrete directory-conflict evidence |
| 7 | Clean HEAD/included edits, preparation, original preservation | Native clean/repeat test and ordered failed-preparation test |
| 8 | Fresh repeat environments and output contents/sets/modes | Native environment canary and three output-difference fixtures |
| 9 | Environment and mapped comparison limits | Passive executable identity, mapping collision checks and incompatible evidence outcomes |
| 10 | Unsupported, collection overflow, timeout, cancel, descendants | Native protected child continuation, bounded overflow, timeout, cancellation, nested/lingering children |
| 11 | No implicit retention, atomic collisions | Native no-report/collision test and private index cleanup regression |
| 12 | Arguments, environment, contents, stdout canaries | Core argv redaction and native receipt/stdin/environment fixtures |
| 13 | Hostile/large reports, escaping and output separation | Native hostile envelope/forged changes/HTML/JSON tests, schema drift test |
| 14 | Release inventory, checksums, signatures, install/rollback | Python release fixtures, native installer authentication doubles, actual signed-install gate before publication |
| 15 | Public docs, accessibility, fixed ports | Both changed docs apps' pnpm test, route validator regressions, occupied fixed-port checks |
| 16 | Repeatable scans/overhead/spill/limits | Deterministic benchmark driver plus bounded-map and native overflow regressions |

This table maps implementation evidence, not blanket certification. Minimum OS
validation and authentic signed installer runs remain mandatory release conditions.
No real release, Homebrew publication, or docs deployment occurs in this task.

## Reproducing local benchmark evidence

Build the native fixture and CLI with `cargo build --features test-support` from
`crates/runlens`. From the repository root, invoke
`python3 crates/runlens/scripts/benchmark.py --runlens crates/runlens/target/debug/runlens --fixture crates/runlens/target/debug/runlens-test-command --sizes 1000,10000 --samples 5`.
Use `.exe` suffixes on Windows. Run measurements sequentially on an otherwise idle
host. Repeat with `--memory-bytes 65536` for spill pressure and with
`--total-bytes 8192 --expect-incomplete` for a deliberately exhausted collection.
The JSON includes tool digest, native host, deterministic bytes/counts, every
wall-clock sample, and exit codes. These debug-build warm-cache measurements are
procedural evidence, not a performance SLA or release benchmark. Use OS resource
tools for peak RSS and disk accounting; metadata limits do not bound the child
process's memory or output files.

Source copies use bounded cancellable reads, refuse last-component symlink swaps,
and compare file/directory identity before and after copying. Unix literal
backslashes remain filename bytes; non-Unicode symlink targets remain unknown.
Unreadable native fixtures verify unknown content rather than an empty digest.
A single bounded Mach-O parser covers both root preflight and intercepted child
execution: hardened descendants proceed unchanged with incomplete coverage.
Initialization failure fixtures assert that no target side effect occurs.

Unix collector failures cannot panic an intercepted host call. Optional client
initialization, invalid payloads, removed cwd, and exec preparation failures
preserve execution with typed incomplete evidence when observable. Unsupported
macOS children retain the original executable and user preloads while dropping
only the Runlens-owned DYLD library, including the arm64/arm64e boundary. Git
preparation failures log only a bounded enum classification and exit status.

## Local benchmark observation (2026-09-19)

Sequential warm-cache debug-build runs on macOS 26.6.2 arm64 used the pinned
nightly-2026-08-02 toolchain, five samples per case, and deterministic 992-byte
input files. Executable SHA-256: `ba33ea921014a4c0ca2aa0ac355db5f09d21c5706193a7d35aaddc8d23dacfdc`.
No release performance claim follows from this development-host measurement.

| Case | Files | Direct median (s) | Traced median (s) | Traced statuses |
| --- | --- | --- | --- | --- |
| default | 1000 | 0.0172 | 0.3520 | [0, 0, 0, 0, 0] |
| default | 10000 | 0.1561 | 1.5642 | [0, 0, 0, 0, 0] |
| spill | 1000 | 0.0180 | 0.4640 | [0, 0, 0, 0, 0] |
| limit | 1000 | 0.0216 | 0.2040 | [4, 4, 4, 4, 4] |

`spill` used a 65,536-byte memory threshold; `limit` used an 8,192-byte total
collection budget. All limit trials returned incomplete (4). Baseline and traced
wall-clock samples, respectively:

- default/1000: [0.010558, 0.017081, 0.017191, 0.017884, 0.01732] / [0.427886, 0.352025, 0.34786, 0.342392, 0.359469].
- default/10000: [0.107657, 0.155794, 0.185924, 0.156072, 0.158571] / [1.551664, 1.567362, 1.564183, 1.579052, 1.548858].
- spill/1000: [0.010908, 0.018206, 0.017826, 0.01799, 0.017968] / [0.465465, 0.463071, 0.45484, 0.467986, 0.463957].
- limit/1000: [0.01074, 0.021563, 0.022467, 0.021864, 0.021398] / [0.237434, 0.268534, 0.204042, 0.20277, 0.201333].

Every Git metadata/source operation uses a fresh private HOME and an empty regular
global-config file within it. Native Windows arm64 Git rejects `NUL` for this
purpose; real files avoid platform null-device behavior without loading user
configuration. Reused internal config files must remain empty regular files.

Unix rename collection records source and destination write attempts for native
rename/renameat, macOS extended variants, and Linux renameat2. Linux seccomp covers
raw/static calls too. Failed renames still record attempts; only snapshots assert
workspace deletion or creation. An external destination therefore remains visible
to literal write-boundary policies even when only the source is in snapshot scope.

Write allowlists cover snapshot-only directory membership changes on ancestors of
known allowed descendant changes. The bounded ancestor index follows concrete
changed paths, including wildcard patterns, rather than permitting the whole root.
Every sibling and leaf is still checked; explicit denies, direct write attempts,
unknown evidence, and directory type changes retain their original classification.

Unix removal collection covers unlink, unlinkat, rmdir, and libc remove. File and
directory removals, including directory-relative AT_REMOVEDIR, are write attempts
on the named path. Linux seccomp covers native static syscall callers as well.
Missing-path failures retain attempts, and external changes are never invented
as snapshot evidence; external deny-write policies still see the attempted path.

Unix open observations include write attempts for creation/truncation independently
of descriptor read/write access mode, and for Linux O_TMPFILE. Stdio update modes
r+, w+, and a+ include both read and write, including binary-mode variants. Preload
and raw/static Linux syscall collection use the same classification so external
mutations cannot be misclassified as read-only because they lack workspace snapshots.

Windows Detours setup and teardown use the returned LONG error code, with zero
as the only success value. Failures propagate through the existing transaction
abort and typed unsupported-collection marker, preserving child execution while
preventing a complete receipt. Native Windows validation includes an explicitly
invoked checker test using a real DetourAttach invalid-parameter failure.

Shebang scripts execute as requested, but their text digest cannot identify the
kernel-selected interpreter or a subsequent `env`/PATH selection. Until those
interpreter chains are bound, their executable digest is absent and collection is
incomplete. An explicit native interpreter argv can provide native identity.

Executable compatibility requires identity bound to the launched native image:
Linux uses the inspected descriptor with original argv[0], Windows briefly locks
the canonical file and ancestor rename chain through process creation, and macOS
compares the mapped main image vnode reported before main. Retained file metadata
must remain stable through child completion. Missing or mismatched evidence clears
the executable digest and marks the receipt incomplete, including fast processes.

Linux seccomp syscall observation applies to dynamically linked and static native
images, including inline kernel calls and descendants inheriting the filter.
Preload hooks alone are not sufficient evidence of complete Linux collection.

Imported known filesystem states require the collector's kind-specific metadata:
file size/digest and Unix executable bit, directory membership digest, or symlink
target. Inapplicable metadata and known special-file states are rejected. Equal
empty metadata cannot establish known equality in offline verification.

An access beneath a vanished ancestor absent from both observations is outside
known snapshot scope: the ancestor could have been a transient external symlink.
Missing leaf attempts remain distinct from this unknown ancestor identity.

Clean/repeat isolated HOME and cache access attempts remain in receipts under
`${temporary}` with `in_scope=false`; their contents remain outside snapshots.
Only Runlens-owned collector/library paths are omitted from access records.
Temporary isolation therefore cannot erase evidence used by access policies.

Cache output coverage derives directory membership ancestors from concrete known
changed paths matched by output globs, including intermediate wildcard segments.
It shares the bounded ancestor index with write policies; direct ancestor writes,
type changes, unknown states, and unrelated siblings still require coverage.

Clean/repeat distinguishes collection loss from a definite unsuccessful child:
a zero-exit child with incomplete evidence is inconclusive (exit 4), while known
child, policy, or output failures remain failed. Policy analysis still inspects
retained partial evidence, including preparation evidence, after an early stop.

Windows interception treats process-creation attribute pointers and lengths as
untrusted even in the child process. Bounded kernel-assisted copies avoid Rust
references to unreadable memory; parse failure preserves the original NT call
and records incomplete collection instead of panicking the child.

Offline policy input/output coverage uses the same normalized argv, cwd, and recorded-name binding as cache audits. A changed or redacted identity is inconclusive; global boundaries still apply to observed accesses. Clean verification builds the expected identity from the active configuration in each fresh checkout.

Explain searches both access and change keys using Windows ordinal case-insensitive comparison on Windows, preserving the concrete stored evidence paths. ASCII aliases work across analyst platforms. Windows Unicode aliases queried on another OS use simple uppercase candidates and retain an unknown finding because the source OS uppercase table is unavailable; full Unicode expansions do not define filename equality. Unix keys remain case-sensitive.

Windows snapshot stability checks include volume serial number and file index for pathname and opened-handle metadata. Missing IDs cannot establish stability. Same-size, same-timestamp replacement therefore yields unknown evidence even if a detached handle remains readable. These IDs remain transient and are not persisted in reports.

Cache and required declaration audits check static input/output glob language intersections, including directory-root expansion and report-OS casing, even when no observed path lies in the intersection. The DFA construction and product traversal are bounded (8 MiB construction limits, 65,536 product states, four million byte transitions); exhaustion yields unknown evidence rather than passing. Witnesses must be nonempty UTF-8 paths without NUL.

Linux kernel collection includes `readlink` and `readlinkat` attempts for dynamic/static targets, recording the link path rather than the returned target text. Empty-path descriptors resolve to the held link. Unresolvable caller arguments preserve the syscall and make evidence incomplete.

Clean reports import supplied historical baseline executions on every completed report path, including stopped targets and incomplete collection. Policy findings therefore retain valid evidence references when explicitly saved, while historical outcomes remain excluded from current exit and cleanup classifications.

Fresh Windows HOME/cache environment paths use canonical locations with ordinary drive/UNC syntax, avoiding Git-incompatible verbatim prefixes. Git still reads only the private empty configuration. Native Windows regression checks both environment spelling and Git global-config reads.

Linux metadata collection distinguishes operand-free NULL capability probes from `AT_EMPTY_PATH` descriptor reads for statx/fstatat. Kernel-rejected probes without a filesystem operand do not imply lost evidence; descriptor reads remain observable. Collector debug diagnostics expose only stable stages and numeric syscall/error classifications, never raw paths or argument buffers.

Linux installs one user-notification filter before the root image and retains its supervisor through the managed tree. Nested exec/spawn keeps environment preparation but reuses inherited kernel collection instead of requesting a conflicting second listener, including dynamic-to-static execution.

The 1 MiB report-envelope parser budget is scoped to each external read and restored on every exit. Analysis/export rehydration of already bounded internal Entries does not spend that incoming-envelope budget again; memory, row-size, record-count, and spill limits still apply. Regression coverage includes 1,056 executions under a 128-descriptor ceiling and repeated command identities in receipt output.

Inherited stdin/stdout/stderr backed by regular files (or unclassified handles) make collection incomplete because path hooks cannot see I/O through pre-opened handles outside snapshots. Pipe, socket, terminal, and null-device forwarding remains available; stream contents are never captured or persisted. Native regression redirects each stream to an external file and verifies preserved I/O, the child exit status, and an inconclusive offline policy verdict.

Linux raw execve/execveat records the script read and unsupported interpreter coverage because the kernel opens the shebang interpreter without a separate notification. This covers dynamic and static callers, relative paths, and AT_EMPTY_PATH descriptors; the requested script continues, but offline verification cannot pass on missing interpreter evidence. Header inspection is bounded and nonblocking; unreadable images remain incomplete.

Linux syscall collection records getxattr/lgetxattr/fgetxattr and listxattr/llistxattr/flistxattr as input attempts, including missing paths and write-only descriptors. Attribute names and values are not retained. Native dynamic/static fixtures verify external read denials and canary omission independently of ordinary open-read observations.

Unix setsid/setpgid attempts and posix_spawn session/group attributes report incomplete collection before forwarding. Linux kernel collection covers raw and static callers too. These operations can detach children from process-group ownership; an empty original group must not certify that all descendants finished. Detached services remain unsupported and may outlive collection; Runlens does not claim containment or cleanup for escaped groups. Finite native detach fixtures verify continued execution and prevent a policy pass.

Windows NtCreateUserProcess interception distinguishes same-thread CreateProcess injection ownership from direct native creation. Calls outside the suspended-create/inject/resume wrapper emit incomplete collection before forwarding, even when image extraction succeeds. The requested child remains unchanged and in the inherited Job, but its accesses cannot certify a policy pass. A native NT process-creation fixture verifies the child's external write and incomplete policy outcome; wrapper children retain the existing complete-collection checks.

Executable resolution follows the host environment's key semantics: only the exact PATH key participates on Unix, while Windows accepts case variants. A distinct Unix Path variable must never select a different program or replace an absent PATH.

Unix prelaunch descriptor checks enumerate the process descriptor directory and inspect close-on-exec flags. Any nonstandard inherited descriptor or enumeration error makes collection incomplete without closing caller resources. Descriptor 3 and higher-numbered external-file fixtures preserve writes and prevent policy passes; collector/runtime-owned close-on-exec descriptors do not degrade ordinary runs.

Linux statfs/fstatfs reads, including libc statvfs/fstatvfs wrappers, record filesystem metadata input attempts without persisting capacity or filesystem data. Dynamic and static fixtures exercise present/missing external paths and write-only descriptors against deny-read policies.

Conflict analysis matches Windows case aliases, including UNC paths and directory-read ancestors, and preserves each execution's stored key in evidence and usage results. Unix paths stay case-sensitive. Windows uses native ordinal comparison; other hosts retain explicit Unicode case-table uncertainty. Alias comparisons are capped at four million per analysis, returning partial findings and unknown evidence on exhaustion.

Linux libc `execveat` is forwarded unchanged to inherited kernel collection. Relative paths, descriptor execution, no-follow errors and invalid flags preserve the child’s native semantics.

Nested Unix `execvp`/`execlp` and Linux `execvpe` retain libc’s shell fallback for executable text without a shebang. That child-requested fallback still marks protected interpreter coverage incomplete when applicable.

Unix variadic exec collection preserves native argument limits: argv storage uses checked allocation, while the OS enforces its effective argument/environment byte budget and returns the original errno.

Windows injection validates the exact DLL pathname after ANSI encoding. An OS short-path alias is allowed only when it also round-trips exactly. If no representable name exists, initialization returns typed Unsupported before target creation; no lossy path reaches Detours.

Windows NT/verbatim UNC observations retain their absolute network root when converted from collector records. Equivalent UNC bases can be stripped lexically; external shares never become current-worktree-relative observations.

Frozen source copies preserve dangling symbolic links. On Windows the copied link’s directory/file kind comes from its own metadata, so preparation can create its target later without changing link semantics.

Windows tracing finishes fallible setup and Job assignment while the child remains suspended. After resumption the original child handle transfers directly to lifecycle collection; no optional handle duplication can misclassify an already-started command as an initialization failure.
The same original handle remains the required target for both Detours DLL
injection and payload copying before resumption; removing a redundant duplicate
does not change either native call's ABI or target ownership.

The native source-selection regression asserts source/environment comparability independently of observed execution equivalence. Fresh processes may have different access sets despite the same HEAD; clean must fail on those observed deltas while reserving inconclusive for incompatible source policies or revisions. Test failures include the explicit offline comparison for diagnosis.

Native Unix test commands mark ambient nonstandard descriptors close-on-exec
before launching Runlens, because a cold Cargo build can otherwise leak its
jobserver descriptors into fixtures. The boundary belongs to the test harness;
production retains all caller descriptors and its conservative incomplete
classification. A nested native harness with an injected descriptor verifies
isolation, while explicit descriptor fixtures re-enable their selected handles
and continue to require incomplete collection and non-passing policy checks.

Directory changes depend on directory metadata: Unix chdir/fchdir attempts are
reads resolved before cwd changes, including missing paths and renamed descriptors.
They do not imply directory enumeration. Linux inherited seccomp also covers raw
syscalls from static binaries; unresolved descriptor/path evidence stays incomplete.

Comparison and baseline compatibility require a present matching source revision.
Two absent revisions never bind source identity, including non-Git runs and failed
Git discovery. Available differences remain inspectable, but comparisons and
new-access absence claims are inconclusive; definite independent policy violations
still fail. Native regression coverage uses separate non-Git workspaces and every
one-sided/two-sided missing-revision combination.

macOS observes dyld image additions, including images mapped before tracer attachment. Each loaded library contributes read evidence. The separately bound main executable and private collector are excluded from this loader check; only active shared-cache images with matching Mach-O cache flags avoid incompleteness. Loose/custom libraries retain their original execution but cannot certify collection, because naming a loaded image does not bind earlier loader reads or initializer accesses. Native tests rebuild a custom external dylib without changing its executable and verify changed stdout, incomplete receipts, and read-policy denial.

Spilled metadata and per-execution collector files share a private storage root.
A weak process registry lets retained baseline indexes and later snapshots share
that namespace; indexes keep it alive only until their last owner drops. Both
snapshot exclusion and collected-access filtering omit the entire private root,
while caller temporary files and fresh HOME/cache evidence remain observable.
Cleanup closes each SQLite connection/file before releasing the directory owner,
and failures retain the existing typed cleanup classification. Native enumeration
fixtures force before-snapshot spills, read the temporary tree, verify no private
index appears as an external dependency, and check cleanup after serialization.

Linux io_uring setup, enter, and registration attempts mark collection incomplete before forwarding the unchanged syscall. Ring filesystem operations are not ordinary syscall notifications, and SQPOLL can submit without enter. Failed attempts conservatively retain the same limitation; neither policies nor cache audits may certify these runs. Native dynamic/static fixtures compare direct and observed setup/SQPOLL success or errno and independent invalid-descriptor enter/register calls.

New-access policies compare Windows baseline paths with native ordinal case-insensitive equality (portable for ASCII) and combine access modes across proven aliases. Current report keys remain the finding evidence. Non-Windows Unicode table uncertainty or exhausting the shared four-million-comparison budget yields unknown evidence instead of claiming new access or equivalence. Unix remains case-sensitive.

Workspace access scope requires each ancestor below the workspace root to be a known directory both before and after execution. A removed or newly created directory cannot prove that a transient link did not redirect the access outside the workspace. These accesses retain unknown scope even when snapshots prove output creation; verification stays inconclusive unless a definite violation already fails it. Missing leaf attempts under established directories remain in scope.

The report parser rejects contradictory child outcomes containing both an exit code and a termination signal, regardless of collection completeness or errors. Any signal also prevents success and yields failed-execution evidence in direct in-memory policy/cache analysis. A signal-only termination remains a valid report outcome.

Linux inotify watch registration records the named file or directory as a read attempt, including failed registration. Dynamic/static syscall fixtures verify external deny-read policies. Event contents and ordering are not retained.

Windows file-object interception copies OBJECT_ATTRIBUTES, nested UNICODE_STRING,
and bounded UTF-16 bytes with ReadProcessMemory before inspecting them. Invalid
addresses, lengths, or embedded NULs mark collection incomplete and preserve the
original NT call. The native malformed-file-attributes regression compares exact
NT statuses with an untraced process and verifies child continuation; the preload
unit test covers each pointer layer and malformed lengths. Remove this local
patch only when upstream provides equivalent bounded, non-dereferencing handling.

Linux static exec removes only the tracer-owned LD_PRELOAD entry, preserving
caller preload bytes and dynamic-descendant loader behavior. The regression
compares static environment output and a real caller-library symbol in a dynamic
descendant against untraced execution. Removal: upstream must preserve caller
preloads across static exec with equivalent unit and native coverage.

All Runlens publication versions share one workflow-wide concurrency group with
cancellation disabled, covering validation, signing, install checks, release and
Homebrew mutations. Dry runs use run-specific groups and cannot interrupt it.

Each authenticated install matrix row declares its expected platform and checks
the executing host before downloading or installing archives. The guard requires
macOS 13, Windows NT 10.0.19045, or Ubuntu 22.04 and the matching native
architecture. Rosetta and mismatched Windows native architecture are rejected;
runner labels and a successful doctor on a newer OS cannot replace this check.

Execution preflight validates every direct, named, and preparation argv before
launch (1–1024 arguments, at most 32768 bytes per argument, no NUL). The same
metadata validator used for report acceptance checks masked field sizes, JSON
string expansion, vector counts, and the 1 MiB envelope before snapshots.
Clean/repeat additionally budgets all planned preparations, targets, repetitions,
and imported baseline execution metadata before the first preparation. Planning
reserves a digest and 4 KiB for built-in limitation notices. Invalid metadata is
invalid-input even without --save; no child side effects occur before rejection.

Windows NtDeleteFile records a write attempt before forwarding the original
object attributes, including missing external paths. The native regression
compares NT results and deletion behavior with untraced execution and requires
a deny-write violation. Remove this local interception patch when upstream
provides equivalent native deletion coverage with safe attribute copying.

macOS readlink/readlinkat interception records attempted reads of the link path
before forwarding, including missing and directory-relative paths. It never
records returned link text as another input. Native fixtures compare byte counts,
text and errors with untraced execution and enforce external read policies.
Remove when upstream supplies equivalent macOS link-read coverage. Linux keeps
its existing seccomp hooks to avoid recursive descriptor-path resolution.

Secret-value masking uses the union of literal matches in the original text.
Overlapping selected/sensitive environment values, repeated self-overlaps and
Unicode matches cannot expose suffixes or depend on environment ordering;
replacement markers are never matched again as secret values.

Windows doctor and execution preflight require a successful IsWow64Process2
query proving a non-emulated process whose native machine equals the compiled
Runlens artifact architecture. Query failure, unknown machines and mixed
x64/arm64 execution are unsupported before tracing starts.

Unix execution preserves the exact requested argv[0], including PATH names,
relative paths and aliases. Resolving and retaining the native executable for
launch identity must not rewrite the command-visible argument zero. Native
regressions compare each form with direct execution and retain the image digest.

Conflict analysis retains / as the terminal directory-read ancestor of absolute
POSIX paths, with concrete reader/writer evidence. Ordinary root file reads do
not cover descendants, and relative or workspace-placeholder paths never acquire
a synthetic POSIX root.

macOS getxattr/listxattr and descriptor variants record input attempts, including size queries, missing attributes and missing paths. Only the file path enters evidence; native results and buffers remain unchanged, and attribute names/values remain excluded.

Linux fanotify_mark records read attempts for absolute, directory-relative and NULL-path descriptor targets, including failed registrations. Flush operations ignore their unused path operand. The collector never stores fanotify event payloads or claims their contents were observed.

macOS execvP uses its explicit search path rather than ambient PATH. It shares executable observations, protected-child handling and native text-file shell fallback with other exec families. Native tests preserve custom argv zero, missing-image errors and protected/script outcomes.

Failed Windows normalized-name lookup for a relative NT root handle records collection loss and forwards the unchanged operation. Native coverage exercises the failure branch with well-formed attributes and a non-directory handle; successful SMB operations with denied name lookup remain incomplete rather than disappearing.

Masked recorded command names are inconclusive identities before declaration lookup, including explicit-pattern and selected-value masking. Coverage checks retain unknown evidence while independent read/write denials still apply. Clean verification saves its inconclusive result instead of failing to parse the configured name.

SystemRoot, WINDIR, COMSPEC and PATHEXT are implicit OS launch context only on Windows. On macOS/Linux, clean and repeat omit them unless explicitly selected, including preparation stages. Selected names remain visible as environment metadata without values, and selected values cannot prove repeat compatibility.

Debug source-revision diagnostics distinguish temporary-directory creation,
isolated environment preparation, managed Git failures, UTF-8 decoding and
revision-shape validation. Only static operation/error classifications and IO
error kinds are logged; Git stderr, paths, argv and metadata bytes remain private.
The stopped-clean baseline regression enables these diagnostics to expose the
intermittent Windows Git HEAD failure without retries or weakened assertions.

Linux file-handle lookup is metadata input. name_to_handle_at records path and empty-path descriptor reads even when a filesystem rejects handles or requires a larger buffer; native results are preserved and handle bytes/mount IDs are omitted.

macOS posix_spawn_file_actions_addopen marks collection incomplete at action construction, including unused or subsequently failing actions. Darwin opens those files before child injection; retaining the requested spawn and I/O is mandatory. No opaque action structure or stream body is read.

POSIX installation stages a private directory containing the fixed `runlens` basename and renames into the explicit install directory. A destination directory created after the last type check makes mv fail, preserving its contents and removing owned staging instead of reporting a nested installation as success. The installer fixture injects this race at the mv boundary.

Frozen-source copies preserve access and modification timestamps, including link metadata without following links and directories after copying descendants. Each round restores copy-induced atime changes in the owned source; original-worktree reads never restore or write source timestamps. HEAD timestamps are those of its single selected checkout (Git does not store worktree times). Creation/change timestamps and inode identities are not portable frozen metadata.

macOS syscall() adapters preserve all integer argument registers and the caller stack before tail-forwarding to libSystem, marking bypassed collection incomplete. fork() marks loss before creating a child, including one that detaches via inline kernel instructions. These conservative markers do not create kernel enforcement or claim arbitrary inline operations are observable. Native tests cover variadic operands/errno, raw setsid/setpgid descendants, and fork followed by inline setsid.

Repetition-directory removal failures retain completed preparation/target evidence and baseline references. The just-completed target keeps its actual child exit status and adds CleanupFailed with incomplete collection; further rounds stop and verification is inconclusive unless independent evidence proves a policy failure. Outer owned-directory cleanup still runs, and explicit save can persist the report.

Native cache-overlap fixtures retain debug-stage diagnostics and the command result on observation failure. This preserves the CI distinction between launch/collection/snapshot failures and offline cache analysis without retries or relaxed completeness assertions. A host-specific failure without those diagnostics is unresolved evidence, not proof of a backend repair.

macOS getattrlist, getattrlistat and fgetattrlist record metadata read attempts with unchanged native request buffers, options and results. getattrlistbulk retains the directory input but marks collection incomplete because it can expose unbound per-child metadata. The native fixture compares file/directory/missing behavior and enforces external read boundaries without persisting attribute payloads.

macOS cloning observes source reads and destination writes for pathname, directory-relative and source-descriptor APIs. Access attempts remain separate from actual file changes. Native fixtures compare copied bytes and errors for new/existing destinations and missing sources and independently enforce each boundary; bodies never enter reports.

Unix file-access interception copies path operands through self process_vm_readv on Linux and mach_vm_read_overwrite on macOS before creating a string. Fixed sub-page reads stop at NUL and cap paths at PATH_MAX; stream mode strings are bounded separately. Invalid/null/protected/unterminated/overlong path buffers report collection loss while native calls retain their operands, errors and continuation. Tests also cover a valid NUL at a protected-page boundary and file-body canaries.

Windows .git administrative directory aliases are excluded with native ordinal component semantics before snapshots enumerate or charge their descendants. Unix retains exact spelling. The native Windows fixture renames a real Git repository to .GIT and populates enough administrative entries to exceed the snapshot budget if pruning fails; Git HEAD discovery and ordinary source observations must still succeed.

Linux seccomp marks mount/umount2/move_mount/mount_setattr, unshare/setns, chroot/pivot_root and fsopen/fsconfig/fsmount/open_tree/fspick attempts incomplete. clone/clone3 remain supported only without namespace-creation flags. Dynamic/static fixtures preserve native success or errors; a private bind-mount fixture proves parent workspace state stays unchanged while the command reads external bytes through a remapped workspace spelling. No mount enforcement or sandbox guarantee is added.

The library-level cleanup-failure native test runs in an isolated subprocess with null stdin, piped output and close-on-exec ambient Unix descriptors. Redirected runner files must not stop collection before the injected second-round cleanup failure; separate regressions exercise file streams and inherited jobserver-like descriptors.

Windows timestamp preservation opens files with FILE_WRITE_ATTRIBUTES, no-follow reparse handling and directory semantics. It never requires content-write access or clears read-only attributes in the frozen Git object database. Failures log only the stage, operation, error kind and OS error number, never raw source paths. The native read-only-copy regression repeats preservation across three rounds; SetFileTime access requirements are documented by [Microsoft](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-setfiletime).

Unix descriptor fstat reads are metadata inputs even after a write-only open. Linux kernel collection covers static callers, and macOS interposes the native descriptor API without reading caller output. Anonymous pipe/socket procfs identities are excluded only when they match the exact kernel form; ordinary file paths and named FIFOs remain inputs.
