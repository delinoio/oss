// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func deletionPlanWithWorkers(count int) SessionDeletion {
	v := SessionDeletion{Version: 1, ID: domain.NewID(), SessionID: domain.NewID(), ServerID: domain.NewID(), RequestID: domain.NewID(), Actor: domain.Principal{Type: domain.OwnerDevice}, ExpectedRevision: 1, Revision: 1, AcceptedAt: time.Now().UTC()}
	for range count {
		v.Workers = append(v.Workers, SessionDeletionWorker{Work: domain.SessionDeletionWork{Version: 1, DeletionID: v.ID, SessionID: v.SessionID, ServerID: v.ServerID, MachineID: domain.NewID(), DeviceID: domain.NewID(), Copies: []domain.SessionDeletionCopy{{JobID: domain.NewID(), Type: domain.PrepareWorkspaceJob, Revision: 1, Digest: strings.Repeat("a", 64), InstanceID: domain.NewID()}}}})
	}
	return v
}

func TestSessionDeletionWorkerCapacityPersistence(t *testing.T) {
	s, _ := openTest(t)
	for _, count := range []int{101, 1001, domain.MaxSessionDeletionJobs} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			v := deletionPlanWithWorkers(count)
			if err := s.writeSessionDeletion(v); err != nil {
				t.Fatal("valid bounded owners rejected", err)
			}
			got, err := s.readSessionDeletion(v.SessionID)
			if err != nil || len(got.Workers) != count {
				t.Fatal("original owners lost on read", len(got.Workers), err)
			}
			for i := range v.Workers {
				if got.Workers[i].Work.Digest() != v.Workers[i].Work.Digest() {
					t.Fatal("original work changed", i)
				}
			}
		})
	}
	for _, name := range []string{"capacity", "duplicate", "malformed", "acknowledgement"} {
		t.Run(name, func(t *testing.T) {
			v := deletionPlanWithWorkers(domain.MaxSessionDeletionJobs)
			switch name {
			case "capacity":
				v.Workers = append(v.Workers, v.Workers[0])
			case "duplicate":
				v.Workers[len(v.Workers)-1].Work.DeviceID = v.Workers[0].Work.DeviceID
			case "malformed":
				v.Workers[len(v.Workers)-1].Work.Copies[0].Digest = "invalid"
			case "acknowledgement":
				v.Workers[len(v.Workers)-1].Acknowledged = true
			}
			if err := s.writeSessionDeletion(v); err == nil {
				t.Fatal("invalid ownership plan persisted")
			}
			if _, err := os.Lstat(s.sessionDeletionPath(v.SessionID)); !os.IsNotExist(err) {
				t.Fatal("rejected plan published an intent", err)
			}
		})
	}
	t.Run("serialized limit", func(t *testing.T) {
		v := deletionPlanWithWorkers(domain.MaxSessionDeletionJobs)
		for i := range v.Workers {
			for range 16 {
				v.Workers[i].Work.PreparationDigests = append(v.Workers[i].Work.PreparationDigests, strings.Repeat("b", 64))
			}
		}
		if err := v.validate(); err != nil {
			t.Fatal("fixture has invalid ownership", err)
		}
		raw, err := json.Marshal(v)
		if err != nil || len(raw) <= domain.MaxSessionDeletionBytes {
			t.Fatal("fixture did not exceed serialized bound", len(raw), err)
		}
		if err := s.writeSessionDeletion(v); err == nil {
			t.Fatal("oversized intent accepted")
		}
		if _, err := os.Lstat(s.sessionDeletionPath(v.SessionID)); !os.IsNotExist(err) {
			t.Fatal("oversized plan published an intent", err)
		}
	})
}

