# clibox Rust foundation

## Scope
`crates/clibox` owns the Rust executable and public crates.io package `clibox`.

## Runtime and Language
Rust 2021, MIT license, repository-pinned Rust toolchain, and `clap` argument parsing. It is an explicit Cargo workspace member and an approved crates.io publication target.

## Users and Operators
Developers invoking a pinned CLI, and maintainers building and releasing the same executable through Cargo and npm.

## Interfaces and Contracts
- `clibox`, `clibox --help`, and `clibox -h` print help to stdout and exit successfully.
- `clibox --version` and `clibox -V` print `clibox <Cargo package version>` and exit successfully.
- The seven text/time/Base64/hash commands and their common policies below implement issue #917.
- Invalid arguments produce redacted diagnostics on stderr and exit with code 2.
- There is no public Rust library API; issue #916 remains separate scope.

## Storage
No persistent settings, project configuration, caches, operation history, or telemetry. Input/output files and unpublished same-directory temporary files are the only new filesystem state.

## Security
Transformations run offline in Rust with current-user permissions, without delegated transformation tools, retries, authentication, services, or fixed timeouts. Output files use the permission/link/publication contract below. Cargo publication remains explicit and gated by the repository release coordinator.

## Logging
Help, version, transformed data, and verification reports are command output. Structured `tracing` diagnostics use stderr, default to warnings/errors, honor `RUST_LOG` and `NO_COLOR`, and use color only on a TTY. Parser, dependency, panic, and runtime errors use static safe messages and enum error codes; input, patterns, replacements, digests, argv, and paths are never logged. Dependency logging is excluded even when trace logging is enabled.

## Build and Test
- `cargo run -p clibox -- --help`
- `cargo test -p clibox`
- Root `cargo test` and `cargo fmt --all --check` remain required repository checks.
- `cargo publish -p clibox --dry-run` verifies standalone crate packaging.
- Process tests cover help, version, no arguments, malformed input, output streams, and exit codes. npm packaging also checks the native executable version against both source manifests.

## Dependencies and Integrations
Uses `clap`, `regex`, `chrono` with pinned `chrono-tz`, `base64`, `sha2`, and BLAKE3 with its exact-version pure-Rust configuration. Native access-permission adapters use libc/libSystem or Windows security APIs, without external transformation tools. npm distribution is owned by `packages/clibox`. The root cargo-mono tag allowlist includes `clibox` and releases use `clibox@v<version>`.

## Change Triggers
Update the project index, npm distribution contract, `crates/AGENTS.md`, and root release contracts when command behavior, naming, versions, platforms, or publication changes.

## References
- [Project index](project-clibox.md)
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
- Preserve existing access permissions when replacing a file; fail rather than silently discarding them.
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
- Preserve existing help/version, #916 interfaces, launcher argv/signal forwarding, exact version synchronization, script-free installation, eight target packages, and release boundaries.
- No service SLO, telemetry dashboard, or additional compliance gate is required. Rollback uses an earlier pinned package version and does not restore overwritten files.


## Implementation and compatibility decisions

- `--in-place` explicitly authorizes replacement without a separate `--force`; `--force` affects ordinary `--output` replacement. An output filename `-` is a literal file; only input/manifest selectors treat `-` as stdin.
- `src/cli.rs` owns enum-backed parser modes. A cancellable preparation worker validates semantic arguments before filesystem work and captures the time instant once. The transformation worker sends bounded chunks to an output worker; `src/runtime.rs` alone owns cancellation supervision and final publication. Workers cannot publish. Handled cancellation returns 1 without joining blocked workers; process exit closes remaining worker handles. No fixed input/output size limit is added.
- `src/publication.rs` owns same-directory temporary paths, no-clobber or replacing rename, final link/type revalidation, and permissions. Unix ownership, mode and access ACLs are retained; macOS empty ACLs are explicitly restored. Windows owner/group/DACL and access attributes must be retained. Permission preservation failures are fatal. No original-file removal, backup, identity comparison, locking, or multi-file transaction is introduced.
- Custom formats follow the pinned Chrono strftime grammar in fixed English/C locale. Input `%Z` and output `%#z` are unsupported; unknown directives fail. Custom date-only input defaults to midnight and missing time components default to zero. RFC 3339 parsing requires an explicit offset, rejects leap seconds and more than nine fractional digits. Its output retains the input fractional digit count (integer seconds/date: zero; milliseconds: three; current instant: nine). Historical IANA sub-minute offsets require custom output with `%::z`; RFC 3339 cannot represent them and fails rather than rounding.
- The timezone database is the pinned `chrono-tz` dependency's `IANA_TZDB_VERSION`; no OS database, `TZ`, `TZDIR`, or locale override participates in calculations. Dependency/database changes arrive with a clibox release.
- GNU untagged manifests accept binary and text markers but always hash raw bytes. LF/CRLF record separators are accepted. Malformed records, including blank/comment lines, are errors; no missing entry is skipped. A filename `-` inside a record is a relative file, not another stdin selector. Unix non-UTF-8 filenames round-trip as native bytes in checksum records and use escaped bytes in reports.
- Verification JSON has ordered `results` and `errors`. Each result contains `source` (`kind`: `file`, `stdin`, or `text`; file sources also have `path`), `status` (`ok`, `mismatch`, or `error`), and an optional stable `code`. Manifest errors contain `code` and an optional one-based `line`. No digests or text content are included. Human source names escape control characters.
- Complete reports, including manifest read/parse errors and per-entry failures, publish with exit 1. Cancellation and output failure do not publish incomplete file reports.

## Expanded validation

Run the root Rust suite and formatter check, `cargo test --locked -p clibox`, Cargo publication dry-run, npm launcher/distribution tests and installed npm/pnpm consumer smoke tests. CI runs the Rust command suite on Linux, macOS, and Windows and exercises the seven commands through the installed launcher for all eight existing native artifacts, including Alpine musl consumers. Fixtures cover parsing/redaction, Unicode and binary data, strict Base64 chunk boundaries, exact hash vectors, timezone/DST/precision behavior, ordered manifests, permissions/ACLs/links, failed publication, last-writer-wins, cancellation, and partial/broken stdout. Remove repository-owned generated `dist` directories afterward.
