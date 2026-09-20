# clibox Rust foundation

## Scope
`crates/clibox` owns the Rust executable and public crates.io package `clibox`. Issue [#916](https://github.com/delinoio/oss/issues/916) extends the original help/version foundation with six developer utilities.

## Runtime and Language
Rust 2021, MIT license, repository-pinned Rust toolchain, and `clap` parsing. The crate is an explicit Cargo workspace member and approved crates.io publication target. Native adapters are private implementation modules; there is no public Rust library API.

## Users and Operators
Developers invoking a pinned CLI from terminals and npm scripts, and maintainers distributing the same executable through Cargo and npm.

## Interfaces and Contracts
Root no-argument, `--help`/`-h`, and `--version`/`-V` behavior remains compatible. Every command has English help and examples. Malformed or missing CLI inputs return 2; runtime failures return 1. Delegated commands retain their own exit status and supported termination signals.

```text
clibox run env [KEY=VALUE ...] [--] COMMAND [ARG ...]
clibox port which PORT... [--protocol tcp|udp|all] [--json | --quiet]
clibox port kill PORT... [--protocol tcp|udp|all] [--json]
clibox open TARGET [--app APP] [--wait]
clibox clipboard copy [TEXT]
clibox clipboard paste
```

### Environment execution
Compatibility is based on cross-env v10.1.0 commit `152ae6a85b5725ac3c725a8a3e471aee79acc712`: assignment quotes/escaping, parent-environment variable references, duplicate assignments (last value wins), empty values, PATH/NODE_PATH list conversion, and platform-specific command conversion. Assignments refer to the inherited parent environment, not earlier assignments. Windows command conversion supports simple references and `${NAME:-default}`; command arguments are never path-normalized. Executable lookup uses the child PATH and Windows PATHEXT, including npm `.cmd` shims. Simple batch variable references are resolved before Rust's batch argument escaping; never concatenate an arbitrary shell expression or use unescaped raw arguments. Unsupported safe batch encoding fails instead of weakening argument boundaries.

The child inherits cwd, stdio, and environment; assignments change only its environment. `--` ends the assignment prefix. Unlike cross-env, the command is required, already-tokenized child quotes/backslashes and empty arguments are preserved, and SIGINT termination is not mapped to success. Assignment escaping does not reparse the child command or its arguments; the documented Windows variable conversion still applies. Unix termination signals are forwarded and reproduced; Windows console cancellation is forwarded to the child process group using supported CTRL_BREAK delivery. Shell expressions, cross-env-shell, dotenv, and persisted presets are excluded.

### Port ownership
Inputs are space-separated decimal ports 1–65535, deduplicated. TCP LISTEN is the default; UDP includes bound sockets and `all` combines both. IPv4 and IPv6 local addresses are included. Lookup is scoped to the caller's OS visibility and Linux network/PID namespaces.

Human output is a PID/name/protocol/address/port table without command lines. `which --quiet` emits unique PIDs, one per line, and conflicts with JSON. JSON is `{ "results": [...], "errors": [...] }`; results have `pid`, `name`, `protocol`, `address`, and `port`, with null unknown PID/name. Kill rows also have `status`: `killed`, `already-exited`, `skipped`, or `failed`. Errors have an enum-backed kebab-case `code`, safe `message`, and applicable `pid`/`port` fields. Results are ordered deterministically and duplicate endpoint rows are removed.

Partial enumeration returns available results plus errors and exit 1. Permission failures must not become verified empty results. Complete empty lookup and unoccupied-port kill succeed. Termination deduplicates PIDs, never elevates privileges or targets descendants, and considers only original owners. Immediately before a forceful OS termination, the adapter rechecks process birth identity and an originally observed relevant endpoint; changed/unverifiable identities or ownership are skipped without chasing replacements. Linux uses pidfds when available and immediate identity/socket revalidation on older kernels; macOS rechecks libproc identities; Windows terminates through a verified process handle. Already-exited processes succeed. All sent terminations share one maximum five-second verification wait; other failures do not prevent remaining targets from being processed. Port availability is not reserved after completion.

Linux uses `/proc` socket tables and descriptor ownership; macOS uses libproc; Windows uses native IP Helper owner tables and process APIs. Missing metadata remains null with diagnostics. Port ranges, service names, established TCP connections, graceful termination, recursive termination, and elevation are excluded.

### Resource opening
Exactly one local file/directory or registered URI is accepted. Existing local paths are made absolute before delegation, preventing leading dashes from becoming launcher options. The target is passed as data. Default apps use macOS `open`, Windows ShellExecuteExW, or Linux `xdg-open`. Explicit apps are names/`.app` paths on macOS and executable paths/PATH names on Windows/Linux. Browser aliases and additional app arguments are excluded.

`--wait` always requires `--app`. macOS uses OS-supported `open -W`; Windows/Linux observe the directly launched application process. Known dispatcher executables and Windows batch shims cannot provide this explicit application wait. Dispatch success without waiting confirms dispatch only. Waiting does not track document/tab closure or another process receiving a handoff. Known unsupported tracking is rejected before launch; post-dispatch tracking failure explicitly says the application may already have opened and never retries. Cancellation leaves the opened application running; only the macOS waiting helper may be stopped.

### Text clipboard
The current desktop session's ordinary text clipboard is used. Explicit TEXT (including empty text) takes precedence over stdin; otherwise input is read to EOF. Paste emits UTF-8 with no added newline. Copy produces no stdout. Whitespace and line endings are preserved.

Input must be valid NUL-free UTF-8 of at most 16 MiB (16,777,216 bytes); invalid copy data is rejected before replacement, and paste is fully validated before output. Empty clipboard succeeds with empty output, while non-text-only content fails. Native macOS NSPasteboard and Windows Unicode clipboard APIs provide text. Linux selects Wayland when WAYLAND_DISPLAY is present, otherwise X11 when DISPLAY is present, without fallback between sessions. Wayland requires wl-copy/wl-paste; X11 requires xclip. X11 ownership is queried with pure-Rust x11rb because older xclip conversion errors cannot distinguish no owner from non-text data. Missing tools, inaccessible sessions and unsupported protocol capabilities have actionable redacted failures; tools are never installed automatically.

Linux copy supplies text through stdin and returns after setup while the tool owns clipboard data in the background until replacement/session termination. It must not wait for the owner process's lifetime or keep the CLI foregrounded. Tool diagnostics are captured or suppressed, never relayed verbatim. Because wl-copy buffers stdin in a temporary file, clibox supplies a private verified tmpfs-backed directory and removes it after setup or failure; no clipboard bytes are written to persistent storage. Missing writable tmpfs is an unsupported capability reported before copy. Existing desktop clipboard managers remain outside clibox's retention control. Images, rich text, binary content, alternate selections, history, sync, and file options are excluded.

## Storage and Security
No persistent configuration, cache, operation history, application authentication, tenancy, backend service, or telemetry is added. Use current-user/session permissions only. No automatic retries or fixed execution timeout applies apart from the shared port-termination wait. Users can interrupt child execution, stdin reading, OS work, and application waits. Cancellation does not undo completed clipboard replacement, termination, or launch.

Cargo publication remains explicit and gated by the repository release coordinator. Rollback is installation of an earlier pinned version; previous OS effects are not undone. No feature flag is needed because each capability is explicitly invoked.

## Logging
Structured Rust tracing goes to stderr, defaults to warnings/errors, and honors RUST_LOG for detailed diagnostics. Stdout is exclusively results or inherited child stdout. Color is TTY-only and honors NO_COLOR. Allowed diagnostic context includes operation, backend, PID, port and stable failure code, never clipboard content, environment values, complete argv, full URLs or paths. Clap errors are redacted rather than echoing rejected values. Child output is inherited user-program output, not authored diagnostic output.

## Build and Test
- `cargo test -p clibox` covers parser contracts, compatible environment transformations, process execution/signals, test-owned TCP/UDP IPv4/IPv6 sockets, mocked partial termination and shared deadlines, mocked open/clipboard adapters, invalid text boundaries, and secret-marker diagnostic checks.
- Root `cargo test` and `cargo fmt --all --check` remain mandatory. Generate the existing DevHud frontend dependency before root Rust checks, then remove repository-owned generated dist directories after validation.
- `cargo publish -p clibox --dry-run` checks standalone packaging, including private adapter sources and tests. macOS compilation uses its standard SDK/libclang through libproc; Linux builds add no system C-library dependency beyond the target's libc.
- Linux/macOS/Windows CI runs clibox Rust process tests alongside npm distribution/consumer tests. The existing eight-target release matrix and Alpine consumers remain intact. Test termination may target only disposable test-owned processes; CLI kill tests decline when complete exclusive port ownership cannot be established.
- Automated parser/process/mocked-adapter tests are the completion gate. Real GUI launching, desktop clipboard persistence, and application termination waiting remain deferred follow-up verification and must not be represented as completed by mocks.

## Dependencies and Integrations
Uses clap, serde/serde_json, regex, tracing/tracing-subscriber, Unix signal-hook/libc, macOS libproc/Objective-C framework bindings, Windows windows-sys, and Linux x11rb with installed desktop tools. npm distribution remains owned by `packages/clibox`; the root cargo-mono tag allowlist includes clibox and releases use `clibox@v<version>`.

## Change Triggers
Update the project index, npm contract, root/domain AGENTS rules, user READMEs/help, tests and affected release/CI contracts together when commands, privacy, versions, platforms or publication change.

## References
- [Project index](project-clibox.md)
- [npm distribution](packages-clibox-distribution-contract.md)
- [Repository defaults](repository-defaults.md)
