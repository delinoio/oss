package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type DesiredState string
type RuntimePhase string
type RuntimeState string

const (
	WorkerRunning   DesiredState = "running"
	WorkerStopped   DesiredState = "stopped"
	RuntimeReserved RuntimePhase = "reserved"
	RuntimeStarting RuntimePhase = "starting"
	RuntimeReady    RuntimePhase = "ready"
	RuntimeExited   RuntimePhase = "exited"
	StateIdle       RuntimeState = "not-started"
	StateStarting   RuntimeState = "starting"
	StateRunning    RuntimeState = "running"
	StateStopping   RuntimeState = "stopping"
	StateExited     RuntimeState = "exited"
	StateUncertain  RuntimeState = "uncertain"
)

// Lifecycle is infrastructure evidence only. An exited Worker controller does
// not prove cleanup of every native session. Their original journals and server
// recovery gates remain authoritative, including after explicit replacement.
type Lifecycle struct {
	Version       int          `json:"version"`
	Generation    domain.ID    `json:"generation"`
	ServerID      domain.ID    `json:"server_id"`
	DeviceID      domain.ID    `json:"device_id"`
	MachineID     domain.ID    `json:"machine_id"`
	Endpoint      string       `json:"endpoint"`
	WorkerVersion string       `json:"worker_version"`
	Desired       DesiredState `json:"desired"`
	Phase         RuntimePhase `json:"phase"`
}
type RuntimeStatus struct {
	Lifecycle        Lifecycle    `json:"lifecycle"`
	State            RuntimeState `json:"state"`
	ControllerActive bool         `json:"controller_active"`
}

