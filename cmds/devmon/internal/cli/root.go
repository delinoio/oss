package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/delinoio/oss/cmds/devmon/internal/config"
	"github.com/delinoio/oss/cmds/devmon/internal/contracts"
	"github.com/delinoio/oss/cmds/devmon/internal/executor"
	"github.com/delinoio/oss/cmds/devmon/internal/logging"
	"github.com/delinoio/oss/cmds/devmon/internal/menubar"
	"github.com/delinoio/oss/cmds/devmon/internal/paths"
	"github.com/delinoio/oss/cmds/devmon/internal/scheduler"
	"github.com/delinoio/oss/cmds/devmon/internal/servicecontrol"
	"github.com/delinoio/oss/cmds/devmon/internal/state"
)

var newServiceManager = servicecontrol.NewManager
var runMenubar = menubar.Run

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
	case contracts.DevmonCommandService:
		return executeService(args[1:], stdout, stderr)
	case contracts.DevmonCommandMenubar:
		return executeMenubar(args[1:], stderr)
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
	fs.StringVar(&configPath, "config", defaultConfigPath(), "path to devmon TOML config")

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
	stateStore, err := state.NewStore("", logger)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "init state store: %v\n", err)
		return 1
	}
	runner.SetStateStore(stateStore)

	runContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	daemonPID := os.Getpid()
	if err := stateStore.MarkDaemonStarted(daemonPID); err != nil {
		logging.Event(
			logger,
			slog.LevelError,
			"state_store_mark_daemon_started_failed",
			slog.String("path", stateStore.Path()),
			slog.String("error", err.Error()),
		)
	}
	defer func() {
		if err := stateStore.MarkDaemonStopped(daemonPID); err != nil {
			logging.Event(
				logger,
				slog.LevelError,
				"state_store_mark_daemon_stopped_failed",
				slog.String("path", stateStore.Path()),
				slog.String("error", err.Error()),
			)
		}
	}()

	heartbeatContext, cancelHeartbeat := context.WithCancel(runContext)
	defer cancelHeartbeat()
	go runHeartbeat(heartbeatContext, logger, stateStore, runner, daemonPID)

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
	fs.StringVar(&configPath, "config", defaultConfigPath(), "path to devmon TOML config")

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

func executeService(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		printServiceUsage(stderr)
		return 2
	}
	if len(args) > 1 {
		_, _ = fmt.Fprintln(stderr, "service command accepts exactly one action")
		printServiceUsage(stderr)
		return 2
	}

	logger, err := logging.NewWithWriter(stderr, "info")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "init logger: %v\n", err)
		return 1
	}

	action := contracts.DevmonServiceAction(args[0])
	switch action {
	case contracts.DevmonServiceActionInstall,
		contracts.DevmonServiceActionUninstall,
		contracts.DevmonServiceActionStart,
		contracts.DevmonServiceActionStop,
		contracts.DevmonServiceActionStatus:
	default:
		_, _ = fmt.Fprintf(stderr, "unknown service action: %s\n", args[0])
		printServiceUsage(stderr)
		return 2
	}

	manager, err := newServiceManager(logger)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "init service manager: %v\n", err)
		return 1
	}

	ctx := context.Background()

	switch action {
	case contracts.DevmonServiceActionInstall:
		if err := manager.Install(ctx); err != nil {
			_, _ = fmt.Fprintf(stderr, "service install: %v\n", err)
			return 1
		}
	case contracts.DevmonServiceActionUninstall:
		if err := manager.Uninstall(ctx); err != nil {
			_, _ = fmt.Fprintf(stderr, "service uninstall: %v\n", err)
			return 1
		}
	case contracts.DevmonServiceActionStart:
		if err := manager.Start(ctx); err != nil {
			_, _ = fmt.Fprintf(stderr, "service start: %v\n", err)
			return 1
		}
	case contracts.DevmonServiceActionStop:
		if err := manager.Stop(ctx); err != nil {
			_, _ = fmt.Fprintf(stderr, "service stop: %v\n", err)
			return 1
		}
	case contracts.DevmonServiceActionStatus:
		summary, err := manager.Status(ctx)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "service status: %v\n", err)
			return 1
		}

		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(summary); err != nil {
			_, _ = fmt.Fprintf(stderr, "service status output: %v\n", err)
			return 1
		}
	}

	return 0
}

func executeMenubar(args []string, stderr io.Writer) int {
	if len(args) > 0 {
		_, _ = fmt.Fprintln(stderr, "menubar does not accept arguments")
		return 2
	}

	logger, err := logging.NewWithWriter(stderr, "info")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "init logger: %v\n", err)
		return 1
	}

	runContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runMenubar(runContext, logger); err != nil {
		_, _ = fmt.Fprintf(stderr, "run menubar: %v\n", err)
		return 1
	}

	return 0
}

func runHeartbeat(
	ctx context.Context,
	logger *slog.Logger,
	stateStore *state.Store,
	runner *scheduler.Runner,
	daemonPID int,
) {
	const heartbeatInterval = 5 * time.Second

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := stateStore.MarkHeartbeat(daemonPID, runner.ActiveJobs()); err != nil {
				logging.Event(
					logger,
					slog.LevelError,
					"state_store_heartbeat_failed",
					slog.String("path", stateStore.Path()),
					slog.String("error", err.Error()),
				)
			}
		}
	}
}

func defaultConfigPath() string {
	path, err := paths.ConfigPath()
	if err != nil {
		return "devmon.toml"
	}
	return path
}

func printUsage(stderr io.Writer) {
	_, _ = fmt.Fprintln(stderr, "usage:")
	_, _ = fmt.Fprintln(stderr, "  devmon daemon --config <path>")
	_, _ = fmt.Fprintln(stderr, "  devmon validate --config <path>")
	_, _ = fmt.Fprintln(stderr, "  devmon service <install|uninstall|start|stop|status>")
	_, _ = fmt.Fprintln(stderr, "  devmon menubar")
}

func printServiceUsage(stderr io.Writer) {
	_, _ = fmt.Fprintln(stderr, "service usage:")
	_, _ = fmt.Fprintln(stderr, "  devmon service install")
	_, _ = fmt.Fprintln(stderr, "  devmon service uninstall")
	_, _ = fmt.Fprintln(stderr, "  devmon service start")
	_, _ = fmt.Fprintln(stderr, "  devmon service stop")
	_, _ = fmt.Fprintln(stderr, "  devmon service status")
}
