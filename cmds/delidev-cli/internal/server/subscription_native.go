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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"time"
)

type serverSubscriptionNative interface {
	ConsumeServerResetCredit(context.Context, domain.ServerCreditOperation) (domain.SubscriptionResetOutcome, error)
	ReadManagedQuota(context.Context, domain.ID) (domain.SubscriptionQuotaObservation, error)
	Version() string
	StartManagedLogin(context.Context, bool) (codex.ManagedLoginProgress, error)
	WaitManagedLogin(context.Context, string) error
	CancelManagedLogin(context.Context, string) error
	ManagedBundle(context.Context, bool) ([]byte, error)
	LogoutManaged(context.Context) error
	Close([]byte) error
}

// An opener error proves the native process was joined. Account-owned
// credential files and the persistent profile are retained by every outcome;
// only explicit logout or account deletion removes them.
type serverSubscriptionOpener func(context.Context, nativeScope, domain.ID, []byte, *slog.Logger) (serverSubscriptionNative, error)

type serverCodexRuntime struct {
	*codex.Client
	home      string
	lease     *nativeprofile.Lease
	owner     domain.ID
	logger    *slog.Logger
	guard     *http.Server
	guardDone chan struct{}
	loggedOut bool
}

// LogoutManaged runs the native logout; the explicit logout then owns removal
// of every retained credential file once the original process is joined.
func (r *serverCodexRuntime) LogoutManaged(ctx context.Context) error {
	err := r.Client.LogoutManaged(ctx)
	r.loggedOut = err == nil
	return err
}

// acquireAccountProfile resolves the permanent account profile for one purpose.
// A busy or invalid profile blocks with its closed profile cause; it is never
// replaced by a temporary home.
func acquireAccountProfile(ctx context.Context, scope nativeScope, harnessKind domain.Harness, purpose nativeprofile.Purpose) (*nativeprofile.Lease, error) {
	if scope.Profiles == nil || scope.Account.Validate() != nil {
		return nil, subscriptionDenied()
	}
	return scope.Profiles.Acquire(ctx, nativeprofile.Key{Harness: harnessKind, Purpose: purpose, Account: scope.Account})
}

// profileCredentialBlock rejects unsafe credential paths in a retained profile.
// A retained regular credential file in the original account profile is
// allowed (forbidManaged false): it grants no authority, and the next operation
// rewrites it from the currently authorized vault generation. An unsafe entry
// such as a symlink always blocks.
func profileCredentialBlock(scope nativeScope, lease *nativeprofile.Lease, owner domain.ID, logger *slog.Logger, forbidManaged bool) error {
	present, err := subscription.CredentialFilePresent(lease.Root)
	if err != nil || present && forbidManaged {
		nativeprofile.Log(logger, "native_profile_blocked", lease.Key, lease.ID, nativeprofile.ReasonCredentialPresent, "operation_id", owner)
		return nativeprofile.Failure(domain.RecoveryRequired, nativeprofile.ReasonCredentialPresent, "A transient credential file remains in the retained native profile.", "Reconcile the original operation before retrying.")
	}
	return nil
}

