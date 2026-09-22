# System commands

```text
clibox env run [KEY=VALUE ...] [--] COMMAND [ARG ...]
clibox port list PORT... [--protocol tcp|udp|all] [--json | --quiet | --pids]
clibox port kill PORT... [--protocol tcp|udp|all] [--json | --quiet]
clibox open TARGET [--app APP] [--wait]
clibox clipboard copy [TEXT]
clibox clipboard paste
```

Use `--help` after any command for English help and examples. Root help (`clibox`, `--help`, or `-h`) also identifies the built version, Delino maintainer, repository, MIT license, and GitHub Issues support path; subcommand help stays focused on that command. Running `clibox` without arguments or using explicit `--help` prints help to stdout and returns exit code **0**. Running `clibox env`, `clibox port`, `clibox clipboard`, `clibox wait`, `clibox text`, `clibox time`, `clibox base64`, `clibox hash`, `clibox dotenv`, or `clibox yaml` without a subcommand prints that command's help to stderr and returns exit code **2**. Other invalid or missing arguments return exit code **2** with an error diagnostic; runtime failures return **1**. `env run` forwards the child program's exit status and supported termination signals.

## Run with environment variables

```sh
clibox env run NODE_ENV=production node build.js
clibox env run FIRST=one SECOND=two -- node script.js
```

Use this command in npm scripts to set a child environment across operating systems. It inherits the working directory, standard input/output/error, and parent environment. Assignments never change the calling shell. Duplicate assignments use the last value; empty values and empty child arguments are preserved. A child command is required.

Assignment escaping, variable references, PATH/NODE_PATH lists and platform-specific command conversion follow cross-env v10.1.0. Assignment references use the parent environment, not earlier assignments. Your invoking shell or JSON package script may apply its own quoting before clibox receives arguments. clibox preserves the literal quotes and backslashes in the resulting child command and arguments instead of unquoting them again; Windows variable conversion still applies. `--` explicitly ends the assignments. Windows supports npm `.cmd` commands resolved through the child PATH/PATHEXT; unsafe or unrepresentable batch arguments fail instead of becoming shell expressions. Invoke the underlying executable directly if its batch wrapper cannot represent an argument safely.

Environment execution has no shell-expression mode, dotenv loading or stored command preset. The child is awaited, and signal termination (including SIGINT) is not reported as success. Windows console interruption uses supported process-group CTRL_BREAK delivery.

On Windows, `env run` also treats `$1` as an environment-variable reference and removes it when unset. Invoke `clibox text replace` directly when passing regex capture references; wrapping it in `env run` applies that extra conversion even after shell quoting.

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
