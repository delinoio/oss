// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"slices"
	"testing"
	"time"
)

func TestGeneratedImageDeletionPreservesIndependentOriginalOutput(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	parent, q, _ := deletionSession(t, s, "parent")
	child, _, _ := deletionSession(t, s, "child")
	machine, device, execution := domain.NewID(), domain.NewID(), domain.NewID()
	a := imageLifetimeFixture(t, s, parent.ID, q.ID, machine, device)
	b := imageLifetimeFixture(t, s, parent.ID, q.ID, machine, device)
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.generated-output", nil, func(tx *Tx) (any, error) {
		for _, ref := range []domain.ImageAttachment{a.Attachment, b.Attachment} {
			r, v, e := tx.ImageUploadRecord(ref.ID)
			if e != nil {
				return nil, e
			}
			v.GeneratedExecutionID = execution
			if ref.ID == a.Attachment.ID {
				v.Owners = append(v.Owners, child.ID)
			}
			if _, e = tx.PutImageUpload(r, v); e != nil {
				return nil, e
			}
		}
		plan, e := tx.planImageSessionDeletion(SessionDeletion{SessionID: parent.ID, ID: domain.NewID(), ServerID: domain.NewID()})
		if e != nil {
			return nil, e
		}
		if len(plan.Workers) != 1 || !plan.Workers[0].Work.GeneratedImageCleanup || !slices.Equal(plan.Workers[0].Work.Images, []domain.ImageAttachment{b.Attachment}) || !slices.Equal(plan.Workers[0].Work.PreservedGeneratedImages, []domain.ImageAttachment{a.Attachment}) {
			t.Fatal("original output gained parent deletion ownership or orphan protection lost")
		}
		if e = tx.applyImageSessionDeletion(plan); e != nil {
			return nil, e
		}
		_, first, e := tx.ImageUploadRecord(a.Attachment.ID)
		if e != nil {
			return nil, e
		}
		if first.State != domain.ImageClaimed || !slices.Equal(first.Owners, []domain.ID{child.ID}) {
			t.Fatal("independent owner retired")
		}
		last, e := tx.planImageSessionDeletion(SessionDeletion{SessionID: child.ID, ID: domain.NewID(), ServerID: domain.NewID()})
		if e != nil {
			return nil, e
		}
		if len(last.Workers) != 1 || !last.Workers[0].Work.GeneratedImageCleanup || !slices.Equal(last.Workers[0].Work.Images, []domain.ImageAttachment{a.Attachment}) || len(last.Workers[0].Work.PreservedGeneratedImages) != 0 {
			t.Fatal("last-owner output cleanup lost")
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedImageDeletionFreezesUnpublishedAllGenerationIntentsAndDowngrade(t *testing.T) {
	s, _ := openTest(t)
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	server, machine, device, instance := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	if err := s.BindIdentity(owner, server); err != nil {
		t.Fatal(err)
	}
	session, _, _ := deletionSession(t, s, "unpublished outputs")
	executions := []domain.ID{domain.NewID(), domain.NewID()}
	_, err := s.Mutate(owner, domain.NewID(), "fixture.image-intents", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "Original", OS: "linux", Architecture: "amd64", WorkerCapabilities: []domain.WorkerCapability{domain.NativeImageGenerationV1}}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Original", Type: domain.WorkerDevice, MachineID: machine, PairedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		if err := tx.SetWorkerInstance(machine, instance, time.Now().UTC()); err != nil {
			return nil, err
		}
		for _, execution := range executions {
			raw, _ := json.Marshal(domain.ExecutionJobInput{SessionID: session.ID, MachineID: machine, ExecutionID: execution, NativeImageGeneration: true})
			jobID := domain.NewID()
			j := domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobQueued, MachineID: machine, Input: raw, AcceptedAt: time.Now().UTC()}
			row, err := tx.PutJob(jobID, 0, session.ID, "", j)
			if err != nil {
				return nil, err
			}
			j.State, j.InstanceID, j.AssignedDeviceID = domain.JobClaimed, instance, device
			if _, err = tx.PutJob(jobID, row.Revision, session.ID, "", j); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	frozen, _, err := s.DeleteSession(owner, domain.NewID(), session.ID, server, session.Revision)
	if err != nil || len(frozen.Workers) != 1 {
		t.Fatal(frozen, err)
	}
	work := frozen.Workers[0].Work
	if !work.GeneratedImageCleanup || len(work.Images) != 0 || len(work.PreservedGeneratedImages) != 0 || len(work.Copies) != 2 {
		t.Fatal("unpublished all-generation cleanup lost", work)
	}
	for _, execution := range executions {
		if !slices.ContainsFunc(work.Copies, func(copy domain.SessionDeletionCopy) bool {
			return copy.ExecutionID == execution && copy.InstanceID == instance
		}) {
			t.Fatal("original generation omitted", execution)
		}
	}
	setCapabilities := func(values []domain.WorkerCapability) {
		t.Helper()
		_, err := s.Mutate(owner, domain.NewID(), "fixture.worker-upgrade", nil, func(tx *Tx) (any, error) {
			row, err := tx.Get(domain.MachineKind, machine)
			if err != nil {
				return nil, err
			}
			value, err := Decode[domain.Machine](row)
			if err != nil {
				return nil, err
			}
			value.WorkerCapabilities = values
			return tx.Put(domain.MachineKind, machine, row.Revision, "", "", value)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: device, MachineID: machine})
	request := domain.NewID()
	setCapabilities(nil)
	if _, err = s.AcknowledgeSessionDeletion(worker, session.ID, frozen.ID, request, instance, work.Digest()); err == nil {
		t.Fatal("old Worker acknowledged unpublished generation intents")
	}
	current, err := s.readSessionDeletion(session.ID)
	if err != nil || current.Revision != frozen.Revision || current.Workers[0].Acknowledged || current.Workers[0].Work.Digest() != work.Digest() {
		t.Fatal("downgrade replaced frozen cleanup", current, err)
	}
	setCapabilities([]domain.WorkerCapability{domain.NativeImageGenerationV1})
	acknowledged, err := s.AcknowledgeSessionDeletion(worker, session.ID, frozen.ID, request, instance, work.Digest())
	if err != nil || !acknowledged.Workers[0].Acknowledged {
		t.Fatal(acknowledged, err)
	}
	setCapabilities(nil)
	replay, err := s.AcknowledgeSessionDeletion(worker, session.ID, frozen.ID, request, instance, work.Digest())
	if err != nil || replay.Revision != acknowledged.Revision {
		t.Fatal("original receipt was replaced", replay, err)
	}
}
