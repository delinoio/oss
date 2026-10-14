// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package tailscale

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const (
	loginMessage  = "Tailscale login is required."
	loginGuidance = "Open Tailscale on this computer and sign in. Then refresh Tailscale devices and inspect the original access request."
	genericText   = "Tailscale is unavailable or its observation is unsupported."
)

// installFakeTailscale places an executable `tailscale` script first on PATH.
// It prints the given status JSON for `status --json` and records every other
// invocation so tests can prove that no Serve child was started.
func installFakeTailscale(t *testing.T, statusJSON string) (invocations string) {
	t.Helper()
	dir := t.TempDir()
	invocations = filepath.Join(dir, "invocations")
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = \"status\" ] && [ \"$2\" = \"--json\" ]; then\n" +
		"printf '%s\\n' '" + statusJSON + "'\n" +
		"exit 0\nfi\n" +
		"printf '%s\\n' \"$*\" >> '" + invocations + "'\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "tailscale"), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return invocations
}

func newLoginManager(t *testing.T, logs *bytes.Buffer) (*Manager, domain.ID) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "tailnet")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	server := domain.NewID()
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	return &Manager{adapter: Adapter{Root: root, Logger: logger}, parent: context.Background(), server: server, state: Off, value: accessIntent{Version: 1, ServerID: server}}, server
}

func TestEnableReportsExplicitLoginRequirement(t *testing.T) {
	invocations := installFakeTailscale(t, `{"Version":"fixture","BackendState":"NeedsLogin"}`)
	var logs bytes.Buffer
	manager, _ := newLoginManager(t, &logs)
	id := domain.NewID()
	got, err := manager.Set(context.Background(), id, true)
	if err == nil {
		t.Fatal("logged-out client enabled access")
	}
	safe := domain.SafeError(err)
	if safe.Code != domain.Unavailable || safe.Message != loginMessage || safe.Guidance != loginGuidance {
		t.Fatal(safe)
	}
	if !got.Enabled || got.State != SetupRequired {
		t.Fatal(got)
	}
	if manager.value.Request != id || len(manager.value.Receipts) != 1 || manager.value.Receipts[0] != (accessReceipt{ID: id, Enabled: true}) {
		t.Fatal("enabled receipt and request ID were not retained", manager.value)
	}
	if manager.http != nil || manager.listener != nil || manager.child != nil || manager.cancel != nil || manager.value.Owner != "" {
		t.Fatal("ingress listener or Serve child was started")
	}
	if raw, readErr := os.ReadFile(invocations); readErr == nil {
		t.Fatal("unexpected native invocation", string(raw))
	}
	if !strings.Contains(logs.String(), "login_required") || strings.Contains(logs.String(), "fixture") {
		t.Fatal("login log missing or unsanitized", logs.String())
	}
	// The original request is replayable and still reports the same failure.
	_, err = manager.Set(context.Background(), id, true)
	if again := domain.SafeError(err); again.Message != loginMessage {
		t.Fatal(again)
	}
}

func TestEnableKeepsGenericFailureForOtherStates(t *testing.T) {
	for _, status := range []string{
		`{"Version":"fixture","BackendState":"NeedsMachineAuth"}`,
		`{"Version":"fixture","BackendState":"Stopped"}`,
		`{"Version":"fixture","BackendState":"Starting"}`,
		`{"Version":"fixture","BackendState":"NoState"}`,
		`{"Version":"fixture","BackendState":"Running","CertDomains":[],"Self":{"ID":"self","HostName":"Self","DNSName":"self.example.ts.net.","Online":true,"CapMap":{}},"Peer":{}}`,
		`{"Version":"fixture","BackendState":"Surprise"}`,
	} {
		installFakeTailscale(t, status)
		var logs bytes.Buffer
		manager, _ := newLoginManager(t, &logs)
		_, err := manager.Set(context.Background(), domain.NewID(), true)
		safe := domain.SafeError(err)
		if err == nil || safe.Message == loginMessage || safe.Guidance == loginGuidance {
			t.Fatal("relabelled", status, err)
		}
		if strings.Contains(status, "Surprise") {
			if safe.Code != domain.Unsupported {
				t.Fatal(status, safe)
			}
			continue
		}
		if safe.Code != domain.Unavailable || safe.Message != genericText {
			t.Fatal(status, safe)
		}
	}
}

func TestEnableKeepsGenericFailureWhenClientIsMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var logs bytes.Buffer
	manager, _ := newLoginManager(t, &logs)
	_, err := manager.Set(context.Background(), domain.NewID(), true)
	safe := domain.SafeError(err)
	if err == nil || safe.Code != domain.NotFound || safe.Message != genericText {
		t.Fatal(err)
	}
}

func TestLoginRequirementSurvivesRPCRoundTrip(t *testing.T) {
	wire := rpc.Error(LoginRequired(), "correlation")
	restored := rpc.ClientError(wire)
	if restored.Code != domain.Unavailable || restored.Message != loginMessage || restored.Guidance != loginGuidance {
		t.Fatal(restored)
	}
	if !strings.Contains(wire.Error(), loginMessage) {
		t.Fatal(wire)
	}
}
