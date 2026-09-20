# clibox Rust foundation

## Scope
`crates/clibox` owns the Rust executable and public crates.io package `clibox`. Issue [#919](https://github.com/delinoio/oss/issues/919) adds stateless TCP, HTTP, and file readiness waits. The complementary interfaces in #916 and #917 remain reserved for independent implementation; this change does not implement or redefine them.

## Runtime and Language
Rust 2021, MIT license, repository-pinned Rust toolchain, and `clap` argument parsing. It is an explicit Cargo workspace member and approved crates.io publication target. Runtime operation uses Tokio, Hickory DNS, reqwest, Rustls with statically linked ring cryptography, and native OS certificate roots. No installed networking or shell polling utilities are invoked.

## Users and Operators
Developers invoking a pinned CLI in portable local workflows and CI, and maintainers building and releasing the same executable through Cargo and npm. Operate with current-user permissions only.

## Interfaces and Contracts
- `clibox`, `clibox --help`, and `clibox -h` print help to stdout and exit successfully; `--version`/`-V` print `clibox <Cargo package version>`.
- `clibox wait tcp HOST:PORT [--timeout DURATION] [--interval DURATION] [--attempt-timeout DURATION] [--quiet | --json]`.
- `clibox wait http URL [--method get|head] [--status CODE] [--timeout DURATION] [--interval DURATION] [--attempt-timeout DURATION] [--quiet | --json]`.
- `clibox wait file PATH [--timeout DURATION] [--interval DURATION] [--quiet | --json]`.
- Accept exactly one target. Missing, malformed, extra, unknown, or conflicting arguments exit 2 with static actionable English stderr diagnostics and no JSON. Never render clap's raw parser errors, which can contain sensitive argv. Help includes examples and command limitations. There is no public Rust library API.
- Commands do not consume stdin, launch subsequent commands, reverse-wait, continuously monitor, or accept mixed/multiple targets.

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

TLS uses OS roots and verifies hostname/trust. No proxy discovery, authentication, custom headers, custom CA options, client certificates, certificate-verification bypass, cookies, or implicit credentials. Explicitly disable proxy environment discovery and redirects even under Cargo feature unification. Clear only `SSL_CERT_FILE`/`SSL_CERT_DIR` in the single-threaded executable startup before native roots are loaded, because the current loader would otherwise replace OS trust with those overrides; no caller environment or OS trust store is mutated. TLS tests inject isolated roots internally and never alter user trust. Rustls uses an explicitly selected ring provider and no client identity or TLS key-log configuration.

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
No persistent configuration, application cache, history, authentication, tenancy, backend, privileges, or migrations. The commands do not modify target files. Rollback is an earlier pinned package version; readiness observations have no persisted effect to reverse.

## Security
No raw argv/locator/credential/body logging, including parser failures or dependency errors. Cargo publication remains explicit and gated by the repository release coordinator. There is no feature flag, remote telemetry, metrics service, dashboard, compliance gate, separate SLO, performance target, or mandatory external-service/manual validation.

## Logging
Structured `tracing` diagnostics go to stderr, defaulting to warnings/errors. Routine pending checks are debug events; `RUST_LOG=clibox=debug` enables kind, attempt, elapsed time, numeric HTTP status, and stable failure classification. Filter out **all dependency targets**, even with `RUST_LOG=trace` or explicit dependency filters, and never echo malformed filter values. `NO_COLOR` disables color; otherwise color is allowed only on a TTY. Static failure diagnostics remain visible even if log filtering disables tracing.

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

## Dependencies and Integrations
npm distribution is owned by `packages/clibox`. The root cargo-mono tag allowlist includes `clibox`; releases use `clibox@v<version>`. No new distribution channel, Homebrew, public GitHub Release binaries, docs website, or public library API is introduced.

## Change Triggers
Update the project index, both domain contracts, relevant root/domain AGENTS rules, Cargo/npm READMEs, CLI help, tests, and applicable release workflows together when command behavior, privacy, naming, versions, platforms, dependencies, or publication changes.

## References
- [Project index](project-clibox.md)
- [Repository defaults](repository-defaults.md)
