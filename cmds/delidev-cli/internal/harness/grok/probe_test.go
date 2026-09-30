package grok

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// These sanitized fixtures preserve the actual pinned native field shapes.
// No user configuration, hostname, runtime path or credential is embedded.
//
//go:embed testdata/initialize.json
var initializeFixture []byte

//go:embed testdata/inspect.json
var inspectionFixture []byte

const inventoryFixture = `{"jsonrpc":"2.0","method":"_x.ai/mcp/servers_updated","params":{"mcpServers":[]}}` + "\n"

func fixtureObject(raw []byte) map[string]any {
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		panic("invalid native fixture")
	}
	return object
}

func fixtureResponse(id domain.ID, cwd string) []byte {
	result := fixtureObject(initializeFixture)
	result["_meta"].(map[string]any)["currentWorkingDirectory"] = cwd
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	return append(raw, '\n')
}

func init() {
	apiFixtureProcess()
	root := os.Getenv("HOME")
	if len(os.Args) < 3 || os.Args[1] != "--no-auto-update" || !strings.HasPrefix(filepath.Base(root), "fixture-") {
		return
	}
	for _, key := range []string{"XAI_API_KEY", "GROK_CONFIG", "GROK_CONFIG_PATH", "GROK_LOG_FILE", "GROK_AGENT", "GROK_OIDC_ISSUER", "NODE_OPTIONS", "HTTPS_PROXY", "SSH_AUTH_SOCK", "BASH_ENV", "RUST_LOG"} {
		if os.Getenv(key) != "" {
			os.Exit(11)
		}
	}
	cwd, _ := os.Getwd()
	if cwd != root || os.Getenv("GROK_HOME") != filepath.Join(root, "grok") || os.Getenv("GROK_DISABLE_AUTOUPDATER") != "1" || os.Getenv("GROK_XAI_API_BASE_URL") != "http://127.0.0.1:1" {
		os.Exit(12)
	}
	for _, vendor := range []string{"CLAUDE", "CURSOR"} {
		for _, surface := range []string{"SKILLS", "RULES", "AGENTS", "MCPS", "HOOKS"} {
			if os.Getenv("GROK_"+vendor+"_"+surface+"_ENABLED") != "0" {
				os.Exit(13)
			}
		}
	}
	mode := strings.TrimPrefix(filepath.Base(root), "fixture-")
	if os.Args[2] == "inspect" {
		if !reflect.DeepEqual(os.Args[1:], []string{"--no-auto-update", "inspect", "--json"}) {
			os.Exit(14)
		}
		report := fixtureObject(inspectionFixture)
		report["cwd"] = root
		switch mode {
		case "inspect-managed":
			report["permissions"].(map[string]any)["managedSettingsExists"] = true
		case "inspect-hooks":
			report["hooks"] = []any{map[string]any{"command": "private-hook-sentinel"}}
		case "inspect-overflow":
			_, _ = os.Stdout.Write(bytes.Repeat([]byte{'x'}, maxProbeStderr+1))
			os.Exit(0)
		case "inspect-stderr":
			_, _ = os.Stderr.Write(bytes.Repeat([]byte{'e'}, maxProbeStderr+1))
		case "inspect-exit":
			os.Exit(2)
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
		os.Exit(0)
	}
	if !reflect.DeepEqual(os.Args[1:], []string{"--no-auto-update", "agent", "stdio"}) {
		os.Exit(15)
	}
	_ = os.WriteFile(filepath.Join(root, "tmp", "acp-started"), []byte("started"), 0600)
	if mode == "exit" {
		os.Exit(0)
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		os.Exit(16)
	}
	var request struct {
		JSONRPC string           `json:"jsonrpc"`
		ID      domain.ID        `json:"id"`
		Method  string           `json:"method"`
		Params  initializeParams `json:"params"`
	}
	if decode(scanner.Bytes(), &request) != nil || request.JSONRPC != "2.0" || request.ID.Validate() != nil || request.Method != "initialize" || request.Params.ProtocolVersion != 1 || request.Params.ClientInfo.Name != "delidev" {
		os.Exit(17)
	}
	id := request.ID
	if mode == "foreign" {
		id = domain.NewID()
	}
	raw := fixtureResponse(id, root)
	if mode == "valid" || mode == "trailing" {
		raw = append(raw, inventoryFixture...)
	}
	switch mode {
	case "error":
		raw, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": "private-native-error-sentinel", "data": map[string]string{"secret": "private-value-sentinel"}}})
		raw = append(raw, '\n')
	case "duplicate":
		raw = append(raw, raw...)
	case "trailing":
		raw = append(raw, '{')
	case "request":
		raw = []byte(`{"jsonrpc":"2.0","id":"foreign","method":"session/request_permission","params":{}}` + "\n")
	case "notification":
		raw = []byte(`{"jsonrpc":"2.0","method":"session/update","params":{}}` + "\n")
	case "inventory-first":
		raw = append([]byte(inventoryFixture), raw...)
	case "inventory-duplicate":
		raw = append(raw, (inventoryFixture + inventoryFixture)...)
	case "inventory-nonempty":
		raw = append(raw, strings.Replace(inventoryFixture, `[]`, `["foreign"]`, 1)...)
	case "inventory-missing":
		// Keep only the successful response; it cannot complete this profile.
	case "oversize":
		raw = bytes.Repeat([]byte{'x'}, maxProbeFrame+1)
	case "stderr":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte{'e'}, maxProbeStderr+1))
	case "timeout":
		raw = nil
	}
	_, _ = os.Stderr.WriteString("private-stderr-sentinel")
	if mode == "valid" {
		for _, b := range raw {
			_, _ = os.Stdout.Write([]byte{b})
		}
	} else {
		_, _ = os.Stdout.Write(raw)
	}
	if scanner.Scan() {
		os.Exit(18) // No authenticate, session/new, prompt or callback reply.
	}
	os.Exit(0)
}

