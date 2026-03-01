//go:build darwin

package servicecontrol

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/delinoio/oss/cmds/devmon/internal/config"
	"github.com/delinoio/oss/cmds/devmon/internal/logging"
	"github.com/delinoio/oss/cmds/devmon/internal/paths"
	"github.com/delinoio/oss/cmds/devmon/internal/state"
)

type darwinManager struct {
	commandRunner  CommandRunner
	stateReader    StateReader
	nowFn          func() time.Time
	logger         *slog.Logger
	domain         string
	executablePath string
	configPath     string
	daemonLogPath  string
	menubarLogPath string
	daemonPlist    string
	menubarPlist   string
}

func newManager(options *managerOptions) (Manager, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable path: %w", err)
	}
	executablePath, err = filepath.Abs(executablePath)
	if err != nil {
		return nil, fmt.Errorf("resolve executable absolute path: %w", err)
	}

	configPath, err := paths.ConfigPath()
	if err != nil {
		return nil, err
	}
	daemonLogPath, err := paths.DaemonLogPath()
	if err != nil {
		return nil, err
	}
	menubarLogPath, err := paths.MenubarLogPath()
	if err != nil {
		return nil, err
	}
	daemonPlist, err := paths.DaemonPlistPath()
	if err != nil {
		return nil, err
	}
	menubarPlist, err := paths.MenubarPlistPath()
	if err != nil {
		return nil, err
	}

	return &darwinManager{
		commandRunner:  options.commandRunner,
		stateReader:    options.stateReader,
		nowFn:          options.nowFn,
		logger:         options.logger,
		domain:         fmt.Sprintf("gui/%d", os.Getuid()),
		executablePath: executablePath,
		configPath:     configPath,
		daemonLogPath:  daemonLogPath,
		menubarLogPath: menubarLogPath,
		daemonPlist:    daemonPlist,
		menubarPlist:   menubarPlist,
	}, nil
}

func (manager *darwinManager) Install(ctx context.Context) error {
	if err := manager.validateDaemonConfig(); err != nil {
		return err
	}

	if err := manager.ensureInstallDirectories(); err != nil {
		return err
	}

	daemonContent, err := manager.renderDaemonPlist()
	if err != nil {
		return err
	}
	if err := writePlist(manager.daemonPlist, daemonContent); err != nil {
		return err
	}

	menubarContent, err := manager.renderMenubarPlist()
	if err != nil {
		return err
	}
	if err := writePlist(manager.menubarPlist, menubarContent); err != nil {
		return err
	}

	if err := manager.validatePlist(ctx, manager.daemonPlist); err != nil {
		return err
	}
	if err := manager.validatePlist(ctx, manager.menubarPlist); err != nil {
		return err
	}

	_ = manager.bootoutIgnore(ctx, paths.DaemonLaunchAgentLabel)
	_ = manager.bootoutIgnore(ctx, paths.MenubarLaunchAgentLabel)

	if err := manager.bootstrap(ctx, manager.daemonPlist); err != nil {
		return err
	}
	if err := manager.bootstrap(ctx, manager.menubarPlist); err != nil {
		return err
	}

	if err := manager.enable(ctx, paths.DaemonLaunchAgentLabel); err != nil {
		return err
	}
	if err := manager.enable(ctx, paths.MenubarLaunchAgentLabel); err != nil {
		return err
	}

	if err := manager.kickstart(ctx, paths.DaemonLaunchAgentLabel, true); err != nil {
		return err
	}
	if err := manager.kickstart(ctx, paths.MenubarLaunchAgentLabel, true); err != nil {
		return err
	}

	return nil
}

func (manager *darwinManager) Uninstall(ctx context.Context) error {
	_ = manager.bootoutIgnore(ctx, paths.DaemonLaunchAgentLabel)
	_ = manager.bootoutIgnore(ctx, paths.MenubarLaunchAgentLabel)

	if err := removeIfExists(manager.daemonPlist); err != nil {
		return err
	}
	if err := removeIfExists(manager.menubarPlist); err != nil {
		return err
	}

	return nil
}

func (manager *darwinManager) Start(ctx context.Context) error {
	if err := manager.validateDaemonConfig(); err != nil {
		return err
	}

	if _, err := os.Stat(manager.daemonPlist); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("daemon launch agent is not installed: %s", manager.daemonPlist)
		}
		return fmt.Errorf("check daemon launch agent: %w", err)
	}

	_ = manager.bootoutIgnore(ctx, paths.DaemonLaunchAgentLabel)
	if err := manager.bootstrap(ctx, manager.daemonPlist); err != nil {
		return err
	}
	if err := manager.enable(ctx, paths.DaemonLaunchAgentLabel); err != nil {
		return err
	}
	if err := manager.kickstart(ctx, paths.DaemonLaunchAgentLabel, true); err != nil {
		return err
	}

	return nil
}

