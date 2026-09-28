package connections_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/connections"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestConnectionRenameRetainsOriginalAuthorityAndExactMutationReceipts(t *testing.T) {
	f := start(t)
	root := filepath.Join(t.TempDir(), "desktop")
	id, request := domain.NewID(), domain.NewID()
	originalGrant := grant(t, f)
	original, err := connections.Pair(context.Background(), root, id, "Original name", originalGrant)
	if err != nil {
		t.Fatal(err)
	}
	credentialPath := filepath.Join(root, "connections", string(id), "client", "device.json")
	before, err := os.ReadFile(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := connections.Rename(context.Background(), root, id, request, original.Revision, "New display name 한글")
	if err != nil || changed.Revision != 2 || changed.Name != "New display name 한글" {
		t.Fatal(changed, err)
	}
	restoredIdentity := changed
	restoredIdentity.Name, restoredIdentity.Revision = original.Name, original.Revision
	if restoredIdentity != original {
		t.Fatal("name edit replaced original authority")
	}
	latest, err := connections.Rename(context.Background(), root, id, domain.NewID(), changed.Revision, "Later display name")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := connections.Rename(context.Background(), root, id, request, original.Revision, changed.Name)
	if err != nil || replay != latest {
		t.Fatal("old receipt reapplied a superseded name", err)
	}
	if _, err := connections.Rename(context.Background(), root, id, request, original.Revision, "Changed retry"); err == nil {
		t.Fatal("changed receipt accepted")
	}
	if _, err := connections.Rename(context.Background(), root, id, domain.NewID(), original.Revision, "Stale edit"); err == nil {
		t.Fatal("stale edit accepted")
	}
	paired, err := connections.Pair(context.Background(), root, id, original.Name, originalGrant)
	if err != nil || paired != latest {
		t.Fatal("rename changed original pairing retry", err)
	}
	after, err := os.ReadFile(credentialPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("renaming rewrote a credential", err)
	}
	verified, err := connections.Verify(context.Background(), root, id)
	if err != nil || verified.Profile != latest {
		t.Fatal("rename invalidated original authenticated client", err)
	}
	secondID := domain.NewID()
	second, err := connections.Pair(context.Background(), root, secondID, "Other connection", grant(t, f))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connections.Rename(context.Background(), root, secondID, request, second.Revision, changed.Name); err == nil {
		t.Fatal("mutation identity crossed profile ownership")
	}
	if got, err := connections.Inspect(root, secondID); err != nil || got != second {
		t.Fatal("foreign retry changed another connection", err)
	}
}

func TestConnectionRenameNeverCreatesMissingStateOrRepairsDamagedAuthority(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	if _, err := connections.Rename(context.Background(), root, domain.NewID(), domain.NewID(), 1, "Name"); err == nil {
		t.Fatal("missing profile adopted")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("rename created private state")
	}
	f := start(t)
	id := domain.NewID()
	original, err := connections.Pair(context.Background(), root, id, "Name", grant(t, f))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := connections.Rename(ctx, root, id, domain.NewID(), original.Revision, "Canceled"); err == nil {
		t.Fatal("canceled edit accepted")
	}
	for _, name := range []string{"", "   ", "bad\x00name", string(bytes.Repeat([]byte("x"), 257))} {
		if _, err := connections.Rename(context.Background(), root, id, domain.NewID(), original.Revision, name); err == nil {
			t.Fatal("invalid name accepted")
		}
	}
	if got, err := connections.Inspect(root, id); err != nil || got != original {
		t.Fatal("refused edits changed the profile", err)
	}
	if err := os.Remove(filepath.Join(root, "connections", string(id), "client", "device.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.Rename(context.Background(), root, id, domain.NewID(), original.Revision, "Must not repair"); err == nil {
		t.Fatal("rename repaired missing original credential")
	}
}
