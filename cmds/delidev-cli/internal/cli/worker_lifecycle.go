package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func startDetachedWorker(ctx context.Context, o options, root string) (any, error) {
	// Select the signed installed controller before it reserves a generation.
	// A stale bundled CLI cannot reserve its own version and then spawn another
	// version. The selected original binary performs the ordinary local admission
	// and independently revalidates its private update history.
	executable, err := os.Executable()
	if err != nil {
		return nil, domain.SafeError(err)
	}
	selected, err := worker.InstalledExecutable(root, executable)
	if err != nil {
		return nil, err
	}
	if selected == executable {
		return startDetachedWorkerWithAdmission(ctx, o, root, nil)
	}
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(bounded, selected, "--json", "--data-dir", o.dataDir, "worker", "start", "--worker-dir", root, "--detach")
	cmd.Env = updateEnvironment()
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	err = cmd.Run()
	status, statusError := worker.Status(root)
	if statusError != nil {
		return nil, statusError
	}
	if err != nil || status.State != worker.StateRunning {
		return status, workerUpdateFailure()
	}
	return status, nil
}
func startDetachedWorkerWithAdmission(ctx context.Context, o options, root string, admitted func(worker.RuntimeStatus) error) (any, error) {
	if _, err := worker.LoadCredential(root); err != nil {
		return nil, err
	}
	lock, err := security.TryLock(filepath.Join(root, "worker-startup.lock"))
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	status, launch, err := worker.PrepareStart(root)
	if err != nil {
		return status, err
	}
	if admitted != nil {
		if err := admitted(status); err != nil {
			return status, err
		}
	}
	generation := status.Lifecycle.Generation
	var exited <-chan error
	if launch {
		executable, err := os.Executable()
		if err != nil {
			return status, domain.SafeError(err)
		}
		path := filepath.Join(root, "worker.log")
		if _, err := os.Lstat(path); err == nil {
			if err := security.RegularPrivate(path); err != nil {
				return status, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return status, domain.SafeError(err)
		}
		log, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return status, domain.SafeError(err)
		}
		defer log.Close()
		cmd := exec.Command(executable, "--data-dir", o.dataDir, "worker", "start", "--worker-dir", root, "--startup-id", string(generation))
		cmd.Stdout, cmd.Stderr = log, log
		detach(cmd)
		if err := cmd.Start(); err != nil {
			// No child was created, but retain the reservation so a retry cannot
			// pretend a prior launch was accepted. Explicit stop cancels it safely.
			return status, domain.Fail(domain.Unavailable, "The Worker process could not start.", "Inspect the private Worker log and cancel its reserved generation before explicitly starting again.")
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		exited = done
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := worker.Status(root)
		if err != nil {
			return status, err
		}
		status = current
		if status.Lifecycle.Generation != generation {
			return status, domain.Fail(domain.Conflict, "The Worker startup generation changed.", "Inspect current Worker status before taking another lifecycle action.")
		}
		if status.State == worker.StateRunning {
			return status, nil
		}
		if status.State == worker.StateExited || status.State == worker.StateUncertain || status.State == worker.StateStopping {
			return status, domain.Fail(domain.RecoveryRequired, "The Worker did not confirm startup.", "Inspect its private log, device registration and retained session recovery before explicitly starting again.")
		}
		select {
		case <-ctx.Done():
			return status, domain.Fail(domain.RecoveryRequired, "Worker startup is still unconfirmed.", "Read Worker status before retrying. The original process may still connect; do not register a replacement.")
		case <-exited:
			return status, domain.Fail(domain.Unavailable, "The Worker exited before readiness.", "Inspect its private structured log and device registration; existing session recovery remains independent.")
		case <-ticker.C:
		}
	}
}
func stopLocalWorker(ctx context.Context, root string, generation domain.ID) (any, error) {
	if err := worker.RequestStop(root, generation); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var status worker.RuntimeStatus
	for {
		current, err := worker.Status(root)
		if err != nil {
			return status, err
		}
		status = current
		if status.Lifecycle.Generation != generation {
			return status, domain.Fail(domain.Conflict, "The original Worker generation was superseded.", "Refresh status; this stop cannot cancel a replacement Worker.")
		}
		if status.State == worker.StateExited {
			return status, nil
		}
		if status.State == worker.StateUncertain {
			return status, domain.Fail(domain.RecoveryRequired, "Stop intent is saved, but Worker exit is unconfirmed.", "Inspect original Worker and session recovery records. Missing process ownership does not prove native cleanup.")
		}
		select {
		case <-ctx.Done():
			return status, domain.Fail(domain.RecoveryRequired, "Stop intent is saved; the Worker is still exiting.", "Inspect status or retry this exact generation. Native session cleanup remains independently tracked.")
		case <-ticker.C:
		}
	}
}
