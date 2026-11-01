# Output and exit codes

> CacheCow 0.1.0 is unreleased. This guide describes intended workflows, not published artifacts or verified native support.

## Streams and diagnostics

CLI messages are in English. Child stdout and stderr pass through unchanged. CacheCow notices go to stderr so they do not contaminate child stdout. Shell initialization emits shell code separately from diagnostics; it is not a management JSON stream.

Use `ccow doctor` and `--verbose` to inspect paths, tool versions, CoW capability, lock state, and failure causes. `ccow shim doctor` also explains installation state, selected executables, and PATH precedence. ANSI notices are enabled by default. Set `NO_COLOR` to disable them.

Diagnostics must not persist original file contents, environment-variable values, or full command arguments. The exact command displayed for interactive source-build confirmation is not permission to save it in logs. Review diagnostic material before sharing it. CacheCow has no remote telemetry.

## Management JSON

Management commands support `--json`, for example:

```sh
ccow status --json
ccow doctor --json
ccow shim list --json
```

The envelope uses `schemaVersion: 1`, `success`, and either `result` or `error`. Errors contain `code`, `cause`, and `remediation`. Result fields depend on the command. Check the schema version and success indication before reading the result.

A text or JSON error must provide actionable remediation for noninteractive decisions, unsupported CoW, permissions, source selection, and lock contention. `run` preserves child output and is not a JSON wrapper around the build command.

## Exit codes

When the child runs, CacheCow preserves its exit code. A child can itself return any of these numbers; use diagnostics to distinguish a child failure from a CacheCow-owned failure.

| CacheCow-owned code | Meaning                                    |
| ------------------- | ------------------------------------------ |
| 2                   | Arguments or configuration are invalid     |
| 125                 | Cache, environment, or internal failure    |
| 126                 | Command execution is denied or unavailable |
| 127                 | Command was not found                      |

Successful management operations return success. Do not treat a failed preparation as a successful uncached build: preparation errors must prevent build launch. Noninteractive operations that need a source decision must fail with instructions rather than choose silently.
