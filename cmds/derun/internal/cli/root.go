package cli

import (
	"fmt"
	"os"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
)

const helpCommandName = "help"

func Execute(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 2
	}
	if isRootHelpFlag(args[0]) {
		printUsage()
		return 0
	}
	if args[0] == helpCommandName {
		return executeHelp(args[1:])
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

func executeHelp(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 0
	}
	if len(args) > 1 {
		fmt.Fprintln(os.Stderr, "help command accepts at most one topic")
		return 2
	}

	switch contracts.DerunCommand(args[0]) {
	case contracts.DerunCommandRun:
		printRunUsage()
		return 0
	case contracts.DerunCommandMCP:
		printMCPUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown help topic: %s\n", args[0])
		printUsage()
		return 2
	}
}

func isRootHelpFlag(value string) bool {
	return value == "-h" || value == "--help"
}
