# nodeup

`nodeup` is a Rust-based Node.js version manager with rustup-like commands, deterministic runtime resolution, and managed alias dispatch for `node`, `npm`, `npx`, `yarn`, and `pnpm`.

## Overview

- Manage multiple Node.js runtimes from one CLI.
- Resolve active runtime consistently with explicit, override, and default selectors.
- Use human-friendly output for operators and JSON output for automation.
- Run `node`, `npm`, `npx`, `yarn`, and `pnpm` through one binary by executable-name dispatch.

## Install

Tag contract:

- `nodeup@v<semver>`

Package manager:

- macOS/Linux: `brew install delinoio/tap/nodeup`

Direct installers:

Install `cosign` first and leave it on `PATH`; the installers require it to verify `SHA256SUMS` entries and Sigstore bundle sidecars (`*.sigstore.json`).

macOS and Linux:

```bash
(
  installer_url="https://raw.githubusercontent.com/delinoio/oss/refs/heads/main/scripts/install/nodeup.sh"
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT
  if ! curl -fsSL "$installer_url" -o "$tmp_dir/nodeup.sh"; then
    exit 1
  fi
  bash "$tmp_dir/nodeup.sh" --version latest --method direct
)
```

Windows PowerShell:

```powershell
$InstallerUrl = "https://raw.githubusercontent.com/delinoio/oss/refs/heads/main/scripts/install/nodeup.ps1"
$Installer = Join-Path ([System.IO.Path]::GetTempPath()) ("nodeup-install-" + [System.Guid]::NewGuid().ToString("N") + ".ps1")
try {
  Invoke-WebRequest -Uri $InstallerUrl -OutFile $Installer -UseBasicParsing
  Unblock-File -LiteralPath $Installer -ErrorAction SilentlyContinue
  $PowerShell = (Get-Process -Id $PID).Path
  & $PowerShell -NoProfile -ExecutionPolicy Bypass -File $Installer -Version latest -Method direct
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
}
finally {
  Remove-Item -LiteralPath $Installer -Force -ErrorAction SilentlyContinue
}
```

These commands fetch the current first-party installer scripts from `delinoio/oss`. For reproducible automation, pin the same raw URL paths to a reviewed commit or repository tag instead of `refs/heads/main`, and replace `latest` with an explicit Nodeup semver.

Canonical in-repo installer paths for maintainer workflows:

- `scripts/install/nodeup.sh`
- `scripts/install/nodeup.ps1`

`cargo-binstall`:

```bash
cargo binstall nodeup --no-confirm
```

GitHub Actions:

```yaml
- uses: taiki-e/install-action@v2
  with:
    tool: cargo-binstall
- run: cargo binstall nodeup --no-confirm
```

Direct installers support bundle-enabled releases only.

Direct installers place the binary in `~/.local/bin` by default and do not modify your shell `PATH`. Add that directory before running `nodeup`, or pass `--install-dir` / `-InstallDir` with a directory already on `PATH`.

macOS and Linux:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Windows PowerShell:

```powershell
$env:Path = "$HOME\.local\bin;$env:Path"
```

## Quick Command Reference

- `nodeup toolchain list [--quiet|--verbose]`
- `nodeup toolchain install <runtime>...`
- `nodeup toolchain uninstall <runtime>...`
- `nodeup toolchain link <name> <path>`
- `nodeup toolchain unlink <name>...`
- `nodeup default [runtime]`
- `nodeup show active-runtime`
- `nodeup show home`
- `nodeup update [runtime]...`
- `nodeup check`
- `nodeup override list`
- `nodeup override set <runtime> [--path <path>]`
- `nodeup override unset [--path <path>] [--nonexistent]`
- `nodeup which [--runtime <runtime>] <command>`
- `nodeup run [--install] <runtime> <command>...`
- `nodeup self update`
- `nodeup self uninstall`
- `nodeup self upgrade-data`
- `nodeup completions <shell> [command]`

## Runtime Resolution Precedence

Runtime resolution follows a stable order:

1. Explicit selector (`run <runtime>`, `which --runtime <runtime>`)
2. Directory override (`override set`)
3. Global default (`default`)

If no selector resolves, commands fail with deterministic `not-found` errors.

## Linked Runtimes

`nodeup toolchain link <name> <path>` registers an external runtime directory
without copying or owning that directory. The runtime must provide a runnable
Node executable under `bin/`: `bin/node` on Unix-like hosts or `bin/node.exe`
when Windows platform behavior is selected. Unix hosts require an executable
permission bit on `bin/node`.

`nodeup toolchain unlink <name>...` removes linked runtime records from nodeup
settings and tracked selectors without deleting external runtime directories.
Unlinking fails with `conflict` when the linked name is the current default or
is referenced by a directory override; change the default or remove/update the
override before unlinking. Missing linked names fail with deterministic
`not-found` errors.

## `packageManager` Support

`nodeup` resolves `package.json` from the current working directory upward and supports
the `packageManager` field for `yarn` and `pnpm` commands.

- Supported format: `<manager>@<exact-semver>`
- Supported managers: `yarn`, `pnpm`
- Strict behavior:
  - if `packageManager` exists, requested command must match manager
  - mismatches fail with `conflict`
  - malformed values fail with `invalid-input`
