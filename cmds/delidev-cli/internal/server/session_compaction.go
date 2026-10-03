// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func compactionActor(ctx context.Context) error {
	a, ok := domain.PrincipalFrom(ctx)
	if !ok || a.Type != domain.OwnerDevice && a.Type != domain.ClientDevice {
		return domain.Fail(domain.PermissionDenied, "Only an owner or paired client can request session compaction.", "Use an authenticated product client.")
	}
	return nil
}

func sameCompactionInstallation(current, assigned domain.Installation) bool {
	// Discovery timestamps describe when the current observation was made, not
	// the executable identity assigned to the original native action. Every
	// other installation field remains immutable so a changed path, version,
	// digest, protocol or capability set cannot receive the old action.
	current.ObservedAt, assigned.ObservedAt = nil, nil
	return reflect.DeepEqual(current, assigned)
}

// Source verification shares current account/installation eligibility, but never
// claims a queued input, changes the preceding outcome or reruns routing.
func compactionSource(tx *store.Tx, sr store.Record, session domain.Session, action domain.ID) (domain.SessionCompactionInput, error) {
	var empty domain.SessionCompactionInput
	p := session.Execution
	if !session.WorkspaceAvailable() || session.CompactionJobID != "" || session.InitialExecution == nil || session.Preparation == nil || session.Preparation.State != domain.PreparationReady || session.ActiveExecutionID != "" || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || p == nil || !p.CleanupVerified {
		return empty, domain.CompactionUncertain()
	}
	h := session.InitialExecution.Configuration.Harness
	switch h {
	case domain.ClaudeCode:
		if !p.ClaudeContinuationBoundary(p.InputID) {
			return empty, domain.CompactionUncertain()
		}
	case domain.Codex, domain.OpenCode:
		if session.Dispatch != domain.DispatchReady || session.Outcome != domain.ExecutionSucceeded || session.PendingInputs != 0 || session.PendingInputBytes != 0 || p.Waiting != (domain.NativeWaiting{}) || p.UnconfirmedResponses != 0 || len(p.Subagents) != 0 || !p.NativeCompactions.Closed() {
			return empty, domain.CompactionUncertain()
		}
	default:
		return empty, domain.Fail(domain.Unsupported, "This harness has no supported manual compaction profile.", "Update the server and original Worker together when this profile becomes available.")
	}
	if err := tx.RequireNoSessionFork(sr.ID); err != nil {
		return empty, err
	}
	r, err := tx.SessionExecutionJob(sr.ID, p.ExecutionID)
	if err != nil {
		return empty, err
	}
	j, err := store.Decode[domain.Job](r)
	if err != nil {
		return empty, err
	}
	var original domain.ExecutionJobInput
	var done domain.ExecutionCompletion
	if j.State != domain.JobSucceeded || domain.Decode(j.Input, &original) != nil || original.Validate() != nil || !session.OwnsExecution(original) || domain.Decode(j.Output, &done) != nil || done.Version != 2 || done.Outcome != domain.ExecutionSucceeded || done.ExecutionID != p.ExecutionID || done.InputID != p.InputID || done.NativeTurnID != domain.NativeIdentity(p.NativeTurnID) || done.NativeThreadID != domain.NativeIdentity(p.NativeThreadID) || done.LastSequence != p.LastSequence {
		return empty, domain.CompactionUncertain()
	}
	if err := checkContinuationInputs(tx, sr.ID, original, *p); err != nil {
		return empty, err
	}
	_, machine, err := activeMachine(tx, session.MachineID)
	if err != nil {
		return empty, err
	}
	if h == domain.Codex && (!slices.Contains(machine.WorkerCapabilities, domain.NativeSessionCompactionV1) || !slices.Contains(machine.WorkerCapabilities, domain.CodexSessionCompactionV1)) {
		return empty, domain.Fail(domain.Unsupported, "The original Worker does not support Codex compaction.", "Update that Worker before requesting this operation.")
	}
	if h == domain.OpenCode && (!slices.Contains(machine.WorkerCapabilities, domain.NativeSessionCompactionV1) || !slices.Contains(machine.WorkerCapabilities, domain.OpenCodeSessionCompactionV1)) {
		return empty, domain.Fail(domain.Unsupported, "The original Worker does not support OpenCode compaction.", "Update that Worker before requesting this operation.")
	}
	instance, seen, err := tx.WorkerInstance(session.MachineID)
	if err != nil {
		return empty, err
	}
	if instance.Validate() != nil || time.Since(seen) > domain.WorkerConnectionTimeout || seen.After(time.Now().UTC().Add(time.Second)) {
		return empty, domain.Fail(domain.Unavailable, "The original Worker is unavailable.", "Reconnect it before requesting compaction.")
	}
	checked, err := checkedExecutionAssignment(tx, sr, session, machine, original)
	if err != nil {
		return empty, err
	}
	if !sameCompactionInstallation(checked.Installation, original.Installation) || !bytes.Equal(checked.Preparation, original.Preparation) || !bytes.Equal(checked.Manifest, original.Manifest) {
		return empty, domain.CompactionUncertain()
	}
	if err := tx.RequireSessionBudget(sr.ID, session.EstimatedCostBudget); err != nil {
		return empty, err
	}
	restored := original
	restored.Version, restored.ExecutionID, restored.InputID = 2, action, domain.NewID()
	restored.ThreadRequestID, restored.TurnRequestID = domain.NewID(), domain.NewID()
	intent := domain.ContinueAutomatically
	var previous *domain.SessionCompactionRef
	if session.Compaction != nil && session.Compaction.ExecutionID == original.ExecutionID {
		previous = session.Compaction
		if previous.RequiresResume {
			intent = domain.ContinueExplicitly
		}
	}
	restored.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: session.InitialExecution.ID, HistoryRequestID: domain.NewID(), Previous: *p, Completion: done, AssignmentInputDigest: continuationDigest(j.Input), InputMode: original.Input.Mode, PromptDigest: continuationDigest([]byte(original.Input.Prompt)), Intent: intent, Compaction: previous}
	version := uint32(1)
	if h == domain.Codex {
		version = 2
	} else if h == domain.OpenCode {
		version = 3
	}
	input := domain.SessionCompactionInput{Version: version, ActionID: action, SourceJobID: r.ID, Assignment: original, Restore: restored, Completion: done, Previous: previous, Dispatch: session.Dispatch, Intent: session.NextExecutionIntent}
	return input, input.Validate()
}
func (s *Service) CompactSession(ctx context.Context, req *connect.Request[pb.CompactSessionRequest]) (*connect.Response[pb.CompactSessionResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if err := compactionActor(ctx); err != nil {
		return nil, rpc.Error(err, corr)
	}
	m := req.Msg.Mutation
	if err := validateSessionMutation(m); err != nil {
		return nil, rpc.Error(err, corr)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		Session  domain.ID
		Revision uint64
		Actor    domain.Principal
	}{domain.ID(m.Id), m.ExpectedRevision, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "session.compact", identity, func(tx *store.Tx) (any, error) {
		if err := tx.Authorize(); err != nil {
			return nil, err
		}
		sr, session, err := sessionRecord(tx, identity.Session)
		if err != nil {
			return nil, err
		}
		if sr.Revision != identity.Revision {
			return nil, continuationConflict()
		}
		input, err := compactionSource(tx, sr, session, domain.ID(m.RequestId))
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		job, err := tx.PutJob(domain.NewID(), 0, sr.ID, sr.ProjectID, domain.Job{Type: domain.CompactSessionJob, State: domain.JobQueued, MachineID: session.MachineID, ParentID: input.SourceJobID, Input: raw, AcceptedAt: time.Now().UTC()})
		if err != nil {
			return nil, err
		}
		session.CompactionJobID, session.LastCompactionJobID = job.ID, job.ID
		session.Dispatch = domain.DispatchPaused
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return struct{ JobID domain.ID }{job.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	var ref struct{ JobID domain.ID }
	if json.Unmarshal(result.Data, &ref) != nil {
		return nil, rpc.Error(domain.CompactionUncertain(), corr)
	}
	var job store.Record
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var e error
		job, e = tx.Get(domain.JobKind, ref.JobID)
		return e
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	s.logger.InfoContext(ctx, "session_compaction_accepted", "session_id", m.Id, "job_id", ref.JobID, "request_id", m.RequestId, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.CompactSessionResponse{Job: rpc.Resource(job), RequestId: m.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func finishSessionCompaction(tx *store.Tx, r store.Record, j domain.Job, revision uint64, raw json.RawMessage, problem *domain.Error) (store.Record, error) {
	var input domain.SessionCompactionInput
	if domain.Decode(j.Input, &input) != nil || input.Validate() != nil {
		return store.Record{}, domain.CompactionUncertain()
	}
	sr, session, err := sessionRecord(tx, r.SessionID)
	if err != nil {
		return store.Record{}, err
	}
	if session.CompactionJobID != r.ID || !session.OwnsExecution(input.Assignment) {
		return store.Record{}, domain.CompactionUncertain()
	}
	var output domain.SessionCompactionResult
	verified := problem == nil && domain.Decode(raw, &output) == nil && output.Validate() == nil && output.ActionID == input.ActionID && output.ExecutionID == input.Assignment.ExecutionID && output.Checkpoint.JobID == r.ID
	if verified {
		if input.Version == 3 {
			verified = output.Version == 3 && output.Harness == domain.OpenCode && output.OpenCode != nil && output.OpenCode.NativeSessionID == input.Completion.NativeThreadID && output.OpenCode.SourceNativeInputID == input.Completion.NativeTurnID
		} else if input.Version == 2 {
			verified = output.Version == 2 && output.Harness == input.Assignment.Configuration.Harness && output.Codex != nil && output.Codex.NativeThreadID == input.Completion.NativeThreadID && output.Codex.SourceNativeTurnID == input.Completion.NativeTurnID
		} else {
			verified = output.Version == 1
		}
	}
	if verified && output.Version == 3 {
		v := input.Assignment
		for n, usage := range output.OpenCode.Usages {
			id := domain.NewID()
			observation := domain.OpenCodeUsageRecord{ExecutionID: input.ActionID, AccountID: v.AccountID, ConnectionID: v.ConnectionID, ProviderID: v.Configuration.ProviderID, ModelID: v.Configuration.ModelID, Harness: domain.OpenCode, Version: v.Installation.Version, ThreadID: string(output.OpenCode.NativeSessionID), TurnID: string(output.OpenCode.UserID), Sequence: uint64(n + 1), Usage: usage}
			if err := tx.PutOpenCodeUsage(id, sr.ID, sr.ProjectID, observation); err != nil {
				return store.Record{}, err
			}
			if err := tx.PutOpenCodeAccounting(id, v.InputID, sr.ID, sr.ProjectID, observation); err != nil {
				return store.Record{}, err
			}
		}
	}
	canceled, err := tx.JobCancellationRequested(r.ID)
	if err != nil {
		return store.Record{}, err
	}
	now := time.Now().UTC()
	j.FinishedAt = &now
	session.Dispatch, session.NextExecutionIntent = domain.DispatchPaused, ""
	if !verified || canceled {
		j.State, j.Problem, j.Output = domain.JobUncertain, domain.CompactionUncertain(), nil
		// Cancellation of claimed work retains native ownership even when a
		// valid late report arrives. Its observations remain evidence, but cannot
		// grant a successor checkpoint or finalize a pending Archive.
		if verified {
			j.Output = raw
		}
		session.Recovery = domain.NeedsRecovery
	} else {
		session.CompactionJobID = ""
		session.Compaction = &output.Checkpoint
		j.Output = raw
		j.State = domain.JobSucceeded
		j.Problem = nil
		if output.Outcome == domain.CompactionFailed {
			j.State = domain.JobFailed
			j.Problem = domain.Fail(domain.Conflict, "The native compaction command failed.", "Explicit Resume is required before later input; the prior execution outcome is preserved.")
		}
		if output.Outcome == domain.CompactionSucceeded && session.Archive == domain.NotArchived && session.Recovery == domain.NoRecovery && input.Dispatch == domain.DispatchReady {
			session.Dispatch, session.NextExecutionIntent = domain.DispatchReady, input.Intent
		}
		if session.Archive == domain.ArchivePending && session.Recovery == domain.NoRecovery {
			session.Archive = domain.Archived
		}
	}
	if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
		return store.Record{}, err
	}
	saved, err := tx.PutJob(r.ID, revision, r.SessionID, r.ProjectID, j)
	return store.Record{ID: saved.ID}, err
}
func loseSessionCompaction(tx *store.Tx, r store.Record, j domain.Job) error {
	sr, session, err := sessionRecord(tx, r.SessionID)
	if err != nil {
		return err
	}
	if session.CompactionJobID != r.ID {
		return domain.CompactionUncertain()
	}
	// Revocation cancels queued jobs before any Worker claim. Release only that
	// undispatched action; a lost claimed job still owns possible native effects.
	if j.State == domain.JobCanceled {
		session.CompactionJobID = ""
	} else {
		session.Recovery = domain.NeedsRecovery
	}
	session.Dispatch, session.NextExecutionIntent = domain.DispatchPaused, ""
	_, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
	return err
}
