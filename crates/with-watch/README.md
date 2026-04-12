# with-watch

`with-watch` reruns a delegated command when its inferred or explicit filesystem inputs change.

It executes the delegated command once immediately after input inference, watcher setup, and baseline capture, then waits for later filesystem changes to trigger reruns.

## Why use

- Keep familiar POSIX/coreutils-style commands while adding automatic reruns.
- Let `with-watch` infer watched inputs for common read/write utilities, search tools, and schema/codegen commands such as `cat`, `rg`, `fd`, `protoc`, and `find`.
- Fall back to explicit `exec --input` globs when inference would be ambiguous or when the delegated command has no meaningful filesystem inputs.

## Install

```sh
cargo install with-watch
brew install delinoio/tap/with-watch
```

```sh
./scripts/install/with-watch.sh --version latest --method package-manager
```

```powershell
./scripts/install/with-watch.ps1 -Version latest -Method direct
```

```sh
cargo binstall with-watch --no-confirm
```

GitHub Actions:

```yaml
- uses: taiki-e/install-action@v2
  with:
    tool: cargo-binstall
- run: cargo binstall with-watch --no-confirm
```

Direct installers verify Sigstore bundle sidecars (`*.sigstore.json`) and require `cosign`.

## Command modes

- Passthrough mode: `with-watch [--no-hash] [--clear] <utility> [args...]`
- Shell mode: `with-watch [--no-hash] [--clear] --shell '<expr>'`
- Explicit-input mode: `with-watch exec [--no-hash] [--clear] --input <glob>... -- <command> [args...]`

All modes also accept `--clear`, which clears the terminal before the initial run and each rerun when stdout is an interactive terminal.

Use passthrough mode for a single delegated command, shell mode for simple command-line expressions that need `&&`, `||`, or `|`, and `exec --input` when you want to declare the watched files yourself.

## Quick start

```sh
with-watch cat input.txt
with-watch --clear cat input.txt
with-watch cp src.txt dest.txt
with-watch ls -l
with-watch rg TODO src
with-watch --shell 'cat src.txt | grep hello'
with-watch protoc -I proto proto/service.proto --go_out gen
with-watch sed -i.bak -e 's/old/new/' config.txt
with-watch exec --input 'src/**/*.rs' -- cargo test -p with-watch
```

## Inference Model

- Passthrough and shell modes use built-in command adapters before falling back to conservative path heuristics.
- Known outputs, inline scripts, patterns, and shell output redirects are filtered out of the watch set.
- Search adapters such as `rg`, `ag`, and `fd` watch explicit search roots and file-valued pattern/ignore inputs without treating patterns, globs, or type filters as watched paths.
- Schema/codegen adapters such as `protoc`, `flatc`, `thrift`, and `capnp compile` watch source files plus include/import roots while filtering output directories and generated artifacts.
- Pathless defaults are intentionally narrow: only `ls`, `dir`, `vdir`, `du`, and `find` implicitly watch the current directory.
- `ls`-style commands watch directory listings via metadata snapshots: plain `ls` watches immediate children, `ls -R` stays recursive, and `ls -d` watches only the named path.
- `exec --input` remains the explicit escape hatch when a delegated command has no meaningful filesystem inputs or when fallback inference would be ambiguous.

## Recognized command inventory

`with-watch --help` lists the full recognized command inventory in the same order as the analyzer.

Wrapper commands:

- `env`, `nice`, `nohup`, `stdbuf`, `timeout`

Dedicated built-in adapters and aliases:

- `cp`, `mv`, `install`, `ln`, `link`, `rm`, `unlink`, `rmdir`, `shred`
- `sort`, `uniq`, `split`, `csplit`, `tee`
- `grep`, `egrep`, `fgrep`, `rg`, `ag`, `sed`
- `awk`, `gawk`, `mawk`, `nawk`
- `find`, `fd`, `xargs`, `tar`, `touch`, `truncate`
- `chmod`, `chown`, `chgrp`, `dd`, `protoc`, `flatc`, `thrift`, `capnp`

Generic read-path commands:

- `cat`, `tac`, `head`, `tail`, `wc`, `nl`, `od`, `cut`, `fmt`, `fold`, `paste`, `pr`, `tr`
- `expand`, `unexpand`, `stat`, `readlink`, `realpath`
- `md5sum`, `b2sum`, `cksum`, `sum`, `sha1sum`, `sha224sum`, `sha256sum`, `sha384sum`
- `sha512sum`, `sha512_224sum`, `sha512_256sum`
- `base32`, `base64`, `basenc`, `comm`, `join`, `cmp`, `tsort`, `shuf`

Safe current-directory defaults:

- `find`, `ls`, `dir`, `vdir`, `du`

Recognized but not auto-watchable commands:

- `echo`, `printf`, `seq`, `yes`, `sleep`, `date`, `uname`, `pwd`, `true`, `false`
- `basename`, `dirname`, `nproc`, `printenv`, `whoami`, `logname`, `users`, `hostid`
- `numfmt`, `mktemp`, `mkdir`, `mkfifo`, `mknod`

These commands are recognized, but they do not expose stable filesystem inputs on their own. Use `exec --input` when you want them to rerun from explicit globs or paths.

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

- `with-watch` always performs one initial run after it has inferred inputs and armed the watcher, even before any external filesystem change occurs.
- `--clear` clears stdout before the initial run and each rerun only when stdout is a terminal; redirected and piped output remain unchanged.
- The default rerun filter compares content hashes, which avoids reruns from metadata churn alone.
- `ls`, `dir`, and `vdir` use metadata-based listing snapshots instead of hashing every file under the watched directory before the first run.
- `--no-hash` switches the filter to metadata-only comparison.
- Commands that write excluded outputs such as `cp src.txt dest.txt` should rerun when the source input changes, not when the output file changes.
- Commands that mutate watched inputs directly, such as `sed -i.bak -e 's/old/new/' config.txt`, refresh their baseline after each run so they do not loop on their own writes.
- Path-based inputs anchor the watcher at the nearest existing directory so replace-style writers keep producing later external change events.

## Logging

- `with-watch` reads `tracing` filter directives only from `WW_LOG`.
- Diagnostic logs are off by default. Set `WW_LOG=with_watch=info` or `WW_LOG=with_watch=debug` when you want planner and watcher details.
- `RUST_LOG` does not configure `with-watch` logging.
- `WITH_WATCH_LOG_COLOR` and `NO_COLOR` continue to control ANSI log coloring.
- Fatal user-facing errors still print to stderr even when diagnostic logging is off.

## Troubleshooting

- `No watch inputs could be inferred from the delegated command`: switch to `with-watch exec --input ... -- <command>`.
- `--shell cannot be combined with delegated argv or the exec subcommand`: choose exactly one command mode per invocation.
- `--shell` works only for simple command-line expressions on Unix-like platforms.
