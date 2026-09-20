# clibox

A native Rust CLI, also available as `@delino/clibox` on npm for project-local version pinning.

Use `clibox wait tcp`, `clibox wait http`, and `clibox wait file` to wait for observable readiness in local workflows and CI scripts.

```sh
cargo install clibox
clibox --help
clibox --version
```

For JavaScript projects:

```sh
pnpm add -D -E @delino/clibox
pnpm exec clibox --version
```

The npm launcher requires Node.js 22 or newer. Prebuilt binaries cover macOS and Windows x64/arm64, and Linux x64/arm64 with glibc or musl. npm installation does not require Rust or installation scripts.

## Readiness waits

```sh
clibox wait tcp localhost:3000 --timeout 30s
clibox wait tcp '[::1]:3000' --timeout 30s
clibox wait http https://example.com/health --method head --status 204 --timeout 1m
clibox wait file "build/ready file" --timeout 2m --json
```

```text
clibox wait tcp HOST:PORT [--timeout DURATION] [--interval DURATION]
  [--attempt-timeout DURATION] [--quiet | --json]
clibox wait http URL [--method get|head] [--status CODE]
  [--timeout DURATION] [--interval DURATION] [--attempt-timeout DURATION]
  [--quiet | --json]
clibox wait file PATH [--timeout DURATION] [--interval DURATION]
  [--quiet | --json]
```

Each command accepts one target and checks immediately. Waiting is **unlimited by default**; `--timeout 0` explicitly selects unlimited waiting. Durations are nonnegative integers followed by `ms`, `s`, `m`, or `h` (for example `500ms`, `3s`, `2m`, `1h`). Bare `0` is accepted only for `--timeout`. Fractions, negative values, and overflow are rejected. Interval and attempt timeout must be positive.

After a failed check completes, the command waits `--interval` (default `250ms`) before checking again. Checks do not overlap. Network attempts have a `3s` default budget covering DNS, connection, TLS, and response headers. The overall monotonic timeout includes checks and delays; remaining overall time clips each attempt and delay. The first successful observation exits. Readiness is a point-in-time observation: it neither reserves a resource nor guarantees it will remain ready.

### TCP

Use a local or remote DNS name, IPv4 address, or bracketed IPv6 address with an explicit decimal port from 1 to 65535. Success means a TCP connection was established to at least one resolved address. Each resolved address receives a connection opportunity within the attempt budget. The connection closes without sending application data. TCP connectivity does **not** verify application readiness or any application protocol.

DNS lookup failures, connection refusal, temporary network failures, and attempt timeouts retry. Permission denial, resolver configuration failures, and other non-readiness runtime faults terminate with a safe diagnostic.

### HTTP

Use an absolute `http` or `https` URL. GET is the default; `--method head` selects HEAD. Any final `2xx` status succeeds unless `--status` selects one exact code from `200` through `599`. Status is evaluated as soon as response headers arrive. The response body is never inspected, printed, or waited for.

Redirects are not followed. A redirect succeeds only when its exact status was explicitly selected. Unexpected statuses, DNS/connection failures, temporary network failures, and attempt timeouts retry. Certificate validation, OS trust initialization, TLS protocol errors, malformed HTTP responses, permissions, and other runtime faults terminate.

HTTPS verifies certificate trust and hostname using the OS trust store. Linux systems, including Alpine, need their OS CA certificates installed (Alpine: `apk add ca-certificates`). No OpenSSL runtime library is required. Proxies, authentication, custom headers, custom CA options, client certificates, and certificate-verification bypass are unsupported. Proxy environment discovery and implicit credentials are disabled; URL user information is rejected. `SSL_CERT_FILE` and `SSL_CERT_DIR` do not override OS trust. Polling retries are controlled by clibox, without automatic HTTP-client retries.

### Files

Relative paths use the invocation's current working directory. A regular file, including an empty file, succeeds. Symbolic links are followed; a dangling link remains pending until its target exists. Missing paths retry. Directories, other special files, permission denial, symbolic-link loops, and other filesystem errors fail immediately.

Only metadata is inspected; file contents are not read and do not require read permission. File existence does **not** establish that a writer has finished. There is no size-stability, content-matching, or write-completion detection.

### Results and cancellation

Default success output is one summary, such as `tcp ready after 1250 ms (3 attempts).` Failures and handled cancellation produce actionable stderr diagnostics. `--quiet` suppresses stdout without suppressing failures. `--json` conflicts with `--quiet` and emits exactly one final JSON object for each started wait, including runtime failure, timeout, and handled cancellation:

```json
{"kind":"tcp","status":"ready","elapsed_ms":1250,"attempts":3,"error":null}
```

`kind` is `tcp`, `http`, or `file`. `status` is `ready`, `timeout`, `failed`, or `cancelled`. `elapsed_ms` is nonnegative elapsed milliseconds; `attempts` counts started checks. Failures replace `error` with an object containing a stable `code` and a safe, actionable `message`. Timeout messages include the last retryable failure classification when available. CLI validation failures produce stderr diagnostics and no JSON.

| Outcome | Exit code |
|---|---:|
| Ready | 0 |
| Runtime failure or overall timeout | 1 |
| Invalid CLI input | 2 |
| Ctrl+C | 130 |
| Unix SIGTERM | 143 |

Ctrl+C and Unix SIGTERM stop waiting and release owned resources without terminating the target service or changing the target file. Wait commands never consume stdin or launch subsequent commands. They do not support reverse waiting, multiple targets, or continuous monitoring. They create no persistent application state, configuration, cache, or operation history.

Results and diagnostics omit input hosts, URLs, paths, credentials, and response contents. Detailed troubleshooting uses redacted stderr logs:

```sh
RUST_LOG=clibox=debug clibox wait tcp localhost:3000 --timeout 10s
```

In PowerShell, set `$env:RUST_LOG = "clibox=debug"` before invoking the command. Logs may show kind, attempt, elapsed time, numeric HTTP status, and stable failure codes. Dependency logs remain disabled even with `RUST_LOG=trace`; routine pending checks are absent from default warning/error output. Set `NO_COLOR` to disable color; color is used only on a terminal. Repair the reported network, OS trust, permission, or filesystem condition, or adjust the timeout when readiness legitimately takes longer. To roll back command behavior, select an earlier pinned package version; there is no application state to migrate.

Licensed under MIT.
