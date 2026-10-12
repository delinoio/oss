// SPDX-License-Identifier: Apache-2.0
package tailscale

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
)

func TestDisabledIntentRetainsUnknownOriginalChild(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tailnet")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	server, owner, request := domain.NewID(), domain.NewID(), domain.NewID()
	// A missing owner index cannot prove that the original child was never
	// launched. Disabled intent must not erase its retained cleanup obligation.
	value := accessIntent{Version: 1, ServerID: server, Owner: owner, Request: request, Receipts: []accessReceipt{{ID: request, Enabled: false}}}
	raw, _ := json.Marshal(value)
	if err := security.WriteAtomic(filepath.Join(root, "access.json"), raw); err != nil {
		t.Fatal(err)
	}
	manager, err := Open(context.Background(), root, server, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := manager.Status(); got.Enabled || got.State != CleanupRequired {
		t.Fatal(got)
	}
	if _, err := manager.Set(context.Background(), request, false); err == nil {
		t.Fatal("missing original owner accepted as cleaned")
	}
	raw, err = security.ReadPrivate(filepath.Join(root, "access.json"), 128<<10)
	if err != nil {
		t.Fatal(err)
	}
	var retained accessIntent
	if json.Unmarshal(raw, &retained) != nil || retained.Owner != owner {
		t.Fatal("original owner lost")
	}
}
func TestSupersededAccessRequestNeverReenables(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tailnet")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	original, current, server := domain.NewID(), domain.NewID(), domain.NewID()
	manager := &Manager{adapter: Adapter{Root: root}, parent: context.Background(), server: server, state: Off, value: accessIntent{Version: 1, ServerID: server, Request: current, Receipts: []accessReceipt{{ID: original, Enabled: true}, {ID: current, Enabled: false}}}}
	got, err := manager.Set(context.Background(), original, true)
	if err != nil || got.Enabled || got.State != Off {
		t.Fatal(got, err)
	}
	if _, err := manager.Set(context.Background(), original, false); err == nil {
		t.Fatal("altered original request accepted")
	}
}
