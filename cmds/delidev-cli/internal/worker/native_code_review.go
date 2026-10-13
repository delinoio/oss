// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type nativeReviewIntent struct {
	Version          uint32    `json:"version"`
	JobID            domain.ID `json:"job_id"`
	ActionID         domain.ID `json:"action_id"`
	SourceJobID      domain.ID `json:"source_job_id"`
	AssignmentDigest string    `json:"assignment_digest"`
	RegistrationID   domain.ID `json:"registration_id"`
	ThreadRequestID  domain.ID `json:"thread_request_id"`
	TokenDigest      string    `json:"token_digest"`
}

func reviewProtectedBundle(raw []byte) ([]string, error) {
	bundle, _, err := subscription.Parse(raw)
	if err != nil {
		return nil, err
	}
	return []string{bundle.Tokens.ID, bundle.Tokens.Access, bundle.Tokens.Refresh, bundle.Tokens.Account}, nil
}

func nativeReviewMayIgnoreMetadata(event codex.Event, threadID, turnID domain.ID) bool {
	if event.Kind != codex.MetadataEvent {
		return false
	}
	if event.Safety != nil {
		return event.Safety.MatchesPassiveEvent(event, threadID, turnID)
	}
	if event.ThreadID != "" && event.ThreadID != threadID || event.TurnID != "" && event.TurnID != turnID {
		return false
	}
	switch event.Metadata {
	case codex.NativeReviewItemPending, codex.ThreadIdentityChecked, codex.ThreadSettingsChecked, codex.RemoteControlDisabled, codex.QuotaUnavailable, codex.RawSupplementDiscarded, codex.NativeGoalAbsent, codex.SkillsChangedDiscarded, codex.ModelVerificationAbsent, codex.CodexAppsStartupObserved, codex.WindowsSandboxSetupDiscarded, codex.WindowsSandboxWarningDiscarded, codex.CurrentTimeReplySent:
		return true
	default:
		return false
	}
}

