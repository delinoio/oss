package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	DaemonLaunchAgentLabel  = "io.delino.devmon.daemon"
	MenubarLaunchAgentLabel = "io.delino.devmon.menubar"
)

func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "devmon", "devmon.toml"), nil
}

func ConfigDirectory() (string, error) {
	configPath, err := ConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(configPath), nil
}

func StatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "devmon", "status.json"), nil
}

func StateDirectory() (string, error) {
	statePath, err := StatePath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(statePath), nil
}

func LogDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, "Library", "Logs", "devmon"), nil
}

func DaemonLogPath() (string, error) {
	logDirectory, err := LogDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(logDirectory, "daemon.log"), nil
}

func MenubarLogPath() (string, error) {
	logDirectory, err := LogDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(logDirectory, "menubar.log"), nil
}

func LaunchAgentsDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents"), nil
}

func DaemonPlistPath() (string, error) {
	launchAgentsDirectory, err := LaunchAgentsDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(launchAgentsDirectory, DaemonLaunchAgentLabel+".plist"), nil
}

func MenubarPlistPath() (string, error) {
	launchAgentsDirectory, err := LaunchAgentsDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(launchAgentsDirectory, MenubarLaunchAgentLabel+".plist"), nil
}
