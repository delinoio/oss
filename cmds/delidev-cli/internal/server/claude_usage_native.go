// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"
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

// usageLeaseKeeper retains exclusive usage profile leases whose release was not
// confirmed. A failed Close leaves the lease open and holding the profile lock;
// once its runtime is dropped nothing else could retry it, so every later login,
// renewal and cleanup would find the profile busy until the server restarts.
// Every release funnels through close, and every acquisition first calls retry.
type usageLeaseKeeper struct {
	mu      sync.Mutex
	pending []usageProfileLease
}

var strandedUsageLeases usageLeaseKeeper

// close releases lease. A failed release is reported and the lease is retained
// for retry.
func (k *usageLeaseKeeper) close(lease usageProfileLease) error {
	err := lease.Close()
	if err == nil {
		return nil
	}
	k.mu.Lock()
	if !slices.Contains(k.pending, lease) {
		k.pending = append(k.pending, lease)
	}
	k.mu.Unlock()
	return err
}

// retry closes every retained lease again, dropping the confirmed ones, and
// returns how many are still unconfirmed.
func (k *usageLeaseKeeper) retry(logger *slog.Logger) int {
	k.mu.Lock()
	pending := k.pending
	k.pending = nil
	k.mu.Unlock()
	var failed []usageProfileLease
	for _, lease := range pending {
		if err := lease.Close(); err != nil {
			failed = append(failed, lease)
			if logger != nil {
				logger.Warn("claude_usage_profile_release_retry_failed", "code", domain.SafeError(err).Code)
			}
		}
	}
	if len(pending) != 0 && logger != nil {
		logger.Info("claude_usage_profile_release_retried", "retained", len(pending), "unconfirmed", len(failed))
	}
	k.mu.Lock()
	for _, lease := range failed {
		if !slices.Contains(k.pending, lease) {
			k.pending = append(k.pending, lease)
		}
	}
	remaining := len(k.pending)
	k.mu.Unlock()
	return remaining
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
	strandedUsageLeases.retry(logger)
	lease, e := acquireAccountProfile(ctx, scope, domain.ClaudeCode, nativeprofile.PurposeUsage)
	if e != nil {
		return nil, "", e
	}
	defer func() {
		if err != nil {
			// A failed release is retained and retried before the next acquisition.
			if closeErr := strandedUsageLeases.close(lease); closeErr != nil && logger != nil {
				logger.Warn("claude_usage_profile_release_failed", "code", domain.SafeError(closeErr).Code)
			}
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
	return strandedUsageLeases.close(r.lease)
}

// removeClaudeUsageStorage removes only the native credential storage derived
// from the account's own usage profile. A busy profile (a live login) is not
// touched and keeps the removal owed.
func removeClaudeUsageStorage(ctx context.Context, scope nativeScope, logger *slog.Logger) (err error) {
	if scope.Profiles == nil || scope.Account.Validate() != nil {
		return subscriptionDenied()
	}
	key := nativeprofile.Key{Harness: domain.ClaudeCode, Purpose: nativeprofile.PurposeUsage, Account: scope.Account}
	strandedUsageLeases.retry(logger)
	// Cleanup must never create a profile that did not exist.
	if _, found, lookupErr := scope.Profiles.Lookup(ctx, key); lookupErr != nil {
		return lookupErr
	} else if !found {
		return nil
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	lease, err := scope.Profiles.AcquireWait(bounded, key, 2*time.Second)
	if err != nil {
		return err
	}
	// The removal is reported complete only once the exclusive profile lease was
	// released too: an unconfirmed release must keep the cleanup owed, otherwise
	// the next login could be blocked by the unresolved lock with no durable flag
	// left to retry it.
	defer func() { err = closeUsageProfileLease(lease, err, logger) }()
	if removeErr := claudeUsageStorageFor(lease.Root).Remove(ctx); removeErr != nil {
		nativeprofile.Log(logger, "native_profile_credential_cleanup_blocked", lease.Key, lease.ID, nativeprofile.ReasonCredentialBlocked)
		return removeErr
	}
	nativeprofile.Log(logger, "native_profile_credential_cleaned", lease.Key, lease.ID, nativeprofile.ReasonCredentialCleaned)
	return nil
}

// closeUsageProfileLease releases the exclusive usage profile lease. A failed
// release is returned when the guarded operation had no earlier error, so the
// caller never reports success for a lock that was not confirmed released; an
// earlier error stays the reported cause.
func closeUsageProfileLease(lease usageProfileLease, err error, logger *slog.Logger) error {
	closeErr := strandedUsageLeases.close(lease)
	if closeErr == nil {
		return err
	}
	if logger != nil {
		logger.Warn("claude_usage_profile_release_failed", "code", domain.SafeError(closeErr).Code)
	}
	if err != nil {
		return err
	}
	return closeErr
}

// claudeUsageRenewal is the native side of one supervised refresh-login. It
// owns the exclusive usage profile lease from open to Release, runs the one
// child and joins it, and captures the rotated credential only after a joined
// completion.
type claudeUsageRenewal interface {
	// Run executes the single refresh-login child and joins it. The status says
	// whether an exchange could have happened; the caller never calls Run twice.
	Run(context.Context) (claude.UsageRefreshStatus, error)
	// Capture reads the rotated credential and identity from the joined profile.
	// The caller clears the returned bundle.
	Capture(context.Context) (claudeUsageCapture, error)
	// Release drops the exclusive profile lease. It is safe to call repeatedly:
	// a failed release is retried by the next call until it is confirmed.
	Release() error
}

// claudeUsageRenewer opens a renewal for the current bundle. The returned string
// is the observed executable version. An opener error proves that no child ran;
// a RecoveryRequired error retains the original operation for recovery.
type claudeUsageRenewer func(context.Context, nativeScope, domain.ID, subscription.ClaudeUsageBundle, *slog.Logger) (claudeUsageRenewal, string, error)

type claudeUsageRenewalRuntime struct {
	lease   usageProfileLease
	config  claude.AuthConfig
	storage claude.UsageStorage
	bundle  subscription.ClaudeUsageBundle

	mu     sync.Mutex
	joined bool
}

func openClaudeUsageRenewal(ctx context.Context, scope nativeScope, owner domain.ID, bundle subscription.ClaudeUsageBundle, logger *slog.Logger) (returned claudeUsageRenewal, observed string, err error) {
	runtimeRoot := filepath.Join(scope.Root, "subscription-runtime")
	strandedUsageLeases.retry(logger)
	lease, e := acquireAccountProfile(ctx, scope, domain.ClaudeCode, nativeprofile.PurposeUsage)
	if e != nil {
		return nil, "", e
	}
	defer func() {
		if err != nil {
			// A failed release is retained and retried before the next acquisition.
			if closeErr := strandedUsageLeases.close(lease); closeErr != nil && logger != nil {
				logger.Warn("claude_usage_profile_release_failed", "code", domain.SafeError(closeErr).Code)
			}
		}
	}()
	root := lease.Root
	env, e := harness.PrivateRuntimeEnvironment(root)
	if e != nil {
		return nil, "", subscriptionDenied()
	}
	storage := claudeUsageStorageFor(root)
	// The linked connection keeps no native credential storage between reads, so
	// leftover storage is never this renewal's input and is never adopted.
	present, e := storage.Present(ctx)
	if e != nil || present {
		nativeprofile.Log(logger, "native_profile_blocked", lease.Key, lease.ID, nativeprofile.ReasonCredentialPresent, "operation_id", owner)
		return nil, "", nativeprofile.Failure(domain.RecoveryRequired, nativeprofile.ReasonCredentialPresent, "The Claude usage profile already holds credential storage.", "Let the owned cleanup finish before renewing the usage credential.")
	}
	installation, observed, e := discoverServerClaude(ctx, scope, runtimeRoot, owner, logger)
	if e != nil {
		return nil, observed, e
	}
	config := claude.AuthConfig{Version: installation.Version, Home: filepath.Join(root, "claude"), Process: process.Config{Directory: filepath.Join(runtimeRoot, "processes"), OwnerID: owner, Executable: installation.ResolvedPath, Cwd: root, Env: env, Logger: logger}}
	return &claudeUsageRenewalRuntime{lease: lease, config: config, storage: storage, bundle: bundle}, observed, nil
}

func (r *claudeUsageRenewalRuntime) Run(ctx context.Context) (claude.UsageRefreshStatus, error) {
	status, err := claude.RunUsageRefresh(ctx, r.config, r.bundle)
	r.mu.Lock()
	r.joined = status == claude.UsageRefreshCompleted
	r.mu.Unlock()
	return status, err
}

func (r *claudeUsageRenewalRuntime) Capture(ctx context.Context) (claudeUsageCapture, error) {
	r.mu.Lock()
	joined := r.joined
	r.mu.Unlock()
	if !joined {
		return claudeUsageCapture{}, domain.Fail(domain.RecoveryRequired, "The Claude usage renewal child is not joined.", "Join the original renewal before reading its profile.")
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
// by every later call until the lock release is confirmed, like the login
// runtime: the lease itself makes a confirmed close final.
func (r *claudeUsageRenewalRuntime) Release() error {
	return strandedUsageLeases.close(r.lease)
}
