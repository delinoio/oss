// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// managedTitleIntent is the durable one-time send intent. It is written before
// protected credentials are requested, so a crash, lost response or reconnect
// can never lead to a second native request or a second credential delivery.
type managedTitleIntent struct {
	Version         uint32    `json:"version"`
	JobID           domain.ID `json:"job_id"`
	OperationID     domain.ID `json:"operation_id"`
	ThreadRequestID domain.ID `json:"thread_request_id"`
	InputID         domain.ID `json:"input_id"`
	TurnRequestID   domain.ID `json:"turn_request_id"`
}

// executeManagedSessionTitle runs one automatic title with the original
// protected ChatGPT account. It uses the existing EXECUTE lease, a fresh private
// retry-free no-tools native profile and no API credential or relay. The
// protected Finish completes before the title can be reported: the final
// authentication bundle is captured, the native tree joined, the auth file
// compared and removed, processes reconciled and the private runtime removed.
func executeManagedSessionTitle(ctx context.Context, config Config, jobID domain.ID, job domain.Job, input domain.AuxiliaryTitleInput) (output json.RawMessage, returned error) {
	connection := config.execution
	if err := resolveStartupTitleExecutable(ctx, config, &input); err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(input.Executable)
	if err != nil || resolved != input.Executable || !filepath.IsAbs(input.Executable) {
		return nil, domain.Fail(domain.RecoveryRequired, "The frozen Codex executable identity changed after profile verification.", "Preserve the title operation and refresh native installation evidence before another session.")
	}
	journal, err := createTitleJournal(config.Root, jobID)
	if err != nil {
		return nil, err
	}
	profile, err := titleProfile(ctx, config, input)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := profile.Close(); err != nil && returned == nil {
			output, returned = nil, publicationUncertain()
		}
	}()
	cleanup := newTitleFinalizer(journal, jobID, config.Logger)
	var (
		managed                                       *managedSubscriptionLease
		closeManagedRPC                               func()
		managedLatest                                 []byte
		managedCleanup, managedSuccess, managedUnused bool
		managedPreNative, managedHelperRetained       bool
		nativeHome                                    string
	)
	// One deferred owner finalizes native, process, runtime and protected
	// credential state for every outcome. Pre-native authentication cleanup must
	// run before the runtime directory is removed.
	defer func() {
		if closeManagedRPC != nil {
			defer closeManagedRPC()
		}
		if managed != nil && managedPreNative {
			managedCleanup, _ = cleanupPreNativeAuthentication(nativeHome, managed.response.Bundle, managedHelperRetained)
			if managedCleanup {
				managedLatest = bytes.Clone(managed.response.Bundle)
				managedUnused = true
			}
		}
		if err := cleanup.run(); err != nil {
			managedCleanup = false
			output, returned = nil, err
		}
		if managed == nil {
			return
		}
		conclusive := managedCleanup && (managedSuccess || managedUnused)
		if err := managed.finish(managedLatest, managedCleanup, false, managedSuccess); err != nil {
			output, returned = nil, &managedExecutionUncertain{err}
		} else if !conclusive {
			if returned == nil {
				returned = subscription.Invalid()
			}
			output, returned = nil, &managedExecutionUncertain{returned}
		}
		clear(managed.response.Bundle)
		if config.Logger != nil {
			config.Logger.InfoContext(ctx, "managed automatic title protected finish", "job_id", jobID, "cleanup_confirmed", managedCleanup, "authentication_captured", managedSuccess, "unused_original", managedUnused)
		}
	}()
	home := profile.Root
	processRoot := filepath.Join(config.Root, "processes")
	workdir := filepath.Join(home, "work")
	if err := security.PrivateDir(workdir); err != nil {
		return nil, err
	}
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return nil, err
	}
	nativeHome = filepath.Join(home, "codex")
	cleanup.reconcile = func() error { return process.ReconcileOwner(processRoot, jobID) }
	if err := validateManagedAuthenticationHome(nativeHome, []string{workdir}); err != nil {
		return nil, err
	}
	intent := managedTitleIntent{Version: 1, JobID: jobID, OperationID: input.OperationID, ThreadRequestID: domain.NewID(), InputID: domain.NewID(), TurnRequestID: domain.NewID()}
	intentBytes, err := json.Marshal(intent)
	if err != nil || security.WriteAtomic(filepath.Join(journal, "title-intent.json"), intentBytes) != nil {
		return nil, publicationUncertain()
	}
	client, closeRPC, err := subscriptionRPC(ctx, config, connection.Credential)
	if err != nil {
		return nil, err
	}
	closeManagedRPC = closeRPC
	managed, err = takeManagedSubscription(ctx, config, client, connection.Credential, connection.Instance, input.AccountID, jobID, connection.Assignment.Revision, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE)
	if err != nil {
		return nil, err
	}
	if _, _, err := subscription.Parse(managed.response.Bundle); err != nil {
		return nil, err
	}
	// Install pre-native cleanup before the atomic write: even a synchronization
	// error may leave the owned plaintext file committed.
	managedPreNative = true
	if err := managed.recordAuthenticationHome(nativeHome); err != nil {
		return nil, err
	}
	if err := subscription.WriteCredentialFile(filepath.Join(nativeHome, "auth.json"), managed.response.Bundle); err != nil {
		return nil, subscription.Invalid()
	}
	nativeConfig := codex.Config{
		Mode: codex.ThreadProtocol, Version: input.NativeVersion, Home: nativeHome,
		ManagedAuthentication: true, ManagedTitle: true,
		Process: process.Config{Directory: processRoot, OwnerID: jobID, Executable: resolved, Cwd: workdir, Env: env, Logger: config.Logger},
	}
	native, err := codex.Open(ctx, nativeConfig)
	if err != nil {
		managedHelperRetained = codex.NativeJoinedHelperRetirementFailed(err)
		managedPreNative = managedHelperRetained || domain.SafeError(err).Code != domain.RecoveryRequired
		return nil, err
	}
	managedPreNative = false
	captured, closed := false, false
	cleanup.closeNative = func() error {
		if closed {
			return nil
		}
		if !captured {
			captured = true
			bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
			latest, bundleErr := native.ManagedBundle(bounded, false)
			stop()
			managedLatest, managedSuccess = latest, bundleErr == nil
		}
		nativeErr, helperErr := native.CloseWithExecutionHelpers()
		if nativeErr != nil {
			return errors.Join(nativeErr, helperErr)
		}
		closed = true
		authErr := managed.cleanupAuthentication(nativeHome, managedLatest, managed.response.Bundle)
		managedCleanup = helperErr == nil && authErr == nil
		logExecutionAuthenticationCleanup(config.Logger, jobID, authErr)
		if helperErr != nil {
			return helperErr
		}
		if !managedCleanup || !managedSuccess {
			return subscription.Invalid()
		}
		return nil
	}
	settings := codex.ThreadSettings{
		Model: input.NativeModel, Provider: codex.ManagedTitleProvider, Effort: input.Effort, Cwd: workdir,
		Instructions: domain.AutomaticTitleInstructions,
		Options:      domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: string(codex.ApprovalNever), ServiceTier: input.ServiceTier},
	}
	thread, err := native.StartThread(ctx, intent.ThreadRequestID, settings)
	if err != nil || thread.Thread == nil || thread.Thread.ID.Validate() != nil || thread.Effective == nil || thread.Effective.Model != input.NativeModel || thread.Effective.Provider != codex.ManagedTitleProvider || !matchesTitleOption(thread.Effective.Effort, input.Effort) || !matchesTitleOption(thread.Effective.ServiceTier, input.ServiceTier) || thread.Effective.Cwd != workdir || thread.Effective.ApprovalPolicy != codex.ApprovalNever || thread.Effective.Sandbox.Type != codex.ReadOnly {
		if err != nil {
			return nil, err
		}
		return nil, publicationUncertain()
	}
	inferenceCtx, cancelInference := context.WithTimeout(ctx, 30*time.Second)
	defer cancelInference()
	turn, err := native.StartTurn(inferenceCtx, intent.TurnRequestID, intent.InputID, domain.SessionInput{Prompt: input.Prompt, Mode: domain.ExecuteMode})
	if err != nil {
		return nil, err
	}
	if turn.TurnID.Validate() != nil {
		return nil, publicationUncertain()
	}
	response, err := readTitleResponse(inferenceCtx, ctx, config, native, jobID, input, titleIntent{InputID: intent.InputID}, thread, turn)
	if err != nil {
		return nil, err
	}
	// Confirm native shutdown, final bundle capture, file comparison, plaintext
	// removal, process reconciliation and runtime removal before any success.
	if err := cleanup.run(); err != nil {
		return nil, err
	}
	var usageRecord *domain.ResponseUsageRecord
	if response.Usage != nil {
		usageRecord = &domain.ResponseUsageRecord{
			Purpose: domain.SessionTitleUsage, SessionID: input.SessionID, ProjectID: input.ProjectID,
			ExecutionID: input.OriginalExecutionID, AccountID: input.AccountID, ConnectionID: input.ConnectionID,
			SubscriptionService: domain.SubscriptionChatGPT, ModelID: input.ModelID, Harness: domain.Codex, Version: input.NativeVersion,
			ThreadID: string(thread.Thread.ID), TurnID: string(turn.TurnID), Sequence: 1, Usage: *response.Usage,
		}
	}
	result := domain.AuxiliaryTitleResult{Version: 2, OperationID: input.OperationID, NameGeneration: input.NameGeneration, Title: response.Title, CleanupVerified: true, ManagedFinish: managed.finishID, UsageRecord: usageRecord}
	if err := result.Validate(input); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, publicationUncertain()
	}
	if config.Logger != nil {
		config.Logger.InfoContext(ctx, "managed automatic title inference completed", "job_id", jobID, "operation_id", input.OperationID, "session_id", input.SessionID, "usage_observed", usageRecord != nil)
	}
	return encoded, nil
}
