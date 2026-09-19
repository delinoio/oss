# TaskFlow Configuration

`taskflow.yml` uses `version: 1` and an explicit, unique `project` ID. Unknown
fields, duplicate YAML keys, invalid references, and prerequisite cycles are
errors. Generate the editor schema with `tflow schema`.

## Native discovery

At the workspace root, reference native manifests instead of copying member lists:

```yaml
version: 1
project: repo
workspace:
  manifests: [pnpm-workspace.yaml, Cargo.toml, go.work]
  cargoFeatures: []
  cargoNoDefaultFeatures: false
tasks:
  install:
    command: [pnpm, install, --frozen-lockfile]
    install: true
```

Omitting `workspace.manifests` discovers supported root manifests. With no native
workspace, TaskFlow uses a single project. One directory discovered by multiple
adapters remains one project. Unconfigured projects have query IDs beginning with
`path:` and no generated commands.

pnpm uses native membership and lockfile resolution; Cargo uses versioned metadata;
Go uses modules, workspaces, and replacements. Alias names, dependency kinds, and
resolved local identities remain visible in queries. Cargo feature options and
optional `cargoTarget` select the metadata configuration. Conditional Cargo
dependencies select prerequisites only when active for the task platform. An
explicit `cargoTarget` takes precedence; otherwise TaskFlow uses the matching
Rust compiler host target or GNU Linux, Apple Darwin, and Windows MSVC defaults
for another selected platform. Incomplete metadata is
reported and expands affected selection conservatively. A prerequisite selector
with unresolved metadata cannot execute until an explicit `install: true`
prerequisite prepares dependencies and TaskFlow refreshes metadata. Merely querying
the graph never installs dependencies.

## Commands and dependencies

```yaml
version: 1
project: web
tasks:
  build:
    command: [pnpm, exec, rsbuild, build]
    dependsOn:
      - repo#install
      - task: build
        from: [dependencies, devDependencies]
    input:
      - auto: true
      - "!build-output/**"
    output: [build-output/**]
```

Arrays pass argv directly. Strings use the platform command shell; `shell` can
provide an explicit executable and argument prefix. Commands inherit the project
working directory. `task` refers to a local task; `project#task` is explicit.

Native selectors use only direct dependencies. Supported kinds are `dependencies`,
`devDependencies`, `buildDependencies`, `peerDependencies`, and
`optionalDependencies`, where provided by the adapter. Each selected task adds its
own prerequisites. A selected project without that task is skipped with an
explanation; a task-less intermediary is never traversed automatically. Explicit
missing references are errors.

## Inputs and outputs

Paths and ordered positive/negative globs are relative to the configuration
directory. Inputs can refer to other projects inside the workspace. `auto: true`
excludes generated directories such as `node_modules`, `target`, and `dist`.
Explicit patterns can include needed generated inputs. Native manifests and
lockfiles participate in invalidation automatically.

Outputs must remain inside the owning project and cannot overlap another task's
outputs. Cache and CI snapshots own complete directories, exact files, or
`directory/**` trees; avoid partial wildcard output ownership. Declare shared native
incremental caches through common `resources` locks instead of treating them as
portable output snapshots. `output: []` explicitly declares a check with no outputs.
Omitted outputs make CI keep dependent tasks in the same job.

## Execution controls

| Field | Meaning |
| --- | --- |
| `cache` | Opt-in task result reuse; default `false`. |
| `env`, `envInputs`, `secrets`, `tools` | Environment, masking, and cache-key contracts. |
| `resources` | Names of exclusive locks shared across tasks and invocations. |
| `timeout` | Maximum command duration such as `15m`. |
| `platform.os` | `macos`, `linux`, or `windows`. |
| `platform.arch` | `x64` or `arm64`. |
| `platform.executor` | `host` (default) or `docker`. |
| `effect` | `local` (default) or `external`; external effects cannot be cached. |
| `install` | Allows explicit dependency preparation before native graph refresh. |

Global `--os` and `--arch` fill platform components omitted by tasks. Explicit
task requirements win. Host commands reject a mismatched platform. Docker requires
`platform.os: linux` and an image pinned as `name@sha256:...`; optional `ports`
contains Docker publish mappings. The workspace is mounted in the container, and
only declared task environment values are forwarded. Docker and cached tool probes
use the same image and architecture. Remote Docker daemons are not supported.

Continue with [Commands and sessions](commands), [Caching and secrets](cache), or
[Sharding and CI](ci).