func executeNativeCodeReview(ctx context.Context, config Config, owner domain.ID, job domain.Job) (output json.RawMessage, returned error) {
	c := config.execution
	var input domain.NativeCodeReviewInput
	if c == nil || c.Client == nil || c.Assignment == nil || config.executionContext == nil || domain.ID(c.Assignment.Id) != owner || domain.DecodeNativeCodeReviewInput(job.Input, &input) != nil || input.Validate() != nil || input.SourceJobID != job.ParentID || input.Source.MachineID != c.Credential.MachineID {
		return nil, domain.NativeCodeReviewUnavailable()
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("job_id", owner, "action_id", input.ActionID, "session_id", input.Source.SessionID, "target_kind", input.Target.Kind)
	nativeSent, nativeJoined, workspaceJoined, authJoined := false, true, true, true
	defer func() {
		if returned != nil && !nativeSent && nativeJoined && workspaceJoined && authJoined {
			proof := domain.NativeCodeReviewRejectedProof{Version: 1, ActionID: input.ActionID, NoSend: true, CleanupVerified: true}
			raw, err := json.Marshal(proof)
			if err == nil {
				output, returned = raw, nil
			}
		}
	}()
	defer func() {
		if returned != nil {
			logger.WarnContext(ctx, "native_code_review_unconfirmed", "code", domain.SafeError(returned).Code)
		}
	}()
	var prep workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(input.Source.Preparation, &prep) != nil || domain.Decode(input.Source.Manifest, &manifest) != nil || workspace.ValidateResult(prep, manifest, runtime.GOOS) != nil {
		return nil, domain.NativeCodeReviewUnavailable()
	}
	installation, err := resolveOriginalStartup(ctx, config, input.SourceJobID, input.Source)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(config.Root, "runtimes")
	if security.PrivateDir(root) != nil {
		return nil, domain.NativeCodeReviewUnavailable()
	}
	home := filepath.Join(root, string(input.ActionID))
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		// Retained original runtime evidence can represent an earlier send.
		// This invocation's absence of sending cannot prove original no-send.
		nativeSent = true
		return nil, publicationUncertain()
	}
	env, err := harness.PrivateRuntimeEnvironment(home)
	if err != nil {
		return nil, err
	}
	nativeHome := filepath.Join(home, "codex")
	if security.PrivateDir(nativeHome) != nil {
		return nil, publicationUncertain()
	}
	token, err := security.RandomToken()
	if err != nil {
		return nil, err
	}
	token = apiproxy.TokenPrefix + token
	digest := sha256.Sum256([]byte(token))
	intent := nativeReviewIntent{Version: 1, JobID: owner, ActionID: input.ActionID, SourceJobID: input.SourceJobID, AssignmentDigest: executionInputDigest(c.Assignment.DocumentJson), RegistrationID: domain.NewID(), ThreadRequestID: domain.NewID(), TokenDigest: hex.EncodeToString(digest[:])}
	jobRoot := filepath.Join(config.Root, "jobs", string(owner))
	if security.PrivateDir(jobRoot) != nil || writeJSON(filepath.Join(jobRoot, "review-intent.json"), intent) != nil {
		return nil, publicationUncertain()
	}
	var managed *managedSubscriptionLease
	var latest []byte
	var closeRPC func()
	managedClosed, managedUnused, managedStarted, managedHelperRetained := false, false, false, false
	protected := []string{token}
	defer func() {
		if closeRPC != nil {
			defer closeRPC()
		}
		if managed == nil {
			return
		}
		if !managedStarted {
			// A joined tree with failed helper retirement still authorizes the
			// independent authentication cleanup but keeps the lease retained.
			managedClosed, _ = cleanupPreNativeAuthentication(nativeHome, managed.response.Bundle, managedHelperRetained)
			if managedClosed {
				latest = bytes.Clone(managed.response.Bundle)
				managedUnused = true
			}
		}
		if err := managed.finish(latest, managedClosed, false, managedClosed); err != nil || !managedClosed {
			output = nil
			if err == nil {
				err = subscription.Invalid()
			}
			returned = &managedExecutionUncertain{err}
		} else {
			authJoined = true
		}
		clear(latest)
		clear(managed.response.Bundle)
	}()
	if input.Source.Configuration.Subscription {
		client, close, err := subscriptionRPC(ctx, config, c.Credential)
		if err != nil {
			return nil, err
		}
		closeRPC = close
		authJoined = false
		managed, err = takeManagedSubscription(ctx, config, client, c.Credential, c.Instance, input.Source.AccountID, owner, c.Assignment.Revision, pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE)
		if err != nil {
			return nil, err
		}
		values, err := reviewProtectedBundle(managed.response.Bundle)
		if err != nil {
			return nil, err
		}
		protected = append(protected, values...)
		if err := validateManagedAuthenticationHome(nativeHome, manifest.WorkspaceRoots()); err != nil {
			return nil, err
		}
		if security.WriteAtomic(filepath.Join(nativeHome, "auth.json"), managed.response.Bundle) != nil {
			return nil, subscription.Invalid()
		}
	}
	bounded, stop := context.WithTimeout(ctx, 30*time.Second)
	registration, err := c.Client.RegisterExecution(bounded, authenticated(c.Credential, &pb.RegisterExecutionRequest{Mutation: &pb.Mutation{RequestId: string(intent.RegistrationID), Id: string(owner), ExpectedRevision: c.Assignment.Revision}, MachineId: string(input.Source.MachineID), InstanceId: string(c.Instance), CredentialDigest: digest[:]}))
	stop()
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	expectedProxy := apiproxy.Prefix
	if managed != nil {
		expectedProxy = ""
	}
	if registration == nil || registration.Msg == nil || registration.Msg.ProxyPath != expectedProxy {
		return nil, publicationUncertain()
	}
	manager := &workspace.Manager{Root: config.Root, Logger: logger}
	request := workspace.ReadRequest{ID: input.ActionID, Preparation: prep, Manifest: manifest}
	var result domain.NativeCodeReviewResult
	workspaceJoined = false
	err = manager.WithNativeCodeReview(ctx, request, input.Target, func(ctx context.Context, path string, selection domain.NativeCodeReviewSelection) (returned error) {
		nativeCtx, cancel := context.WithCancel(config.executionContext)
		defer cancel()
		stopCancellation := context.AfterFunc(ctx, cancel)
		defer stopCancellation()
		nativeConfig := codex.Config{CodeReviewModel: input.Source.Configuration.NativeModel, Mode: codex.ThreadProtocol, Version: installation.Version, Home: nativeHome, API: &codex.APIConfig{ServerOrigin: c.Credential.Endpoint, Token: token}, Process: process.Config{Directory: filepath.Join(config.Root, "processes"), OwnerID: owner, Executable: installation.ResolvedPath, Cwd: path, Env: env, Logger: logger, ProtectedValues: protected}}
		var proxy *nativeproxy.Proxy
		if managed != nil {
			nativeConfig.API = nil
			nativeConfig.ManagedAuthentication = true
			nativeConfig.QuotaObserver = managed.publishRollingQuota
		} else {
			proxy, err = openCodexNativeProxy(nativeCtx, config, input.ActionID)
			if err != nil {
				return err
			}
			if proxy != nil {
				nativeConfig.API.LoopbackProxyURL = proxy.NativeURL()
				nativeConfig.Process.ProtectedValues = append(protected, proxy.ProtectedValues()...)
			}
		}
		closed := false
		var native *codex.Client
		closeNative := func() error {
			if closed {
				return nil
			}
			var failures []error
			if native != nil {
				if managed != nil {
					capture, stop := context.WithTimeout(context.Background(), 5*time.Second)
					var e error
					latest, e = native.ManagedBundle(capture, false)
					stop()
					if e != nil {
						failures = append(failures, e)
					} else {
						values, e := reviewProtectedBundle(latest)
						if e != nil {
							failures = append(failures, e)
						} else {
							protected = append(protected, values...)
						}
					}
				}
				if e := native.Close(); e != nil {
					failures = append(failures, e)
				}
			}
			if proxy != nil {
				if e := proxy.Close(); e != nil {
					failures = append(failures, e)
				}
			}
			cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			if e := process.ReconcileOwnerContext(cleanup, filepath.Join(config.Root, "processes"), owner); e != nil {
				failures = append(failures, e)
			}
			// Authentication uncertainty cannot skip physical child/proxy joining. It
			// retains the original protected lease instead of granting another send.
			if managed != nil && len(failures) == 0 {
				var e error
				if !managedStarted {
					_, e = cleanupPreNativeAuthentication(nativeHome, managed.response.Bundle, managedHelperRetained)
					if e == nil && !managedHelperRetained {
						latest = bytes.Clone(managed.response.Bundle)
						managedUnused = true
					}
				} else {
					e = cleanupExecutionAuthentication(nativeHome, latest, managed.response.Bundle)
				}
				if e != nil {
					failures = append(failures, e)
				} else if !managedHelperRetained {
					managedClosed = true
				}
			}
			if len(failures) > 0 {
				return errors.Join(failures...)
			}
			closed = true
			nativeJoined = true
			return nil
		}
		defer func() {
			if e := closeNative(); e != nil {
				returned = e
			}
		}()
		nativeJoined = false
		managedStarted = managed != nil
		native, err = codex.Open(nativeCtx, nativeConfig)
		if err != nil {
			managedHelperRetained = codex.NativeJoinedHelperRetirementFailed(err)
			if managedHelperRetained || domain.SafeError(err).Code != domain.RecoveryRequired {
				managedStarted = false
			}
			return err
		}
		options := input.Source.Configuration.Options
		options.Permission, options.ApprovalPolicy, options.ApprovalsReviewer = domain.PermissionReadOnly, "never", ""
		options.ApprovalReviewModel, options.SubagentModel, options.SubagentEffort, options.MaxConcurrency = "", "", "", 0
		instructions, err := input.Source.Configuration.NativeInstructions(input.Source.Input.Mode)
		if err != nil {
			return err
		}
		settings := codex.ThreadSettings{Model: input.Source.Configuration.NativeModel, Provider: codexExecutionProvider(managed != nil), Effort: input.Source.Configuration.Effort, Cwd: path, WorkspaceRoots: []string{path}, Instructions: instructions, Options: options}
		thread, err := native.StartThread(ctx, intent.ThreadRequestID, settings)
		if err != nil {
			return err
		}
		if thread.Thread == nil || thread.Effective == nil || thread.Effective.Model != settings.Model || thread.Effective.Cwd != path || !matchesTitleOption(thread.Effective.Effort, settings.Effort) || !matchesTitleOption(thread.Effective.ServiceTier, options.ServiceTier) {
			return publicationUncertain()
		}
		publicationSequence := uint64(0)
		publishObservation := func(request *pb.PublishNativeCodeReviewRequest) error {
			publicationSequence++
			return publishNativeReviewObservation(ctx, c, jobRoot, publicationSequence, request)
		}
		publish := func(state domain.NativeCodeReviewState, turn domain.ID, item string) error {
			return publishObservation(&pb.PublishNativeCodeReviewRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(owner), ExpectedRevision: c.Assignment.Revision}, MachineId: string(input.Source.MachineID), InstanceId: string(c.Instance), State: rpc.NativeReviewStateWire(state), Selection: rpc.NativeReviewSelectionWire(selection), ThreadId: string(thread.Thread.ID), TurnId: string(turn), ItemId: item})
		}
		if err := publish(domain.NativeReviewReady, "", ""); err != nil {
			return err
		}
		// The durable job journal, review intent and Ready receipt precede this one
		// original send. No response-loss path re-enters StartCodeReview.
		nativeSent = true
		turn, err := native.StartCodeReview(ctx, input.ActionID, selection)
		if err != nil {
			inspect, stop := context.WithTimeout(context.Background(), 5*time.Second)
			claimed, e := native.CodeReviewSendClaimed(inspect)
			stop()
			if e == nil && !claimed {
				nativeSent = false
			}
			return err
		}
		usages := []domain.NativeResponseUsage{}
		usageState := domain.NativeReviewReady
		for count := 0; ; count++ {
			if count >= domain.MaxExecutionEvents {
				return publicationUncertain()
			}
			event, err := native.NextEvent(ctx)
			if err != nil {
				return err
			}
			if event.Kind == codex.MetadataEvent {
				if !nativeReviewMayIgnoreMetadata(event, thread.Thread.ID, turn.TurnID) {
					return domain.NativeCodeReviewUnavailable()
				}
				continue
			}
			if !event.Correlated || event.Late || event.ThreadID != thread.Thread.ID || event.TurnID != "" && event.TurnID != turn.TurnID {
				return publicationUncertain()
			}
			switch event.Kind {
			case codex.ModelSafetyEvent:
				if !event.Safety.MatchesPassiveEvent(event, thread.Thread.ID, turn.TurnID) {
					return publicationUncertain()
				}
			case codex.CodeReviewEvent:
				if event.CodeReview == nil || event.CodeReview.ActionID != input.ActionID {
					return publicationUncertain()
				}
				if err := publish(event.CodeReview.Stage, turn.TurnID, event.CodeReview.ItemID); err != nil {
					return err
				}
				usageState = event.CodeReview.Stage
			case codex.ResponseUsageEvent:
				if event.ResponseUsage == nil || event.ResponseUsage.Validate() != nil || len(usages) >= 128 {
					return publicationUncertain()
				}
				if usageState != domain.NativeReviewEntered && usageState != domain.NativeReviewExited {
					return publicationUncertain()
				}
				usageErr := publishObservation(&pb.PublishNativeCodeReviewRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(owner), ExpectedRevision: c.Assignment.Revision}, MachineId: string(input.Source.MachineID), InstanceId: string(c.Instance), State: rpc.NativeReviewStateWire(usageState), Selection: rpc.NativeReviewSelectionWire(selection), ThreadId: string(thread.Thread.ID), TurnId: string(turn.TurnID), Usage: rpc.NativeReviewUsageWire(*event.ResponseUsage, uint64(len(usages)+1), installation.Version)})
				if usageErr != nil {
					return usageErr
				}
				usages = append(usages, *event.ResponseUsage)
			case codex.TurnCompletedEvent:
				if event.Turn == nil || event.Turn.ID != turn.TurnID || event.Turn.Status != codex.TurnCompleted {
					return publicationUncertain()
				}
				result, err = native.CodeReviewResult(ctx)
				if err != nil {
					return err
				}
				if err := closeNative(); err != nil {
					return err
				}
				encoded, _ := json.Marshal(result)
				if !security.NewProtectedJSON(protected).Safe(encoded) {
					return subscription.Invalid()
				}
				for index, usage := range usages {
					result.UsageRecords = append(result.UsageRecords, domain.ResponseUsageRecord{ContextRevision: input.ContextRevision, Purpose: domain.NativeCodeReviewUsage, SessionID: input.Source.SessionID, ProjectID: domain.ID(c.Assignment.ProjectId), ExecutionID: input.ActionID, AccountID: input.Source.AccountID, ConnectionID: input.Source.ConnectionID, ProviderID: input.Source.Configuration.ProviderID, SubscriptionService: input.Source.Configuration.SubscriptionService, ModelID: input.Source.Configuration.ModelID, Harness: domain.Codex, Version: installation.Version, ThreadID: string(result.ThreadID), TurnID: string(result.TurnID), Sequence: uint64(index + 1), Usage: usage})
				}
				return nil
			case codex.ToolStartedEvent, codex.ToolCompletedEvent:
				if event.Tool == nil || event.Tool.Kind != codex.CommandTool && event.Tool.Kind != codex.ImageViewTool {
					return domain.NativeCodeReviewUnavailable()
				}
			case codex.ToolOutputEvent, codex.ArtifactStartedEvent, codex.ArtifactCompletedEvent, codex.ArtifactDeltaEvent, codex.MessageStartedEvent, codex.MessageCompletedEvent, codex.TextDeltaEvent, codex.UsageEvent, codex.ThreadStatusEvent, codex.TurnStartedEvent, codex.TurnPlanEvent:
				// Original typed observations remain private. They grant no ordinary
				// input acceptance, write, account substitution or public message claim.
			case codex.NoticeEvent:
				if event.Notice != domain.NativeWarning {
					return domain.NativeCodeReviewUnavailable()
				}
			default:
				return domain.NativeCodeReviewUnavailable()
			}
		}
	})
	workspaceJoined = err == nil || domain.SafeError(err).Code != domain.RecoveryRequired
	if err != nil {
		return nil, err
	}
	if managed != nil {
		if !managedClosed || managedUnused {
			return nil, subscription.Invalid()
		}
		// Finish is joined before a successful output can escape this function.
		if err := managed.finish(latest, true, false, true); err != nil {
			return nil, &managedExecutionUncertain{err}
		}
		clear(latest)
		clear(managed.response.Bundle)
		managed = nil
		authJoined = true
	}
	result.CleanupVerified = true
	return finalizeNativeReviewResult(ctx, logger, jobRoot, result)
}

