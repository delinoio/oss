// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func capacityImage(session, machine, device domain.ID) domain.ImageUpload {
	return domain.ImageUpload{Version: 1, Attachment: domain.ImageAttachment{ID: domain.NewID(), MachineID: machine, MediaType: domain.ImagePNG, ByteLength: 1, SHA256: strings.Repeat("a", 64)}, WorkerDeviceID: device, Actor: domain.Principal{Type: domain.OwnerDevice}, DraftID: domain.NewID(), OperationID: domain.NewID(), MachineRevision: 1, SessionID: session, State: domain.ImageReady, UploadedBytes: 1}
}
func seedCapacityImage(tx *Tx, v domain.ImageUpload) (Record, error) {
	raw, _ := json.Marshal(v)
	return tx.PutJob(v.Attachment.ID, 0, "", "", domain.Job{Type: domain.ImageAttachmentJob, State: domain.JobSucceeded, MachineID: v.Attachment.MachineID, Input: raw, AcceptedAt: time.Now().UTC()})
}
func TestImageCapacityCountsDistinctCurrentAndRemovedInputsAndUncertainDrafts(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	session, machine, device := domain.NewID(), domain.NewID(), domain.NewID()
	var terminal, pending domain.ImageUpload
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.capacity", nil, func(tx *Tx) (any, error) {
		// Claimed references retain their capacity even after queue removal.
		for i := 0; i < domain.MaxSessionImageAttachments-1; i++ {
			v := capacityImage(session, machine, device)
			v.InputID = domain.NewID()
			if i == 0 {
				if _, err := tx.Put(domain.QueueKind, v.InputID, 0, session, "", domain.QueuedInput{Sequence: 1, ContentRevision: 1, Mode: domain.ExecuteMode, Delivery: domain.InputRemoved, Attachments: []domain.ImageAttachment{v.Attachment}}); err != nil {
					return nil, err
				}
			}
			v.State = domain.ImageClaimed
			v.Owners = []domain.ID{session}
			if _, err := seedCapacityImage(tx, v); err != nil {
				return nil, err
			}
		}
		pending = capacityImage(session, machine, device)
		pending.State = domain.ImageDeleting
		pending.Quarantined = true
		if _, err := seedCapacityImage(tx, pending); err != nil {
			return nil, err
		}
		terminal = capacityImage(session, machine, device)
		terminal.State = domain.ImageDeleted
		if _, err := seedCapacityImage(tx, terminal); err != nil {
			return nil, err
		}
		// A released source reference lives only on the independent child.
		released := capacityImage(session, machine, device)
		released.InputID = domain.NewID()
		released.State = domain.ImageClaimed
		released.Owners = []domain.ID{domain.NewID()}
		if _, err := seedCapacityImage(tx, released); err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := capacityImage(session, machine, device)
	if err := s.Read(ctx, func(tx *Tx) error {
		rows, err := tx.sessionImageUploads(session)
		if err != nil {
			return err
		}
		if len(rows) != domain.MaxSessionImageAttachments {
			t.Fatalf("inventory=%d", len(rows))
		}
		return tx.ValidateImageCapacity(candidate)
	}); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatalf("overflow admitted: %v", err)
	}
	// Only independently observed terminal removal frees the uncertain slot.
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.confirmed-removal", nil, func(tx *Tx) (any, error) {
		row, v, err := tx.ImageUploadRecord(pending.Attachment.ID)
		if err != nil {
			return nil, err
		}
		v.State = domain.ImageDeleted
		return tx.PutImageUpload(row, v)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error { return tx.ValidateImageCapacity(candidate) }); err != nil {
		t.Fatal(err)
	}
}

