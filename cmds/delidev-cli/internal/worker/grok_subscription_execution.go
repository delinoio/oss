// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// Grok subscription execution. The Worker Takes the account's exclusive execute
// lease through protected Take, keeps the sealed envelope in memory until the
// harness asks for the credential (after every native inspection of the empty
// private profile and immediately before the native process starts), writes
// $GROK_HOME/auth.json with owner-only permission through the bound home, runs
// the existing session code, and on every exit path (success, Stop, crash,
// timeout) reads the possibly refreshed credential, retires auth.json and
// auth.json.lock and only then releases the lease through Finish. Logs use the
// closed phase EXECUTION and never carry credentials, paths or identity.

// grokSubscriptionRun is the lease and credential custody of one execution.
type grokSubscriptionRun struct {
	lease     *managedSubscriptionLease
	closeHTTP func()
	logger    *slog.Logger
	owner     domain.ID
	processes string
	original  subscription.GrokBundle
	attempted bool
	// installation is the verified observation the execute Take authorized. It is
	// the only installation the lease may launch: discovery can replace the
	// job's frozen observation between dispatch and Take.
	installation domain.Installation
	// launchPath and launchInfo are the private verified snapshot of the
	// executable that every native run of this execution launches (see
	// bindLaunch); empty until it is bound.
	launchPath string
	launchInfo os.FileInfo
}

// authorizedGrokInstallation decodes and validates the installation returned by
// the execute Take: a Grok Build installation at or above the subscription
// baseline with an absolute path and the SHA-256 identity of the executable the
// server admitted. The identity is what the lease authorizes: the Worker may
// launch only bytes that hash to it, so an observation without one is refused.
func authorizedGrokInstallation(raw []byte) (domain.Installation, error) {
	var installation domain.Installation
	if domain.Decode(raw, &installation) != nil || installation.Harness != domain.GrokBuild || !domain.GrokSubscriptionVersionAllowed(installation.Version) || !filepath.IsAbs(installation.ResolvedPath) || !domain.ValidExecutableDigest(installation.ExecutableSHA256) {
		return domain.Installation{}, subscription.Invalid()
	}
	return installation, nil
}

// executable returns the authorized installation's executable after proving that
// its path is its own canonical identity (no symlink swap since Take).
func (r *grokSubscriptionRun) executable() (string, string, error) {
	resolved, err := filepath.EvalSymlinks(r.installation.ResolvedPath)
	if err != nil || resolved != r.installation.ResolvedPath {
		return "", "", domain.Fail(domain.RecoveryRequired, "The authorized native executable identity changed.", "Refresh Worker discovery; no PATH fallback is used.")
	}
	return resolved, r.installation.Version, nil
}

// verifiedExecutable is executable plus proof that the bytes behind the path are
// the ones the Take authorized: the current contents must hash to the digest of
// the admitted observation. Bytes replaced at the same canonical path after
// discovery or Take fail closed; the current contents are never adopted as the
// authorized identity.
func (r *grokSubscriptionRun) verifiedExecutable(ctx context.Context) (string, string, error) {
	executable, version, err := r.executable()
	if err != nil {
		return "", "", err
	}
	digest, err := harness.InspectExecutable(ctx, executable)
	if err != nil {
		return "", "", domain.Fail(domain.RecoveryRequired, "The authorized native executable cannot be verified.", "Refresh Worker discovery; no PATH fallback is used.")
	}
	if digest != r.installation.ExecutableSHA256 {
		return "", "", domain.Fail(domain.RecoveryRequired, "The authorized native executable changed since it was admitted.", "Refresh Worker discovery; the credential is not delivered to different bytes.")
	}
	return executable, version, nil
}

