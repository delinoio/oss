# clibox file-access workflow contract

## Status and ownership

Issue [#971](https://github.com/delinoio/oss/issues/971) defines seven unreleased file-access workflows. `crates/clibox-fspy` is a private, MIT-licensed, non-publishable Cargo workspace member. It owns workflow parsing, execution lifecycle, the versioned trace, analysis, and interactive controls. `crates/clibox` owns root composition. The existing private fspy source fork owns platform interception changes; pnport-mode interception and existing pnport behavior remain separate.

The current implementation contains the common trace schema, complete-record reader, and comparison model. No #971 command is exposed in the clibox executable yet. The feature is not complete or released while any workflow or target gate below remains outstanding.

## Platform and observation boundary

The required native targets are macOS x64/arm64, Windows MSVC x64/arm64, Linux GNU x64/arm64, and Linux musl x64/arm64. Linux uses syscall-entry and syscall-completion observation through ptrace. macOS and Windows extend the existing injected fspy approach. Every backend fails explicitly if tracing, injection, permission, or ownership cannot be established. No external tracing executable, runtime download, elevation, existing-process attachment, daemon adoption, or protection bypass is allowed.

The declared observation boundary covers synchronous file opens/closes, reads/writes and positional equivalents, metadata queries, directory enumeration, pathname mutations, executable images, and supported process ancestry. mmap, asynchronous I/O, and interception bypasses are outside the guarantee. Completion means complete within that declared boundary. An attempted open, existence check, metadata query, or old fspy `READ` classification is not evidence of a successful content read.

## Invocation and output

Execution commands accept literal tokenized arguments after `--`, inherit environment and working directory, and trace the launched process and supported descendants. `--root` changes path selection/classification only. File selectors union repeatable includes and subtract excludes; paths escaping the existing root are rejected. Project paths and external accesses remain distinguishable, and symlink aliases retain logical and resolved identity.

The commands are `record`, `compare`, `autowatch`, `assetcov`, `latencylab`, `min-repro`, and `fbreak`, with the exact interfaces and acceptance criteria in #971. Report commands own stdout while forwarding child stdout/stderr to stderr; autowatch preserves child stdio, and fbreak reserves control input and closes child stdin. Human, JSON, and quiet reports use existing clibox conventions. Explicit file output uses private atomic publication; `--force` is valid only for an explicit file destination. `--output -` means stdout, while `./-` names a file. Existing artifacts survive failed publication.

## Trace schema and validation

`record` produces NDJSON schema version 1. One header contains a UUID-v7 execution ID, platform, backend, root, and exact coverage declaration. Every operation has correlated start/completion events identifying process/thread and parent process, operation kind, one or two paths, relative monotonic time, native result/error, applicable byte count, and injected delay. A final summary declares completeness, child result, counts, and stable failure classification. Sequence numbers describe collector receipt order, not a total execution order across threads.

Paths are base64-encoded raw Unix bytes or Windows UTF-16LE units, with explicit project/external scope. Records exclude file contents, environment values, and complete argv. Diagnostics separately exclude raw paths, argv, environment values, file contents, matching strings, and raw dependency errors.

The reader rejects unsupported versions, malformed/duplicate/unpaired events, event loss, missing summaries, incomplete traces, invalid results, and limits. A complete read requires an explicit complete summary and reconciled counts. It never infers completeness from EOF or child success. Default limits are 1,000,000 events and 256 MiB of encoded trace. Explicit local outputs have no automatic history, retention, or deletion; users manage published artifacts.

## Workflow invariants

- `compare` reads two complete compatible records, compares project-relative paths across distinct roots, and reports added/removed paths, operation-kind changes, counts, failures, timing, and separate external accesses. PID and absolute start-time differences are ignored. `--fail-on-change` gates only added/removed paths or changed operation kinds.
- `autowatch` runs immediately, then serially watches observed file inputs, directory queries, and absent-path dependencies. Its default debounce is 200 ms. Success replaces the watch set; child failure unions previous and new dependencies. Changes during discovery/replacement are preserved. Confirmed self-writes do not rerun; empty or ambiguous watch sets fail with guidance.
- `assetcov` fixes its denominator before execution from existing selected regular files and deduplicates actual files. Only a successful positive-byte read, or successful EOF read on an initially empty file, covers an input. A missing denominator, incomplete trace, or child failure cannot yield successful coverage. `--fail-under` is an optional 0–100 gate.
- `latencylab` defaults to read operations and three baseline/delayed pairs, alternates conditions with equal tracing, injects a positive selected delay before each matching operation on its calling thread, and reports requested/observed delay, operation time, execution time, medians, and slowdown. It stops on first failure and never resets filesystem or caches.
- `min-repro` snapshots selected eligible inputs before execution, checks an explicit nonzero exit and stderr substring, collects observed required project inputs, verifies the candidate once from another working directory with the same predicates and without original-tree dependencies, then atomically publishes only a verified new bundle. It preserves required internal links/targets, lists external dependencies, blocks the sensitive paths specified in #971 without bypass, and never bundles runtimes. A separate cwd is not an OS sandbox or portability guarantee.
- `fbreak` requires a real control TTY before spawn. Matching project operations stop the calling thread immediately before execution; concurrent stops queue for `n`, `c`, or `q` control. It does not provide whole-process stops, debugger inspection, child input, or a JSON control protocol. Control loss and cancellation release or terminate owned callers safely.

## Supervision, limits, and validation

Execution has no default timeout; optional `--timeout` applies per autowatch run. Graceful cleanup waits `--kill-after` (default 5 seconds), then forces owned descendants and confirms cleanup within five more seconds. Surviving descendants, trace loss, failed cleanup, and timeout cannot become success. Default reproduction snapshot/result limits are each 1 GiB and 100,000 files. Positive CLI overrides enforce all limits without dropping events or appearing complete.

Exit codes are 0 success, 1 runtime/analysis failure, 2 invalid arguments, 124 execution timeout, 130 handled Ctrl+C/Windows Ctrl+Break or interactive quit, and 143 handled Unix SIGTERM. With sound tracing and cleanup, `record`, `assetcov`, and `fbreak` preserve child failure status and supported Unix signal identity. Logging uses structured `tracing` on stderr with stable classifications, identifiers, counts, stages, and timing; actionable errors remain visible when logging is disabled.

Completion requires automated fixtures for all seven workflows and the failure/privacy cases in #971, root `cargo test`, formatting/lints, pnport regression coverage for shared interception changes, public-docs frontend tests if that surface changes, native and installed npm/pnpm tests on all eight targets, and the package test commands. Generated repository-owned `dist` directories are removed before delivery. Publication and mandatory manual platform certification are separate.
