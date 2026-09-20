# clibox

A native Rust CLI, also available as `@delino/clibox` on npm for project-local version pinning.

Cross-platform utilities for child environments, local port owners, resource opening, the desktop text clipboard, and TCP/HTTP/file readiness waits.

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

## Commands

```text
clibox run env [KEY=VALUE ...] [--] COMMAND [ARG ...]
clibox port which PORT... [--protocol tcp|udp|all] [--json | --quiet]
clibox port kill PORT... [--protocol tcp|udp|all] [--json]
clibox open TARGET [--app APP] [--wait]
clibox clipboard copy [TEXT]
clibox clipboard paste
```

Use `--help` after any command for English help and examples. Running `clibox` without arguments displays help. Invalid or missing arguments return exit code **2**; runtime failures return **1**. `run env` forwards the child program's exit status and supported termination signals.

### Run with environment variables

```sh
clibox run env NODE_ENV=production node build.js
clibox run env FIRST=one SECOND=two -- node script.js
```

Use this command in npm scripts to set a child environment across operating systems. It inherits the working directory, standard input/output/error, and parent environment. Assignments never change the calling shell. Duplicate assignments use the last value; empty values and empty child arguments are preserved. A child command is required.

Assignment escaping, variable references, PATH/NODE_PATH lists and platform-specific command conversion follow cross-env v10.1.0. Assignment references use the parent environment, not earlier assignments. Your invoking shell or JSON package script may apply its own quoting before clibox receives arguments. clibox preserves the literal quotes and backslashes in the resulting child command and arguments instead of unquoting them again; Windows variable conversion still applies. `--` explicitly ends the assignments. Windows supports npm `.cmd` commands resolved through the child PATH/PATHEXT; unsafe or unrepresentable batch arguments fail instead of becoming shell expressions. Invoke the underlying executable directly if its batch wrapper cannot represent an argument safely.

There is no shell-expression mode, dotenv loading or stored command preset. The child is awaited, and signal termination (including SIGINT) is not reported as success. Windows console interruption uses supported process-group CTRL_BREAK delivery.

### Inspect and terminate port owners

```sh
clibox port which 3000 8080
clibox port which 5353 --protocol udp --json
clibox port which 3000 --quiet
clibox port kill 3000 8080 --json
```

Supply decimal ports from 1 through 65535, separated by spaces. Duplicate ports are removed. The default inspects TCP LISTEN sockets; `udp` selects UDP bindings and `all` combines both. IPv4 and IPv6 local endpoints are included. Port ranges, service names and established TCP connections are not selected.

The default table includes PID, process name, protocol, address and port, without process command lines. `which --quiet` prints unique PIDs, one per line, and cannot be combined with `--json`.

JSON uses this envelope (unknown PID/name values are `null`):

```json
{"results":[{"pid":1234,"name":"node","protocol":"tcp","address":"127.0.0.1","port":3000}],"errors":[]}
```

Kill rows also contain `status`: `killed`, `already-exited`, `skipped` or `failed`. Errors contain a stable `code`, safe `message`, and applicable `pid`/`port` context. Partial lookup returns available results alongside errors with exit code 1; it never reports inaccessible ownership as a verified empty result. A complete empty lookup or killing an unoccupied port succeeds.

`port kill` forcefully terminates every verified owner PID without confirmation or automatic privilege elevation. It deduplicates owners and does not terminate descendants automatically. Before each termination, it rechecks process identity and the original relevant socket ownership. Changed or unverifiable identities are skipped; replacement owners are not pursued. Already-exited owners succeed. All requested terminations share at most five seconds of verification, and permission failures/timeouts do not prevent processing the remaining targets. Partial failure returns 1. Completion does not reserve a port against subsequent reuse.

### Open a resource

```sh
clibox open .
clibox open https://example.com
clibox open report.txt --app TextEdit --wait
```

Open exactly one file, directory or URI with an OS-registered scheme. The default application is used unless `--app` is supplied. macOS accepts an application name or `.app` path; Windows/Linux accept an executable path or PATH name. Targets are passed as data. Browser aliases and extra application-argument options are not supported.

