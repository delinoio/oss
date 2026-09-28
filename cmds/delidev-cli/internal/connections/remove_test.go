package connections_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/connections"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestConnectionRemovalRetainsIndependentWorkerAndOriginalReceipts(t *testing.T) {
	f := start(t)
	root := filepath.Join(t.TempDir(), "desktop")
	id, request := domain.NewID(), domain.NewID()
	originalGrant := grant(t, f)
	profile, err := connections.Pair(context.Background(), root, id, "Original", originalGrant)
	if err != nil {
		t.Fatal(err)
	}
	renameRequest := domain.NewID()
	profile, err = connections.Rename(context.Background(), root, id, renameRequest, profile.Revision, "Selected name")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := connections.RegisterWorker(context.Background(), root, id)
	if err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(root, "connections", string(id), "worker", "workspace-evidence")
	if err := os.WriteFile(retained, []byte("owned Worker evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.Remove(context.Background(), root, id, request, 1); err == nil {
		t.Fatal("stale removal accepted")
	}
	removed, err := connections.Remove(context.Background(), root, id, request, profile.Revision)
	if err != nil || removed.State != connections.Removed || removed.Revision != profile.Revision+1 || removed.Removal == nil || removed.Removal.RequestID != request {
		t.Fatal(removed, err)
	}
	if got, err := connections.Remove(context.Background(), root, id, request, profile.Revision); err != nil || !reflect.DeepEqual(got, removed) {
		t.Fatal("exact removal retry changed result", err)
	}
	if values, err := connections.List(root); err != nil || len(values) != 0 {
		t.Fatal("removed client consumes an active slot", values, err)
	}
	if page, err := connections.ListRemoved(root, ""); err != nil || len(page.Connections) != 1 || !reflect.DeepEqual(page.Connections[0], removed) {
		t.Fatal("removal evidence missing", err)
	}
	for _, name := range []string{"device.json", "pairing-pending.json"} {
		if _, err := os.Lstat(filepath.Join(root, "connections", string(id), "client", name)); !os.IsNotExist(err) {
			t.Fatal("client secret retained", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "connections", string(id), "connection.json"))
	if err != nil || strings.Contains(string(raw), originalGrant.Code) {
		t.Fatal("original pairing grant retained", err)
	}
	if got, err := connections.WorkerCredential(root, id); err != nil || got != registered {
		t.Fatal("independent Worker authority changed", err)
	}
	if got, err := os.ReadFile(retained); err != nil || string(got) != "owned Worker evidence" {
		t.Fatal("Worker workspace touched", err)
	}
	if _, err := connections.Pair(context.Background(), root, id, "Original", originalGrant); err == nil {
		t.Fatal("pair retry resurrected removed profile")
	}
	if _, err := connections.Retry(context.Background(), root, id); err == nil {
		t.Fatal("removed pairing retried")
	}
	if _, err := connections.Verify(context.Background(), root, id); err == nil {
		t.Fatal("removed client connected")
	}
	if _, err := connections.RegisterWorker(context.Background(), root, id); err == nil {
		t.Fatal("removed client created a Worker")
	}
	otherID := domain.NewID()
	other, err := connections.Pair(context.Background(), root, otherID, "Another", grant(t, f))
	if err != nil {
		t.Fatal(err)
	}
	for _, used := range []domain.ID{request, renameRequest} {
		if _, err := connections.Remove(context.Background(), root, otherID, used, other.Revision); err == nil {
			t.Fatal("removed history request reused")
		}
		if _, err := connections.Rename(context.Background(), root, otherID, used, other.Revision, "Changed"); err == nil {
			t.Fatal("removed history rename reused")
		}
	}
	if got, err := connections.Inspect(root, otherID); err != nil || !reflect.DeepEqual(got, other) {
		t.Fatal("another profile changed", err)
	}
}
