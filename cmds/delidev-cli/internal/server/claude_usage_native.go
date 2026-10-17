// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// Native side of the server-owned Claude usage login. One ordinary
// `claude auth login --claudeai` child runs in the exclusive, DeliDev-owned
// usage profile of one account. The profile is independent of the execution
// setup-token profile and of every personal, legacy or external profile: the
// adapter never enumerates or adopts any of them.

// claudeUsageCapture is the credential and identity of a joined login.
type claudeUsageCapture struct {
	Bundle   subscription.ClaudeUsageBundle
	Identity claude.UsageIdentity
}

func (c *claudeUsageCapture) clear() { c.Bundle.Clear() }

// claudeUsageNative is the native login owned by exactly one usage operation.
type claudeUsageNative interface {
	Progress() (claude.AuthLoginProgress, error)
	Submit([]byte) error
	Done() <-chan struct{}
	Wait() error
	// Close joins the child and wipes its buffers. Capture is only valid after
	// a successful Close.
	Close() error
	// Capture reads the newly created credential and identity from the joined
	// profile. The caller clears the returned bundle.
	Capture(context.Context) (claudeUsageCapture, error)
	// Release drops the exclusive profile lease. It is safe to call repeatedly:
	// a failed release is retried by the next call until it is confirmed.
	Release() error
}

// claudeUsageOpener starts the native login. The returned string is the observed
// executable version (empty when discovery did not reach an installation). An
// opener error proves that no native child is running; a RecoveryRequired
// error retains the original operation for recovery.
type claudeUsageOpener func(context.Context, nativeScope, domain.ID, *slog.Logger) (claudeUsageNative, string, error)

// claudeUsageStorageRemover removes the native credential storage of an
// account's usage profile. A missing profile owes nothing.
type claudeUsageStorageRemover func(context.Context, nativeScope, *slog.Logger) error

func claudeUsageStorageFor(root string) claude.UsageStorage {
	return claude.UsageStorage{ConfigDir: filepath.Join(root, "claude"), Account: claude.UsageKeychainAccount(), Keychain: claude.SystemUsageKeychain()}
}

type claudeUsageRuntime struct {
	login   *claude.AuthLogin
	lease   usageProfileLease
	config  claude.AuthConfig
	storage claude.UsageStorage
	owner   domain.ID
	logger  *slog.Logger

	closeOnce sync.Once
	closeErr  error
	joined    bool
	mu        sync.Mutex
}

// usageProfileLease is the exclusive profile lease of the usage login.
// nativeprofile.Lease implements it: a Close that failed is not final and may be
// retried until the lock release is confirmed.
type usageProfileLease interface{ Close() error }

func openClaudeUsageLogin(ctx context.Context, scope nativeScope, owner domain.ID, logger *slog.Logger) (returned claudeUsageNative, observed string, err error) {
	runtimeRoot := filepath.Join(scope.Root, "subscription-runtime")
	lease, e := acquireAccountProfile(ctx, scope, domain.ClaudeCode, nativeprofile.PurposeUsage)
	if e != nil {
		return nil, "", e
	}
	defer func() {
		if err != nil {
			_ = lease.Close()
		}
	}()
	root := lease.Root
	env, e := harness.PrivateRuntimeEnvironment(root)
	if e != nil {
		return nil, "", subscriptionDenied()
	}
	storage := claudeUsageStorageFor(root)
	// First admission: the profile must hold no native credential storage. A
	// leftover or foreign entry is never adopted as this login's result and never
	// deleted here; the owned cleanup removes only profile-derived storage.
	present, e := storage.Present(ctx)
	if e != nil || present {
		nativeprofile.Log(logger, "native_profile_blocked", lease.Key, lease.ID, nativeprofile.ReasonCredentialPresent, "operation_id", owner)
		return nil, "", nativeprofile.Failure(domain.RecoveryRequired, nativeprofile.ReasonCredentialPresent, "The Claude usage profile already holds credential storage.", "Let the owned cleanup finish before connecting usage again.")
	}
	installation, observed, e := discoverServerClaude(ctx, scope, runtimeRoot, owner, logger)
	if e != nil {
		return nil, observed, e
	}
	config := claude.AuthConfig{Version: installation.Version, Home: filepath.Join(root, "claude"), Process: process.Config{Directory: filepath.Join(runtimeRoot, "processes"), OwnerID: owner, Executable: installation.ResolvedPath, Cwd: root, Env: env, Logger: logger}}
	login, e := claude.StartUsageLogin(ctx, config)
	if e != nil {
		return nil, observed, e
	}
	return &claudeUsageRuntime{login: login, lease: lease, config: config, storage: storage, owner: owner, logger: logger}, observed, nil
}

func (r *claudeUsageRuntime) Progress() (claude.AuthLoginProgress, error) { return r.login.Progress() }
func (r *claudeUsageRuntime) Submit(code []byte) error                    { return r.login.Submit(code) }
func (r *claudeUsageRuntime) Done() <-chan struct{}                       { return r.login.Done() }
func (r *claudeUsageRuntime) Wait() error                                 { return r.login.Wait() }

func (r *claudeUsageRuntime) Close() error {
	r.closeOnce.Do(func() {
		r.closeErr = r.login.Close()
		r.mu.Lock()
		r.joined = r.closeErr == nil
		r.mu.Unlock()
	})
	return r.closeErr
}

func (r *claudeUsageRuntime) Capture(ctx context.Context) (claudeUsageCapture, error) {
	r.mu.Lock()
	joined := r.joined
	r.mu.Unlock()
	if !joined {
		return claudeUsageCapture{}, domain.Fail(domain.RecoveryRequired, "The Claude usage login child is not joined.", "Join the original login before reading its profile.")
	}
	bundle, err := r.storage.Read(ctx)
	if err != nil {
		return claudeUsageCapture{}, err
	}
	identity, err := claude.ReadUsageIdentity(ctx, r.config)
	if err != nil {
		bundle.Clear()
		return claudeUsageCapture{}, err
	}
	return claudeUsageCapture{Bundle: bundle, Identity: identity}, nil
}

// Release drops the exclusive profile lease. A failed close is reported again
// by every later call until the lock release is confirmed: the lease itself
// makes a confirmed close final, so repeated calls are safe, and a failure is
// never masked by a later nil that the lock never confirmed.
func (r *claudeUsageRuntime) Release() error {
	return r.lease.Close()
}

// removeClaudeUsageStorage removes only the native credential storage derived
// from the account's own usage profile. A busy profile (a live login) is not
// touched and keeps the removal owed.
func removeClaudeUsageStorage(ctx context.Context, scope nativeScope, logger *slog.Logger) error {
	if scope.Profiles == nil || scope.Account.Validate() != nil {
		return subscriptionDenied()
	}
	key := nativeprofile.Key{Harness: domain.ClaudeCode, Purpose: nativeprofile.PurposeUsage, Account: scope.Account}
	// Cleanup must never create a profile that did not exist.
	if _, found, err := scope.Profiles.Lookup(ctx, key); err != nil {
		return err
	} else if !found {
		return nil
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	lease, err := scope.Profiles.AcquireWait(bounded, key, 2*time.Second)
	if err != nil {
		return err
	}
	defer lease.Close()
	if err := claudeUsageStorageFor(lease.Root).Remove(ctx); err != nil {
		nativeprofile.Log(logger, "native_profile_credential_cleanup_blocked", lease.Key, lease.ID, nativeprofile.ReasonCredentialBlocked)
		return err
	}
	nativeprofile.Log(logger, "native_profile_credential_cleaned", lease.Key, lease.ID, nativeprofile.ReasonCredentialCleaned)
	return nil
}