Without `--wait`, success confirms launch dispatch, not successful rendering. `--wait` requires `--app` on every platform. macOS waits for the application's termination; Windows/Linux wait for the directly identifiable application process. This does not detect document/tab closure or track another process receiving a handoff. Known dispatchers such as xdg-open, gio, Explorer and rundll32, and Windows batch shims, cannot be used for an application wait. If tracking fails after dispatch, clibox explicitly reports that the application may already have opened and does not retry. Cancelling the wait leaves the application running.

### Copy and paste text

```sh
clibox clipboard copy "hello"
clibox clipboard copy ""
cat notes.txt | clibox clipboard copy
clibox clipboard paste > notes.txt
```

An explicit text argument takes precedence over stdin. Without an argument, copy reads stdin until EOF. Copy writes nothing to stdout. Paste writes UTF-8 without adding a newline. Empty text, whitespace and original line endings are preserved. An empty clipboard succeeds with empty output; a clipboard containing only non-text data fails.

The limit is **16 MiB (16,777,216 UTF-8 bytes)**. Invalid UTF-8, embedded NUL and oversized text are rejected without truncation. Copy validates before replacing the clipboard; paste validates before emitting output. Only the current desktop session's ordinary text clipboard is supported, without images, rich text, alternate selections, history or synchronization.

Linux requires installed `wl-copy`/`wl-paste` from **wl-clipboard** on Wayland, or **xclip** on X11. Wayland takes precedence when both display environments are present; a failing Wayland session does not silently fall back to X11. Linux resource opening also requires **xdg-open** from xdg-utils. Tools are not installed automatically. Wayland copy requires writable tmpfs-backed shared memory or a tmpfs-backed XDG runtime directory, so tool buffering remains in memory.

Linux copy returns after successful setup while an OS tool retains clipboard ownership in the background until replacement or session termination; the calling CLI need not stay in the foreground. Clibox-managed clipboard processing uses memory only. Existing desktop clipboard managers may retain content independently.

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

HTTPS verifies certificate trust and hostname using the OS trust store. HTTPS negotiates HTTP/2 or HTTP/1.1 with the server; plain HTTP uses HTTP/1.1. Linux systems, including Alpine, need their OS CA certificates installed (Alpine: `apk add ca-certificates`). No OpenSSL runtime library is required. Proxies, authentication, custom headers, custom CA options, client certificates, and certificate-verification bypass are unsupported. Proxy environment discovery and implicit credentials are disabled; URL user information is rejected. `SSL_CERT_FILE` and `SSL_CERT_DIR` do not override OS trust. Polling retries are controlled by clibox, without automatic HTTP-client retries.

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

## Diagnostics and operation limits

All operations use the current OS user's permissions and desktop session. No authentication service, saved configuration, cache, operation history or telemetry is added. The environment, port, open, and clipboard commands have no fixed execution timeout or automatic retry apart from the shared five-second port-termination verification. Readiness waits use the polling and deadline rules above. Interrupting a command does not undo completed copies, terminations or application launches.

Warnings/errors use structured stderr diagnostics. Set `RUST_LOG=debug` for more detail. Stdout remains dedicated to results or the delegated child's output. Color is used only on a TTY and respects `NO_COLOR`. Clibox-authored diagnostics omit clipboard text, environment values, complete argv, full URLs and paths; a child program still controls its own inherited output.

For port permission errors, inspect the returned partial results and use the appropriate user/session permissions; clibox does not elevate privileges. Clipboard access needs a reachable desktop session, its normal display authorization and the listed installed tools. A busy Windows clipboard or a Wayland compositor without the required capability produces an actionable failure. Open failures may indicate missing applications, URI associations, Linux xdg-utils or desktop access. Errors after dispatch do not guarantee that nothing opened.

Automated parser, process, mocked OS-adapter and package tests cover these contracts. Real GUI behavior, desktop clipboard persistence and application-wait verification remain follow-up validation; mocked coverage does not establish those desktop observations. To roll back, install an earlier exact package version. Previous OS effects are not undone.

Licensed under MIT.
