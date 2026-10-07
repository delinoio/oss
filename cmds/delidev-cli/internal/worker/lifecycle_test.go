package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func lifecycleFixture(t *testing.T) (string, Credential) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "worker")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	token, err := RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	c := Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: "http://127.0.0.1:1", ServerID: domain.NewID(), DeviceID: domain.NewID(), MachineID: domain.NewID(), PairingID: domain.NewID(), Token: token}
	if err := writeJSON(credentialPath(root), c); err != nil {
		t.Fatal(err)
	}
	return root, c
}

func TestDesktopRecoveryPreservesStopAndReservedAdmission(t *testing.T) {
	root, _ := lifecycleFixture(t)
	if status, launch, err := PrepareDesktopStart(root, false, ""); err != nil || launch || status.State != StateIdle {
		t.Fatal(status, launch, err)
	}
	first, launch, err := PrepareDesktopStart(root, true, "")
	if err != nil || !launch {
		t.Fatal(first, launch, err)
	}
	if next, launch, err := PrepareDesktopStart(root, false, ""); err != nil || launch || next.Lifecycle.Generation != first.Lifecycle.Generation {
		t.Fatal("unconfirmed admission replay", next, err)
	}
	if err := RequestStop(root, first.Lifecycle.Generation); err != nil {
		t.Fatal(err)
	}
	if stopped, launch, err := PrepareDesktopStart(root, false, first.Lifecycle.Generation); err != nil || launch || stopped.Lifecycle.Desired != WorkerStopped {
		t.Fatal("supervision reopened Stop", stopped, err)
	}
	second, launch, err := PrepareDesktopStart(root, true, "")
	if err != nil || !launch || second.Lifecycle.Generation == first.Lifecycle.Generation {
		t.Fatal("fresh launch", second, err)
	}
}

