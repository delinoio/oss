# clibox fspy workflow contract

## Scope

Issue [#971](https://github.com/delinoio/oss/issues/971) defines seven local file-access workflows: `record`, `compare`, `autowatch`, `assetcov`, `latencylab`, `min-repro`, and `fbreak`. This document records their implementation boundary; the issue remains the acceptance contract. The private, Apache-2.0 `clibox-fspy` crate owns the command family, execution, records, analysis, and controls. `clibox` owns only root command composition and redacted parser errors. The existing fspy source fork and its MIT notices remain separate.

## Observation boundary

The current fspy `PathAccess` result is an attempted path-access classification. It cannot establish a successful content read, operation result, duration, or pre-operation intervention. No workflow may promote it to such evidence. A complete execution requires a new operation stream with paired start/completion events and supervised descendant cleanup. Linux must observe syscall entry and exit with ptrace; macOS and Windows extend their injected hooks. A failed injection, lost event, surviving owned descendant, or unavailable tracer makes the execution incomplete. Neither mmap nor asynchronous I/O is in the supported guarantee.

The Linux ptrace supervisor uses `PTRACE_GET_SYSCALL_INFO` to pair selected syscall entries and completions, follows fork/clone/vfork descendants, serializes local sessions sharing `waitpid`, and bounds cancellation, timeout, and surviving-descendant cleanup. Its callback may hold the calling thread before a syscall. The Linux capture layer decodes selected syscall paths while the tracee is stopped and pairs native results into version-one records, including actual read bytes and descriptor identity. This remains a private backend foundation: operation coverage, exceptional paths, event byte limits, and GNU/musl target validation must be completed before the command family can expose it as a complete tracer.

The private command module currently has Linux implementations of `record`, `assetcov`, and `latencylab`, plus platform-neutral `compare`. It is deliberately not composed into `clibox` while `autowatch`, `min-repro`, `fbreak`, and macOS/Windows tracing remain incomplete. Integration tests may invoke the private module directly; no release or user-facing availability is implied.

## Record and analysis

Records are explicit, versioned NDJSON outputs, bounded by event count and encoded size. The header declares a UUID-v7 execution ID, backend, root, and exact observation coverage. Each start/completion pair carries a correlation ID, process/thread and ancestry, lossless native path representation, operation kind, receipt sequence, monotonic time, result, byte count when applicable, and requested/observed injected delay. The terminal summary states completeness, child outcome, counts, and stable failure classification. Receipt sequence is not a global execution order across threads. A parser rejects unknown versions, malformed or unpaired events, exceeded limits, missing summaries, and incomplete executions before analysis.

Paths are encoded as native Unix bytes or Windows UTF-16 code units; a display string must never replace the native identity. Project paths retain both logical and resolved identities so internal aliases can be counted once. External accesses remain distinguishable. Records never contain file contents, full argv, or environment values. Product artifacts intentionally contain access paths; `tracing` diagnostics must not.

A failed native call may supply a null, invalid, or overlong pathname pointer, or an invalid relative directory descriptor. The Linux decoder records the paired native result with an explicit `path_unavailable` marker instead of inventing a path or turning that ordinary failed call into an incomplete trace. Path-based workflows cannot select such an operation as a project input.

The pre-execution asset denominator walks the selected root, follows only internal symlinks, and deduplicates file identities, including hard links, without holding one open descriptor per file. A read completion must carry the opened file's identity and actual byte count; the analyzer cannot recover it reliably from a pathname after the execution. A missing identity cannot cover a selected file.

## Workflow semantics

- `autowatch` runs once immediately, then watches observed project inputs, directory queries, and absent paths. It runs serially, debounces by 200 ms by default, replaces dependencies after success, unions new dependencies after child failure, and rejects an empty or unsafe watch set. Confirmed self-writes alone cannot trigger a rerun.
- `assetcov` fixes the selected existing-file denominator before execution. Only a successful positive-byte read, or a successful EOF read of an initially empty file, covers a file. It deduplicates resolved aliases and preserves child failure separately from a threshold failure.
- `compare` accepts only complete compatible records, compares project-relative paths across roots, separates external accesses, and gates only added/removed files and changed operation kinds when requested. Counts and timing remain informational.
- `latencylab` alternates equally traced baseline and delayed runs, three pairs by default. Delay occurs before selected operations on their calling threads. It reports requested delay, observed injection, operation time, and execution time separately, stopping at the first failed execution.
- `min-repro` snapshots selected eligible inputs before the original run, collects observed dependencies, verifies the expected nonzero exit and stderr substring in a separate candidate directory, proves no original-tree or uncollected project read, and only then publishes a new bundle. It blocks credential-like paths listed in issue #971 and does not promise an OS sandbox or cross-machine portability.
- `fbreak` requires a control TTY and stops the matching calling thread before the operation. Controls `n`, `c`, and `q` respectively advance, continue, and terminate. Loss of the control channel releases or terminates all waiting callers through owned-process cleanup.

## Limits and publication

Defaults are 1,000,000 events, 256 MiB trace, and 1 GiB/100,000 files for both reproduction snapshot and result. All overrides are positive. There is no default execution timeout. Cleanup requests graceful termination, waits five seconds by default, then forces termination and verifies cleanup within five additional seconds. Explicit file outputs use the clibox atomic publication and `--force` rules; a failed publication preserves the prior destination. Temporary private state is removed after handled failures and cancellation. No automatic history, artifact retention, or release publication is added.

## Validation

Native behavior tests must exercise all seven commands and the eight supported targets, including Alpine consumers and pnport regressions where interception changes. Run root `cargo test`, formatting and applicable lint checks, `pnpm --filter @delino/clibox test`, `pnpm --filter @delino/clibox test:package`, and public-docs `pnpm test` when that frontend changes. Package and CI inventories must include the companion crate and affected native sources. Do not count parser, fixture, or macOS-only tests as validation for other native targets.
