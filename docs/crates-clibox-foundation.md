# clibox Rust foundation

## Scope
`crates/clibox` owns the Rust executable. Its private companion crates own configuration processing (`crates/clibox-config`), OS utilities (`crates/clibox-system`), offline transformations (`crates/clibox-transform`), readiness waits (`crates/clibox-wait`), and file-access workflows (`crates/clibox-fspy`). All six are explicit workspace members with `publish = false`; distribution uses npm, GitHub Release archives, and APT/DNF rather than crates.io. Issue [#916](https://github.com/delinoio/oss/issues/916) extends the original help/version foundation with six OS utilities. Issue [#917](https://github.com/delinoio/oss/issues/917) adds seven text/time/Base64/hash utilities alongside them. Issue [#919](https://github.com/delinoio/oss/issues/919) adds stateless TCP, HTTP, and file readiness waits.

Issue [#920](https://github.com/delinoio/oss/issues/920) adds three local dotenv/YAML configuration commands. Issue [#951](https://github.com/delinoio/oss/issues/951) adds portable CPU counts.

### Crate boundaries
- `clibox` owns the process entrypoint, logging/panic initialization, static redacted parser diagnostics, and root command composition. It depends directly on the five companion crates through path dependencies.
- `clibox-config` owns dotenv/YAML command definitions, bounded parsing and reference resolution, configuration cancellation and diagnostics, and private permission-preserving atomic publication.
- `clibox-system` owns its clap command tree, environment/open/clipboard/port/CPU implementations, private OS adapters, contextual failures, and signal/child-status handling.
- `clibox-transform` owns its clap command tree, text/time/Base64/hash implementations, streaming I/O, cancellation supervisor, safe errors, and permission-preserving atomic publication.
- `clibox-wait` owns its clap command tree and target validation, polling and deadlines, DNS/TCP/HTTP/TLS/file probes, cancellation, and final reports.
- `clibox-fspy` owns the file-access command tree, versioned trace, Linux collector, and analysis; its full seven-workflow contract is [documented separately](crates-clibox-fspy-contract.md).
- Companion crates do not depend on each other or on the executable. Exports are limited to command composition and execution plus redacted runtime-failure reporters; the process panic hook uses the configuration reporter. These are internal interfaces, not supported public Rust library APIs.
- Each crate declares only its own runtime/platform dependencies. Unit tests follow their implementation; executable process tests remain in `crates/clibox/tests`, with their own dev-dependencies. Companion parser fixtures use test-local wrappers instead of depending on the root CLI.
- Only the executable Cargo manifest/lock entry, npm source manifest, and generated artifacts participate in product version synchronization. Companion versions begin at `0.1.0`, are not automatically bumped, and are not registry dependency constraints.

## Runtime and Language
Rust 2021 in the original five crates and Rust 2024 in `clibox-fspy`, MIT license, repository-pinned Rust toolchain, and `clap` argument parsing. All six crates are non-publishable Cargo workspace members; the executable is built with Cargo for npm and native distribution. Runtime operation uses Tokio, Hickory DNS, reqwest, Rustls with statically linked ring cryptography, and native OS certificate roots. Readiness waits invoke no installed networking or shell polling utilities. Environment/open/clipboard commands retain their explicitly delegated child and installed desktop-tool behavior. Native adapters are private implementation modules.

## Users and Operators
Developers invoking a pinned CLI in terminals, npm scripts, portable local workflows, and CI, and maintainers building the executable with Cargo and releasing it through npm and native packages. Operate with current-user permissions only.

## Interfaces and Contracts
### Implemented CLI consistency revision
- Canonical names are `run env`, `port list`, and `hash compute`. The former `env run`, `port which`, and `hash encode` names are rejected with exit 2 and static migration guidance; they are not aliases.
- Report commands (`port list`, `port kill`, `wait`, `hash verify`) use `--quiet` to suppress stdout results while retaining failures on stderr and their exit status. It conflicts with `--json`. Only `port list --pids` prints sorted unique PIDs; it conflicts with quiet and JSON modes.
- Commands with file output treat omitted output and `--output -` as stdout. Use `--output ./-` for a literal dash filename. Checksum paths are rebased only for an actual manifest file destination. `--force` requires a real file output or `--in-place`; redundant `--in-place --force` is accepted. Invalid combinations fail before input reads or side effects. `hash verify --quiet` still conflicts with any explicit output.
- Short help includes core rules and examples; long help contains those rules plus the complete detailed constraints. Parser failures use only allowlisted command paths, never raw argv or clap input-error rendering. Runtime failures remain actionable when `RUST_LOG=off`.
- Owned operations return numeric 130 for Ctrl+C/Windows Ctrl+Break and 143 for Unix SIGTERM after cleanup. `run env` preserves the delegated child's exit status and Unix signal identity. Interrupted publication does not overwrite the original, and completed effects are not undone.
- Published version 0.1.6 already includes `port list`, `hash compute`, and the output/cancellation semantics above, but uses `env run`. The subsequent `run env` rename is implemented and targets the next minor release; it is not part of the published 0.1.6 artifacts. Version changes and publication remain owned by the existing manual Release Project workflow; documentation changes do not bump or publish versions.

- `clibox`, `clibox --help`, and `clibox -h` print help to stdout and exit successfully. Root help includes its Cargo-derived version, Delino maintainer, repository, MIT license, and the repository's GitHub Issues support URL; subcommand help does not repeat this footer. `--version`/`-V` remain exactly `clibox <Cargo package version>`.
- `clibox run`, `clibox port`, `clibox clipboard`, `clibox system`, `clibox wait`, `clibox text`, `clibox time`, `clibox base64`, `clibox hash`, `clibox dotenv`, and `clibox yaml` without a subcommand print the corresponding command's help on stderr, leave stdout empty, and exit 2. Explicit `--help`/`-h` for those commands prints help on stdout and exits 0. Preserve clap's generated `DisplayHelpOnMissingArgumentOrSubcommand` output without replacing it with a generic diagnostic.
- `clibox wait tcp HOST:PORT [--timeout DURATION] [--interval DURATION] [--attempt-timeout DURATION] [--quiet | --json]`.
- `clibox wait http URL [--method get|head] [--status CODE] [--timeout DURATION] [--interval DURATION] [--attempt-timeout DURATION] [--quiet | --json]`.
- `clibox wait file PATH [--timeout DURATION] [--interval DURATION] [--quiet | --json]`.
- Wait commands accept exactly one target. Missing targets and malformed, extra, unknown, or conflicting arguments exit 2 with static actionable English stderr diagnostics and no JSON. Only generated help/version output may bypass these diagnostics; never render clap's raw input-error diagnostics, which can contain sensitive argv. Help includes examples and command limitations. There is no public Rust library API.
- Wait commands do not consume stdin, launch subsequent commands, reverse-wait, continuously monitor, or accept mixed/multiple targets.

### Utility commands
The seven transformation interfaces and their file-publication rules follow below.
Root no-argument, `--help`/`-h`, and `--version`/`-V` behavior remains compatible. Every command has English help and examples. Malformed or missing CLI inputs return 2; runtime failures return 1. Delegated commands retain their own exit status and supported termination signals.

```text
clibox run env [KEY=VALUE ...] [--] COMMAND [ARG ...]
clibox port list PORT... [--protocol tcp|udp|all] [--json | --quiet | --pids]
clibox port kill PORT... [--protocol tcp|udp|all] [--json | --quiet]
clibox open TARGET [--app APP] [--wait]
clibox clipboard copy [TEXT]
clibox clipboard paste
clibox system cpus [--kind available|logical] [--json | --quiet]
clibox dotenv list [--input FILE] [--output FILE] [--force]
clibox dotenv merge FILE... [--output FILE] [--force]
clibox yaml normalize [--input FILE] [--output FILE | --in-place] [--force]
```

### CPU counts (#951)

`clibox-system` owns enum-backed kinds and private OS adapters. `available` delegates directly to `std::thread::available_parallelism()` without adjustment or fallback. It is an estimate of suitable parallelism rather than idle CPUs, physical cores, or guaranteed capacity; document standard-library affinity, cgroup, VM, and Windows processor-group limits. `logical` returns online logical CPUs visible to the current OS/VM, without clibox affinity or quota reductions. Linux glibc and musl read `/sys/devices/system/cpu/online` and count ordered nonoverlapping CPU-list entries/ranges with checked arithmetic and no per-CPU allocation. macOS queries `hw.logicalcpu` with `sysctlbyname`; Windows passes `ALL_PROCESSOR_GROUPS` to `GetActiveProcessorCount`. Missing, malformed, empty, zero, or overflowing results fail without substituting `1` or another mode. Neither mode interprets `OMP_*`.

The default output is a positive decimal integer and one LF. JSON is exactly one compact `{"kind":"available|logical","count":N}` object and one LF; quiet queries without stdout. The two output flags conflict. Invalid kinds/options, positional arguments, and flag conflicts fail before querying. No stdin, external utility, network, file output, history, configuration, monitoring, elevation, retries, or fixed timeout is added. Each invocation queries only its selected mode and observations may change between invocations. Runtime query or output failure exits 1, invalid CLI exits 2, handled Ctrl+C/Windows Ctrl+Break exits 130, and handled Unix SIGTERM exits 143. Query failure produces no stdout; output failure may leave partial stdout. Failed diagnostics do not replace operation status. Shared `clibox-system` cancellation checks the query wait and the publication boundary; no other family installs signal handlers. Redacted debug events include operation, kind, backend, and completion/failure classification only, while existing `Failure` codes distinguish permission, unavailable backend, invalid enumeration, and output I/O failures.

### Environment execution
Compatibility is based on cross-env v10.1.0 commit `152ae6a85b5725ac3c725a8a3e471aee79acc712`: assignment quotes/escaping, parent-environment variable references, duplicate assignments (last value wins), empty values, PATH/NODE_PATH list conversion, and platform-specific command conversion. Assignments refer to the inherited parent environment, not earlier assignments. Windows command conversion supports simple references and `${NAME:-default}`; command arguments are never path-normalized. Executable lookup uses the child PATH and Windows PATHEXT, including npm `.cmd` shims. Simple batch variable references are resolved before Rust's batch argument escaping; never concatenate an arbitrary shell expression or use unescaped raw arguments. Unsupported safe batch encoding fails instead of weakening argument boundaries.

The child inherits cwd, stdio, and environment; assignments change only its environment. `--` ends the assignment prefix. Unlike cross-env, the command is required, already-tokenized child quotes/backslashes and empty arguments are preserved, and SIGINT termination is not mapped to success. Assignment escaping does not reparse the child command or its arguments; the documented Windows variable conversion still applies. Unix termination signals are forwarded and reproduced; Windows console cancellation is forwarded to the child process group using supported CTRL_BREAK delivery. Shell expressions, cross-env-shell, dotenv loading in environment execution, and persisted presets are excluded.

Windows command conversion includes numeric references such as `$1`; an unset variable becomes empty before the child runs. Integration fixtures must preserve this cross-env behavior when delegating transformation commands. Regex capture arguments should use direct `clibox text replace` invocation, whose parser and npm launcher preserve the received dollar references.

### Port ownership
Inputs are space-separated decimal ports 1–65535, deduplicated. TCP LISTEN is the default; UDP includes bound sockets and `all` combines both. IPv4 and IPv6 local addresses are included. Lookup is scoped to the caller's OS visibility and Linux network/PID namespaces.

Human output is a PID/name/protocol/address/port table without command lines. `list --pids` emits sorted unique PIDs, one per line, and conflicts with JSON and quiet mode. Both `list --quiet` and `kill --quiet` suppress stdout while preserving failure diagnostics and exit status; quiet conflicts with JSON. JSON is `{ "results": [...], "errors": [...] }`; results have `pid`, `name`, `protocol`, `address`, and `port`, with null unknown PID/name. Kill rows also have `status`: `killed`, `already-exited`, `skipped`, or `failed`. Errors have an enum-backed kebab-case `code`, safe `message`, and applicable `pid`/`port` fields. Results are ordered deterministically and duplicate endpoint rows are removed.

Partial enumeration returns available results plus errors and exit 1. Permission failures must not become verified empty results. Complete empty lookup and unoccupied-port kill succeed. Termination deduplicates PIDs, never elevates privileges or targets descendants, and considers only original owners. Immediately before a forceful OS termination, the adapter rechecks process birth identity and an originally observed relevant endpoint; changed/unverifiable identities or ownership are skipped without chasing replacements. Linux uses pidfds when available and immediate identity/socket revalidation on older kernels; macOS rechecks libproc identities; Windows terminates through a verified process handle. Already-exited processes succeed. All sent terminations share one maximum five-second verification wait; other failures do not prevent remaining targets from being processed. Port availability is not reserved after completion.

Linux uses `/proc` socket tables and descriptor ownership; macOS uses libproc; Windows uses native IP Helper owner tables and process APIs. Missing metadata remains null with diagnostics. Port ranges, service names, established TCP connections, graceful termination, recursive termination, and elevation are excluded.

### Resource opening
Exactly one local file/directory or registered URI is accepted. Existing local paths are made absolute before delegation, preventing leading dashes from becoming launcher options. The target is passed as data. Default apps use macOS `open`, Windows ShellExecuteExW, or Linux `xdg-open`. Explicit apps are names/`.app` paths on macOS and executable paths/PATH names on Windows/Linux. Browser aliases and additional app arguments are excluded.

`--wait` always requires `--app`. macOS uses OS-supported `open -W`; Windows/Linux observe the directly launched application process. Known dispatcher executables and Windows batch shims cannot provide this explicit application wait. Dispatch success without waiting confirms dispatch only. Waiting does not track document/tab closure or another process receiving a handoff. Known unsupported tracking is rejected before launch; post-dispatch tracking failure explicitly says the application may already have opened and never retries. Cancellation leaves the opened application running; only the macOS waiting helper may be stopped.

### Text clipboard
The current desktop session's ordinary text clipboard is used. Explicit TEXT (including empty text) takes precedence over stdin; otherwise input is read to EOF. Paste emits UTF-8 with no added newline. Copy produces no stdout. Whitespace and line endings are preserved.

Input must be valid NUL-free UTF-8 of at most 16 MiB (16,777,216 bytes); invalid copy data is rejected before replacement, and paste is fully validated before output. Empty clipboard succeeds with empty output, while non-text-only content fails. Native macOS NSPasteboard and Windows Unicode clipboard APIs provide text. Linux selects Wayland when WAYLAND_DISPLAY is present, otherwise X11 when DISPLAY is present, without fallback between sessions. Wayland requires wl-copy/wl-paste; X11 requires xclip. X11 ownership is queried with pure-Rust x11rb because older xclip conversion errors cannot distinguish no owner from non-text data. Missing tools, inaccessible sessions and unsupported protocol capabilities have actionable redacted failures; tools are never installed automatically.

Linux copy supplies text through stdin and returns after setup while the tool owns clipboard data in the background until replacement/session termination. It must not wait for the owner process's lifetime or keep the CLI foregrounded. Tool diagnostics are captured or suppressed, never relayed verbatim. Because wl-copy buffers stdin in a temporary file, clibox supplies a private verified tmpfs-backed directory and removes it after setup or failure; no clipboard bytes are written to persistent storage. Missing writable tmpfs is an unsupported capability reported before copy. Existing desktop clipboard managers remain outside clibox's retention control. Images, rich text, binary content, alternate selections, history, sync, and file options are excluded.

### Configuration commands (#920)
- `dotenv list [--input FILE] [--output FILE] [--force]` reads only the current directory's `.env` by default; `--input -` selects stdin. It validates all records and lists unique ASCII identifier keys in case-sensitive lexical order, without values.
- `dotenv merge FILE... [--output FILE] [--force]` requires one or more ordered inputs and permits stdin (`-`) once. Last assignment within a file and later files win, including empty values. The Node.js dotenv baseline supports optional `export`, comments, whitespace, and multiline single/double quotes. An assignment key may itself be `export`, including whitespace before `=` (`export =enabled`); recognize the optional prefix only when an immediate ASCII space and another assignment key follow. Tabs may occur after that space or as ordinary assignment whitespace, but `export` immediately followed by a tab and another key is invalid. Invalid records are errors even when overwritten. Winning value tokens retain quotes, escapes, literal variable/shell references, and internal line endings; generated assignments use `KEY=token` and LF.
- `yaml normalize [--input FILE] [--output FILE | --in-place] [--force]` defaults to stdin. In-place requires an explicit regular input file and authorizes its replacement. Other existing destinations require force; force without file output is a CLI error. Relative paths use the invocation directory; explicit file input never reads stdin.
- YAML uses 1.2 Core scalar types without machine-number conversion, string keys, document-local anchors/aliases, shallow merge keys (explicit entries win; earlier merge-sequence entries win), and multiple documents. Quoted/string-tagged `<<` is ordinary data. Reject duplicate ordinary keys, invalid merges, unresolved/cyclic references, non-string keys, unsupported tags, repeated version directives within one document, and version directives other than 1.2. Sort mappings recursively by Unicode scalar value; preserve scalar types/precision, sequence order, and document order. Emit deterministic two-space block formatting with no anchors/comments, no initial marker for one document, and a marker before each of multiple documents. Output is byte-idempotent. Scalar strings containing escaped `U+FFFE` or `U+FFFF` retain escapes in keys and values because those code points are prohibited literally in YAML source.
- Raw input aggregated across files and serialized output each have independent 64 MiB ceilings. UTF-8 is required, one initial BOM per input is removed, and raw NUL is rejected. Collection nesting is at most 128 (root collection is one). Check expansion size/depth before allocating expanded results; validate all input before publishing any result. Measure escaped scalar sizes without allocating encoded strings, and apply the serialized-byte ceiling only to the resolved document output: explicit keys and earlier merge operands can shadow otherwise oversized encodings. Shared measurement/emission escaping keeps exact size checks consistent.
- Empty/comment-only streams yield zero bytes; explicit empty YAML documents yield `null`. Nonempty output ends in LF. Dotenv quoted internal line endings remain untouched.
- File publication uses temporary files on the destination filesystem, with Unix files held inside a private child directory until rename, new Unix mode 0600 independent of caller umask or inherited Windows ACL, preserved existing access permissions, and rejects symlink/multiple-hardlink replacements. Read all inputs before publication. Failure/cancellation removes unpublished temporaries and preserves destinations. No backups, locks, concurrent-change detection, or undo of completed writes; last successful replacement wins. Windows sharing can reject overlapping inspection/publication; there is no automatic retry. Repeated concurrent tests require at least one success per round, a complete output from a successful writer, cleaned staging before a later uncontended winner, and fixture-only exit/status diagnostics on failure. A deterministic Windows sharing-denial fixture separately verifies unchanged destination bytes, runtime failure, cleanup, and successful publication after the test-owned handle closes.
- Exit codes are 0 success, 1 runtime/content/filesystem/limit failure, 2 CLI usage, 130 handled Ctrl+C, and 143 handled Unix SIGTERM. Reading, processing, and output are interruptible. Stdout is result-only; a write failure or cancellation while emitting stdout can leave partial bytes. File output never duplicates the result on stdout.
- Structured `tracing` diagnostics use stderr, warnings/errors by default and additional `RUST_LOG` detail. Color requires a TTY and respects `NO_COLOR`. Only operation, input/document ordinals, line/column, and stable classifications are permitted; never log input content, keys, values, paths, argv, environment, or raw dependency errors. Runtime is offline, executes no shell, and stores no configuration, caches, or history.

Resolved YAML mappings share persistent ordered-tree branches and key strings across merge versions. Incremental byte/line totals and depth frequencies avoid rescanning every inherited key. Structural diffs skip shared branches when merging repeated or nearly identical operands, retaining earlier-operand and explicit-key precedence. Per-entry byte/line contributions saturate above the output ceiling, while their u64 aggregate remains exact for subtraction; replacing an oversized value therefore restores the correct small result and depth. A long shadowed merge-chain process fixture must complete under a 256 MiB Linux child address-space limit.

Configuration, transformations, readiness waits, and system utilities share the root CLI but have separate private runtimes. The private `config_command` module owns configuration argument shapes and jobs, while `cli` composes all command families. Configuration publication stays in `config_publication`, with full validation before bounded output and private staging. Streaming transformations retain the separate `publication` module and supervisor-owned staging from the issue #917 contract below. `config_runtime` bounds processing and returns numeric cancellation statuses after publication cleanup; `runtime` returns numeric cancellation for owned work and preserves child signal behavior for system utilities. The Tokio wait runtime separately owns readiness cancellation and final JSON; the transformation supervisor returns numeric 130/143 for handled interruption. Only the selected command family's signal handlers are installed per invocation.

### Readiness commands (#919)

### Polling and deadlines
The first check starts immediately. Overall waiting is unlimited by default; `--timeout 0` explicitly means unlimited. Durations accept nonnegative integers suffixed `ms`, `s`, `m`, or `h`; bare `0` is accepted only for timeout. Interval and attempt timeout must be positive. Fractions, negative values, millisecond arithmetic overflow, and unrepresentable monotonic deadlines fail validation.

The interval defaults to 250ms **after** an unsuccessful attempt completes. Polling attempts never overlap. Network attempts default to 3s. One monotonic overall budget includes DNS configuration/resolution, connection, trust initialization, TLS, response headers, and polling delays. Attempts and delays are clipped to its remaining duration. A successful observation after the overall deadline is not readiness within that deadline. The implementation has an injected Tokio clock for deterministic scheduler tests.

Cancellation drops the active asynchronous operation and its connections, stops polling, and releases owned resources without terminating the service or mutating the file. Blocking read-only OS metadata/trust/configuration calls run off the runtime thread; runtime shutdown must not wait for an uninterruptible OS call after a handled deadline or cancellation. A successful result is a point-in-time observation and does not reserve the resource or guarantee continued availability.

HTTP client/native trust initialization retains one in-flight blocking job across attempt timeouts. Later attempts await that same result instead of spawning overlapping native certificate loaders; the completed client is reused for the invocation. Overall timeout and cancellation still stop waiting promptly without waiting for that blocking job to return.

### TCP
Accept DNS names, strict IPv4, and bracketed IPv6 with an explicit decimal port 1–65535 (`localhost:3000`, `127.0.0.1:3000`, `[::1]:3000`). Resolve using system DNS configuration and hosts data, with no persistent cache or fallback public resolver. All returned addresses receive a connection opportunity within a shared attempt deadline; one stalled address/family cannot consume another address's opportunity. Stop when one connection succeeds and close it without sending application data. This establishes connectivity only, not application-protocol readiness.

Retry DNS lookup failures, refusal, temporary network failures, and attempt timeouts. A permission or other terminal error for one destination does not discard other pending connections. Return readiness when any address connects; if every address fails, preserve a terminal classification over retryable failures regardless of completion order. Local permission denial, resolver initialization failure, local resource errors, and other non-readiness failures then terminate. Connection attempts within one polling attempt may run concurrently; polling attempts never do.

### HTTP and TLS
Plain HTTP uses HTTP/1.1; HTTPS negotiates HTTP/2 or HTTP/1.1 through ALPN. Enable reqwest's HTTP/2 feature explicitly for standalone builds and advertise both protocols in the preconfigured Rustls client; no h2c prior-knowledge mode is exposed.

Accept absolute `http` and `https` URLs. Inspect raw authority before URL normalization, rejecting all user information including empty userinfo, malformed authority, and control/whitespace ambiguity. Default GET succeeds on any final 2xx; HEAD is selectable and `--status` selects exactly one final code in 200–599. Observe status at response headers and drop the response without reading, inspecting, printing, or waiting for the body. Do not follow redirects: a redirect succeeds only when its exact status is selected.

Retry unexpected statuses, DNS/connection failures, temporary network failures (including truncated/closed connections), and attempt timeouts. Certificate trust/hostname failures, OS trust initialization failures, TLS protocol faults, invalid HTTP responses, permissions, and other runtime faults are terminal. DNS, connections, TLS negotiation, and response headers share one attempt budget. reqwest automatic retries and connection pooling are disabled; clibox owns polling retries.

TLS uses OS roots and verifies hostname/trust. Preserve usable roots when other OS entries fail loading or parsing; trust initialization fails when no usable roots remain. Debug logs expose only aggregate root/error counts, never individual loader errors or certificate data. No proxy discovery, authentication, custom headers, custom CA options, client certificates, certificate-verification bypass, cookies, or implicit credentials. Explicitly disable proxy environment discovery and redirects even under Cargo feature unification. Clear only `SSL_CERT_FILE`/`SSL_CERT_DIR` in the single-threaded executable startup before native roots are loaded, because the current loader would otherwise replace OS trust with those overrides. This clearing is scoped to wait execution only; `run env` must preserve both variables in the child environment unless explicitly assigned; no caller environment or OS trust store is mutated. TLS tests inject isolated roots internally and never alter user trust. Rustls uses an explicitly selected ring provider and no client identity or TLS key-log configuration.

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

## Storage and Security
No persistent configuration, cache, operation history, application authentication, tenancy, backend service, or telemetry is added. Use current-user/session permissions only. For OS utilities and transformations, no automatic retries or fixed execution timeout applies apart from the shared port-termination wait; readiness waits use the polling/deadline contract above. Users can interrupt child execution, stdin reading, OS work, application waits, and transformations. Owned OS operations and transformations return numeric 130 for Ctrl+C/Windows Ctrl+Break and 143 for Unix SIGTERM after cleanup; `run env` preserves delegated child status and Unix signal identity. Cancellation does not undo completed clipboard replacement, termination, launch, or file publication. Transformations may read/write explicit files and same-directory temporary output under the publication contract below.

Explicit configuration-file publication is the only persistent state owned by configuration commands; unpublished staging files are cleaned on handled failure/cancellation and completed writes are not undone. Configuration processing accesses no network and delegates no commands. No persistent application configuration, cache, operation history, application authentication, tenancy, backend service, or telemetry is added. Use current-user/session permissions only. For environment, port, open, and clipboard commands, no automatic retries or fixed execution timeout applies apart from the shared port-termination wait; readiness waits use the polling/deadline contract above. Users can interrupt child execution, stdin reading, OS work, and application waits. Cancellation does not undo completed clipboard replacement, termination, or launch.

Wait operations add no persistent configuration, application cache, history, authentication, tenancy, backend, privileges, or migrations. Wait commands do not modify target files. Rollback is an earlier pinned package version; readiness observations have no persisted effect to reverse.

Cargo publication remains explicit and gated by the repository release coordinator. Rollback is installation of an earlier pinned version; previous OS effects are not undone. No feature flag is needed because each capability is explicitly invoked.

## Security
No raw argv/locator/credential/body logging, including parser failures or dependency errors. Cargo publication remains explicit and gated by the repository release coordinator. There is no feature flag, remote telemetry, metrics service, dashboard, compliance gate, separate SLO, performance target, or mandatory external-service/manual validation.

## Logging
Structured `tracing` diagnostics go to stderr, defaulting to warnings/errors. Routine pending checks are debug events; `RUST_LOG=clibox=debug` enables kind, attempt, elapsed time, numeric HTTP status, and stable failure classification. Filter out **all dependency targets**, even with `RUST_LOG=trace` or explicit dependency filters, and never echo malformed filter values. `NO_COLOR` disables color; otherwise color is allowed only on a TTY. Static wait/parser failure diagnostics remain visible even if log filtering disables tracing.

Structured Rust tracing goes to stderr, defaults to warnings/errors, and honors RUST_LOG for detailed diagnostics. Stdout is exclusively results or inherited child stdout. Color is TTY-only and honors NO_COLOR. Utility diagnostic context includes operation, backend, PID, port and stable failure code, never clipboard content, environment values, transformation input, patterns, replacements, digests, complete argv, full URLs or paths. Dependency logging is excluded even with trace logging enabled. Clap errors are redacted rather than echoing rejected values. Child output is inherited user-program output, not authored diagnostic output. Configuration diagnostics permit only operation, input/document ordinals, line/column, and stable failure classifications; keys, values, content, environment, and dependency error text are excluded. Dotenv and YAML source diagnostic columns count Unicode scalar values, while token slices and resource limits continue to count UTF-8 bytes. YAML source validation treats CR, LF, and CRLF as one line break each, including when the redacted fallback reports positions with logging disabled. Configuration debug events mark operation/input start and completion; Windows rename/cleanup failures may also include only a numeric OS error code; panic payloads and locations are suppressed for all commands. Help/version are user output, not logs. Malformed arguments use static actionable usage diagnostics without supplied values. When tracing suppresses configuration error events, a direct stderr fallback emits only static guidance, operation, enum classification, and numeric input/document/line/column positions; this includes argument validation, initialization, content, I/O, publication, and cancellation failures. A closed diagnostic stream must not panic or change success, runtime-failure, or cancellation status; disable the tracing subscriber's internal fallback that prints raw write errors to stderr.

Wait signal handlers are installed only by the Tokio wait runtime. Configuration commands install only their private cancellation runtime, utilities retain native signal forwarding/reproduction, and transformations retain their publication/cancellation supervisor. Never register more than one handler family per invocation.

## Build and Test
- `cargo run -p clibox -- wait --help` and `cargo test --locked -p clibox -p clibox-config -p clibox-fspy -p clibox-system -p clibox-transform -p clibox-wait`.
- Root `cargo test` and `cargo fmt --all --check` remain required repository checks.
- Validate npm/native artifact packaging; no crates.io publication or Cargo publish dry-run is required.
- `pnpm --filter @delino/clibox test` and `pnpm --filter @delino/clibox test:package` validate the launcher and installed consumers.
- Scheduler tests use paused time: immediate first checks, unlimited waiting, after-completion delays, clipped budgets, no overlapping polls, retry recovery, and cancellation/resource drop during checks and delays.
- Loopback TCP/HTTP and isolated TLS fixtures cover DNS/IPv4/IPv6, multiple addresses/refusal/cleanup, GET/HEAD, statuses/transitions, redirect policy, delayed headers, nonterminating bodies, trusted/untrusted certificates, hostname mismatch, partial trust loading, malformed root entries, and trust loading without usable roots.
- Delayed TCP startup uses a dev-only socket2 socket bound before the child runs and retained until `listen`, preventing ephemeral-port reuse by parallel fixtures while still exercising the not-yet-listening state.
- File/process fixtures cover empty/delayed/relative/Unicode files, symlinks/dangling links/loops/special files, metadata without content access, typed permissions/error races, parser validation, output/exit behavior, handled Unix signals and isolated-console Windows Ctrl+C, proxy isolation, and secret markers under detailed or invalid log filters.
- `node-clibox-test` selects all six crates for Cargo unit/process tests on Linux, macOS, and Windows. Help assertions account for clap's platform-specific executable name, including `clibox.exe` on Windows. Release builds run those tests for all eight targets before packaging and preserve Alpine consumer validation. Cross-platform evidence is produced by those jobs; local validation alone does not claim all platforms were executed.
- ring requires a C compiler at build time: MSVC on Windows, Xcode clang on macOS, and native C compilers on GNU Linux. Both musl jobs install `musl-tools` and set the target-specific `CC` to `musl-gcc` for ring, while the final linker remains pinned `rust-lld` with self-contained Rust runtime objects. No runtime OpenSSL/shared crypto dependency is added. Linux HTTPS consumers need their OS CA certificates, including Alpine `ca-certificates`.
- Preserve Cargo/npm exact version synchronization, all eight native targets, script-free installation, and launcher argv/signal forwarding. Remove generated repository-owned `dist` after validation. No actual package publication is part of implementation.

- `cargo test --locked -p clibox -p clibox-config -p clibox-fspy -p clibox-system -p clibox-transform -p clibox-wait` covers parser contracts, compatible environment transformations, process execution/signals, test-owned TCP/UDP IPv4/IPv6 sockets, mocked partial termination and shared deadlines, mocked open/clipboard adapters, invalid text boundaries, and secret-marker diagnostic checks.
- Root `cargo test` and `cargo fmt --all --check` remain mandatory. Generate the existing DevHud frontend dependency before root Rust checks, then remove repository-owned generated dist directories after validation.
- The six-crate test selection includes private adapter sources and unit tests as well as the executable's process tests. macOS compilation uses its standard SDK/libclang through libproc; Linux adapters add no system C-library dependency beyond the target's libc; readiness TLS additionally compiles ring as documented above.
- Linux/macOS/Windows CI runs clibox Rust process tests alongside npm distribution/consumer tests. The existing eight-target release matrix and Alpine consumers remain intact. Test termination may target only disposable test-owned processes; CLI kill tests decline when complete exclusive port ownership cannot be established.
- Automated parser/process/mocked-adapter tests are the completion gate. Real GUI launching, desktop clipboard persistence, and application termination waiting remain deferred follow-up verification and must not be represented as completed by mocks.

- Windows DACL preservation fixtures compare the ACL entries and protection/control semantics, allowing only the OS-maintained `SE_DACL_AUTO_INHERITED` bookkeeping bit to change; they do not require byte-identical self-relative security descriptors.
- Unit/process tests cover CLI defaults/conflicts, dotenv syntax and precedence, YAML Core/merge/alias semantics, precision and idempotence, encoding, exact/exceeded aggregate input and output limits, depth/expansion, file permissions/links/failure cleanup/concurrent replacement, interruption, broken stdout, and secret-marker privacy under trace logging. npm packaging also checks the native executable version against both source manifests.


Windows repository integration also drives the Node launcher with a real native child in a disposable console, checking both Ctrl+C and Ctrl+Break, configuration exit 130, transformation exit 130, unchanged destinations, and removed staging. Standalone Cargo packages skip this integration when the npm workspace is absent; native-only cancellation tests still run.

The isolated Windows Ctrl+C test helper must explicitly clear inherited Ctrl+C-ignore state before spawning clibox, including under Git Bash release jobs. Keep this normalization confined to the test-owned process and include helper stdout/stderr on failure; production signal behavior is unchanged.

## Dependencies and Integrations
Uses clap, serde/serde_json, regex, tracing/tracing-subscriber, the pure-Rust yaml-rust2 event parser, im-rc persistent ordered maps, Unix signal-hook/libc, macOS libproc/Objective-C framework bindings, Windows windows-sys, and Linux x11rb with installed desktop tools. Transformations additionally use base64, pinned pure-Rust BLAKE3, chrono with pinned chrono-tz, sha2, ctrlc, and tempfile. Configuration publication also uses tempfile and Windows configuration cancellation uses ctrlc. npm distribution remains owned by `packages/clibox`; clibox is excluded from the root cargo-mono tag allowlist, and Release Project still creates `clibox@v<version>` after immutable release-source validation without waiting for main CI or publishing to the Cargo registry. Readiness uses Tokio, hickory-resolver, reqwest/hyper, Rustls/ring, and rustls-native-certs; dev-only TLS fixtures use rcgen and tokio-rustls. Public documentation is owned by the consolidated `/clibox` section under `docs/apps-clibox-docs-foundation.md`; no public library API is introduced.

GNU Linux releases use the pinned AlmaLinux 9/glibc 2.34 build boundary for both npm and stable APT/DNF distribution. The same executable bytes pass ELF compatibility checks before signed GitHub publication; see [native repository ownership](repository-linux-packages-contract.md). Musl remains a separate npm target and desktop tools remain optional user-installed runtime capabilities.

A scanner adapter validates YAML 1.2 directives and resolves all document-local Core tag handles before grammar parsing, compensating for the parser's permissive version handling and loss of earlier tag directives; width-preserving token substitutions retain diagnostic positions. YAML resolution retains a shared reference graph and computes output size before emission; numeric lexemes never convert through machine numbers. In-place YAML input uses one no-follow handle for validation and reading, including handle-based regular-file and single-link checks on every supported platform. The blocking reader owns that handle until input is complete; pathname replacement cannot redirect the read after validation. Ordinary non-in-place reads still follow links. Configuration Unix destination inspection uses a read descriptor or a non-truncating write-only descriptor when read access is denied; neither path reads or changes destination content before publication. Both retain no-follow/nonblocking flags and handle-based regular-file/link checks. Unix mode/owner/group and Linux/macOS ACL preservation use native OS APIs; Windows replacement retains its destination DACL. Unix staging directories enforce mode 0700 independently of umask, clear inherited macOS ACL grants before file creation, and remain alive through permission copying and atomic rename; failure/cancellation cleans up both file and directory. Windows configuration publication uses its private `config_windows_publication` adapter: acquire delete/security/attribute access to staging (including both READ_CONTROL and WRITE_DAC, since DACL assignment also reads its inheritance state), clear temporary attributes, copy the validated destination DACL and protection state through handles, close the destination inspection handle, check cancellation, and commit with `SetFileInformationByHandle(FileRenameInfo)`. The destination data handle must close before rename even when it shares deletion; otherwise the operation blocks on its own inspection handle. Avoid `ReplaceFileW`, whose multi-step namespace changes can interfere between concurrent writers. The retained delete handle cleans unpublished staging even after restrictive DACL copying, and successful publication disables old-path cleanup. Permission diagnostics record only an operation enum and numeric OS error at debug level; concurrent-writer process fixtures enable those diagnostics on failure. Windows adapter fixtures cover DACL copying followed by either commit or unpublished staging cleanup, alongside restrictive-DACL cleanup and no-clobber races. No locks, retries, backup files, or cross-family dependencies are added. Unsupported rename capabilities fail closed.

Homebrew and a public library API remain excluded. Public guides use the existing `public-docs` workspace and deployment. GNU Linux GitHub Release archives and stable APT/DNF packages use the release contract below.

## Change Triggers
Update the project index, both domain contracts, relevant root/domain AGENTS rules, native/npm READMEs, CLI help, tests, and applicable release workflows together when command behavior, privacy, naming, versions, platforms, dependencies, or publication changes.

## References
- [Project index](project-clibox.md)
- [npm distribution](packages-clibox-distribution-contract.md)
- [Repository defaults](repository-defaults.md)

## Issue #917 command contract

### Command interfaces

```text
clibox text replace PATTERN REPLACEMENT
  [--regex] [--first] [--require-match]
  [--input FILE | --text TEXT]
  [--output FILE | --in-place] [--force]

clibox time format [VALUE]
  [--from rfc3339|date|unix-s|unix-ms | --input-format FORMAT]
  [--to rfc3339|unix-s|unix-ms | --format FORMAT]
  [--timezone ZONE]

clibox time add [VALUE]
  [--from rfc3339|date|unix-s|unix-ms | --input-format FORMAT]
  [--to rfc3339|unix-s|unix-ms | --format FORMAT]
  [--timezone ZONE]
  [--years N] [--months N] [--weeks N] [--days N]
  [--hours N] [--minutes N] [--seconds N]

clibox base64 encode
  [--input FILE | --text TEXT]
  [--url-safe] [--no-padding]
  [--output FILE] [--force]

clibox base64 decode
  [--input FILE | --text TEXT]
  [--url-safe] [--no-padding]
  [--output FILE] [--force]

clibox hash compute
  [--algorithm sha256|sha512|blake3]
  [--input FILE | --text TEXT]
  [--format hex|base64|checksum]
  [--output FILE] [--force]

clibox hash verify EXPECTED
  [--algorithm sha256|sha512|blake3]
  [--input FILE | --text TEXT]
  [--format hex|base64]
  [--quiet | --json]
  [--output FILE] [--force]

clibox hash verify --check CHECKSUM_FILE
  [--algorithm sha256|sha512|blake3]
  [--quiet | --json]
  [--output FILE] [--force]
```

### Common input, output, and failure behavior

- File/string input selectors are mutually exclusive. Without either, read stdin until EOF. `--input -` explicitly selects stdin. Explicit file/string input does not consume stdin.
- Direct strings provide their UTF-8 bytes without an implicit newline. Base64 and hashing support arbitrary binary file/stdin input; text replacement requires valid UTF-8.
- Output defaults to stdout. Text and Base64 add no newline. Time values and encoded hashes end with one LF.
- `--output FILE` writes the command result to a file without duplicating it on stdout; `--output -` selects stdout. `--force` is required to replace an existing output file and requires a real file destination or `--in-place`.
- Exit codes are `0` for success, `1` for runtime failure or checksum mismatch, `2` for missing, malformed, or conflicting CLI arguments, `130` for Ctrl+C/Windows Ctrl+Break, and `143` for Unix SIGTERM.
- Keep diagnostics on stderr and command results on stdout. Provide English help, examples, and actionable errors.
- Impose no fixed input/output size limit. Text replacement loads the complete input/result into memory; Base64 and hashing stream data. Resource exhaustion can fail the operation.
- Streaming stdout may contain partial output before an input, decoding, write, or cancellation failure. Document this behavior.

### File publication

- Prepare file output in a temporary file in the destination directory and publish it only after processing succeeds.
- Preserve existing access permissions when replacing a file; fail rather than silently discarding them. Ordinary forced output replacement requires metadata/security access but not read access to existing contents; in-place transformation still requires readable input.
- Reject symbolic-link and multiply-linked destinations for replacement. `text --in-place` requires one explicitly selected regular input file and cannot be combined with `--output` or `--text`.
- Do not create automatic backups or check for concurrent modifications. Atomic replacement does not provide locking or lost-update protection; the last successful replacement wins.
- Clean up unpublished temporary files on handled failures/cancellation. Completed replacements are not undone.
- Verification reports are complete results even when they report mismatches or per-entry errors: publish the completed report and return code `1`. `--quiet` produces no result file and conflicts with `--output`.

### Text replacement

- Default to case-sensitive literal replacement of every non-overlapping match. Treat replacement text literally in this mode.
- `--regex` enables Rust `regex` syntax and `$1`/`${name}` replacement references. Follow its documented escaping and inline flags; do not add lookaround or pattern backreferences.
- `--first` replaces only the first match in the entire input. `--require-match` makes zero matches a runtime failure; otherwise zero matches succeeds unchanged.
- Reject empty search patterns and references to nonexistent capture groups. Allow an empty replacement, explicit zero-width regular expressions such as `^`, and unmatched optional groups according to the regex replacement semantics.
- Preserve UTF-8 content, whitespace, and line endings except where explicitly changed by a match. Validate before publishing an in-place result.

### Time formatting and arithmetic

- Omitted `VALUE` captures the current instant once. The default input format is RFC 3339, the default output is RFC 3339, and the default processing/output timezone is UTC.
- Require explicit `--from unix-s` or `--from unix-ms` for signed integer timestamps; do not guess units from digit counts.
- `--from date` accepts `YYYY-MM-DD` at midnight. Custom input/output formats use a documented, platform-independent strftime-style implementation with fixed English/C-locale conventions.
- Explicit input offsets establish the instant. Offset-free civil input uses `--timezone`; that option also selects the timezone for calendar arithmetic and output.
- Support UTC and IANA timezone names. Bundle the same IANA database across all platform artifacts of a clibox version; never prefer the OS database. Rule updates arrive through clibox releases.
- Support years `1`–`9999` and up to nanosecond precision. Reject leap seconds, invalid dates, unknown zones, unsupported format directives, and out-of-range results.
- Preserve fractional precision unless the requested output is coarser. Integer Unix timestamp output uses mathematical floor, including negative timestamps.
- `time add` requires at least one unit option. Values are signed integers; explicit zero is valid.
- Combine years/months into a calendar-month adjustment, clamp to the destination month’s final day, then apply combined weeks/days as calendar days, then hours/minutes/seconds as elapsed time.
- A calendar day differs from 24 elapsed hours across DST transitions. Reject nonexistent or ambiguous local times during parsing or calendar arithmetic; do not automatically shift them or choose an occurrence.
- Do not change the system clock.

### Base64

- Default to the standard alphabet with padding. `--url-safe` selects the URL-safe alphabet; `--no-padding` selects unpadded encoding/decoding.
- Do not automatically detect or mix alphabets or padding modes.
- Encoding produces one unwrapped stream without an added newline.
- Decoding ignores ASCII whitespace and rejects other invalid characters, invalid padding, and incomplete/noncanonical encodings for the selected mode.
- Empty input succeeds with empty output. Decoding writes the original bytes without UTF-8 conversion or newline normalization.

### Hash generation and verification

- Support `sha256`, `sha512`, and fixed 256-bit BLAKE3. Default to SHA-256.
- Hash exact input bytes without line-ending or text normalization.
- `hash compute` defaults to lowercase hex. Base64 output uses the standard padded alphabet.
- `--format checksum` requires a file input and emits a GNU-style checksum record, including its filename escaping rules and binary marker. The selected algorithm remains explicit rather than embedded in or inferred from the record.
- With explicit `--output`, resolve a relative input and the output parent through the filesystem, then record a path relative to that parent; use an absolute path if Windows volumes/shares differ. Preserve supplied absolute input paths and supplied paths in stdout records. This makes generated file manifests directly verifiable from any cwd, including output through a symlinked directory. Shell redirection has no known manifest destination and cannot rebase records.
- Direct verification accepts hex case-insensitively or explicitly selected standard padded Base64. Validate digest length against the selected algorithm.
- `--check` is mutually exclusive with direct digest/input options. `--check -` reads the manifest from stdin.
- Parse GNU-style checksum records using the selected algorithm. Resolve relative file paths against the manifest’s directory, or cwd for a stdin manifest. Preserve absolute-path behavior.
- Process entries in order and continue after individual mismatches or file-read failures. Malformed records are reported as errors; an empty manifest is an error.
- Return `1` if any record is malformed, mismatched, or unreadable. Do not silently ignore missing files.
- Human output identifies the file or `stdin`/`text` source and its `ok`, `mismatch`, or `error` status. Never print the supplied text or expected/actual digests in verification results.
- Failed verification always emits a static redacted stderr summary, even with `--quiet` or `RUST_LOG=off`. `--quiet` suppresses stdout. `--json` returns one object containing ordered `results` and `errors`; results include source identification, status, and applicable stable error code, while manifest-level errors include the line number when available. Neither format contains raw input content or digests.

### Runtime, privacy, and compatibility

- Implement transformations in Rust without invoking installed `sed`, `date`, Base64, or checksum tools. Use enum-backed command modes and algorithms.
- Operate offline with current OS permissions. Add no application authentication, tenancy, persistent settings, cache, operation history, service, or remote telemetry.
- Input/output files and temporary files required for atomic publication are the only new file state.
- Add no automatic retries or fixed execution timeout. Support interruption of input reading and processing; cancellation does not reverse completed output publication.
- Use structured `tracing` diagnostics on stderr, defaulting to warnings/errors with `RUST_LOG` for detail. Honor `NO_COLOR` and use color only on a TTY.
- Logs and diagnostics must omit input content, digests, patterns, replacements, raw argv, and paths, including those embedded in dependency errors. Explicit verification-result filenames are command output, not diagnostic logs.
- Preserve existing help/version and the implemented #916 interfaces, launcher argv/signal forwarding, exact version synchronization, script-free installation, eight target packages, and release boundaries.
- No service SLO, telemetry dashboard, or additional compliance gate is required. Rollback uses an earlier pinned package version and does not restore overwritten files.


## Implementation and compatibility decisions

- `--in-place` explicitly authorizes replacement without a separate `--force`; `--force` affects ordinary `--output` replacement. `--output -` selects stdout; `--output ./-` names a literal dash file. Force requires real file output or in-place replacement, with redundant `--in-place --force` accepted. Validate these combinations before reading input or creating output.
- `clibox/src/cli.rs` composes the companion crates' system and transformation command enums plus readiness into one public command tree. `clibox-system/src/system.rs` owns OS-command dispatch; its `runtime.rs` and `error.rs` retain OS signal/child-status and safe contextual-error behavior. `clibox-transform/src/transform.rs` and its `transform_error.rs` retain transformation cancellation/publication supervision and safe exit-code diagnostics. Only the selected command family installs its signal handler; readiness waits dispatch through `clibox-wait/src/wait_command.rs` and its Tokio runtime, independently of OS utilities and transformations. A cancellable preparation worker validates semantic arguments before filesystem work and captures the time instant once. The transformation worker sends bounded chunks to an output worker; `clibox-transform/src/transform.rs` alone owns transformation cancellation supervision and final publication. Workers cannot publish. Handled cancellation returns numeric 130 for Ctrl+C/Windows Ctrl+Break or 143 for Unix SIGTERM; the previous transformation-only SIGHUP cleanup with exit 1 remains supported. Cancellation never joins blocked workers; process exit closes remaining worker handles. No fixed input/output size limit is added.
- `clibox-transform/src/publication.rs` owns same-directory temporary paths, no-clobber or replacing rename, final link/type revalidation, and permissions. Unix ownership, mode and access ACLs are retained; macOS empty ACLs are explicitly restored. Windows owner/group/DACL and access attributes must be retained. Permission preservation failures are fatal. Destination inspection does not open file contents: Linux uses an O_PATH handle and the held inode through `/proc/self/fd` for POSIX ACL xattrs (missing procfs fails before publication); macOS uses non-following metadata and ACL pathname APIs because O_EVTONLY still requires data access; Windows requests FILE_READ_ATTRIBUTES and READ_CONTROL only. No original-file removal, backup, identity comparison, locking, or multi-file transaction is introduced.
- Windows retains a DELETE/FILE_WRITE_ATTRIBUTES temporary-file handle before permission copying and publishes with FileRenameInfoEx, using REPLACE_IF_EXISTS/IGNORE_READONLY_ATTRIBUTE only for authorized replacement. This preserves the original read-only attribute throughout preparation, cancellation, and publication failure; the OS requires attribute-write permission for a read-only target. Unsupported filesystem capabilities fail without changing the original. The held handle clears only unpublished temporary attributes and marks that temporary inode for deletion on failure, even after a restrictive ACL is copied. Apply the original access attributes before its DACL; a copied FILE_WRITE_ATTRIBUTES denial must not prevent an otherwise permitted rename. Do not use tempfile's Windows persist path: it clears the temporary file's attributes before MoveFileEx and cannot replace a read-only target.
- Custom formats follow the pinned Chrono strftime grammar in fixed English/C locale. Input `%Z` and output `%#z` are unsupported; unknown directives fail. Custom date-only input defaults to midnight and missing time components default to zero. RFC 3339 parsing requires an explicit offset, rejects leap seconds and more than nine fractional digits. Its output retains the input fractional digit count (integer seconds/date: zero; milliseconds: three; current instant: nine). Historical IANA sub-minute offsets require custom output with `%::z`; RFC 3339 cannot represent them and fails rather than rounding.
- The timezone database is the pinned `chrono-tz` dependency's `IANA_TZDB_VERSION`; no OS database, `TZ`, `TZDIR`, or locale override participates in calculations. Dependency/database changes arrive with a clibox release.
- GNU untagged manifests accept binary and text markers but always hash raw bytes. LF/CRLF record separators are accepted. Malformed records, including blank/comment lines, are errors; no missing entry is skipped. A filename `-` inside a record is a relative file, not another stdin selector. Unix non-UTF-8 filenames round-trip as native bytes in checksum records and use escaped bytes in reports.
- Verification JSON has ordered `results` and `errors`. Each result contains `source` (`kind`: `file`, `stdin`, or `text`; file sources also have `path`), `status` (`ok`, `mismatch`, or `error`), and an optional stable `code`. Manifest errors contain `code` and an optional one-based `line`. No digests or text content are included. Human source names escape control characters.
- Complete reports, including manifest read/parse errors and per-entry failures, publish with exit 1. Cancellation and output failure do not publish incomplete file reports.

## Expanded validation

Run the root Rust suite and formatter check, `cargo test --locked -p clibox -p clibox-config -p clibox-fspy -p clibox-system -p clibox-transform -p clibox-wait`, native artifact packaging, npm launcher/distribution tests and installed npm/pnpm consumer smoke tests. CI runs the Rust command suite on Linux, macOS, and Windows and exercises the seven commands through the installed launcher for all eight existing native artifacts, including Alpine musl consumers. Fixtures cover parsing/redaction, Unicode and binary data, strict Base64 chunk boundaries, exact hash vectors, timezone/DST/precision behavior, ordered manifests, permissions/ACLs/links, failed publication, last-writer-wins, cancellation, and partial/broken stdout. Remove repository-owned generated `dist` directories afterward.

Windows protected-DACL fixtures compare owner/group, ACEs and protection state and probe effective attribute-write access by opening a fresh handle requesting exactly `FILE_WRITE_ATTRIBUTES`. Check both allowed and denied cases before publication, after cancellation and after replacement. Reapplying unchanged attributes through `SetFileAttributesW` can succeed without proving that access is allowed, so it must not be used as a denial assertion.

## Issue #953 execution-wrapper contract

`clibox run` is a command group. With no subcommand it renders its command help to stderr and exits 2; explicit help succeeds on stdout. Its five subcommands accept wrapper options before one literal workload using the `run env` grammar:

```text
[KEY=VALUE ...] [--] COMMAND [ARG ...]
```

Every workload is prepared by the existing environment planner: assignments expand from the parent environment, duplicate and empty assignments retain `run env` behavior, argv is never shell-evaluated, child PATH/PATHEXT lookup remains platform-specific, cwd is inherited, and the child environment is isolated. On Windows, wrappers supervise blocking executable lookup against cancellation and the active wrapper deadline, and a late lookup result cannot start a child. All wrappers accept `--kill-after DURATION` (default `5s`). Durations are nonnegative integer `ms`, `s`, `m`, or `h` values; periods, polling, attempts, and retry delays are positive. Bare `0` disables an individual execution limit (`--ready-timeout`, retry `--timeout`, or either timeout limit), while a lock/rate admission `--wait-timeout 0` makes an immediate admission decision.

| Command | Contract |
| --- | --- |
| `run with-rate-limit` | Requires `--name`, `--limit`, and `--period`; optional `--burst` from 1 through 9007199254740992, scope, project identity, and admission wait timeout. It deducts one shared token immediately before spawning, does not refund on spawn/cancellation failure, and preserves bucket configuration. |
| `run with-lock` | Requires `--name`; `--on-locked wait` is default, `skip` exits 0 without workload, and `fail` exits 75. `--wait-timeout` is valid only for `wait`. |
| `run with-service URL` | Waits for headers-only HTTP readiness before the workload. `--service SERVICE ... -- WORKLOAD ...` owns the service; without it the endpoint is external and is never terminated. |
| `run with-retry` | Retries eligible nonzero numeric child exits, defaulting to three total attempts, 1 s initial delay, factor 2, 30 s cap, and full jitter. It never replays stdin, retries spawn failures, or retries Unix signal termination. |
| `run with-timeout` | Requires a positive overall `--timeout` or `--idle-timeout`. Output bytes from either workload stream reset idle timing at the successful read and are immediately forwarded; rapid reads retain their latest activity timestamp. Supervision wakes at the earliest active overall or idle deadline, and a read after an expired idle deadline cannot clear that timeout. A forwarding failure stops owned work and returns a wrapper failure. |

Names are case-sensitive ASCII `[A-Za-z0-9._-]{1,128}`. `project` scope is the canonical invocation directory by default; `--project-dir` selects another existing directory for identity only and is invalid with `user` scope. Keys hash the namespace, scope, stable OS machine identity, canonical identity, and name, so state contains no raw project path, machine identity, name, command argv, environment values, or output. Lock and bucket namespaces are separate.

State is same-user and same-machine only: Linux uses `$XDG_STATE_HOME/clibox/run` or `~/.local/state/clibox/run`; macOS uses `~/Library/Application Support/clibox/run`; Windows uses `LocalAppData/clibox/run`. Directories and state files must be private, regular, and non-substituted. On Unix, every existing state-directory ancestor must be owned by root or the current effective user. An accepted root- or current-user-owned ancestor symlink must also have its full canonical target ancestor chain validated by those same rules. New macOS state objects clear inherited extended ACLs and existing objects reject every extended ACL entry. Windows state handles reject reparse points and file hard links, and require the current user to own a protected single-user DACL. The machine identity is only an input to the state-key hash, so a shared network home remains independently coordinated per machine without storing or logging that identity. Linux prefers a nonzero `/etc/machine-id`, then the D-Bus machine ID; an all-zero machine-ID sentinel is invalid. Windows requires a nonzero canonical `MachineGuid` UUID. Minimal containers without either Linux identity use the kernel boot ID, retaining coordination for that running kernel without creating identity state. Unsafe permissions, links, ownership/ACLs, malformed JSON, unknown state fields, or failed atomic publication stop execution. Bucket state is versioned, capped at 4 KiB before parsing, and atomically replaced while an OS-owned file lock guards coordination. It records capacity/configuration, token accounting, and UTC refill baseline only. Backward UTC movement adds no tokens or moves the baseline backward; forward movement refills only to burst capacity. State is retained. Operators may remove the affected hashed local state only after every affected invocation has stopped; there is intentionally no management command.

HTTP readiness uses only absolute HTTP(S) URLs without user information, GET by default or HEAD, default 2xx or an exact 200–599 status, no redirects, proxies, request credentials, custom headers, body reads, custom CA, or TLS bypass. Defaults are a 250 ms interval, 3 s attempt budget, and 30 s ready budget. Every unsuccessful probe, including the preflight before external observation or managed-service startup, waits one interval before the next attempt. A managed service has one preflight: an already-ready endpoint fails before children start and directs the user to omit `--service`; a retryable not-ready result permits startup after one interval; a terminal failure does not. The first standalone `--` after `--service` is the service/workload delimiter, so a standalone delimiter inside service argv is unsupported. Managed service stdout/stderr go to wrapper stderr and are terminated on readiness failure, workload completion/failure, cancellation, or a post-spawn supervision error. Cancellation requests termination of both owned trees before waiting for either cleanup grace period. After confirmed cleanup, every outcome waits for bounded forwarding of the managed service's final output and reports a forwarding failure. If it exits before the workload completes, or its completion races the workload completion, the wrapper fails rather than assuming an exit order; it requests workload termination before any bounded service-cleanup wait. An external endpoint is only observed and is never terminated.

Root wrappers put owned Unix children in a process group and Windows children in an isolated process group and Job Object. Unix ownership is bounded to that process group: workloads and managed services must remain in it, and programs that daemonize or deliberately create another session or process group are unsupported and manage their own lifecycle. A root Unix wrapper that is itself foreground on a controlling terminal transfers that terminal's foreground process group to its workload and restores the caller's group after completion, so inherited terminal input or output remains usable when another standard stream is redirected. A foreground Unix interrupt relay joins that workload group, forwards terminal Ctrl+C once to the native supervisor, and is reaped during terminal restoration, so cancellation keeps bounded cleanup even if the workload ignores the direct interrupt. If the foreground workload stops (for example with Ctrl+Z), the wrapper restores its own terminal group and suspends the complete invoking job, including an installed npm launcher; on continuation it returns the terminal and a continuation signal to the workload group. Nested Unix wrappers keep their workloads in the root wrapper's owned process group when either their direct parent is the same wrapper executable in the required separate group, or every same-group intermediary is a structurally proved `node` or `nodejs` launcher before reaching the same outer wrapper. This evidence cannot be controlled through workload environment values, which remain unchanged. Nested wrappers signal only their direct child locally and leave the outer wrapper able to terminate every remaining descendant if an inner wrapper exits or is killed. An installed Unix launcher explicitly forwards `SIGINT`, `SIGTERM`, and `SIGHUP`, including when they target the launcher PID. On timeout, cancellation, service failure, or remaining owned work, wrappers request graceful termination, wait `--kill-after`, force termination, and allow at most five further seconds for confirmation. A second cancellation skips remaining grace. Ordering matters, for example a lock outside retry holds through all attempts while a lock inside retry is reacquired for each attempt. Cleanup cannot promise recovery after abnormal wrapper loss or Unix SIGKILL.

Natural child numeric status and Unix signal identity are preserved. Every wrapper rechecks cancellation immediately before spawning a workload, including after rate-limit, lock, or readiness admission. Admission/readiness/overall/idle timeout returns 124; lock `fail` returns 75; lock `skip` returns 0; invalid arguments return 2; other wrapper failures return 1; handled Ctrl+C/Ctrl+Break returns 130 and Unix SIGTERM returns 143. Wrapper diagnostics use redacted structured stderr tracing, allowing only command kind, lifecycle stage, counts, PID, timing, and stable classifications. They never include names, argv, paths, URLs, environment values, credentials, response data, or raw dependency errors. There is no result-file, JSON summary, queue, remote coordination, telemetry, automatic state deletion, or service adoption by port.
