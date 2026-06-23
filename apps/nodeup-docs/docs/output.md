# Output, Errors, and Color

Nodeup supports human output for operators and JSON output for automation.

## Script-Safe Output

Use one of these patterns when stdout is consumed by another program:

| Need | Recommended command | Stdout contract |
| --- | --- | --- |
| Structured command data | `nodeup --output json <command>` | JSON only; JSON mode defaults Nodeup logging off unless `RUST_LOG` is set. |
| Runtime identifiers for shell loops | `nodeup toolchain list --quiet` | One runtime identifier per line, no headings; Nodeup logging defaults off unless `RUST_LOG` is set. |
| Completion script redirection | `nodeup completions <shell> >file` | Raw shell completion script text only; Nodeup logging defaults off unless `RUST_LOG` is set. |
| Human command output with logs disabled | Set `RUST_LOG=off`, then run `nodeup <command>` | Human result text only. |

Tracing logs are written to stderr when enabled, so stdout remains parseable for quiet runtime lists and completion scripts. Use `RUST_LOG=nodeup=debug` for troubleshooting, not in pipelines that parse stderr.

## Human Output

Human output is the default:

```bash
nodeup show active-runtime
```

Management commands write a concise human result to stdout. Human errors are written to stderr with this shape:

```text
nodeup error: <cause>. Hint: <next action>.
```

## JSON Output

Use JSON mode for scripts:

```bash
nodeup --output json show home
nodeup --output json toolchain list
```

Successful command payloads are pretty-printed JSON on stdout. Handled failures and command-line parser failures are JSON envelopes on stderr:

```json
{
  "kind": "invalid-input",
  "message": "<cause>. Hint: <next action>.",
  "exit_code": 2
}
```

Stable envelope fields:

- `kind`
- `message`
- `exit_code`

`kind` values include `internal`, `invalid-input`, `unsupported-platform`, `network`, `not-found`, `conflict`, and `not-implemented`.

ANSI styling is never injected into JSON stdout or stderr payloads.

Without `--output json`, command-line parser failures keep clap's native human help and error formatting.

## Selector Metadata

JSON payloads that report runtime selectors include selector metadata for automation:

- `selector_kind`: `exact-version`, `channel`, or `linked-runtime`
- `canonical_selector`: the canonical selector identity used for tracking and alias reporting
- `selector_alias_of`: present only for aliases, currently `latest` as an alias of `current`

Exact versions canonicalize to `v<semver>`. `current` and `latest` both resolve to the newest release-index entry, but `current` is the canonical selector.

## Delegated Commands in JSON Mode

`nodeup run --output json ...` keeps stdout reserved for the final Nodeup response. Delegated command stdout is routed to stderr in JSON mode.

The final payload is:

```json
{
  "runtime": "v22.1.0",
  "command": "node",
  "exit_code": 0
}
```

The Nodeup process exits with the delegated command's exit code.

## Self Uninstall Output

`nodeup self uninstall` reports what it removed and what remains manual.

Stable JSON fields:

- `removed_paths`
- `manual_leftover_paths`
- `ownership_refused_paths`
- `cleanup_boundaries`
- `remaining_manual_steps`
- `likely_leftover_paths`

The command removes Nodeup-owned data, cache, and config roots only. Binary removal, managed shim cleanup, and shell profile or PATH edits are always manual. Configured roots that are not clearly Nodeup-owned are refused without deletion and reported in `ownership_refused_paths`.

## Completion Output

`nodeup completions` always writes raw shell script text to stdout. It does not wrap output in JSON, even when `--output json` is supplied.

## Human Color Precedence

Human stdout and stderr color controls use this precedence:

1. `--color auto|always|never`
2. `NODEUP_COLOR=auto|always|never`
3. `NO_COLOR`
4. stream-aware `auto`

`auto` enables ANSI styles only when the relevant stream is a terminal. Invalid `NODEUP_COLOR` values are ignored.
Human-mode commands print a concise stderr warning for invalid `NODEUP_COLOR` values. JSON output does not include those warnings on stdout and never injects ANSI styles into JSON payloads.

Examples:

```bash
nodeup --color never show active-runtime
NODEUP_COLOR=always nodeup show home
NO_COLOR=1 nodeup show home
NO_COLOR=1 NODEUP_COLOR=always nodeup show color
```

When `NO_COLOR` and `NODEUP_COLOR=always` are both set, `NODEUP_COLOR` wins by design. Use `--color never` or `NODEUP_COLOR=never` when you need to force plain human output regardless of a global `NO_COLOR` conflict.

Inspect the effective decisions with:

```bash
nodeup show color
nodeup --output json show color
```

The diagnostic reports separate decisions for human stdout, human stderr, and logs. It also reports ignored invalid `NODEUP_COLOR` values and whether `NO_COLOR` was overridden by a Nodeup-specific color setting.

## Log Color

Logs use `NODEUP_LOG_COLOR=always|auto|never`. The default is colored logs unless `NO_COLOR` disables color. `NODEUP_LOG_COLOR=always` overrides `NO_COLOR`.

Invalid `NODEUP_LOG_COLOR` values are ignored and fall back to `NO_COLOR` or the default. Human-mode commands print a concise stderr warning, and `nodeup show color` reports ignored invalid log color values. JSON command output stays parseable.

## Logging Defaults

Default filters depend on context:

- Managed alias dispatch: `nodeup=warn`
- Human management commands: `nodeup=warn`
- JSON management commands: `nodeup=off`
- Script-safe quiet runtime lists and completion generation: `nodeup=off`

Set `RUST_LOG` to override logging:

```bash
RUST_LOG=nodeup=debug nodeup show active-runtime
```

Keep `RUST_LOG` unset or off when a script needs clean JSON output, quiet runtime lists, completion scripts, or stderr.