// launchTarget returns the executable and version the lease authorized and
// moves the startup evidence to it. The evidence (the executable the ready
// report inspects, its digest and the journal a continuation resolves) must
// describe the executable that is launched, not the one resolved before the
// Take; when the identity cannot be proven, or the bytes differ from the digest
// the Take admitted, the execution fails closed. The check holds for a job with
// no direct startup evidence too, because the credential reaches the executable
// either way.
func (r *grokSubscriptionRun) launchTarget(ctx context.Context, config Config, owner domain.ID, input domain.ExecutionJobInput) (string, string, domain.Installation, error) {
	executable, version, err := r.verifiedExecutable(ctx)
	if err != nil {
		return "", "", input.Installation, err
	}
	installation, err := config.startup.rebindExecutable(ctx, config, owner, input, executable, version, r.installation.ExecutableSHA256)
	if err != nil {
		return "", "", input.Installation, err
	}
	return executable, version, installation, nil
}

// grokHomeIdentity reads the stable identity of a bound home. Tests replace it to
// model a platform that cannot establish one.
var grokHomeIdentity = func(home subscription.GrokHome) string { return home.Identity() }

// beginGrokSubscriptionExecution Takes the execute lease for the claimed job and
// binds the private native home. Nothing is written to the home yet. A failure
// after the Take releases the lease; if that Finish is not acknowledged the
// ownership is uncertain, never an ordinary failed job.
func beginGrokSubscriptionExecution(ctx context.Context, config Config, owner domain.ID, input domain.ExecutionJobInput, grokHome string) (*grokSubscriptionRun, error) {
	connection := config.execution
	// The private home is bound, and its stable identity journaled with the
	// durable claim, before the server is asked for the lease. A home that is
	// unsafe, replaced or cannot be identified is refused here, so no credential
	// crosses the Worker boundary and no lease needs a Finish for a precondition
	// the Worker already knew was invalid.
	home, err := subscription.BindGrokHome(grokHome)
	if err != nil {
		return nil, err
	}
	identity := grokHomeIdentity(home)
	if identity == "" {
		_ = home.Close()
		return nil, domain.Fail(domain.Unavailable, "The Grok private home cannot be identified.", "Repair the Worker's native home directory and retry.")
	}
	// The home must be credential-free before the server is asked for the lease.
	// A leftover auth.json (a crashed earlier run, another writer) is retired
	// here; a home whose leftover cannot be proven retired is refused before any
	// lease exists, so no Finish can later release a lease over a plaintext file.
	if present, err := home.AuthFilePresent(); err != nil || present {
		retired := confirmGrokHomeCredentialFree(home)
		if config.Logger != nil {
			config.Logger.WarnContext(ctx, "grok_subscription_execution_leftover_credential", "phase", "EXECUTION", "step", "home", "retired", retired)
		}
		if !retired {
			_ = home.Close()
			return nil, domain.Fail(domain.Unavailable, "The Grok private home holds a credential that could not be retired.", "Repair the Worker's native home directory and retry.")
		}
	}
	client, closeHTTP, err := subscriptionRPC(ctx, config, connection.Credential)
	if err != nil {
		_ = home.Close()
		return nil, err
	}
	// The home path is retained so the original Worker can retire a credential it
	// left behind, even after a restart.
	lease, release, err := takeManagedSubscriptionClaimed(ctx, config, client, connection.Credential, connection.Instance, input.AccountID, owner, connection.Assignment.Revision, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE, nil, func(j *managedSubscriptionJournal) {
		j.GrokHome, j.GrokHomeIdentity = grokHome, identity
	})
	release()
	if err != nil {
		_ = home.Close()
		closeHTTP()
		return nil, err
	}
	lease.grokHome = home
	run := &grokSubscriptionRun{lease: lease, closeHTTP: closeHTTP, logger: config.Logger, owner: owner, processes: filepath.Join(config.Root, "processes")}
	abort := func(cause error) (*grokSubscriptionRun, error) {
		clear(lease.response.Bundle)
		if err := run.release(true); err != nil {
			return nil, err
		}
		return nil, cause
	}
	if len(lease.response.Bundle) == 0 || domain.ID(lease.response.GenerationId).Validate() != nil {
		// An execute lease always carries the current generation.
		return abort(subscription.Invalid())
	}
	run.installation, err = authorizedGrokInstallation(lease.response.InstallationJson)
	if err != nil {
		return abort(err)
	}
	if config.Logger != nil {
		config.Logger.InfoContext(ctx, "grok_subscription_execution_leased", "phase", "EXECUTION", "step", "lease", "account_id", input.AccountID, "lease_id", lease.response.LeaseId)
	}
	return run, nil
}

