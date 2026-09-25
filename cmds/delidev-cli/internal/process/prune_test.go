package process

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestOwnerRecoveryPrunesOnlyReleasedCompletedScopesBeforeLimit(t *testing.T) {
	c := config(t, "streams")
	h, err := Start(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := h.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := h.CloseInput(); err != nil {
		t.Fatal(err)
	}
	_ = h.Wait() // The streams fixture deliberately exits with status seven.
	identity := h.Identity()
	raw, err := os.ReadFile(filepath.Join(identity.ScopeDir, "ownership.json"))
	if err != nil {
		t.Fatal(err)
	}
	var journal map[string]any
	if json.Unmarshal(raw, &journal) != nil || journal["complete"] != true {
		t.Fatal("fixture lacks native completion")
	}
	parent := filepath.Dir(identity.ScopeDir)
	// Separate completed-journal fixtures cross the small internal limit. A
	// production owner uses 10,000; only unresolved/live-controller scopes count.
	for range 7 {
		path := filepath.Join(parent, string(domain.NewID()))
		if err := security.PrivateDir(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "ownership.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := reconcileOwnerContext(context.Background(), c.Directory, c.OwnerID, 2); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(identity.ScopeDir) {
		t.Fatal("completed histories consumed the owner bound or an open handle lost its journal")
	}
	if err := ReconcileProcess(identity); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reconcileOwnerContext(context.Background(), c.Directory, c.OwnerID, 2); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("released native completion was not retired")
	}
	if err := ReconcileOwner(c.Directory, c.OwnerID); err != nil {
		t.Fatal("retained empty owner index lost completion", err)
	}
	if err := ReconcileProcess(identity); err == nil {
		t.Fatal("a missing individual journal fabricated individual process proof")
	}
}

func TestOwnerRecoveryPreservesIncompleteOrInvalidScopes(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		root := filepath.Join(t.TempDir(), "processes")
		if err := security.PrivateDir(root); err != nil {
			t.Fatal(err)
		}
		owner := domain.NewID()
		path := filepath.Join(root, string(owner), string(domain.NewID()))
		if err := security.PrivateDir(path); err != nil {
			t.Fatal(err)
		}
		raw := []byte(`{"version":1,"owner_id":"` + string(owner) + `","owner":{},"complete":false}`)
		if invalid {
			raw = []byte(`{"complete":true}`)
		}
		if err := os.WriteFile(filepath.Join(path, "ownership.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := reconcileOwnerContext(context.Background(), root, owner, 0); err == nil {
			t.Fatal("unproven journal accepted")
		}
		after, err := os.ReadFile(filepath.Join(path, "ownership.json"))
		if err != nil || string(after) != string(raw) {
			t.Fatal("unproven ownership was removed or rewritten")
		}
	}
}

func TestOwnerRecoveryFinishesInterruptedRetirement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "processes")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	owner := domain.NewID()
	path := filepath.Join(root, string(owner), ".retired-"+string(domain.NewID()))
	if err := security.PrivateDir(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "controller.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileOwner(root, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("interrupted retirement remained in the active owner index")
	}
}
