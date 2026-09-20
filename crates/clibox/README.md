# clibox

A native Rust CLI, also available as `@delino/clibox` on npm for project-local version pinning.

Cross-platform utilities for child environments, local port owners, resource opening, the desktop text clipboard, text replacement, time formatting/arithmetic, Base64 encoding/decoding, and checksum generation/verification.

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

## OS commands

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

## Text, time, Base64, and hash commands

```text
clibox text replace PATTERN REPLACEMENT
  [--regex] [--first] [--require-match]
  [--input FILE | --text TEXT] [--output FILE | --in-place] [--force]

clibox time format [VALUE]
  [--from rfc3339|date|unix-s|unix-ms | --input-format FORMAT]
  [--to rfc3339|unix-s|unix-ms | --format FORMAT] [--timezone ZONE]

clibox time add [VALUE]
  [--from rfc3339|date|unix-s|unix-ms | --input-format FORMAT]
  [--to rfc3339|unix-s|unix-ms | --format FORMAT] [--timezone ZONE]
  [--years N] [--months N] [--weeks N] [--days N]
  [--hours N] [--minutes N] [--seconds N]

clibox base64 encode|decode
  [--input FILE | --text TEXT] [--url-safe] [--no-padding]
  [--output FILE] [--force]

clibox hash encode [--algorithm sha256|sha512|blake3]
  [--input FILE | --text TEXT] [--format hex|base64|checksum]
  [--output FILE] [--force]

clibox hash verify EXPECTED [--algorithm sha256|sha512|blake3]
  [--input FILE | --text TEXT] [--format hex|base64]
  [--quiet | --json] [--output FILE] [--force]

clibox hash verify --check CHECKSUM_FILE [--algorithm sha256|sha512|blake3]
  [--quiet | --json] [--output FILE] [--force]
```

Use `clibox <command> <operation> --help` for command-specific help. `clibox`, `-h`/`--help`, and `-V`/`--version` remain available.

### Input, output, and exit codes

File and string selectors are mutually exclusive. With neither, clibox reads stdin until EOF; `--input -` explicitly selects stdin. Explicit file/string input does not consume stdin. `--text` supplies its exact UTF-8 bytes without an implicit newline. Base64 and hashes accept arbitrary binary file/stdin input. Text replacement requires valid UTF-8.

Results default to stdout. Text and Base64 add no newline; time values, generated hashes, and verification reports end with LF. `--output FILE` writes the result only to that file. An output filename `-` is a literal file, not stdout.

Exit status is `0` for success, `1` for runtime failure, checksum mismatch, or handled interruption, and `2` for missing, malformed, or conflicting arguments. Diagnostics use stderr. `RUST_LOG` enables more detailed structured diagnostics; the default is warnings/errors. Color requires a TTY and is disabled by `NO_COLOR`. Diagnostics omit input content, digests, patterns, replacements, raw arguments, and paths. Verification filenames are intentional command results.

Operations are offline and use current OS permissions. No settings, cache, history, telemetry, automatic retries, or fixed execution timeout is added. No fixed input/output size limit is imposed: text replacement holds the entire input/result in memory, while Base64 and hashing stream bytes. Resource exhaustion can fail an operation. **Streaming stdout may already contain partial output when reading, decoding, writing, or interruption fails.** Use `--output` when an incomplete result must not replace a file.

### File replacement

Output is prepared in a temporary file beside its destination and published only after processing succeeds. Existing output requires `--force`. Replacement does not require reading the existing file contents; metadata and security information must remain accessible. `--in-place` still needs read access to its input. Access permissions, including supported native ACLs and ownership, are preserved on replacement; inability to preserve them fails instead of silently discarding them. Symbolic-link and multiply-linked replacement destinations are rejected.

`text replace --in-place` requires one explicitly selected regular input file, conflicts with `--text` and `--output`, and itself authorizes replacement without `--force`. Invalid UTF-8, invalid patterns/references, and required-match failures leave the original intact. Unpublished temporary files are cleaned up after handled failures/interruption. Completed replacements are not undone.

There are no automatic backups, file locks, or concurrent-modification checks. The last successful replacement wins; atomic publication does not prevent lost updates. Pin an earlier clibox package version to roll back command behavior; this does not restore overwritten files.

### Text replacement

