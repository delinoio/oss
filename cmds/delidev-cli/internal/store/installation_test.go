// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestWorkerUpdateFencesDirectFirstClaimAndPreservesOriginalClaim(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	machine := domain.NewID()
	original := domain.NewID()
	job := domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobClaimed, MachineID: machine, InstanceID: domain.NewID(), Input: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC()}
	var accepted Record
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.direct-claim", nil, func(tx *Tx) (any, error) {
		var err error
		accepted, err = tx.PutJob(original, 0, "", "", job)
		return nil, err
	})
	if err != nil {
		t.Fatal("Direct original claim requires no prior row", err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.update-fence", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.UpdateKind, domain.NewID(), 0, "", "", map[string]any{"machine_id": machine, "state": "WAITING_FOR_IDLE"})
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.original-claim-retention", nil, func(tx *Tx) (any, error) { return tx.PutJob(original, accepted.Revision, "", "", job) })
	if err != nil {
		t.Fatal("Original claimed state cannot acquire a second native owner", err)
	}
	fresh := domain.NewID()
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.blocked-direct-claim", nil, func(tx *Tx) (any, error) { return tx.PutJob(fresh, 0, "", "", job) })
	if err == nil || domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("Direct first claim bypassed original update fence", err)
	}
	if _, err = s.Get(ctx, domain.JobKind, fresh); err == nil || domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("Blocked first claim partially published", err)
	}
}
