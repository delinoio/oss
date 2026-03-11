package cli

import (
	"fmt"
	"os"
)

func printUsage() {
	printHelpLines(
		"derun: terminal-faithful command execution with local transcript capture for MCP clients.",
		"",
		"Usage:",
		"  derun run [--session-id <id>] [--retention <duration>] -- <command> [args...]",
		"  derun mcp",
		"  derun help [run|mcp]",
		"",
		"Commands:",
		"  run   Execute a command and capture terminal output without mutating visible output streams.",
		"  mcp   Start a read-only MCP stdio server for session discovery, replay, and live tail.",
		"  help  Show help for derun, run, or mcp.",
		"",
		"Execution model:",
		"  1. `derun run` launches your command in PTY/ConPTY mode when an interactive terminal is attached.",
		"  2. Output bytes are forwarded to your terminal unchanged and duplicated to local session artifacts.",
		"  3. `derun mcp` exposes list/get/read/wait tools so agents can inspect captured sessions.",
		"",
		"Key rules:",
		"  - `derun run` requires the `--` separator before the target command.",
		"  - `--retention` must be positive and use whole-second precision (for example: 1s, 5m, 24h).",
		"  - Session artifacts are local-only and default to a 24-hour retention TTL.",
		"",
		"Environment:",
		"  DERUN_STATE_ROOT",
		"      Override the state directory used for metadata, output bytes, and indexes.",
		"",
		"Exit codes:",
		"  0  Success",
		"  1  Runtime/internal failure",
		"  2  Usage or validation error",
		"",
		"Examples:",
		"  derun run -- make test",
		"  derun run --session-id 01J0S444444444444444444444 --retention 6h -- bash -lc 'pnpm test'",
		"  DERUN_STATE_ROOT=/tmp/derun-state derun run -- echo \"hello\"",
		"  derun mcp",
		"",
		"Use `derun help run` or `derun help mcp` for command-specific details.",
	)
}

func printRunUsage() {
	printHelpLines(
		"Run command: execute a target command with terminal-fidelity streaming and transcript capture.",
		"",
		"Usage:",
		"  derun run [--session-id <id>] [--retention <duration>] -- <command> [args...]",
		"",
		"Flags:",
		"  --session-id <id>",
		"      Optional explicit session identifier. It must be unique and path-safe.",
		"      If omitted, derun generates a ULID-style session identifier.",
		"  --retention <duration>",
		"      Session retention TTL (default: 24h).",
		"      Use Go duration syntax with whole-second precision: 30s, 10m, 24h.",
		"      Invalid examples: 500ms, 1500ms, 0s, negative values.",
		"  -h, --help",
		"      Show this help text.",
		"",
		"Behavior:",
		"  - The `--` separator is required to split derun flags from target command arguments.",
		"  - Validation failures return exit code 2 and do not create or mutate session artifacts.",
		"  - Child exit code/signal is propagated as the `derun run` process exit result.",
		"  - Output capture is side-channel only; forwarded terminal bytes remain unmodified.",
		"",
		"Transport selection:",
		"  - Interactive POSIX terminal: posix-pty",
		"  - Interactive Windows terminal: windows-conpty (fallback to pipe when unavailable)",
		"  - Non-interactive execution: pipe (separate stdout/stderr channels)",
		"",
		"Examples:",
		"  derun run -- ls -la",
		"  derun run --retention 48h -- npm run dev",
		"  derun run --session-id 01J0S444444444444444444444 --retention 1h -- sh -lc 'printf \"hello\"'",
		"  DERUN_STATE_ROOT=/tmp/derun-state derun run -- go test ./cmds/derun/...",
	)
}

func printMCPUsage() {
	printHelpLines(
		"MCP command: start derun's read-only MCP server over stdio.",
		"",
		"Usage:",
		"  derun mcp",
		"",
		"Flags:",
		"  -h, --help",
		"      Show this help text.",
		"",
		"Behavior:",
		"  - Reads MCP JSON-RPC requests from stdin and writes responses to stdout.",
		"  - Rejects positional arguments; command form is exactly `derun mcp`.",
		"  - Runs an initial retention sweep and then periodic sweeps while serving requests.",
		"",
		"Exposed MCP tools:",
		"  - derun_list_sessions: list recent/active sessions with lifecycle state",
		"  - derun_get_session: fetch session metadata and lifecycle details",
		"  - derun_read_output: read historical output bytes from a cursor",
		"  - derun_wait_output: long-poll for new output bytes from a cursor",
		"",
		"Example:",
		"  DERUN_STATE_ROOT=/tmp/derun-state derun mcp",
	)
}

func printHelpLines(lines ...string) {
	for _, line := range lines {
		_, _ = fmt.Fprintln(os.Stderr, line)
	}
}
