# Output, Errors, and Color

Nodeup supports human output for operators and JSON output for automation.

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
- `cleanup_boundaries`
- `remaining_manual_steps`
- `likely_leftover_paths`

The command removes Nodeup-owned data, cache, and config roots only. Binary removal, managed shim cleanup, and shell profile or PATH edits are always manual.

## Completion Output

`nodeup completions` always writes raw shell script text to stdout. It does not wrap output in JSON, even when `--output json` is supplied.

## Human Color Precedence

Human stdout and stderr color controls use this precedence:

1. `--color auto|always|never`
2. `NODEUP_COLOR=auto|always|never`
3. `NO_COLOR`
4. stream-aware `auto`

`auto` enables ANSI styles only when the relevant stream is a terminal. Invalid `NODEUP_COLOR` values are ignored.

Examples:

```bash
nodeup --color never show active-runtime
NODEUP_COLOR=always nodeup show home
NO_COLOR=1 nodeup show home
```

Inspect the effective decisions with:

```bash
nodeup show color
nodeup --output json show color
```

The diagnostic reports separate decisions for human stdout, human stderr, and logs. It also reports ignored invalid `NODEUP_COLOR` values.

## Log Color

Logs use `NODEUP_LOG_COLOR=always|auto|never`. The default is colored logs unless `NO_COLOR` disables color. `NODEUP_LOG_COLOR=always` overrides `NO_COLOR`.

Invalid `NODEUP_LOG_COLOR` values are ignored and fall back to `NO_COLOR` or the default. `nodeup show color` reports ignored invalid log color values.

## Logging Defaults

Default filters depend on context:

- Managed alias dispatch: `nodeup=warn`
- Human management commands: `nodeup=warn`
- JSON management commands: `nodeup=off`

Set `RUST_LOG` to override logging:

```bash
RUST_LOG=nodeup=debug nodeup show active-runtime
```

Keep `RUST_LOG` unset or off when a script needs clean JSON output.
