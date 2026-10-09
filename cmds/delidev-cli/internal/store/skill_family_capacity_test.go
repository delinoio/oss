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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func skillFamilyBinding(device domain.ID) domain.SkillBinding {
	return domain.SkillBinding{WorkerDeviceID: device, InventoryID: domain.NewID(), SkillID: domain.NewID(), SnapshotID: domain.NewID(), ContentRevision: strings.Repeat("a", 64)}
}
func addFamilySkills(tx *Tx, session, device domain.ID, count int) (Record, error) {
	if err := tx.CheckSkillSnapshotCapacity(session, "", count); err != nil {
		return Record{}, err
	}
	bindings := make([]domain.SkillBinding, count)
	for i := range bindings {
		bindings[i] = skillFamilyBinding(device)
	}
	var sequence uint64
	if err := tx.tx.QueryRowContext(tx.ctx, "SELECT COALESCE(MAX(CAST(json_extract(body,'$.sequence') AS INTEGER)),0)+1 FROM entities WHERE kind='queue' AND session_id=?", session).Scan(&sequence); err != nil {
		return Record{}, err
	}
	current := count
	if current > 8 {
		current = 8
	}
	return tx.Put(domain.QueueKind, domain.NewID(), 0, session, "", domain.QueuedInput{Sequence: sequence, ContentRevision: 1, Mode: domain.PlanMode, Delivery: domain.InputRemoved, Skills: bindings[:current], RetiredSkills: bindings[current:]})
}
func cloneFamilySidechat(tx *Tx, parent domain.ID, original domain.Session, machine domain.ID) (Record, error) {
	id := domain.NewID()
	v := original
	fork := *original.Fork
	fork.JobID = domain.NewID()
	fork.RuntimeID = domain.NewID()
	fork.NativeThreadID = domain.NativeIdentity(domain.NewID())
	v.Fork = &fork
	preparation := domain.NewID()
	raw, _ := json.Marshal(workspace.PrepareRequest{SessionID: id, MachineID: machine, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}})
	now := time.Now().UTC()
	if _, err := tx.PutJob(preparation, 0, id, "", domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobSucceeded, MachineID: machine, Input: raw, Output: []byte(`{}`), AcceptedAt: now, FinishedAt: &now}); err != nil {
		return Record{}, err
	}
	v.Preparation = &domain.SessionPreparation{JobID: preparation, State: domain.PreparationReady}
	if err := tx.RegisterSidechat(parent, id); err != nil {
		return Record{}, err
	}
	return tx.Put(domain.SessionKind, id, 0, id, "", v)
}

