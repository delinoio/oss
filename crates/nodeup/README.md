# nodeup

`nodeup` is a Rust-based Node.js version manager with rustup-like commands, deterministic runtime resolution, and managed alias dispatch for `node`, `npm`, and `npx`.

## Overview

- Manage multiple Node.js runtimes from one CLI.
- Resolve active runtime consistently with explicit, override, and default selectors.
- Use human-friendly output for operators and JSON output for automation.
- Run `node`, `npm`, and `npx` through one binary by executable-name dispatch.

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

## Output and Logging

- Global output mode: `--output human|json` (default: `human`)
- `human` mode:
  - command results and logs are written for operators
  - default log filter is `nodeup=info` for management commands
- `json` mode:
  - success payloads are written to stdout as JSON
  - handled failures are written to stderr as JSON envelopes
    - fields: `kind`, `message`, `exit_code`
  - default logging is off unless explicitly enabled via `RUST_LOG`

Color control:

- `NODEUP_LOG_COLOR=always|auto|never` (default `always`)
- `NO_COLOR` disables color when `NODEUP_LOG_COLOR` is unset or `auto`

## Testing Strategy

`nodeup` validation combines unit tests and end-to-end CLI integration tests.

- Unit tests cover selectors, resolver, release index cache behavior, logging mode selection, and installer helpers.
- CLI integration tests cover command contracts, JSON error envelopes, selector precedence, override lifecycle, update/check branches, self-management commands, and alias dispatch (`node`, `npm`, `npx`).

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

## Documentation Links

- Project index: [`docs/project-nodeup.md`](../../docs/project-nodeup.md)
- Runtime contract: [`docs/crates-nodeup-foundation.md`](../../docs/crates-nodeup-foundation.md)
- Public guide: [`apps/public-docs/nodeup.mdx`](../../apps/public-docs/nodeup.mdx)
