# Feature: operations

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
- On Windows, `append.lock` uses an exclusive `LockFileEx` byte-range lock (offset `0`, length `1`) to serialize appenders.


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
- Windows ConPTY E2E coverage requires console device handles (`CONIN$`, `CONOUT$`) and may skip parity assertions when the host returns no ConPTY output bytes.
- Distribution pipeline:
: `.github/workflows/release-derun.yml`
: tag trigger: `derun@v*`
: `workflow_dispatch` supports `version` and `dry_run`
- Release artifact contract:
: `derun-linux-amd64.tar.gz`
: `derun-darwin-amd64.tar.gz`
: `derun-darwin-arm64.tar.gz`
: `derun-windows-amd64.zip`
: `SHA256SUMS` + per-artifact cosign signatures (`*.sig`, `*.pem`)
- Package-manager publication integration:
: Homebrew formula update via `scripts/release/update-homebrew.sh` (`derun`)
: winget manifest update via `scripts/release/update-winget.sh` (`DelinoIO.Derun`)

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
11. Windows state flow keeps live sessions running and enforces append lock serialization.

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
11. Windows process liveness and append lock serialization:
`cmds/derun/internal/state/process_windows_test.go` (`TestProcessAliveCurrentProcess`, `TestProcessAliveInvalidPID`, `TestProcessAliveTreatsAccessDeniedAsAlive`) and
`cmds/derun/internal/state/store_windows_test.go` (`TestGetSessionKeepsRunningWhenPIDIsAlive`, `TestAppendOutputBlocksWhileAppendLockIsHeld`)

