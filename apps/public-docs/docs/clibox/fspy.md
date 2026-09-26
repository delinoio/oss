# File access workflows

The `clibox fspy` command family is implemented in source for a future clibox release. It is **not included in the published 0.2.0 packages**. Check `clibox fspy --help` after upgrading to a release that includes it.

These commands run a newly launched program with your current user permissions. Put the program and its arguments after `--`; clibox does not interpret them as a shell command. The default project root is your current directory. `--root DIR` changes which paths count as project paths without changing the child's working directory.

## Commands

| Command | Use |
| --- | --- |
| `record` | Save a complete execution as versioned NDJSON. |
| `compare` | Compare two complete records. |
| `autowatch` | Rerun when observed project inputs change. |
| `assetcov` | Measure which selected existing files were actually read. |
| `latencylab` | Compare baseline runs with runs that delay selected file operations. |
| `min-repro` | Collect observed inputs and verify a failing command in a separate working directory. |
| `fbreak` | Pause a matching calling thread just before a file operation. |

```sh
clibox fspy record --output trace.ndjson -- cargo test
clibox fspy compare before.ndjson after.ndjson --fail-on-change
clibox fspy autowatch --include 'src/**' -- cargo test
clibox fspy assetcov --include 'assets/**' --fail-under 80 -- cargo test
clibox fspy latencylab --include 'src/**' --delay 10ms --runs 3 -- cargo test
clibox fspy min-repro --include 'src/**' --bundle-dir repro --expect-exit 1 --expect-stderr 'failed' -- cargo test
clibox fspy fbreak --include 'config/**' --op read -- ./app
```

`--include` values are repeatable; matching inputs form a union, then repeated `--exclude` values are removed. Selection is limited to the project root. The selectors for the six execution workflows never select external paths. `record` retains external access information and `compare` reports it separately.

## Records and coverage

A record has a versioned header, paired operation start and completion events, and a terminal completeness summary. It identifies the process and thread, path, operation, native result, applicable byte count, timing, and any injected delay. Paths are retained in their native encoding, including non-UTF-8 Unix paths and Windows UTF-16. Receipt sequence numbers describe collection order across threads; they are not a guaranteed global execution order.

The observation boundary includes synchronous opens, closes, reads, writes, positional I/O, metadata and directory queries, pathname mutations, and executable access for the launched command and supported descendants. Memory-mapped and asynchronous I/O, attachment to an existing process, and workloads that leave the owned process tree are outside this boundary. A complete record means complete within the stated boundary. Tracing failure, lost events, or an incomplete summary cannot be used as a successful `compare` or `assetcov` input.

`assetcov` fixes its denominator before running the child. A selected existing file counts as covered only after a successful read of at least one byte, or a successful EOF read of an initially empty file. Opens and metadata checks alone do not count. Aliases of the same file count once. `--fail-under` accepts 0 through 100 percent.

`compare` reports added and removed project files, changed operation kinds, counts, failures, timing differences, and external accesses. `--fail-on-change` gates path and operation-kind changes; count and timing differences remain informational. Neither command compares file contents.

## Watches, delays, and breakpoints

`autowatch` runs once immediately, then watches inputs learned from that run, including missing paths whose creation could change the result. It debounces changes for 200 ms by default, runs serially, and updates its watch set after each run. A child failure does not end a valid watch session. An empty or unsafe watch set fails with guidance instead of watching the whole project.

`latencylab` alternates baseline and delayed runs, three pairs by default. The delay applies on the matching calling thread immediately before each selected operation. Reports separate the requested delay, observed delay, operation time, and total execution time. Results reflect the current workload, filesystem, and cache state; they are not predictions for a storage device. The command does not reset files or caches between runs.

`fbreak` requires a real terminal. It displays the matching path, operation, process ID, and thread ID, then accepts `n` to release one match and stop at the next, `c` to continue without further breaks, or `q` to terminate the owned execution. Child stdin is closed. Interactive child input and debugger-style stack inspection are not supported.

## Reproduction bundles

`min-repro` requires an explicit file selector, a new bundle directory, a nonzero expected exit code, and an expected fixed stderr substring. It snapshots eligible selected files before the original run, collects the inputs actually observed, reruns a candidate once from a separate working directory, and publishes the bundle only if the candidate matches the requested failure and does not read the original project or uncollected project inputs.

The bundle includes collected files, required internal links, a file and hash manifest, external dependency information, and rerun instructions. It does not install external runtimes or copy external system dependencies. It rejects required `.git`, `.ssh`, `.env`, `.env.*`, common private-key filenames, and `*.pem`, `*.key`, `*.p12`, or `*.pfx` paths. That fixed denylist does not prove that other selected files contain no secrets. A separate working directory is not an OS sandbox, and a verified bundle is not guaranteed to run on another machine. “Minimal” means the verified observed-input set; no repeated reduction search is performed.

## Outputs and failure handling

`record` writes NDJSON. `compare`, `assetcov`, `latencylab`, and `min-repro` offer human and `--json` reports, with `--quiet` to suppress successful reports. Report output defaults to stdout. Use `--output FILE` for atomic file output, `--force` to replace an existing regular output file, `--output -` for stdout, or `--output ./-` for a file literally named `-`. Reproduction bundles always use a new `--bundle-dir`, independent of report output.

Default limits are 1,000,000 events and 256 MiB of encoded trace per execution. Reproduction snapshots and results each default to 1 GiB and 100,000 files. Positive overrides are available in command help. `--timeout` is optional; there is no default execution deadline. Cleanup waits five seconds by default before forcing termination and confirms that owned processes exited.

Successful operations return 0. Runtime, tracing, gated comparison, coverage, or reproduction failures return 1; invalid arguments return 2; timeouts return 124; handled Ctrl+C or Windows Ctrl+Break returns 130; handled Unix SIGTERM returns 143. When tracing and supervision succeed, `record`, `assetcov`, and `fbreak` preserve a failing child's exit status or supported Unix signal identity. A tracing or cleanup failure takes priority over a child's success.

Records and bundles contain file paths and may reveal a project's structure. They never intentionally record file contents, full child arguments, or environment values, except that a reproduction bundle explicitly contains selected input files. Review them before sharing. There is no automatic history, upload, retention policy, or cleanup for published artifacts; remove them manually when no longer needed. The commands run locally without elevated privileges, runtime downloads, or silent fallback when tracing is unavailable. macOS may reject injection into protected executables.
