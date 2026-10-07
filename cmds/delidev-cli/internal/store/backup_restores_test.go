package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func restoreFixture(t *testing.T) (*Store, string, context.Context, BackupRestoreInput, domain.ID) {
	t.Helper()
	s, root := openTest(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	server := domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	create(t, s, id, "committed WAL before backup")
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := s.InspectBackup(ctx, backup, server)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := s.RestoreRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return s, root, ctx, BackupRestoreInput{Backup: observed.Backup, SHA256: observed.SHA256, ServerID: server, Actor: domain.Principal{Type: domain.OwnerDevice}, ExpectedRevision: revision}, id
}

// Exit immediately rather than running Store.Close or scratch defers. The
// parent owns every file, request and subprocess and tests actual lock release
// and startup recovery with the journal exactly as the killed process left it.
func TestBackupRestoreProcessCrash(t *testing.T) {
	if raw := os.Getenv("DELIDEV_TEST_RESTORE_CRASH"); raw != "" {
		var control struct {
			Root, Boundary string
			Request        domain.ID
			Input          BackupRestoreInput
		}
		if err := json.Unmarshal([]byte(raw), &control); err != nil {
			t.Fatal(err)
		}
		ctx := domain.WithPrincipal(context.Background(), control.Input.Actor)
		s, err := Open(ctx, control.Root)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = s.restoreBackupWithBarrier(ctx, control.Request, control.Input, func(boundary string) error {
			if boundary == control.Boundary {
				os.Exit(89)
			}
			return nil
		}, func() error {
			return security.WriteAtomic(filepath.Join(control.Root, "epoch-ended.json"), []byte(`{"stopped":true}`))
		})
		t.Fatal("crash checkpoint not reached", err)
	}
	for _, boundary := range []string{"staged", "closed", "journaled", "prepared", "renamed", "synchronized", "recorded"} {
		t.Run(boundary, func(t *testing.T) {
			s, root, ctx, in, _ := restoreFixture(t)
			later := domain.NewID()
			create(t, s, later, "preserved until replacement")
			in.ExpectedRevision, _ = s.RestoreRevision(ctx)
			request := domain.NewID()
			s.Close()
			raw, _ := json.Marshal(struct {
				Root, Boundary string
				Request        domain.ID
				Input          BackupRestoreInput
			}{root, boundary, request, in})
			child := exec.Command(os.Args[0], "-test.run=^TestBackupRestoreProcessCrash$")
			child.Env = append(os.Environ(), "DELIDEV_TEST_RESTORE_CRASH="+string(raw))
			output, err := child.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 89 {
				t.Fatal("unexpected child outcome", err, string(output))
			}
			reopened, err := Open(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			_, laterErr := reopened.Get(ctx, domain.ProjectKind, later)
			receipt, err := reopened.GetBackupRestore(ctx, request)
			switch boundary {
			case "staged", "closed":
				if laterErr != nil || domain.SafeError(err).Code != domain.NotFound {
					t.Fatal(laterErr, receipt, err)
				}
			case "journaled", "prepared":
				if laterErr != nil || err != nil || receipt.State != RestoreRolledBack {
					t.Fatal(laterErr, receipt, err)
				}
			default:
				if laterErr == nil || err != nil || receipt.State != RestoreCompleted {
					t.Fatal(laterErr, receipt, err)
				}
				if _, err := security.ReadPrivate(filepath.Join(root, "epoch-ended.json"), 64); err != nil {
					t.Fatal("publication crossed an uncommitted epoch barrier", err)
				}
			}
		})
	}
}

func TestBackupRestorePreservesExternalBackupDeletion(t *testing.T) {
	s, root, ctx, in, _ := restoreFixture(t)
	other, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := s.InspectBackup(ctx, other, in.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	deletionRequest := domain.NewID()
	job, _, err := s.DeleteBackup(ctx, deletionRequest, BackupDeletionInput{Backup: inspection.Backup, SHA256: inspection.SHA256, ExpectedRevision: 1, ServerID: in.ServerID, Actor: in.Actor})
	if err != nil {
		t.Fatal(err)
	}
	in.ExpectedRevision, _ = s.RestoreRevision(ctx)
	if _, _, err := s.RestoreBackup(ctx, domain.NewID(), in); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.RestoreBackupDeletionIntents(ctx, in.ServerID); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.RunBackupDeletion(ctx, job.ID, in.ServerID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "backups", string(other)+".sqlite")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("external deletion lost", err)
	}
	if _, err := os.Stat(filepath.Join(root, "backups", string(in.Backup.ID)+".sqlite")); err != nil {
		t.Fatal("restore removed source", err)
	}
}

func TestBackupRestoreCommittedWALAndExactReceipt(t *testing.T) {
	s, root, ctx, in, original := restoreFixture(t)
	later := domain.NewID()
	create(t, s, later, "post-backup state")
	in.ExpectedRevision, _ = s.RestoreRevision(ctx)
	source := filepath.Join(root, "backups", string(in.Backup.ID)+".sqlite")
	before, _ := os.ReadFile(source)
	request := domain.NewID()
	result, replayed, err := s.RestoreBackup(ctx, request, in)
	if err != nil || replayed || result.State != RestorePublished || !s.RestoreFrozen() {
		t.Fatal(result, replayed, err)
	}
	if _, err := s.Get(ctx, domain.ProjectKind, original); err == nil {
		t.Fatal("old epoch read the replacement")
	}
	s.Close()
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.Get(ctx, domain.ProjectKind, original); err != nil {
		t.Fatal("committed WAL content missing", err)
	}
	if _, err := reopened.Get(ctx, domain.ProjectKind, later); err == nil {
		t.Fatal("replacement retained post-backup project")
	}
	after, _ := os.ReadFile(source)
	if !bytes.Equal(before, after) {
		t.Fatal("restore changed source image")
	}
	receipt, err := reopened.GetBackupRestore(ctx, request)
	if err != nil || receipt.State != RestoreCompleted {
		t.Fatal(receipt, err)
	}
	replay, replayed, err := reopened.RestoreBackup(ctx, request, in)
	if err != nil || !replayed || replay.State != RestoreCompleted || reopened.RestoreFrozen() {
		t.Fatal(replay, replayed, err)
	}
	in.SHA256 = string(bytes.Repeat([]byte("0"), 64))
	_, _, err = reopened.RestoreBackup(ctx, request, in)
	assertCode(t, err, domain.Conflict)
	_, err = reopened.Events(ctx, 0, "", 200)
	assertCode(t, err, domain.CursorExpired)
}

func TestBackupRestoreCrashBoundaries(t *testing.T) {
	for _, boundary := range []string{"staged", "prepared", "renamed", "synchronized", "recorded"} {
		t.Run(boundary, func(t *testing.T) {
			s, root, ctx, in, original := restoreFixture(t)
			later := domain.NewID()
			create(t, s, later, "current state survives rollback")
			in.ExpectedRevision, _ = s.RestoreRevision(ctx)
			request := domain.NewID()
			fault := errors.New("test crash boundary")
			_, _, err := s.restoreBackup(ctx, request, in, func(phase string) error {
				if phase == boundary {
					return fault
				}
				return nil
			})
			if !errors.Is(err, fault) {
				t.Fatal(err)
			}
			s.Close()
			reopened, err := Open(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if _, err := reopened.Get(ctx, domain.ProjectKind, original); err != nil {
				t.Fatal(err)
			}
			_, laterErr := reopened.Get(ctx, domain.ProjectKind, later)
			receipt, err := reopened.GetBackupRestore(ctx, request)
			switch boundary {
			case "staged":
				if laterErr != nil || domain.SafeError(err).Code != domain.NotFound {
					t.Fatal(laterErr, receipt, err)
				}
			case "prepared":
				if laterErr != nil || err != nil || receipt.State != RestoreRolledBack {
					t.Fatal(laterErr, receipt, err)
				}
			default:
				if laterErr == nil || err != nil || receipt.State != RestoreCompleted {
					t.Fatal(laterErr, receipt, err)
				}
			}
		})
	}
}

func TestBackupRestoreKeepsCurrentRevocationsAndSessionDeletion(t *testing.T) {
	s, root, ctx, in, _ := restoreFixture(t)
	client, deletedSession, child, retained, job := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	digest := sha256.Sum256([]byte("fixture client credential"))
	_, err := s.Mutate(ctx, domain.NewID(), "fixture", nil, func(tx *Tx) (any, error) {
		_, err := tx.Put(domain.DeviceKind, client, 0, "", "", domain.Device{Type: domain.ClientDevice, Name: "fixture"})
		if err != nil {
			return nil, err
		}
		if err := tx.PutCredential(client, digest[:]); err != nil {
			return nil, err
		}
		for _, entry := range []struct {
			id     domain.ID
			kind   domain.Kind
			parent domain.ID
			body   any
		}{
			{deletedSession, domain.SessionKind, "", domain.Session{Name: "deleted", Dispatch: domain.DispatchPaused, Recovery: domain.NoRecovery}},
			{child, domain.MessageKind, deletedSession, map[string]string{"text": "must stay deleted"}},
			{retained, domain.SessionKind, "", domain.Session{Name: "paused", Dispatch: domain.DispatchReady, Recovery: domain.NoRecovery}},
			{job, domain.JobKind, retained, domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobQueued, Input: []byte(`{}`)}},
		} {
			if _, err := tx.Put(entry.kind, entry.id, 0, entry.parent, "", entry.body); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := s.InspectBackup(ctx, backup, in.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	in.Backup, in.SHA256 = observed.Backup, observed.SHA256
	_, err = s.Mutate(ctx, domain.NewID(), "revoke-and-delete", nil, func(tx *Tx) (any, error) {
		r, err := tx.Get(domain.DeviceKind, client)
		if err != nil {
			return nil, err
		}
		v, _ := Decode[domain.Device](r)
		v.Revoked = true
		if _, err := tx.Put(domain.DeviceKind, client, r.Revision, "", "", v); err != nil {
			return nil, err
		}
		if err := tx.RevokeCredential(client); err != nil {
			return nil, err
		}
		return nil, tx.Delete(domain.SessionKind, deletedSession, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	in.ExpectedRevision, _ = s.RestoreRevision(ctx)
	if _, _, err := s.RestoreBackup(ctx, domain.NewID(), in); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.Authenticate(ctx, digest[:]); err == nil {
		t.Fatal("revoked token regained access")
	}
	for _, entry := range []struct {
		id   domain.ID
		kind domain.Kind
	}{{deletedSession, domain.SessionKind}, {child, domain.MessageKind}} {
		if _, err := reopened.Get(ctx, entry.kind, entry.id); err == nil {
			t.Fatal("deleted data resurrected", entry)
		}
	}
	r, err := reopened.Get(ctx, domain.SessionKind, retained)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := Decode[domain.Session](r)
	if v.Dispatch != domain.DispatchPaused || v.Recovery != domain.NeedsRecovery {
		t.Fatal(v)
	}
	r, err = reopened.Get(ctx, domain.JobKind, job)
	if err != nil {
		t.Fatal(err)
	}
	j, _ := Decode[domain.Job](r)
	if j.State != domain.JobCanceled {
		t.Fatal(j)
	}
}

func TestBackupRestoreRejectsChangedInspectionRevisionAndOwnership(t *testing.T) {
	for _, mode := range []string{"digest", "revision", "active", "uncertain", "newer", "foreign", "corrupt", "replaced"} {
		t.Run(mode, func(t *testing.T) {
			s, root, ctx, in, original := restoreFixture(t)
			source := filepath.Join(root, "backups", string(in.Backup.ID)+".sqlite")
			switch mode {
			case "digest":
				in.SHA256 = string(bytes.Repeat([]byte("0"), 64))
			case "revision":
				in.ExpectedRevision++
			case "active", "uncertain":
				state := domain.JobClaimed
				if mode == "uncertain" {
					state = domain.JobUncertain
				}
				_, err := s.Mutate(ctx, domain.NewID(), "owned", nil, func(tx *Tx) (any, error) {
					return tx.Put(domain.JobKind, domain.NewID(), 0, "", "", domain.Job{Type: domain.ExecuteSessionJob, State: state, Input: []byte(`{}`)})
				})
				if err != nil {
					t.Fatal(err)
				}
				in.ExpectedRevision, _ = s.RestoreRevision(ctx)
			case "corrupt":
				if err := os.WriteFile(source, []byte("corrupt image"), 0600); err != nil {
					t.Fatal(err)
				}
			case "replaced":
				raw, _ := os.ReadFile(source)
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(source, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "newer", "foreign":
				image, err := openRestoreDatabase(source)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "newer" {
					_, err = image.Exec("PRAGMA user_version=999")
				} else {
					_, err = image.Exec("UPDATE metadata SET value=? WHERE key='server_id'", domain.NewID())
				}
				if err != nil {
					t.Fatal(err)
				}
				image.Close()
			}
			before, _ := os.ReadFile(source)
			if _, _, err := s.RestoreBackup(ctx, domain.NewID(), in); err == nil {
				t.Fatal("invalid restore accepted")
			}
			if s.RestoreFrozen() {
				t.Fatal("validation froze the live database")
			}
			if _, err := s.Get(ctx, domain.ProjectKind, original); err != nil {
				t.Fatal("live state changed", err)
			}
			after, _ := os.ReadFile(source)
			if !bytes.Equal(before, after) {
				t.Fatal("validation changed source")
			}
		})
	}
}

func TestBackupRestoreRequiresIndependentForwardCleanup(t *testing.T) {
	for _, test := range []struct {
		name    string
		forward domain.Forward
		allowed bool
	}{
		{"pending", domain.Forward{State: domain.ForwardPending}, false},
		{"active", domain.Forward{State: domain.ForwardActive, ClientClaimed: true, WorkerClaimed: true}, false},
		{"stopping", domain.Forward{State: domain.ForwardStopping, ClientClaimed: true, WorkerClaimed: true, ClientClean: true}, false},
		{"missing-client-proof", domain.Forward{State: domain.ForwardStopped, ClientClaimed: true, WorkerClaimed: true, WorkerClean: true}, false},
		{"missing-worker-proof", domain.Forward{State: domain.ForwardStopped, ClientClaimed: true, WorkerClaimed: true, ClientClean: true}, false},
		{"settled", domain.Forward{State: domain.ForwardStopped, ClientClaimed: true, WorkerClaimed: true, ClientClean: true, WorkerClean: true}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, root, ctx, in, original := restoreFixture(t)
			_, err := s.Mutate(ctx, domain.NewID(), "fixture-forward", nil, func(tx *Tx) (any, error) {
				return tx.Put(domain.ForwardKind, domain.NewID(), 0, "", "", test.forward)
			})
			if err != nil {
				t.Fatal(err)
			}
			in.ExpectedRevision, err = s.RestoreRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "backups", string(in.Backup.ID)+".sqlite")
			before, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = s.RestoreBackup(ctx, domain.NewID(), in)
			if test.allowed {
				if err != nil {
					t.Fatal("settled forward blocked restore", err)
				}
			} else {
				if domain.SafeError(err).Code != domain.RecoveryRequired || s.RestoreFrozen() {
					t.Fatal("unsettled forward did not preserve the live epoch", err)
				}
				if _, err := s.Get(ctx, domain.ProjectKind, original); err != nil {
					t.Fatal("live state changed", err)
				}
			}
			after, err := os.ReadFile(source)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("restore changed the source image", err)
			}
		})
	}
}

func TestBackupRestoreQuarantinesHistoricalForwards(t *testing.T) {
	s, root, ctx, in, _ := restoreFixture(t)
	active, pending, stopped := domain.NewID(), domain.NewID(), domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture-forwards", nil, func(tx *Tx) (any, error) {
		for _, entry := range []struct {
			id      domain.ID
			forward domain.Forward
		}{
			{active, domain.Forward{State: domain.ForwardActive, ClientClaimed: true, WorkerClaimed: true}},
			{pending, domain.Forward{State: domain.ForwardPending}},
			{stopped, domain.Forward{State: domain.ForwardStopped, ClientClaimed: true, WorkerClaimed: true, ClientClean: true, WorkerClean: true}},
		} {
			if _, err := tx.Put(domain.ForwardKind, entry.id, 0, "", "", entry.forward); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := s.InspectBackup(ctx, backup, in.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	in.Backup, in.SHA256 = inspection.Backup, inspection.SHA256
	// The current timeline has independent positive cleanup, while the image
	// retains its original unknown claims. Replacement cannot fabricate proof.
	_, err = s.Mutate(ctx, domain.NewID(), "fixture-settled-forwards", nil, func(tx *Tx) (any, error) {
		return nil, tx.VisitForwards("", func(row Record, forward domain.Forward) error {
			forward.ClientClean, forward.WorkerClean = true, true
			forward.Stop()
			_, err := tx.Put(domain.ForwardKind, row.ID, row.Revision, "", "", forward)
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	in.ExpectedRevision, err = s.RestoreRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RestoreBackup(ctx, domain.NewID(), in); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	row, err := reopened.Get(ctx, domain.ForwardKind, active)
	if err != nil {
		t.Fatal(err)
	}
	forward, err := Decode[domain.Forward](row)
	if err != nil || forward.State != domain.ForwardStopping || !forward.ClientClaimed || !forward.WorkerClaimed || forward.ClientClean || forward.WorkerClean {
		t.Fatal("historical native ownership was reopened or fabricated", forward, err)
	}
	for _, id := range []domain.ID{pending, stopped} {
		row, err := reopened.Get(ctx, domain.ForwardKind, id)
		if err != nil {
			t.Fatal(err)
		}
		forward, err := Decode[domain.Forward](row)
		if err != nil || !forward.Closed() {
			t.Fatal("historical closed or unclaimed forward was reopened", forward, err)
		}
	}
}

func TestConcurrentBackupRestoresPublishAtMostOnce(t *testing.T) {
	s, _, ctx, in, _ := restoreFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _, err := s.RestoreBackup(ctx, domain.NewID(), in); errs <- err }()
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("concurrent publication count", success)
	}
}

func TestBackupRestoreRejectsChangedRecoveryEvidence(t *testing.T) {
	for _, target := range []string{"safety.sqlite", "live", "sidecar"} {
		t.Run(target, func(t *testing.T) {
			s, root, ctx, in, _ := restoreFixture(t)
			request := domain.NewID()
			_, _, err := s.restoreBackup(ctx, request, in, func(boundary string) error {
				if boundary == "prepared" {
					return errors.New("fixture crash")
				}
				return nil
			})
			if err == nil {
				t.Fatal("fault ignored")
			}
			s.Close()
			path := filepath.Join(restoreDirectory(root, request), target)
			if target == "live" {
				path = filepath.Join(root, "state.sqlite")
			}
			if target == "sidecar" {
				path = filepath.Join(root, "state.sqlite-wal")
			}
			if err := os.WriteFile(path, []byte("changed retained evidence"), 0600); err != nil {
				t.Fatal(err)
			}
			if reopened, err := Open(ctx, root); err == nil {
				reopened.Close()
				t.Fatal("changed recovery evidence opened")
			}
			if _, err := os.Stat(filepath.Join(restoreRoot(root), "active.json")); err != nil {
				t.Fatal("recovery discarded barrier", err)
			}
		})
	}
}

func TestCompletedRestoreCannotBeBypassedByReplacingLiveDatabase(t *testing.T) {
	s, root, ctx, in, _ := restoreFixture(t)
	request := domain.NewID()
	if _, _, err := s.RestoreBackup(ctx, request, in); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	reopened.Close()
	raw, err := os.ReadFile(filepath.Join(root, "backups", string(in.Backup.ID)+".sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state.sqlite"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(ctx, root); err == nil {
		reopened.Close()
		t.Fatal("older image bypassed completed external safety boundary")
	}
}

func TestRestoreRollBackRequestCannotBeReusedForAnotherMutation(t *testing.T) {
	s, root, ctx, in, _ := restoreFixture(t)
	request := domain.NewID()
	_, _, err := s.restoreBackup(ctx, request, in, func(boundary string) error {
		if boundary == "prepared" {
			return errors.New("fixture fault")
		}
		return nil
	})
	if err == nil {
		t.Fatal("fault ignored")
	}
	s.Close()
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	called := false
	_, err = reopened.Mutate(ctx, request, "other", nil, func(*Tx) (any, error) { called = true; return nil, nil })
	assertCode(t, err, domain.Conflict)
	if called {
		t.Fatal("external request UUID was borrowed")
	}
}

func TestRestoreDisconnectsAccountsAndWorkersAndPausesSchedules(t *testing.T) {
	s, root, ctx, in, _ := restoreFixture(t)
	account, integration, schedule, client, worker, machine := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	clientToken := sha256.Sum256([]byte("current client fixture"))
	workerToken := sha256.Sum256([]byte("current worker fixture"))
	_, err := s.Mutate(ctx, domain.NewID(), "fixture-authority", nil, func(tx *Tx) (any, error) {
		for _, entry := range []struct {
			id   domain.ID
			kind domain.Kind
			body any
		}{
			{account, domain.AccountKind, domain.Account{Alias: "fixture", ProviderID: domain.NewID(), Type: domain.APIAccount, Health: domain.AccountReady, Connection: &domain.AccountConnection{ID: domain.NewID()}}},
			{integration, domain.IntegrationKind, domain.Integration{Connection: &domain.IntegrationConnection{GenerationID: domain.NewID()}}},
			{schedule, domain.ScheduleKind, domain.Schedule{ConfigurationRevision: 1, Definition: domain.ScheduleDefinition{Enabled: true}}},
			{machine, domain.MachineKind, domain.Machine{}},
			{client, domain.DeviceKind, domain.Device{Type: domain.ClientDevice, Name: "client"}},
			{worker, domain.DeviceKind, domain.Device{Type: domain.WorkerDevice, MachineID: machine, Name: "worker"}},
		} {
			if _, err := tx.Put(entry.kind, entry.id, 0, "", "", entry.body); err != nil {
				return nil, err
			}
		}
		if err := tx.PutCredential(client, clientToken[:]); err != nil {
			return nil, err
		}
		return nil, tx.PutCredential(worker, workerToken[:])
	})
	if err != nil {
		t.Fatal(err)
	}
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := s.InspectBackup(ctx, backup, in.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	in.Backup, in.SHA256 = inspection.Backup, inspection.SHA256
	in.ExpectedRevision, _ = s.RestoreRevision(ctx)
	secretPath := filepath.Join(root, "secrets", "fixture-sealed-payload")
	if err := os.WriteFile(secretPath, []byte("fixture protected bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RestoreBackup(ctx, domain.NewID(), in); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if actor, err := reopened.Authenticate(ctx, clientToken[:]); err != nil || actor.DeviceID != client {
		t.Fatal("current client authorization lost", actor, err)
	}
	if _, err := reopened.Authenticate(ctx, workerToken[:]); err == nil {
		t.Fatal("Worker silently reattached")
	}
	r, err := reopened.Get(ctx, domain.AccountKind, account)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := Decode[domain.Account](r)
	if a.Connection != nil || a.Health != domain.AccountDisconnected {
		t.Fatal(a)
	}
	r, err = reopened.Get(ctx, domain.IntegrationKind, integration)
	if err != nil {
		t.Fatal(err)
	}
	i, _ := Decode[domain.Integration](r)
	if i.Connection != nil || i.Pending != nil {
		t.Fatal(i)
	}
	r, err = reopened.Get(ctx, domain.ScheduleKind, schedule)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := Decode[domain.Schedule](r)
	if v.Definition.Enabled || v.NextRunAt != nil {
		t.Fatal(v)
	}
	if raw, err := os.ReadFile(secretPath); err != nil || string(raw) != "fixture protected bytes" {
		t.Fatal("restore touched protected storage", err)
	}
}

func TestBackupRestoreParentSyncFailurePreservesLive(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires an unprivileged POSIX directory permission fixture")
	}
	s, root, ctx, in, _ := restoreFixture(t)
	later := domain.NewID()
	create(t, s, later, "preserved on directory durability failure")
	var err error
	in.ExpectedRevision, err = s.RestoreRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	request := domain.NewID()
	t.Cleanup(func() {
		if err := os.Chmod(root, 0700); err != nil {
			t.Error(err)
		}
	})
	// Search/write still permit staging and rename. Denying directory reads
	// makes its fsync fail, exposing publication before parent durability.
	if err := os.Chmod(root, 0300); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Open(root); err == nil {
		f.Close()
		t.Fatal("permission fixture did not deny opening the directory")
	}
	_, _, err = s.RestoreBackup(ctx, request, in)
	if err == nil {
		t.Fatal("restore accepted a failed parent synchronization")
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if s.RestoreFrozen() {
		t.Fatal("directory durability failure crossed the live close/publication boundary")
	}
	if _, err := s.Get(ctx, domain.ProjectKind, later); err != nil {
		t.Fatal("directory durability failure lost current live state", err)
	}
	revision, err := s.RestoreRevision(ctx)
	if err != nil || revision != in.ExpectedRevision {
		t.Fatal("directory durability failure changed the live revision", revision, err)
	}
	for _, path := range []string{restoreDirectory(root, request), filepath.Join(restoreRoot(root), "active.json")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("directory durability failure accepted a restore attempt", err)
		}
	}
	observation, err := s.InspectBackup(ctx, in.Backup.ID, in.ServerID)
	if err != nil || observation.SHA256 != in.SHA256 {
		t.Fatal("directory durability failure changed the source image", err)
	}
}

func TestBackupRestorePreservesSessionPRActivityDeletion(t *testing.T) {
	s, root, ctx, in, _ := restoreFixture(t)
	f := newRemediationStoreFixture(t, s)
	attempt, err := f.reserve(t, domain.PRRemediationAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	attempt = f.bind(t, attempt)
	bound, err := Decode[domain.PRRemediationAttempt](attempt)
	if err != nil {
		t.Fatal(err)
	}
	// The reserved transition predates session binding and has no session_id.
	// Its deletion tombstone must survive restoration along with bound history.
	if rows := prActivityRows(t, s); len(rows) != 3 {
		t.Fatal("fixture omitted original observed/reserved/bound activity", len(rows))
	}
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := s.InspectBackup(ctx, backup, in.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	in.Backup, in.SHA256 = observed.Backup, observed.SHA256
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.delete-pr-activity-session", nil, func(tx *Tx) (any, error) {
		r, err := tx.Get(domain.SessionKind, bound.SessionID)
		if err != nil {
			return nil, err
		}
		return nil, tx.Delete(r.Kind, r.ID, r.Revision)
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows := prActivityRows(t, s); len(rows) != 1 {
		t.Fatal("deletion retained original session-owned transitions", len(rows))
	}
	in.ExpectedRevision, _ = s.RestoreRevision(ctx)
	if _, _, err := s.RestoreBackup(ctx, domain.NewID(), in); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if rows := prActivityRows(t, reopened); len(rows) != 1 {
		t.Fatal("restore resurrected deleted activity or erased shared PR evidence", len(rows))
	}
	for _, id := range []domain.ID{bound.SessionID, bound.InputID} {
		var retained int
		if err := reopened.db.QueryRow("SELECT count(*) FROM entities WHERE id=?", id).Scan(&retained); err != nil || retained != 0 {
			t.Fatal("restore resurrected original session-owned state", id, retained, err)
		}
	}
}