func (manager *darwinManager) Stop(ctx context.Context) error {
	return manager.bootout(ctx, paths.DaemonLaunchAgentLabel)
}

func (manager *darwinManager) Status(ctx context.Context) (Summary, error) {
	summary := Summary{
		Domain:        manager.domain,
		StatusFile:    manager.statusFilePath(),
		ConfigFile:    manager.configPath,
		DaemonLogFile: manager.daemonLogPath,
		Daemon: UnitStatus{
			Label:     paths.DaemonLaunchAgentLabel,
			PlistPath: manager.daemonPlist,
		},
		Menubar: UnitStatus{
			Label:     paths.MenubarLaunchAgentLabel,
			PlistPath: manager.menubarPlist,
		},
	}

	summary.Daemon.Loaded = manager.isLoaded(ctx, paths.DaemonLaunchAgentLabel)
	summary.Menubar.Loaded = manager.isLoaded(ctx, paths.MenubarLaunchAgentLabel)

	stateSnapshot, err := manager.stateReader.Read()
	if err != nil {
		summary.DaemonHealth = DaemonHealthError
		summary.Message = fmt.Sprintf("read status file: %v", err)
		return summary, nil
	}
	summary.State = stateSnapshot

	heartbeatFresh := state.IsHeartbeatFresh(stateSnapshot, manager.nowFn(), heartbeatStaleThreshold)
	pidAlive := isPIDAlive(stateSnapshot.PID)
	runningByState := stateSnapshot.Running && stateSnapshot.PID > 0 && heartbeatFresh && pidAlive

	switch {
	case summary.Daemon.Loaded && runningByState:
		summary.DaemonHealth = DaemonHealthRunning
	case !summary.Daemon.Loaded && !runningByState:
		summary.DaemonHealth = DaemonHealthStopped
	default:
		summary.DaemonHealth = DaemonHealthError
		messageParts := make([]string, 0, 3)
		if !summary.Daemon.Loaded {
			messageParts = append(messageParts, "daemon launch agent not loaded")
		}
		if !heartbeatFresh {
			messageParts = append(messageParts, "heartbeat stale")
		}
		if stateSnapshot.PID > 0 && !pidAlive {
			messageParts = append(messageParts, "pid not running")
		}
		summary.Message = strings.Join(messageParts, ", ")
	}

	if summary.Message == "" && stateSnapshot.LastError != "" {
		summary.Message = stateSnapshot.LastError
	}

	return summary, nil
}

func (manager *darwinManager) statusFilePath() string {
	statusPath, err := paths.StatePath()
	if err != nil {
		return ""
	}
	return statusPath
}

func (manager *darwinManager) ensureInstallDirectories() error {
	launchAgentsDirectory, err := paths.LaunchAgentsDirectory()
	if err != nil {
		return err
	}
	logDirectory, err := paths.LogDirectory()
	if err != nil {
		return err
	}
	configDirectory, err := paths.ConfigDirectory()
	if err != nil {
		return err
	}
	stateDirectory, err := paths.StateDirectory()
	if err != nil {
		return err
	}

	for _, directoryPath := range []string{
		launchAgentsDirectory,
		logDirectory,
		configDirectory,
		stateDirectory,
	} {
		if err := os.MkdirAll(directoryPath, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", directoryPath, err)
		}
	}

	return nil
}

func (manager *darwinManager) renderDaemonPlist() (string, error) {
	arguments := []string{
		manager.executablePath,
		"daemon",
		"--config",
		manager.configPath,
	}
	return renderPlist(renderPlistInput{
		Label:      paths.DaemonLaunchAgentLabel,
		Arguments:  arguments,
		StdoutPath: manager.daemonLogPath,
		StderrPath: manager.daemonLogPath,
		KeepAlive:  true,
		RunAtLoad:  true,
	})
}

func (manager *darwinManager) renderMenubarPlist() (string, error) {
	arguments := []string{
		manager.executablePath,
		"menubar",
	}
	return renderPlist(renderPlistInput{
		Label:      paths.MenubarLaunchAgentLabel,
		Arguments:  arguments,
		StdoutPath: manager.menubarLogPath,
		StderrPath: manager.menubarLogPath,
		KeepAlive:  false,
		RunAtLoad:  true,
	})
}

func (manager *darwinManager) validateDaemonConfig() error {
	if _, err := os.Stat(manager.configPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("daemon config file is missing: %s", manager.configPath)
		}
		return fmt.Errorf("check daemon config file: %w", err)
	}

	if _, err := config.Load(manager.configPath); err != nil {
		return fmt.Errorf("daemon config validation failed: %w", err)
	}

	return nil
}

