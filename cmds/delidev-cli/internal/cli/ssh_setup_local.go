// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/sshsetup"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

type sshLocalPhase string

const (
	sshPairing  sshLocalPhase = "pairing"
	sshStarting sshLocalPhase = "starting"
	sshReady    sshLocalPhase = "ready"
)

type sshLocalIntent struct {
	Version     uint32                `json:"version"`
	Operation   domain.ID             `json:"operation"`
	Server      domain.ID             `json:"server"`
	InputSHA256 string                `json:"input_sha256"`
	Phase       sshLocalPhase         `json:"phase"`
	Device      domain.ID             `json:"device,omitempty"`
	Machine     domain.ID             `json:"machine,omitempty"`
	Generation  domain.ID             `json:"generation,omitempty"`
	Reused      bool                  `json:"reused"`
	Result      *sshsetup.SetupResult `json:"result,omitempty"`
}

func sshLocalFailure(boundary ...string) error {
	error := domain.Fail(domain.RecoveryRequired, "The original SSH setup remains unconfirmed.", "Inspect this exact operation and original Worker scope. Never replace registration, credentials or workspaces to retry setup.")
	if len(boundary) == 1 {
		error.Cause = boundary[0]
	}
	return error
}
func localSSHSetup(ctx context.Context, o options, args []string, input io.Reader) (any, error) {
	fs := flags("worker ssh-setup")
	root := fs.String("worker-dir", "", "original private Worker root")
	id := fs.String("operation-id", "", "original setup operation")
	inspect := fs.Bool("inspect-only", false, "inspect the original setup without native side effects")
	if parse(fs, args) != nil || o.server != "" || o.tokenStdin || domain.ID(*id).Validate() != nil || *root == "" || filepath.Clean(o.dataDir) != filepath.Clean(*root) {
		return nil, usage()
	}
	raw, e := io.ReadAll(io.LimitReader(input, (64<<10)+1))
	if e != nil || len(raw) > 64<<10 {
		return nil, usage()
	}
	defer clear(raw)
	var document sshsetup.SetupDocument
	if domain.DecodeWithLimit(raw, &document, 64<<10) != nil || document.Validate() != nil || document.OperationID != domain.ID(*id) || document.ReleaseVersion != rpc.Version || document.SourceRevision != rpc.SourceRevision {
		return nil, sshLocalFailure("input-ownership")
	}
	target, e := updates.SelectTarget(runtime.GOOS, runtime.GOARCH)
	if e != nil || target != document.Artifact.Target {
		return nil, sshLocalFailure()
	}
	executable, e := os.Executable()
	if e != nil || updates.VerifyFile(executable, document.Artifact) != nil {
		return nil, sshLocalFailure("binary-identity")
	}
	if *inspect {
		if security.CheckPrivateDir(*root) != nil {
			return nil, sshLocalFailure()
		}
	} else if security.PrivateDir(*root) != nil {
		return nil, sshLocalFailure("private-root")
	}
	lock, e := security.TryLock(filepath.Join(*root, "ssh-setup-control.lock"))
	if e != nil {
		return nil, e
	}
	defer lock.Close()
	directory := filepath.Join(*root, "ssh-setup")
	if *inspect {
		if security.CheckPrivateDir(directory) != nil {
			return nil, sshLocalFailure()
		}
	} else if security.PrivateDir(directory) != nil {
		return nil, sshLocalFailure("private-journal")
	}
	path := filepath.Join(directory, string(document.OperationID)+".json")
	normalized, _ := json.Marshal(document)
	sum := sha256.Sum256(normalized)
	clear(normalized)
	digest := hex.EncodeToString(sum[:])
	intent := sshLocalIntent{Version: 1, Operation: document.OperationID, Server: document.ServerID, InputSHA256: digest, Phase: sshPairing}
	saved, e := security.ReadPrivate(path, 16<<10)
	if e == nil {
		if domain.Decode(saved, &intent) != nil || intent.Version != 1 || intent.Operation != document.OperationID || intent.Server != document.ServerID || intent.InputSHA256 != digest {
			return nil, sshLocalFailure()
		}
		// A replay observes original readiness only. Missing progress cannot authorize
		// another pair, lifecycle admission or process spawn.
		credential, e := worker.LoadCredential(*root)
		if e != nil || domain.OwnershipBlocks(domain.OwnershipActor, "", credential.Type != domain.WorkerDevice) ||
			domain.OwnershipBlocks(domain.OwnershipInstance,
				"",

				credential.
					ServerID !=
					document.ServerID,
			) ||
			credential.Endpoint != document.Grant.Endpoint || intent.Device != "" && intent.Device != credential.DeviceID || intent.Machine != "" && intent.Machine != credential.MachineID || intent.Device == "" && credential.PairingID != document.Grant.PairingID {
			return nil, sshLocalFailure()
		}
		status, e := worker.Status(*root)
		if e != nil || status.State != worker.StateRunning || intent.Generation == "" || status.Lifecycle.Generation != intent.Generation ||
			domain.OwnershipBlocks(domain.OwnershipInstance,
				"",

				status.Lifecycle.
					ServerID !=
					document.
						ServerID) ||
			domain.OwnershipBlocks(domain.OwnershipDevice,
				"",
				status.
					Lifecycle.
					DeviceID !=
					credential.
						DeviceID) ||
			domain.OwnershipBlocks(domain.OwnershipMachine,
				"",

				status.Lifecycle.
					MachineID !=
					credential.
						MachineID) ||
			(!intent.Reused && status.Lifecycle.WorkerVersion != rpc.Version) {
			return nil, sshLocalFailure()
		}
		result := sshsetup.SetupResult{Version: 1, OperationID: document.OperationID, ServerID: document.ServerID, DeviceID: credential.DeviceID, MachineID: credential.MachineID, Generation: status.Lifecycle.Generation, WorkerVersion: status.Lifecycle.WorkerVersion, Target: target, Running: true, Reused: intent.Reused}
		if intent.Result != nil && *intent.Result != result {
			return nil, sshLocalFailure()
		}
		intent.Result = &result
		intent.Phase = sshReady
		if e = writeSSHLocal(path, intent); e != nil {
			return nil, e
		}
		return result, nil
	}
	if !errors.Is(e, os.ErrNotExist) || *inspect {
		return nil, sshLocalFailure()
	}
	credential, e := worker.LoadCredential(*root)
	if e == nil {
		if domain.OwnershipBlocks(domain.OwnershipInstance,
			"",

			credential.
				ServerID !=
				document.ServerID,
		) ||
			credential.Endpoint != document.Grant.Endpoint || domain.OwnershipBlocks(domain.OwnershipActor, "", credential.Type != domain.WorkerDevice) {
			return nil, sshLocalFailure()
		}
		intent.Device = credential.DeviceID
		intent.Machine = credential.MachineID
		intent.Reused = true
		if status, e := worker.Status(*root); e == nil && status.State == worker.StateRunning {
			intent.Generation = status.Lifecycle.Generation
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, sshLocalFailure("existing-credential")
	}
	if e = writeSSHLocal(path, intent); e != nil {
		return nil, e
	}
	if !intent.Reused {
		credential, e = worker.Pair(ctx, *root, document.Grant, domain.WorkerDevice, document.Name)
		if e != nil {
			return nil, sshLocalFailure("pairing")
		}
		intent.Device = credential.DeviceID
		intent.Machine = credential.MachineID
	}
	intent.Phase = sshStarting
	if e = writeSSHLocal(path, intent); e != nil {
		return nil, e
	}
	if intent.Reused {
		status, e := worker.Status(*root)
		if e == nil && status.State == worker.StateRunning {
			result := sshsetup.SetupResult{Version: 1, OperationID: document.OperationID, ServerID: document.ServerID, DeviceID: credential.DeviceID, MachineID: credential.MachineID, Generation: status.Lifecycle.Generation, WorkerVersion: status.Lifecycle.WorkerVersion, Target: target, Running: true, Reused: true}
			if result.Validate(document) != nil {
				return nil, sshLocalFailure()
			}
			intent.Generation = status.Lifecycle.Generation
			intent.Phase = sshReady
			intent.Result = &result
			if e := writeSSHLocal(path, intent); e != nil {
				return nil, e
			}
			return result, nil
		}
	}
	value, e := startDetachedWorkerWithAdmission(ctx, o, *root, func(status worker.RuntimeStatus) error {
		intent.Generation = status.Lifecycle.Generation
		return writeSSHLocal(path, intent)
	})
	if e != nil {
		return nil, sshLocalFailure("startup")
	}
	status, ok := value.(worker.RuntimeStatus)
	if !ok || status.State != worker.StateRunning || (!intent.Reused && status.Lifecycle.WorkerVersion != rpc.Version) {
		return nil, sshLocalFailure()
	}
	result := sshsetup.SetupResult{Version: 1, OperationID: document.OperationID, ServerID: document.ServerID, DeviceID: credential.DeviceID, MachineID: credential.MachineID, Generation: status.Lifecycle.Generation, WorkerVersion: status.Lifecycle.WorkerVersion, Target: target, Running: true, Reused: intent.Reused}
	if result.Validate(document) != nil {
		return nil, sshLocalFailure()
	}
	intent.Phase = sshReady
	intent.Result = &result
	if e = writeSSHLocal(path, intent); e != nil {
		return nil, e
	}
	return result, nil
}
func writeSSHLocal(path string, value sshLocalIntent) error {
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	return security.WriteAtomic(path, raw)
}
