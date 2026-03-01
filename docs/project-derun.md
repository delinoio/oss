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
- `cmds/derun/internal/transport`: process execution for pipe mode, POSIX PTY mode, and Windows ConPTY mode.
- `cmds/derun/internal/state`: session artifact storage, append locking, and cursor reads.
- `cmds/derun/internal/mcp`: MCP stdio server, framing, tool routing, and tool handlers.
- `cmds/derun/internal/capture`: side-channel output writer.
- `cmds/derun/internal/retention`: retention GC sweep.
- `cmds/derun/internal/logging`: JSONL structured log sink.
- `cmds/derun/internal/e2e`: contract-level behavioral integration tests and helper fixtures.

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
- `derun run --retention` values must be positive and use whole-second precision (`duration % 1s == 0`); sub-second or fractional-second values (for example `500ms`, `1500ms`) are rejected with exit code `2`.
- `derun run` must reject explicit `--session-id` values when persisted metadata already exists (`meta.json` or `final.json`), returning exit code `2` without mutating existing artifacts.
- `derun run` must reject explicit invalid `--session-id` values (including path-segment alias `"."`), returning exit code `2` without mutating session artifacts.
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
- Retention metadata persists at second granularity (`retention_seconds`); CLI validation rejects `--retention` values that are not whole seconds so requested TTL never silently truncates.
- Expired session cleanup runs at `derun run` startup and periodic intervals in `derun mcp`.
- Unreadable/orphan session directories (for example missing or malformed `meta.json`) use fallback expiration: `lastTouchedAt + sweepTTL`, where `lastTouchedAt` is the latest filesystem modification time across the session directory and direct child artifacts.
- Expired unreadable/orphan session directories are removed; non-expired unreadable/orphan session directories are retained.
- Active sessions must never be removed during retention sweeps.

Write consistency contract:
- `meta.json` and `final.json` are written atomically via temp-file + rename.
- `derun run` writes initial `meta.json` before transport startup so startup-failure sessions remain discoverable via MCP list/get flows.
- On successful process launch, `meta.json` is rewritten to persist the child PID while keeping session identity metadata stable.
- `output.bin` and `index.jsonl` append operations are guarded by per-session advisory file lock (`append.lock`).

## Security
- Session storage is same-user local only with restrictive filesystem permissions:
- Directory permissions: `0700`
- File permissions: `0600`
- Persist output stream only by default; stdin is proxied but not persisted.
- Resolve canonical real paths before session artifact IO and reject path traversal or symlink escape outside `sessions/<session-id>`, including dangling symlink targets.
- Reject path-segment alias session IDs (for example `"."`) so all artifacts remain under `sessions/<session-id>/...` and never directly under `sessions/`.
- Apply traversal/symlink checks consistently for `meta.json`, `final.json`, `output.bin`, `index.jsonl`, and `append.lock` across read and write flows.
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
- `cleanup_reason`

Logging boundary rules:
- Structured logs must go to internal log sink.
- Child stdout/stderr streams must remain unmodified terminal payload.
- Retention sweep must emit per-session `cleanup_result` logs for skip/remove/error outcomes with explicit `cleanup_reason` values (`not_expired`, `active_session`, `expired`, `unreadable_not_expired`, `unreadable_expired`, `unreadable_stat_error`, `remove_error`).

## Build and Test
Validation commands:
- Build: `go build ./cmds/derun/...`
- Test: `go test ./cmds/derun/...`
- Workspace validation: `go test ./...`
- CI gating: `.github/workflows/CI.yml` runs `go test ./...` on `ubuntu-latest`, `macos-latest`, and `windows-latest`.

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
9. Session artifact traversal and symlink-escape attempts are rejected for both read and write operations.
10. Missing-session MCP reads/waits fail consistently with deterministic `session not found` errors.

Behavioral coverage map:
1. ANSI/curses PTY parity:
`cmds/derun/internal/e2e/contract_posix_test.go` (`TestANSIParityThroughRunWithPTY`)
2. Signal/exit propagation:
`cmds/derun/internal/e2e/contract_posix_test.go` (`TestSignalPropagationForCtrlC`) and
`cmds/derun/internal/cli/run_test.go` (`TestExecuteRunPipeModeCapturesOutputAndExitCode`)
3. Live tail via MCP while active:
`cmds/derun/internal/mcp/server_contract_test.go` (`TestServerContractLiveTailThroughWaitOutput`)
4. Historical replay from cursor `0`:
`cmds/derun/internal/mcp/server_contract_test.go` (`TestServerContractHistoricalReplayFromCursorZero`)
5. Concurrent session isolation:
`cmds/derun/internal/e2e/contract_test.go` (`TestConcurrentSessionsAreIsolated`)
6. Large-output chunking cursor stability:
`cmds/derun/internal/e2e/contract_test.go` (`TestLargeOutputChunkingHasStableCursorProgression`)
7. TTL expiration and active-session preservation:
`cmds/derun/internal/retention/gc_test.go` (`TestSweepRemovesOnlyExpiredCompletedSessions`)
8. Windows ConPTY + POSIX PTY parity:
`cmds/derun/internal/e2e/contract_windows_test.go` (`TestWindowsConPTYRunnerParity`, `TestInteractiveRunUsesWindowsConPTYTransport`) and
`cmds/derun/internal/e2e/contract_posix_test.go` (`TestANSIParityThroughRunWithPTY`)
9. Traversal and symlink-escape rejection:
`cmds/derun/internal/state/store_test.go` (`TestStoreRejectsTraversalSessionIDAcrossEntrypoints`, `TestStoreRejectsSessionDirectorySymlinkEscape`, `TestStoreRejectsSessionArtifactSymlinkEscape`)
10. Deterministic missing-session read/wait errors:
`cmds/derun/internal/mcp/tools_test.go` (`TestHandleReadOutputMissingSessionReturnsError`, `TestHandleWaitOutputMissingSessionReturnsError`)

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