Default replacement is case-sensitive, literal, and replaces every non-overlapping match. Replacement text is literal unless `--regex` is selected. `--first` replaces only the first match in the entire input. `--require-match` fails when no match exists; otherwise unchanged input succeeds. Empty replacement is allowed, but empty search patterns are rejected.

Regex mode uses the [Rust regex syntax](https://docs.rs/regex/latest/regex/#syntax), including inline flags, numbered `$1` and named `${name}` references. `$$` inserts a literal dollar. References must name existing capture groups; unmatched optional groups expand to empty text. Explicit zero-width expressions such as `^` are supported. Lookaround and pattern backreferences are not supported. Untouched Unicode, whitespace, and LF/CRLF line endings are preserved.

Git Bash examples keep captures and paths quoted so the shell does not expand them:

```sh
clibox text replace old new --input 'path with spaces.txt' --in-place
clibox text replace '(hello)' '$1 world' --regex --text hello
clibox text replace '(?<word>hello)' '${word} world' --regex --text hello
```

For an npm script, double quotes inside JSON need escaping. The escaped dollar below protects `$1` in POSIX shells and Git Bash; it is shell quoting, not a regex escape:

```json
{
  "scripts": {
    "rewrite": "clibox text replace \"(hello)\" \"\\$1 world\" --regex --input \"path with spaces.txt\" --in-place",
    "checksum": "clibox hash encode --input \"path with spaces.zip\""
  }
}
```

Windows npm defaults to `cmd.exe`, where `$1` is already literal; use `"$1 world"` as the shell argument without the POSIX backslash (escaped double quotes are still required in JSON). For arguments beginning with `-`, use `--option=value` or `--` before positional arguments as appropriate.

### Time formatting and arithmetic

The default input/output format is RFC 3339 and the default processing/output timezone is UTC. Omitting `VALUE` captures the current instant once; time commands do not read stdin. Signed integer timestamps require explicit `--from unix-s` or `--from unix-ms`; units are never guessed. `--from date` accepts `YYYY-MM-DD` at midnight.

Custom formats use the platform-independent [Chrono strftime dialect](https://docs.rs/chrono/latest/chrono/format/strftime/index.html) in fixed English/C locale. For example, `%F` is a date, `%T` is a time, `%.f` is fractional seconds, and `%:z` is an offset. Input `%Z` is rejected because timezone abbreviations are ambiguous; output `%#z` is unsupported. Unknown directives fail. A custom date without time defaults to midnight; omitted time components default to zero.

Explicit input offsets establish the instant. Offset-free civil input uses `--timezone`, which also selects the zone for calendar arithmetic and output. UTC and bundled IANA names such as `America/New_York` and `Asia/Seoul` are supported. Every platform artifact of a clibox version uses the same bundled database, independent of the OS timezone database, `TZ`, `TZDIR`, or locale. Timezone rule updates arrive through clibox releases.

Years 1–9999 and up to nanosecond precision are supported. Leap seconds, invalid dates, unknown zones, unsupported directives, and out-of-range results fail. RFC 3339 output preserves the input fractional digit count; Unix seconds/date inputs have none, milliseconds have three, and the captured current instant has nine. Explicit coarser formats may discard precision. Integer Unix output uses mathematical floor even before the epoch. Historical zones with sub-minute offsets require a custom output containing `%::z`; RFC 3339 output fails instead of rounding the offset.

`time add` requires at least one signed integer unit; explicit zero is valid. It combines years/months, clamps to the destination month's final day, applies combined calendar weeks/days, then adds hours/minutes/seconds as elapsed time. Nonexistent or ambiguous local times during parsing or calendar changes fail; clibox does not shift them or choose an occurrence. An explicit input offset can disambiguate an instant. No system clock changes are made.

```sh
clibox time format -1 --from unix-ms --to unix-s
# -1
clibox time add 2024-01-31 --from date --months 1
# 2024-02-29T00:00:00Z
clibox time add 2024-03-09T12:00:00-05:00 --timezone America/New_York --days 1
# 2024-03-10T12:00:00-04:00
clibox time add 2024-03-09T12:00:00-05:00 --timezone America/New_York --hours 24
# 2024-03-10T13:00:00-04:00
```

### Base64

The default is standard alphabet with padding. `--url-safe` selects the URL-safe alphabet; `--no-padding` explicitly selects unpadded encoding/decoding. Alphabets and padding modes are never autodetected or mixed. Encoding produces one unwrapped stream with no extra newline. Decoding ignores ASCII whitespace only and rejects other invalid characters, invalid padding, incomplete encodings, and noncanonical trailing bits. Empty input succeeds. Decoded bytes are not converted to UTF-8 or newline-normalized.

```sh
clibox base64 encode --text hello
clibox base64 encode --input image.png --url-safe --no-padding --output image.b64
clibox base64 decode --input image.b64 --url-safe --no-padding --output recovered.png
```

### Hashes and verification

Algorithms are SHA-256 (default), SHA-512, and fixed 256-bit BLAKE3. Hashes cover exact bytes without newline or text normalization. Encoding defaults to lowercase hex; Base64 uses the standard padded alphabet. `--format checksum` requires a file input and emits a GNU-style untagged record with the binary `*` marker, escaping backslashes, newlines, and carriage returns in filenames. The algorithm is selected explicitly, never inferred from a record.

Direct verification accepts case-insensitive hex or explicit standard padded Base64 and checks digest length against the selected algorithm. `--check` conflicts with the direct digest, input, and format options. `--check -` reads a manifest from stdin. Relative filenames resolve against the manifest's directory, or cwd for a stdin manifest; absolute paths retain their usual meaning. A record's filename `-` means a literal file. Both GNU text/binary markers hash raw bytes. LF/CRLF manifest separators are accepted; malformed records, including blank/comment lines, are errors. An empty manifest fails.

Entries are processed in order and continue after mismatches and file-read failures. Missing files are errors. Statuses are `ok`, `mismatch`, and `error`; neither human nor JSON reports include supplied text or expected/actual digests. `--quiet` suppresses results and conflicts with `--json` and `--output`. Any mismatch or error returns 1, while a completed report is still published to `--output`.

```sh
clibox hash encode --input 'archive.zip' --format checksum --output SHA256SUMS
clibox hash verify --check SHA256SUMS
clibox hash verify --check SHA256SUMS --json --output verification.json
```

JSON has ordered `results` and `errors` arrays. A result contains a source (`kind`: `file`, `stdin`, or `text`; files also have `path`), a status, and an optional stable error code. Manifest errors include a code and a one-based line number when available:

```json
{"results":[{"source":{"kind":"file","path":"archive.zip"},"status":"error","code":"read-failed"}],"errors":[{"code":"malformed-record","line":2}]}
```

Human filenames escape control characters. Unix non-UTF-8 filenames use byte escapes in reports while checksum records preserve their native bytes.

### Command troubleshooting

- Invalid-argument errors intentionally omit your values; compare options with command help. Check explicit timestamp units, regex references, or the selected digest encoding/length.
- For DST errors, choose a valid calendar time or supply an explicit offset when parsing an ambiguous instant.
- For file failures, check access permissions, free space, links, and other applications holding the destination open. Use `--force` only when replacement is intended; retain your own backups when needed.
- Check exit status before consuming a streaming result. An interrupted or failed stdout stream can be incomplete, whereas a completed verification report can validly accompany exit code 1.

## Diagnostics and operation limits

All operations use the current OS user's permissions and desktop session. No authentication service, saved configuration, cache, operation history or telemetry is added. Apart from the shared five-second port-termination verification, there is no fixed execution timeout or automatic retry. OS-command interruption preserves supported termination signals, while handled transformation interruption returns exit code 1. Interruption does not undo completed copies, terminations, application launches or file replacements.

Warnings/errors use structured stderr diagnostics. Set `RUST_LOG=debug` for more detail. Stdout remains dedicated to results or the delegated child's output. Color is used only on a TTY and respects `NO_COLOR`. Clibox-authored diagnostics omit clipboard text, environment values, transformation input, patterns, replacements, digests, complete argv, full URLs and paths; a child program still controls its own inherited output.

For port permission errors, inspect the returned partial results and use the appropriate user/session permissions; clibox does not elevate privileges. Clipboard access needs a reachable desktop session, its normal display authorization and the listed installed tools. A busy Windows clipboard or a Wayland compositor without the required capability produces an actionable failure. Open failures may indicate missing applications, URI associations, Linux xdg-utils or desktop access. Errors after dispatch do not guarantee that nothing opened.

Automated parser, process, mocked OS-adapter and package tests cover these contracts. Real GUI behavior, desktop clipboard persistence and application-wait verification remain follow-up validation; mocked coverage does not establish those desktop observations. To roll back, install an earlier exact package version. Previous OS effects are not undone.

Licensed under MIT.
