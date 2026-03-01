//go:build darwin

package menubar

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/getlantern/systray"

	"github.com/delinoio/oss/cmds/devmon/internal/logging"
	"github.com/delinoio/oss/cmds/devmon/internal/paths"
	"github.com/delinoio/oss/cmds/devmon/internal/servicecontrol"
)

const defaultPollInterval = 4 * time.Second

type menuState struct {
	mu      sync.Mutex
	lastErr error
}

func Run(ctx context.Context, logger *slog.Logger) error {
	manager, err := servicecontrol.NewManager(logger)
	if err != nil {
		return err
	}

	daemonLogPath, err := paths.DaemonLogPath()
	if err != nil {
		return err
	}
	configPath, err := paths.ConfigPath()
	if err != nil {
		return err
	}

	app := &menuApp{
		ctx:          ctx,
		logger:       logger,
		manager:      manager,
		daemonLog:    daemonLogPath,
		configPath:   configPath,
		pollInterval: defaultPollInterval,
		openPathFn:   openPath,
		state:        &menuState{},
	}

	return app.run()
}

type menuApp struct {
	ctx          context.Context
	logger       *slog.Logger
	manager      servicecontrol.Manager
	daemonLog    string
	configPath   string
	pollInterval time.Duration
	openPathFn   func(path string) error
	state        *menuState
}

func (app *menuApp) run() error {
	go func() {
		<-app.ctx.Done()
		systray.Quit()
	}()

	systray.Run(app.onReady, app.onExit)

	app.state.mu.Lock()
	defer app.state.mu.Unlock()
	if app.state.lastErr != nil {
		return app.state.lastErr
	}
	return nil
}

func (app *menuApp) onReady() {
	systray.SetTitle("devmon")
	systray.SetTooltip("devmon daemon manager")

	statusItem := systray.AddMenuItem("Status: loading...", "")
	statusItem.Disable()
	systray.AddSeparator()

	startItem := systray.AddMenuItem("Start Devmon", "Start devmon daemon")
	stopItem := systray.AddMenuItem("Stop Devmon", "Stop devmon daemon")
	openLogsItem := systray.AddMenuItem("Open Logs", "Open devmon daemon log file")
	openConfigItem := systray.AddMenuItem("Open Config", "Open devmon config file")
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit Menubar", "Quit devmon menubar")

	updateStatus := func() {
		summary, err := app.manager.Status(context.Background())
		if err != nil {
			statusItem.SetTitle(fmt.Sprintf("Status: error (%s)", truncateForMenu(err.Error())))
			return
		}

		title := app.statusTitle(summary)
		statusItem.SetTitle(title)

		switch summary.DaemonHealth {
		case servicecontrol.DaemonHealthRunning:
			startItem.Disable()
			stopItem.Enable()
		case servicecontrol.DaemonHealthStopped:
			startItem.Enable()
			stopItem.Disable()
		default:
			startItem.Enable()
			stopItem.Enable()
		}
	}

	updateStatus()

	go func() {
		ticker := time.NewTicker(app.pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-app.ctx.Done():
				return
			case <-ticker.C:
				updateStatus()
			}
		}
	}()

	go func() {
		for {
			select {
			case <-app.ctx.Done():
				return
			case <-startItem.ClickedCh:
				if err := app.manager.Start(context.Background()); err != nil {
					app.handleActionError("start", err)
				}
				updateStatus()
			case <-stopItem.ClickedCh:
				if err := app.manager.Stop(context.Background()); err != nil {
					app.handleActionError("stop", err)
				}
				updateStatus()
			case <-openLogsItem.ClickedCh:
				if err := app.openPathFn(app.daemonLog); err != nil {
					app.handleActionError("open_logs", err)
				}
			case <-openConfigItem.ClickedCh:
				if err := app.openPathFn(app.configPath); err != nil {
					app.handleActionError("open_config", err)
				}
			case <-quitItem.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func (app *menuApp) onExit() {}

func (app *menuApp) statusTitle(summary servicecontrol.Summary) string {
	switch summary.DaemonHealth {
	case servicecontrol.DaemonHealthRunning:
		return "Status: running"
	case servicecontrol.DaemonHealthStopped:
		return "Status: stopped"
	default:
		message := summary.Message
		if message == "" {
			message = "unknown"
		}
		return fmt.Sprintf("Status: error (%s)", truncateForMenu(message))
	}
}

func (app *menuApp) handleActionError(action string, err error) {
	logging.Event(
		app.logger,
		slog.LevelError,
		"menubar_action_failed",
		slog.String("action", action),
		slog.String("error", err.Error()),
	)

	app.state.mu.Lock()
	defer app.state.mu.Unlock()
	app.state.lastErr = err
}

func openPath(path string) error {
	command := exec.Command("open", path)
	if output, err := command.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return fmt.Errorf("open %s: %w (%s)", path, err, message)
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	return nil
}

func truncateForMenu(input string) string {
	if len(input) <= 48 {
		return input
	}
	return input[:45] + "..."
}
