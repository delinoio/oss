// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// controllerEvidence is private infrastructure proof, never native execution
// cleanup authority. Generation-named records remain immutable in history.
type controllerEvidence struct {
	Version         int                        `json:"version"`
	Generation      domain.ID                  `json:"generation"`
	ServerID        domain.ID                  `json:"server_id"`
	DeviceID        domain.ID                  `json:"device_id"`
	MachineID       domain.ID                  `json:"machine_id"`
	Scope           string                     `json:"scope"`
	DesktopClientID domain.ID                  `json:"desktop_client_id"`
	Original        process.ControllerIdentity `json:"original"`
}

type controllerProofReason string

const (
	controllerProofMissing     controllerProofReason = "missing"
	controllerProofDenied      controllerProofReason = "permission denied"
	controllerProofUnavailable controllerProofReason = "unavailable"
	controllerProofInvalid     controllerProofReason = "invalid"
	controllerProofUnknown     controllerProofReason = "unknown"
	controllerProofAlive       controllerProofReason = "still alive"
)

func controllerProofFailure(reason controllerProofReason) error {
	code := domain.RecoveryRequired
	if reason == controllerProofDenied {
		code = domain.PermissionDenied
	}
	slog.Warn("desktop_worker_controller_proof", "phase", "blocked", "reason", reason, "code", code)
	return domain.Fail(code, "The original local Worker controller proof is "+string(reason)+".", "Open Connection & diagnostics and inspect the original Worker recovery state. Do not delete state, repeat accepted input or infer native cleanup.")
}
func controllerProofReadFailure(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return controllerProofFailure(controllerProofMissing)
	}
	if errors.Is(err, os.ErrPermission) || domain.SafeError(err).Code == domain.PermissionDenied {
		return controllerProofFailure(controllerProofDenied)
	}
	return controllerProofFailure(controllerProofUnavailable)
}
func controllerScope(root string) (string, error) {
	if err := security.CheckPrivateDir(root); err != nil {
		return "", controllerProofReadFailure(err)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", controllerProofFailure(controllerProofInvalid)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", controllerProofFailure(controllerProofUnavailable)
	}
	return canonical, nil
}
func evidencePath(scope string, generation domain.ID) string {
	return filepath.Join(scope, "worker-controller-evidence", string(generation)+".json")
}
func validateControllerEvidence(root string, c Credential, lifecycle Lifecycle, value controllerEvidence) error {
	scope, err := controllerScope(root)
	if err != nil {
		return err
	}
	client, err := LoadCredential(filepath.Join(filepath.Dir(scope), "desktop-client"))
	if err != nil || client.Type != domain.ClientDevice || client.ServerID != c.ServerID {
		return controllerProofFailure(controllerProofInvalid)
	}
	if value.Version != 1 || value.Generation.Validate() != nil || value.Generation != lifecycle.Generation || value.ServerID != c.ServerID || value.DeviceID != c.DeviceID || value.MachineID != c.MachineID || value.Scope != scope || value.DesktopClientID.Validate() != nil || value.DesktopClientID != client.DeviceID || !value.Original.Valid() {
		return controllerProofFailure(controllerProofInvalid)
	}
	return nil
}
func readControllerEvidence(root string, c Credential, lifecycle Lifecycle) (controllerEvidence, error) {
	var value controllerEvidence
	scope, err := controllerScope(root)
	if err != nil {
		return value, err
	}
	path := evidencePath(scope, lifecycle.Generation)
	if err := security.CheckPrivateDir(filepath.Dir(path)); err != nil {
		return value, controllerProofReadFailure(err)
	}
	raw, err := security.ReadPrivate(path, 4096)
	if err != nil {
		return value, controllerProofReadFailure(err)
	}
	if domain.Decode(raw, &value) != nil {
		return value, controllerProofFailure(controllerProofInvalid)
	}
	return value, validateControllerEvidence(root, c, lifecycle, value)
}
func publishControllerEvidence(root string, c Credential, lifecycle Lifecycle, clientID domain.ID) error {
	if lifecycle.Phase != RuntimeReserved || lifecycle.Desired != WorkerRunning || clientID.Validate() != nil {
		return controllerProofFailure(controllerProofInvalid)
	}
	scope, err := controllerScope(root)
	if err != nil {
		return err
	}
	original, err := process.CaptureControllerIdentity()
	if err != nil {
		return controllerProofFailure(controllerProofUnavailable)
	}
	value := controllerEvidence{Version: 1, Generation: lifecycle.Generation, ServerID: c.ServerID, DeviceID: c.DeviceID, MachineID: c.MachineID, Scope: scope, DesktopClientID: clientID, Original: original}
	if err := validateControllerEvidence(root, c, lifecycle, value); err != nil {
		return err
	}
	path := evidencePath(scope, lifecycle.Generation)
	if err := security.PrivateDir(filepath.Dir(path)); err != nil {
		return controllerProofFailure(controllerProofUnavailable)
	}
	// Synchronize the new evidence directory's parent as well as the record's
	// parent. File-only durability must not lose the directory after a reboot.
	if err := security.SyncParent(filepath.Dir(path)); err != nil {
		return controllerProofFailure(controllerProofUnavailable)
	}
	if _, err := os.Lstat(path); err == nil {
		retained, err := readControllerEvidence(root, c, lifecycle)
		if err != nil || retained != value {
			return controllerProofFailure(controllerProofInvalid)
		}
		// A previous final directory synchronization may have failed after the
		// atomic rename. Recheck durability without changing original bytes.
		if err := security.SyncParent(path); err != nil {
			return controllerProofFailure(controllerProofUnavailable)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return controllerProofFailure(controllerProofUnavailable)
	}
	// The lifecycle/controller locks own this publication. Atomic write includes
	// file and parent synchronization before the start barrier can be released.
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 4096 {
		return controllerProofFailure(controllerProofInvalid)
	}
	if err := security.WriteAtomic(path, raw); err != nil {
		return controllerProofFailure(controllerProofUnavailable)
	}
	return nil
}
func observeDesktopController(root string, c Credential, lifecycle Lifecycle) (process.ControllerObservation, error) {
	value, err := readControllerEvidence(root, c, lifecycle)
	if err != nil {
		return process.ControllerUnknown, err
	}
	state := process.ObserveControllerIdentity(value.Original)
	if state == process.ControllerUnknown {
		return state, controllerProofFailure(controllerProofUnknown)
	}
	return state, nil
}
