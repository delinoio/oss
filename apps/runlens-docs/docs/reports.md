# Reports and execution receipts

## Explicit storage

```sh
runlens run --save build.json -- your-build-tool build
runlens receipt build.json --json
runlens export build.json --format json --output shared.json
runlens export build.json --format html --output shared.html
```

`--save` and `export --output` are explicit destinations. Parent directories must already exist. Writes are atomic and refuse existing files, including symlinks. Save failures are tool failures. Saved files belong to you and have no automatic expiration, deletion, migration, or last-run pointer.

HTML is self-contained and works offline. Its semantic headings, table captions, and text classifications do not rely on color. It has no external resources, scripts, source attachments, log attachments, or ZIP bundle.

## Schema version 1

One JSON report format covers `run`, `clean`, and `repeat`. It includes:

- `schema_version`, report `kind`, and UUID-v7 execution identifiers.
- Execution roles distinguish `target`, `preparation`, and historical `baseline`
  evidence. Baseline errors affect comparison certainty, not the current command's
  operational exit status.
- Sanitized command identity and argv, working directory, environment names, available source metadata, OS/architecture, Runlens version, tracing-engine revision, and a passively computed executable SHA-256 when its identity is verified against the launched image.
- Declared snapshot scope, exclusions, input/output patterns, and coverage status.
- Separate `accesses`, `before`, `after`, and `changes` maps.
- Separate child exit/signal information, collection status, typed errors, and elapsed time.
- Findings with classifications and evidence references, verification outcome when applicable, and limitations.

Linux `os_version` includes the distribution and version as `ID:VERSION_ID`, such
as `ubuntu:22.04`. Missing or legacy version-only metadata makes comparisons
inconclusive; equal version numbers alone do not establish the same environment.

A path/access-mode pair records an **attempt**. It does not establish a successful read, syscall result, exact process identity, timeline, or causal dependency. A snapshot records filesystem state; comparing known states can establish a change. Missing, unreadable, unstable, excluded, or out-of-scope evidence is not an empty file.

Snapshots include Git-ignored paths and exclude Git internals, execution-owned temporary material, and explicit exclusions. Outside this scope, accesses do not imply complete before/after coverage.

Readers reject unsupported schemas, malformed or oversized data, duplicate metadata keys, invalid identifiers, and broken evidence references. Reports are data and never cause command execution or archive extraction. CLI/report compatibility is preserved within the same product major version; schema versions are independent. Upgrades and rollback never rewrite saved reports.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Requested operation completed successfully; read-only comparison completion does not assert equivalence |
| 1 | Child command failed |
| 2 | Invalid arguments, configuration, or report |
| 3 | Unsupported host, executable, or tracing initialization |
| 4 | Incomplete collection or inconclusive check |
| 5 | Policy or verification failed |
| 6 | Timeout |
| 7 | Cancellation |
| 8 | Save/export/output failure |
| 9 | Cleanup failure |
| 10 | Internal tool failure |

Structured results retain all relevant error categories and child status. If execution has multiple failures, the largest listed code takes precedence. Failed or incomplete verification is never code 0.


Download the [report schema v1](https://runlens.delino.io/schema/report-v1.json) and
[configuration schema v1](https://runlens.delino.io/schema/config-v1.json). The configuration schema
describes the data model represented by `runlens.toml`. JSON Schema checks shape;
Runlens additionally checks bounded records, UUID-v7 spelling, digest formats,
evidence references, filesystem-state consistency, and verification outcomes.

The non-evidence string envelope is limited to 1 MiB. Arrays and individual
records are bounded while parsing; an analysis accepting multiple reports allows
at most 64 inputs totaling 1 GiB. Oversized metadata is rejected before analysis.
Conflict analysis additionally limits the combined number of target-execution
pairs across different reports to 65,536. Preparation executions do not count.
Exceeding that limit returns invalid input (exit 2) before analysis produces any
results. Select fewer reports or reports containing fewer target executions.

Child termination records an exit code or a signal, never both. Reports containing both are rejected as invalid input. A signal termination cannot pass verification even if collection completed successfully.