// finalizeNativeReviewResult validates the completed review, enforces the
// aggregate encoded-size ceiling and durably retains the exact reported bytes.
// An over-limit result is rejected before review-result.json is written. The
// native send already happened, so callers must keep nativeSent true and never
// derive a no-send proof from this error.
func finalizeNativeReviewResult(ctx context.Context, logger *slog.Logger, jobRoot string, result domain.NativeCodeReviewResult) (json.RawMessage, error) {
	if err := result.Validate(); err != nil {
		if domain.SafeError(err).Code == domain.ResourceExhausted {
			logger.WarnContext(ctx, "native_code_review_result_too_large", "findings", len(result.Findings), "responses", len(result.UsageRecords), "limit_bytes", domain.MaxWorkerJobOutputBytes)
			return nil, domain.NativeCodeReviewResultTooLarge()
		}
		return nil, domain.NativeCodeReviewUnavailable()
	}
	output, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(output) > domain.MaxWorkerJobOutputBytes {
		logger.WarnContext(ctx, "native_code_review_result_too_large", "findings", len(result.Findings), "responses", len(result.UsageRecords), "encoded_bytes", len(output), "limit_bytes", domain.MaxWorkerJobOutputBytes)
		return nil, domain.NativeCodeReviewResultTooLarge()
	}
	if security.WriteAtomic(filepath.Join(jobRoot, "review-result.json"), output) != nil {
		return nil, publicationUncertain()
	}
	logger.InfoContext(ctx, "native_code_review_completed", "findings", len(result.Findings), "responses", len(result.UsageRecords), "cleanup_verified", true)
	return output, nil
}

