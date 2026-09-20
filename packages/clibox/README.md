# @delino/clibox

Run the native `clibox` CLI with a version pinned in your JavaScript project's package manifest and lockfile.

```sh
pnpm add -D -E @delino/clibox
pnpm exec clibox --help
pnpm exec clibox --version
```

With npm:

```sh
npm install --save-dev --save-exact @delino/clibox
npm exec -- clibox --version
```

Use `clibox` commands in `package.json` scripts, for example `clibox run env NODE_ENV=production node build.js`.

## Requirements

- Node.js 22 or newer.
- macOS or Windows (x64 or arm64), or Linux (x64 or arm64, glibc or musl). GNU builds require glibc 2.35+ on x64 and 2.39+ on arm64; Alpine uses the musl builds.
- Optional dependencies enabled in your package manager.

The package manager installs the matching prebuilt executable. Rust, postinstall scripts, and separate binary downloads are not required. You can install with `--ignore-scripts`.

## Troubleshooting

If the native package is missing, reinstall with optional dependencies enabled (`npm install --include=optional` or `pnpm install` without `--no-optional`). Do not copy `node_modules` between operating systems, architectures, or Linux libc environments; reinstall from your lockfile on the destination machine. If a lockfile omits the destination's optional package, regenerate it with your package manager and commit the corrected lockfile.

If versions disagree, reinstall the dependency so `@delino/clibox` and its selected platform package have the same exact version. Cargo installation with `cargo install clibox` requires a compatible Rust toolchain on a supported macOS, Windows or Linux host.

The package provides the `clibox` command only, with no public JavaScript import API.

## System utilities

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

Environment execution has no shell-expression mode, dotenv loading or stored command preset. The child is awaited, and signal termination (including SIGINT) is not reported as success. Windows console interruption uses supported process-group CTRL_BREAK delivery.

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

### Utility diagnostics and operation limits

These utilities use the current OS user's permissions and desktop session. No authentication service, saved configuration, cache, operation history or telemetry is added. Apart from the shared five-second port-termination verification, there is no fixed execution timeout or automatic retry. Interrupting a command does not undo completed copies, terminations or application launches.

Warnings/errors use structured stderr diagnostics. Set `RUST_LOG=debug` for more detail. Stdout remains dedicated to results or the delegated child's output. Color is used only on a TTY and respects `NO_COLOR`. Clibox-authored diagnostics omit clipboard text, environment values, complete argv, full URLs and paths; a child program still controls its own inherited output.

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

The [Node.js dotenv syntax](https://nodejs.org/api/environment_variables.html#dotenv) is the baseline: keys match `[A-Za-z_][A-Za-z0-9_]*`, with optional `export`, assignment whitespace, comments, empty values, and multiline single- or double-quoted values. Invalid keys, missing `=`, unterminated quotes, and unexpected content following quoted values are errors.

Merging emits sorted `KEY=<winning value token>` records. It removes `export`, assignment whitespace, comments outside values, and unrelated blank lines. Quotes, escapes, quoted internal line endings, and the original winning value representation are preserved without decoding/re-encoding. `$VAR`, `${VAR}`, and command substitutions remain literal: no process environment is loaded into results and no shell code is executed.

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

Relative paths resolve against the invocation's current directory. Explicit file input does not consume stdin. Results default to stdout; `--output FILE` writes only to that filesystem path, with no duplicate stdout result. `--output -` names a file literally called `-`; use shell redirection for stdout. Empty/comment-only input succeeds with zero output bytes. Generated boundaries use LF and nonempty output ends with LF; quoted dotenv internal line endings remain unchanged.

An existing output requires `--force`. `--in-place` requires an explicitly selected regular YAML input file, conflicts with `--output`, and authorizes replacing the input without `--force`. `--force` requires a file-output operation. Ordinary reads may follow symlinks, but in-place inputs and replacement destinations cannot be symlinks; multiply-linked replacement destinations are also rejected. Input files otherwise remain untouched.

Clibox prepares a temporary file on the destination filesystem and publishes only after processing succeeds. Unix staging stays inside a private directory under the destination directory, so replacement permissions cannot expose unpublished bytes. New Unix output/temporary files use mode `0600`; new Windows files inherit the parent directory's ACL. Replacements preserve existing access permissions and fail if preservation is impossible. Unpublished temporary files are cleaned up on handled failures/cancellation. No backups, locks, or concurrent-change detection are provided: the last successful replacement wins. Completed writes are not undone by cancellation or by pinning an earlier package version.

A write failure or interruption **while emitting stdout can leave partial output**. File publication and stdout streaming have different failure boundaries. Clibox supports interruption during reading, processing, and output; it has no automatic retries or fixed execution timeout. It runs offline with your current OS permissions and stores no configuration, cache, or history.

### Exit codes and diagnostics

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