func TestDesktopRecoveryRequiresExactOriginalExitAndPreservesHistory(t *testing.T) {
	root, credential := lifecycleFixture(t)
	first, _, err := PrepareStart(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enterLifecycle(root, credential, first.Lifecycle.Generation); err != nil {
		t.Fatal(err)
	}
	for _, exited := range []domain.ID{"", domain.NewID()} {
		if _, launch, err := PrepareDesktopStart(root, false, exited); err == nil || launch {
			t.Fatal("unknown exit authorized recovery")
		}
	}
	second, launch, err := PrepareDesktopStart(root, false, first.Lifecycle.Generation)
	if err != nil || !launch || second.Lifecycle.Generation == first.Lifecycle.Generation {
		t.Fatal(second, err)
	}
	if _, err := os.Stat(filepath.Join(root, "worker-lifecycle-history", string(first.Lifecycle.Generation)+".json")); err != nil {
		t.Fatal("original uncertainty erased", err)
	}
	if _, launch, err := PrepareDesktopStart(root, false, first.Lifecycle.Generation); err != nil || launch {
		t.Fatal("old native exit replayed", err)
	}
}
func TestLifecycleCancelReservedChildAndKeepOriginalGeneration(t *testing.T) {
	root, _ := lifecycleFixture(t)
	first, launch, err := PrepareStart(root)
	if err != nil || !launch {
		t.Fatal(first, launch, err)
	}
	retry, launch, err := PrepareStart(root)
	if err != nil || launch || retry.Lifecycle != first.Lifecycle {
		t.Fatal("duplicate reservation", err)
	}
	if err := RequestStop(root, first.Lifecycle.Generation); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), Config{Root: root, StartupID: first.Lifecycle.Generation}); err == nil {
		t.Fatal("delayed canceled child started")
	}
	stopped, err := Status(root)
	if err != nil || stopped.State != StateExited || stopped.ControllerActive {
		t.Fatal(stopped, err)
	}
	second, launch, err := PrepareStart(root)
	if err != nil || !launch || second.Lifecycle.Generation == first.Lifecycle.Generation {
		t.Fatal(second, err)
	}
	if err := RequestStop(root, first.Lifecycle.Generation); err == nil {
		t.Fatal("old stop canceled replacement")
	}
	if err := Run(context.Background(), Config{Root: root, StartupID: first.Lifecycle.Generation}); err == nil {
		t.Fatal("superseded child started")
	}
	current, err := Status(root)
	if err != nil || current.Lifecycle.Desired != WorkerRunning || current.Lifecycle.Generation != second.Lifecycle.Generation {
		t.Fatal(current, err)
	}
	if _, err := os.Stat(filepath.Join(root, "worker-lifecycle-history", string(first.Lifecycle.Generation)+".json")); err != nil {
		t.Fatal("missing superseded evidence", err)
	}
}
func TestLifecycleOfflineStopJoinsControllerAndPreservesJobs(t *testing.T) {
	root, _ := lifecycleFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Config{Root: root}) }()
	var status RuntimeStatus
	for {
		value, err := Status(root)
		if err != nil {
			t.Fatal(err)
		}
		status = value
		if value.ControllerActive && value.Lifecycle.Phase == RuntimeStarting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("no startup")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, _, err := PrepareStart(root); err != nil {
		t.Fatal("running startup not reused", err)
	}
	if err := RequestStop(root, status.Lifecycle.Generation); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("stop did not join controller")
	}
	status, err := Status(root)
	if err != nil || status.State != StateExited || status.ControllerActive || status.Lifecycle.Desired != WorkerStopped {
		t.Fatal(status, err)
	}
	if _, err := os.Stat(filepath.Join(root, "jobs")); err != nil {
		t.Fatal("retained jobs removed", err)
	}
}
func TestLifecycleAbsentLockIsNotExitEvidence(t *testing.T) {
	root, c := lifecycleFixture(t)
	first, _, err := PrepareStart(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enterLifecycle(root, c, first.Lifecycle.Generation); err != nil {
		t.Fatal(err)
	}
	status, err := Status(root)
	if err != nil || status.State != StateUncertain {
		t.Fatal("absence became cleanup", status, err)
	}
	if err := RequestStop(root, first.Lifecycle.Generation); err != nil {
		t.Fatal(err)
	}
	status, err = Status(root)
	if err != nil || status.State != StateUncertain {
		t.Fatal("stop invented exit", status, err)
	}
	// Explicit replacement preserves the crashed infrastructure record; native
	// assignment/cleanup journals retain their separate recovery requirements.
	second, launch, err := PrepareStart(root)
	if err != nil || !launch || second.Lifecycle.Generation == first.Lifecycle.Generation {
		t.Fatal(second, err)
	}
}
func TestLifecycleRejectsCorruptForeignAndLinkedEvidence(t *testing.T) {
	for _, mode := range []string{"corrupt", "foreign", "linked", "legacy-active"} {
		t.Run(mode, func(t *testing.T) {
			root, _ := lifecycleFixture(t)
			path := filepath.Join(root, "worker-lifecycle.json")
			if mode == "legacy-active" {
				lock, err := security.TryLock(filepath.Join(root, "worker.lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			} else {
				first, _, err := PrepareStart(root)
				if err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "corrupt":
					if err := security.WriteAtomic(path, []byte(`{}`)); err != nil {
						t.Fatal(err)
					}
				case "foreign":
					first.Lifecycle.DeviceID = domain.NewID()
					if err := saveLifecycle(root, first.Lifecycle); err != nil {
						t.Fatal(err)
					}
				case "linked":
					if err := os.Rename(path, path+".original"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(path+".original", path); err != nil {
						t.Skip("symlinks unavailable")
					}
				}
			}
			if _, launch, err := PrepareStart(root); err == nil || launch {
				t.Fatal("invalid evidence allowed startup", err)
			}
		})
	}
}

func TestLifecycleExitPublicationWaitsForOriginalProcessLock(t *testing.T) {
	root, credential := lifecycleFixture(t)
	status, _, err := PrepareStart(root)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := security.TryLock(filepath.Join(root, "worker.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := setPhase(root, credential, status.Lifecycle.Generation, RuntimeExited); err != nil {
		t.Fatal(err)
	}
	observed, err := Status(root)
	if err != nil || observed.State != StateStopping || !observed.ControllerActive {
		t.Fatal("exit publication was reported as lost ownership", observed, err)
	}
}
