// SPDX-License-Identifier: Apache-2.0
package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"io"
	"os"
	"path/filepath"
	"time"
)

// InspectExecutable pins bytes and file identity instead of trusting a mutable
// PATH or a version string. No executable contents enter diagnostics.
func InspectExecutable(ctx context.Context, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", domain.NativeModelFailure()
	}
	f, err := os.Open(path)
	if err != nil {
		return "", domain.NativeModelFailure()
	}
	defer f.Close()
	original, err := f.Stat()
	if err != nil || !original.Mode().IsRegular() || original.Size() > 512<<20 {
		return "", domain.NativeModelFailure()
	}
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	var total int64
	for {
		if ctx.Err() != nil {
			return "", domain.SafeError(ctx.Err())
		}
		n, err := f.Read(buffer)
		total += int64(n)
		if total > original.Size() {
			return "", domain.NativeModelFailure()
		}
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", domain.NativeModelFailure()
		}
	}
	current, err := os.Lstat(path)
	final, finalErr := f.Stat()
	if err != nil || finalErr != nil || !os.SameFile(original, current) || !os.SameFile(original, final) || original.Size() != final.Size() || !original.ModTime().Equal(final.ModTime()) {
		return "", domain.NativeModelFailure()
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func DiscoverNativeModels(ctx context.Context, config DiscoveryConfig, scope domain.NativeModelScope) (result domain.NativeModelObservation, returned error) {
	started := time.Now()
	if config.Logger != nil {
		config.Logger.InfoContext(ctx, "native model observation started", "job_id", config.OwnerID, "machine_id", scope.MachineID, "account_id", scope.AccountID, "phase", "observe")
	}
	defer func() {
		if config.Logger != nil {
			code := domain.Code("")
			if returned != nil {
				code = domain.SafeError(returned).Code
			}
			config.Logger.InfoContext(ctx, "native model observation finished", "job_id", config.OwnerID, "machine_id", scope.MachineID, "account_id", scope.AccountID, "phase", "cleanup", "duration_ms", time.Since(started).Milliseconds(), "model_count", len(result.Models), "code", code)
		}
	}()
	if err := scope.Validate(); err != nil {
		return result, err
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ctx = bounded
	digest, err := InspectExecutable(ctx, scope.Executable)
	if err != nil || digest != scope.ExecutableSHA256 {
		return result, domain.NativeModelFailure()
	}
	lease, err := acquireSystemProfile(ctx, config.Profiles, domain.Codex, nativeprofile.PurposeModelObservation)
	if err != nil {
		return result, domain.NativeModelFailure()
	}
	defer lease.Close()
	directory := lease.Root
	processRoot := filepath.Join(config.Root, "processes")
	// snapshot is set only after O_EXCL proves that this attempt created the
	// transient executable copy, so cleanup never removes another attempt's file.
	var snapshot string
	defer func() {
		// The profile and its history are retained. The per-operation process
		// journal is joined first: a still-running or unproven process may use the
		// executable copy, so the copy is removed only after that proof.
		if err := process.ReconcileOwner(processRoot, config.OwnerID); err != nil {
			if config.Logger != nil {
				config.Logger.WarnContext(ctx, "native model observation cleanup unconfirmed", "job_id", config.OwnerID, "phase", "cleanup", "transient_copy_retained", snapshot != "")
			}
			result, returned = domain.NativeModelObservation{}, err
			return
		}
		if snapshot != "" {
			if err := os.Remove(snapshot); err != nil && !errors.Is(err, os.ErrNotExist) {
				result, returned = domain.NativeModelObservation{}, domain.Fail(domain.RecoveryRequired, "Native model observation cleanup is unconfirmed.", "Retain the original operation and reconcile its transient executable copy.")
				return
			}
		}
		if returned == nil {
			result.CleanupVerified = true
		}
	}()
	env, err := PrivateRuntimeEnvironment(directory)
	if err != nil {
		return result, err
	}
	// Launch an identity-checked private copy, closing the pathname replacement
	// window between source inspection and OS process creation.
	if err := security.PrivateDir(filepath.Join(directory, "tmp")); err != nil {
		return result, domain.NativeModelFailure()
	}
	snapshotPath := filepath.Join(directory, "tmp", "codex-native-"+string(config.OwnerID)+filepath.Ext(scope.Executable))
	if _, statErr := os.Lstat(snapshotPath); statErr == nil {
		// An interrupted attempt of this same operation retained its copy. When it
		// ever launched a process, the copy may be removed only after its owned
		// processes are proven joined; any uncertainty keeps the copy and fails the
		// retry. An owner with no process journal never launched anything.
		if _, journalErr := os.Lstat(filepath.Join(processRoot, string(config.OwnerID))); journalErr == nil {
			if err := process.ReconcileOwner(processRoot, config.OwnerID); err != nil {
				return result, err
			}
		} else if !errors.Is(journalErr, os.ErrNotExist) {
			return result, domain.NativeModelFailure()
		}
		if err := os.Remove(snapshotPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, domain.Fail(domain.RecoveryRequired, "Native model observation cleanup is unconfirmed.", "Retain the original operation and reconcile its transient executable copy.")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return result, domain.NativeModelFailure()
	}
	source, err := os.Open(scope.Executable)
	if err != nil {
		return result, domain.NativeModelFailure()
	}
	target, err := os.OpenFile(snapshotPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		_ = source.Close()
		return result, domain.NativeModelFailure()
	}
	snapshot = snapshotPath
	_, copyErr := io.Copy(target, io.LimitReader(source, (512<<20)+1))
	syncErr := target.Sync()
	closeErr := target.Close()
	sourceErr := source.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || sourceErr != nil {
		return result, domain.NativeModelFailure()
	}
	copied, err := InspectExecutable(ctx, snapshotPath)
	if err != nil || copied != digest {
		return result, domain.NativeModelFailure()
	}
	// The isolated native profile receives neither an execution credential nor
	// the selected account's key. Native API listings are static/fallback data.
	client, err := codex.Open(ctx, codex.Config{ModelObservation: true, Version: scope.NativeVersion, Home: filepath.Join(directory, "codex"), Process: process.Config{Directory: processRoot, OwnerID: config.OwnerID, Executable: snapshotPath, Cwd: directory, Env: env, Logger: config.Logger}})
	if err != nil {
		return result, err
	}
	models, err := client.ModelList(ctx, scope.IncludeHidden)
	closeErr = client.Close()
	if closeErr != nil {
		return result, domain.Fail(domain.RecoveryRequired, "Native model process cleanup is unconfirmed.", "Retain the original operation and reconcile owned processes.")
	}
	if err != nil {
		return result, err
	}
	current, err := InspectExecutable(ctx, scope.Executable)
	if err != nil || current != digest {
		return result, domain.NativeModelFailure()
	}
	return domain.NativeModelObservation{Version: 1, ObservedAt: time.Now().UTC(), Models: models}, nil
}
