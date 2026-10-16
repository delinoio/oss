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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// grokExecutionPhase is the phase every structured record of a managed Grok
// execution carries. Records name the account and lease identifiers only: never
// a credential, a path or an identity value.
const grokExecutionPhase = "EXECUTION"

// grokCleanupRetries bounds the immediate owner retries of an unconfirmed
// private-home cleanup. grokCleanupRetryWait is the pause before each retry; it
// is a variable only so tests do not wait.
const grokCleanupRetries = 3

var grokCleanupRetryWait = time.Second

// grokSubscriptionExecution is one managed Grok execution lease held by this
// Worker. It owns the whole credential lifecycle of a native Grok process:
//
//  1. begin takes the account lease through protected Take. The sealed bundle
//     lives in memory only.
//  2. credentialHook (run once, immediately before the native process starts,
//     after every credential-free inspection) records the private home in the
//     durable journal, then opens the envelope against this server, the account
//     and the delivered generation and writes auth.json (owner-only) plus no other
//     copy into the bound private home.
//  3. finish runs after the native process is joined, on every exit path (success,
//     Stop, crash, timeout): it reads the latest auth.json once, verifies it
//     against the original identity, returns a refreshed bundle through protected
//     Finish, deletes auth.json and auth.json.lock, and only then reports the
//     cleanup that releases the lease.
//
// A native process that was not joined never has its credential read or removed:
// the lease is reported uncleaned, so the server fences the account and the
// original Worker recovers it (runGrokRecovery) once the process is gone.
type grokSubscriptionExecution struct {
	lease     *managedSubscriptionLease
	closeHTTP func()
	logger    *slog.Logger
	account   domain.ID
	original  subscription.GrokBundle
	// attempted is set when materialization started: from then on a credential
	// file may exist in the home, so the release is never the trivial one.
	attempted    bool
	materialized bool
	home         string
	settled      bool
}

func beginGrokSubscriptionExecution(ctx context.Context, config Config, owner domain.ID, input domain.ExecutionJobInput) (*grokSubscriptionExecution, error) {
	connection := config.execution
	if connection == nil || input.Continuation != nil || input.Fork != nil {
		return nil, domain.Fail(domain.Unsupported, "A managed Grok account runs only a first input on its original Worker.", "Retain existing native history; a continuation acquires no resume authority.")
	}
	client, closeHTTP, err := subscriptionRPC(ctx, config, connection.Credential)
	if err != nil {
		return nil, err
	}
	lease, err := takeManagedSubscription(ctx, config, client, connection.Credential, connection.Instance, input.AccountID, owner, connection.Assignment.Revision, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE)
	if err != nil {
		closeHTTP()
		return nil, err
	}
	e := &grokSubscriptionExecution{lease: lease, closeHTTP: closeHTTP, logger: config.Logger, account: input.AccountID}
	// A managed Grok lease always delivers one sealed bundle for a settled
	// generation and never a native profile of the device-owned lanes.
	if len(lease.response.Bundle) == 0 || domain.ID(lease.response.GenerationId).Validate() != nil || lease.response.NativeProfileId != "" || lease.response.OwnerLeaseId != "" {
		clear(lease.response.Bundle)
		lease.response.Bundle = nil
		e.settled = true
		defer closeHTTP()
		if err := lease.finish(nil, false, false, false); err != nil {
			return nil, err
		}
		return nil, &managedReportedFailure{subscription.GrokBundleInvalid()}
	}
	e.log(ctx, slog.LevelInfo, "grok_subscription_execution_started")
	return e, nil
}

func (e *grokSubscriptionExecution) log(ctx context.Context, level slog.Level, message string, attrs ...slog.Attr) {
	if e.logger == nil {
		return
	}
	attrs = append([]slog.Attr{slog.String("phase", grokExecutionPhase), slog.String("account_id", string(e.account)), slog.String("lease_id", e.lease.response.LeaseId)}, attrs...)
	e.logger.LogAttrs(ctx, level, message, attrs...)
}

