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

```bash
./scripts/install/nodeup.sh --version latest --method package-manager
```

```powershell
./scripts/install/nodeup.ps1 -Version latest -Method direct
```

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

Direct installers verify Sigstore bundle sidecars (`*.sigstore.json`) and require `cosign`.

## Quick Command Reference

- `nodeup toolchain list [--quiet|--verbose]`
- `nodeup toolchain install <runtime>...`
- `nodeup toolchain uninstall <runtime>...`
- `nodeup toolchain link <name> <path>`
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
  - command results and logs are written for operators
  - default log filter is `nodeup=info` for management commands
- `json` mode:
  - success payloads are written to stdout as JSON
  - handled failures are written to stderr as JSON envelopes
    - fields: `kind`, `message`, `exit_code`
    - `message` follows `<cause>. Hint: <next action>` for actionable recovery guidance
  - default logging is off unless explicitly enabled via `RUST_LOG`
- `completions` command:
  - always writes raw completion script text to stdout
  - does not wrap completion output in JSON, even when `--output json` is set

Human output color control:

- Global color mode for human output: `--color auto|always|never` (default: `auto`)
- Environment override for human output: `NODEUP_COLOR=auto|always|never`
- Precedence: `--color` > `NODEUP_COLOR` > `NO_COLOR` > `auto`
- `auto` enables ANSI styles per stream only when the stream is a terminal
- `--output json` never injects ANSI styles into JSON payloads
- `completions` output remains raw shell script text even when `--color always` is set

Log color control:

- `NODEUP_LOG_COLOR=always|auto|never` (default `always`)
- `NO_COLOR` disables color when `NODEUP_LOG_COLOR` is unset or `auto`

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
  - verify `<path>/bin/node` exists before `toolchain link`
- JSON parsing issues in automation:
  - use `--output json` and keep `RUST_LOG` unset (or `off`) to avoid log noise
- Error troubleshooting:
  - follow the `Hint:` action in the error message first, then rerun with `RUST_LOG=nodeup=debug` when deeper diagnostics are needed

## Documentation Links

- Project index: [`docs/project-nodeup.md`](../../docs/project-nodeup.md)
- Runtime contract: [`docs/crates-nodeup-foundation.md`](../../docs/crates-nodeup-foundation.md)
- Public guide: [`apps/public-docs/nodeup.mdx`](../../apps/public-docs/nodeup.mdx)