func TestSkillFamilyAdmissionKeepsCompleteOuterDeletionEnvelopeBounded(t *testing.T) {
	s, _ := openTest(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	server := domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	parent, child, independent, worker, _ := sidechatDeletionFixture(t, s, ctx)
	sessions := []domain.ID{parent.ID, child.ID}
	var first Record
	request := domain.NewID()
	_, err := s.Mutate(ctx, request, "fixture.skill-family", nil, func(tx *Tx) (any, error) {
		value, _ := Decode[domain.Session](parent)
		value.MachineID = worker.MachineID
		var err error
		parent, err = tx.Put(domain.SessionKind, parent.ID, parent.Revision, parent.ID, "", value)
		if err != nil {
			return nil, err
		}
		original, _ := Decode[domain.Session](child)
		for i := 0; i < 2; i++ {
			row, err := cloneFamilySidechat(tx, parent.ID, original, worker.MachineID)
			if err != nil {
				return nil, err
			}
			sessions = append(sessions, row.ID)
		}
		for _, id := range sessions {
			for i := 0; i < 64; i++ {
				row, err := addFamilySkills(tx, id, worker.DeviceID, 16)
				if err != nil {
					return nil, err
				}
				if first.ID == "" {
					first = row
				}
			}
		}
		// An independent Fork carries source history, but joins no dependent budget.
		fork := *original.Fork
		fork.SidechatParentSnapshot = nil
		original.Source = domain.ManualSession
		original.Fork = &fork
		if _, err := tx.Put(domain.SessionKind, independent.ID, independent.Revision, independent.ID, "", original); err != nil {
			return nil, err
		}
		if _, err := addFamilySkills(tx, independent.ID, worker.DeviceID, 16); err != nil {
			return nil, err
		}
		return sessions, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Exact replay skips admission without counting accepted ownership twice.
	if result, err := s.Mutate(ctx, request, "fixture.skill-family", nil, func(*Tx) (any, error) { t.Fatal("replay reran admission"); return nil, nil }); err != nil || !result.Replayed {
		t.Fatal("lost exact admission receipt", err)
	}
	for _, id := range sessions {
		err := s.Read(ctx, func(tx *Tx) error { return tx.CheckSkillSnapshotCapacity(id, "", 1) })
		if domain.SafeError(err).Code != domain.ResourceExhausted {
			t.Fatal("family member exceeded aggregate budget", id, err)
		}
	}
	if err := s.Read(ctx, func(tx *Tx) error { return tx.CheckSkillSnapshotCapacity(parent.ID, first.ID, 17) }); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("edit dropped retired ownership from budget", err)
	}
	// Publication must recheck an incoming child's accepted references atomically.
	if _, err := s.Mutate(ctx, domain.NewID(), "fixture.skill-family-join", nil, func(tx *Tx) (any, error) { return nil, tx.RegisterSidechat(parent.ID, independent.ID) }); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("Sidechat publication bypassed family budget", err)
	}
	plan, _, err := s.DeleteSession(ctx, domain.NewID(), parent.ID, server, parent.Revision)
	if err != nil {
		t.Fatal("accepted family cannot be deleted", err)
	}
	raw, err := json.Marshal(plan)
	if err != nil || len(raw) > domain.MaxSessionDeletionBytes || len(plan.Dependents) != 3 || plan.validate() != nil {
		t.Fatal("full outer envelope is invalid", len(raw), err)
	}
	count := 0
	for _, v := range append([]SessionDeletion{plan}, plan.Dependents...) {
		for _, w := range v.Workers {
			if w.Work.Validate() != nil {
				t.Fatal("invalid original Worker work")
			}
			count += len(w.Work.SkillSnapshots)
		}
	}
	if count != domain.MaxRetainedSkillSnapshots {
		t.Fatal("current/retired ownership disappeared", count)
	}
	if _, err := s.Get(ctx, domain.SessionKind, independent.ID); err != nil {
		t.Fatal("independent Fork joined parent deletion", err)
	}
	t.Logf("complete parent plus three prepared Sidechats: refs=%d outer_bytes=%d bound=%d", count, len(raw), domain.MaxSessionDeletionBytes)
}

func TestSkillFamilyConcurrentParentInputAndChildEditAdmissionIsAtomic(t *testing.T) {
	s, _ := openTest(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	parent, child, _, worker, _ := sidechatDeletionFixture(t, s, ctx)
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.skill-family-fill", nil, func(tx *Tx) (any, error) {
		for remaining := 4095; remaining > 0; {
			count := 16
			if remaining < count {
				count = remaining
			}
			if _, err := addFamilySkills(tx, parent.ID, worker.DeviceID, count); err != nil {
				return nil, err
			}
			remaining -= count
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var edited Record
	if _, err := s.Mutate(ctx, domain.NewID(), "fixture.empty-child-edit", nil, func(tx *Tx) (any, error) {
		var err error
		edited, err = addFamilySkills(tx, child.ID, worker.DeviceID, 0)
		return edited, err
	}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []domain.ID{parent.ID, child.ID} {
		wg.Add(1)
		go func(id domain.ID) {
			defer wg.Done()
			_, err := s.Mutate(ctx, domain.NewID(), "fixture.skill-family-race", id, func(tx *Tx) (any, error) {
				if id == child.ID {
					if err := tx.CheckSkillSnapshotCapacity(id, edited.ID, 1); err != nil {
						return nil, err
					}
					value, _ := Decode[domain.QueuedInput](edited)
					value.ContentRevision++
					value.Skills = []domain.SkillBinding{skillFamilyBinding(worker.DeviceID)}
					return tx.Put(domain.QueueKind, edited.ID, edited.Revision, id, "", value)
				}
				row, err := addFamilySkills(tx, id, worker.DeviceID, 1)
				return row, err
			})
			results <- err
		}(id)
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
		t.Fatal("concurrent admission overcommitted family", accepted, rejected)
	}
}

func TestSkillFamilyCapacityRetainsPurgedDependentUntilConfirmedRetirement(t *testing.T) {
	s, _ := openTest(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	server := domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	parent, child, _, worker, instance := sidechatDeletionFixture(t, s, ctx)
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.child-skills", nil, func(tx *Tx) (any, error) {
		_, err := addFamilySkills(tx, child.ID, worker.DeviceID, 16)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := s.DeleteSession(ctx, domain.NewID(), child.ID, server, child.Revision)
	if err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		if err := s.Read(ctx, func(tx *Tx) error { return tx.CheckSkillSnapshotCapacity(parent.ID, "", 1) }); err == nil {
			t.Fatal("unconfirmed dependent cleanup released family admission")
		}
		if err := s.Read(ctx, func(tx *Tx) error { return tx.RequireSidechatCapacity(parent.ID) }); err == nil {
			t.Fatal("new Sidechat bypassed original dependent cleanup")
		}
		if err := s.Read(ctx, func(tx *Tx) error { return tx.CheckSkillSnapshotCapacity(parent.ID, "", 0) }); err != nil {
			t.Fatal("plain input acquired package cleanup fence", err)
		}
	}
	check()
	if _, err := s.AcknowledgeSessionDeletion(domain.WithPrincipal(context.Background(), worker), child.ID, plan.ID, domain.NewID(), instance, plan.Workers[0].Work.Digest()); err != nil {
		t.Fatal(err)
	}
	removed, err := s.PurgeDeletedSession(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	check()
	if err := s.RemoveSessionBackups(ctx, removed); err != nil {
		t.Fatal(err)
	}
	check()
	if _, err := s.CompleteSessionDeletion(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		if err := tx.RequireSidechatCapacity(parent.ID); err != nil {
			return err
		}
		return tx.CheckSkillSnapshotCapacity(parent.ID, "", domain.MaxRetainedSkillSnapshots)
	}); err != nil {
		t.Fatal("confirmed retirement did not release capacity", err)
	}
}
