package server

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (s *Service) RecoverSessionExecution(ctx context.Context, req *connect.Request[pb.RecoverSessionExecutionRequest]) (*connect.Response[pb.RecoverSessionExecutionResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Only an owner or paired client can request execution recovery.", "Use an authorized client."), correlation)
	}
	meta := req.Msg.Mutation
	if err := validateSessionMutation(meta); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	execution := domain.ID(req.Msg.ExpectedExecutionId)
	if err := execution.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	identity := struct {
		Session, Execution domain.ID
		Revision           uint64
		Actor              domain.Principal
	}{domain.ID(meta.Id), execution, meta.ExpectedRevision, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "session.execution.recover", identity, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, identity.Session)
		if err != nil {
			return nil, err
		}
		if sr.Revision != identity.Revision || session.InitialExecution == nil || session.ExecutionSelection().ID != execution || (session.Recovery != domain.NeedsRecovery && session.Recovery != domain.Reconciling) || session.Archive == domain.Archived {
			return nil, domain.ExecutionRecoveryUncertain()
		}
		if _, _, err := activeMachine(tx, session.MachineID); err != nil {
			return nil, err
		}
		if session.ExecutionRecoveryJobID != "" {
			prior, err := tx.Get(domain.JobKind, session.ExecutionRecoveryJobID)
			if err != nil {
				return nil, err
			}
			job, err := store.Decode[domain.Job](prior)
			if err != nil {
				return nil, err
			}
			original, err := tx.SessionExecutionJob(sr.ID, execution)
			if err != nil {
				return nil, err
			}
			if prior.SessionID != sr.ID || job.Type != domain.RecoverExecutionJob || job.ParentID != original.ID || (session.Execution != nil && job.ParentID != session.Execution.JobID) {
				return nil, domain.ExecutionRecoveryUncertain()
			}
			if job.State == domain.JobQueued || job.State == domain.JobClaimed {
				return sessionReceipt{SessionID: sr.ID}, nil
			}
		}
		input, err := executionRecoveryRequest(tx, s.Identity.ServerID, sr, session)
		if err != nil {
			s.logger.WarnContext(ctx, "session_execution_recovery_rejected", "session_id", sr.ID, "execution_id", execution, "phase", "original-ownership", "code", domain.SafeError(err).Code)
			return nil, err
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		recoveryID := domain.NewID()
		if _, err := tx.PutJob(recoveryID, 0, sr.ID, sr.ProjectID, domain.Job{Type: domain.RecoverExecutionJob, State: domain.JobQueued, MachineID: session.MachineID, ParentID: input.JobID, Input: raw, AcceptedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		session.ExecutionRecoveryJobID = recoveryID
		session.Recovery, session.Dispatch, session.NextExecutionIntent = domain.Reconciling, domain.DispatchPaused, ""
		if _, err := tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return sessionReceipt{SessionID: sr.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	change, err := s.sessionResult(ctx, result)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "session_execution_recovery_accepted", "session_id", meta.Id, "execution_id", execution, "replayed", result.Replayed, "correlation_id", correlation)
	response := connect.NewResponse(&pb.RecoverSessionExecutionResponse{Change: change})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

// Rebuild all comparison facts inside each acceptance/report transaction. No
// current account readiness is required to inspect old completion; Resume owns
// fresh execution authorization after this operation leaves dispatch paused.
func executionRecoveryRequest(tx *store.Tx, serverID domain.ID, sr store.Record, session domain.Session) (domain.ExecutionRecoveryRequest, error) {
	var result domain.ExecutionRecoveryRequest
	// A separate manual action owns its checkpoint and workspace until cleanup
	// is proved. Original conversation recovery cannot release that ownership.
	if session.CompactionJobID != "" {
		return result, domain.ExecutionRecoveryUncertain()
	}
	if session.Execution == nil {
		return prStartupRecoveryRequest(tx, serverID, sr, session)
	}
	progress := session.Execution
	if progress == nil || session.InitialExecution == nil || session.Preparation == nil || session.Preparation.State != domain.PreparationReady || session.PendingSteerID != "" || progress.Waiting != (domain.NativeWaiting{}) || progress.UnconfirmedResponses != 0 || (session.ActiveExecutionID != "" && session.ActiveExecutionID != progress.ExecutionID) || (session.Recovery != domain.NeedsRecovery && session.Recovery != domain.Reconciling) {
		return result, domain.ExecutionRecoveryUncertain()
	}
	if len(progress.Subagents) != 0 {
		return result, domain.ExecutionRecoveryUncertain()
	}
	if session.Outcome != domain.ExecutionSucceeded && session.Outcome != domain.ExecutionFailed && session.Outcome != domain.ExecutionStopped {
		return result, domain.ExecutionRecoveryUncertain()
	}
	original, err := tx.SessionExecutionJob(sr.ID, session.ExecutionSelection().ID)
	if err != nil {
		return result, err
	}
	job, err := store.Decode[domain.Job](original)
	if err != nil {
		return result, err
	}
	if original.ID != progress.JobID || job.Type != domain.ExecuteSessionJob || (job.State != domain.JobUncertain && !job.State.Terminal()) || job.MachineID != session.MachineID {
		return result, domain.ExecutionRecoveryUncertain()
	}
	assigned, err := tx.JobAssignment(original.ID)
	if err != nil {
		return result, err
	}
	claim, err := store.Decode[domain.Job](assigned)
	if err != nil {
		return result, err
	}
	var input domain.ExecutionJobInput
	if claim.Type != domain.ExecuteSessionJob || claim.State != domain.JobClaimed || claim.InstanceID != job.InstanceID || claim.MachineID != job.MachineID || assigned.SessionID != sr.ID || !bytes.Equal(claim.Input, job.Input) || domain.Decode(claim.Input, &input) != nil || input.Validate() != nil || !session.OwnsExecution(input) || progress.ExecutionID != input.ExecutionID || progress.InputID != input.InputID {
		return result, domain.ExecutionRecoveryUncertain()
	}
	if err := checkContinuationInputs(tx, sr.ID, input, *progress); err != nil {
		return result, err
	}
	primary, err := tx.Get(domain.QueueKind, input.InputID)
	if err != nil {
		return result, err
	}
	primaryInput, err := store.Decode[domain.QueuedInput](primary)
	if err != nil || primaryInput.NativeRequestID != input.TurnRequestID || !queuedSessionInput(primaryInput).Equal(input.Input) {
		return result, domain.ExecutionRecoveryUncertain()
	}
	bindings, err := domain.CheckedExecutionInputs(input.InputID, domain.BindSessionInput(input.InputID, input.Input).PromptDigest, progress.AcceptedInputs)
	if err != nil {
		return result, err
	}
	if err := tx.RequireSettledExecutionDeliveries(sr.ID, input.ExecutionID, len(bindings)); err != nil {
		return result, err
	}
	grant, err := tx.ExecutionGrantForJob(original.ID)
	if err != nil {
		return result, err
	}
	if grant.ExecutionID != input.ExecutionID || grant.MachineID != job.MachineID || grant.InstanceID != claim.InstanceID {
		return result, domain.ExecutionRecoveryUncertain()
	}
	history := input.ExecutionID
	if input.Continuation != nil {
		history = input.Continuation.HistoryExecutionID
	}
	if input.Fork != nil {
		// The child's first execution resumes the native home created by Fork;
		// its fresh execution ID owns the report, not the retained history.
		history = input.Fork.RuntimeID
	}
	result = domain.ExecutionRecoveryRequest{ApprovalsReviewer: input.Configuration.Options.ApprovalsReviewer,
		Version: 1, ServerID: serverID, DeviceID: grant.DeviceID, InstanceID: claim.InstanceID, JobID: original.ID, SessionID: sr.ID, MachineID: session.MachineID,
		AssignmentRevision: assigned.Revision, AssignmentDigest: continuationDigest(assigned.Data), AssignmentInputDigest: continuationDigest(claim.Input), ConfigurationDigest: input.ConfigurationDigest,
		AccountID: input.AccountID, ConnectionID: input.ConnectionID, HistoryExecutionID: history, InputMode: input.Input.Mode, PromptDigest: domain.BindSessionInput(input.InputID, input.Input).PromptDigest, AcceptedInputs: progress.AcceptedInputs, Preparation: input.Preparation, Manifest: input.Manifest,
		Completion: domain.ExecutionCompletion{Version: 1, ExecutionID: input.ExecutionID, InputID: input.InputID, NativeThreadID: domain.NativeIdentity(progress.NativeThreadID), NativeTurnID: domain.NativeIdentity(progress.NativeTurnID), LastSequence: progress.LastSequence, Outcome: progress.Outcome, CleanupVerified: true},
	}
	if input.Configuration.Harness == domain.OpenCode {
		if len(bindings) != 1 {
			return domain.ExecutionRecoveryRequest{}, domain.ExecutionRecoveryUncertain()
		}
		creation := input.ThreadRequestID
		if session.Fork != nil {
			creation, err = openCodeForkRecoveryCreation(tx, sr, session, input, history, grant.DeviceID)
			if err != nil {
				return domain.ExecutionRecoveryRequest{}, err
			}
		} else if input.Continuation != nil {
			first, err := tx.SessionExecutionJob(sr.ID, history)
			if err != nil {
				return result, err
			}
			initial, err := store.Decode[domain.Job](first)
			var original domain.ExecutionJobInput
			if err != nil || domain.Decode(initial.Input, &original) != nil || original.Validate() != nil || (original.Version != 1 && original.Version != 4) || original.Configuration.Harness != domain.OpenCode || original.ExecutionID != history || original.SessionID != sr.ID || original.MachineID != input.MachineID || original.ConfigurationDigest != input.ConfigurationDigest || original.AccountID != input.AccountID || original.ConnectionID != input.ConnectionID {
				return domain.ExecutionRecoveryRequest{}, domain.ExecutionRecoveryUncertain()
			}
			creation = original.ThreadRequestID
		}
		result.Harness = domain.OpenCode
		result.OpenCode = &domain.OpenCodeRecoveryReference{ClaimVersion: nativeRecoveryClaimVersion(input), CreationRequestID: creation, BindingRequestID: input.ThreadRequestID, InputRequestID: input.TurnRequestID}
	} else if input.Configuration.Harness == domain.ClaudeCode {
		permission, err := input.Configuration.ClaudeAPIInputPermission(input.Input.Mode)
		if err != nil || !progress.ClaudeContinuationBoundary(input.InputID) {
			return domain.ExecutionRecoveryRequest{}, domain.ExecutionRecoveryUncertain()
		}
		eligible, err := tx.ClaudeRootContentContinuation(input.ExecutionID)
		if err != nil {
			return result, err
		}
		if !eligible {
			return domain.ExecutionRecoveryRequest{}, domain.ExecutionRecoveryUncertain()
		}
		result.Harness = domain.ClaudeCode
		result.Claude = &domain.ClaudeRecoveryReference{ClaimVersion: nativeRecoveryClaimVersion(input), Version: input.Installation.Version, Executable: input.Installation.ResolvedPath, Model: input.Configuration.NativeModel, Effort: input.Configuration.Effort, Permission: permission, InstructionsDigest: continuationDigest([]byte(input.Configuration.Instructions)), BindingRequestID: input.ThreadRequestID, InputRequestID: input.TurnRequestID}
		if input.Version == 4 {
			if session.Startup == nil || session.Startup.Ready == nil || session.Startup.Ready.Validate() != nil {
				return result, domain.ExecutionRecoveryUncertain()
			}
			selection := *input.Startup
			selection.ExecutableSHA256 = session.Startup.Ready.ExecutableSHA256
			result.Claude.Startup = &selection
			result.Claude.Version = ""
		}
	} else if input.Configuration.Harness != domain.Codex {
		return domain.ExecutionRecoveryRequest{}, domain.ExecutionRecoveryUncertain()
	}
	return result, result.Validate()
}

func validateExecutionRecoveryResult(tx *store.Tx, record store.Record, job domain.Job, raw []byte) error {
	var expected domain.ExecutionRecoveryRequest
	if domain.Decode(job.Input, &expected) == nil && expected.Startup != nil {
		return validatePRStartupRecoveryResult(tx, record, job, expected, raw)
	}
	var evidence domain.ExecutionRecoveryEvidence
	if domain.Decode(job.Input, &expected) != nil || domain.Decode(raw, &evidence) != nil || evidence.Validate(expected) != nil || expected.JobID != job.ParentID || expected.SessionID != record.SessionID || expected.MachineID != job.MachineID {
		return domain.ExecutionRecoveryUncertain()
	}
	sr, session, err := sessionRecord(tx, record.SessionID)
	if err != nil {
		return err
	}
	if session.ExecutionRecoveryJobID != record.ID {
		return domain.ExecutionRecoveryUncertain()
	}
	fresh, err := executionRecoveryRequest(tx, expected.ServerID, sr, session)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(expected)
	b, _ := json.Marshal(fresh)
	if !bytes.Equal(a, b) {
		return domain.ExecutionRecoveryUncertain()
	}
	return nil
}

func finishExecutionRecovery(tx *store.Tx, record store.Record, job domain.Job) error {
	sr, session, err := sessionRecord(tx, record.SessionID)
	if err != nil {
		return err
	}
	if session.ExecutionRecoveryJobID != record.ID {
		return nil
	}
	session.Dispatch, session.NextExecutionIntent = domain.DispatchPaused, ""
	if job.State != domain.JobSucceeded {
		session.Recovery = domain.NeedsRecovery
		if session.Problem == nil {
			session.Problem = domain.ExecutionRecoveryUncertain()
		}
	} else {
		if err := validateExecutionRecoveryResult(tx, record, job, job.Output); err != nil {
			return err
		}
		var expected domain.ExecutionRecoveryRequest
		if domain.Decode(job.Input, &expected) == nil && expected.Startup != nil {
			return finishPRStartupRecovery(tx, record, job)
		}
		var evidence domain.ExecutionRecoveryEvidence
		if err := domain.Decode(job.Output, &evidence); err != nil {
			return err
		}
		original, err := tx.Get(domain.JobKind, job.ParentID)
		if err != nil {
			return err
		}
		previous, err := store.Decode[domain.Job](original)
		if err != nil {
			return err
		}
		previous.State = domain.JobSucceeded
		previous.Problem = nil
		if evidence.Completion.Outcome == domain.ExecutionFailed {
			previous.State = domain.JobFailed
			previous.Problem = session.Problem
		}
		if evidence.Completion.Outcome == domain.ExecutionStopped {
			previous.State = domain.JobCanceled
		}
		previous.Output, err = json.Marshal(evidence.Completion)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		previous.FinishedAt = &now
		if _, err := tx.PutJob(original.ID, original.Revision, original.SessionID, original.ProjectID, previous); err != nil {
			return err
		}
		var retryInput domain.ExecutionJobInput
		if domain.Decode(previous.Input, &retryInput) != nil {
			return domain.ExecutionRecoveryUncertain()
		}
		if retryInput.SidechatRetry != nil {
			canceled, err := tx.JobCancellationRequested(original.ID)
			if err != nil {
				return err
			}
			for i := range session.SidechatRetries {
				g := &session.SidechatRetries[i]
				if g.ID == retryInput.SidechatRetry.GenerationID && g.ExecutionID == evidence.Completion.ExecutionID && g.ExecutionJobID == original.ID {
					if evidence.Completion.Outcome == domain.ExecutionSucceeded && !canceled && !session.AutomaticRemediationStopped && session.Archive == domain.NotArchived {
						g.Completed = true
						session.SidechatCurrentAnswer = g.ExecutionID
					}
					session.SidechatActiveRetry = ""
				}
			}
		}
		session.Execution.CleanupVerified = true
		session.Recovery, session.ActiveExecutionID = domain.NoRecovery, ""
		if session.Problem != nil && session.Problem.Code == domain.RecoveryRequired {
			session.Problem = nil
		}
		if session.Archive == domain.ArchivePending {
			session.Archive = domain.Archived
		}
		if evidence.Completion.Outcome == domain.ExecutionSucceeded && session.NameMode == domain.AutomaticSessionName && session.NameOwner == domain.AutomaticNameOwner && session.TitleState == domain.TitleWaiting && session.TitleOperationID == "" {
			if job.MachineID == previous.MachineID && job.InstanceID == previous.InstanceID && job.AssignedDeviceID == previous.AssignedDeviceID {
				var originalInput domain.ExecutionJobInput
				if err := domain.Decode(previous.Input, &originalInput); err != nil || originalInput.Validate() != nil {
					return domain.ExecutionRecoveryUncertain()
				}
				if err := queueAutomaticSessionTitle(tx, sr, &session, original, originalInput, true); err != nil {
					return err
				}
			} else {
				session.TitleOperationID = domain.NewID()
				session.TitleState, session.TitleReason = domain.TitleSkipped, domain.TitleReasonAuthorityLost
			}
		}
	}
	_, err = tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
	return err
}

func nativeRecoveryClaimVersion(input domain.ExecutionJobInput) uint32 {
	if input.Continuation != nil || input.Fork != nil {
		return 2
	}
	return 1
}

// The child owns its immutable creation marker independently of its parent.
// Legacy seeds may derive it only from the exact retained completed Fork input.
func openCodeForkRecoveryCreation(tx *store.Tx, row store.Record, session domain.Session, input domain.ExecutionJobInput, history, device domain.ID) (domain.ID, error) {
	f := session.Fork
	if f == nil || f.Validate() != nil || f.Snapshot.Configuration.Harness != domain.OpenCode || f.RuntimeID != history || f.NativeThreadID != domain.NativeIdentity(session.Execution.NativeThreadID) || f.WorkerDeviceID != device || f.Snapshot.ConfigurationDigest != input.ConfigurationDigest || f.Snapshot.InitialAccountID != input.AccountID || f.Snapshot.ConnectionID != input.ConnectionID || session.InitialExecution.ID != history {
		return "", domain.ExecutionRecoveryUncertain()
	}
	if input.Fork != nil && (input.Fork.JobID != f.JobID || input.Fork.RuntimeID != f.RuntimeID || input.Fork.NativeThreadID != f.NativeThreadID || input.Fork.NativeTurnID != f.ChildTurn() || input.Fork.CheckpointDigest != f.CheckpointDigest) {
		return "", domain.ExecutionRecoveryUncertain()
	}
	if f.OpenCodeCreationProof != nil {
		if !f.VerifyOpenCodeCreation(row.ID) {
			return "", domain.ExecutionRecoveryUncertain()
		}
		return f.OpenCodeCreationRequestID, nil
	}
	original, err := tx.Get(domain.JobKind, f.JobID)
	if err != nil {
		return "", domain.ExecutionRecoveryUncertain()
	}
	job, err := store.Decode[domain.Job](original)
	var seed domain.ForkJobInput
	if err != nil || original.SessionID != f.SourceSessionID || job.Type != domain.ForkSessionJob || job.State != domain.JobSucceeded || job.MachineID != session.MachineID || job.AssignedDeviceID != f.WorkerDeviceID || forkInputDigest(job.Input) != f.JobInputDigest || domain.Decode(job.Input, &seed) != nil || seed.Validate() != nil || seed.OpenCode == nil || seed.ChildSessionID != row.ID || seed.SourceSessionID != f.SourceSessionID || seed.SourceRevision != f.SourceRevision || seed.RuntimeID != f.RuntimeID || seed.Completion.ExecutionID != f.SourceExecutionID || seed.Completion.NativeTurnID != f.SourceTurnID || seed.SourceAssignment.ConfigurationDigest != input.ConfigurationDigest || seed.SourceAssignment.AccountID != input.AccountID || seed.SourceAssignment.ConnectionID != input.ConnectionID {
		return "", domain.ExecutionRecoveryUncertain()
	}
	var completion domain.ForkJobResult
	if domain.Decode(job.Output, &completion) != nil || completion.ValidateIdentity(seed) != nil || completion.ChildSessionID != row.ID || completion.RuntimeID != f.RuntimeID || completion.NativeThreadID != f.NativeThreadID || completion.NativeTurnID != f.ChildTurn() || completion.CheckpointDigest != f.CheckpointDigest {
		return "", domain.ExecutionRecoveryUncertain()
	}
	actual, _ := json.Marshal(f.Snapshot)
	expected, _ := json.Marshal(seed.Snapshot)
	selected, _ := json.Marshal(f.Startup)
	originalSelection, _ := json.Marshal(seed.Startup)
	if !bytes.Equal(actual, expected) || !bytes.Equal(selected, originalSelection) {
		return "", domain.ExecutionRecoveryUncertain()
	}
	if f.OpenCodeCreationRequestID != "" && f.OpenCodeCreationRequestID != seed.OpenCode.Fork {
		return "", domain.ExecutionRecoveryUncertain()
	}
	return seed.OpenCode.Fork, nil
}
