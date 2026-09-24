package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

// Explicitly opt in only inside the disposable Secret Service container. This
// runs the real CLI/server executable, native OS storage and SQLite together,
// without reading a host login or calling an external provider/inference API.
func TestNativeAccountCLISecretService(t *testing.T) {
	if os.Getenv("DELIDEV_TEST_ISOLATED_SECRET_SERVICE") != "1" {
		t.Skip("requires disposable native credential session")
	}
	binary := os.Getenv("DELIDEV_TEST_CLI_BINARY")
	if !filepath.IsAbs(binary) {
		t.Skip("requires the explicitly built native CLI")
	}
	root := filepath.Join(t.TempDir(), "server")
	secret := "temporary-native-account-cli-api-key"
	var providerCalls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		if r.URL.Path != "/v1/models" || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer "+secret || r.Header.Get("HTTP-Referer") != "https://deli.dev" {
			t.Error("invalid native provider request")
		}
		fmt.Fprint(w, `{"data":[{"id":"native-fixture-model"}]}`)
	}))
	defer providerServer.Close()
	run := func(input string, args ...string) (int, map[string]any) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, append([]string{"--data-dir", root}, args...)...)
		command.Stdin = strings.NewReader(input)
		output, err := command.CombinedOutput()
		if bytes.Contains(output, []byte(secret)) || bytes.Contains(output, []byte(base64.StdEncoding.EncodeToString([]byte(secret)))) {
			t.Fatal("CLI exposed the secret in output")
		}
		exit := 0
		if err != nil {
			if status, ok := err.(*exec.ExitError); ok {
				exit = status.ExitCode()
			} else {
				t.Fatal("CLI process did not complete", err)
			}
		}
		var envelope map[string]any
		if json.Unmarshal(output, &envelope) != nil {
			t.Fatalf("CLI returned invalid structured output (exit %d)", exit)
		}
		return exit, envelope
	}
	var stop context.CancelFunc
	var done chan error
	var logs bytes.Buffer
	start := func() {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		done = make(chan error, 1)
		command := exec.CommandContext(ctx, binary, "--data-dir", root, "server", "start", "--foreground", "--listen", "127.0.0.1:0")
		command.Stdout = &logs
		command.Stderr = &logs
		if err := command.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		stop = cancel
		go func() { done <- command.Wait() }()
		deadline := time.NewTimer(10 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case err := <-done:
				cancel()
				stop = nil
				t.Fatalf("native server terminated before readiness: %v", err)
			case <-deadline.C:
				cancel()
				<-done
				stop = nil
				t.Fatal("native server readiness deadline")
			case <-tick.C:
				if _, err := server.LoadEndpoint(root); err == nil {
					return
				}
			}
		}
	}
	shutdown := func() {
		t.Helper()
		if stop == nil {
			return
		}
		code, _ := run("", "server", "stop")
		if code != 0 {
			stop()
		}
		select {
		case err := <-done:
			if err != nil {
				t.Error("native server shutdown", err)
			}
		case <-time.After(10 * time.Second):
			stop()
			<-done
			t.Error("native server shutdown deadline")
		}
		stop()
		stop = nil
		if bytes.Contains(logs.Bytes(), []byte(secret)) {
			t.Error("server diagnostic exposed the secret")
		}
	}
	defer shutdown()
	start()
	providerJSON, _ := json.Marshal(domain.Provider{Name: "native test", Endpoint: providerServer.URL + "/v1", Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth, Discovery: false})
	code, value := run(string(providerJSON), "provider", "create", "--input", "-")
	if code != 0 {
		t.Fatalf("provider creation: %+v", value)
	}
	provider := value["result"].(map[string]any)["resource"].(map[string]any)["id"].(string)
	raw, _ := json.Marshal(domain.Account{Alias: "native test", ProviderID: domain.ID(provider), Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected})
	code, value = run(string(raw), "account", "create", "--input", "-")
	if code != 0 {
		t.Fatalf("account creation: %+v", value)
	}
	id := value["result"].(map[string]any)["resource"].(map[string]any)["id"].(string)
	requestID := string(domain.NewID())
	connectArgs := []string{"account", "connect", "--id", id, "--revision", "1", "--key-stdin", "--request-id", requestID}
	code, value = run(secret+"\n", connectArgs...)
	if code != 0 {
		t.Fatalf("native connect: %+v", value)
	}
	body := value["result"].(map[string]any)["account"].(map[string]any)["data"].(map[string]any)
	if body["health"] != "unverified" || body["connection"] == nil {
		t.Fatal("native connect readiness was fabricated")
	}
	shutdown()
	start()
	code, value = run(secret+"\n", connectArgs...)
	if code != 0 || value["result"].(map[string]any)["replayed"] != true {
		t.Fatalf("native restart replay: %+v", value)
	}
	current := value["result"].(map[string]any)["account"].(map[string]any)
	validationArgs := []string{"account", "validate", "--id", id, "--revision", strconv.FormatUint(uint64(current["revision"].(float64)), 10), "--request-id", string(domain.NewID())}
	code, value = run("", validationArgs...)
	if code != 6 {
		t.Fatalf("custom public catalog must retain unverified credentials: %+v", value)
	}
	current = value["result"].(map[string]any)["account"].(map[string]any)
	observation := value["result"].(map[string]any)["validation"].(map[string]any)
	if observation["model_count"] != float64(1) || observation["authentication"] != "unknown" || current["data"].(map[string]any)["health"] != "unverified" {
		t.Fatal("native validation invented credential evidence")
	}
	code, value = run("", validationArgs...)
	if code != 6 || value["result"].(map[string]any)["replayed"] != true || providerCalls.Load() != 1 {
		t.Fatal("native validation replay repeated network")
	}
	code, value = run("", "account", "disconnect", "--id", id, "--revision", strconv.FormatUint(uint64(current["revision"].(float64)), 10))
	if code != 0 {
		t.Fatalf("native disconnect: %+v", value)
	}
	current = value["result"].(map[string]any)["account"].(map[string]any)
	body = current["data"].(map[string]any)
	if body["health"] != "disconnected" || body["connection"] != nil || body["removal"] != nil {
		t.Fatal("native cleanup not complete")
	}
	code, value = run(secret+"\n", connectArgs...)
	if code != 0 || value["result"].(map[string]any)["account"].(map[string]any)["data"].(map[string]any)["connection"] != nil {
		t.Fatal("old native request resurrected the connection")
	}
	code, value = run("", "account", "delete", "--id", id, "--revision", strconv.FormatUint(uint64(current["revision"].(float64)), 10))
	if code != 0 {
		t.Fatalf("native deletion: %+v", value)
	}
	code, _ = run(secret+"\n", connectArgs...)
	if code != 8 {
		t.Fatal("deleted account was recreated by its old request")
	}
	shutdown()
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString([]byte(secret)))) {
			t.Error("secret persisted outside OS-backed encryption")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
