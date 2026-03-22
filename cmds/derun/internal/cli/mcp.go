package cli

import (
	"context"
	"errors"
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
	fs.Usage = printMCPUsage
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 2
		}
		return 2
	}
	if len(fs.Args()) != 0 {
		fmt.Fprintln(
			os.Stderr,
			formatUsageError("mcp command does not accept positional arguments", "use `derun mcp` without extra arguments"),
		)
		return 2
	}

	stateRoot, err := resolveStateRootForMCP()
	if err != nil {
		fmt.Fprintln(os.Stderr, formatRuntimeError("resolve state root", err))
		return 1
	}
	store, err := state.New(stateRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, formatRuntimeError("initialize state store", err))
		return 1
	}
	logger, err := logging.New(stateRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, formatRuntimeError("initialize logger", err))
		return 1
	}
	defer logger.Close()

	_, _ = retention.Sweep(store, defaultRetention, logger)

	server := mcp.NewServer(store, logger, 10*time.Minute, defaultRetention)
	if err := server.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, formatRuntimeError("run mcp server", err))
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
