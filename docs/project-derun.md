# Project: derun

## Goal
`derun` is a Go CLI that provides terminal-faithful command execution for humans and MCP-based output retrieval for AI.
Its primary contract is zero-intrusion command proxying for `derun run`, with side-channel transcript capture that can be queried later through `derun mcp`.

## Path
- Canonical CLI path: `cmds/derun`

## Runtime and Language
- Go CLI

## Users
- Engineers running interactive commands in local terminals
- AI coding agents that need replay/tail access to command output
- Tool maintainers who need deterministic local session capture and retrieval

## In Scope
- `derun run` command execution with terminal-fidelity proxy behavior
- Full stdin/TTY/ANSI passthrough without output mutation in user-visible streams
- Local transcript capture for AI retrieval as raw output bytes plus indexed chunks
- `derun mcp` stdio MCP server exposing session discovery and output read tools
- Historical replay and live tail over cursor-based MCP APIs
- Cross-platform transport abstraction with POSIX PTY and Windows ConPTY support
- Retention garbage collection with 24-hour default TTL for session artifacts

## Out of Scope
- Remote execution service or multi-host session aggregation
- AI-initiated keystroke injection into active user sessions in v1
- Secret redaction transforms that mutate captured raw output bytes
- Replacing shell/runtime behavior beyond proxy and capture boundaries

## Architecture
Primary components:
- CLI Router: resolves `run` and `mcp` command families.
- Run Executor: launches child command and owns lifecycle/exit propagation.
- PTY/Pipe Transport Adapter: chooses `posix-pty`, `windows-conpty`, or pipe fallback mode.
- Transcript Writer: duplicates output stream to append-only storage side channel.
- Session Indexer: records byte offsets, channel metadata, and cursor progression.
- MCP Bridge Server: exposes read-only session/output tools over stdio.
- Retention GC: removes expired session artifacts at startup and periodic MCP intervals.

Implemented package layout:
- `cmds/derun/internal/cli`: command parsing and command dispatch (`run`, `mcp`).
- `cmds/derun/internal/transport`: process execution for pipe mode and POSIX PTY mode.
- `cmds/derun/internal/state`: session artifact storage, append locking, and cursor reads.
- `cmds/derun/internal/mcp`: MCP stdio server, framing, tool routing, and tool handlers.
- `cmds/derun/internal/capture`: side-channel output writer.
- `cmds/derun/internal/retention`: retention GC sweep.
- `cmds/derun/internal/logging`: JSONL structured log sink.

Runtime flow:
1. `derun run` allocates PTY/ConPTY when interactive TTY is present; otherwise uses pipe transport.
2. Child output bytes are forwarded to the user terminal unchanged and simultaneously persisted to transcript storage.
3. Session metadata and index entries are updated incrementally as chunks are written.
4. `derun mcp` serves `list/get/read/wait` tools, reading session state by cursor and long-polling for live updates.
5. Final session state and exit metadata are persisted at process termination.

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

Command contracts:
- `derun run [--session-id <id>] [--retention <duration>] -- <command> [args...]`
: Executes user command with terminal-fidelity proxying and side-channel transcript capture.
- `derun mcp`
: Starts stdio MCP server for AI-driven session/output retrieval.

MCP I/O contracts:
- `derun_list_sessions(state?, limit?)`
: Returns active/recent session metadata with session identifier and lifecycle state.
- `derun_get_session(session_id)`
: Returns lifecycle, execution metadata, transport mode, and retention metadata.
- `derun_read_output(session_id, cursor?, max_bytes?)`
: Returns raw output chunks, `next_cursor`, and `eof` flag.
- `derun_wait_output(session_id, cursor, timeout_ms)`
: Long-polls for live output and returns chunk delta with new cursor.
- `derun_wait_output` must wait until new output bytes arrive or timeout when the session is still active and the cursor is at the current output tail.

Schema-version contract:
- Every MCP tool response includes `schema_version`.
- Initial schema version is `v1alpha1`.
- Cursor values are stringified unsigned byte offsets.

Terminal fidelity rules:
- No prefix/banner injection into child stdout/stderr streams.
- Interactive sessions must forward stdin bytes, resize events, and termination signals.
- Child exit code or signal must be propagated as `derun run` process exit result.
- Capture pipeline must be side-channel only and must not transform forwarded bytes.

Session discovery/attach contract:
- Explicit session identifier attach model only.
- No implicit "latest active session" selection in v1.

## Storage
State roots:
- POSIX: `$XDG_STATE_HOME/derun` (fallback: `~/.local/state/derun`)
- Windows: `%LOCALAPPDATA%/derun/state`

Per-session artifact layout:
- `meta.json`: immutable session identity and command metadata
- `output.bin`: append-only raw output bytes
- `index.jsonl`: chunk index records (`offset`, `length`, `channel`, `timestamp`)
- `final.json`: final lifecycle state, exit code/signal, end timestamps

Retention contract:
- Default retention TTL: 24 hours.
- Per-session `retention_seconds` from `meta.json` overrides the global sweep default when present.
- Expired session cleanup runs at `derun run` startup and periodic intervals in `derun mcp`.
- Active sessions must never be removed during retention sweeps.

Write consistency contract:
- `meta.json` and `final.json` are written atomically via temp-file + rename.
- `output.bin` and `index.jsonl` append operations are guarded by per-session advisory file lock (`append.lock`).

## Security
- Session storage is same-user local only with restrictive filesystem permissions:
- Directory permissions: `0700`
- File permissions: `0600`
- Persist output stream only by default; stdin is proxied but not persisted.
- Reject path traversal and symlink escape conditions while loading session artifacts.
- Keep MCP tool surface read-only in v1.
- Do not emit secret values into operational logs.

## Logging
Required baseline logs:
- `session_id`
- `transport_mode`
- `tty_attached`
- `chunk_offset`
- `chunk_size`
- `state_transition`
- `exit_code`
- `signal`
- `cleanup_result`

Logging boundary rules:
- Structured logs must go to internal log sink.
- Child stdout/stderr streams must remain unmodified terminal payload.

## Build and Test
Validation commands:
- Build: `go build ./cmds/derun/...`
- Test: `go test ./cmds/derun/...`
- Workspace validation: `go test ./...`

Implemented defaults:
- `derun_read_output` default `max_bytes`: `65536`.
- `derun_wait_output` default `timeout_ms`: `30000` (cap `60000`).
- MCP retention sweep interval: every 10 minutes.

Required behavioral test scenarios:
1. ANSI/curses app parity (`vim`, colorized output) through `derun run`.
2. Signal/exit propagation (`Ctrl-C`, process exit code, termination signal).
3. Live tail through `derun mcp` while command runs in another terminal.
4. Historical replay from cursor `0` after command completion.
5. Concurrent sessions with isolated metadata and cursors.
6. Large-output chunking with stable `next_cursor` semantics.
7. TTL expiration removes only expired sessions and preserves active sessions.
8. Windows ConPTY parity tests and POSIX PTY parity tests.

## Roadmap
- Phase 1: Terminal-fidelity `run` execution and transcript persistence.
- Phase 2: MCP replay/live-tail tool surface and cursor consistency guarantees.
- Phase 3: Cross-platform hardening for PTY/ConPTY behavior and stress tests.
- Phase 4: Optional policy and ACL extensions for session access governance.

## Open Questions
- Final MCP schema versioning strategy and backward compatibility policy.
- Optional compression policy for large session outputs while preserving raw replay fidelity.
- Slow-filesystem lock behavior and retry policy tuning beyond advisory lock v1 baseline.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
