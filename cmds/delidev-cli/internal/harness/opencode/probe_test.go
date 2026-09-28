package opencode

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Synthetic descriptions/schemas with the pinned native operation identities.
// Actual native tests independently read the complete installed OpenAPI schema.
//
//go:embed testdata/schema.json
var schemaFixture []byte

func init() {
	root := os.Getenv("HOME")
	if len(os.Args) < 2 || os.Args[1] != "serve" || !strings.HasPrefix(filepath.Base(root), "fixture-") {
		return
	}
	if len(os.Args) != 5 || os.Args[2] != "--hostname=127.0.0.1" || !strings.HasPrefix(os.Args[3], "--port=") || os.Args[4] != "--mdns=false" {
		os.Exit(11)
	}
	for _, name := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "XAI_API_KEY", "OPENCODE_CONFIG", "OPENCODE_TEST_HOME", "OPENCODE_TEST_MANAGED_CONFIG_DIR", "OPENCODE_MODELS_URL", "OPENCODE_MODELS_PATH", "OPENCODE_DB", "OPENCODE_CONSOLE_TOKEN", "NODE_OPTIONS", "HTTPS_PROXY", "SSH_AUTH_SOCK", "BASH_ENV"} {
		if os.Getenv(name) != "" {
			os.Exit(12)
		}
	}
	cwd, _ := os.Getwd()
	if cwd != root || os.Getenv("OPENCODE_CONFIG_DIR") != filepath.Join(root, "opencode") || os.Getenv("OPENCODE_DISABLE_AUTOUPDATE") != "true" || os.Getenv("OPENCODE_DISABLE_PROJECT_CONFIG") != "true" || os.Getenv("OPENCODE_DISABLE_MODELS_FETCH") != "true" || os.Getenv("OPENCODE_SERVER_USERNAME") != "delidev" || len(os.Getenv("OPENCODE_SERVER_PASSWORD")) != 43 {
		os.Exit(13)
	}
	mode := strings.TrimPrefix(filepath.Base(root), "fixture-")
	if mode == "exit" {
		os.Exit(0)
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strings.TrimPrefix(os.Args[3], "--port=")))
	if err != nil {
		os.Exit(14)
	}
	announce := "opencode server listening on http://" + listener.Addr().String() + "\n"
	switch mode {
	case "timeout":
		announce = ""
	case "foreign":
		announce = "opencode server listening on http://127.0.0.1:1\n"
	case "duplicate":
		announce += announce
	case "trailing":
		announce += "{"
	case "warning":
		announce = "Warning: private-native-sentinel\n" + announce
	case "stderr":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte{'e'}, maxOutput+1))
	}
	_, _ = os.Stdout.WriteString(announce)
	_, _ = os.Stderr.WriteString("private-native-stderr-sentinel")
	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Header.Get("x-opencode-directory") != "" {
			os.Exit(15)
		}
		username, password, _ := r.BasicAuth()
		if mode != "unauthenticated" && (username != "delidev" || password != os.Getenv("OPENCODE_SERVER_PASSWORD")) {
			w.Header().Set("Www-Authenticate", `Basic realm="Secure Area"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/global/health":
			if mode == "version" {
				_, _ = io.WriteString(w, `{"healthy":true,"version":"foreign"}`)
				return
			}
			_, _ = fmt.Fprintf(w, `{"healthy":true,"version":%q}`, SupportedVersion)
		case "/global/config":
			if mode == "config" {
				_, _ = io.WriteString(w, `{"provider":{"foreign":{}}}`)
				return
			}
			_, _ = io.WriteString(w, `{}`)
		case "/doc":
			if mode == "schema" {
				_, _ = io.WriteString(w, `{}`)
				return
			}
			_, _ = w.Write(schemaFixture)
		default:
			os.Exit(16) // No instance, session, provider or mutation route.
		}
	})}
	_ = server.Serve(listener)
	os.Exit(0)
}

func fixtureConfig(t *testing.T, mode string) (ProbeConfig, *bytes.Buffer) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fixture-"+mode)
	for _, name := range []string{"", "opencode", "config", "cache", "data", "state", "tmp"} {
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
	var paths []string
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.IsAbs(entry) {
			paths = append(paths, entry)
		}
	}
	env := []string{"PATH=" + strings.Join(paths, string(os.PathListSeparator))}
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	logs := &bytes.Buffer{}
	return ProbeConfig{Version: SupportedVersion, Home: filepath.Join(root, "opencode"), Process: process.Config{Directory: filepath.Join(filepath.Dir(root), "processes"), OwnerID: domain.NewID(), Executable: executable, Env: env, Cwd: root, Logger: slog.New(slog.NewJSONHandler(logs, nil))}}, logs
}

func TestProbeOwnsAuthenticatedServerAndCleanup(t *testing.T) {
	for _, test := range []struct {
		mode string
		code domain.Code
	}{
		{"valid", ""}, {"foreign", domain.Unsupported}, {"duplicate", domain.Unsupported}, {"trailing", domain.Unsupported}, {"warning", domain.Unsupported},
		{"stderr", domain.ResourceExhausted}, {"timeout", domain.Unavailable}, {"exit", domain.Unavailable}, {"unauthenticated", domain.Unsupported},
		{"version", domain.Unsupported}, {"config", domain.Unsupported}, {"schema", domain.Unsupported},
	} {
		t.Run(test.mode, func(t *testing.T) {
			config, logs := fixtureConfig(t, test.mode)
			for _, key := range []string{"HOME", "OPENCODE_SERVER_PASSWORD", "OPENAI_API_KEY", "OPENCODE_CONFIG", "OPENCODE_TEST_HOME", "NODE_OPTIONS", "HTTPS_PROXY", "SSH_AUTH_SOCK"} {
				config.Process.Env = append(config.Process.Env, key+"=private-environment-sentinel")
			}
			duration := 5 * time.Second
			if test.mode == "timeout" {
				duration = 500 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), duration)
			defer cancel()
			err := Probe(ctx, config)
			if test.code == "" && err != nil || test.code != "" && (err == nil || domain.SafeError(err).Code != test.code) {
				t.Fatalf("probe: %v, want %s; logs: %s", err, test.code, logs.String())
			}
			if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(logs.String(), "sentinel") || strings.Contains(logs.String(), config.Home) || strings.Contains(logs.String(), "127.0.0.1") {
				t.Fatal("private native details entered logs")
			}
			if err := filepath.WalkDir(config.Process.Directory, func(path string, entry os.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				raw, err := os.ReadFile(path)
				if bytes.Contains(raw, []byte("OPENCODE_SERVER_PASSWORD")) || bytes.Contains(raw, []byte("sentinel")) || bytes.Contains(raw, []byte(config.Home)) {
					t.Fatal("native configuration entered ownership journals")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProbeRejectsExistingRuntimeBeforeLaunch(t *testing.T) {
	for _, change := range []string{"version", "home", "cwd", "config", "auth", "root", "path", "duplicate-path"} {
		t.Run(change, func(t *testing.T) {
			config, _ := fixtureConfig(t, "valid")
			switch change {
			case "version":
				config.Version = "1.18.33"
			case "home":
				config.Home = "relative"
			case "cwd":
				config.Process.Cwd = filepath.Dir(config.Process.Cwd)
			case "config":
				if err := os.WriteFile(filepath.Join(config.Home, "opencode.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "auth":
				if err := os.WriteFile(filepath.Join(config.Process.Cwd, "data", "auth.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "root":
				if err := os.WriteFile(filepath.Join(config.Process.Cwd, "opencode.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "path":
				config.Process.Env = []string{"PATH=."}
			case "duplicate-path":
				config.Process.Env = append(config.Process.Env, "Path=/bin")
			}
			if err := Probe(context.Background(), config); err == nil {
				t.Fatal("invalid runtime accepted")
			}
			if _, err := os.Lstat(config.Process.Directory); !os.IsNotExist(err) {
				t.Fatal("invalid runtime launched a process")
			}
		})
	}
}

func TestManualNativeOpenCodeInitialization(t *testing.T) {
	executable := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit private OpenCode initialization")
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
}
