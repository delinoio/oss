# File-access workflows

`clibox fspy` contains seven **unreleased** local file-access workflows. They are not in published clibox 0.1.6. Check `clibox --version` and `clibox fspy --help` before using an example. The implementation is still being validated across the native targets; do not use a trace from a platform that reports `tracing_unavailable` as evidence of coverage.

All execution commands launch the program after `--` with its literal arguments. They inherit your working directory and environment. `--root DIR` selects the existing project root used to classify and select paths; it does not change the child's working directory. Run them with your current user permissions. They do not attach to an existing process, elevate privileges, or trace a daemon after it leaves the supported descendant tree.

## Record and compare

```sh
clibox fspy record --output before.ndjson -- cargo test
clibox fspy record --output after.ndjson -- cargo test
clibox fspy compare before.ndjson after.ndjson --fail-on-change --json
```

`record` writes a versioned NDJSON header, paired operation starts and completions, and a final summary. The record includes native success or failure, bytes read or written where applicable, operation timing, selected path identity, and supported process ancestry. An attempted open or metadata check is not proof of a content read. A missing or incomplete summary cannot be compared as a complete trace. `compare` reports added or removed project paths, changed operation kinds, counts, failures, timing, and external accesses separately. `--fail-on-change` gates path and operation-kind changes; count and timing differences remain informational.

The declared boundary covers synchronous open/close, read/write and positional equivalents, metadata, directory queries, pathname mutations, and supported descendants. It excludes memory-mapped I/O, asynchronous I/O, and interception bypasses. Sequence numbers reflect collector receipt order and do not promise a total order between threads. Comparison does not compare file contents.

## Rerun from observed inputs

```sh
clibox fspy autowatch --include 'src/**' --debounce 200ms -- cargo test
```

`autowatch` runs once immediately, then watches the selected project inputs actually queried by the command. It includes missing-path checks and directory queries. Runs are serial; a change during a run schedules one later run. A successful run replaces the dependency set. A failed child run retains old dependencies and adds new ones. Confirmed write-only outputs do not trigger a rerun. If no input can be watched, or reads and self-writes cannot be distinguished safely, the command exits with guidance instead of watching the entire tree. Stop it with Ctrl+C.

## Measure asset reads

```sh
clibox fspy assetcov --include 'assets/**' --fail-under 80 --json -- cargo test
```

`assetcov` fixes its denominator before launch from existing selected regular files. A file counts as covered only after a successful positive-byte read. An initially empty file counts after a successful EOF read. Opening, metadata inspection, and failed reads do not count. Aliases of the same file count once. `--fail-under` accepts 0–100 and is optional; a child failure remains a failure regardless of coverage. The report describes the whole test command, not individual test cases.

## Experiment with delay

```sh
clibox fspy latencylab --include 'assets/**' --delay 5ms --op read --runs 3 -- cargo test
```

`latencylab` alternates baseline and delayed runs, three pairs by default. It adds the selected positive delay immediately before each matching operation on that calling thread and reports requested and observed delay, operation and execution times, medians, and slowdown. A failed run or no matching operation ends the experiment. The command does not reset files or caches. Results describe these executions and do not predict a storage device's performance.

## Collect a verified reproduction

```sh
clibox fspy min-repro --include 'src/**' --bundle-dir ./repro \
  --expect-exit 1 --expect-stderr 'failure' -- cargo test
```

`min-repro` privately snapshots selected eligible files before the original run, then collects the project inputs that run needed. It runs the candidate once from a separate directory and publishes a new bundle only when both runs have the requested nonzero exit and fixed stderr substring, the candidate uses no original-tree input, and its project inputs were collected. The bundle holds copied input files, required internal links and targets, a file/hash manifest, external dependency names, and English rerun instructions. It excludes generated output, the full command arguments, and the stderr substring. The destination must be a new directory; it never overwrites an existing bundle. A separate working directory is not an OS sandbox or a guarantee that the bundle works on another machine.

Collection rejects required `.git` or `.ssh` components, `.env` and `.env.*` files, `id_rsa`, `id_dsa`, `id_ecdsa`, `id_ed25519`, and `*.pem`, `*.key`, `*.p12`, or `*.pfx`. An internal link is preserved; a link escaping the root is rejected. The denylist does not inspect arbitrary source files for secrets, so review a bundle before sharing it. There is no repeated shrinking search: the result is the verified observed-input set.

## Pause before an operation

```sh
clibox fspy fbreak --include 'assets/**' --op read -- cargo test
```

`fbreak` requires a real control terminal. It pauses a matching calling thread immediately before its operation and displays the path, operation, process ID, and thread ID. Other threads may continue. Press `n` to release the current match and stop at the next one, `c` to release waiting matches and continue without more breaks, or `q` to terminate the owned run. The child's stdin is closed. This is not a whole-process debugger and does not provide stack inspection or an input-forwarding protocol.

## Output, limits, and recovery

`record` emits NDJSON. `compare`, `assetcov`, `latencylab`, and `min-repro` offer human, `--json`, and `--quiet` reports. Report-producing commands forward child stdout and stderr to stderr so stdout remains available for the result. `autowatch` preserves child stdio. `--output FILE` writes a report atomically; `--force` requires an explicit file destination. Omitted output and `--output -` use stdout, while `--output ./-` names a literal file. `--quiet` suppresses results, not failure diagnostics.

Traces are limited to 1,000,000 events and 256 MiB by default. Reproduction snapshots and result bundles each default to 1 GiB and 100,000 files. Positive limit overrides are available in command help. Commands have no default timeout; `--timeout` sets one execution budget, and `autowatch` applies it to each run. Cleanup first allows the configured `--kill-after` grace period (default 5 seconds), then forcefully stops owned descendants. Incomplete tracing, exhausted limits, or failed cleanup cannot produce successful analysis.

Statuses are 0 for success, 1 for analysis or runtime failure, 2 for invalid input, 124 for timeout, 130 for handled Ctrl+C or interactive quit, and 143 for handled Unix SIGTERM. When tracing and cleanup succeed, `record`, `assetcov`, and `fbreak` preserve a child's failure status and supported Unix signal identity. Logging with `RUST_LOG=clibox=debug` adds structured, redacted progress to stderr; child output is forwarded as provided by the child and may contain its own secrets.

Records intentionally contain accessed paths. Reports and reproduction bundles can also expose path names or selected file contents. They are explicit local artifacts with no automatic retention or deletion: inspect and remove them manually when finished. For `tracing_unavailable`, check platform support, permission, injection compatibility, and command help. For a missing or incomplete summary, rerun the command after repairing the reported failure; do not reuse an incomplete trace. For an empty selector, confirm the root, include patterns, and excludes. For a reproduction mismatch, check that the command works from another cwd and does not require an absolute path back into the original project.
