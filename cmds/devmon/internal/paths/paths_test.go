package paths

import (
	"path/filepath"
	"testing"
)

func TestPathFunctionsAreConsistentWithHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configPath, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath returned error: %v", err)
	}
	expectedConfigPath := filepath.Join(home, ".config", "devmon", "devmon.toml")
	if configPath != expectedConfigPath {
		t.Fatalf("expected config path=%s, got=%s", expectedConfigPath, configPath)
	}

	configDirectory, err := ConfigDirectory()
	if err != nil {
		t.Fatalf("ConfigDirectory returned error: %v", err)
	}
	if configDirectory != filepath.Dir(expectedConfigPath) {
		t.Fatalf("expected config dir=%s, got=%s", filepath.Dir(expectedConfigPath), configDirectory)
	}

	statePath, err := StatePath()
	if err != nil {
		t.Fatalf("StatePath returned error: %v", err)
	}
	expectedStatePath := filepath.Join(home, ".local", "state", "devmon", "status.json")
	if statePath != expectedStatePath {
		t.Fatalf("expected state path=%s, got=%s", expectedStatePath, statePath)
	}

	stateDirectory, err := StateDirectory()
	if err != nil {
		t.Fatalf("StateDirectory returned error: %v", err)
	}
	if stateDirectory != filepath.Dir(expectedStatePath) {
		t.Fatalf("expected state dir=%s, got=%s", filepath.Dir(expectedStatePath), stateDirectory)
	}

	logDirectory, err := LogDirectory()
	if err != nil {
		t.Fatalf("LogDirectory returned error: %v", err)
	}
	expectedLogDirectory := filepath.Join(home, "Library", "Logs", "devmon")
	if logDirectory != expectedLogDirectory {
		t.Fatalf("expected log dir=%s, got=%s", expectedLogDirectory, logDirectory)
	}

	daemonLogPath, err := DaemonLogPath()
	if err != nil {
		t.Fatalf("DaemonLogPath returned error: %v", err)
	}
	expectedDaemonLogPath := filepath.Join(expectedLogDirectory, "daemon.log")
	if daemonLogPath != expectedDaemonLogPath {
		t.Fatalf("expected daemon log path=%s, got=%s", expectedDaemonLogPath, daemonLogPath)
	}

	menubarLogPath, err := MenubarLogPath()
	if err != nil {
		t.Fatalf("MenubarLogPath returned error: %v", err)
	}
	expectedMenubarLogPath := filepath.Join(expectedLogDirectory, "menubar.log")
	if menubarLogPath != expectedMenubarLogPath {
		t.Fatalf("expected menubar log path=%s, got=%s", expectedMenubarLogPath, menubarLogPath)
	}

	launchAgentsDirectory, err := LaunchAgentsDirectory()
	if err != nil {
		t.Fatalf("LaunchAgentsDirectory returned error: %v", err)
	}
	expectedLaunchAgentsDirectory := filepath.Join(home, "Library", "LaunchAgents")
	if launchAgentsDirectory != expectedLaunchAgentsDirectory {
		t.Fatalf("expected launch agents dir=%s, got=%s", expectedLaunchAgentsDirectory, launchAgentsDirectory)
	}

	daemonPlistPath, err := DaemonPlistPath()
	if err != nil {
		t.Fatalf("DaemonPlistPath returned error: %v", err)
	}
	expectedDaemonPlistPath := filepath.Join(expectedLaunchAgentsDirectory, DaemonLaunchAgentLabel+".plist")
	if daemonPlistPath != expectedDaemonPlistPath {
		t.Fatalf("expected daemon plist path=%s, got=%s", expectedDaemonPlistPath, daemonPlistPath)
	}

	menubarPlistPath, err := MenubarPlistPath()
	if err != nil {
		t.Fatalf("MenubarPlistPath returned error: %v", err)
	}
	expectedMenubarPlistPath := filepath.Join(expectedLaunchAgentsDirectory, MenubarLaunchAgentLabel+".plist")
	if menubarPlistPath != expectedMenubarPlistPath {
		t.Fatalf("expected menubar plist path=%s, got=%s", expectedMenubarPlistPath, menubarPlistPath)
	}
}