func openServerSubscription(ctx context.Context, scope nativeScope, owner domain.ID, bundle []byte, logger *slog.Logger) (returned serverSubscriptionNative, err error) {
	version, phase := "", domain.CodexRuntime
	defer func() { err = domain.WithCodexDiagnostic(version, phase, err) }()
	runtimeRoot := filepath.Join(scope.Root, "subscription-runtime")
	lease, e := acquireAccountProfile(ctx, scope, domain.Codex, nativeprofile.PurposeAuth)
	if e != nil {
		return nil, e
	}
	home := lease.Root
	released := false
	defer func() {
		if err != nil && !released {
			// Credential files, history and caches in the account profile stay.
			if logger != nil {
				logger.Info("server_subscription_credentials_retained", "operation_id", owner, "reason", "opener_failed")
			}
			_ = lease.Close()
		}
	}()
	// A retained regular credential file is allowed; unsafe entries block.
	if e := profileCredentialBlock(scope, lease, owner, logger, false); e != nil {
		return nil, e
	}
	env, e := harness.PrivateRuntimeEnvironment(home)
	if e != nil {
		return nil, subscriptionDenied()
	}
	phase = domain.CodexDiscovery
	installation, e := harness.DiscoverCodex(ctx, harness.DiscoveryConfig{Root: runtimeRoot, OwnerID: owner, Logger: logger, Profiles: scope.Profiles})
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
		if e := subscription.WriteCredentialFile(filepath.Join(nativeHome, "auth.json"), bundle); e != nil {
			return nil, subscriptionDenied()
		}
	}
	native, e := codex.Open(ctx, codex.Config{Version: installation.Version, Mode: codex.SubscriptionProtocol, ManagedAuthentication: true, Home: nativeHome, Process: process.Config{Directory: filepath.Join(runtimeRoot, "processes"), OwnerID: owner, Executable: installation.ResolvedPath, Cwd: home, Env: env, Logger: logger}})
	if e != nil {
		return nil, e
	}
	released = true
	return &serverCodexRuntime{Client: native, home: home, lease: lease, owner: owner, logger: logger}, nil
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
	defer r.lease.Close()
	if err != nil {
		logSubscriptionCleanup(r.logger, r.owner, subscription.CleanupNativeProcess, subscription.CleanupUnconfirmed, 0, 0)
		return subscriptionDenied()
	}
	if r.loggedOut {
		// Explicit logout is destructive: remove the account-owned credential files.
		if rerr := subscription.RetireCredentialFiles(r.home); rerr != nil {
			logSubscriptionRuntimeCleanup(r.logger, r.owner, rerr)
			return subscriptionDenied()
		}
		if r.logger != nil {
			r.logger.Info("server_subscription_credentials_removed_by_logout", "operation_id", r.owner)
		}
		return nil
	}
	err = verifyServerSubscriptionCredentials(r.home, r.owner, r.logger, latest)
	if err == nil && r.logger != nil {
		r.logger.Info("server_subscription_credentials_retained", "operation_id", r.owner, "reason", "operation_finished")
	}
	return err
}

