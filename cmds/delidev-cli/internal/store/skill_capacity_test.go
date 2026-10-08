// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
)

func TestSkillSnapshotAdmissionSharesWholeSessionCleanupBound(t *testing.T) {
	s, _ := openTest(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	server := domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	session, queue, _ := deletionSession(t, s, "capacity")
	device := domain.NewID()
	references := func(count int) []domain.SkillBinding {
		out := make([]domain.SkillBinding, count)
		for i := range out {
			out[i] = domain.SkillBinding{WorkerDeviceID: device, InventoryID: domain.NewID(), SkillID: domain.NewID(), SnapshotID: domain.NewID(), ContentRevision: strings.Repeat("a", 64)}
		}
		return out
	}
	rows := []Record{queue}
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.fill-skills", nil, func(tx *Tx) (any, error) {
		value, e := Decode[domain.Session](session)
		if e != nil {
			return nil, e
		}
		value.MachineID = domain.NewID()
		session, e = tx.Put(domain.SessionKind, session.ID, session.Revision, session.ID, "", value)
		if e != nil {
			return nil, e
		}
		for index := 0; index < 2; index++ {
			id, revision := domain.NewID(), uint64(0)
			if index == 0 {
				id, revision = queue.ID, queue.Revision
			}
			if e = tx.CheckSkillSnapshotCapacity(session.ID, id, 2048); e != nil {
				return nil, e
			}
			bindings := references(2048)
			row, e := tx.Put(domain.QueueKind, id, revision, session.ID, "", domain.QueuedInput{Skills: bindings[:1], RetiredSkills: bindings[1:], Prompt: "accepted", Mode: domain.ExecuteMode, Delivery: domain.InputRemoved, Sequence: uint64(index + 1), ContentRevision: 1})
			if e != nil {
				return nil, e
			}
			if index == 0 {
				rows[0] = row
			} else {
				rows = append(rows, row)
			}
		}
		return session, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, replace := range []domain.ID{"", rows[0].ID} {
		err = s.Read(ctx, func(tx *Tx) error {
			count := 1
			if replace != "" {
				count = 2049
			}
			return tx.CheckSkillSnapshotCapacity(session.ID, replace, count)
		})
		var problem *domain.Error
		if !errors.As(err, &problem) || problem.Code != domain.ResourceExhausted {
			t.Fatal("aggregate bound admitted undeletable references", err)
		}
	}
	plan, _, err := s.DeleteSession(ctx, domain.NewID(), session.ID, server, session.Revision)
	if err != nil || len(plan.Workers) != 1 || len(plan.Workers[0].Work.SkillSnapshots) != domain.MaxRetainedSkillSnapshots {
		t.Fatal("accepted complete session cannot be deleted", err)
	}
}
