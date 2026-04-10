# with-watch

`with-watch` reruns a delegated command when its inferred or explicit filesystem inputs change.

## Why use

- Keep familiar POSIX/coreutils-style commands while adding automatic reruns.
- Let `with-watch` infer watched inputs for common read/write utilities such as `cat`, `cp`, `sed`, and `find`.
- Fall back to explicit `exec --input` globs when inference would be ambiguous or when the delegated command has no meaningful filesystem inputs.

## Install

```sh
cargo install with-watch
brew install delinoio/tap/with-watch
```

## Command modes

- Passthrough mode: `with-watch [--no-hash] <utility> [args...]`
- Shell mode: `with-watch [--no-hash] --shell '<expr>'`
- Explicit-input mode: `with-watch exec [--no-hash] --input <glob>... -- <command> [args...]`

Use passthrough mode for a single delegated command, shell mode for simple command-line expressions that need `&&`, `||`, or `|`, and `exec --input` when you want to declare the watched files yourself.

## Quick start

```sh
with-watch cat input.txt
with-watch cp src.txt dest.txt
with-watch ls -l
with-watch --shell 'cat src.txt | grep hello'
with-watch sed -i.bak -e 's/old/new/' config.txt
with-watch exec --input 'src/**/*.rs' -- cargo test -p with-watch
```

## Inference Model

- Passthrough and shell modes use built-in command adapters before falling back to conservative path heuristics.
- Known outputs, inline scripts, patterns, and shell output redirects are filtered out of the watch set.
- Pathless defaults are intentionally narrow: only `ls`, `dir`, `vdir`, `du`, and `find` implicitly watch the current directory.
- `ls`-style commands watch directory listings via metadata snapshots: plain `ls` watches immediate children, `ls -R` stays recursive, and `ls -d` watches only the named path.
- `exec --input` remains the explicit escape hatch when a delegated command has no meaningful filesystem inputs or when fallback inference would be ambiguous.

## When to use exec --input

Use `exec --input` when the delegated command does not read a stable filesystem input by itself, or when you want the watch set to be explicit.

For example, commands like `echo hello` are intentionally rejected because there is nothing safe to watch:

```sh
with-watch exec --input 'src/**/*.rs' -- cargo test -p with-watch
```

`with-watch` reruns the delegated command exactly as provided. It does not inject changed paths into argv or environment variables.

## Shell limitations

- `--shell` is for command-line expressions, not shell scripts.
- Supported operators are `&&`, `||`, and `|`.
- Input redirects (`<`, `<>`) are treated as watched inputs.
- Output redirects (`>`, `>>`, `&>`, `&>>`, `>|`) are filtered as outputs and are not watched.
- Broader shell control-flow remains out of scope for v1.

## Rerun behavior

- The default rerun filter compares content hashes, which avoids reruns from metadata churn alone.
- `ls`, `dir`, and `vdir` use metadata-based listing snapshots instead of hashing every file under the watched directory before the first run.
- `--no-hash` switches the filter to metadata-only comparison.
- Commands that write excluded outputs such as `cp src.txt dest.txt` should rerun when the source input changes, not when the output file changes.
- Commands that mutate watched inputs directly, such as `sed -i.bak -e 's/old/new/' config.txt`, refresh their baseline after each run so they do not loop on their own writes.
- Path-based inputs anchor the watcher at the nearest existing directory so replace-style writers keep producing later external change events.

## Troubleshooting

- `No watch inputs could be inferred from the delegated command`: switch to `with-watch exec --input ... -- <command>`.
- `--shell cannot be combined with delegated argv or the exec subcommand`: choose exactly one command mode per invocation.
- `--shell` works only for simple command-line expressions on Unix-like platforms.
