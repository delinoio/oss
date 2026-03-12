# Feature: interfaces

## Interfaces
Canonical command identifiers:

```ts
enum DerunCommand {
  Run = "run",
  Mcp = "mcp",
}
```

Canonical session lifecycle states:

```ts
enum DerunSessionState {
  Starting = "starting",
  Running = "running",
  Exited = "exited",
  Signaled = "signaled",
  Failed = "failed",
  Expired = "expired",
}
```

Canonical output channels:

```ts
enum DerunOutputChannel {
  Pty = "pty",
  Stdout = "stdout",
  Stderr = "stderr",
}
```

Canonical transport modes:

```ts
enum DerunTransportMode {
  PosixPty = "posix-pty",
  WindowsConPty = "windows-conpty",
  Pipe = "pipe",
}
```

Canonical MCP tool identifiers:

```ts
enum DerunMcpTool {
  ListSessions = "derun_list_sessions",
  GetSession = "derun_get_session",
  ReadOutput = "derun_read_output",
  WaitOutput = "derun_wait_output",
}
```

Canonical release tag prefix:

```ts
enum DerunReleaseTagPrefix {
  Stable = "derun@v",
}
```

Canonical package identifiers:

```ts
enum DerunPackageId {
  HomebrewFormula = "derun",
  Winget = "DelinoIO.Derun",
}
```

Installer script contract:
- `scripts/install/derun.sh`
- `scripts/install/derun.ps1`
- Required shared flags:
: `--version <semver|latest>`
: `--method package-manager|direct`

Command contracts:
- `derun run [--session-id <id>] [--retention <duration>] -- <command> [args...]`
: Executes user command with terminal-fidelity proxying and side-channel transcript capture.
- `derun run` requires the `--` separator before the target command; calls that omit the separator (for example `derun run echo hi`) must fail with exit code `2` and must not create or mutate session artifacts.
- `derun run --retention` values must be positive and use whole-second precision (`duration % 1s == 0`); sub-second or fractional-second values (for example `500ms`, `1500ms`) are rejected with exit code `2`.
- `derun run` must reject explicit `--session-id` values when persisted metadata already exists (`meta.json` or `final.json`), returning exit code `2` without mutating existing artifacts.
- `derun run` must reject explicit invalid `--session-id` values (including path-segment alias `"."`), returning exit code `2` without mutating session artifacts.
- `derun mcp`
: Starts stdio MCP server for AI-driven session/output retrieval.
- `derun help [run|mcp]`
: Prints detailed root/subcommand usage, behavior notes, and examples.
- `derun --help`, `derun run --help`, and `derun mcp --help` print detailed usage text to `stderr`.
: Root-level help exits with `0`; subcommand flag help exits with `2` to preserve existing flag-parser semantics.

Workspace integration contract:
- Root `pnpm dev` must execute `./scripts/dev.sh`, which runs `go -C <repo-root> run ./cmds/derun run -- turbo dev` with repository-local `DERUN_STATE_ROOT`, `GOMODCACHE`, `GOCACHE`, and `GOPATH` exports so local development sessions are discoverable by the configured `derun mcp` server.

MCP I/O contracts:
- `derun_list_sessions(state?, limit?)`
: Returns active/recent session metadata with session identifier and lifecycle state.
- `derun_get_session(session_id)`
: Returns lifecycle, execution metadata, transport mode, and retention metadata.
- `derun_read_output(session_id, cursor?, max_bytes?)`
: Returns raw output chunks, `next_cursor`, and `eof` flag.
- `derun_wait_output(session_id, cursor, timeout_ms)`
: Long-polls for live output and returns chunk delta with new cursor.
- Integer-schema MCP numeric arguments (`limit`, `max_bytes`, `timeout_ms`) must be whole integers; fractional numeric values are rejected with validation errors and are never truncated/coerced.
- `derun_wait_output` must wait until new output bytes arrive or timeout when the session is still active and the cursor is at the current output tail.
- `derun_read_output` and `derun_wait_output` must both return a deterministic `session not found` tool error for unknown `session_id` values.

Schema-version contract:
- Every MCP tool response includes `schema_version`.
- Initial schema version is `v1alpha1`.
- Cursor values are stringified unsigned byte offsets.

Terminal fidelity rules:
- No prefix/banner injection into child stdout/stderr streams.
- Interactive sessions must forward stdin bytes, resize events, and termination signals.
- Windows ConPTY interactive sessions must continuously synchronize console resize changes during active runs.
- Windows ConPTY shutdown must allow a short post-exit output-drain grace window and then force-close the pseudo console only when draining stalls so PTY capture reaches EOF without truncating buffered output.
- Signal-forwarding handlers must be registered before PID publication callbacks so startup-window interrupts are forwarded to the child process.
- When interactive Windows ConPTY allocation is unavailable, `derun run` must fall back to pipe transport and continue command execution.
- Child exit code or signal must be propagated as `derun run` process exit result.
- Capture pipeline must be side-channel only and must not transform forwarded bytes.
- POSIX PTY output readers must treat terminal-close `EIO` as a benign completion condition, not a runtime failure.
- PTY eligibility must use OS terminal probing (`isatty` semantics, e.g. ioctl/GetConsoleMode), not character-device-only checks.

Session discovery/attach contract:
- Explicit session identifier attach model only.
- No implicit "latest active session" selection in v1.

