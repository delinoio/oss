// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// Server-owned Claude usage tracking.
//
// A separate, explicitly linked read-only OAuth login of a server-token Claude
// account runs the original `claude auth login --claudeai` in one exclusive
// persistent usage profile of that account. The profile exists only for this
// purpose: personal, legacy execution and external profiles are never read. The
// native credentials stay in the profile only while a login or refresh-login is
// running; the claude-usage vault reference is the sole authority afterwards.
// Execution authentication and readiness are never changed by any of it.

// claudeUsageLoginProcess is the native child of one login or refresh-login.
type claudeUsageLoginProcess interface {
	Progress() (claude.AuthLoginProgress, error)
	Submit([]byte) error
	Done() <-chan struct{}
	Wait() error
	Close() error
}

// claudeUsageStorage is the native credential storage of exactly one profile.
type claudeUsageStorage interface {
	Present(context.Context) (bool, error)
	Read(context.Context) ([]byte, error)
	Remove(context.Context) error
}

// claudeUsageNative is the native boundary of one exclusively held profile.
type claudeUsageNative interface {
	Storage() claudeUsageStorage
	StartLogin(context.Context) (claudeUsageLoginProcess, error)
	StartRefresh(context.Context, claude.UsageRefreshInput) (claudeUsageLoginProcess, error)
	Identity(context.Context) (claude.UsageIdentity, error)
	// Close releases the exclusive profile lease. The profile and its caches are
	// retained; only explicit disconnect, logout or deletion removes them.
	Close() error
}

// claudeUsageOpener acquires the exclusive usage profile for one operation.
type claudeUsageOpener func(ctx context.Context, scope nativeScope, operation domain.ID, logger *slog.Logger) (claudeUsageNative, error)

type serverClaudeUsageNative struct {
	config claude.UsageConfig
	lease  *nativeprofile.Lease
	store  claude.UsageStore
	once   sync.Once
	closed error
}

func openClaudeUsageNative(ctx context.Context, scope nativeScope, operation domain.ID, logger *slog.Logger) (returned claudeUsageNative, err error) {
	lease, e := acquireAccountProfile(ctx, scope, domain.ClaudeCode, nativeprofile.PurposeUsage)
	if e != nil {
		return nil, e
	}
	defer func() {
		if err != nil {
			err = failClaudeUsageOpen(lease, err)
		}
	}()
	root := lease.Root
	env, e := harness.PrivateRuntimeEnvironment(root)
	if e != nil {
		return nil, domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureProfileUnsafe, 0)
	}
	runtimeRoot := filepath.Join(scope.Root, "subscription-runtime")
	installation, e := harness.DiscoverClaude(ctx, harness.DiscoveryConfig{Root: runtimeRoot, OwnerID: operation, Logger: logger, Profiles: scope.Profiles})
	if e != nil || installation.State != domain.InstallationDetected {
		if ctx.Err() != nil {
			return nil, domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureInterrupted, 0)
		}
		return nil, domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureInstallation, 0)
	}
	if !domain.HarnessVersionAllowed(domain.ClaudeCode, installation.Version) || !installation.ProtocolVerified || installation.Protocol == nil || installation.Protocol.State != domain.ProtocolVerified || installation.Problem != nil || installation.ResolvedPath == "" {
		return nil, domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureInstallation, 0)
	}
	config := claude.UsageConfig{Version: installation.Version, Home: filepath.Join(root, "claude"), Process: process.Config{Directory: filepath.Join(runtimeRoot, "processes"), OwnerID: operation, Executable: installation.ResolvedPath, Cwd: root, Env: env, Logger: logger}}
	return &serverClaudeUsageNative{config: config, lease: lease, store: claude.NewUsageStore(root)}, nil
}

func (n *serverClaudeUsageNative) Storage() claudeUsageStorage { return n.store }
func (n *serverClaudeUsageNative) StartLogin(ctx context.Context) (claudeUsageLoginProcess, error) {
	login, err := claude.StartUsageLogin(ctx, n.config)
	if err != nil {
		return nil, err
	}
	return login, nil
}
func (n *serverClaudeUsageNative) StartRefresh(ctx context.Context, input claude.UsageRefreshInput) (claudeUsageLoginProcess, error) {
	login, err := claude.StartUsageRefresh(ctx, n.config, input)
	if err != nil {
		return nil, err
	}
	return login, nil
}
func (n *serverClaudeUsageNative) Identity(ctx context.Context) (claude.UsageIdentity, error) {
	return claude.ReadUsageIdentity(ctx, n.config)
}
func (n *serverClaudeUsageNative) Close() error {
	n.once.Do(func() { n.closed = n.lease.Close() })
	return n.closed
}

// claudeUsageLeaseReleaseError reports an open that failed and could not release
// the exclusive usage profile lease either. The lease may stay locked, so the
// caller records a cleanup obligation instead of an ordinary failure.
type claudeUsageLeaseReleaseError struct {
	cause   error
	release error
}

func (e *claudeUsageLeaseReleaseError) Error() string {
	return "Claude usage profile lease not released."
}
func (e *claudeUsageLeaseReleaseError) Unwrap() []error {
	return []error{e.cause, e.release}
}

// failClaudeUsageOpen releases the lease of a failed open and keeps a release
// failure visible to the caller.
func failClaudeUsageOpen(lease interface{ Close() error }, cause error) error {
	if releaseErr := lease.Close(); releaseErr != nil {
		return &claudeUsageLeaseReleaseError{cause: cause, release: releaseErr}
	}
	return cause
}

// claudeUsageLeaseLeaked reports whether a failed open could not release its lease.
func claudeUsageLeaseLeaked(err error) bool {
	var leaked *claudeUsageLeaseReleaseError
	return errors.As(err, &leaked)
}

// claudeUsageFailureFor maps an arbitrary error to the closed usage failure
// vocabulary. An unclassified error never leaks its text.
func claudeUsageFailureFor(err error, fallback domain.ClaudeLimitFailure) domain.ClaudeLimitFailure {
	if err == nil {
		return domain.ClaudeLimitFailureNone
	}
	if failure := domain.ClaudeLimitFailureOf(err); failure != domain.ClaudeLimitFailureNone {
		return failure
	}
	if errors.Is(err, context.Canceled) {
		return domain.ClaudeLimitFailureInterrupted
	}
	if reason, ok := nativeprofile.Reasoned(err); ok && (reason == nativeprofile.ReasonCredentialPresent || reason == nativeprofile.ReasonOwnerInvalid || reason == nativeprofile.ReasonRegistryInvalid) {
		return domain.ClaudeLimitFailureProfileUnsafe
	}
	return fallback
}

// claudeLimitIdentityCommitment is the keyed digest of the stable validated
// identity (email and organization identifier). A refresh must reproduce it.
// It is bound to this server and carries no decodable identity.
func (s *Service) claudeLimitIdentityCommitment(account domain.ID, identity claude.UsageIdentity) string {
	mac := hmac.New(sha256.New, []byte(s.Identity.Token))
	mac.Write([]byte("delidev/claude-usage-identity/v1\x00" + string(s.Identity.ServerID) + "\x00" + string(account) + "\x00"))
	key := identity.StableKey()
	mac.Write(key)
	clear(key)
	return hex.EncodeToString(mac.Sum(nil))
}
