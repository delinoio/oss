package forwarding

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
)

type cleanupJournal struct {
	Version  uint32          `json:"version"`
	Endpoint string          `json:"endpoint"`
	Peer     *pb.ForwardPeer `json:"peer"`
	ReportID domain.ID       `json:"report_id"`
	Clean    bool            `json:"clean"`
}

func cleanupDir(c Config) string { return filepath.Join(c.Root, "forward-cleanup") }
func cleanupPath(c Config, peer *pb.ForwardPeer) string {
	return filepath.Join(cleanupDir(c), peer.RuntimeId+".json")
}
func beginCleanup(c Config) (cleanupJournal, error) {
	j := cleanupJournal{Version: 1, Endpoint: c.Endpoint, Peer: c.Peer, ReportID: domain.NewID()}
	if c.Peer == nil || domain.ID(c.Peer.RuntimeId).Validate() != nil || c.Endpoint == "" {
		return j, unavailable()
	}
	if err := security.PrivateDir(c.Root); err != nil {
		return j, domain.SafeError(err)
	}
	if err := security.PrivateDir(cleanupDir(c)); err != nil {
		return j, domain.SafeError(err)
	}
	dir, err := os.Open(cleanupDir(c))
	if err != nil {
		return j, domain.SafeError(err)
	}
	entries, err := dir.ReadDir(257)
	_ = dir.Close()
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, io.EOF) {
		return j, domain.SafeError(err)
	}
	if len(entries) >= 256 {
		return j, domain.Fail(domain.ResourceExhausted, "The private forward cleanup inventory is full.", "Reconcile completed original lifetimes; preserve uncertain records.")
	}
	if _, err := os.Lstat(cleanupPath(c, c.Peer)); !errors.Is(err, os.ErrNotExist) {
		return j, domain.Fail(domain.RecoveryRequired, "This forward runtime already has private ownership history.", "Preserve it; native work cannot be replayed.")
	}
	raw, _ := json.Marshal(j)
	return j, security.WriteAtomic(cleanupPath(c, c.Peer), raw)
}
func completeCleanup(c Config, j cleanupJournal) error {
	raw, err := security.ReadPrivate(cleanupPath(c, j.Peer), 16<<10)
	var before cleanupJournal
	if err != nil || domain.Decode(raw, &before) != nil || before.Version != j.Version || before.Endpoint != j.Endpoint || before.ReportID != j.ReportID || before.Clean || !proto.Equal(before.Peer, j.Peer) {
		return domain.Fail(domain.RecoveryRequired, "The original private forward claim changed before cleanup publication.", "Preserve its history; changed records cannot be overwritten as cleanup proof.")
	}
	j.Clean = true
	raw, _ = json.Marshal(j)
	return security.WriteAtomic(cleanupPath(c, j.Peer), raw)
}

// Reconcile only sends a retained positive cleanup receipt. A missing or
// incomplete record cannot establish native closure, even after process exit.
func Reconcile(ctx context.Context, c Config) error {
	raw, err := security.ReadPrivate(cleanupPath(c, c.Peer), 16<<10)
	if err != nil {
		return domain.Fail(domain.RecoveryRequired, "The original forward cleanup proof is unavailable.", "Preserve the original runtime history; absence is not cleanup.")
	}
	var j cleanupJournal
	if domain.Decode(raw, &j) != nil || j.Version != 1 || j.Endpoint != c.Endpoint || j.ReportID.Validate() != nil || j.Peer == nil || j.Peer.ForwardId != c.Peer.ForwardId || j.Peer.SessionId != c.Peer.SessionId || j.Peer.RuntimeId != c.Peer.RuntimeId || j.Peer.MachineId != c.Peer.MachineId || j.Peer.InstanceId != c.Peer.InstanceId || !j.Clean {
		return domain.Fail(domain.RecoveryRequired, "The original forward cleanup remains unconfirmed.", "Only the runtime that closed and joined its original handles may confirm cleanup.")
	}
	_, err = c.Client.ReportForwardCleanup(ctx, request(c, &pb.ReportForwardCleanupRequest{RequestId: string(j.ReportID), Peer: j.Peer}))
	if err != nil {
		return rpc.ClientError(err)
	}
	// Keep proof until the exact durable receipt is confirmed. A failed remove is
	// safe: reconnect replays only this same cleanup receipt, never native work.
	if err := os.Remove(cleanupPath(c, c.Peer)); err != nil {
		return domain.SafeError(err)
	}
	return nil
}
func ReconcileWorker(ctx context.Context, c Config, machine domain.ID) error {
	dir, err := os.Open(cleanupDir(c))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return domain.SafeError(err)
	}
	defer dir.Close()
	entries, err := dir.ReadDir(257)
	if err != nil && !errors.Is(err, io.EOF) {
		return domain.SafeError(err)
	}
	if len(entries) > 256 {
		return domain.Fail(domain.ResourceExhausted, "The forward cleanup inventory exceeds its bound.", "Preserve the private records and inspect cleanup.")
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return domain.SafeError(ctx.Err())
		}
		if !entry.Type().IsRegular() {
			return unavailable()
		}
		raw, err := security.ReadPrivate(filepath.Join(cleanupDir(c), entry.Name()), 16<<10)
		if err != nil {
			return domain.SafeError(err)
		}
		var j cleanupJournal
		if domain.Decode(raw, &j) != nil || j.Version != 1 || j.Peer == nil || domain.ID(j.Peer.RuntimeId).Validate() != nil || entry.Name() != j.Peer.RuntimeId+".json" {
			return unavailable()
		}
		if !j.Clean {
			continue
		}
		if j.Endpoint != c.Endpoint || j.Peer.MachineId != string(machine) {
			return unavailable()
		}
		selected := c
		selected.Peer = j.Peer
		if err := Reconcile(ctx, selected); err != nil {
			return err
		}
	}
	return nil
}
