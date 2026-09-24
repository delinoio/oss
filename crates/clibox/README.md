# clibox

[Documentation](https://oss.delino.io/clibox/) · [Command reference](https://oss.delino.io/clibox/commands)

A native Rust CLI distributed through native packages and `@delino/clibox` on npm for project-local version pinning.

Cross-platform utilities for child environments, local port owners, resource opening, the desktop text clipboard, text replacement, time formatting/arithmetic, Base64 encoding/decoding, checksum generation/verification, and TCP/HTTP/file readiness waits.

For JavaScript projects:

```sh
pnpm add -D -E @delino/clibox
pnpm exec clibox --help
pnpm exec clibox --version
```

The npm launcher requires Node.js 22 or newer. Prebuilt binaries cover macOS and Windows x64/arm64, and Linux x64/arm64 with glibc or musl. npm installation does not require Rust or installation scripts.

## Migrating older command syntax

The examples below use `run env`, which is implemented for the next minor release. Published version **0.1.6** uses **`env run`** instead; substitute that spelling in the environment examples and use `clibox env --help` for its command group. Version 0.1.6 already includes `port list`, `hash compute`, and the output/cancellation behavior described here. Update scripts when upgrading to the corresponding interface; rejected old names return exit code 2 with migration guidance and are not aliases.

| Previous use | New use |
| --- | --- |
| `clibox env run KEY=VALUE command` | `clibox run env KEY=VALUE command` |
| `clibox port which 3000` | `clibox port list 3000` |
| `clibox hash encode --text hello` | `clibox hash compute --text hello` |
| `clibox port which 3000 --quiet` (PIDs) | `clibox port list 3000 --pids` |
| `--output -` (a file named `-`) | `--output ./-` |

`--quiet` suppresses stdout results on port listing/termination, waits, and hash verification. It never suppresses failure diagnostics or changes the exit status. It conflicts with `--json`; `port list --pids` conflicts with both.

Omitted output and `--output -` both select stdout. `--force` requires an actual file output or `--in-place`; redundant `--in-place --force` is accepted. Remove standalone `--force` and do not combine it with stdout output. Checksum filename rebasing applies only when a manifest is written to an actual file.

Owned operations now return numeric **130** for Ctrl+C/Windows Ctrl+Break and **143** for Unix SIGTERM after cleanup, including text/Base64/hash/time operations that previously returned 1. `run env` continues to preserve its child's exit status and Unix termination signal. Completed effects are not undone.

Use `-h` for core rules and examples, or `--help` for those rules plus detailed constraints. Failures remain visible on stderr even with `RUST_LOG=off`.

## OS commands

```text
clibox run env [KEY=VALUE ...] [--] COMMAND [ARG ...]
clibox port list PORT... [--protocol tcp|udp|all] [--json | --quiet | --pids]
clibox port kill PORT... [--protocol tcp|udp|all] [--json | --quiet]
clibox open TARGET [--app APP] [--wait]
clibox clipboard copy [TEXT]
clibox clipboard paste
```

Use `--help` after any command for English help and examples. Root help (`clibox`, `--help`, or `-h`) also identifies the built version, Delino maintainer, repository, MIT license, and GitHub Issues support path; subcommand help stays focused on that command. Running `clibox` without arguments or using explicit `--help` prints help to stdout and returns exit code **0**. Running `clibox run`, `clibox port`, `clibox clipboard`, `clibox wait`, `clibox text`, `clibox time`, `clibox base64`, `clibox hash`, `clibox dotenv`, or `clibox yaml` without a subcommand prints that command's help to stderr and returns exit code **2**. Other invalid or missing arguments return exit code **2** with an error diagnostic; runtime failures return **1**. `run env` forwards the child program's exit status and supported termination signals.

### Run with environment variables

```sh
clibox run env NODE_ENV=production node build.js
clibox run env FIRST=one SECOND=two -- node script.js
```

Use this command in npm scripts to set a child environment across operating systems. It inherits the working directory, standard input/output/error, and parent environment. Assignments never change the calling shell. Duplicate assignments use the last value; empty values and empty child arguments are preserved. A child command is required.

Assignment escaping, variable references, PATH/NODE_PATH lists and platform-specific command conversion follow cross-env v10.1.0. Assignment references use the parent environment, not earlier assignments. Your invoking shell or JSON package script may apply its own quoting before clibox receives arguments. clibox preserves the literal quotes and backslashes in the resulting child command and arguments instead of unquoting them again; Windows variable conversion still applies. `--` explicitly ends the assignments. Windows supports npm `.cmd` commands resolved through the child PATH/PATHEXT; unsafe or unrepresentable batch arguments fail instead of becoming shell expressions. Invoke the underlying executable directly if its batch wrapper cannot represent an argument safely.

Environment execution has no shell-expression mode, dotenv loading or stored command preset. The child is awaited, and signal termination (including SIGINT) is not reported as success. Windows console interruption uses supported process-group CTRL_BREAK delivery.

On Windows, `run env` also treats `$1` as an environment-variable reference and removes it when unset. Invoke `clibox text replace` directly when passing regex capture references; wrapping it in `run env` applies that extra conversion even after shell quoting.

### Control local command execution

`clibox run` applies one local execution control to the same literal `[KEY=VALUE ...] [--] COMMAND [ARG ...]` workload grammar as `run env`. Wrapper options precede the workload; assignment expansion, literal argv, child-only environment, cwd, PATH/PATHEXT lookup, and no-shell behavior are unchanged.

```sh
clibox run with-rate-limit --name publish --limit 2 --period 1m -- npm publish
clibox run with-lock --name migrate --on-locked fail -- pnpm migrate
clibox run with-service http://127.0.0.1:3000/health -- npm test
clibox run with-service http://127.0.0.1:3000/health --service node server.js -- npm test
clibox run with-retry --max-attempts 5 --jitter none -- cargo fetch
clibox run with-timeout --timeout 10m --idle-timeout 30s -- npm test
```

Rate limits require `--name`, `--limit`, and `--period`; `--burst` defaults to one and is at most `9007199254740992`. A named local token bucket admits one workload per token, preserves its configuration, and does not refund a token after a failed spawn/cancellation. `--wait-timeout` bounds admission; `0` is immediate. Refill uses UTC accounting, never adds tokens on a backward clock movement, and caps forward refill at burst capacity.

Locks use the same name/scope rules. They wait by default; `--on-locked skip` returns 0 and `--on-locked fail` returns 75 without starting a workload. `--wait-timeout` is valid only for `wait`. Same-key nested locks are ordinary contention rather than reentrant bypasses.

Names are case-sensitive ASCII letters, digits, dots, underscores, or hyphens, up to 128 characters. Scope is the canonical current project directory by default; `--scope user` shares a current-user machine-local name, while `--project-dir DIR` selects another existing identity without changing cwd and cannot combine with user scope. Linux state is `$XDG_STATE_HOME/clibox/run` or `~/.local/state/clibox/run`, macOS state is `~/Library/Application Support/clibox/run`, and Windows state is LocalAppData `clibox/run`. Names and project paths are hashed before storage. State is local and retained; remove affected state manually only after all relevant wrappers stop, and never relax its private permissions or replace it with links.

`with-service` uses headers-only HTTP readiness: GET/any 2xx by default, or HEAD/exact `--status`. It has no redirects, proxies, authentication, body reads, custom CAs, or TLS bypass. Without `--service`, clibox polls the external endpoint until ready and never stops it. With it, clibox rejects an already-ready endpoint, starts after a retryable not-ready preflight, sends service output to stderr, and owns service cleanup. The first standalone `--` after `--service` separates service and workload; another standalone separator inside service argv is unsupported. `--ready-timeout 0` disables its overall readiness deadline; polling and individual HTTP attempts remain bounded by their positive intervals.

`with-retry` defaults to three attempts, 1 s delay, factor 2, 30 s cap, and full jitter. It retries nonzero numeric exits (or only repeated `--retry-exit-code` values), never replays consumed stdin, and does not retry spawn errors or Unix signal termination. `with-timeout` requires a positive total or idle limit; any stdout/stderr bytes reset idle timing and are forwarded promptly, though TTY identity is not guaranteed. A wrapper stops owned work and returns a runtime failure if it cannot forward an owned stream. Every wrapper has `--kill-after DURATION` (default `5s`); it requests graceful owned-tree termination, then forces after the grace period and waits up to five seconds for confirmation. On Unix, run workloads and managed services in the foreground: programs that daemonize or create a new session or process group leave wrapper ownership and must manage their own lifecycle.

Natural child status and Unix signal identity are preserved. Wrapper timeouts return 124, invalid arguments return 2, other wrapper failures return 1, Ctrl+C/Ctrl+Break returns 130, and Unix SIGTERM returns 143. On Unix, the npm launcher forwards SIGINT, SIGTERM, and SIGHUP to the native command, including PID-targeted signals. Diagnostics are redacted stderr logs and never include wrapper names, argv, paths, URLs, environment values, credentials, or HTTP content.

### Inspect and terminate port owners

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

clibox hash compute [--algorithm sha256|sha512|blake3]
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

Results default to stdout. Text and Base64 add no newline; time values, generated hashes, and verification reports end with LF. `--output FILE` writes the result only to that file. `--output -` explicitly selects stdout; use `--output ./-` for a literal dash filename. `--force` requires an actual file output or `--in-place`; standalone force and force with stdout are argument errors.

Exit status is `0` for success, `1` for runtime failure or checksum mismatch, `2` for missing, malformed, or conflicting arguments, `130` for Ctrl+C/Windows Ctrl+Break, and `143` for Unix SIGTERM. Diagnostics use stderr. `RUST_LOG` enables more detailed structured diagnostics; the default is warnings/errors. Color requires a TTY and is disabled by `NO_COLOR`. Diagnostics omit input content, digests, patterns, replacements, raw arguments, and paths. Verification filenames are intentional command results.

Operations are offline and use current OS permissions. No settings, cache, history, telemetry, automatic retries, or fixed execution timeout is added. No fixed input/output size limit is imposed: text replacement holds the entire input/result in memory, while Base64 and hashing stream bytes. Resource exhaustion can fail an operation. **Streaming stdout may already contain partial output when reading, decoding, writing, or interruption fails.** Use `--output` when an incomplete result must not replace a file.

### File replacement

Output is prepared in a temporary file beside its destination and published only after processing succeeds. Existing output requires `--force`. Replacement does not require reading the existing file contents; metadata and security information must remain accessible. `--in-place` still needs read access to its input. Access permissions, including supported native ACLs and ownership, are preserved on replacement; inability to preserve them fails instead of silently discarding them. Symbolic-link and multiply-linked replacement destinations are rejected. On Windows, replacing a read-only file requires attribute-write and replacement permission and preserves its read-only status. Unsupported filesystem replacement capabilities fail without changing the original.

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
    "checksum": "clibox hash compute --input \"path with spaces.zip\""
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

When checksum generation uses `--output`, relative input paths are resolved and rebased against the output directory so the saved manifest verifies from any working directory. For example, `--input archive.zip --output checksums/SHA256SUMS` records `../archive.zip`. Different Windows volumes use an absolute path. Absolute input paths and stdout records retain the supplied filename; use `--output` when saving into another directory because shell redirection cannot rebase filenames.

Direct verification accepts case-insensitive hex or explicit standard padded Base64 and checks digest length against the selected algorithm. `--check` conflicts with the direct digest, input, and format options. `--check -` reads a manifest from stdin. Relative filenames resolve against the manifest's directory, or cwd for a stdin manifest; absolute paths retain their usual meaning. A record's filename `-` means a literal file. Both GNU text/binary markers hash raw bytes. LF/CRLF manifest separators are accepted; malformed records, including blank/comment lines, are errors. An empty manifest fails.

Entries are processed in order and continue after mismatches and file-read failures. Missing files are errors. Statuses are `ok`, `mismatch`, and `error`; neither human nor JSON reports include supplied text or expected/actual digests. `--quiet` suppresses results and conflicts with `--json` and `--output`. Any mismatch or error returns 1 and emits a static redacted stderr summary, including with `--quiet` or `RUST_LOG=off`, while a completed report is still published to file `--output`.

```sh
clibox hash compute --input 'archive.zip' --format checksum --output SHA256SUMS
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

## Utility diagnostics and operation limits

All operations use the current OS user's permissions and desktop session. No authentication service, saved configuration, cache, operation history or telemetry is added. Environment, port, open, clipboard, and transformation commands have no automatic retry or fixed execution timeout apart from the shared five-second port-termination verification. Readiness waits use their polling/deadline options above. Owned operations return numeric 130 for Ctrl+C/Windows Ctrl+Break and 143 for Unix SIGTERM after cleanup. `run env` preserves the delegated child's exit status and Unix signal identity. Interruption does not undo completed copies, terminations, application launches or file replacements.

Configuration input/output and nesting limits are documented below.

Warnings/errors use structured stderr diagnostics. Set `RUST_LOG=debug` for more detail. Stdout remains dedicated to results or the delegated child's output. Color is used only on a TTY and respects `NO_COLOR`. Clibox-authored diagnostics omit clipboard text, environment values, transformation input, patterns, replacements, digests, complete argv, full URLs and paths; a child program still controls its own inherited output.

For port permission errors, inspect the returned partial results and use the appropriate user/session permissions; clibox does not elevate privileges. Clipboard access needs a reachable desktop session, its normal display authorization and the listed installed tools. A busy Windows clipboard or a Wayland compositor without the required capability produces an actionable failure. Open failures may indicate missing applications, URI associations, Linux xdg-utils or desktop access. Errors after dispatch do not guarantee that nothing opened.

Automated parser, process, mocked OS-adapter and package tests cover these contracts. Real GUI behavior, desktop clipboard persistence and application-wait verification remain follow-up validation; mocked coverage does not establish those desktop observations. To roll back, install an earlier exact package version. Previous OS effects are not undone.

## Configuration commands

```text
clibox dotenv list [--input FILE] [--output FILE] [--force]
clibox dotenv merge FILE... [--output FILE] [--force]
clibox yaml normalize [--input FILE] [--output FILE | --in-place] [--force]
```

### List dotenv keys

```sh
clibox dotenv list
clibox dotenv list --input local.env
clibox dotenv list --input - < local.env
clibox dotenv list --output keys.txt
```

The default is `.env` in the current directory only. Clibox does not search parents or automatically read related files. Every record is validated; each unique key appears once in ascending case-sensitive lexical order. Values are never printed by `dotenv list`.

### Merge dotenv files

```sh
clibox dotenv merge base.env local.env
clibox dotenv merge base.env - --output merged.env < overrides.env
clibox dotenv merge base.env local.env --output local.env --force
```

Supply at least one input. Inputs are read in order; `-` may occur once to read stdin at that position. The last assignment within each file wins, and later inputs override earlier inputs, including explicit empty values. Malformed records fail even if a later assignment would overwrite them. All inputs are read before any file replacement.

The [Node.js dotenv syntax](https://nodejs.org/api/environment_variables.html#dotenv) is the baseline: keys match `[A-Za-z_][A-Za-z0-9_]*`, with an optional `export` prefix, assignment whitespace, comments, empty values, and multiline single- or double-quoted values. Invalid keys, missing `=`, unterminated quotes, and unexpected content following quoted values are errors.

The `export` prefix is recognized only when followed immediately by an ASCII space and another assignment key, as in `export NAME=value`. Tabs may follow that initial space, but `export<TAB>NAME=value` is invalid. The word `export` can itself be a key: `export=value`, `export =value`, and `export<TAB>=value` are ordinary assignments and merge to `export=value`. Here, `<TAB>` denotes an actual tab character.

Merging emits sorted `KEY=<winning value token>` records. It removes the optional `export` prefix, assignment whitespace, comments outside values, and unrelated blank lines. Quotes, escapes, quoted internal line endings, and the original winning value representation are preserved without decoding/re-encoding. `$VAR`, `${VAR}`, and command substitutions remain literal: no process environment is loaded into results and no shell code is executed.

For `base.env` containing `B=base` and `A="keep ${HOME}"`, and `local.env` containing `B=local`, the result is:

```dotenv
A="keep ${HOME}"
B=local
```

### Normalize YAML

```sh
clibox yaml normalize < config.yaml
clibox yaml normalize --input - < config.yaml
clibox yaml normalize --input config.yaml --output normalized.yaml
clibox yaml normalize --input config.yaml --in-place
```

The default input is stdin. YAML normalization supports [YAML 1.2 Core](https://yaml.org/spec/1.2.2/#103-core-schema) nulls, booleans, numeric values, strings, sequences, and string-key mappings, plus anchors, aliases, multiple documents, and the [merge-key extension](https://yaml.org/type/merge.html). Numeric precision is preserved, including integers and decimal/exponent values larger than machine numeric types; floats may carry an explicit `!!float` tag.

Aliases expand into independent output values. Merges are shallow: directly specified keys win, and earlier mappings in a merge sequence win over later ones. Quoted or explicitly string-tagged `<<` keys remain ordinary keys. Anchors are scoped to one document. Duplicate ordinary keys, invalid merge operands, unresolved/cyclic references, non-string keys, unsupported tags, and version directives other than 1.2 are rejected. Quote keys that look like numbers, booleans, or null.

Mapping keys sort recursively by Unicode scalar value, without case folding or Unicode normalization. Scalar values/types, sequence order, and document order are preserved. Comments, anchors, and source formatting are removed. Output uses deterministic block formatting with two-space indentation and quoted/escaped strings. Normalizing it again produces identical bytes. One document has no initial `---`; multiple documents each start with `---`. Explicit empty documents become `null`.

For example:

```yaml
base: &base {z: 2, a: 1}
copy: {<<: *base, z: 3}
```

becomes:

```yaml
"base":
  "a": 1
  "z": 2
"copy":
  "a": 1
  "z": 3
```

### Input limits and output safety

All inputs require valid UTF-8, permit one initial UTF-8 BOM, and reject NUL bytes. Aggregate raw input across files and serialized output each have an independent **64 MiB** limit. YAML collections are limited to **128 nesting levels**, counting a root mapping/sequence as level one, including expanded references. These limits cannot be adjusted. Invalid input or exceeded limits produces no stdout result and does not replace the destination.

Relative paths resolve against the invocation's current directory. Explicit file input does not consume stdin. Results default to stdout; `--output FILE` writes only to that filesystem path, with no duplicate stdout result. `--output -` also selects stdout; use `--output ./-` for a file literally called `-`. `--force` requires real file output or `--in-place`; redundant `--in-place --force` is accepted. Empty/comment-only input succeeds with zero output bytes. Generated boundaries use LF and nonempty output ends with LF; quoted dotenv internal line endings remain unchanged.

An existing output requires `--force`. `--in-place` requires an explicitly selected regular YAML input file, conflicts with `--output`, and authorizes replacing the input without `--force`. `--force` requires a file-output operation. Ordinary reads may follow symlinks, but in-place inputs and replacement destinations cannot be symlinks; multiply-linked replacement destinations are also rejected. Input files otherwise remain untouched.

Clibox prepares a temporary file on the destination filesystem and publishes only after processing succeeds. Unix staging stays inside a private directory under the destination directory, so replacement permissions cannot expose unpublished bytes. New Unix output/temporary files use mode `0600`; new Windows files inherit the parent directory's ACL. Replacements preserve existing access permissions and fail if preservation is impossible. Unpublished temporary files are cleaned up on handled failures/cancellation. No backups, locks, or concurrent-change detection are provided: the last successful replacement wins. Windows sharing rules may reject overlapping replacements; wait for competing writers or blocking file handles to finish before retrying. Completed writes are not undone by cancellation or by pinning an earlier package version.

A write failure or interruption **while emitting stdout can leave partial output**. File publication and stdout streaming have different failure boundaries. Clibox supports interruption during reading, processing, and output; it has no automatic retries or fixed execution timeout. It runs offline with your current OS permissions and stores no configuration, cache, or history.

### Exit codes and diagnostics

On Windows, Ctrl+C or Ctrl+Break lets the native command finish cancellation cleanup before the npm/pnpm launcher returns its exit code.

| Outcome | Exit code |
|---|---:|
| Success | 0 |
| Content, filesystem, resource-limit, or other runtime failure | 1 |
| Missing, malformed, or conflicting CLI arguments | 2 |
| Handled Ctrl+C | 130 |
| Handled Unix SIGTERM | 143 |

English structured diagnostics go to stderr, with warnings/errors enabled by default. Use `RUST_LOG=clibox=debug` for operation progress (or `RUST_LOG` filters for more detail). Diagnostic color is used only on a terminal and respects `NO_COLOR`. Diagnostics omit input content, keys, values, paths, raw arguments, and dependency error text; intentional command results are separate.

For syntax errors, inspect the reported input/document ordinal and line/column in your local input. For file errors, check access permissions, the destination's link status, and free space. For limit errors, reduce the input, nesting, or expanded YAML result. Argument errors intentionally omit supplied values: use the command's `--help` to check syntax. Share redacted diagnostics when requesting support; avoid sharing secret configuration values.

Licensed under MIT.

## Linux APT and DNF

Native packages are not published yet. After the first native package release, register the stable repository using the [Linux package setup guide](https://oss.delino.io/linux-packages), including its key fingerprint check. Then install with `sudo apt-get install clibox` or `sudo dnf install clibox` and check `clibox --version`. Native installation does not require Node.js. Update with `sudo apt-get install --only-upgrade clibox` or `sudo dnf upgrade clibox`; remove with `sudo apt-get remove clibox` or `sudo dnf remove clibox`. Desktop helpers remain separately installed runtime capabilities.
