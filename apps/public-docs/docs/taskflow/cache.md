# TaskFlow Caching and Secrets

Caching is disabled by default. Enable it only when inputs, outputs, relevant
environment, and tool identities describe the command completely.

## Local reuse and restoration

```yaml
version: 1
project: example
tasks:
  build:
    command: [node, build.mjs]
    input: [src/**, build.mjs]
    output: [build-output/**]
    cache: true
    envInputs: [BUILD_MODE]
    tools:
      node: [node, --version]
```

Keys include configuration, input contents, Unix permissions and deletions, manifests and lockfiles,
declared environment, tool output, execution platform/image, and prerequisite
results. Required outputs must exist and match their snapshot. Deleted or modified
outputs are restored from a verified entry or rebuilt. An unchanged lockfile does
not establish that dependencies are installed.

When a command compiles dependent projects internally, include their source trees
in its declared inputs or depend on tasks with complete output contracts. Native
project relationships alone do not establish all of an arbitrary command's inputs.

The ignored `.taskflow` directory contains local cache objects, receipts, locks,
and masked execution logs. Add it to your ignore rules. `tflow cache list` shows
entries, `tflow cache verify` checks their integrity, and `tflow cache clean`
removes task-cache entries while preserving task outputs and native caches.

Artifacts are bounded to 512 MiB per encoded object. Paths, ownership, digests,
and relative links are validated before staged restoration. Cancelled or invalidated
executions cannot publish successful entries. Native incremental caches remain
under their tools' control; use shared resource locks where necessary.

## Environment and secrets

Dotenv is enabled by default. Precedence, from highest to lowest, is CLI `--env`,
task `env`, inherited environment, project dotenv, then root dotenv. Set
`dotenv: false` or use `--no-dotenv` to disable loading. Cache-enabled tasks receive
declared environment plus the OS/tool lookup context needed to launch programs.

Name sensitive variables in `secrets`; obtain their values from your environment
or your own uncommitted dotenv file. Secret-consuming tasks are uncached. Designated
values are masked across output chunks and newlines, including query output and
stored logs. `--show-secrets` deliberately reveals only current live task output;
stored logs remain masked. Values not designated as secrets cannot be recognized
automatically, so keep credentials out of command literals and tracked YAML.

## R2 and S3-compatible storage

Configure `remote` at the root:

```yaml
remote:
  endpoint: https://ACCOUNT.r2.cloudflarestorage.com
  bucket: taskflow-cache
  namespace: trusted-project/v1
  region: auto
  accessKeyEnv: TFLOW_R2_ACCESS
  secretKeyEnv: TFLOW_R2_SECRET
  mode: read-write
```

Supply the two referenced variables through your own credential provider. They
never enter task environments or cache manifests. Modes are `read-only` (default),
`read-write`, and `off`; temporary S3 credentials can also name `sessionTokenEnv`.
Use the provider's region for S3. The endpoint is an HTTPS origin; HTTP is allowed
only for loopback test services.

Remote transfer uses the same validated artifact format. An unavailable or corrupt
entry is diagnosed and falls back to local execution. A clean runner can restore
outputs without rebuilding. This is remote caching; commands still run locally.
Restrict cache writers to trusted identities and namespaces. TaskFlow disables
remote access for pull-request jobs, including `pull_request_target`.

See [Sharding and CI](ci) for trusted credential mapping and artifact transfer
without a remote cache.
