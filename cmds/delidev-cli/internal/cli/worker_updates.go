// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

func workerUpdateFailure() error {
	return domain.Fail(domain.RecoveryRequired, "The original Worker replacement is unconfirmed.", "Preserve both binaries and the original registration; inspect the exact update and Worker generation without resending installation.")
}
func updateEnvironment() []string {
	values := []string{}
	for _, key := range []string{"PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "TMP", "TEMP", "SystemRoot", "WINDIR", "COMSPEC", "LOCALAPPDATA", "APPDATA", "USERPROFILE", "LANG", "LC_ALL", "CARGO_HOME", "RUSTUP_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
		if value, ok := os.LookupEnv(key); ok {
			values = append(values, key+"="+value)
		}
	}
	return values
}
func preservePreviousWorker(root string) (string, *updates.Artifact, error) {
	executable, e := os.Executable()
	if e != nil {
		return "", nil, e
	}
	before, e := os.Lstat(executable)
	if e != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > updates.ArtifactLimit {
		return "", nil, workerUpdateFailure()
	}
	source, e := os.Open(executable)
	if e != nil {
		return "", nil, e
	}
	defer source.Close()
	opened, e := source.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return "", nil, workerUpdateFailure()
	}
	directory := filepath.Join(root, "worker-updates", "previous")
	if e = security.PrivateDir(directory); e != nil {
		return "", nil, e
	}
	file, e := os.CreateTemp(directory, ".pending-")
	if e != nil {
		return "", nil, e
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	defer file.Close()
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(file, h), io.LimitReader(source, updates.ArtifactLimit+1))
	after, statErr := source.Stat()
	named, nameErr := os.Lstat(executable)
	if e != nil || statErr != nil || nameErr != nil || n != before.Size() || !os.SameFile(before, after) || !os.SameFile(before, named) || !before.ModTime().Equal(after.ModTime()) {
		return "", nil, workerUpdateFailure()
	}
	if e = file.Chmod(0700); e == nil {
		e = file.Sync()
	}
	if e != nil {
		return "", nil, e
	}
	digest := hex.EncodeToString(h.Sum(nil))
	path := filepath.Join(directory, digest)
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if e = security.PublishImmutable(tmp, path); e != nil && !errors.Is(e, os.ErrExist) {
		return "", nil, e
	}
	target, e := updates.SelectTarget(runtime.GOOS, runtime.GOARCH)
	if e != nil {
		return "", nil, e
	}
	artifact := &updates.Artifact{Component: updates.Worker, Target: target, Name: updates.ArtifactName(updates.Worker, target), Size: n, SHA256: digest}
	if e = updates.VerifyFile(path, *artifact); e != nil {
		return "", nil, e
	}
	if e = security.SyncParent(path); e != nil {
		return "", nil, e
	}
	return path, artifact, nil
}
func executeWorkerHelper(ctx context.Context, o options, root, path string, id domain.ID, rollback bool) error {
	command := "update-replace"
	if rollback {
		command = "update-rollback"
	}
	bounded, stop := context.WithTimeout(ctx, 90*time.Second)
	defer stop()
	cmd := exec.CommandContext(bounded, path, "--json", "--data-dir", o.dataDir, "worker", command, "--worker-dir", root, "--operation-id", string(id))
	cmd.Env = updateEnvironment()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	// This helper is an owned direct child. Its replacement Worker has a separately
	// journaled generation and detached lifetime; timeout does not prove its exit.
	if e := cmd.Run(); e != nil {
		return workerUpdateFailure()
	}
	return nil
}
func handoffWorkerUpdate(ctx context.Context, o options, root string, id domain.ID) (any, error) {
	j, e := worker.ReadUpdateJournal(root, id)
	if e != nil {
		return nil, e
	}
	status, e := worker.Status(root)
	if e != nil || j.Phase != worker.UpdatePrepared || status.State != worker.StateExited || status.Lifecycle.Generation != j.OldGeneration || status.Lifecycle.Desired != worker.WorkerRunning {
		return nil, workerUpdateFailure()
	}
	credential, e := worker.LoadCredential(root)
	if e != nil ||
		domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(id), credential.ServerID != j.Operation.ServerID) ||
		domain.OwnershipBlocks(domain.OwnershipDevice, domain.ID(id), credential.DeviceID != j.Operation.DeviceID) ||
		domain.OwnershipBlocks(domain.OwnershipMachine, domain.ID(id), credential.MachineID != j.Operation.MachineID) {
		return nil, workerUpdateFailure()
	}
	verifier, e := updates.NewVerifier()
	if e != nil {
		return nil, e
	}
	candidate, e := verifier.Verify(j.Operation.Manifest, j.Operation.CurrentVersion, time.Now().UTC())
	if e != nil || candidate.ManifestSHA256 != j.Operation.ManifestSHA256 || candidate.Payload.Version != j.Operation.Version {
		return nil, workerUpdateFailure()
	}
	artifact, e := candidate.Artifact(updates.Worker, j.Operation.Target)
	if e != nil || updates.VerifyFile(j.ArtifactPath, artifact) != nil {
		return nil, workerUpdateFailure()
	}
	j.PreviousPath, j.PreviousArtifact, e = preservePreviousWorker(root)
	if e != nil {
		return nil, e
	}
	j.Phase = worker.UpdateStarting
	if e = worker.WriteUpdateJournal(root, j); e != nil {
		return nil, e
	}
	e = executeWorkerHelper(ctx, o, root, j.ArtifactPath, id, false)
	current, readError := worker.ReadUpdateJournal(root, id)
	if readError != nil {
		return nil, workerUpdateFailure()
	}
	// A committed success remains authoritative even if the helper's exit
	// response is lost. Once reporting begins, do not reverse a possibly
	// accepted success: inspect/replay that original report instead.
	if current.Phase == worker.UpdateComplete && current.Outcome == pb.WorkerUpdateOutcome_WORKER_UPDATE_OUTCOME_SUCCEEDED {
		return map[string]any{"update_id": id, "state": "succeeded", "version": current.Operation.Version}, nil
	}
	// Rollback needs positive exit of the exact failed generation. Never replace
	// a running/unknown process or a superseding explicit Stop/start decision.
	if current.NewGeneration == "" || current.Outcome != pb.WorkerUpdateOutcome_WORKER_UPDATE_OUTCOME_UNSPECIFIED {
		return nil, workerUpdateFailure()
	}
	beforeStop, statusError := worker.Status(root)
	if statusError != nil || beforeStop.Lifecycle.Generation != current.NewGeneration || beforeStop.Lifecycle.Desired != worker.WorkerRunning || ctx.Err() != nil {
		return nil, workerUpdateFailure()
	}
	stopped, stopErr := stopLocalWorker(ctx, root, current.NewGeneration)
	status, ok := stopped.(worker.RuntimeStatus)
	if stopErr != nil || !ok || status.State != worker.StateExited || status.Lifecycle.Generation != current.NewGeneration {
		return nil, workerUpdateFailure()
	}
	current.Phase = worker.UpdateRollback
	current.ReportID = domain.NewID()
	current.Outcome = 0
	if e = worker.WriteUpdateJournal(root, current); e != nil {
		return nil, e
	}
	if e = executeWorkerHelper(ctx, o, root, current.PreviousPath, id, true); e != nil {
		return nil, workerUpdateFailure()
	}
	current, e = worker.ReadUpdateJournal(root, id)
	if e != nil || current.Phase != worker.UpdateComplete {
		return nil, workerUpdateFailure()
	}
	return map[string]any{"update_id": id, "state": "failed", "previous_version_retained": true, "version": current.Operation.CurrentVersion}, nil
}
func workerReplacementCommand(ctx context.Context, o options, args []string, rollback bool) (any, error) {
	fs := flags("worker replacement")
	root := fs.String("worker-dir", "", "original private Worker root")
	id := fs.String("operation-id", "", "original accepted update")
	if parse(fs, args) != nil || o.server != "" || o.tokenStdin || *root == "" || domain.ID(*id).Validate() != nil {
		return nil, usage()
	}
	lock, e := security.TryLock(filepath.Join(*root, "worker-updates", "replacement.lock"))
	if e != nil {
		return nil, e
	}
	defer lock.Close()
	j, e := worker.ReadUpdateJournal(*root, domain.ID(*id))
	if e != nil {
		return nil, e
	}
	phase, version := worker.UpdateStarting, j.Operation.Version
	generation := j.NewGeneration
	path := j.ArtifactPath
	if rollback {
		phase, version, generation, path = worker.UpdateRollback, j.Operation.CurrentVersion, j.RollbackGeneration, j.PreviousPath
	}
	if j.Phase != phase || generation != "" || rpc.Version != version {
		return nil, workerUpdateFailure()
	}
	executable, e := os.Executable()
	if e != nil {
		return nil, e
	}
	var artifact updates.Artifact
	if rollback {
		if j.PreviousArtifact == nil {
			return nil, workerUpdateFailure()
		}
		artifact = *j.PreviousArtifact
	} else {
		v, e := updates.NewVerifier()
		if e != nil {
			return nil, e
		}
		verified, e := v.Verify(j.Operation.Manifest, j.Operation.CurrentVersion, time.Now().UTC())
		if e != nil || verified.ManifestSHA256 != j.Operation.ManifestSHA256 {
			return nil, workerUpdateFailure()
		}
		artifact, e = verified.Artifact(updates.Worker, j.Operation.Target)
		if e != nil {
			return nil, e
		}
	}
	actual, err := filepath.Abs(executable)
	expected, pathErr := filepath.Abs(path)
	if err != nil || pathErr != nil || actual != expected || updates.VerifyFile(executable, artifact) != nil {
		return nil, workerUpdateFailure()
	}
	status, e := worker.Status(*root)
	if e != nil || status.State != worker.StateExited {
		return nil, workerUpdateFailure()
	}
	expectedOld := j.OldGeneration
	if rollback {
		expectedOld = j.NewGeneration
	}
	if status.Lifecycle.Generation != expectedOld {
		return nil, workerUpdateFailure()
	}
	return startDetachedWorkerWithAdmission(ctx, o, *root, func(status worker.RuntimeStatus) error {
		if rollback {
			j.RollbackGeneration = status.Lifecycle.Generation
		} else {
			j.NewGeneration = status.Lifecycle.Generation
		}
		return worker.WriteUpdateJournal(*root, j)
	})
}
