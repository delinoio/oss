# Text, time, Base64, and hashes

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

See [Input, output, and file replacement](/clibox/output) before writing files.

## Text replacement

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

## Time formatting and arithmetic

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

## Base64

The default is standard alphabet with padding. `--url-safe` selects the URL-safe alphabet; `--no-padding` explicitly selects unpadded encoding/decoding. Alphabets and padding modes are never autodetected or mixed. Encoding produces one unwrapped stream with no extra newline. Decoding ignores ASCII whitespace only and rejects other invalid characters, invalid padding, incomplete encodings, and noncanonical trailing bits. Empty input succeeds. Decoded bytes are not converted to UTF-8 or newline-normalized.

```sh
clibox base64 encode --text hello
clibox base64 encode --input image.png --url-safe --no-padding --output image.b64
clibox base64 decode --input image.b64 --url-safe --no-padding --output recovered.png
```

## Hashes and verification

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

See [Troubleshooting](/clibox/troubleshooting) for argument, time, and file failures.
