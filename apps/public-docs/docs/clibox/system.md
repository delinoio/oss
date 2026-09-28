# System commands

Published version **0.2.0** names environment execution `run env` and includes `system cpus`, as shown below. Version 0.1.6 uses `env run` instead and does not include CPU queries. See [Migration](/clibox/migration).

```text
clibox run env [KEY=VALUE ...] [--] COMMAND [ARG ...]
clibox port list PORT... [--protocol tcp|udp|all] [--json | --quiet | --pids]
clibox port kill PORT... [--protocol tcp|udp|all] [--json | --quiet]
clibox open TARGET [--app APP] [--wait]
clibox clipboard copy [TEXT]
clibox clipboard paste
clibox system cpus [--kind available|logical] [--json | --quiet]
```

Use `--help` after any command for English help and examples. Root help (`clibox`, `--help`, or `-h`) also identifies the built version, Delino maintainer, repository, Apache-2.0 license, and GitHub Issues support path; subcommand help stays focused on that command. Running `clibox` without arguments or using explicit `--help` prints help to stdout and returns exit code **0**. Running `clibox run`, `clibox port`, `clibox clipboard`, `clibox system`, `clibox wait`, `clibox text`, `clibox time`, `clibox base64`, `clibox hash`, `clibox dotenv`, or `clibox yaml` without a subcommand prints that command's help to stderr and returns exit code **2**. Other invalid or missing arguments return exit code **2** with an error diagnostic; runtime failures return **1**. `run env` forwards the child program's exit status and supported termination signals.

## Query CPU counts

```sh
clibox system cpus
clibox system cpus --kind logical
clibox system cpus --json
clibox system cpus --kind logical --quiet
```