// Fixture reads may overlap final diagnostics from the owned process. Protect
// both operations; the slog handler's writer lock does not cover String reads.
type fixtureLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *fixtureLogBuffer) Write(raw []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(raw)
}

func (b *fixtureLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func fixtureConfig(t *testing.T, mode string) (ProbeConfig, *fixtureLogBuffer) {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "fixture-"+mode)
	for _, name := range []string{"", "grok", "config", "cache", "data", "state", "tmp"} {
		if err := security.PrivateDir(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var path []string
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.IsAbs(entry) {
			path = append(path, entry)
		}
	}
	env := []string{"PATH=" + strings.Join(path, string(os.PathListSeparator))}
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	logs := &fixtureLogBuffer{}
	return ProbeConfig{Version: SupportedVersion, Home: filepath.Join(root, "grok"), Process: process.Config{Directory: filepath.Join(filepath.Dir(root), "processes"), OwnerID: domain.NewID(), Executable: executable, Env: env, Cwd: root, Logger: slog.New(slog.NewJSONHandler(logs, nil))}}, logs
}

func TestProbeOwnsBoundedInspectedInitialization(t *testing.T) {
	for _, test := range []struct {
		mode string
		code domain.Code
	}{
		{"valid", ""}, {"foreign", domain.Unsupported}, {"error", domain.Unavailable},
		{"duplicate", domain.Unsupported}, {"trailing", domain.Unsupported}, {"request", domain.Unsupported}, {"notification", domain.Unsupported},
		{"oversize", domain.ResourceExhausted}, {"stderr", domain.ResourceExhausted}, {"exit", domain.Unavailable}, {"timeout", domain.Unavailable},
		{"inventory-first", domain.Unsupported}, {"inventory-duplicate", domain.Unsupported}, {"inventory-nonempty", domain.Unsupported}, {"inventory-missing", domain.Unavailable},
		{"inspect-managed", domain.Unsupported}, {"inspect-hooks", domain.Unsupported}, {"inspect-overflow", domain.ResourceExhausted}, {"inspect-stderr", domain.ResourceExhausted}, {"inspect-exit", domain.Unavailable},
	} {
		t.Run(test.mode, func(t *testing.T) {
			config, logs := fixtureConfig(t, test.mode)
			for _, name := range []string{"XAI_API_KEY", "GROK_CONFIG", "GROK_CONFIG_PATH", "GROK_LOG_FILE", "GROK_AGENT", "GROK_OIDC_ISSUER", "NODE_OPTIONS", "HTTPS_PROXY", "SSH_AUTH_SOCK", "BASH_ENV", "RUST_LOG", "HOME", "GROK_HOME"} {
				config.Process.Env = append(config.Process.Env, name+"=private-environment-sentinel")
			}
			// Use Probe's existing whole-operation budget: Windows inspection and
			// ACP startup each launch an owned subprocess, so a shorter caller
			// deadline can expire before a malformed protocol frame is observed.
			timeout := 10 * time.Second
			if test.mode == "timeout" || test.mode == "inventory-missing" {
				timeout = 250 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			err := Probe(ctx, config)
			if test.code == "" && err != nil || test.code != "" && (err == nil || domain.SafeError(err).Code != test.code) {
				t.Fatalf("probe outcome: %v; want %s; logs: %s", err, test.code, logs.String())
			}
			if strings.HasPrefix(test.mode, "inspect-") {
				if _, err := os.Lstat(filepath.Join(config.Process.Cwd, "tmp", "acp-started")); !os.IsNotExist(err) {
					t.Fatal("failed configuration inspection launched ACP")
				}
			}
			if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(logs.String(), "sentinel") || strings.Contains(logs.String(), config.Home) || strings.Contains(logs.String(), "Private command") {
				t.Fatal("native content entered logs")
			}
			if err := filepath.WalkDir(config.Process.Directory, func(path string, entry os.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				raw, err := os.ReadFile(path)
				if bytes.Contains(raw, []byte("sentinel")) || bytes.Contains(raw, []byte(config.Home)) {
					t.Fatal("private native facts entered ownership journals")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProbeRejectsProfileAndRuntimeBeforeLaunch(t *testing.T) {
	for _, change := range []string{"version", "cwd", "home", "retained-config", "retained-cache", "project-config", "root-file", "path", "duplicate-path", "nonprivate", "symlink"} {
		t.Run(change, func(t *testing.T) {
			if runtime.GOOS == "windows" && (change == "nonprivate" || change == "symlink") {
				t.Skip("Unix mode/link fixture; Windows uses native ACLs")
			}
			config, _ := fixtureConfig(t, "valid")
			switch change {
			case "version":
				config.Version = "1.0.42"
			case "cwd":
				config.Process.Cwd = filepath.Dir(config.Process.Cwd)
			case "home":
				config.Home = "relative"
			case "retained-config":
				if err := os.WriteFile(filepath.Join(config.Home, "auth.json"), []byte("private"), 0600); err != nil {
					t.Fatal(err)
				}
			case "retained-cache":
				if err := os.WriteFile(filepath.Join(config.Process.Cwd, "cache", "old"), []byte("private"), 0600); err != nil {
					t.Fatal(err)
				}
			case "project-config":
				if err := os.Mkdir(filepath.Join(config.Process.Cwd, ".grok"), 0700); err != nil {
					t.Fatal(err)
				}
			case "root-file":
				if err := os.WriteFile(filepath.Join(config.Process.Cwd, "AGENTS.md"), []byte("private"), 0600); err != nil {
					t.Fatal(err)
				}
			case "path":
				config.Process.Env = []string{"PATH=."}
			case "duplicate-path":
				config.Process.Env = append(config.Process.Env, "Path=/bin")
			case "nonprivate":
				if err := os.Chmod(config.Home, 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(config.Home); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), config.Home); err != nil {
					t.Fatal(err)
				}
			}
			if err := Probe(context.Background(), config); err == nil {
				t.Fatal("invalid runtime accepted")
			}
			if _, err := os.Lstat(config.Process.Directory); !os.IsNotExist(err) {
				t.Fatal("invalid runtime launched a native process")
			}
		})
	}
}

func TestManualNativeGrokInitialization(t *testing.T) {
	executable := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit private native Grok Build initialization")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("select an absolute native executable")
	}
	config, logs := fixtureConfig(t, "native")
	config.Process.Executable = executable
	if err := Probe(context.Background(), config); err != nil {
		t.Fatalf("native probe: %v; logs: %s", err, logs.String())
	}
	if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(config.Home, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("probe created authentication state")
	}
}
