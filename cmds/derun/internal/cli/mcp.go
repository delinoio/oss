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
		firstArg := ""
		if len(fs.Args()) > 0 {
			firstArg = fs.Args()[0]
		}
		fmt.Fprintln(
			os.Stderr,
			formatUsageErrorWithDetails(
				"mcp command does not accept positional arguments",
				"use `derun mcp` without extra arguments",
				map[string]any{
					"arg_count": len(fs.Args()),
					"first_arg": firstArg,
				},
			),
		)
		return 2
	}

	stateRoot, err := resolveStateRootForMCP()
	if err != nil {
		fmt.Fprintln(os.Stderr, formatRuntimeErrorWithDetails("resolve state root", err, map[string]any{
			"has_derun_state_root": os.Getenv("DERUN_STATE_ROOT") != "",
		}))
		return 1
	}
	store, err := state.New(stateRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, formatRuntimeErrorWithDetails("initialize state store", err, map[string]any{
			"state_root": stateRoot,
		}))
		return 1
	}
	logger, err := logging.New(stateRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, formatRuntimeErrorWithDetails("initialize logger", err, map[string]any{
			"state_root": stateRoot,
		}))
		return 1
	}
	defer logger.Close()

	_, _ = retention.Sweep(store, defaultRetention, logger)

	server := mcp.NewServer(store, logger, 10*time.Minute, defaultRetention)
	if err := server.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, formatRuntimeErrorWithDetails("run mcp server", err, map[string]any{
			"state_root":     stateRoot,
			"gc_interval_ms": (10 * time.Minute).Milliseconds(),
			"retention_ms":   defaultRetention.Milliseconds(),
		}))
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
