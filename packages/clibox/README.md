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

You can also add clibox commands to `package.json` scripts. The CLI provides dotenv listing/merging and YAML normalization, plus help and version output.

## Requirements

- Node.js 22 or newer.
- macOS or Windows (x64 or arm64), or Linux (x64 or arm64, glibc or musl). GNU builds require glibc 2.35+ on x64 and 2.39+ on arm64; Alpine uses the musl builds.
- Optional dependencies enabled in your package manager.

The package manager installs the matching prebuilt executable. Rust, postinstall scripts, and separate binary downloads are not required. You can install with `--ignore-scripts`.

## Troubleshooting

If the native package is missing, reinstall with optional dependencies enabled (`npm install --include=optional` or `pnpm install` without `--no-optional`). Do not copy `node_modules` between operating systems, architectures, or Linux libc environments; reinstall from your lockfile on the destination machine. If a lockfile omits the destination's optional package, regenerate it with your package manager and commit the corrected lockfile.

If versions disagree, reinstall the dependency so `@delino/clibox` and its selected platform package have the same exact version. Unsupported platforms can use `cargo install clibox` where the Rust toolchain supports the host.

The package provides the `clibox` command only, with no public JavaScript import API.

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

Clibox prepares a temporary file in the destination directory and publishes only after processing succeeds. New Unix output/temporary files use mode `0600`; new Windows files inherit the parent directory's ACL. Replacements preserve existing access permissions and fail if preservation is impossible. Unpublished temporary files are cleaned up on handled failures/cancellation. No backups, locks, or concurrent-change detection are provided: the last successful replacement wins. Completed writes are not undone by cancellation or by pinning an earlier package version.

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
