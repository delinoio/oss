# with-watch

`with-watch` is a Rust-based command rerun watcher that repeats a delegated command when its inferred or explicit filesystem inputs change.

It performs one initial run immediately after input inference, watcher setup, and baseline capture, then listens for later filesystem changes.

## Why use with-watch

- Keep familiar CLI tools such as `cat`, `cp`, `sed`, `rg`, `protoc`, and `find`.
- Let `with-watch` infer watched inputs for common file-oriented, search, and schema/codegen commands.
- Declare explicit inputs with `exec --input` when you want fully predictable reruns.

## Install

Tag contract:

- `with-watch@v<semver>`

Package manager:

- macOS/Linux: `brew install delinoio/tap/with-watch`
- Homebrew installs prebuilt archives on macOS Intel, macOS Apple Silicon, Linux amd64, and Linux arm64

`cargo-binstall`:

```bash
cargo binstall with-watch --no-confirm
```

GitHub Actions:

```yaml
- uses: taiki-e/install-action@v2
  with:
    tool: cargo-binstall
- run: cargo binstall with-watch --no-confirm
```

Cargo:

```bash
cargo install with-watch
```

## Quick start

Rerun a file reader when its input changes:

```bash
with-watch cat input.txt
```

Watch the current directory with a safe pathless default:

```bash
with-watch ls -l
```

Search a source tree without watching the pattern itself:

```bash
with-watch rg TODO src
```

Run a simple shell expression:

```bash
with-watch --shell 'cat input.txt | grep hello'
```

Watch proto inputs and rerun codegen when schemas change:

```bash
with-watch protoc -I proto proto/service.proto --go_out gen
```

Provide explicit inputs for an arbitrary command:

```bash
with-watch exec --input 'src/**/*.rs' -- cargo test -p with-watch
```

## Input inference model

- Passthrough mode and `--shell` use built-in command adapters first.
- Known outputs, inline scripts, and output redirects are filtered out of the watch set.
- Search adapters such as `rg`, `ag`, and `fd` watch explicit search roots and file-valued pattern/ignore inputs without treating patterns, globs, or type filters as watched paths.
- Schema/codegen adapters such as `protoc`, `flatc`, `thrift`, and `capnp compile` watch source files plus include/import roots while filtering output directories and generated artifacts.
- Safe pathless defaults are intentionally narrow: `ls`, `dir`, `vdir`, `du`, and `find`.
- If `with-watch` cannot infer safe filesystem inputs, it fails instead of guessing.

## Recognized command inventory

`with-watch --help` includes the full recognized command inventory in analyzer order.

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

These commands are recognized, but they do not expose stable filesystem inputs on their own. Use `exec --input` when you want explicit watch inputs.

## When exec --input is required

Some commands do not expose meaningful filesystem inputs on their own. For example, `echo hello` has nothing safe to watch.

In those cases, use `exec --input` to declare the watch set explicitly:

```bash
with-watch exec --input 'src/**/*.rs' -- cargo test -p with-watch
```

`with-watch` reruns the delegated command unchanged. It does not inject changed file paths into argv or environment variables.

## Shell support boundaries

- `--shell` supports command-line expressions with `&&`, `||`, and `|`.
- Input redirects (`<`, `<>`) become watched inputs.
- Output redirects (`>`, `>>`, `&>`, `&>>`, `>|`) are treated as outputs and filtered from watching.
- Full shell control-flow is out of scope for v1.

## Rerun behavior

- `with-watch` always performs one initial run after it has inferred inputs and armed the watcher, even before any external filesystem change occurs.
- Default rerun filtering uses content hashes.
- `--no-hash` switches to metadata-only comparison.
- Self-mutating commands such as `sed -i.bak -e 's/old/new/' config.txt` refresh their baseline after each run so they do not loop on their own writes.
- Replace-style writers remain watchable because path inputs subscribe from the nearest existing directory anchor.

## Logging

- `with-watch` reads diagnostic `tracing` filters only from `WW_LOG`.
- Diagnostic logs are off by default.
- Set `WW_LOG=with_watch=info` for normal watcher diagnostics or `WW_LOG=with_watch=debug` for deeper troubleshooting.
- `RUST_LOG` does not affect `with-watch` logging.
- `WITH_WATCH_LOG_COLOR` and `NO_COLOR` still control ANSI log coloring.
- Fatal user-facing errors still print to stderr even when diagnostic logging is off.

## Related pages

- [Projects Overview](projects-overview)
- [Documentation Lifecycle](documentation-lifecycle)
