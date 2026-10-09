// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"slices"
	"testing"
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
		if len(plan.Workers) != 1 || !slices.Equal(plan.Workers[0].Work.Images, []domain.ImageAttachment{b.Attachment}) || !slices.Equal(plan.Workers[0].Work.PreservedGeneratedImages, []domain.ImageAttachment{a.Attachment}) {
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
		if len(last.Workers) != 1 || !slices.Equal(last.Workers[0].Work.Images, []domain.ImageAttachment{a.Attachment}) || len(last.Workers[0].Work.PreservedGeneratedImages) != 0 {
			t.Fatal("last-owner output cleanup lost")
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