// prepare is the harness credential callback. The journal records that a
// credential may exist before it is written, so recovery after a crash retires it.
func (r *grokSubscriptionRun) prepare(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	// The owner's process index is retained before the journal says a native
	// process may exist and before any credential is written. A later missing
	// index is then never completion proof: only an index that is present and
	// reconciles empty proves the owned process is gone.
	// The last gate before the credential can exist: the executable the native
	// process is about to start must still be the one the Take authorized, so
	// bytes replaced since the launch target was verified never receive it.
	if err := r.verifyLaunch(ctx); err != nil {
		return nil, err
	}
	owner := filepath.Join(r.processes, string(r.owner))
	if err := security.PrivateDir(owner); err != nil {
		return nil, domain.SafeError(err)
	}
	if err := security.SyncParent(owner); err != nil {
		return nil, domain.SafeError(err)
	}
	r.lease.journal.ExecutionStarted = true
	if err := writeJSON(r.lease.journalPath, r.lease.journal); err != nil {
		return nil, subscription.Invalid()
	}
	r.attempted = true
	original, err := r.lease.materializeGrokBound(r.lease.grokHome)
	if err != nil {
		return nil, err
	}
	r.original = original
	if r.logger != nil {
		r.logger.InfoContext(ctx, "grok_subscription_execution_credential_ready", "phase", "EXECUTION", "step", "credential", "account_id", r.lease.account, "lease_id", r.lease.response.LeaseId)
	}
	return original.ProtectedValues(), nil
}

// finish joins the native owner, retires the credential and releases the lease.
// joined is the caller's own evidence that the native process is gone; the
// process index is consulted again so a process the caller lost track of can
// never be assumed gone.
func (r *grokSubscriptionRun) finish(joined bool) error {
	defer r.closeHTTP()
	if r.attempted {
		bounded, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		joined = joined && grokNativeJoined(bounded, r.processes, r.owner, true)
		cancel()
	}
	// The snapshot is not a credential; remove it once nothing can still run it.
	if joined || !r.attempted {
		defer r.removeLaunch()
	}
	original := r.original
	r.original = subscription.GrokBundle{}
	return r.lease.finishGrokExecution(original, r.attempted, joined)
}

// release is finish for a caller that reports through the job runner: a Finish
// that is not acknowledged leaves the execute lease possibly held, which is
// execution uncertainty (the primary lane closes and ownership is reconciled),
// never an ordinary failed job.
func (r *grokSubscriptionRun) release(joined bool) error {
	if err := r.finish(joined); err != nil {
		return &managedExecutionUncertain{err}
	}
	return nil
}

// grokNativeJoined proves that no native process owned by owner remains. When no
// native process could have started (the journal never recorded the start of
// the execution) there is nothing to join. Otherwise the owner's retained
// process index must exist and reconcile: a missing index is never completion
// proof, because it may have been removed while the child is still alive.
func grokNativeJoined(ctx context.Context, processes string, owner domain.ID, started bool) bool {
	if !started {
		return true
	}
	return process.ReconcileOwnerContext(ctx, processes, owner) == nil
}

