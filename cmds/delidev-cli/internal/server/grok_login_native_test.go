// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bufio"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// nativeGrok runs the native binary against a scratch home and returns its
// combined output. Credential values never appear in the arguments.
func nativeGrok(ctx context.Context, binary, home string, input string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, binary, append([]string{"--no-auto-update"}, args...)...)
	command.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "GROK_HOME="+home, "GROK_DISABLE_AUTOUPDATER=1", "CI=1", "NO_COLOR=1")
	command.Dir = home
	if input != "" {
		command.Stdin = strings.NewReader(input)
	}
	output, err := command.CombinedOutput()
	return string(output), err
}

// TestManualNativeGrokLoginBundleIsAcceptedByNativeGrok is the opt-in real
// acceptance run of the Go-owned browser login. It needs a native Grok Build
// binary and the account owner's interactive approval, so it skips unless both
// are explicitly supplied:
//
//	DELIDEV_NATIVE_GROK_EXECUTABLE=/path/to/grok
//	DELIDEV_NATIVE_GROK_ACCOUNT_OWNER_APPROVAL=1
//
// It runs the production login code against the real issuer with a scratch
// home, writes the Go-produced auth.json there, requires `grok models` and ACP
// `initialize` (cached_token) to accept it, then removes it with `grok logout`.
// The real ~/.grok is never read or written.
func TestManualNativeGrokLoginBundleIsAcceptedByNativeGrok(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if binary == "" || os.Getenv("DELIDEV_NATIVE_GROK_ACCOUNT_OWNER_APPROVAL") != "1" {
		t.Skip("explicit native Grok Build binary and account owner approval required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client := subscription.GrokOAuthClient{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	endpoints, err := client.Discover(ctx)
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	authorization, err := subscription.NewGrokAuthorization(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer authorization.Clear()
	flow, err := newGrokLoginFlow(domain.NewID(), authorization.State)
	if err != nil {
		t.Fatal(err)
	}
	defer flow.close()
	redirect := subscription.GrokRedirectURI(flow.port)
	t.Logf("The account owner opens this URL now (single use): %s", endpoints.AuthorizationURL(redirect, authorization))
	var callback grokCallback
	select {
	case callback = <-flow.callback:
	case <-ctx.Done():
		t.Fatal("the account owner did not approve in time")
	}
	flow.close()
	if callback.denied {
		t.Fatal("the account owner denied the request")
	}
	tokens, err := client.ExchangeCode(ctx, endpoints, callback.code, authorization.Verifier, redirect)
	clear(callback.code)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	claims, err := client.ValidateIDToken(ctx, endpoints, tokens.IDToken, authorization.Nonce)
	if err != nil {
		t.Fatalf("ID token: %v", err)
	}
	info, err := client.UserInfo(ctx, endpoints, tokens.AccessToken, claims)
	if err != nil {
		t.Fatalf("userinfo: %v", err)
	}
	bundle, err := subscription.BuildGrokBundle(time.Now(), tokens, claims, info)
	if err != nil {
		t.Fatalf("mapping: %v", err)
	}
	defer bundle.Clear()
	home := t.TempDir()
	file, err := bundle.AuthFile()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(file)
	authPath := filepath.Join(home, "auth.json")
	if err := os.WriteFile(authPath, file, 0o600); err != nil {
		t.Fatal(err)
	}
	// Always remove the scratch credential, even when an assertion fails.
	defer func() {
		_, _ = nativeGrok(context.Background(), binary, home, "", "logout")
		_ = os.Remove(authPath)
	}()
	models, err := nativeGrok(ctx, binary, home, "", "models")
	if err != nil || !strings.Contains(strings.ToLower(models), "logged in") {
		t.Fatalf("grok models did not report a signed-in account: %v", err)
	}
	initialize, err := nativeGrok(ctx, binary, home, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}`+"\n", "agent", "stdio")
	if err != nil && !strings.Contains(initialize, "authMethods") {
		t.Fatalf("ACP initialize: %v", err)
	}
	scanner := bufio.NewScanner(strings.NewReader(initialize))
	found := false
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "cached_token") {
			found = true
		}
	}
	if !found {
		t.Fatal("ACP initialize did not offer the cached_token method")
	}
	if _, err := nativeGrok(ctx, binary, home, "", "logout"); err != nil {
		t.Fatalf("grok logout: %v", err)
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatal("grok logout left the credential file")
	}
}
