package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/delinoio/oss/cmds/devmon/internal/config"
	"github.com/delinoio/oss/cmds/devmon/internal/contracts"
	"github.com/delinoio/oss/cmds/devmon/internal/executor"
	"github.com/delinoio/oss/cmds/devmon/internal/logging"
	"github.com/delinoio/oss/cmds/devmon/internal/scheduler"
)

func Execute(args []string) int {
	return execute(args, os.Stdout, os.Stderr)
}

func execute(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch contracts.DevmonCommand(args[0]) {
	case contracts.DevmonCommandDaemon:
		return executeDaemon(args[1:], stderr)
	case contracts.DevmonCommandValidate:
		return executeValidate(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func executeDaemon(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet(string(contracts.DevmonCommandDaemon), flag.ContinueOnError)
	fs.SetOutput(stderr)

	var configPath string
	fs.StringVar(&configPath, "config", "devmon.toml", "path to devmon TOML config")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load config: %v\n", err)
		return 2
	}

	logger, err := logging.NewWithWriter(stderr, cfg.Daemon.LogLevel)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "init logger: %v\n", err)
		return 2
	}

	runner := scheduler.NewRunner(cfg, logger, executor.NewShellExecutor(logger))

	runContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runner.Run(runContext); err != nil {
		_, _ = fmt.Fprintf(stderr, "run daemon: %v\n", err)
		return 1
	}

	return 0
}

func executeValidate(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet(string(contracts.DevmonCommandValidate), flag.ContinueOnError)
	fs.SetOutput(stderr)

	var configPath string
	fs.StringVar(&configPath, "config", "devmon.toml", "path to devmon TOML config")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "validate config: %v\n", err)
		return 2
	}

	jobCount := 0
	for _, folder := range cfg.Folders {
		jobCount += len(folder.Jobs)
	}

	_, _ = fmt.Fprintf(stdout, "config is valid (%d folders, %d jobs)\n", len(cfg.Folders), jobCount)
	return 0
}

func printUsage(stderr io.Writer) {
	_, _ = fmt.Fprintln(stderr, "usage:")
	_, _ = fmt.Fprintln(stderr, "  devmon daemon --config <path>")
	_, _ = fmt.Fprintln(stderr, "  devmon validate --config <path>")
}