func lifecycleConflict() error {
	return domain.Fail(domain.RecoveryRequired, "The original Worker lifecycle cannot be confirmed.", "Inspect the private Worker log and original session recovery records; do not infer native cleanup or repeat accepted work.")
}
func workerCredential(root string) (Credential, error) {
	c, err := LoadCredential(root)
	if err == nil && c.Type != domain.WorkerDevice {
		err = domain.Fail(domain.PermissionDenied, "This scope is not a Worker.", "Select the original private Worker directory.")
	}
	return c, err
}
func readLifecycle(root string, c Credential) (Lifecycle, error) {
	var value Lifecycle
	raw, err := security.ReadPrivate(filepath.Join(root, "worker-lifecycle.json"), 4096)
	if errors.Is(err, os.ErrNotExist) {
		return value, nil
	}
	if err != nil {
		return value, err
	}
	if err := domain.Decode(raw, &value); err != nil {
		return value, err
	}
	if value.Version != 1 || value.Generation.Validate() != nil || value.ServerID != c.ServerID || value.DeviceID != c.DeviceID || value.MachineID != c.MachineID || value.Endpoint != c.Endpoint || value.WorkerVersion == "" || (value.Desired != WorkerRunning && value.Desired != WorkerStopped) {
		return value, lifecycleConflict()
	}
	switch value.Phase {
	case RuntimeReserved, RuntimeStarting, RuntimeReady, RuntimeExited:
	default:
		return value, lifecycleConflict()
	}
	return value, nil
}
func lifecycleLock(root string) (*security.Lock, error) {
	// A lifecycle write contains no network or process work. Bounded contention
	// waits avoid treating a simultaneous status/stop as corrupted ownership.
	deadline := time.Now().Add(2 * time.Second)
	for {
		lock, err := security.TryLock(filepath.Join(root, "worker-lifecycle.lock"))
		if err == nil || domain.SafeError(err).Code != domain.Conflict || time.Now().After(deadline) {
			return lock, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func saveLifecycle(root string, value Lifecycle) error {
	return writeJSON(filepath.Join(root, "worker-lifecycle.json"), value)
}
func newLifecycle(root string, c Credential, previous Lifecycle, phase RuntimePhase) (Lifecycle, error) {
	if previous.Version != 0 {
		// Preserve superseded infrastructure evidence, including a crashed process;
		// replacement never rewrites or clears its native execution journals.
		history := filepath.Join(root, "worker-lifecycle-history")
		if err := security.PrivateDir(history); err != nil {
			return Lifecycle{}, err
		}
		if err := writeJSON(filepath.Join(history, string(previous.Generation)+".json"), previous); err != nil {
			return Lifecycle{}, err
		}
	}
	value := Lifecycle{Version: 1, Generation: domain.NewID(), ServerID: c.ServerID, DeviceID: c.DeviceID, MachineID: c.MachineID, Endpoint: c.Endpoint, WorkerVersion: rpc.Version, Desired: WorkerRunning, Phase: phase}
	return value, saveLifecycle(root, value)
}

// PrepareStart reserves exactly one generation while no Worker owns the scope.
// A previously reserved child may still arrive, so retries wait on that original
// generation instead of launching another child. Explicit stop can cancel it.
func PrepareStart(root string) (RuntimeStatus, bool, error) {
	return prepareStart(root, true, "")
}

// PrepareDesktopStart distinguishes intentional fresh-process Start from
// supervision. Only native observation of the exact original child's exit may
// replace incomplete controller evidence; native session journals are retained.
func PrepareDesktopStart(root string, reopen bool, exited domain.ID) (RuntimeStatus, bool, error) {
	if exited != "" && exited.Validate() != nil {
		return RuntimeStatus{}, false, lifecycleConflict()
	}
	return prepareStart(root, reopen, exited)
}

func prepareStart(root string, reopen bool, exited domain.ID) (RuntimeStatus, bool, error) {
	c, err := workerCredential(root)
	if err != nil {
		return RuntimeStatus{}, false, err
	}
	lock, err := lifecycleLock(root)
	if err != nil {
		return RuntimeStatus{}, false, err
	}
	defer lock.Close()
	current, err := readLifecycle(root, c)
	if err != nil {
		return RuntimeStatus{}, false, err
	}
	process, err := security.TryLock(filepath.Join(root, "worker.lock"))
	if err != nil {
		if domain.SafeError(err).Code != domain.Conflict {
			return RuntimeStatus{}, false, err
		}
		status := runtimeStatus(current, true)
		if current.WorkerVersion != rpc.Version || current.Desired != WorkerRunning || status.State == StateUncertain {
			return status, false, lifecycleConflict()
		}
		return status, false, nil
	}
	defer process.Close()
	if !reopen {
		status := runtimeStatus(current, false)
		if current.Version == 0 || current.Desired == WorkerStopped {
			return status, false, nil
		}
		if current.Phase != RuntimeExited && (exited == "" || exited != current.Generation) {
			if current.Phase == RuntimeReserved {
				return status, false, nil
			}
			return status, false, lifecycleConflict()
		}
	}
	if current.Phase == RuntimeReserved && current.Desired == WorkerRunning {
		if exited == current.Generation {
			value, err := newLifecycle(root, c, current, RuntimeReserved)
			return RuntimeStatus{Lifecycle: value, State: StateStarting}, err == nil, err
		}
		if current.WorkerVersion != rpc.Version {
			return RuntimeStatus{}, false, lifecycleConflict()
		}
		return RuntimeStatus{Lifecycle: current, State: StateStarting}, false, nil
	}
	value, err := newLifecycle(root, c, current, RuntimeReserved)
	return RuntimeStatus{Lifecycle: value, State: StateStarting}, err == nil, err
}

func Status(root string) (RuntimeStatus, error) {
	c, err := workerCredential(root)
	if err != nil {
		return RuntimeStatus{}, err
	}
	lock, err := lifecycleLock(root)
	if err != nil {
		return RuntimeStatus{}, err
	}
	defer lock.Close()
	process, err := security.TryLock(filepath.Join(root, "worker.lock"))
	active := err != nil
	if err != nil && domain.SafeError(err).Code != domain.Conflict {
		return RuntimeStatus{}, err
	}
	if process != nil {
		defer process.Close()
	}
	value, err := readLifecycle(root, c)
	if err != nil {
		return RuntimeStatus{}, err
	}
	return runtimeStatus(value, active), nil
}
func runtimeStatus(value Lifecycle, active bool) RuntimeStatus {
	state := StateUncertain
	switch {
	case value.Version == 0 && !active:
		state = StateIdle
	case value.Version == 0:
		state = StateUncertain
	case value.Desired == WorkerStopped && active:
		state = StateStopping
	case value.Phase == RuntimeExited && !active:
		state = StateExited
	case value.Phase == RuntimeExited && active:
		// The joined controller publishes exit before releasing its process lock.
		// Keep this brief publication barrier pending instead of inventing a loss.
		state = StateStopping
	case value.Phase == RuntimeReserved:
		state = StateStarting
	case value.Phase == RuntimeStarting && active:
		state = StateStarting
	case value.Phase == RuntimeReady && active:
		state = StateRunning
	}
	return RuntimeStatus{Lifecycle: value, State: state, ControllerActive: active}
}

// RequestStop is generation-bound and durable even while the server is offline.
// It neither signals a PID nor claims that a crashed Worker cleaned up sessions.
func RequestStop(root string, generation domain.ID) error {
	if err := generation.Validate(); err != nil {
		return err
	}
	c, err := workerCredential(root)
	if err != nil {
		return err
	}
	lock, err := lifecycleLock(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	value, err := readLifecycle(root, c)
	if err != nil {
		return err
	}
	if value.Generation != generation {
		return domain.Fail(domain.Conflict, "The Worker lifecycle generation changed.", "Refresh Worker status before stopping the currently selected generation.")
	}
	value.Desired = WorkerStopped
	// Reserved means the start barrier has not opened. Cancellation under this
	// same lock permanently rejects a delayed child without any native launch.
	if value.Phase == RuntimeReserved {
		value.Phase = RuntimeExited
	}
	return saveLifecycle(root, value)
}
func enterLifecycle(root string, c Credential, generation domain.ID) (Lifecycle, error) {
	lock, err := lifecycleLock(root)
	if err != nil {
		return Lifecycle{}, err
	}
	defer lock.Close()
	return enterLifecycleLocked(root, c, generation)
}
func enterLifecycleLocked(root string, c Credential, generation domain.ID) (Lifecycle, error) {
	current, err := readLifecycle(root, c)
	if err != nil {
		return Lifecycle{}, err
	}
	if generation == "" {
		if current.Phase == RuntimeReserved && current.Desired == WorkerRunning {
			return Lifecycle{}, lifecycleConflict()
		}
		return newLifecycle(root, c, current, RuntimeStarting)
	}
	if current.Generation != generation || current.Desired != WorkerRunning || current.Phase != RuntimeReserved || current.WorkerVersion != rpc.Version {
		return Lifecycle{}, lifecycleConflict()
	}
	current.Phase = RuntimeStarting
	return current, saveLifecycle(root, current)
}
func setPhase(root string, c Credential, generation domain.ID, phase RuntimePhase) error {
	lock, err := lifecycleLock(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	value, err := readLifecycle(root, c)
	if err != nil {
		return err
	}
	if value.Generation != generation || (phase == RuntimeReady && value.Desired != WorkerRunning) {
		return lifecycleConflict()
	}
	value.Phase = phase
	return saveLifecycle(root, value)
}
func observeStop(ctx context.Context, root string, c Credential, generation domain.ID, cancel context.CancelCauseFunc) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			value, err := readLifecycle(root, c)
			if err != nil || value.Generation != generation {
				cancel(lifecycleConflict())
				return
			}
			if value.Desired == WorkerStopped {
				cancel(context.Canceled)
				return
			}
		}
	}
}