The default `available` result is [Rust's estimate of suitable parallelism](https://doc.rust-lang.org/std/thread/fn.available_parallelism.html), not an idle CPU count, physical core count, or guaranteed capacity. Affinity, cgroup limits, VMs, and Windows processor groups can make that estimate differ from actual capacity. `logical` counts online logical CPUs visible to this OS or VM without clibox affinity or quota reductions. `OMP_*` variables do not override either count. Each invocation observes only its selected kind, and separate invocations can return different values as CPU availability changes.

Plain output is a positive decimal integer followed by one LF, with no label or formatting. `--json` emits a single compact object and LF, for example `{"kind":"available","count":8}`. `--quiet` still performs the query and preserves the exit status while suppressing stdout; it conflicts with `--json`. The command reads no stdin and runs no external utility. It has no file output or persistent state.

Invalid arguments exit 2. Query or output failures exit 1 with redacted stderr guidance. Failed queries never emit a substitute count or JSON success result; a failed stdout write can leave partial output. Handled Ctrl+C/Windows Ctrl+Break exits 130, and Unix SIGTERM exits 143. On Linux, if online CPU information is unavailable, `logical` fails; check whether the current environment exposes it. Use `RUST_LOG=clibox=debug` for safe operation and failure classifications.

## Run with environment variables

```sh
clibox run env NODE_ENV=production node build.js
clibox run env FIRST=one SECOND=two -- node script.js
```

Use this command in npm scripts to set a child environment across operating systems. It inherits the working directory, standard input/output/error, and parent environment. Assignments never change the calling shell. Duplicate assignments use the last value; empty values and empty child arguments are preserved. A child command is required.

Assignment escaping, variable references, PATH/NODE_PATH lists and platform-specific command conversion follow cross-env v10.1.0. Assignment references use the parent environment, not earlier assignments. Your invoking shell or JSON package script may apply its own quoting before clibox receives arguments. clibox preserves the literal quotes and backslashes in the resulting child command and arguments instead of unquoting them again; Windows variable conversion still applies. `--` explicitly ends the assignments. Windows supports npm `.cmd` commands resolved through the child PATH/PATHEXT; unsafe or unrepresentable batch arguments fail instead of becoming shell expressions. Invoke the underlying executable directly if its batch wrapper cannot represent an argument safely.

Environment execution has no shell-expression mode, dotenv loading or stored command preset. The child is awaited, and signal termination (including SIGINT) is not reported as success. Windows console interruption uses supported process-group CTRL_BREAK delivery.

On Windows, `run env` also treats `$1` as an environment-variable reference and removes it when unset. Invoke `clibox text replace` directly when passing regex capture references; wrapping it in `run env` applies that extra conversion even after shell quoting.

## Coordinate execution

The following wrappers are available in published version 0.2.0, alongside `run env`; they are not present in version 0.1.6. Each wrapper accepts one workload using the same environment-assignment grammar as `run env`:

```text
[KEY=VALUE ...] [--] COMMAND [ARG ...]
```

The workload is never interpreted as a shell expression. It inherits the working directory and standard streams, resolves commands through its child environment, and preserves literal arguments. Use `--` when the command might otherwise look like an environment assignment.

```sh
clibox run with-rate-limit --name ci-publish --limit 1 --period 1m -- pnpm publish
clibox run with-lock --name database-migrate --on-locked fail -- pnpm migrate
clibox run with-service http://127.0.0.1:3000/health -- pnpm test
clibox run with-service http://127.0.0.1:3000/health --service node server.js -- pnpm test
clibox run with-retry --max-attempts 5 --jitter none -- pnpm install
clibox run with-timeout --timeout 10m --idle-timeout 30s -- pnpm test
```

All wrappers support `--kill-after DURATION`, which defaults to `5s`. On timeout, cancellation, or a managed-service failure, clibox asks owned work to stop, waits for that grace period, then forces termination if necessary. A cancellation observed before workload startup prevents that workload from starting; a later cancellation skips remaining grace. Durations use nonnegative integer `ms`, `s`, `m`, or `h` values. On Unix, keep workloads and managed services in the foreground: programs that daemonize or create a new session or process group leave wrapper ownership and must manage their own lifecycle. Child exit statuses remain meaningful; see [Output and cancellation](/clibox/output#execution-wrapper-statuses).

### Rate limits and locks

`with-rate-limit` requires `--name`, `--limit`, and `--period`. It admits one workload through a named local token bucket. `--burst` defaults to `1` and accepts values through `9007199254740992`. A token is consumed immediately before the workload starts and is not refunded when startup or cancellation fails. Calls using the same name must retain the same limit, period, and burst configuration.

`with-lock` requires `--name` and acquires a named local exclusive lock before starting its workload. `--on-locked wait` is the default. `--on-locked skip` returns 0 without running the workload; `--on-locked fail` returns 75. `--wait-timeout` is accepted only with `wait`. For either command, `--wait-timeout 0` makes an immediate admission decision instead of waiting.

Both commands default to `--scope project`, which coordinates calls from the same canonical project directory. Use `--scope user` to coordinate across projects, or `--project-dir DIRECTORY` to select another existing directory as project identity. Names are case-sensitive ASCII letters, digits, dots, underscores, or hyphens, from 1 through 128 characters. Rate limits and locks coordinate only the current user on the current machine; they do not create a shared service or cross-machine queue.

### HTTP service readiness

`with-service URL` waits for HTTP headers before starting the workload. It sends GET and accepts any 2xx status by default; use `--method head` or `--status CODE` for a specific probe. Polling defaults to every `250ms`, each attempt to `3s`, and the overall `--ready-timeout` to `30s`. After every unsuccessful probe, including the initial preflight before external observation or managed-service startup, clibox waits one polling interval before checking again. `--ready-timeout 0` disables only the overall readiness deadline; intervals and individual attempts remain positive.

Without `--service`, the URL is external: clibox observes it but never terminates it. With `--service KEY=VALUE ... SERVER ARG ... -- WORKLOAD ...`, clibox first confirms that the URL is not already ready, then starts and owns that service. The first standalone `--` after `--service` separates the service command from the workload; a standalone separator inside service arguments is unsupported. Managed-service output goes to stderr. If the service exits before the workload finishes, its completion races the workload completion, or readiness fails, clibox stops its owned work.

Service probes use only absolute HTTP(S) URLs without user information. Redirects, proxies, credentials, custom headers, response bodies, custom certificate authorities, and TLS-verification bypass are not supported. HTTPS verifies the operating system's trust store and hostname.

### Retries and timeouts

`with-retry` defaults to three total attempts, a `1s` initial delay, a backoff factor of `2`, a `30s` maximum delay, and full jitter. Use `--retry-exit-code CODE` one or more times to retry only those nonzero numeric exit statuses. It never replays stdin and does not retry startup failures or Unix signal termination. `--timeout 0` disables the wrapper's overall retry deadline.

`with-timeout` requires a positive `--timeout` or `--idle-timeout`. The first bounds total runtime; the second resets whenever the workload writes bytes to stdout or stderr. Output is forwarded promptly, and output that arrives after an elapsed idle deadline does not extend the run. In a foreground Unix terminal, a workload with inherited input or output remains the foreground terminal process group even when another standard stream is redirected, so interactive prompts, reads, and writes work normally. Ctrl+C still cancels the wrapper and its owned workload, including a workload that ignores the direct interrupt. Ctrl+Z suspends the wrapper job as well as the workload; use your shell's normal `fg` command to resume it. Set either limit to `0` only to disable that individual limit while the other remains positive.

## Inspect and terminate port owners

```sh
clibox port list 3000 8080
clibox port list 5353 --protocol udp --json
clibox port list 3000 --pids
clibox port kill 3000 8080 --json
```

Supply decimal ports from 1 through 65535, separated by spaces. Duplicate ports are removed. The default inspects TCP LISTEN sockets; `udp` selects UDP bindings and `all` combines both. IPv4 and IPv6 local endpoints are included. Port ranges, service names and established TCP connections are not selected.

The default table includes PID, process name, protocol, address and port, without process command lines. `list --pids` prints sorted unique PIDs, one per line, and cannot be combined with `--json` or `--quiet`. Both `list --quiet` and `kill --quiet` suppress stdout results while retaining failure diagnostics and exit status. Quiet and JSON modes conflict.

JSON uses this envelope (unknown PID/name values are `null`):

```json
{"results":[{"pid":1234,"name":"node","protocol":"tcp","address":"127.0.0.1","port":3000}],"errors":[]}
```

Kill rows also contain `status`: `killed`, `already-exited`, `skipped` or `failed`. Errors contain a stable `code`, safe `message`, and applicable `pid`/`port` context. Partial lookup returns available results alongside errors with exit code 1; it never reports inaccessible ownership as a verified empty result. A complete empty lookup or killing an unoccupied port succeeds.

`port kill` forcefully terminates every verified owner PID without confirmation or automatic privilege elevation. It deduplicates owners and does not terminate descendants automatically. Before each termination, it rechecks process identity and the original relevant socket ownership. Changed or unverifiable identities are skipped; replacement owners are not pursued. Already-exited owners succeed. All requested terminations share at most five seconds of verification, and permission failures/timeouts do not prevent processing the remaining targets. Partial failure returns 1. Completion does not reserve a port against subsequent reuse.

## Open a resource

```sh
clibox open .
clibox open https://example.com
clibox open report.txt --app TextEdit --wait
```

Open exactly one file, directory or URI with an OS-registered scheme. The default application is used unless `--app` is supplied. macOS accepts an application name or `.app` path; Windows/Linux accept an executable path or PATH name. Targets are passed as data. Browser aliases and extra application-argument options are not supported.

Without `--wait`, success confirms launch dispatch, not successful rendering. `--wait` requires `--app` on every platform. macOS waits for the application's termination; Windows/Linux wait for the directly identifiable application process. This does not detect document/tab closure or track another process receiving a handoff. Known dispatchers such as xdg-open, gio, Explorer and rundll32, and Windows batch shims, cannot be used for an application wait. If tracking fails after dispatch, clibox explicitly reports that the application may already have opened and does not retry. Cancelling the wait leaves the application running.

## Copy and paste text

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

See [Output and cancellation](/clibox/output) and [Troubleshooting](/clibox/troubleshooting) for shared behavior and desktop validation limits.