// credentialHook returns the function the harness runs once, right before it
// launches the native process. The home must be the private $GROK_HOME created
// by the Worker's per-conversation profile; it is bound (opened and validated
// through its handle) before anything is written.
func (e *grokSubscriptionExecution) credentialHook(home string) func() error {
	return func() error {
		if e.attempted {
			return subscription.GrokBundleInvalid()
		}
		e.attempted, e.home = true, home
		// The home is durable before the credential is written, so a crash in the
		// window still recovers the credential in exactly this directory.
		e.lease.journal.GrokHome = home
		if err := writeJSON(e.lease.journalPath, e.lease.journal); err != nil {
			return publicationUncertain()
		}
		bundle, err := e.lease.materializeGrok(home)
		if err != nil {
			return err
		}
		e.original, e.materialized = bundle, true
		e.lease.journal.NativeStarted = true
		if err := writeJSON(e.lease.journalPath, e.lease.journal); err != nil {
			return publicationUncertain()
		}
		e.log(context.Background(), slog.LevelInfo, "grok_subscription_execution_credential_materialized")
		return nil
	}
}

// finish settles the lease. joined says whether the native process is confirmed
// gone; it is false only when its owned scope could not be closed. It returns an
// error when the settlement is not acknowledged or the private cleanup cannot be
// confirmed, which closes the primary lane and leaves the lease fenced.
func (e *grokSubscriptionExecution) finish(joined bool) (returned error) {
	if e.settled {
		return nil
	}
	e.settled = true
	defer e.closeHTTP()
	defer func() { e.original.Clear() }()
	ctx := context.Background()
	switch {
	case !joined:
		// The process may still hold or rewrite the file: nothing is read or
		// removed, and the lease is reported uncleaned.
		_ = e.lease.grokHome.Close()
		e.log(ctx, slog.LevelWarn, "grok_subscription_execution_not_joined")
		return e.lease.finish(nil, false, false, false)
	case !e.attempted:
		// No credential was ever written: the lease is released unchanged.
		e.log(ctx, slog.LevelInfo, "grok_subscription_execution_finished", slog.Bool("credential_written", false))
		return e.lease.finish(nil, true, false, true)
	case !e.materialized:
		// Materialization failed after the home was named. Retire anything that
		// may exist in that directory and report whether the cleanup is proven;
		// success is never claimed, so the account is fenced for a cleanup-only
		// recovery by this same Worker.
		confirmed := retireGrokHome(e.home)
		e.log(ctx, slog.LevelWarn, "grok_subscription_execution_materialization_failed", slog.Bool("cleanup_confirmed", confirmed))
		return e.lease.finish(nil, confirmed, false, false)
	}
	if err := e.lease.finishGrok(e.original, true); err != nil {
		return err
	}
	// Each retry reports the cleanup again with a fresh Finish identity on the
	// same lease owner; the server accepts it from that owner only.
	for attempt := 0; e.lease.grokRetry != nil && attempt < grokCleanupRetries; attempt++ {
		time.Sleep(grokCleanupRetryWait)
		if err := e.lease.retryGrokCleanup(); err != nil {
			return err
		}
	}
	if e.lease.grokRetry != nil {
		e.log(ctx, slog.LevelError, "grok_subscription_execution_cleanup_unconfirmed")
		return &managedExecutionUncertain{domain.Fail(domain.RecoveryRequired, "The private Grok credential cleanup could not be confirmed.", "Retain the private runtime; the original Worker retires the credential on recovery.")}
	}
	e.log(ctx, slog.LevelInfo, "grok_subscription_execution_finished", slog.Bool("credential_written", true))
	return nil
}

// retireGrokHome removes the credential file, its lock and interrupted-write
// temporaries from the named private home through a freshly bound handle and
// reports whether their absence is proven. A home that cannot be bound is
// unconfirmed, including one whose pathname no longer exists: a missing name
// cannot prove that the originally journaled directory, which may still hold
// auth.json, was retired (it can have been renamed away).
func retireGrokHome(path string) bool {
	home, err := subscription.BindGrokHome(path)
	if err != nil {
		return false
	}
	defer home.Close()
	if home.Verify() != nil || home.RetireAuthFile() != nil {
		return false
	}
	present, err := home.AuthFilePresent()
	return err == nil && !present && home.Verify() == nil
}

