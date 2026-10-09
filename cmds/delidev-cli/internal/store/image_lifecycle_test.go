// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func imageLifetimeFixture(t *testing.T, s *Store, session, input, machine, device domain.ID) domain.ImageUpload {
	t.Helper()
	upload := domain.ImageUpload{Version: 1, WorkerDeviceID: device, Actor: domain.Principal{Type: domain.OwnerDevice}, Attachment: domain.ImageAttachment{ID: domain.NewID(), MachineID: machine, MediaType: domain.ImagePNG, ByteLength: 1, SHA256: strings.Repeat("a", 64)}, DraftID: domain.NewID(), OperationID: domain.NewID(), MachineRevision: 1, SessionID: session, InputID: input, State: domain.ImageClaimed, UploadedBytes: 1, Owners: []domain.ID{session}}
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.image", nil, func(tx *Tx) (any, error) {
		raw, _ := json.Marshal(upload)
		return tx.PutJob(upload.Attachment.ID, 0, "", "", domain.Job{Type: domain.ImageAttachmentJob, State: domain.JobSucceeded, MachineID: machine, Input: raw, AcceptedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	return upload
}

func TestImageLifetimeIndependentForkPreservesPrefixAndLastOwnerDeletion(t *testing.T) {
	s, _ := openTest(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	server, machine, device, instance, execution := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	parent, queued, _ := deletionSession(t, s, "parent")
	child, _, _ := deletionSession(t, s, "child")
	first := imageLifetimeFixture(t, s, parent.ID, queued.ID, machine, device)
	laterInput := domain.NewID()
	later := imageLifetimeFixture(t, s, parent.ID, laterInput, machine, device)
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.prefix", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "Original Worker", OS: "linux", Architecture: "amd64"}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Original Worker", Type: domain.WorkerDevice, MachineID: machine, PairedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		if err := tx.SetWorkerInstance(machine, instance, time.Now().UTC()); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.QueueKind, queued.ID, queued.Revision, parent.ID, "", domain.QueuedInput{Sequence: 1, ContentRevision: 1, Attachments: []domain.ImageAttachment{first.Attachment}, Mode: domain.ExecuteMode, Delivery: domain.InputAccepted, ExecutionID: execution}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.QueueKind, laterInput, 0, parent.ID, "", domain.QueuedInput{Sequence: 2, ContentRevision: 1, Attachments: []domain.ImageAttachment{later.Attachment}, Mode: domain.ExecuteMode, Delivery: domain.InputQueued}); err != nil {
			return nil, err
		}
		input := domain.ForkJobInput{Purpose: domain.IndependentFork, SourceSessionID: parent.ID, ChildSessionID: child.ID, Completion: domain.ExecutionCompletion{InputID: queued.ID, ExecutionID: execution}}
		if err := tx.InheritForkImages(input); err != nil {
			return nil, err
		}
		input.Purpose, input.ChildSessionID = domain.SidechatFork, domain.NewID()
		return nil, tx.InheritForkImages(input)
	})
	if err != nil {
		t.Fatal(err)
	}
	read := func(id domain.ID) domain.ImageUpload {
		var value domain.ImageUpload
		if err := s.Read(ctx, func(tx *Tx) error { _, v, err := tx.ImageUploadRecord(id); value = v; return err }); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if v := read(first.Attachment.ID); !slices.Equal(v.Owners, []domain.ID{parent.ID, child.ID}) {
		t.Fatal("Fork did not retain independent ownership", v)
	}
	if v := read(later.Attachment.ID); !slices.Equal(v.Owners, []domain.ID{parent.ID}) {
		t.Fatal("Fork adopted a later queued input", v)
	}
	deletion, _, err := s.DeleteSession(ctx, domain.NewID(), parent.ID, server, parent.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if len(deletion.Workers) != 1 || !slices.Equal(deletion.Workers[0].Work.Images, []domain.ImageAttachment{later.Attachment}) {
		t.Fatal("Shared Fork image entered source cleanup", deletion)
	}
	if v := read(first.Attachment.ID); !slices.Equal(v.Owners, []domain.ID{child.ID}) || v.State != domain.ImageClaimed {
		t.Fatal("Source deletion retired child image", v)
	}
	if _, err := s.PurgeDeletedSession(ctx, parent.ID); err == nil {
		t.Fatal("Offline image cleanup was declared complete")
	}
	worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: device, MachineID: machine})
	if _, err := s.AcknowledgeSessionDeletion(worker, parent.ID, deletion.ID, domain.NewID(), instance, deletion.Workers[0].Work.Digest()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PurgeDeletedSession(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, domain.JobKind, later.Attachment.ID); err == nil {
		t.Fatal("Last-owner metadata survived permanent purge")
	}
	if err := s.Read(ctx, func(tx *Tx) error { return tx.ImageAttachmentReadable(child.ID, first.Attachment) }); err != nil {
		t.Fatal("Child lifetime depended on erased source", err)
	}
	last, _, err := s.DeleteSession(ctx, domain.NewID(), child.ID, server, child.Revision)
	if err != nil || len(last.Workers) != 1 || !slices.Equal(last.Workers[0].Work.Images, []domain.ImageAttachment{first.Attachment}) || last.Workers[0].Work.DeviceID != device {
		t.Fatal("Child deletion lost original image owner", last, err)
	}
	if err := s.Read(ctx, func(tx *Tx) error { return tx.ImageAttachmentReadable(child.ID, first.Attachment) }); err == nil {
		t.Fatal("Deleting session kept image read authority")
	}
}

