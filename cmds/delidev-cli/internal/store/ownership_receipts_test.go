// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestReceiptReplayAllowsAuthenticatedPrincipalMetadataAndPreservesOriginalDigest(t *testing.T) {
	s, _ := openTest(t)
	owner := domain.Principal{Type: domain.OwnerDevice}
	worker := domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: domain.NewID()}
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.device", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.DeviceKind, worker.DeviceID, 0, "", "", domain.Device{Name: "Fixture", Type: worker.Type, MachineID: worker.MachineID, PairedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	type command struct {
		Actor    domain.Principal
		Revision uint64
		Selected domain.ID
		Value    string
	}
	for _, original := range []domain.Principal{owner, worker} {
		id := domain.NewID()
		input := command{original, 3, domain.NewID(), "accepted"}
		first, err := s.Mutate(domain.WithPrincipal(context.Background(), original), id, "fixture.replay", input, func(tx *Tx) (any, error) { return "original result", nil })
		if err != nil {
			t.Fatal(err)
		}
		var before string
		if err := s.db.QueryRow("SELECT digest FROM receipts WHERE id=?", id).Scan(&before); err != nil {
			t.Fatal(err)
		}
		input.Actor = owner
		if original == owner {
			input.Actor = worker
		}
		ctx := domain.WithPrincipal(context.Background(), input.Actor)
		for _, replay := range []bool{false, true} {
			var result Result
			if replay {
				var found bool
				result, found, err = s.Replay(ctx, id, "fixture.replay", input)
				if !found {
					t.Fatal("accepted receipt not found", err)
				}
			} else {
				result, err = s.Mutate(ctx, id, "fixture.replay", input, func(tx *Tx) (any, error) { t.Fatal("duplicate mutation ran again"); return nil, nil })
			}
			if err != nil || !result.Replayed || string(result.Data) != string(first.Data) {
				t.Fatal("attribution blocked exact replay", err)
			}
		}
		input.Value = "different payload"
		if _, err := s.Mutate(ctx, id, "fixture.replay", input, nil); domain.SafeError(err).Code != domain.Conflict {
			t.Fatal("metadata relaxation widened request payload", err)
		}
		var after string
		if err := s.db.QueryRow("SELECT digest FROM receipts WHERE id=?", id).Scan(&after); err != nil || before != after {
			t.Fatal("replay converted accepted receipt", err)
		}
	}
}
