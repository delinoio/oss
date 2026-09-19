# TaskFlow Commands and Sessions

Use `--root` to choose the workspace explicitly, `--json` for compact machine
output, and `--no-color` to disable color. Results go to stdout; task output and
diagnostics go to stderr. `tflow --help` and each command's `--help` list flags.

## Queries and execution

```sh
tflow check
tflow query projects
tflow query tasks
tflow query deps web#build --transitive
tflow query rdeps web --projects --transitive
tflow query path web#test repo#install
tflow query owners source.ts
tflow query inputs source.ts
tflow query artifacts
tflow plan web#build
tflow plan build --base main
tflow run build --changed source.ts --jobs 4
```

Project relationships, command prerequisites, and possible artifact relationships
are separate queries. Artifact relationships conservatively describe overlapping
declared paths; they do not add execution ordering.

Direct requests preserve their own execution cause. `--base` or repeated `--changed`
paths select affected work and filter it by the supplied task names. Git selection
includes deleted files, both sides of renames, and untracked files when comparing
the working tree. `--head` selects a committed comparison endpoint and requires
`--base` or `--affected`; it cannot be combined with `--changed`. `--affected`
without a base compares the working tree with `HEAD`.

Shared prerequisites execute once. `--jobs` limits parallel commands, while named
resources serialize shared state. A finite failure blocks dependent tasks and lets
independent work finish. `--force` bypasses task caches.

## Reporting unchanged

From a running task, execute `tflow result unchanged`. If the executable is not on
the search path, use the executable named by `TFLOW_BIN`. The report is accepted
only after successful exit in the same execution. Writing the word “unchanged” to
stdout has no special meaning.

An unchanged prerequisite suppresses only work caused by that prerequisite. A
dependent's direct request, own input change, schedule tick, other changed
prerequisite, or external effect remains effective. Missing required outputs still
require restoration or execution. Results distinguish execution from output change.

## Development sessions

```yaml
version: 1
project: web
start:
  default: [dev]
tasks:
  dev:
    command: [pnpm, exec, rsbuild, dev]
    service: true
    with: [typecheck, refresh]
  typecheck:
    command: [pnpm, exec, tsc, --noEmit]
    input: [src/**, tsconfig.json]
    watch: {initial: true, debounce: 200ms}
    overlap: queue
  refresh:
    command: [node, refresh.mjs]
    input: []
    schedule: {every: 5m, initial: false}
    overlap: skip
```

Run `tflow start` or `tflow start PROFILE`. Watch subscriptions are installed before
initial commands run. `with` activates companions without ordering them; use
`dependsOn` for prerequisite completion. Shared companions and initial prerequisite
work are deduplicated. Companions stop after their last live owner leaves.

An HMR server stays running when a companion reruns. To use a service as a
prerequisite, declare `waitFor: ready` and give it a `readiness` check with `type`
`tcp`, `http`, or `command`, its `address`, `url`, or `command`, and `timeout`.
Finite prerequisites use `waitFor: success` (the default).

| Overlap | While the command is running |
| --- | --- |
| `queue` | Combine new causes into one pending run. Default for file watches. |
| `skip` | Discard new triggers. Default for schedules. |
| `restart` | Cancel and reap the old process tree before replacement. |

Intervals use monotonic time. A schedule can instead use `cron: "*/5 * * * *"`
with an IANA `timezone`, defaulting to `UTC`. Cron has five fields. Missed ticks are
not replayed in a burst; a repeated local minute at a DST transition runs once.
Scheduled tasks are uncached.

Weekdays use 0 or 7 for Sunday and 1–6 for Monday–Saturday; named weekdays also
work. When both day-of-month and weekday are restricted, either match triggers the
schedule.

Check failures retain subscriptions. A server exit or readiness failure ends the
session and cleans up its processes. Invalid configuration pauses new execution
while watching for a correction. Ctrl+C stops subscriptions, timers, pending runs,
child processes, and owned containers. Reading configuration or using `run` does
not activate background watches or schedules.

See [Configuration](configuration) and [Caching and secrets](cache).
