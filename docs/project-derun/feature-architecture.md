# Feature: architecture

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

