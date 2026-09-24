package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func init() {
	mode := os.Getenv("DELIDEV_CODEX_FIXTURE")
	if mode == "" || os.Args[len(os.Args)-1] != "app-server" {
		return
	}
	// Fixtures implement only non-inference readiness. Any accidental account,
	// thread creation or prompt request fails rather than reaching a real CLI.
	initialized, notified := false, false
	threads := &threadFixture{mode: mode}
	scanner := bufio.NewScanner(os.Stdin)
	write := func(id json.RawMessage, result any) {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": id, "result": result})
	}
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(3)
		}
		switch request.Method {
		case "initialize":
			if initialized {
				os.Exit(4)
			}
			initialized = true
			var params struct {
				ClientInfo   struct{ Name, Title, Version string }
				Capabilities struct{ ExperimentalAPI bool }
			}
			if json.Unmarshal(request.Params, &params) != nil || params.ClientInfo.Name != "delidev" || params.Capabilities.ExperimentalAPI != strings.HasPrefix(mode, "thread-") {
				os.Exit(5)
			}
			platform, family := runtime.GOOS, "unix"
			if platform == "darwin" {
				platform = "macos"
			}
			if platform == "windows" {
				family = "windows"
			}
			home := os.Getenv("CODEX_HOME")
			version := SupportedVersion
			switch mode {
			case "home":
				home = filepath.Join(home, "foreign")
			case "platform":
				platform = "foreign"
			case "version":
				version = "9.9.9"
			}
			response := map[string]any{"codexHome": home, "platformFamily": family, "platformOs": platform, "userAgent": "delidev/" + version + " (fixture)"}
			if mode == "unknown-field" {
				response["newMeaning"] = "unknown"
			}
			write(request.ID, response)
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"method": "remoteControl/status/changed", "params": map[string]bool{"enabled": false}, "emittedAtMs": time.Now().UnixMilli()})
		case "initialized":
			if !initialized || notified || len(request.ID) > 0 {
				os.Exit(6)
			}
			notified = true
		case "thread/loaded/list":
			if !notified {
				os.Exit(7)
			}
			threads := []string{}
			if mode == "existing-thread" {
				threads = append(threads, "foreign-thread")
			}
			write(request.ID, map[string]any{"data": threads, "nextCursor": nil})
		default:
			if strings.HasPrefix(mode, "thread-") && threads.handle(request.ID, request.Method, request.Params, write) {
				continue
			}
			os.Exit(8)
		}
	}
	os.Exit(0)
}
func fixtureConfig(t *testing.T, mode string) Config {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	home := filepath.Join(root, "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	return Config{Version: SupportedVersion, Home: home, Process: process.Config{Directory: filepath.Join(root, "processes"), OwnerID: domain.NewID(), Executable: executable, Cwd: root, Env: []string{"CODEX_HOME=" + home, "DELIDEV_CODEX_FIXTURE=" + mode}}}
}
func TestCodexNativeHandshakeAndCleanup(t *testing.T) {
	config := fixtureConfig(t, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if client.Version() != SupportedVersion {
		t.Fatal("version changed")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
		t.Fatal(err)
	}
}
func TestCodexRejectsChangedNativeRuntime(t *testing.T) {
	for _, mode := range []string{"home", "platform", "version", "unknown-field", "existing-thread"} {
		t.Run(mode, func(t *testing.T) {
			config := fixtureConfig(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := Open(ctx, config)
			if err == nil || domain.SafeError(err).Code != domain.Unsupported {
				t.Fatalf("accepted incompatible native runtime: %v", err)
			}
			if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestCodexUnknownVersionAndForeignHomeNeverLaunch(t *testing.T) {
	for _, change := range []string{"version", "home", "duplicate"} {
		config := fixtureConfig(t, "ready")
		switch change {
		case "version":
			config.Version = "0.152.0"
		case "home":
			config.Process.Env = []string{"CODEX_HOME=" + t.TempDir()}
		case "duplicate":
			config.Process.Env = append(config.Process.Env, "CODEX_HOME="+config.Home)
		}
		_, err := Open(context.Background(), config)
		if err == nil || domain.SafeError(err).Code != domain.Unsupported {
			t.Fatalf("invalid preflight: %v", err)
		}
		if _, err := os.Stat(config.Process.Directory); !os.IsNotExist(err) {
			t.Fatal("invalid profile started a native process")
		}
	}
}
