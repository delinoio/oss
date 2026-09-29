package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func deletionSession(t *testing.T, s *Store, name string) (Record, Record, domain.ID) {
	t.Helper()
	id, input := domain.NewID(), domain.NewID()
	request := domain.NewID()
	var sr, qr Record
	_, e := s.Mutate(context.Background(), request, "fixture.session", nil, func(tx *Tx) (any, error) {
		var e error
		sr, e = tx.Put(domain.SessionKind, id, 0, "", "", domain.Session{Name: name, Outcome: domain.ExecutionNotStarted, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Dispatch: domain.DispatchBlocked})
		if e != nil {
			return nil, e
		}
		qr, e = tx.Put(domain.QueueKind, input, 0, id, "", domain.QueuedInput{Sequence: 1, ContentRevision: 1, Prompt: name + " private content", Mode: domain.ExecuteMode, Delivery: domain.InputQueued})
		return qr, e
	})
	if e != nil {
		t.Fatal(e)
	}
	return sr, qr, request
}
func TestSessionDeletionPurgesGraphBackupsAndSurvivesDatabaseRollback(t *testing.T) {
	s, root := openTest(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	server := domain.NewID()
	if e := s.BindIdentity(ctx, server); e != nil {
		t.Fatal(e)
	}
	target, input, creation := deletionSession(t, s, "target")
	other, _, _ := deletionSession(t, s, "other")
	backup, e := s.Backup(ctx)
	if e != nil {
		t.Fatal(e)
	}
	old, e := os.ReadFile(filepath.Join(root, "backups", string(backup)+".sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	request := domain.NewID()
	v, replay, e := s.DeleteSession(ctx, request, target.ID, server, target.Revision)
	if e != nil || replay || v.DatabaseRemoved || v.FinishedAt != nil {
		t.Fatal(v, replay, e)
	}
	if _, _, e := s.DeleteSession(ctx, domain.NewID(), target.ID, server, target.Revision); e == nil {
		t.Fatal("replaced irrevocable request")
	}
	if _, e := s.Mutate(ctx, domain.NewID(), "fixture.enqueue", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.QueueKind, domain.NewID(), 0, target.ID, "", domain.QueuedInput{})
	}); e == nil {
		t.Fatal("created new copy after deletion intent")
	}
	v, e = s.PurgeDeletedSession(ctx, target.ID)
	if e != nil || !v.DatabaseRemoved {
		t.Fatal(v, e)
	}
	if _, e = s.Get(ctx, domain.QueueKind, input.ID); e == nil {
		t.Fatal("queue content retained")
	}
	if _, e = s.Get(ctx, domain.SessionKind, other.ID); e != nil {
		t.Fatal("other session removed", e)
	}
	r, found, e := s.Replay(context.Background(), creation, "fixture.session", nil)
	if e != nil {
		t.Fatal(e)
	}
	if !found || !r.Replayed || strings.Contains(string(r.Data), "private") {
		t.Fatal("receipt retained content", r)
	}
	if e = s.RemoveSessionBackups(ctx, v); e != nil {
		t.Fatal(e)
	}
	v, e = s.CompleteSessionDeletion(ctx, target.ID)
	if e != nil || v.FinishedAt == nil {
		t.Fatal(v, e)
	}
	if _, e = os.Lstat(filepath.Join(root, "backups", string(backup)+".sqlite")); !os.IsNotExist(e) {
		t.Fatal("managed backup retained", e)
	}
	if _, again, e := s.DeleteSession(ctx, request, target.ID, server, target.Revision); e != nil || !again {
		t.Fatal("receipt retry failed", e)
	}
	// Simulate replacing only the database with an older same-server image.
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(filepath.Join(root, "state.sqlite") + suffix)
	}
	if e = os.WriteFile(filepath.Join(root, "state.sqlite"), old, 0600); e != nil {
		t.Fatal(e)
	}
	s, e = Open(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.RestoreSessionDeletionIntents(ctx, server); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, domain.SessionKind, target.ID); e == nil {
		t.Fatal("restored database resurrected target")
	}
	if _, e = s.Get(ctx, domain.SessionKind, other.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.Mutate(ctx, domain.NewID(), "stale.worker", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.QueueKind, input.ID, 0, target.ID, "", domain.QueuedInput{Prompt: "stale"})
	})
	if e == nil {
		t.Fatal("stale Worker report resurrected content")
	}
}

