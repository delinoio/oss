// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func reconcileFailedServerLoginRuntime(ctx context.Context, root string, o domain.ServerSubscriptionOperation) error {
	runtimeRoot := filepath.Join(root, "subscription-runtime")
	home := filepath.Join(runtimeRoot, "auth", string(o.ID))
	if !o.NativeStarted {
		// The durable claim barrier proves that a queued login never launched;
		// settled operation metadata clears this flag only after joined cleanup.
		// Unexpected retained files contradict either proof and must be preserved.
		if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
			return subscriptionDenied()
		}
		return cleanupFailedServerLoginProbes(ctx, runtimeRoot, o.ID, false)
	}
	if err := security.CheckPrivateDir(runtimeRoot); err != nil {
		return subscriptionDenied()
	}
	processRoot := filepath.Join(runtimeRoot, "processes")
	if err := security.CheckPrivateDir(processRoot); err != nil {
		return subscriptionDenied()
	}
	if err := lockFailedServerLoginControllers(ctx, processRoot, o.ID); err != nil {
		return err
	}
	// Missing process journals cannot prove cleanup. The existing owner index
	// survives verified journal retirement, including when it becomes empty.
	if err := process.ReconcileOwnerContext(ctx, processRoot, o.ID); err != nil {
		return err
	}
	if err := cleanupFailedServerLoginProbes(ctx, runtimeRoot, o.ID, true); err != nil {
		return err
	}
	return cleanupFailedServerLoginDirectory(home)
}

// A failed vault lookup can happen before the native opener runs. The durable
// claim still sets NativeStarted as the account fence, so this separate check
// proves the narrower pre-native case without pretending that a process journal
// was joined. Any retained evidence remains blocked for a later server process.
func reconcileFailedServerLoginPreNative(ctx context.Context, root string, owner domain.ID) error {
	if err := owner.Validate(); err != nil {
		return err
	}
	runtimeRoot := filepath.Join(root, "subscription-runtime")
	for _, path := range []string{
		runtimeRoot,
		filepath.Join(runtimeRoot, "auth"),
		filepath.Join(runtimeRoot, "processes"),
	} {
		if err := checkOptionalPrivateDirectory(path); err != nil {
			return err
		}
	}
	home := filepath.Join(runtimeRoot, "auth", string(owner))
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		return subscriptionDenied()
	}
	processOwner := filepath.Join(runtimeRoot, "processes", string(owner))
	if _, err := os.Lstat(processOwner); !errors.Is(err, os.ErrNotExist) {
		return subscriptionDenied()
	}
	return cleanupFailedServerLoginProbes(ctx, runtimeRoot, owner, false)
}

func checkOptionalPrivateDirectory(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || security.CheckPrivateDir(path) != nil {
		return subscriptionDenied()
	}
	return nil
}

// The maintenance runner excludes its own live login goroutines. Retained
// controller locks independently prevent recovery from stopping a live Handle;
// an orphaned supervisor is recoverable only after that controller has left.
func lockFailedServerLoginControllers(ctx context.Context, processRoot string, owner domain.ID) error {
	path := filepath.Join(processRoot, string(owner))
	if err := security.CheckPrivateDir(path); err != nil {
		return subscriptionDenied()
	}
	index, err := os.Open(path)
	if err != nil {
		return subscriptionDenied()
	}
	defer index.Close()
	var locks []*security.Lock
	release := func() error {
		var releaseErr error
		for _, lock := range locks {
			releaseErr = errors.Join(releaseErr, lock.Close())
		}
		locks = nil
		return releaseErr
	}
	keep := false
	defer func() {
		if !keep {
			_ = release()
		}
	}()
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		entries, err := index.ReadDir(256)
		if err != nil && !errors.Is(err, io.EOF) {
			return subscriptionDenied()
		}
		if len(entries) == 0 {
			// The preflight only excludes a still-running original controller.
			// Release those locks before the shared reconciler reacquires each
			// controller lock; no new process can claim this immutable operation.
			if err := release(); err != nil {
				return err
			}
			keep = true
			return nil
		}
		count += len(entries)
		if count > 10000 {
			return subscriptionDenied()
		}
		for _, entry := range entries {
			if !entry.IsDir() || domain.ID(strings.TrimPrefix(entry.Name(), ".retired-")).Validate() != nil {
				return subscriptionDenied()
			}
			if strings.HasPrefix(entry.Name(), ".retired-") {
				continue
			}
			scope := filepath.Join(path, entry.Name())
			controller := filepath.Join(scope, "controller.lock")
			if security.CheckPrivateDir(scope) != nil || security.RegularPrivate(controller) != nil {
				return subscriptionDenied()
			}
			lock, err := security.TryLock(controller)
			if err != nil {
				return err
			}
			locks = append(locks, lock)
		}
	}
}

func cleanupFailedServerLoginDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		parent := filepath.Dir(path)
		if err := security.CheckPrivateDir(parent); errors.Is(err, os.ErrNotExist) {
			// Native process proof was established separately. Synchronize the
			// absent parent in its private runtime root instead of inventing a path.
			if err := security.CheckPrivateDir(filepath.Dir(parent)); err != nil {
				return subscriptionDenied()
			}
			return security.SyncParent(parent)
		} else if err != nil {
			return subscriptionDenied()
		}
		return security.SyncParent(path)
	}
	if err != nil || security.CheckPrivateDir(filepath.Dir(path)) != nil {
		return subscriptionDenied()
	}
	info, err = security.StableStat(path)
	if err != nil {
		return subscriptionDenied()
	}
	return subscription.CleanupRuntime(path, info)
}

func cleanupFailedServerLoginProbes(ctx context.Context, runtimeRoot string, owner domain.ID, remove bool) error {
	path := filepath.Join(runtimeRoot, "probes")
	if err := security.CheckPrivateDir(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if remove {
				// Process ownership was confirmed separately; make a previously
				// removed probe index durable before publishing the checkpoint.
				return security.SyncParent(path)
			}
			return nil
		}
		return subscriptionDenied()
	}
	index, err := os.Open(path)
	if err != nil {
		return subscriptionDenied()
	}
	defer index.Close()
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		entries, err := index.ReadDir(256)
		if err != nil && !errors.Is(err, io.EOF) {
			return subscriptionDenied()
		}
		if len(entries) == 0 {
			// The original probe may already be absent after an interrupted
			// discovery cleanup. Synchronize its containing index as well.
			return security.SyncParent(filepath.Join(path, string(owner)))
		}
		count += len(entries)
		if count > 10000 {
			return subscriptionDenied()
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), string(owner)+"-") {
				continue
			}
			if !remove || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return subscriptionDenied()
			}
			info, err := entry.Info()
			if err != nil {
				return subscriptionDenied()
			}
			if err := subscription.CleanupRuntime(filepath.Join(path, entry.Name()), info); err != nil {
				return err
			}
		}
	}
}
