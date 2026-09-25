# Troubleshooting clibox

## Missing native package or version mismatch


If the native package is missing, reinstall with optional dependencies enabled (`npm install --include=optional` or `pnpm install` without `--no-optional`). Do not copy `node_modules` between operating systems, architectures, or Linux libc environments; reinstall from your lockfile on the destination machine. If a lockfile omits the destination's optional package, regenerate it with your package manager and commit the corrected lockfile.

If versions disagree, reinstall the dependency so `@delino/clibox` and its selected platform package have the same exact version.

The package provides the `clibox` command only, with no public JavaScript import API.

## Arguments, timestamps, and files


- Invalid-argument errors intentionally omit your values; compare options with command help. Check explicit timestamp units, regex references, or the selected digest encoding/length.
- For DST errors, choose a valid calendar time or supply an explicit offset when parsing an ambiguous instant.
- For file failures, check access permissions, free space, links, and other applications holding the destination open. Use `--force` only when replacement is intended; retain your own backups when needed.
- Check exit status before consuming a streaming result. An interrupted or failed stdout stream can be incomplete, whereas a completed verification report can validly accompany exit code 1.

## Desktop commands

For port permission errors, inspect the returned partial results and use the appropriate user/session permissions; clibox does not elevate privileges. Clipboard access needs a reachable desktop session, its normal display authorization and the listed installed tools. A busy Windows clipboard or a Wayland compositor without the required capability produces an actionable failure. Open failures may indicate missing applications, URI associations, Linux xdg-utils or desktop access. Errors after dispatch do not guarantee that nothing opened.

## CPU counts

`system cpus` is available in the next release, not in published version 0.1.6. Check `clibox --version` if the command is unknown. If `--kind logical` fails on Linux, confirm that the environment exposes online CPU information and that the current user can read it. A failed `available` or `logical` query never falls back to a substitute count. Use the stable failure code in stderr and `RUST_LOG=clibox=debug` for redacted details; do not assume that two invocations observe the same CPU state.

## Waits and configuration

Waits are unlimited by default. Set `--timeout` when scripts must stop, and remember that a TCP connection or file's existence does not prove application or write completion. HTTP redirects are not followed. Certificate and permission errors terminate rather than retrying. See [Readiness waits](/clibox/wait).

For dotenv or YAML syntax errors, inspect the reported input/document ordinal and line/column locally. Reduce input or expanded nesting when resource limits are exceeded. Configuration processing accepts at most 64 MiB of aggregate input and 64 MiB of output; YAML allows 128 collection levels. See [Configuration commands](/clibox/configuration).

## Execution wrappers

The `run with-*` commands are available in the next release, not in published version 0.1.6. If a pinned installation reports an unknown `run` subcommand, check `clibox --version` and upgrade the exact package version before changing the script. Use `env run` rather than `run env` while remaining on 0.1.6.

For a rate limit or lock failure, confirm that all callers use the same case-sensitive name and, for a rate limit, the same limit, period, and burst. Choose `--scope project` for one project or `--scope user` for coordination across projects. `--wait-timeout 0` is an immediate decision, not unlimited waiting. A lock with `--on-locked fail` exits 75; `skip` exits 0 without starting its workload.

For `with-service`, first verify that the URL is not already ready when using `--service`; an already-ready endpoint must be observed without `--service`. The first standalone `--` separates the managed service from the workload. Check the service's stderr output for its own failures. External endpoints are never stopped, while managed services are stopped after a wrapper failure, timeout, or cancellation. HTTPS uses normal operating-system trust, so repair the certificate or system CA installation instead of attempting a custom CA or TLS bypass.

For `with-retry`, a consumed stdin stream is not replayed, and startup failures or Unix signal termination are not retried. Use `--retry-exit-code` to narrow retryable nonzero exits. For `with-timeout`, distinguish total runtime from output-idle time: either standard stream resets the idle timer. Timeout statuses are 124; set a limit to `0` only to disable that one limit while the other stays positive. See [System commands](/clibox/system#coordinate-execution) for examples and defaults.

## Diagnostics and support

Use `RUST_LOG=clibox=debug` for structured stderr progress; on PowerShell set `$env:RUST_LOG = "clibox=debug"` first. `NO_COLOR` disables terminal color. Share the version, platform, stable failure code, and redacted diagnostics through [GitHub Issues](https://github.com/delinoio/oss/issues). Avoid sharing clipboard data, environment values, secret configuration, or private input files. A delegated child program controls its own output.

## Validation limits and recovery

Automated parser, process, mocked OS-adapter and package tests cover these contracts. Real GUI behavior, desktop clipboard persistence and application-wait verification remain follow-up validation; mocked coverage does not establish those desktop observations. To roll back, install an earlier exact package version. Previous OS effects are not undone.

See [Releases and verification](/clibox/releases) for selecting an earlier package version.