// recoveredGrokHomeRetired settles the private home a recovery journal named. A
// present home is retired through a bound handle. A missing pathname is clean
// only when the Worker's own profile registry no longer registers that home: an
// explicit session or storage deletion removes the whole profile directory,
// credential included, and its registry entry. A home that the registry still
// names but the filesystem no longer shows was moved away with its credential
// and stays unconfirmed.
func recoveredGrokHomeRetired(config Config, machine domain.ID, path string) bool {
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return retireGrokHome(path)
	}
	registry, err := nativeProfiles(config, machine)
	if err != nil {
		return false
	}
	registered, err := registry.HomeRegistered(path)
	return err == nil && !registered
}

// runGrokRecovery cleans up the private home of this Worker's own interrupted
// Grok execution lease after a server restart, a lost Worker instance or an
// unconfirmed cleanup. It consumes the original journal: it never retakes a lease,
// never starts a native process and never reads or returns the credential. The
// credential file is retired in exactly the directory the journal recorded, and
// the cleanup-only Finish (no success, no bundle) releases the lease on the
// server. A refresh the interrupted process had already written is lost by
// design: without the original in memory its identity cannot be verified.
func runGrokRecovery(ctx context.Context, config Config, client delidevv1connect.SubscriptionServiceClient, credential Credential, accountID domain.ID, account domain.Account) error {
	state := account.Subscription
	if state == nil || !state.RecoveryRequired || state.Lease == nil || state.Lease.Action != domain.SubscriptionExecute || state.Lease.MachineID != credential.MachineID {
		return subscription.Invalid()
	}
	original := state.Lease
	path := filepath.Join(config.Root, "managed-auth", string(original.ID)+".json")
	raw, err := security.ReadPrivate(path, 16<<10)
	var journal managedSubscriptionJournal
	if err != nil || domain.Decode(raw, &journal) != nil || journal.Version != 1 || journal.Lease != original.ID || journal.Account != accountID || journal.Operation != original.OperationID || journal.Instance != original.InstanceID || journal.Finish.Validate() != nil || journal.Action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE {
		return subscription.Invalid()
	}
	// The durable claim precedes the protected delivery. When the Take response was
	// lost, or its fields could not be persisted, the journal still has no
	// generation or lease revision and never named a home or started a native
	// process, while the authenticated server lease carries the settled
	// generation and revision the cleanup-only Finish uses. That exact original
	// claim is accepted; once any field was persisted it must agree with the
	// server lease.
	preDelivery := journal.Generation == "" && journal.LeaseRevision == 0 && journal.GrokHome == "" && !journal.NativeStarted
	if !preDelivery && journal.Generation != original.Generation || journal.LeaseRevision != 0 && journal.LeaseRevision != original.Revision {
		return subscription.Invalid()
	}
	// A lease that never named a home never wrote a credential. Otherwise the
	// native process, supervised under the job identifier, is joined before its home
	// is touched, so a process that is still running can never have its credential
	// removed or race the retirement.
	confirmed := true
	if journal.GrokHome != "" {
		if err := process.ReconcileOwnerContext(ctx, filepath.Join(config.Root, "processes"), original.OperationID); err != nil {
			return err
		}
		confirmed = recoveredGrokHomeRetired(config, credential.MachineID, journal.GrokHome)
	}
	logger := config.Logger
	if !confirmed {
		if logger != nil {
			logger.WarnContext(ctx, "grok_subscription_recovery_cleanup_unconfirmed", "phase", grokExecutionPhase, "account_id", accountID, "lease_id", original.ID)
		}
		return domain.Fail(domain.RecoveryRequired, "The private Grok credential cleanup could not be confirmed.", "Retain the private runtime and retry the recovery.")
	}
	if journal.CleanupFinish == "" {
		journal.CleanupFinish = domain.NewID()
	}
	journal.Cleanup, journal.Success, journal.Refresh, journal.State = true, false, false, managedClosed
	if err := writeJSON(path, journal); err != nil {
		return subscription.Invalid()
	}
	lease := &managedSubscriptionLease{logger: logger, client: client, credential: credential, instance: original.InstanceID, account: accountID, finishID: journal.CleanupFinish, journalPath: path, journal: journal, response: &pb.TakeSubscriptionResponse{LeaseId: string(original.ID), GenerationId: string(original.Generation), LeaseRevision: original.Revision}}
	if logger != nil {
		logger.InfoContext(ctx, "grok_subscription_recovery_cleanup_confirmed", "phase", grokExecutionPhase, "account_id", accountID, "lease_id", original.ID, "pre_delivery", preDelivery)
	}
	return lease.finish(nil, true, false, false)
}
