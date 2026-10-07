// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestCompactionRejectsUnavailableStorageBeforeAcceptanceAndCredentialClaim(t *testing.T) {
	for _, state := range []domain.WorkspaceStorageState{domain.WorkspaceStored, domain.WorkspaceStoragePending, domain.WorkspaceStorageUncertain} {
		t.Run(string(state), func(t *testing.T) {
			f, pf := publicCompactionFixture(t)
			ctx := context.Background()
			before := f.refresh(t)
			accepted, err := sessionClient(f.accountFixture).CompactSession(ctx, ownerRequest(f.identity, &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID())}))
			if err != nil {
				t.Fatal(err)
			}
			var claim store.Record
			_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.compaction.storage", nil, func(tx *store.Tx) (any, error) {
				r, err := tx.Get(domain.JobKind, domain.ID(accepted.Msg.Job.Id))
				if err != nil {
					return nil, err
				}
				j, err := store.Decode[domain.Job](r)
				if err != nil {
					return nil, err
				}
				j.State, j.InstanceID, j.AssignedDeviceID = domain.JobClaimed, pf.instance, pf.device
				claim, err = tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, j)
				if err != nil {
					return nil, err
				}
				sr, session, err := sessionRecord(tx, before.ID)
				if err != nil {
					return nil, err
				}
				session.Storage = &domain.WorkspaceStorage{State: state, JobID: domain.NewID(), SnapshotID: domain.NewID()}
				return tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.SessionID, sr.ProjectID, session)
			})
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256([]byte("compaction-storage-token-sentinel"))
			if _, err := f.workerClient.RegisterExecution(ctx, ownerRequest(f.workerIdentity, &pb.RegisterExecutionRequest{Mutation: acctMutation(resourceForTest(claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, CredentialDigest: digest[:]})); (err == nil) != (state != domain.WorkspaceStored) {
				t.Fatal("unavailable storage granted native compaction credentials")
			}
			// Clear only the already accepted fixture action to independently exercise
			// public acceptance under the same unavailable workspace state.
			_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.compaction.clear", nil, func(tx *store.Tx) (any, error) {
				sr, s, err := sessionRecord(tx, before.ID)
				if err != nil {
					return nil, err
				}
				s.CompactionJobID = ""
				return tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.SessionID, sr.ProjectID, s)
			})
			if err != nil {
				t.Fatal(err)
			}
			current := f.refresh(t)
			if _, err := sessionClient(f.accountFixture).CompactSession(ctx, ownerRequest(f.identity, &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(current), domain.NewID())})); (err == nil) != (state != domain.WorkspaceStored) {
				t.Fatal("unavailable storage accepted compaction")
			}
			if state == domain.WorkspaceStored && f.refresh(t).Revision != current.Revision {
				t.Fatal("rejected compaction changed session")
			}
		})
	}
}
