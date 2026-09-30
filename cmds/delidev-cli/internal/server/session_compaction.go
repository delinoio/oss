// SPDX-License-Identifier: Apache-2.0
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

func compactionActor(ctx context.Context) error {
	a, ok := domain.PrincipalFrom(ctx)
	if !ok || a.Type != domain.OwnerDevice && a.Type != domain.ClientDevice {
		return domain.Fail(domain.PermissionDenied, "Only an owner or paired client can request session compaction.", "Use an authenticated product client.")
	}
	return nil
}

// Source verification shares current account/installation eligibility, but never
// claims a queued input, changes the preceding outcome or reruns routing.
func compactionSource(tx *store.Tx, sr store.Record, session domain.Session, action domain.ID) (domain.SessionCompactionInput, error) {
	var empty domain.SessionCompactionInput
	p := session.Execution
	if session.CompactionJobID != "" || session.InitialExecution == nil || session.Preparation == nil || session.Preparation.State != domain.PreparationReady || session.ActiveExecutionID != "" || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || p == nil || !p.CleanupVerified || !p.ClaudeContinuationBoundary(p.InputID) {
		return empty, domain.CompactionUncertain()
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
	if !bytes.Equal(checked.Preparation, original.Preparation) || !bytes.Equal(checked.Manifest, original.Manifest) {
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
	input := domain.SessionCompactionInput{Version: 1, ActionID: action, SourceJobID: r.ID, Assignment: original, Restore: restored, Completion: done, Previous: previous, Dispatch: session.Dispatch, Intent: session.NextExecutionIntent}
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
	now := time.Now().UTC()
	j.FinishedAt = &now
	session.Dispatch, session.NextExecutionIntent = domain.DispatchPaused, ""
	if !verified {
		j.State, j.Problem, j.Output = domain.JobUncertain, domain.CompactionUncertain(), nil
		session.Recovery = domain.NeedsRecovery
	} else {
		session.CompactionJobID = ""
		session.Compaction = &output.Checkpoint
		j.Output = raw
		j.State = domain.JobSucceeded
		j.Problem = nil
		canceled, err := tx.JobCancellationRequested(r.ID)
		if err != nil {
			return store.Record{}, err
		}
		if output.Outcome == domain.CompactionFailed {
			j.State = domain.JobFailed
			j.Problem = domain.Fail(domain.Conflict, "The native compaction command failed.", "Explicit Resume is required before later input; the prior execution outcome is preserved.")
		}
		if output.Outcome == domain.CompactionSucceeded && !canceled && session.Archive == domain.NotArchived && session.Recovery == domain.NoRecovery && input.Dispatch == domain.DispatchReady {
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
