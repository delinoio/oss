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
Root normalization requires a path start and an exact root or separator boundary;
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
target. The inspected file handle stays open through the before-snapshot; immediately
before launch Runlens rechecks platform eligibility, pathname file identity, and
metadata stability. A changed identity returns incomplete before launching the
replacement. This is a launch-boundary race check, not an OS sandbox or a guarantee
against a concurrent replacement after the final check.
The replacement regression retains the inspected handle, renames the original to
a spare path, and installs a different file at the original path with matching
length and modification time. This keeps the identity check covered on Windows,
where the temporary-file persistence API cannot overwrite an open destination;
the extra rename can be removed when that API supports open-file replacement.
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

Offline checks never pass a report with no target execution.
Windows root masking and scope classification use the native ordinal casing
rules for workspace, home, and temporary roots, preserving component boundaries
and converting UTF-16 match offsets back to UTF-8 without byte-length guesses.
The same root comparison applies to exclusions; Unix roots stay case-sensitive.
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
closure. Their historical operational failures affect comparison certainty but
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
