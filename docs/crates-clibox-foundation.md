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
- Unknown/malformed arguments print sanitized structured usage diagnostics to stderr and exit with code 2; raw clap errors are never emitted.
- Issue #920 adds `dotenv list`, `dotenv merge`, and `yaml normalize`; there is no public Rust library API.

### Configuration commands (#920)
- `dotenv list [--input FILE] [--output FILE] [--force]` reads only the current directory's `.env` by default; `--input -` selects stdin. It validates all records and lists unique ASCII identifier keys in case-sensitive lexical order, without values.
- `dotenv merge FILE... [--output FILE] [--force]` requires one or more ordered inputs and permits stdin (`-`) once. Last assignment within a file and later files win, including empty values. The Node.js dotenv baseline supports optional `export`, comments, whitespace, and multiline single/double quotes. Invalid records are errors even when overwritten. Winning value tokens retain quotes, escapes, literal variable/shell references, and internal line endings; generated assignments use `KEY=token` and LF.
- `yaml normalize [--input FILE] [--output FILE | --in-place] [--force]` defaults to stdin. In-place requires an explicit regular input file and authorizes its replacement. Other existing destinations require force; force without file output is a CLI error. Relative paths use the invocation directory; explicit file input never reads stdin.
- YAML uses 1.2 Core scalar types without machine-number conversion, string keys, document-local anchors/aliases, shallow merge keys (explicit entries win; earlier merge-sequence entries win), and multiple documents. Quoted/string-tagged `<<` is ordinary data. Reject duplicate ordinary keys, invalid merges, unresolved/cyclic references, non-string keys, unsupported tags, and version directives other than 1.2. Sort mappings recursively by Unicode scalar value; preserve scalar types/precision, sequence order, and document order. Emit deterministic two-space block formatting with no anchors/comments, no initial marker for one document, and a marker before each of multiple documents. Output is byte-idempotent.
- Raw input aggregated across files and serialized output each have independent 64 MiB ceilings. UTF-8 is required, one initial BOM per input is removed, and raw NUL is rejected. Collection nesting is at most 128 (root collection is one). Check expansion size/depth before allocating expanded results; validate all input before publishing any result.
- Empty/comment-only streams yield zero bytes; explicit empty YAML documents yield `null`. Nonempty output ends in LF. Dotenv quoted internal line endings remain untouched.
- File publication uses same-directory private temporary files, new Unix mode 0600 or inherited Windows ACL, preserved existing access permissions, and rejects symlink/multiple-hardlink replacements. Read all inputs before publication. Failure/cancellation removes unpublished temporaries and preserves destinations. No backups, locks, concurrent-change detection, or undo of completed writes; last successful replacement wins.
- Exit codes are 0 success, 1 runtime/content/filesystem/limit failure, 2 CLI usage, 130 handled Ctrl+C, and 143 handled Unix SIGTERM. Reading, processing, and output are interruptible. Stdout is result-only; a write failure or cancellation while emitting stdout can leave partial bytes. File output never duplicates the result on stdout.
- Structured `tracing` diagnostics use stderr, warnings/errors by default and additional `RUST_LOG` detail. Color requires a TTY and respects `NO_COLOR`. Only operation, input/document ordinals, line/column, and stable classifications are permitted; never log input content, keys, values, paths, argv, environment, or raw dependency errors. Runtime is offline, executes no shell, and stores no configuration, caches, or history.

## Storage
No persistent state, project configuration, or caches.

## Security
Runtime commands perform no network access or delegated command execution. Explicit output is the only persistent mutation; publication preserves permissions and cleans unpublished temporary files. Cargo publication is explicit and gated by the repository release coordinator.

## Logging
Help and version are user output, not logs. Runtime and argument failures use enum-backed structured `tracing` on stderr. Debug events mark operation/input start and completion without input bytes, paths, keys, values, argv, environment, or dependency error text. Panic payloads are also suppressed.

## Build and Test
- `cargo run -p clibox -- --help`
- `cargo test -p clibox`
- Root `cargo test` and `cargo fmt --all --check` remain required repository checks.
- `cargo publish -p clibox --dry-run` verifies standalone crate packaging.
- Unit/process tests cover CLI defaults/conflicts, dotenv syntax and precedence, YAML Core/merge/alias semantics, precision and idempotence, encoding, exact/exceeded aggregate input and output limits, depth/expansion, file permissions/links/failure cleanup/concurrent replacement, interruption, broken stdout, and secret-marker privacy under trace logging. npm packaging also checks the native executable version against both source manifests.

## Dependencies and Integrations
Uses `clap`, the pure-Rust `yaml-rust2` event parser, regex scalar resolution, `tempfile`, `tracing`, and platform signal/file APIs. YAML resolution retains a shared reference graph and computes output size before emission; numeric lexemes never convert through machine numbers. Unix mode/owner/group and Linux/macOS ACL preservation use native OS APIs; Windows replacement retains its destination DACL. No C build dependency is added. npm distribution is owned by `packages/clibox`. The root cargo-mono tag allowlist includes `clibox` and releases use `clibox@v<version>`.

## Change Triggers
Update the project index, npm distribution contract, `crates/AGENTS.md`, and root release contracts when command behavior, naming, versions, platforms, or publication changes.

## References
- [Project index](project-clibox.md)
- [Repository defaults](repository-defaults.md)
