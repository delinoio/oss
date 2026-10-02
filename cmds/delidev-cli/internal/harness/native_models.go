// SPDX-License-Identifier: Apache-2.0
package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
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
	root := filepath.Join(config.Root, "probes")
	if err := security.PrivateDir(root); err != nil {
		return result, err
	}
	directory, err := os.MkdirTemp(root, string(config.OwnerID)+"-models-")
	if err != nil {
		return result, domain.NativeModelFailure()
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return result, domain.NativeModelFailure()
	}
	processRoot := filepath.Join(config.Root, "processes")
	defer func() {
		if err := process.ReconcileOwner(processRoot, config.OwnerID); err != nil {
			result, returned = domain.NativeModelObservation{}, err
			return
		}
		if os.RemoveAll(directory) != nil {
			result, returned = domain.NativeModelObservation{}, domain.Fail(domain.RecoveryRequired, "Native model observation cleanup is unconfirmed.", "Retain the original operation and reconcile its private runtime.")
			return
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
	snapshot := filepath.Join(directory, "codex-native"+filepath.Ext(scope.Executable))
	source, err := os.Open(scope.Executable)
	if err != nil {
		return result, domain.NativeModelFailure()
	}
	target, err := os.OpenFile(snapshot, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		_ = source.Close()
		return result, domain.NativeModelFailure()
	}
	_, copyErr := io.Copy(target, io.LimitReader(source, (512<<20)+1))
	syncErr := target.Sync()
	closeErr := target.Close()
	sourceErr := source.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || sourceErr != nil {
		return result, domain.NativeModelFailure()
	}
	copied, err := InspectExecutable(ctx, snapshot)
	if err != nil || copied != digest {
		return result, domain.NativeModelFailure()
	}
	// The isolated native profile receives neither an execution credential nor
	// the selected account's key. Native API listings are static/fallback data.
	client, err := codex.Open(ctx, codex.Config{ModelObservation: true, Version: scope.NativeVersion, Home: filepath.Join(directory, "codex"), Process: process.Config{Directory: processRoot, OwnerID: config.OwnerID, Executable: snapshot, Cwd: directory, Env: env, Logger: config.Logger}})
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