func TestSessionDeletionAssemblesEveryHistoricalWorker(t *testing.T) {
	s, _ := openTest(t)
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	server, machine, instance := domain.NewID(), domain.NewID(), domain.NewID()
	if err := s.BindIdentity(owner, server); err != nil {
		t.Fatal(err)
	}
	session, _, _ := deletionSession(t, s, "many owners")
	devices := make([]domain.ID, 101)
	for i := range devices {
		devices[i] = domain.NewID()
	}
	_, err := s.Mutate(owner, domain.NewID(), "fixture.many-workers", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "Worker", OS: "linux", Architecture: "amd64"}); err != nil {
			return nil, err
		}
		if err := tx.SetWorkerInstance(machine, instance, time.Now().UTC()); err != nil {
			return nil, err
		}
		for _, device := range devices {
			if _, err := tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Worker", Type: domain.WorkerDevice, MachineID: machine, PairedAt: time.Now().UTC()}); err != nil {
				return nil, err
			}
			input, err := json.Marshal(map[string]any{"session_id": session.ID, "machine_id": machine, "type": domain.GeneralChat, "repositories": []any{}})
			if err != nil {
				return nil, err
			}
			copies := 1
			if device == devices[0] {
				copies = 2
			}
			for range copies {
				job := domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobQueued, MachineID: machine, Input: input, AcceptedAt: time.Now().UTC()}
				id := domain.NewID()
				record, err := tx.PutJob(id, 0, session.ID, "", job)
				if err != nil {
					return nil, err
				}
				job.State, job.InstanceID, job.AssignedDeviceID = domain.JobClaimed, instance, device
				if _, err := tx.PutJob(id, record.Revision, session.ID, "", job); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := s.DeleteSession(owner, domain.NewID(), session.ID, server, session.Revision)
	if err != nil || len(v.Workers) != len(devices) {
		t.Fatal("not every historical Worker persisted", len(v.Workers), err)
	}
	grouped := false
	for _, obligation := range v.Workers {
		if obligation.Work.DeviceID == devices[0] {
			grouped = len(obligation.Work.Copies) == 2
		}
	}
	if !grouped {
		t.Fatal("repeated original Worker jobs were not grouped")
	}
	for _, obligation := range v.Workers {
		worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: obligation.Work.DeviceID, MachineID: machine})
		if _, err := s.AcknowledgeSessionDeletion(worker, session.ID, v.ID, domain.NewID(), instance, strings.Repeat("0", 64)); err == nil {
			t.Fatal("foreign work digest acknowledged")
		}
		v, err = s.AcknowledgeSessionDeletion(worker, session.ID, v.ID, domain.NewID(), instance, obligation.Work.Digest())
		if err != nil {
			t.Fatal("original Worker acknowledgement rejected", err)
		}
	}
	for _, obligation := range v.Workers {
		if !obligation.Acknowledged {
			t.Fatal("original Worker acknowledgement omitted")
		}
	}
}

func TestExecutionCapacityRetainsDeletionEnvelopeHeadroom(t *testing.T) {
	// A valid original-owner inventory close to the byte bound: one recovery
	// fits, but a complete new generation must be rejected without shrinking it.
	plan := deletionPlanWithWorkers(domain.MaxSessionDeletionJobs)
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	target := domain.MaxSessionDeletionBytes - 24576
	extra := (target - len(raw)) / 67
	if extra < 0 || extra > 16*len(plan.Workers) {
		t.Fatal("invalid near-bound fixture", len(raw), extra)
	}
	for n := 0; n < extra; n++ {
		plan.Workers[n/16].Work.PreparationDigests = append(plan.Workers[n/16].Work.PreparationDigests, strings.Repeat("b", 64))
	}
	if err := plan.validate(); err != nil {
		t.Fatal("invalid retained ownership fixture", err)
	}
	before, _ := json.Marshal(plan)
	if err := checkDeletionHeadroom(plan, 1); err != nil {
		t.Fatal("reserved recovery envelope", len(before), err)
	}
	if err := checkDeletionHeadroom(plan, 5); err == nil {
		t.Fatal("new generation exceeded original envelope", len(before))
	}
	after, _ := json.Marshal(plan)
	if string(before) != string(after) {
		t.Fatal("capacity check rewrote retained ownership")
	}
}