// The caller has already joined the original native process and descendants.
// Verification compares the final credential bytes with the latest published
// generation. It never removes anything: the credential file, persistent
// profile, native history, caches and helper files are retained.
func verifyServerSubscriptionCredentials(home string, owner domain.ID, logger *slog.Logger, latest []byte) error {
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
	return nil
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

// Quota-only runtimes accept no managed bundle and cannot access login or
// consumption methods through this interface.
type serverQuotaNative interface {
	ReadExternalQuota(context.Context, domain.ID, codex.QuotaAuthentication) (domain.SubscriptionQuotaObservation, error)
	Close() error
}
type serverQuotaOpener func(context.Context, nativeScope, domain.ID, codex.QuotaAuthentication, *slog.Logger) (serverQuotaNative, error)
type serverQuotaRuntime struct {
	*codex.Client
	home   string
	lease  *nativeprofile.Lease
	owner  domain.ID
	logger *slog.Logger
}

// Close joins the native client and proves the access-only profile still holds
// no managed auth.json. The permanent profile is retained, including any
// native-created helper links, so ordinary retained data never blocks the next
// eligible operation. Only a process or credential uncertainty does.
func (r *serverQuotaRuntime) Close() error {
	defer r.lease.Close()
	nativeErr, helperErr := r.Client.CloseWithExecutionHelpers()
	if nativeErr != nil {
		nativeprofile.Log(r.logger, "native_profile_blocked", r.lease.Key, r.lease.ID, nativeprofile.ReasonProcessUncertain, "operation_id", r.owner)
		return subscriptionDenied()
	}
	if helperErr != nil {
		// A joined native tree that left its temporary helper aliases behind has
		// produced ordinary retained profile data. It neither blocks this
		// operation nor the next one; only process and credential state do.
		nativeprofile.Log(r.logger, "native_profile_retained_data", r.lease.Key, r.lease.ID, nativeprofile.ReasonHelperRetained, "operation_id", r.owner)
	}
	if _, err := os.Lstat(filepath.Join(r.home, "codex", "auth.json")); !os.IsNotExist(err) {
		nativeprofile.Log(r.logger, "native_profile_blocked", r.lease.Key, r.lease.ID, nativeprofile.ReasonCredentialPresent, "operation_id", r.owner)
		return subscriptionDenied()
	}
	return nil
}

func openServerQuota(ctx context.Context, scope nativeScope, owner domain.ID, auth codex.QuotaAuthentication, logger *slog.Logger) (serverQuotaNative, error) {
	runtime, err := openAccessOnlyRuntime(ctx, scope, nativeprofile.PurposeQuota, codex.QuotaProtocol, owner, auth, logger)
	if err != nil {
		return nil, err
	}
	return &serverQuotaRuntime{Client: runtime.Client, home: runtime.home, lease: runtime.lease, owner: owner, logger: logger}, nil
}

// Credit runtimes use the distinct access-only credit profile. They hold no
// managed auth file and can neither renew tokens nor write credentials back.
type serverCreditNative interface {
	ConsumeExternalResetCredit(context.Context, domain.ServerCreditOperation, codex.QuotaAuthentication, func(domain.SubscriptionResetOutcome) error) (domain.SubscriptionResetOutcome, domain.SubscriptionQuotaObservation, error, error)
	Close() error
}
type serverCreditOpener func(context.Context, nativeScope, domain.ID, codex.QuotaAuthentication, *slog.Logger) (serverCreditNative, error)
type serverCreditRuntime struct{ *serverQuotaRuntime }

func openServerCreditAccess(ctx context.Context, scope nativeScope, owner domain.ID, auth codex.QuotaAuthentication, logger *slog.Logger) (serverCreditNative, error) {
	runtime, err := openAccessOnlyRuntime(ctx, scope, nativeprofile.PurposeCredit, codex.CreditProtocol, owner, auth, logger)
	if err != nil {
		return nil, err
	}
	return &serverCreditRuntime{&serverQuotaRuntime{Client: runtime.Client, home: runtime.home, lease: runtime.lease, owner: owner, logger: logger}}, nil
}

// legacyAccessOnlyDirectory is the historical per-operation runtime of one
// access-only owner. New operations never create it; reconciliation only
// inspects it without following links.
func legacyAccessOnlyDirectory(root, kind string, owner domain.ID) string {
	return filepath.Join(root, "subscription-runtime", kind, string(owner))
}

func openAccessOnlyRuntime(ctx context.Context, scope nativeScope, purpose nativeprofile.Purpose, mode codex.ProtocolMode, owner domain.ID, auth codex.QuotaAuthentication, logger *slog.Logger) (returned *serverQuotaRuntime, err error) {
	version, phase := "", domain.CodexRuntime
	defer func() { err = domain.WithCodexDiagnostic(version, phase, err) }()
	runtimeRoot := filepath.Join(scope.Root, "subscription-runtime")
	lease, e := acquireAccountProfile(ctx, scope, domain.Codex, purpose)
	if e != nil {
		return nil, e
	}
	home := lease.Root
	defer func() {
		if err != nil {
			_ = lease.Close()
		}
	}()
	// Access-only profiles never contain managed authentication.
	if present, _ := subscription.CredentialFilePresent(home); present {
		nativeprofile.Log(logger, "native_profile_blocked", lease.Key, lease.ID, nativeprofile.ReasonCredentialPresent, "operation_id", owner)
		return nil, nativeprofile.Failure(domain.RecoveryRequired, nativeprofile.ReasonCredentialPresent, "A managed credential file is present in an access-only native profile.", "Reconcile the original operation before retrying.")
	}
	env, e := harness.PrivateRuntimeEnvironment(home)
	if e != nil {
		return nil, subscriptionDenied()
	}
	phase = domain.CodexDiscovery
	installation, e := harness.DiscoverCodex(ctx, harness.DiscoveryConfig{Root: runtimeRoot, OwnerID: owner, Logger: logger, Profiles: scope.Profiles})
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
	native, e := codex.Open(ctx, codex.Config{Version: installation.Version, Mode: mode, Home: nativeHome, Process: process.Config{Directory: filepath.Join(runtimeRoot, "processes"), OwnerID: owner, Executable: installation.ResolvedPath, Cwd: home, Env: env, ProtectedValues: []string{auth.Access}, Logger: logger}})
	if e != nil {
		return nil, e
	}
	return &serverQuotaRuntime{Client: native, home: home, lease: lease, owner: owner, logger: logger}, nil
}
