// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// This interactive test uses the actual server RPC, installed Grok CLI and OS
// vault. Only the transient approval pair goes to the controlling terminal;
// native output, credentials and account identity never enter test logs.
func TestGrokCLIServerAccountAcceptance(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_GROK_ACCOUNT") != "1" {
		t.Skip("explicit interactive real-account opt-in required")
	}
	terminalName := "/dev/tty"
	if runtime.GOOS == "windows" {
		terminalName = "CONOUT$"
	}
	terminal, err := os.OpenFile(terminalName, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal("an interactive controlling terminal is required")
	}
	defer terminal.Close()
	f := newGrokFixture(t)
	f.service.grokLoginPerform = nil
	f.service.grokLogoutPerform = nil
	f.service.accountSecrets = nil
	t.Cleanup(func() { _ = f.service.closeAccountSecrets() })
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "acceptance.remove.runner", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.MachineKind, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		return nil, tx.Delete(domain.MachineKind, r.ID, r.Revision)
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.DeviceCode = true })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 6*time.Minute)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); f.service.runGrokLogin(ctx, f.input.AccountID) }()
	t.Cleanup(func() { cancel(); <-done })
	shown := false
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
waiting:
	for {
		select {
		case <-done:
			break waiting
		case <-ctx.Done():
			t.Fatal("real server login exceeded its approval deadline")
		case <-ticker.C:
			progress := f.progressFor(response.OperationId)
			if !shown && progress.State == grokWaiting && progress.UserCode != "" {
				if _, err := fmt.Fprintf(terminal, "\nGrok approval URL: %s\nGrok approval code: %s\n", progress.Url, progress.UserCode); err != nil {
					t.Fatal("cannot display approval details")
				}
				shown = true
			}
		}
	}
	if f.progressFor(response.OperationId).State != grokSucceeds {
		t.Fatalf("server CLI login did not succeed: %v", f.progressFor(response.OperationId).State)
	}
	_, account := f.record()
	if account.Connection == nil {
		t.Fatal("login did not publish a connection")
	}
	// Reopen the durable store and native vault; no login is replayed.
	root := f.service.Store.Root()
	if err := f.service.ownedVault.Close(); err != nil {
		t.Fatal(err)
	}
	f.service.ownedVault = nil
	f.service.accountSecrets = nil
	if err := f.service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	f.service.Store = reopened
	t.Cleanup(func() { _ = reopened.Close() })
	f.service.subscriptionEpoch = domain.NewID()
	_, retained := f.record()
	if retained.Connection == nil || retained.Subscription.Generation != account.Subscription.Generation {
		t.Fatal("reopen did not retain the connected generation")
	}
	vault, err := f.service.secrets()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := vault.Get(ctx, credentials.Ref{Owner: f.input.AccountID, ID: retained.Subscription.Generation, Purpose: credentials.AccountLogin})
	clear(raw)
	if err != nil {
		t.Fatal("reopened protected generation is unavailable")
	}
	logs := f.captureLogs()
	refresh, err := f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.Action = grokRefreshAction })
	if err != nil {
		t.Fatal(err)
	}
	f.service.runGrokRefresh(ctx, f.input.AccountID)
	if f.progressFor(refresh.OperationId).State != grokSucceeds {
		logs.mu.Lock()
		decoder := json.NewDecoder(bytes.NewReader(logs.buf.Bytes()))
		for decoder.More() {
			var event struct {
				Message string `json:"msg"`
				Reason  string `json:"reason"`
				Code    string `json:"code"`
			}
			if decoder.Decode(&event) != nil {
				break
			}
			if event.Reason != "" || event.Code != "" {
				t.Logf("server diagnostic: event=%s reason=%s code=%s", event.Message, event.Reason, event.Code)
			}
		}
		logs.mu.Unlock()
		t.Fatalf("real server refresh did not succeed: %v", f.progressFor(refresh.OperationId).State)
	}
	logout, err := f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.Action = grokLogoutAction })
	if err != nil {
		t.Fatal(err)
	}
	f.service.runGrokLogout(ctx, f.input.AccountID)
	if f.progressFor(logout.OperationId).State != grokSucceeds {
		t.Fatalf("real server CLI logout did not succeed: %v", f.progressFor(logout.OperationId).State)
	}
	_, account = f.record()
	if account.Connection != nil {
		t.Fatal("logout retained a connection")
	}
	t.Log("real server CLI login, durable reopen, protected credential reopen, refresh and logout passed without a Runner")
}