func TestSessionDeletionRequiresOriginalOfflineWorkerAcknowledgment(t *testing.T) {
	s, _ := openTest(t)
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	server, machine, device, instance := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	if e := s.BindIdentity(owner, server); e != nil {
		t.Fatal(e)
	}
	sr, _, _ := deletionSession(t, s, "offline")
	jobID := domain.NewID()
	input, _ := json.Marshal(map[string]any{"session_id": sr.ID, "machine_id": machine, "type": domain.GeneralChat, "repositories": []any{}})
	_, e := s.Mutate(owner, domain.NewID(), "fixture.worker", nil, func(tx *Tx) (any, error) {
		if _, e := tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "Worker", OS: "linux", Architecture: "amd64"}); e != nil {
			return nil, e
		}
		if _, e := tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Worker", Type: domain.WorkerDevice, MachineID: machine, PairedAt: time.Now().UTC()}); e != nil {
			return nil, e
		}
		if e := tx.SetWorkerInstance(machine, instance, time.Now().UTC()); e != nil {
			return nil, e
		}
		j := domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobQueued, MachineID: machine, Input: input, AcceptedAt: time.Now().UTC()}
		r, e := tx.PutJob(jobID, 0, sr.ID, "", j)
		if e != nil {
			return nil, e
		}
		j.State = domain.JobClaimed
		j.InstanceID = instance
		j.AssignedDeviceID = device
		return tx.PutJob(jobID, r.Revision, sr.ID, "", j)
	})
	if e != nil {
		t.Fatal(e)
	}
	v, _, e := s.DeleteSession(owner, domain.NewID(), sr.ID, server, sr.Revision)
	if e != nil || len(v.Workers) != 1 {
		t.Fatal(v, e)
	}
	if _, e := s.PurgeDeletedSession(owner, sr.ID); e == nil {
		t.Fatal("completed offline cleanup")
	}
	worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: device, MachineID: machine})
	report := domain.NewID()
	if _, e := s.AcknowledgeSessionDeletion(worker, sr.ID, v.ID, report, domain.NewID(), v.Workers[0].Work.Digest()); e == nil {
		t.Fatal("foreign instance acknowledged")
	}
	v, e = s.AcknowledgeSessionDeletion(worker, sr.ID, v.ID, report, instance, v.Workers[0].Work.Digest())
	if e != nil || !v.Workers[0].Acknowledged {
		t.Fatal(v, e)
	}
	if _, e := s.AcknowledgeSessionDeletion(worker, sr.ID, v.ID, report, instance, v.Workers[0].Work.Digest()); e != nil {
		t.Fatal(e)
	}
	if _, e := s.PurgeDeletedSession(owner, sr.ID); e != nil {
		t.Fatal(e)
	}
}

func TestSessionDeletionIntentPersistenceFailurePreservesSources(t *testing.T) {
	s, root := openTest(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	server := domain.NewID()
	_ = s.BindIdentity(ctx, server)
	sr, _, _ := deletionSession(t, s, "preserved")
	path := filepath.Join(root, "session-deletions")
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte("block"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.DeleteSession(ctx, domain.NewID(), sr.ID, server, sr.Revision); e == nil {
		t.Fatal("accepted without private intent")
	}
	r, e := s.Get(ctx, domain.SessionKind, sr.ID)
	if e != nil || r.Revision != sr.Revision {
		t.Fatal("changed session before intent", r, e)
	}
}

func TestSessionDeletionRetiresSharedRemediationOperandsWithoutRefundingCounters(t *testing.T) {
	s, _ := openTest(t)
	ctx := notificationOwner()
	f := newRemediationStoreFixture(t, s)
	a, e := f.reserve(t, domain.PRRemediationManual)
	if e != nil {
		t.Fatal(e)
	}
	a = f.bind(t, a)
	bound, e := Decode[domain.PRRemediationAttempt](a)
	if e != nil {
		t.Fatal(e)
	}
	before := f.chain(t)
	session, e := s.Get(ctx, domain.SessionKind, bound.SessionID)
	if e != nil {
		t.Fatal(e)
	}
	server := domain.NewID()
	if e := s.BindIdentity(ctx, server); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.DeleteSession(ctx, domain.NewID(), session.ID, server, session.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e := s.PurgeDeletedSession(ctx, session.ID); e != nil {
		t.Fatal(e)
	}
	after := f.chain(t)
	if after.ActiveAttemptID != "" || before.Sequence != after.Sequence || before.AutomaticAttempts != after.AutomaticAttempts {
		t.Fatal("shared counters changed", before, after)
	}
	e = s.Read(ctx, func(tx *Tx) error {
		_, v, e := tx.GetPRRemediationAttempt(a.ID)
		if e != nil {
			return e
		}
		if v.SessionID != "" || v.InputID != "" || v.InputDigest != "" || v.ExecutionID != "" || v.State != domain.PRRemediationCanceled {
			t.Fatal("retained session operands", v)
		}
		_, e = tx.PRRemediationHistoryFingerprint(f.set.ID)
		return e
	})
	if e != nil {
		t.Fatal("shared history became invalid", e)
	}
}
