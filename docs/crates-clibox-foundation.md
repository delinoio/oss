# clibox Rust foundation

## Scope
`crates/clibox` owns the Rust executable and public crates.io package `clibox`. Issue [#919](https://github.com/delinoio/oss/issues/919) adds stateless TCP, HTTP, and file readiness waits. Issue [#916](https://github.com/delinoio/oss/issues/916) also implements six developer utilities. The complementary #917 interfaces remain reserved for independent implementation.

## Runtime and Language
Rust 2021, MIT license, repository-pinned Rust toolchain, and `clap` argument parsing. It is an explicit Cargo workspace member and approved crates.io publication target. Runtime operation uses Tokio, Hickory DNS, reqwest, Rustls with statically linked ring cryptography, and native OS certificate roots. Readiness waits invoke no installed networking or shell polling utilities. Environment/open/clipboard commands retain their explicitly delegated child and installed desktop-tool behavior. Native adapters are private implementation modules.

## Users and Operators
Developers invoking a pinned CLI in terminals, npm scripts, portable local workflows, and CI, and maintainers building and releasing the same executable through Cargo and npm. Operate with current-user permissions only.

## Interfaces and Contracts
- `clibox`, `clibox --help`, and `clibox -h` print help to stdout and exit successfully; `--version`/`-V` print `clibox <Cargo package version>`.
- `clibox wait tcp HOST:PORT [--timeout DURATION] [--interval DURATION] [--attempt-timeout DURATION] [--quiet | --json]`.
- `clibox wait http URL [--method get|head] [--status CODE] [--timeout DURATION] [--interval DURATION] [--attempt-timeout DURATION] [--quiet | --json]`.
- `clibox wait file PATH [--timeout DURATION] [--interval DURATION] [--quiet | --json]`.
- Accept exactly one target. Missing, malformed, extra, unknown, or conflicting arguments exit 2 with static actionable English stderr diagnostics and no JSON. Never render clap's raw parser errors, which can contain sensitive argv. Help includes examples and command limitations. There is no public Rust library API.
- Wait commands do not consume stdin, launch subsequent commands, reverse-wait, continuously monitor, or accept mixed/multiple targets.

### Utility commands
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

### Polling and deadlines
The first check starts immediately. Overall waiting is unlimited by default; `--timeout 0` explicitly means unlimited. Durations accept nonnegative integers suffixed `ms`, `s`, `m`, or `h`; bare `0` is accepted only for timeout. Interval and attempt timeout must be positive. Fractions, negative values, millisecond arithmetic overflow, and unrepresentable monotonic deadlines fail validation.

The interval defaults to 250ms **after** an unsuccessful attempt completes. Polling attempts never overlap. Network attempts default to 3s. One monotonic overall budget includes DNS configuration/resolution, connection, trust initialization, TLS, response headers, and polling delays. Attempts and delays are clipped to its remaining duration. A successful observation after the overall deadline is not readiness within that deadline. The implementation has an injected Tokio clock for deterministic scheduler tests.

Cancellation drops the active asynchronous operation and its connections, stops polling, and releases owned resources without terminating the service or mutating the file. Blocking read-only OS metadata/trust/configuration calls run off the runtime thread; runtime shutdown must not wait for an uninterruptible OS call after a handled deadline or cancellation. A successful result is a point-in-time observation and does not reserve the resource or guarantee continued availability.

### TCP
Accept DNS names, strict IPv4, and bracketed IPv6 with an explicit decimal port 1–65535 (`localhost:3000`, `127.0.0.1:3000`, `[::1]:3000`). Resolve using system DNS configuration and hosts data, with no persistent cache or fallback public resolver. All returned addresses receive a connection opportunity within a shared attempt deadline; one stalled address/family cannot consume another address's opportunity. Stop when one connection succeeds and close it without sending application data. This establishes connectivity only, not application-protocol readiness.

Retry DNS lookup failures, refusal, temporary network failures, and attempt timeouts. Local permission denial, resolver initialization failure, local resource errors, and other non-readiness failures terminate. Connection attempts within one polling attempt may run concurrently; polling attempts never do.

### HTTP and TLS
Accept absolute `http` and `https` URLs. Inspect raw authority before URL normalization, rejecting all user information including empty userinfo, malformed authority, and control/whitespace ambiguity. Default GET succeeds on any final 2xx; HEAD is selectable and `--status` selects exactly one final code in 200–599. Observe status at response headers and drop the response without reading, inspecting, printing, or waiting for the body. Do not follow redirects: a redirect succeeds only when its exact status is selected.

Retry unexpected statuses, DNS/connection failures, temporary network failures (including truncated/closed connections), and attempt timeouts. Certificate trust/hostname failures, OS trust initialization failures, TLS protocol faults, invalid HTTP responses, permissions, and other runtime faults are terminal. DNS, connections, TLS negotiation, and response headers share one attempt budget. reqwest automatic retries and connection pooling are disabled; clibox owns polling retries.

TLS uses OS roots and verifies hostname/trust. No proxy discovery, authentication, custom headers, custom CA options, client certificates, certificate-verification bypass, cookies, or implicit credentials. Explicitly disable proxy environment discovery and redirects even under Cargo feature unification. Clear only `SSL_CERT_FILE`/`SSL_CERT_DIR` in the single-threaded executable startup before native roots are loaded, because the current loader would otherwise replace OS trust with those overrides. This clearing is scoped to wait execution only; `run env` must preserve both variables in the child environment unless explicitly assigned; no caller environment or OS trust store is mutated. TLS tests inject isolated roots internally and never alter user trust. Rustls uses an explicitly selected ring provider and no client identity or TLS key-log configuration.

### Files
Relative paths resolve from the invocation cwd. Metadata success requires a regular file, including empty files. Follow symbolic links; dangling links and missing paths remain pending. Directories, special files, inaccessible ancestors, permission errors, link loops, and all other filesystem errors terminate. Metadata inspection does not read file contents or require content read permission. File existence does not prove writer completion; size stability, content matching, and write-completion detection are excluded. Paths may contain spaces, Unicode, or platform-native non-UTF-8 bytes.

### Output and exit codes
Default success is one human-readable summary containing wait kind, elapsed milliseconds, and attempt count. Failures and cancellation receive actionable stderr diagnostics. `--quiet` suppresses stdout only and conflicts with `--json`. For each started wait operation, JSON produces exactly one final object, including runtime initialization failure, timeout, and handled cancellation:

```json
{"kind":"tcp","status":"ready","elapsed_ms":1250,"attempts":3,"error":null}
```

`kind` is `tcp|http|file`; `status` is `ready|timeout|failed|cancelled`; elapsed milliseconds are nonnegative and attempts count started checks. `error` is null on success or `{ "code": "stable_classification", "message": "safe actionable explanation" }`. Timeout messages include the last retryable classification when available. Cancellation before the first check can report zero attempts.

Exit codes are ready 0, runtime failure/overall timeout 1, invalid CLI input 2, Ctrl+C 130, and Unix SIGTERM 143. Results and diagnostics omit hosts, URLs, paths, credentials, response contents, and raw argv. Output write failures produce a safe stderr diagnostic and code 1; an unavailable stdout cannot receive a complete result.

Stable error classifications are enum-backed: `dns_lookup`, `dns_configuration`, `connection_refused`, `network_unavailable`, `attempt_timeout`, `unexpected_status`, `tls_certificate`, `tls_protocol`, `trust_store`, `permission_denied`, `network_io`, `http_protocol`, `file_missing`, `not_regular_file`, `filesystem`, `runtime_initialization`, `signal_handler`, `interrupted`, `terminated`, and `overall_timeout`. Command kinds, methods, and outcomes are also enums. Dependency errors are classified by typed sources, never by formatting or logging their text.

## Storage
Wait operations add no persistent configuration, application cache, history, authentication, tenancy, backend, privileges, or migrations. Wait commands do not modify target files. Rollback is an earlier pinned package version; readiness observations have no persisted effect to reverse.

No persistent configuration, cache, operation history, application authentication, tenancy, backend service, or telemetry is added. Use current-user/session permissions only. For environment, port, open, and clipboard commands, no automatic retries or fixed execution timeout applies apart from the shared port-termination wait; readiness waits use the polling/deadline contract above. Users can interrupt child execution, stdin reading, OS work, and application waits. Cancellation does not undo completed clipboard replacement, termination, or launch.

Cargo publication remains explicit and gated by the repository release coordinator. Rollback is installation of an earlier pinned version; previous OS effects are not undone. No feature flag is needed because each capability is explicitly invoked.

## Security
No raw argv/locator/credential/body logging, including parser failures or dependency errors. Cargo publication remains explicit and gated by the repository release coordinator. There is no feature flag, remote telemetry, metrics service, dashboard, compliance gate, separate SLO, performance target, or mandatory external-service/manual validation.

## Logging
Structured `tracing` diagnostics go to stderr, defaulting to warnings/errors. Routine pending checks are debug events; `RUST_LOG=clibox=debug` enables kind, attempt, elapsed time, numeric HTTP status, and stable failure classification. Filter out **all dependency targets**, even with `RUST_LOG=trace` or explicit dependency filters, and never echo malformed filter values. `NO_COLOR` disables color; otherwise color is allowed only on a TTY. Static failure diagnostics remain visible even if log filtering disables tracing.

Structured Rust tracing goes to stderr, defaults to warnings/errors, and honors RUST_LOG for detailed diagnostics. Stdout is exclusively results or inherited child stdout. Color is TTY-only and honors NO_COLOR. For environment/port/open/clipboard utilities, allowed diagnostic context includes operation, backend, PID, port and stable failure code, never clipboard content, environment values, complete argv, full URLs or paths. Clap errors are redacted rather than echoing rejected values. Child output is inherited user-program output, not authored diagnostic output.

Wait signal handlers are installed only by the Tokio wait runtime. Utility commands retain their native signal forwarding/reproduction and shared runtime cancellation. Never register both handler families for one invocation.

## Build and Test
- `cargo run -p clibox -- wait --help` and `cargo test --locked -p clibox`.
- Root `cargo test` and `cargo fmt --all --check` remain required repository checks.
- `cargo publish -p clibox --dry-run` verifies standalone crate packaging.
- `pnpm --filter @delino/clibox test` and `pnpm --filter @delino/clibox test:package` validate the launcher and installed consumers.
- Scheduler tests use paused time: immediate first checks, unlimited waiting, after-completion delays, clipped budgets, no overlapping polls, retry recovery, and cancellation/resource drop during checks and delays.
- Loopback TCP/HTTP and isolated TLS fixtures cover DNS/IPv4/IPv6, multiple addresses/refusal/cleanup, GET/HEAD, statuses/transitions, redirect policy, delayed headers, nonterminating bodies, trusted/untrusted certificates, hostname mismatch, and failed trust loading.
- File/process fixtures cover empty/delayed/relative/Unicode files, symlinks/dangling links/loops/special files, metadata without content access, typed permissions/error races, parser validation, output/exit behavior, handled Unix signals and isolated-console Windows Ctrl+C, proxy isolation, and secret markers under detailed or invalid log filters.
- `node-clibox-test` runs Cargo unit/process tests on Linux, macOS, and Windows. Release builds run those tests for all eight targets before packaging and preserve Alpine consumer validation. Cross-platform evidence is produced by those jobs; local validation alone does not claim all platforms were executed.
- ring requires a C compiler at build time: MSVC on Windows, Xcode clang on macOS, and native C compilers on GNU Linux. Both musl jobs install `musl-tools` and set the target-specific `CC` to `musl-gcc` for ring, while the final linker remains pinned `rust-lld` with self-contained Rust runtime objects. No runtime OpenSSL/shared crypto dependency is added. Linux HTTPS consumers need their OS CA certificates, including Alpine `ca-certificates`.
- Preserve Cargo/npm exact version synchronization, all eight native targets, script-free installation, and launcher argv/signal forwarding. Remove generated repository-owned `dist` after validation. No actual package publication is part of implementation.

- `cargo test -p clibox` covers parser contracts, compatible environment transformations, process execution/signals, test-owned TCP/UDP IPv4/IPv6 sockets, mocked partial termination and shared deadlines, mocked open/clipboard adapters, invalid text boundaries, and secret-marker diagnostic checks.
- Root `cargo test` and `cargo fmt --all --check` remain mandatory. Generate the existing DevHud frontend dependency before root Rust checks, then remove repository-owned generated dist directories after validation.
- `cargo publish -p clibox --dry-run` checks standalone packaging, including private adapter sources and tests. macOS compilation uses its standard SDK/libclang through libproc; Linux adapters add no system C-library dependency beyond the target's libc; readiness TLS additionally compiles ring as documented below.
- Linux/macOS/Windows CI runs clibox Rust process tests alongside npm distribution/consumer tests. The existing eight-target release matrix and Alpine consumers remain intact. Test termination may target only disposable test-owned processes; CLI kill tests decline when complete exclusive port ownership cannot be established.
- Automated parser/process/mocked-adapter tests are the completion gate. Real GUI launching, desktop clipboard persistence, and application termination waiting remain deferred follow-up verification and must not be represented as completed by mocks.

## Dependencies and Integrations
Utility adapters use regex, Unix signal-hook/libc, macOS libproc/Objective-C framework bindings, Windows windows-sys, and Linux x11rb with installed desktop tools. npm distribution is owned by `packages/clibox`. The root cargo-mono tag allowlist includes `clibox`; releases use `clibox@v<version>`. No new distribution channel, Homebrew, public GitHub Release binaries, docs website, or public library API is introduced.

## Change Triggers
Update the project index, both domain contracts, relevant root/domain AGENTS rules, Cargo/npm READMEs, CLI help, tests, and applicable release workflows together when command behavior, privacy, naming, versions, platforms, dependencies, or publication changes.

## References
- [Project index](project-clibox.md)
- [npm distribution](packages-clibox-distribution-contract.md)
- [Repository defaults](repository-defaults.md)
