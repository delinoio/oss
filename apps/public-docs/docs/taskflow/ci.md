# TaskFlow Sharding and CI

## Test adapters

Set `shard.adapter` and `shard.count` on a test task. Native adapters require argv
commands and collect actual test inventories before assignment.

| Adapter | Command example | Unit |
| --- | --- | --- |
| `go` | `[go, test, ./...]` | Top-level test with its subtests. |
| `libtest` | `[cargo, test]` | libtest item; selected doctests form one additional unit. |
| `vitest` | `[pnpm, exec, vitest, run]` | Test file. |
| `jest` | `[pnpm, exec, jest, --runInBand]` | Test file. |
| `generic` | Custom list and run commands. | Stable IDs from your inventory. |

Assignment is deterministic, using measured durations for local complete-suite
runs when available and stable IDs otherwise. `tflow run test --shard 0/4` selects
one zero-based shard; its count must match configuration. An empty shard produces
a valid empty report. Missing, duplicate, failed, and cancelled results cannot
produce whole-suite success. Sharded tasks cannot own shared output directories.

Custom Rust harnesses and unsupported native selection/output flags require
`generic`. A generic `list` command writes this JSON to stdout:

```json
{"version":1,"tests":[{"id":"case-a","durationMs":20},{"id":"case-b"}]}
```

The generic `run` command reads the file named by `TFLOW_SHARD_INPUT`, containing
`{"version":1,"tests":["case-a"]}`, then writes the file named by
`TFLOW_SHARD_RESULT`:

```json
{"version":1,"results":[{"id":"case-a","status":"passed","durationMs":18}]}
```

Statuses are `passed`, `skipped`, `failed`, and `cancelled`. Every selected ID must
appear exactly once. A failed command cannot override its exit status with a
passing report.

## GitHub Actions export

Configure explicit task OS/architecture requirements and root `ci` settings:

```yaml
ci:
  revision: REPLACE_WITH_ACCESSIBLE_TASKFLOW_SOURCE_COMMIT
  rust: nightly-2026-01-01
  node: 24.14.0
  pnpm: 10.26.2
  go: 1.25.5
  runners:
    linux-x64: ubuntu-22.04
    linux-arm64: ubuntu-24.04-arm
    macos-x64: macos-15-intel
    macos-arm64: macos-15
    windows-x64: windows-2022
    windows-arm64: windows-11-arm
```

Replace the revision placeholder with a complete lowercase 40-character commit
SHA in the Delino OSS repository that contains TaskFlow. Check that your account
can use the chosen runner labels. Versions must be exact; Rust also accepts a
dated toolchain. Provide Node/pnpm or Go versions when those adapters are present.

```sh
tflow ci export web#build web#test --output .github/workflows/taskflow.yml
```

Commit both generated files: the workflow and its adjacent `.taskflow.json`
blueprint. Export fails if graph resolution, tool versions, or runner mappings are
missing. Changed native manifests or task configuration require regeneration.
Validate the generated workflow with `actionlint` before committing it.

The workflow checks out and builds TaskFlow at the selected revision on clean
runners. It computes a plan, runs dependency/platform units and test shards, then
aggregates complete receipts. Commands needing the same undeclared state share a
job. Declared outputs and result bundles cross job boundaries through Actions
artifacts, so a remote cache is optional. Each unit uses TaskFlow's execution
causes; GitHub job omission does not decide unchanged propagation. Watches and
timers remain inactive.

## Trust and external effects

Task `secrets` and remote credential references map to identically named GitHub
secrets only on trusted non-PR events. Use ordinary environment identifiers; the
control names `TFLOW_BLUEPRINT`, `TFLOW_UNIT`, and `TFLOW_UNTRUSTED_CI` are reserved. Pull-request jobs
receive empty secret mappings and cannot access remote caches. Keep privileged
operations in a separately reviewed workflow if additional permissions or approval
gates are needed; generated workflows grant only read access to repository content.

Mark deployments or other external mutations with `effect: external`, and declare
their configuration/environment inputs. Once selected, these tasks remain eligible
even if a prerequisite reports unchanged. Exporting a workflow neither publishes
software nor operates a remote execution service.

See [Configuration](configuration), [Commands and sessions](commands), and
[Caching and secrets](cache).