func (manager *darwinManager) validatePlist(ctx context.Context, plistPath string) error {
	output, err := manager.commandRunner.Run(ctx, "plutil", "-lint", plistPath)
	if err != nil {
		return fmt.Errorf("validate plist %s: %w (%s)", plistPath, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (manager *darwinManager) bootstrap(ctx context.Context, plistPath string) error {
	return manager.runLaunchctl(ctx, "bootstrap", "", "bootstrap", manager.domain, plistPath)
}

func (manager *darwinManager) bootout(ctx context.Context, label string) error {
	return manager.runLaunchctl(ctx, "bootout", label, "bootout", manager.serviceTarget(label))
}

func (manager *darwinManager) bootoutIgnore(ctx context.Context, label string) error {
	err := manager.bootout(ctx, label)
	if err != nil {
		logging.Event(
			manager.logger,
			slog.LevelWarn,
			"service_control_bootout_ignored",
			slog.String("action", "bootout"),
			slog.String("label", label),
			slog.String("domain", manager.domain),
			slog.String("result", "ignored-error"),
			slog.String("error", err.Error()),
		)
	}
	return nil
}

func (manager *darwinManager) enable(ctx context.Context, label string) error {
	return manager.runLaunchctl(ctx, "enable", label, "enable", manager.serviceTarget(label))
}

func (manager *darwinManager) kickstart(ctx context.Context, label string, killRunning bool) error {
	args := []string{"kickstart"}
	if killRunning {
		args = append(args, "-k")
	}
	args = append(args, manager.serviceTarget(label))
	return manager.runLaunchctl(ctx, "kickstart", label, args...)
}

func (manager *darwinManager) isLoaded(ctx context.Context, label string) bool {
	_, err := manager.commandRunner.Run(ctx, "launchctl", "print", manager.serviceTarget(label))
	return err == nil
}

func (manager *darwinManager) serviceTarget(label string) string {
	return fmt.Sprintf("%s/%s", manager.domain, label)
}

func (manager *darwinManager) runLaunchctl(
	ctx context.Context,
	action string,
	label string,
	args ...string,
) error {
	commandArgs := make([]string, 0, len(args))
	commandArgs = append(commandArgs, args...)

	output, err := manager.commandRunner.Run(ctx, "launchctl", commandArgs...)
	if err != nil {
		errorText := strings.TrimSpace(string(output))
		logging.Event(
			manager.logger,
			slog.LevelError,
			"service_control_launchctl",
			slog.String("action", action),
			slog.String("label", label),
			slog.String("domain", manager.domain),
			slog.String("result", "failed"),
			slog.String("error", fmt.Sprintf("%v", err)),
			slog.String("output", errorText),
		)
		if errorText != "" {
			return fmt.Errorf("launchctl %s %s failed: %w (%s)", action, label, err, errorText)
		}
		return fmt.Errorf("launchctl %s %s failed: %w", action, label, err)
	}

	logging.Event(
		manager.logger,
		slog.LevelInfo,
		"service_control_launchctl",
		slog.String("action", action),
		slog.String("label", label),
		slog.String("domain", manager.domain),
		slog.String("result", "ok"),
	)
	return nil
}

func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

type renderPlistInput struct {
	Label      string
	Arguments  []string
	StdoutPath string
	StderrPath string
	KeepAlive  bool
	RunAtLoad  bool
}

func renderPlist(input renderPlistInput) (string, error) {
	if input.Label == "" {
		return "", fmt.Errorf("label is required")
	}
	if len(input.Arguments) == 0 {
		return "", fmt.Errorf("program arguments are required")
	}
	if input.StdoutPath == "" || input.StderrPath == "" {
		return "", fmt.Errorf("log paths are required")
	}

	escapedLabel := escapeXML(input.Label)
	var argumentsBuilder strings.Builder
	for _, argument := range input.Arguments {
		argumentsBuilder.WriteString("    <string>")
		argumentsBuilder.WriteString(escapeXML(argument))
		argumentsBuilder.WriteString("</string>\n")
	}

	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
%s  </array>
  <key>KeepAlive</key>
  %s
  <key>RunAtLoad</key>
  %s
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, escapedLabel, argumentsBuilder.String(), toPlistBoolean(input.KeepAlive), toPlistBoolean(input.RunAtLoad), escapeXML(input.StdoutPath), escapeXML(input.StderrPath))

	return content, nil
}

func toPlistBoolean(value bool) string {
	if value {
		return "<true/>"
	}
	return "<false/>"
}

func escapeXML(value string) string {
	buffer := &bytes.Buffer{}
	_ = xml.EscapeText(buffer, []byte(value))
	return buffer.String()
}

func writePlist(path string, content string) error {
	directoryPath := filepath.Dir(path)
	if err := os.MkdirAll(directoryPath, 0o755); err != nil {
		return fmt.Errorf("create plist directory %s: %w", directoryPath, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write plist %s: %w", path, err)
	}
	return nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