func TestImageLifetimeRejectsMalformedOwnershipAndQuarantinedReadback(t *testing.T) {
	s, _ := openTest(t)
	session, input, _ := deletionSession(t, s, "session")
	v := imageLifetimeFixture(t, s, session.ID, input.ID, domain.NewID(), domain.NewID())
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.quarantine", nil, func(tx *Tx) (any, error) {
		row, upload, err := tx.ImageUploadRecord(v.Attachment.ID)
		if err != nil {
			return nil, err
		}
		upload.Owners = append(upload.Owners, session.ID)
		if _, err := tx.PutImageUpload(row, upload); err == nil {
			t.Fatal("Duplicate deletion owners accepted")
		}
		upload = v
		upload.Quarantined = true
		return tx.PutImageUpload(row, upload)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(context.Background(), func(tx *Tx) error { return tx.ImageAttachmentReadable(session.ID, v.Attachment) }); err == nil {
		t.Fatal("Historical image gained read authority")
	}
}

func TestImageLifetimeRestorePreservesCurrentRemovalAndQuarantinesOwnership(t *testing.T) {
	s, root, ctx, restore, _ := restoreFixture(t)
	session, input, _ := deletionSession(t, s, "current image owner")
	removed := imageLifetimeFixture(t, s, session.ID, input.ID, domain.NewID(), domain.NewID())
	retained := imageLifetimeFixture(t, s, session.ID, input.ID, domain.NewID(), domain.NewID())
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.observed-image-removal", nil, func(tx *Tx) (any, error) {
		row, v, err := tx.ImageUploadRecord(removed.Attachment.ID)
		if err != nil {
			return nil, err
		}
		v.Owners = nil
		v.State = domain.ImageDeleted
		return tx.PutImageUpload(row, v)
	})
	if err != nil {
		t.Fatal(err)
	}
	restore.ExpectedRevision, err = s.RestoreRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.RestoreBackup(ctx, domain.NewID(), restore); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Read(ctx, func(tx *Tx) error {
		_, gone, err := tx.ImageUploadRecord(removed.Attachment.ID)
		if err != nil {
			return err
		}
		if gone.State != domain.ImageDeleted || len(gone.Owners) != 0 || !gone.Quarantined || gone.WorkerDeviceID != removed.WorkerDeviceID {
			t.Fatal("Restore rolled back original removal", gone)
		}
		_, current, err := tx.ImageUploadRecord(retained.Attachment.ID)
		if err != nil {
			return err
		}
		if current.State != domain.ImageClaimed || !current.Quarantined || !slices.Equal(current.Owners, retained.Owners) || current.WorkerDeviceID != retained.WorkerDeviceID {
			t.Fatal("Restore manufactured attachment authority", current)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
