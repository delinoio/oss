// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

func TestCLIHeadlessOAuthStartStatusCancelAndOriginalReplay(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
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
		t.Fatal("OAuth server fixture readiness timeout")
	}
	for _, protocol := range []string{"openai-responses", "openai-chat", "anthropic-messages"} {
		code, inventory := cliRun(t, root, []string{"provider", "inventory", "--query", "OpenRouter"}, "")
		if code != 0 {
			t.Fatal(inventory)
		}
		entry := inventory["result"].(map[string]any)["entries"].([]any)[0].(map[string]any)
		args := []string{"account", "oauth", "start", "--provider-id", entry["provider_id"].(string), "--revision", "1", "--api-protocol", protocol}
		code, selected := cliRun(t, root, args, "")
		if code != 0 {
			t.Fatal(selected)
		}
		attempt := selected["result"].(map[string]any)["attempt"].(map[string]any)
		if attempt["api_protocol"] == nil {
			t.Fatal("CLI omitted selected protocol", selected)
		}
		code, canceled := cliRun(t, root, []string{"account", "oauth", "cancel", "--attempt-id", attempt["id"].(string), "--revision", "1"}, "")
		if code != 0 {
			t.Fatal(canceled)
		}
	}

	code, value := cliRun(t, root, []string{"provider", "inventory", "--query", "OpenRouter"}, "")
	if code != 0 {
		t.Fatal(value)
	}
	entry := value["result"].(map[string]any)["entries"].([]any)[0].(map[string]any)
	provider := entry["provider_id"].(string)
	request := string(domain.NewID())
	args := []string{"account", "oauth", "start", "--provider-id", provider, "--revision", "1", "--request-id", request}
	code, value = cliRun(t, root, args, "")
	if code != 0 {
		t.Fatal(value)
	}
	result := value["result"].(map[string]any)
	authorization := result["authorization_url"].(string)
	u, err := url.Parse(authorization)
	if err != nil || u.Host != "openrouter.ai" || u.Path != "/auth" || u.Query().Has("callback_url") || u.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("CLI was not headless S256")
	}
	id := result["attempt"].(map[string]any)["id"].(string)
	code, value = cliRun(t, root, args, "")
	if code != 0 || value["result"].(map[string]any)["authorization_url"] != authorization || value["result"].(map[string]any)["replayed"] != true {
		t.Fatal("start replay changed authority", value)
	}
	code, value = cliRun(t, root, []string{"account", "oauth", "status", "--attempt-id", id}, "")
	if code != 0 || strings.Contains(string(oauthCLIJSON(t, value)), "authorization_url") {
		t.Fatal("status returned browser authority", value)
	}
	code, value = cliRun(t, root, []string{"account", "oauth", "complete", "--attempt-id", id, "--revision", "1", "--recover"}, "")
	if code == 0 || value["error"].(map[string]any)["code"] != "missing_input" {
		t.Fatal("new recovery acquired exchange authority", value)
	}
	code, value = cliRun(t, root, []string{"account", "oauth", "complete", "--attempt-id", id, "--revision", "1", "--code", "secret-argv"}, "")
	if code == 0 || strings.Contains(string(oauthCLIJSON(t, value)), "secret-argv") {
		t.Fatal("secret argv accepted or echoed", value)
	}
	cancelArgs := []string{"account", "oauth", "cancel", "--attempt-id", id, "--revision", "1", "--request-id", string(domain.NewID())}
	code, value = cliRun(t, root, cancelArgs, "")
	if code != 0 {
		t.Fatal(value)
	}
	code, value = cliRun(t, root, cancelArgs, "")
	if code != 0 || value["result"].(map[string]any)["replayed"] != true {
		t.Fatal("cancel replay failed", value)
	}
}
func TestOAuthCodeStdinPreservesExactBytesAndRejectsLineEndings(t *testing.T) {
	for _, text := range []string{" exact code ", "unicode-한글-code"} {
		value, err := readOAuthCode(strings.NewReader(text))
		if err != nil || string(value) != text {
			t.Fatal("opaque code changed", err)
		}
		clear(value)
	}
	for _, text := range []string{"", "code\n", "code\r\n", "code\x00", string([]byte{0xff}), strings.Repeat("x", 8193)} {
		_, err := readOAuthCode(strings.NewReader(text))
		if domain.SafeError(err).Code != domain.InvalidArgument {
			t.Fatal("invalid code accepted", err)
		}
	}
}

func oauthCLIJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestOAuthCallbackEnvelopeIsBoundedAndExcludesMutationAuthority(t *testing.T) {
	for _, raw := range []string{`{"authorizationState":"c3RhdGU="}`, `{"mutation":{},"authorizationCode":"Y29kZQ==","authorizationState":"` + strings.Repeat("c3Nz", 14) + `"}`, `{"authorizationCode":"Y29kZQ==","authorizationState":"` + strings.Repeat("c3Nz", 14) + `","extra":"ignored"}`, strings.Repeat("x", 16385)} {
		code, state, e := readOAuthCallback(strings.NewReader(raw))
		clear(code)
		clear(state)
		if e == nil {
			t.Fatal("invalid callback envelope admitted")
		}
	}
	raw := `{"authorizationCode":"Y29kZQ==","authorizationState":"` + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 43))) + `"}`
	code, state, e := readOAuthCallback(strings.NewReader(raw))
	defer clear(code)
	defer clear(state)
	if e != nil || string(code) != "code" || string(state) != strings.Repeat("s", 43) {
		t.Fatal("original callback bytes changed")
	}
}
