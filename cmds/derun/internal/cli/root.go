package cli

import (
	"fmt"
	"os"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
)

func Execute(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 2
	}

	switch contracts.DerunCommand(args[0]) {
	case contracts.DerunCommandRun:
		return ExecuteRun(args[1:])
	case contracts.DerunCommandMCP:
		return ExecuteMCP(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		printUsage()
		return 2
	}
}

func printUsage() {
	_, _ = fmt.Fprintln(os.Stderr, "usage:")
	_, _ = fmt.Fprintln(os.Stderr, "  derun run [--session-id <id>] [--retention <duration>] -- <command> [args...]")
	_, _ = fmt.Fprintln(os.Stderr, "  derun mcp")
}
