// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestSessionDeletionPinsOriginalStorageReservation(t *testing.T) {
	s, _ := openTest(t)
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	server, machine, device, instance, snapshot := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	if err := s.BindIdentity(owner, server); err != nil {
		t.Fatal(err)
	}
	sr, _, _ := deletionSession(t, s, "stored")
	prepare := workspace.PrepareRequest{SessionID: sr.ID, MachineID: machine, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
	storageID := domain.NewID()
	operation := workspace.StorageRequest{Version: 1, OperationID: storageID, Action: workspace.StorageCreate, SnapshotID: snapshot, PreviousState: domain.WorkspacePresent, Preparation: prepare}
	raw, _ := json.Marshal(operation)
	_, err := s.Mutate(owner, domain.NewID(), "fixture.storage", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "Worker", OS: "linux", Architecture: "amd64"}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Worker", Type: domain.WorkerDevice, MachineID: machine, PairedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		if err := tx.SetWorkerInstance(machine, instance, time.Now().UTC()); err != nil {
			return nil, err
		}
		input, _ := json.Marshal(prepare)
		for _, job := range []struct {
			id    domain.ID
			kind  domain.JobType
			input []byte
		}{{domain.NewID(), domain.PrepareWorkspaceJob, input}, {storageID, domain.WorkspaceStorageJob, raw}} {
			body := domain.Job{Type: job.kind, State: domain.JobQueued, MachineID: machine, Input: job.input, AcceptedAt: time.Now().UTC()}
			record, err := tx.PutJob(job.id, 0, sr.ID, "", body)
			if err != nil {
				return nil, err
			}
			body.State, body.InstanceID, body.AssignedDeviceID = domain.JobClaimed, instance, device
			if _, err := tx.PutJob(job.id, record.Revision, sr.ID, "", body); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	deletion, _, err := s.DeleteSession(owner, domain.NewID(), sr.ID, server, sr.Revision)
	if err != nil || len(deletion.Workers) != 1 {
		t.Fatal(err, deletion)
	}
	work := deletion.Workers[0].Work
	if err := work.Validate(); err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, copy := range work.Copies {
		if copy.JobID == storageID {
			matched = copy.Type == domain.WorkspaceStorageJob && copy.SnapshotID == snapshot
		}
	}
	if !matched {
		t.Fatal("interrupted storage lost reserved snapshot identity")
	}
	retained, err := s.GetSessionDeletion(owner, sr.ID)
	if err != nil || retained.Workers[0].Work.Digest() != work.Digest() {
		t.Fatal("external deletion plan lost storage ownership", err)
	}
}