func TestImageCapacityConcurrentAdmissionAndSameReferenceUpdate(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	session, machine, device := domain.NewID(), domain.NewID(), domain.NewID()
	parent, input, execution := domain.NewID(), domain.NewID(), domain.NewID()
	var retained domain.ImageUpload
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.capacity", nil, func(tx *Tx) (any, error) {
		for i := 0; i < domain.MaxSessionImageAttachments-1; i++ {
			v := capacityImage(session, machine, device)
			retained = v
			if _, err := seedCapacityImage(tx, v); err != nil {
				return nil, err
			}
		}
		v := capacityImage(parent, machine, device)
		v.InputID = input
		v.State = domain.ImageClaimed
		v.Owners = []domain.ID{parent}
		if _, err := seedCapacityImage(tx, v); err != nil {
			return nil, err
		}
		return tx.Put(domain.QueueKind, input, 0, parent, "", domain.QueuedInput{Sequence: 1, ContentRevision: 1, Mode: domain.ExecuteMode, Delivery: domain.InputAccepted, ExecutionID: execution, Attachments: []domain.ImageAttachment{v.Attachment}})
	})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for kind := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := capacityImage(session, machine, device)
			_, err := s.Mutate(ctx, domain.NewID(), "fixture.admit", nil, func(tx *Tx) (any, error) {
				if kind == 1 {
					return nil, tx.InheritForkImages(domain.ForkJobInput{Purpose: domain.IndependentFork, SourceSessionID: parent, ChildSessionID: session, Completion: domain.ExecutionCompletion{InputID: input, ExecutionID: execution}})
				}
				if err := tx.ValidateImageCapacity(v); err != nil {
					return nil, err
				}
				return seedCapacityImage(tx, v)
			})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	accepted, rejected := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if domain.SafeError(err).Code == domain.ResourceExhausted {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("accepted=%d rejected=%d", accepted, rejected)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.claim", nil, func(tx *Tx) (any, error) {
		row, v, err := tx.ImageUploadRecord(retained.Attachment.ID)
		if err != nil {
			return nil, err
		}
		v.InputID = domain.NewID()
		v.State = domain.ImageClaimed
		v.Owners = []domain.ID{session}
		return tx.PutImageUpload(row, v)
	})
	if err != nil {
		t.Fatal("same reference consumed another slot", err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		rows, err := tx.sessionImageUploads(session)
		if err != nil {
			return err
		}
		if len(rows) != domain.MaxSessionImageAttachments {
			t.Fatalf("inventory=%d", len(rows))
		}
		plan, err := tx.planImageSessionDeletion(SessionDeletion{ID: domain.NewID(), ServerID: domain.NewID(), SessionID: session})
		if err != nil {
			return err
		}
		if len(plan.Workers) != 1 || (len(plan.Workers[0].Work.Images) != domain.MaxSessionImageAttachments && len(plan.Workers[0].Work.Images) != domain.MaxSessionImageAttachments-1) {
			t.Fatal("complete deletion inventory was not retained")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestImageCapacityForkAdmissionRollsBackCompleteOwnerSet(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	parent, child, machine, device, input, execution := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	var original []domain.ImageUpload
	var childDraft domain.ImageUpload
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.fork-capacity", nil, func(tx *Tx) (any, error) {
		for i := 0; i < domain.MaxSessionImageAttachments-1; i++ {
			childDraft = capacityImage(child, machine, device)
			if _, err := seedCapacityImage(tx, childDraft); err != nil {
				return nil, err
			}
		}
		var refs []domain.ImageAttachment
		for range 2 {
			v := capacityImage(parent, machine, device)
			v.InputID = input
			v.State = domain.ImageClaimed
			v.Owners = []domain.ID{parent}
			original = append(original, v)
			refs = append(refs, v.Attachment)
			if _, err := seedCapacityImage(tx, v); err != nil {
				return nil, err
			}
		}
		return tx.Put(domain.QueueKind, input, 0, parent, "", domain.QueuedInput{Sequence: 1, ContentRevision: 1, Mode: domain.ExecuteMode, Delivery: domain.InputAccepted, ExecutionID: execution, Attachments: refs})
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.inherit", nil, func(tx *Tx) (any, error) {
		return nil, tx.InheritForkImages(domain.ForkJobInput{Purpose: domain.IndependentFork, SourceSessionID: parent, ChildSessionID: child, Completion: domain.ExecutionCompletion{InputID: input, ExecutionID: execution}})
	})
	if domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatalf("oversized fork admitted: %v", err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		for _, v := range original {
			_, actual, err := tx.ImageUploadRecord(v.Attachment.ID)
			if err != nil {
				return err
			}
			if len(actual.Owners) != 1 || actual.Owners[0] != parent {
				t.Fatal("partial Fork owner survived rollback")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.fork-slot-release", nil, func(tx *Tx) (any, error) {
		row, v, err := tx.ImageUploadRecord(childDraft.Attachment.ID)
		if err != nil {
			return nil, err
		}
		v.State = domain.ImageDeleted
		return tx.PutImageUpload(row, v)
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		_, err = s.Mutate(ctx, domain.NewID(), "fixture.fork-boundary", nil, func(tx *Tx) (any, error) {
			return nil, tx.InheritForkImages(domain.ForkJobInput{Purpose: domain.IndependentFork, SourceSessionID: parent, ChildSessionID: child, Completion: domain.ExecutionCompletion{InputID: input, ExecutionID: execution}})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		rows, err := tx.sessionImageUploads(child)
		if err != nil {
			return err
		}
		if len(rows) != domain.MaxSessionImageAttachments {
			t.Fatalf("Fork boundary inventory=%d", len(rows))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

}
