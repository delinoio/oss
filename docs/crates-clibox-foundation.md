# clibox Rust foundation

## Scope
`crates/clibox` owns the Rust executable and public crates.io package `clibox`. Issue [#916](https://github.com/delinoio/oss/issues/916) extends the original help/version foundation with six OS utilities. Issue [#917](https://github.com/delinoio/oss/issues/917) adds seven text/time/Base64/hash utilities alongside them.

## Runtime and Language
Rust 2021, MIT license, repository-pinned Rust toolchain, and `clap` parsing. The crate is an explicit Cargo workspace member and approved crates.io publication target. Native adapters are private implementation modules; there is no public Rust library API.

## Users and Operators
Developers invoking a pinned CLI from terminals and npm scripts, and maintainers distributing the same executable through Cargo and npm.

## Interfaces and Contracts
The following interfaces cover OS utilities; the seven transformation interfaces and their file-publication rules follow below. Root no-argument, `--help`/`-h`, and `--version`/`-V` behavior remains compatible. Every command has English help and examples. Malformed or missing CLI inputs return 2; runtime failures return 1. Delegated commands retain their own exit status and supported termination signals.

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

Windows command conversion includes numeric references such as `$1`; an unset variable becomes empty before the child runs. Integration fixtures must preserve this cross-env behavior when delegating transformation commands. Regex capture arguments should use direct `clibox text replace` invocation, whose parser and npm launcher preserve the received dollar references.

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
No persistent configuration, cache, operation history, application authentication, tenancy, backend service, or telemetry is added. Use current-user/session permissions only. No automatic retries or fixed execution timeout applies apart from the shared port-termination wait. Users can interrupt child execution, stdin reading, OS work, application waits, and transformations. OS commands preserve supported termination signals; handled transformation cancellation returns 1 after cleaning unpublished temporary files. Cancellation does not undo completed clipboard replacement, termination, launch, or file publication. Transformations may read/write explicit files and same-directory temporary output under the publication contract below.

Cargo publication remains explicit and gated by the repository release coordinator. Rollback is installation of an earlier pinned version; previous OS effects are not undone. No feature flag is needed because each capability is explicitly invoked.

## Logging
Structured Rust tracing goes to stderr, defaults to warnings/errors, and honors RUST_LOG for detailed diagnostics. Stdout is exclusively results or inherited child stdout. Color is TTY-only and honors NO_COLOR. Allowed diagnostic context includes operation, backend, PID, port and stable failure code, never clipboard content, environment values, transformation input, patterns, replacements, digests, complete argv, full URLs or paths. Dependency logging is excluded even with trace logging enabled; parser, panic, and runtime diagnostics remain redacted. Clap errors are redacted rather than echoing rejected values. Child output is inherited user-program output, not authored diagnostic output.

## Build and Test
- `cargo test -p clibox` covers parser contracts, compatible environment transformations, process execution/signals, test-owned TCP/UDP IPv4/IPv6 sockets, mocked partial termination and shared deadlines, mocked open/clipboard adapters, invalid text boundaries, and secret-marker diagnostic checks.
- Root `cargo test` and `cargo fmt --all --check` remain mandatory. Generate the existing DevHud frontend dependency before root Rust checks, then remove repository-owned generated dist directories after validation.
- `cargo publish -p clibox --dry-run` checks standalone packaging, including private adapter sources and tests. macOS compilation uses its standard SDK/libclang through libproc; Linux builds add no system C-library dependency beyond the target's libc.
- Linux/macOS/Windows CI runs clibox Rust process tests alongside npm distribution/consumer tests. The existing eight-target release matrix and Alpine consumers remain intact. Test termination may target only disposable test-owned processes; CLI kill tests decline when complete exclusive port ownership cannot be established.
- Automated parser/process/mocked-adapter tests are the completion gate. Real GUI launching, desktop clipboard persistence, and application termination waiting remain deferred follow-up verification and must not be represented as completed by mocks.

## Dependencies and Integrations
Uses clap, serde/serde_json, regex, tracing/tracing-subscriber, Unix signal-hook/libc, macOS libproc/Objective-C framework bindings, Windows windows-sys, and Linux x11rb with installed desktop tools. Transformations additionally use base64, pinned pure-Rust BLAKE3, chrono with pinned chrono-tz, sha2, ctrlc, and tempfile. npm distribution remains owned by `packages/clibox`; the root cargo-mono tag allowlist includes clibox and releases use `clibox@v<version>`.

## Change Triggers
Update the project index, npm contract, root/domain AGENTS rules, user READMEs/help, tests and affected release/CI contracts together when commands, privacy, versions, platforms or publication change.

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

clibox hash encode
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
- `--output` writes the command result to a file without duplicating it on stdout. `--force` is required to replace an existing output file.
- Exit codes are `0` for success, `1` for runtime failure or checksum mismatch, and `2` for missing, malformed, or conflicting CLI arguments.
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
- `hash encode` defaults to lowercase hex. Base64 output uses the standard padded alphabet.
- `--format checksum` requires a file input and emits a GNU-style checksum record, including its filename escaping rules and binary marker. The selected algorithm remains explicit rather than embedded in or inferred from the record.
- With explicit `--output`, resolve a relative input and the output parent through the filesystem, then record a path relative to that parent; use an absolute path if Windows volumes/shares differ. Preserve supplied absolute input paths and supplied paths in stdout records. This makes generated file manifests directly verifiable from any cwd, including output through a symlinked directory. Shell redirection has no known manifest destination and cannot rebase records.
- Direct verification accepts hex case-insensitively or explicitly selected standard padded Base64. Validate digest length against the selected algorithm.
- `--check` is mutually exclusive with direct digest/input options. `--check -` reads the manifest from stdin.
- Parse GNU-style checksum records using the selected algorithm. Resolve relative file paths against the manifest’s directory, or cwd for a stdin manifest. Preserve absolute-path behavior.
- Process entries in order and continue after individual mismatches or file-read failures. Malformed records are reported as errors; an empty manifest is an error.
- Return `1` if any record is malformed, mismatched, or unreadable. Do not silently ignore missing files.
- Human output identifies the file or `stdin`/`text` source and its `ok`, `mismatch`, or `error` status. Never print the supplied text or expected/actual digests in verification results.
- `--quiet` suppresses stdout. `--json` returns one object containing ordered `results` and `errors`; results include source identification, status, and applicable stable error code, while manifest-level errors include the line number when available. Neither format contains raw input content or digests.

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

- `--in-place` explicitly authorizes replacement without a separate `--force`; `--force` affects ordinary `--output` replacement. An output filename `-` is a literal file; only input/manifest selectors treat `-` as stdin.
- `src/cli.rs` flattens private system and transformation command enums into one public command tree; `src/system.rs` owns OS-command dispatch. `src/runtime.rs` and `src/error.rs` retain OS signal/child-status and safe contextual-error behavior. `src/transform.rs` and `src/transform_error.rs` retain transformation cancellation/publication supervision and safe exit-code diagnostics. Only the selected command family installs its signal handler. A cancellable preparation worker validates semantic arguments before filesystem work and captures the time instant once. The transformation worker sends bounded chunks to an output worker; `src/transform.rs` alone owns transformation cancellation supervision and final publication. Workers cannot publish. Handled cancellation returns 1 without joining blocked workers; process exit closes remaining worker handles. No fixed input/output size limit is added.
- `src/publication.rs` owns same-directory temporary paths, no-clobber or replacing rename, final link/type revalidation, and permissions. Unix ownership, mode and access ACLs are retained; macOS empty ACLs are explicitly restored. Windows owner/group/DACL and access attributes must be retained. Permission preservation failures are fatal. Destination inspection does not open file contents: Linux uses an O_PATH handle and the held inode through `/proc/self/fd` for POSIX ACL xattrs (missing procfs fails before publication); macOS uses non-following metadata and ACL pathname APIs because O_EVTONLY still requires data access; Windows requests FILE_READ_ATTRIBUTES and READ_CONTROL only. No original-file removal, backup, identity comparison, locking, or multi-file transaction is introduced.
- Custom formats follow the pinned Chrono strftime grammar in fixed English/C locale. Input `%Z` and output `%#z` are unsupported; unknown directives fail. Custom date-only input defaults to midnight and missing time components default to zero. RFC 3339 parsing requires an explicit offset, rejects leap seconds and more than nine fractional digits. Its output retains the input fractional digit count (integer seconds/date: zero; milliseconds: three; current instant: nine). Historical IANA sub-minute offsets require custom output with `%::z`; RFC 3339 cannot represent them and fails rather than rounding.
- The timezone database is the pinned `chrono-tz` dependency's `IANA_TZDB_VERSION`; no OS database, `TZ`, `TZDIR`, or locale override participates in calculations. Dependency/database changes arrive with a clibox release.
- GNU untagged manifests accept binary and text markers but always hash raw bytes. LF/CRLF record separators are accepted. Malformed records, including blank/comment lines, are errors; no missing entry is skipped. A filename `-` inside a record is a relative file, not another stdin selector. Unix non-UTF-8 filenames round-trip as native bytes in checksum records and use escaped bytes in reports.
- Verification JSON has ordered `results` and `errors`. Each result contains `source` (`kind`: `file`, `stdin`, or `text`; file sources also have `path`), `status` (`ok`, `mismatch`, or `error`), and an optional stable `code`. Manifest errors contain `code` and an optional one-based `line`. No digests or text content are included. Human source names escape control characters.
- Complete reports, including manifest read/parse errors and per-entry failures, publish with exit 1. Cancellation and output failure do not publish incomplete file reports.

## Expanded validation

Run the root Rust suite and formatter check, `cargo test --locked -p clibox`, Cargo publication dry-run, npm launcher/distribution tests and installed npm/pnpm consumer smoke tests. CI runs the Rust command suite on Linux, macOS, and Windows and exercises the seven commands through the installed launcher for all eight existing native artifacts, including Alpine musl consumers. Fixtures cover parsing/redaction, Unicode and binary data, strict Base64 chunk boundaries, exact hash vectors, timezone/DST/precision behavior, ordered manifests, permissions/ACLs/links, failed publication, last-writer-wins, cancellation, and partial/broken stdout. Remove repository-owned generated `dist` directories afterward.
