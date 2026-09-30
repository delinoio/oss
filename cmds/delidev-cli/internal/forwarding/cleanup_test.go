package forwarding

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestPrivateCleanupRequiresOriginalJoinedEvidence(t *testing.T) {
	c := Config{Root: filepath.Join(t.TempDir(), "client"), Endpoint: "http://127.0.0.1:43211", Peer: &pb.ForwardPeer{ForwardId: string(domain.NewID()), SessionId: string(domain.NewID()), RuntimeId: string(domain.NewID())}}
	if err := Reconcile(context.Background(), c); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("missing original claim became cleanup", err)
	}
	journal, err := beginCleanup(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(context.Background(), c); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("unjoined claim became cleanup", err)
	}
	if _, err := beginCleanup(c); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("original ownership journal replaced", err)
	}
	if err := completeCleanup(c, journal); err != nil {
		t.Fatal(err)
	}
	wrong := c
	wrong.Endpoint = "http://127.0.0.1:43212"
	if err := Reconcile(context.Background(), wrong); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("foreign server accepted private proof", err)
	}
	raw, err := os.ReadFile(cleanupPath(c, c.Peer))
	if err != nil {
		t.Fatal(err)
	}
	var observed cleanupJournal
	if json.Unmarshal(raw, &observed) != nil || !observed.Clean || observed.ReportID != journal.ReportID {
		t.Fatal("positive closure changed original receipt")
	}
	if err := security.RegularPrivate(cleanupPath(c, c.Peer)); err != nil {
		t.Fatal("private proof permissions", err)
	}
	wrong.Peer = &pb.ForwardPeer{ForwardId: string(domain.NewID()), SessionId: c.Peer.SessionId, RuntimeId: c.Peer.RuntimeId}
	wrong.Endpoint = c.Endpoint
	if err := Reconcile(context.Background(), wrong); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("foreign forward accepted private proof", err)
	}
	if err := os.WriteFile(filepath.Join(cleanupDir(c), "unexpected.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileWorker(context.Background(), c, domain.NewID()); err == nil {
		t.Fatal("malformed cleanup inventory silently skipped")
	}
}

func TestPrivateCleanupPreservesChangedOriginalClaim(t *testing.T) {
	c := Config{Root: filepath.Join(t.TempDir(), "client"), Endpoint: "http://127.0.0.1:43211", Peer: &pb.ForwardPeer{ForwardId: string(domain.NewID()), SessionId: string(domain.NewID()), RuntimeId: string(domain.NewID())}}
	journal, err := beginCleanup(c)
	if err != nil {
		t.Fatal(err)
	}
	changed := journal
	changed.ReportID = domain.NewID()
	raw, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	path := cleanupPath(c, c.Peer)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := completeCleanup(c, journal); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("changed ownership became positive cleanup", err)
	}
	retained, err := os.ReadFile(path)
	if err != nil || string(retained) != string(raw) {
		t.Fatal("changed original claim was overwritten", err)
	}
}