- Corepack is not used; `nodeup` uses the selected runtime's `npm exec`.

Mapping rules:

- `pnpm@x.y.z` -> `npm exec --package pnpm@x.y.z -- pnpm ...`
- `yarn@1.x.y` -> `npm exec --package yarn@1.x.y -- yarn ...`
- `yarn@2+` -> `npm exec --package @yarnpkg/cli-dist@x.y.z -- yarn ...`

Fallback rules when `packageManager` is absent:

- if runtime provides `bin/yarn` or `bin/pnpm`, run it directly
- otherwise run through `npm exec` with defaults:
  - `yarn` -> `@yarnpkg/cli-dist`
  - `pnpm` -> `pnpm`

## Output and Logging

- Global output mode: `--output human|json` (default: `human`)
- `human` mode:
  - command results are written to stdout for operators
  - default log filter is `nodeup=warn` for management commands
- `json` mode:
  - success payloads are written to stdout as JSON
  - handled failures are written to stderr as JSON envelopes
    - fields: `kind`, `message`, `exit_code`
    - `message` follows `<cause>. Hint: <next action>` for actionable recovery guidance
  - logging stays off so Nodeup JSON payloads remain parseable
  - `run` redirects delegated command stdout to stderr so stdout can carry the
    Nodeup JSON response; do not parse `run` stderr as a JSON-only stream
- `completions` command:
  - always writes raw completion script text to stdout
  - does not wrap completion output in JSON, even when `--output json` is set
- logs are written to stderr when enabled

Script-safe output patterns:

- Structured automation: `nodeup --output json <command>`
- Runtime identifier loops: set `RUST_LOG=off`, then run `nodeup toolchain list --quiet`
- Completion redirection: set `RUST_LOG=off`, then run `nodeup completions <shell> >file`
- Human output without logs: set `RUST_LOG=off`, then run `nodeup <command>`

Human output color control:

- Global color mode for human output: `--color auto|always|never` (default: `auto`)
- Environment override for human output: `NODEUP_COLOR=auto|always|never`
- Precedence: `--color` > `NODEUP_COLOR` > `NO_COLOR` > `auto`
- `auto` enables ANSI styles per stream only when the stream is a terminal
- `nodeup show color` reports effective human stdout, human stderr, and log color decisions
- Invalid `NODEUP_COLOR` values are ignored and reported by `nodeup show color`
- `--output json` never injects ANSI styles into JSON payloads
- `completions` output remains raw shell script text even when `--color always` is set

Log color control:

- `NODEUP_LOG_COLOR=always|auto|never` (default `always`)
- `NO_COLOR` disables color when `NODEUP_LOG_COLOR` is unset or `auto`
- Invalid `NODEUP_LOG_COLOR` values are ignored and reported by `nodeup show color`

## Completions

`nodeup completions` generates shell completion scripts for:

- `bash`
- `zsh`
- `fish`
- `powershell`
- `elvish`

Scope filtering:

- `nodeup completions <shell>` generates completions for all top-level commands.
- `nodeup completions <shell> <command>` accepts only top-level command scopes:
  - `toolchain`, `default`, `show`, `update`, `check`, `override`, `which`, `run`, `self`, `completions`
  - invalid scopes fail with `invalid-input`.

## Testing Strategy

`nodeup` validation combines unit tests and end-to-end CLI integration tests.

- Unit tests cover selectors, resolver, release index cache behavior, logging mode selection, and installer helpers.
- CLI integration tests cover command contracts, JSON error envelopes, selector precedence, override lifecycle, update/check branches, self-management commands, alias dispatch (`node`, `npm`, `npx`, `yarn`, `pnpm`), and `packageManager`-aware execution planning.
- `nodeup update` treats exact-version selectors as immutable pins, reports `skipped-exact-version`, and canonicalizes tracked exact selectors such as `22.1.0` and `v22.1.0` to one `v<semver>` entry.

Run locally from repository root:

```bash
cargo fmt --all
cargo test -p nodeup
cargo test
```

## Troubleshooting

- Runtime not installed:
  - use `nodeup toolchain install <runtime>` or `nodeup run --install <runtime> ...`
- No selector resolved:
  - set one with `nodeup default <runtime>` or `nodeup override set <runtime>`
- Linked runtime failures:
  - verify `<path>/bin/node` exists and is executable before `toolchain link`
  - use `nodeup toolchain unlink <name>` to remove a stale linked runtime record
- JSON parsing issues in automation:
  - use `--output json` and keep `RUST_LOG` unset (or `off`) to keep stdout JSON-only
- Error troubleshooting:
  - follow the `Hint:` action in the error message first, then rerun with `RUST_LOG=nodeup=debug` when deeper diagnostics are needed

## Documentation Links

- Project index: [`docs/project-nodeup.md`](../../docs/project-nodeup.md)
- Runtime contract: [`docs/crates-nodeup-foundation.md`](../../docs/crates-nodeup-foundation.md)
- Dedicated docs app: [`apps/nodeup-docs`](../../apps/nodeup-docs) (`https://nodeup.delino.io`)
