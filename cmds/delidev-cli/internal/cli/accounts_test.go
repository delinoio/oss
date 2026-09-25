package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

func TestCLIAccountKeylessLifecycleAndReplay(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("server readiness timeout")
	}
	code, value := cliRun(t, root, []string{"provider", "create", "--input", "-"}, `{"name":"local","endpoint":"http://127.0.0.1:11434/v1","protocol":"openai-chat","authentication":"keyless","discovery":false}`)
	if code != 0 {
		t.Fatalf("provider: %+v", value)
	}
	provider := value["result"].(map[string]any)["resource"].(map[string]any)["id"].(string)
	raw, _ := json.Marshal(domain.Account{Alias: "CLI account", ProviderID: domain.ID(provider), Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected})
	code, value = cliRun(t, root, []string{"account", "create", "--input", "-"}, string(raw))
	if code != 0 {
		t.Fatalf("account: %+v", value)
	}
	id := value["result"].(map[string]any)["resource"].(map[string]any)["id"].(string)
	requestID := string(domain.NewID())
	args := []string{"account", "connect", "--id", id, "--revision", "1", "--keyless", "--request-id", requestID}
	code, value = cliRun(t, root, args, "")
	if code != 0 || value["request_id"] != requestID {
		t.Fatalf("connect: %+v", value)
	}
	account := value["result"].(map[string]any)["account"].(map[string]any)
	if account["data"].(map[string]any)["health"] != "unverified" {
		t.Fatal("storage falsely established validation")
	}
	code, value = cliRun(t, root, args, "")
	if code != 0 || value["result"].(map[string]any)["replayed"] != true {
		t.Fatalf("connect replay: %+v", value)
	}
	code, value = cliRun(t, root, []string{"account", "status", "--id", id}, "")
	if code != 0 {
		t.Fatalf("status: %+v", value)
	}
	revision := uint64(value["result"].(map[string]any)["account"].(map[string]any)["revision"].(float64))
	disconnectArgs := []string{"account", "disconnect", "--id", id, "--revision", strconv.FormatUint(revision, 10), "--request-id", string(domain.NewID())}
	code, value = cliRun(t, root, disconnectArgs, "")
	if code != 0 {
		t.Fatalf("disconnect: %+v", value)
	}
	body := value["result"].(map[string]any)["account"].(map[string]any)["data"].(map[string]any)
	if body["health"] != "disconnected" || body["connection"] != nil || body["removal"] != nil {
		t.Fatalf("incomplete disconnect: %+v", body)
	}
	code, value = cliRun(t, root, args, "")
	if code != 0 || value["result"].(map[string]any)["account"].(map[string]any)["data"].(map[string]any)["connection"] != nil {
		t.Fatal("old CLI connect replay recreated connection")
	}
}
func TestAPIKeyInputBoundsLineEndingsAndNoEcho(t *testing.T) {
	for _, ending := range []string{"", "\n", "\r\n"} {
		key, err := readAPIKey(strings.NewReader("private-key" + ending))
		if err != nil || string(key) != "private-key" {
			t.Fatalf("line ending: %v", err)
		}
		clear(key)
	}
	for _, input := range []string{"", "\n", "key\n\n", "key\nother", " key", "key ", "key\r", "key\x00", "keyλ", strings.Repeat("k", domain.MaxAPIKeyBytes+1)} {
		_, err := readAPIKey(strings.NewReader(input))
		if err == nil {
			t.Fatal("invalid input accepted")
		}
		if strings.Contains(err.Error(), input) && len(input) > 5 {
			t.Fatal("error echoed secret input")
		}
	}
	key, err := readAPIKey(strings.NewReader(strings.Repeat("k", domain.MaxAPIKeyBytes) + "\r\n"))
	if err != nil || len(key) != domain.MaxAPIKeyBytes {
		t.Fatalf("maximum key: %v", err)
	}
	clear(key)
}

type unreadableSecretInput struct{ t *testing.T }

func (r unreadableSecretInput) Read([]byte) (int, error) {
	r.t.Fatal("conflicting input consumed stdin")
	return 0, io.EOF
}
func TestAccountSecretAndServerTokenCannotShareStdin(t *testing.T) {
	var out, stderr bytes.Buffer
	code := Run(context.Background(), []string{"--data-dir", filepath.Join(t.TempDir(), "absent"), "--server", "https://example.test", "--token-stdin", "account", "connect", "--id", string(domain.NewID()), "--revision", "1", "--key-stdin"}, IO{In: unreadableSecretInput{t}, Out: &out, Err: &stderr})
	if code != 2 || !bytes.Contains(out.Bytes(), []byte("cannot share stdin")) {
		t.Fatalf("conflicting input: %d %s", code, out.String())
	}
}