// recoverNativeReviewResult settles a native review whose generic job journal
// is still started only when the durable review-result.json written by
// finalizeNativeReviewResult is present and bound to this exact assignment.
// It reads local evidence only: it never starts, resumes or re-sends a native
// review, and any missing or unverifiable evidence keeps the caller's
// recovery-required problem.
func recoverNativeReviewResult(ctx context.Context, config Config, resource *pb.Resource, job domain.Job) (json.RawMessage, bool) {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	var input domain.NativeCodeReviewInput
	if job.Type != domain.NativeCodeReviewJob || domain.DecodeNativeCodeReviewInput(job.Input, &input) != nil || input.Validate() != nil {
		return nil, false
	}
	logger = logger.With("job_id", resource.Id, "action_id", input.ActionID, "session_id", input.Source.SessionID)
	path := filepath.Join(config.Root, "jobs", resource.Id, "review-result.json")
	raw, err := security.ReadPrivate(path, domain.MaxWorkerJobOutputBytes)
	if err != nil {
		logger.InfoContext(ctx, "native_code_review_recovery_unavailable", "reason", "result_unreadable", "missing", errors.Is(err, os.ErrNotExist))
		return nil, false
	}
	var result domain.NativeCodeReviewResult
	if domain.DecodeWithLimit(raw, &result, domain.MaxWorkerJobOutputBytes) != nil || result.Validate() != nil {
		logger.WarnContext(ctx, "native_code_review_recovery_rejected", "reason", "result_invalid")
		return nil, false
	}
	// Only the exact bytes the original Worker produced are reusable.
	canonical, err := json.Marshal(result)
	if err != nil || !bytes.Equal(canonical, raw) {
		logger.WarnContext(ctx, "native_code_review_recovery_rejected", "reason", "result_not_canonical")
		return nil, false
	}
	if result.ActionID != input.ActionID || result.Selection.Target != input.Target || !result.CleanupVerified {
		logger.WarnContext(ctx, "native_code_review_recovery_rejected", "reason", "result_not_bound_to_assignment")
		return nil, false
	}
	for index, usage := range result.UsageRecords {
		if usage.ContextRevision != input.ContextRevision || usage.SessionID != input.Source.SessionID || usage.ProjectID != domain.ID(resource.ProjectId) || usage.ExecutionID != input.ActionID || usage.AccountID != input.Source.AccountID || usage.ConnectionID != input.Source.ConnectionID || usage.ProviderID != input.Source.Configuration.ProviderID || usage.SubscriptionService != input.Source.Configuration.SubscriptionService || usage.ModelID != input.Source.Configuration.ModelID || usage.Harness != domain.Codex || usage.Sequence != uint64(index+1) {
			logger.WarnContext(ctx, "native_code_review_recovery_rejected", "reason", "usage_not_bound_to_assignment", "usage_index", index)
			return nil, false
		}
	}
	logger.InfoContext(ctx, "native_code_review_recovered", "findings", len(result.Findings), "responses", len(result.UsageRecords), "cleanup_verified", true)
	return json.RawMessage(raw), true
}
