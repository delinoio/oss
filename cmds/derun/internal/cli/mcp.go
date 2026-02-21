package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/logging"
	"github.com/delinoio/oss/cmds/derun/internal/mcp"
	"github.com/delinoio/oss/cmds/derun/internal/retention"
	"github.com/delinoio/oss/cmds/derun/internal/state"
)

func ExecuteMCP(args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(fs.Args()) != 0 {
		fmt.Fprintln(os.Stderr, "mcp command does not accept positional arguments")
		return 2
	}

	stateRoot, err := resolveStateRootForMCP()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve state root: %v\n", err)
		return 1
	}
	store, err := state.New(stateRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init state store: %v\n", err)
		return 1
	}
	logger, err := logging.New(stateRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init logger: %v\n", err)
		return 1
	}
	defer logger.Close()

	_, _ = retention.Sweep(store, defaultRetention, logger)

	server := mcp.NewServer(store, logger, 10*time.Minute, defaultRetention)
	if err := server.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "run mcp server: %v\n", err)
		return 1
	}
	return 0
}

func resolveStateRootForMCP() (string, error) {
	if explicit := strings.TrimSpace(os.Getenv("DERUN_STATE_ROOT")); explicit != "" {
		return explicit, nil
	}
	return state.ResolveStateRoot()
}
