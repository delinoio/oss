# Commands

**The pnport 0.1.0 CLI is not published.** These are the current command names and release-contract behaviors, not instructions to install or run an unavailable package.

| Command | Purpose |
| --- | --- |
| `pnport run -- <command> [args...]` | Run a supported child in the selected PnP view. |
| `pnport doctor [--json]` | Check project data, native capabilities, injection prerequisites, and cache access. |
| `pnport cache path` | Print the effective per-user cache path. |
| `pnport cache list` | List retained entries and their states. |
| `pnport cache prune` | Remove abandoned incomplete or obsolete-format entries when safe. |
| `pnport cache clean` | Remove inactive cache entries when safe. |
| `pnport --help` / `pnport --version` | Show CLI help or installed version. |

## Global options

| Option | Behavior |
| --- | --- |
| `--project <path>` | Select a project directory or `.pnp.cjs` without changing cwd. Otherwise search upward from cwd. |
| `--cache-dir <path>` | Override the standard private per-user cache location. |
| `--log-level <level>` | Choose `error`, `warn`, `info`, `debug`, or `trace`. Default: `error`. |
| `--color <mode>` | Choose `auto`, `always`, or `never`. In `auto`, ANSI color requires terminal stderr and no `NO_COLOR` variable. Explicit `always` overrides `NO_COLOR`; `never` disables color. `doctor --json` is ANSI-free in every mode. |

Global options precede the subcommand in the examples here. Cache commands do not require an active PnP project. `cache prune` and `cache clean` preserve entries whose active use or ownership cannot safely be ruled out; see [cache management](/pnport/cache).

## Machine-readable doctor output

`doctor --json` emits one ANSI-free JSON object on stdout. Schema version 1 has `schemaVersion`, `ready`, and `checks`; each check has `id`, `status`, `code`, and `message`. Check IDs cover `project`, `platform`, `injection`, and `cache`, plus `linux-syscall` on Linux; status is `pass`, `fail`, or `unsupported`. A non-ready report exits 125. Do not treat a `ready` result as tool-specific compatibility evidence.

See [diagnostics and troubleshooting](/pnport/diagnostics) for exit codes and privacy boundaries.