// runGrokRecovery is the original Worker's cleanup of an execute lease that the
// server fenced (a server restart, a lost instance or a failed Finish). It
// consumes the local claim only: it joins the owned native process, retires any
// credential left in the recorded private home and reports confirmed cleanup
// with no success and no credential. It never retakes a lease, never starts a
// native process and never replays input.
func runGrokRecovery(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, accountID domain.ID, account domain.Account) error {
	state := account.Subscription
	if state == nil || !state.RecoveryRequired || state.Lease == nil || state.Lease.Action != domain.SubscriptionExecute || state.Lease.Shared || state.Lease.MachineID != credential.MachineID {
		return subscription.Invalid()
	}
	original := state.Lease
	path := filepath.Join(config.Root, "managed-auth", string(original.ID)+".json")
	var journal managedSubscriptionJournal
	raw, err := security.ReadPrivate(path, 16<<10)
	if err != nil || domain.Decode(raw, &journal) != nil || journal.Version != 1 || journal.Lease != original.ID || journal.Account != accountID || journal.Operation != original.OperationID || journal.Instance != original.InstanceID || journal.Action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE || journal.Finish.Validate() != nil {
		return subscription.Invalid()
	}
	if journal.State == managedReported {
		// The Finish already settled; the server fenced a different step.
		return subscription.Invalid()
	}
	processes := filepath.Join(config.Root, "processes")
	if !grokNativeJoined(ctx, processes, journal.Operation, journal.ExecutionStarted) {
		return domain.Fail(domain.RecoveryRequired, "The original Grok process could not be joined.", "Retain the lease until the original Worker confirms process exit.")
	}
	if journal.CleanupFinish == "" {
		journal.CleanupFinish = domain.NewID()
		if err := writeJSON(path, journal); err != nil {
			return subscription.Invalid()
		}
	}
	cleaned := true
	if journal.GrokHome != "" {
		if _, statErr := os.Lstat(journal.GrokHome); errors.Is(statErr, os.ErrNotExist) {
			// A missing home proves the credential gone only when none was ever
			// written or the Worker already retired it: the directory may instead
			// have been renamed away with the plaintext still inside.
			cleaned = !journal.ExecutionStarted || journal.GrokRetired
		} else {
			cleaned = retireRecordedGrokHome(journal.GrokHome, journal.GrokHomeIdentity)
		}
	}
	if !cleaned {
		if config.Logger != nil {
			config.Logger.WarnContext(ctx, "grok_subscription_recovery_cleanup_unconfirmed", "phase", "CLEANUP", "step", "recovery", "account_id", accountID, "lease_id", original.ID)
		}
		return domain.Fail(domain.RecoveryRequired, "The Grok credential cleanup could not be confirmed.", "Retain the lease until the original Worker confirms cleanup.")
	}
	l := &managedSubscriptionLease{logger: config.Logger, client: client, credential: credential, instance: journal.Instance, account: accountID, response: &pb.TakeSubscriptionResponse{LeaseId: string(original.ID), GenerationId: string(journal.Generation), LeaseRevision: journal.LeaseRevision}, finishID: journal.CleanupFinish, journalPath: path, journal: journal}
	if err := l.finish(nil, true, false, false); err != nil {
		return err
	}
	if config.Logger != nil {
		config.Logger.InfoContext(ctx, "grok_subscription_recovery_cleanup_finished", "phase", "CLEANUP", "step", "recovery", "account_id", accountID, "lease_id", original.ID)
	}
	return nil
}

// retireRecordedGrokHome retires auth.json, its lock file and attributed
// temporaries from a recorded private home (identity is the journaled identity of
// the directory the lease bound) and reports whether the cleanup is
// confirmed. A home that cannot be bound safely is never confirmed.
func retireRecordedGrokHome(grokHome, identity string) bool {
	home, err := subscription.BindGrokHome(grokHome)
	if err != nil {
		return false
	}
	defer home.Close()
	// A directory at the recorded path that is not the one the lease bound may be a
	// successor of a renamed original; cleaning it proves nothing about the
	// original, so cleanup stays unconfirmed. Without a journaled identity there is
	// nothing to compare, which is treated the same way.
	if identity == "" || home.Identity() != identity {
		return false
	}
	if home.Verify() != nil || home.RetireAuthFile() != nil {
		return false
	}
	present, err := home.AuthFilePresent()
	return err == nil && !present && home.Verify() == nil
}

// grokRecoveryAccount reports whether the server fenced a Grok execute lease
// that the watching Worker may have to clean up.
func grokRecoveryAccount(account domain.Account) bool {
	state := account.Subscription
	return account.SubscriptionService == domain.SubscriptionGrok && state != nil && state.RecoveryRequired && state.Lease != nil && state.Lease.Action == domain.SubscriptionExecute && !state.Lease.Shared
}
