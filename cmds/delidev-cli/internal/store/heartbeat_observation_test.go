// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestHeartbeatKeepsMachineEntityRevisionAndMutationStreams(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	machine, instance := domain.NewID(), domain.NewID()
	originalSeen := time.Now().UTC().Add(-time.Hour)
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.heartbeat-observation", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "Runner", OS: "linux", Architecture: "amd64", LastSeen: originalSeen}); err != nil {
			return nil, err
		}
		return nil, tx.SetWorkerInstance(machine, instance, time.Now().UTC().Add(-10*time.Second))
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Get(ctx, domain.MachineKind, machine)
	if err != nil {
		t.Fatal(err)
	}
	counts := func() [2]int64 {
		var n [2]int64
		if err := s.db.QueryRow("SELECT COUNT(*) FROM events").Scan(&n[0]); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow("SELECT COUNT(*) FROM receipts").Scan(&n[1]); err != nil {
			t.Fatal(err)
		}
		return n
	}
	prior := counts()
	if err := s.Heartbeat(ctx, machine, instance); err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat(ctx, machine, domain.NewID()); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("foreign instance replaced original lease")
	}
	after, err := s.Get(ctx, domain.MachineKind, machine)
	if err != nil {
		t.Fatal(err)
	}
	if before.Revision != after.Revision || before.UpdatedAt != after.UpdatedAt || !bytes.Equal(before.Data, after.Data) || prior != counts() {
		t.Fatal("heartbeat rewrote Machine/event/receipt stream")
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		current, seen, err := tx.WorkerInstance(machine)
		if err == nil && (current != instance || time.Since(seen) > time.Second) {
			t.Fatal("original authoritative lease was not refreshed")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
