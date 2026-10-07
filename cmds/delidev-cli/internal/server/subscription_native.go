// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"time"
)

type serverSubscriptionNative interface {
	Version() string
	StartManagedLogin(context.Context, bool) (codex.ManagedLoginProgress, error)
	WaitManagedLogin(context.Context, string) error
	CancelManagedLogin(context.Context, string) error
	ManagedBundle(context.Context, bool) ([]byte, error)
	LogoutManaged(context.Context) error
	Close([]byte) error
}

// A non-recovery opener error proves joined native and private-file cleanup.
// A recovery error retains the original operation and runtime for inspection.
type serverSubscriptionOpener func(context.Context, string, domain.ID, []byte, *slog.Logger) (serverSubscriptionNative, error)

type serverCodexRuntime struct {
	*codex.Client
	home      string
	original  os.FileInfo
	owner     domain.ID
	logger    *slog.Logger
	guard     *http.Server
	guardDone chan struct{}
}

func openServerSubscription(ctx context.Context, root string, owner domain.ID, bundle []byte, logger *slog.Logger) (returned serverSubscriptionNative, err error) {
	version, phase := "", domain.CodexRuntime
	defer func() { err = domain.WithCodexDiagnostic(version, phase, err) }()
	runtimeRoot := filepath.Join(root, "subscription-runtime")
	home := filepath.Join(runtimeRoot, "auth", string(owner))
	if _, e := os.Lstat(home); !errors.Is(e, os.ErrNotExist) {
		return nil, subscriptionDenied()
	}
	env, e := harness.PrivateRuntimeEnvironment(home)
	if e != nil {
		return nil, subscriptionDenied()
	}
	info, e := security.StableStat(home)
	if e != nil {
		return nil, subscriptionDenied()
	}
	defer func() {
		if err != nil && domain.SafeError(err).Code != domain.RecoveryRequired {
			if cleanupErr := subscription.CleanupRuntime(home, info); cleanupErr != nil {
				logSubscriptionRuntimeCleanup(logger, owner, cleanupErr)
				err = domain.CodexRecoveryFailure(version, phase, err, subscriptionDenied())
			}
		}
	}()
	phase = domain.CodexDiscovery
	installation, e := harness.DiscoverCodex(ctx, harness.DiscoveryConfig{Root: runtimeRoot, OwnerID: owner, Logger: logger})
	if e != nil {
		return nil, e
	}
	version = installation.Version
	if installation.State != domain.InstallationDetected {
		if installation.State == domain.InstallationIncompatible || installation.State == domain.InstallationFailed {
			phase = domain.CodexVersion
		}
		return nil, domain.InstallationProblem(installation.State)
	}
	if !domain.CodexVersionAllowed(version) {
		return nil, domain.CodexVersionFailure(version)
	}
	if !installation.ProtocolVerified || installation.ExecutableSHA256 == "" {
		if installation.Protocol != nil && installation.Protocol.Diagnostic != nil {
			return nil, domain.RestoreCodexDiagnostic(*installation.Protocol.Diagnostic)
		}
		phase = domain.CodexProfile
		return nil, domain.Fail(domain.Unsupported, "The installed Codex protocol is unavailable.", "Refresh native discovery.")
	}
	phase = domain.CodexProfile
	digest, e := harness.InspectExecutable(ctx, installation.ResolvedPath)
	if e != nil || digest != installation.ExecutableSHA256 {
		return nil, subscriptionDenied()
	}
	nativeHome := filepath.Join(home, "codex")
	if len(bundle) != 0 {
		if _, _, e := subscription.Parse(bundle); e != nil {
			return nil, e
		}
		if e := security.WriteAtomic(filepath.Join(nativeHome, "auth.json"), bundle); e != nil {
			return nil, subscriptionDenied()
		}
	}
	native, e := codex.Open(ctx, codex.Config{Version: installation.Version, Mode: codex.SubscriptionProtocol, ManagedAuthentication: true, Home: nativeHome, Process: process.Config{Directory: filepath.Join(runtimeRoot, "processes"), OwnerID: owner, Executable: installation.ResolvedPath, Cwd: home, Env: env, Logger: logger}})
	if e != nil {
		return nil, e
	}
	return &serverCodexRuntime{Client: native, home: home, original: info, owner: owner, logger: logger}, nil
}

func (r *serverCodexRuntime) StartManagedLogin(ctx context.Context, device bool) (codex.ManagedLoginProgress, error) {
	if !device {
		// Codex 0.151.0 sends /cancel to an occupied preferred port before using
		// its registered fallback port. Own that preferred listener throughout
		// login so the adapter cannot cancel somebody else's login. The fallback
		// never sends /cancel. Remove this guard when the pinned native protocol
		// permits an exclusively owned callback port without foreign cancellation.
		listener, err := net.Listen("tcp4", "127.0.0.1:1455")
		if err != nil {
			return codex.ManagedLoginProgress{}, domain.Fail(domain.Unavailable, "Another browser login is using the server's login port.", "Finish that login before starting another; no existing login was canceled.")
		}
		r.guard = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusNotFound)
		}), ReadHeaderTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, IdleTimeout: time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
		r.guardDone = make(chan struct{})
		go func() { defer close(r.guardDone); _ = r.guard.Serve(listener) }()
	}
	return r.Client.StartManagedLogin(ctx, device)
}

func (r *serverCodexRuntime) Close(latest []byte) error {
	err := r.Client.Close()
	if r.guard != nil {
		_ = r.guard.Close()
		<-r.guardDone
	}
	if err != nil {
		logSubscriptionCleanup(r.logger, r.owner, subscription.CleanupNativeProcess, subscription.CleanupUnconfirmed, 0, 0)
		return subscriptionDenied()
	}
	return cleanupServerSubscriptionRuntime(r.home, r.original, r.owner, r.logger, latest)
}

// The caller has already joined the original native process and descendants.
func cleanupServerSubscriptionRuntime(home string, original os.FileInfo, owner domain.ID, logger *slog.Logger, latest []byte) error {
	if len(latest) > 0 {
		closed, readErr := security.ReadPrivate(filepath.Join(home, "codex", "auth.json"), subscription.MaxBundle)
		equal := readErr == nil && bytes.Equal(closed, latest)
		clear(closed)
		if !equal {
			reason := subscription.CleanupMismatch
			if readErr != nil {
				reason = subscription.CleanupReadFailed
			}
			logSubscriptionCleanup(logger, owner, subscription.CleanupAuthFile, reason, 0, 0)
			return subscriptionDenied()
		}
	}
	err := subscription.CleanupRuntime(home, original)
	logSubscriptionRuntimeCleanup(logger, owner, err)
	return err
}

func logSubscriptionRuntimeCleanup(logger *slog.Logger, owner domain.ID, err error) {
	var failure *subscription.RuntimeCleanupError
	if errors.As(err, &failure) {
		logSubscriptionCleanup(logger, owner, failure.Stage, failure.Reason, failure.FileCount, failure.Bytes)
	}
}

func logSubscriptionCleanup(logger *slog.Logger, owner domain.ID, stage subscription.CleanupStage, reason subscription.CleanupReason, count int, total int64) {
	if logger != nil {
		logger.Warn("server_subscription_cleanup_failed", "operation_id", owner, "stage", stage, "reason", reason,
			"file_count", count, "observed_bytes", total, "file_limit", subscription.RuntimeFileLimit,
			"byte_limit", subscription.RuntimeByteLimit, "code", domain.RecoveryRequired)
	}
}
