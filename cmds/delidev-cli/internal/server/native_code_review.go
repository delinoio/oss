// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// The source remains an ordinary settled execution. Review never replaces its
// current history, selected account or workspace continuation predecessor.
func nativeReviewAuthority(tx *store.Tx, row store.Record, job domain.Job) (domain.NativeCodeReviewInput, error) {
	var i domain.NativeCodeReviewInput
	if job.Type != domain.NativeCodeReviewJob || domain.DecodeNativeCodeReviewInput(job.Input, &i) != nil || i.Validate() != nil || i.Source.SessionID != row.SessionID || job.MachineID != i.Source.MachineID || job.ParentID != i.SourceJobID {
		return i, domain.NativeCodeReviewUnavailable()
	}
	if err := nativeReviewEvidenceAuthority(tx, row, i); err != nil {
		return i, err
	}
	sr, session, err := sessionRecord(tx, row.SessionID)
	if err != nil || session.Archive != domain.NotArchived {
		return i, domain.NativeCodeReviewUnavailable()
	}
	if err := tx.RequireForkActor(i.Actor); err != nil {
		return i, err
	}
	_, machine, err := activeMachine(tx, job.MachineID)
	if err != nil || !slices.Contains(machine.WorkerCapabilities, domain.NativeCodexReviewV1) {
		return i, domain.NativeCodeReviewUnavailable()
	}
	if err := checkedExecutionSource(tx, sr, session, machine, i.Source); err != nil {
		return i, err
	}
	if i.Source.Configuration.Subscription {
		_, account, err := accountFromTx(tx, i.Source.AccountID, 0)
		if err != nil || account.Type != domain.SubscriptionAccount || account.Subscription == nil || account.Subscription.Generation != i.SubscriptionGeneration || account.Subscription.RecoveryRequired {
			return i, domain.NativeCodeReviewUnavailable()
		}
	}
	return i, nil
}

// Evidence publication can acknowledge the original operation after Stop or
// account disconnection. It grants no further native send or credential access.
func nativeReviewEvidenceAuthority(tx *store.Tx, row store.Record, i domain.NativeCodeReviewInput) error {
	_, session, err := sessionRecord(tx, row.SessionID)
	if err != nil || !session.OwnsExecution(i.Source) || session.ActiveExecutionID != "" || !session.WorkspaceAvailable() || session.Recovery != domain.NoRecovery || session.Execution == nil || !session.Execution.CleanupVerified || session.ContextRevision != i.ContextRevision || session.Execution.JobID != i.SourceJobID || session.CompactionJobID != "" || session.PendingSteerID != "" {
		return domain.NativeCodeReviewUnavailable()
	}
	original, err := tx.Get(domain.JobKind, i.SourceJobID)
	if err != nil || original.Revision != i.SourceRevision || original.SessionID != row.SessionID {
		return domain.NativeCodeReviewUnavailable()
	}
	sourceJob, err := store.Decode[domain.Job](original)
	expected, _ := json.Marshal(i.Source)
	if err != nil || sourceJob.Type != domain.ExecuteSessionJob || !sourceJob.State.Terminal() || !bytes.Equal(sourceJob.Input, expected) {
		return domain.NativeCodeReviewUnavailable()
	}
	return nil
}

func (s *Service) CreateNativeCodeReview(ctx context.Context, req *connect.Request[pb.CreateNativeCodeReviewRequest]) (*connect.Response[pb.CreateNativeCodeReviewResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice {
		return nil, rpc.Error(domain.NativeCodeReviewUnavailable(), corr)
	}
	m := req.Msg.Mutation
	if err := validateSessionMutation(m); err != nil {
		return nil, rpc.Error(err, corr)
	}
	target, err := rpc.NativeReviewTarget(req.Msg.Target)
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	identity := struct {
		Session  domain.ID
		Revision uint64
		Target   domain.NativeCodeReviewTarget
		Actor    domain.Principal
	}{domain.ID(m.Id), m.ExpectedRevision, target, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "session.native-code-review", identity, func(tx *store.Tx) (any, error) {
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
		source, err := contextActionSourceForReview(tx, sr, session, domain.ID(m.RequestId), true, true)
		if err != nil {
			return nil, err
		}
		original, err := tx.Get(domain.JobKind, source.SourceJobID)
		if err != nil {
			return nil, err
		}
		input := domain.NativeCodeReviewInput{ContextRevision: session.ContextRevision, Version: 1, ActionID: domain.ID(m.RequestId), SourceJobID: source.SourceJobID, SourceRevision: original.Revision, Source: source.Assignment, Target: target, Actor: actor}
		var manifest workspace.Manifest
		if domain.Decode(input.Source.Manifest, &manifest) != nil {
			return nil, domain.NativeCodeReviewUnavailable()
		}
		selected := false
		for _, r := range manifest.Repositories {
			selected = selected || r.ID == target.RepositoryID
		}
		if !selected {
			return nil, domain.NativeCodeReviewUnavailable()
		}
		if input.Source.Configuration.Subscription {
			_, account, e := accountFromTx(tx, input.Source.AccountID, 0)
			if e != nil || account.Subscription == nil {
				return nil, domain.NativeCodeReviewUnavailable()
			}
			input.SubscriptionGeneration = account.Subscription.Generation
		}
		if input.Validate() != nil {
			return nil, domain.NativeCodeReviewUnavailable()
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		row, err := tx.PutJob(domain.NewID(), 0, sr.ID, sr.ProjectID, domain.Job{Type: domain.NativeCodeReviewJob, State: domain.JobQueued, MachineID: session.MachineID, ParentID: input.SourceJobID, Input: raw, AcceptedAt: time.Now().UTC()})
		if err != nil {
			return nil, err
		}
		return struct{ JobID domain.ID }{row.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	var ref struct{ JobID domain.ID }
	if json.Unmarshal(result.Data, &ref) != nil {
		return nil, rpc.Error(domain.NativeCodeReviewUnavailable(), corr)
	}
	review, err := s.nativeReviewProjection(ctx, identity.Session, ref.JobID)
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	return connect.NewResponse(&pb.CreateNativeCodeReviewResponse{Review: review, RequestId: m.RequestId, Replayed: result.Replayed}), nil
}
func (s *Service) GetNativeCodeReview(ctx context.Context, req *connect.Request[pb.GetNativeCodeReviewRequest]) (*connect.Response[pb.GetNativeCodeReviewResponse], error) {
	value, err := s.nativeReviewProjection(ctx, domain.ID(req.Msg.SessionId), domain.ID(req.Msg.JobId))
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.GetNativeCodeReviewResponse{Review: value}), nil
}
func (s *Service) nativeReviewProjection(ctx context.Context, session, jobID domain.ID) (*pb.NativeCodeReview, error) {
	var value *pb.NativeCodeReview
	if session.Validate() != nil || jobID.Validate() != nil {
		return nil, domain.NativeCodeReviewUnavailable()
	}
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		if _, _, err := sessionRecord(tx, session); err != nil {
			return err
		}
		row, err := tx.Get(domain.JobKind, jobID)
		if err != nil || row.SessionID != session {
			return domain.NativeCodeReviewUnavailable()
		}
		job, err := store.Decode[domain.Job](row)
		var input domain.NativeCodeReviewInput
		if err != nil || job.Type != domain.NativeCodeReviewJob || domain.DecodeNativeCodeReviewInput(job.Input, &input) != nil || input.Validate() != nil {
			return domain.NativeCodeReviewUnavailable()
		}
		value = &pb.NativeCodeReview{Job: rpc.Resource(row), ActionId: string(input.ActionID), Target: rpc.NativeReviewTargetWire(input.Target), State: rpc.NativeReviewStateWire(domain.NativeReviewQueued)}
		observations, usageErr := tx.NativeCodeReviewUsage(row.ID)
		if usageErr != nil {
			return usageErr
		}
		var totals domain.UsageTotals
		var estimates domain.EstimateTotals
		for _, observation := range observations {
			totals.Add(observation.Record.Usage.Counts)
			estimates.Add(observation.Estimate)
		}
		value.Usage, value.Estimates = usageTotals(totals), estimateTotals(estimates)

		progress, found, err := tx.NativeCodeReviewProgress(row.ID)
		if err != nil {
			return err
		}
		if found {
			value.State = rpc.NativeReviewStateWire(progress.State)
			value.Revision = progress.Revision
			value.ThreadId = string(progress.ThreadID)
			value.TurnId = string(progress.TurnID)
			value.EnteredItemId = progress.EnteredItemID
			value.ExitedItemId = progress.ExitedItemID
		}
		var rejected domain.NativeCodeReviewRejectedProof
		if job.State == domain.JobFailed && domain.Decode(job.Output, &rejected) == nil && rejected.Validate() == nil && rejected.ActionID == input.ActionID {
			value.State = rpc.NativeReviewStateWire(domain.NativeReviewRejected)
		} else if job.State == domain.JobSucceeded {
			var result domain.NativeCodeReviewResult
			if domain.Decode(job.Output, &result) != nil || result.Validate() != nil || result.ActionID != input.ActionID || result.Selection.Target != input.Target {
				return domain.NativeCodeReviewUnavailable()
			}
			value.State = rpc.NativeReviewStateWire(domain.NativeReviewCompleted)
			value.Result = rpc.NativeReviewResultWire(result)
			value.UsageComplete = len(observations) > 0
		} else if job.State == domain.JobCanceled {
			value.State = rpc.NativeReviewStateWire(domain.NativeReviewInterrupted)
		} else if job.State == domain.JobFailed || job.State == domain.JobUncertain {
			value.State = rpc.NativeReviewStateWire(domain.NativeReviewUncertain)
			if job.Problem != nil && job.Problem.Code == domain.Conflict {
				value.State = rpc.NativeReviewStateWire(domain.NativeReviewStale)
			}
		}
		return nil
	})
	return value, err
}

func finishNativeCodeReview(tx *store.Tx, row store.Record, job domain.Job, revision uint64, raw []byte, problem *domain.Error) (any, error) {
	if row.Revision != revision {
		return nil, continuationConflict()
	}
	var input domain.NativeCodeReviewInput
	if domain.DecodeNativeCodeReviewInput(job.Input, &input) != nil || input.Validate() != nil {
		return nil, domain.NativeCodeReviewUnavailable()
	}
	progress, found, err := tx.NativeCodeReviewProgress(row.ID)
	if err != nil {
		return nil, err
	}
	var rejected domain.NativeCodeReviewRejectedProof
	if problem == nil && domain.Decode(raw, &rejected) == nil && rejected.Validate() == nil && rejected.ActionID == input.ActionID {
		if found && progress.State != domain.NativeReviewReady {
			return nil, domain.NativeCodeReviewUnavailable()
		}
		job.Output = append(json.RawMessage(nil), raw...)
		job.State = domain.JobFailed
		job.Problem = domain.Fail(domain.Unsupported, "The original native review was rejected before sending.", "Its original no-send and cleanup proofs are verified; refresh the selected target or native profile before a new explicit review.")
	} else if problem == nil {
		var output domain.NativeCodeReviewResult
		if !found || progress.State != domain.NativeReviewExited || domain.Decode(raw, &output) != nil || output.Validate() != nil || output.ActionID != input.ActionID || output.Selection != progress.Selection || output.ThreadID != progress.ThreadID || output.TurnID != progress.TurnID || output.EnteredItemID != progress.EnteredItemID || output.ExitedItemID != progress.ExitedItemID {
			return nil, domain.NativeCodeReviewUnavailable()
		}
		if err := nativeReviewEvidenceAuthority(tx, row, input); err != nil {
			return nil, err
		}
		for _, usage := range output.UsageRecords {
			if usage.ContextRevision != input.ContextRevision || usage.SessionID != row.SessionID || usage.ProjectID != row.ProjectID || usage.AccountID != input.Source.AccountID || usage.ConnectionID != input.Source.ConnectionID || usage.ProviderID != input.Source.Configuration.ProviderID || usage.SubscriptionService != input.Source.Configuration.SubscriptionService || usage.ModelID != input.Source.Configuration.ModelID {
				return nil, domain.NativeCodeReviewUnavailable()
			}
			if _, _, err := tx.PutNativeCodeReviewUsage(row.ID, domain.NewID(), usage); err != nil {
				return nil, err
			}
		}
		job.Output = append(json.RawMessage(nil), raw...)
		job.State = domain.JobSucceeded
	} else {
		// Unknown send/completion/cleanup remains retained and cannot re-enter dispatch.
		job.Problem = problem
		job.State = domain.JobUncertain
		if problem.Code == domain.Conflict {
			job.State = domain.JobFailed
		}
	}
	now := time.Now().UTC()
	job.FinishedAt = &now
	saved, err := tx.PutJob(row.ID, row.Revision, row.SessionID, row.ProjectID, job)
	if err != nil {
		return nil, err
	}
	// A review never supplies a successor conversation checkpoint. Confirmed
	// original cleanup may finish visibility cleanup while preserving Stop.
	if job.State == domain.JobSucceeded || job.State == domain.JobFailed && len(job.Output) > 0 {
		sr, session, err := sessionRecord(tx, row.SessionID)
		if err != nil {
			return nil, err
		}
		if session.Archive == domain.ArchivePending {
			session.Archive, session.Dispatch, session.NextExecutionIntent = domain.Archived, domain.DispatchPaused, ""
			if _, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
				return nil, err
			}
		}
	}
	return store.Record{ID: saved.ID}, nil
}

func (s *Service) PublishNativeCodeReview(ctx context.Context, req *connect.Request[pb.PublishNativeCodeReviewRequest]) (*connect.Response[pb.PublishNativeCodeReviewResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.WorkerDevice || m == nil || domain.ID(m.RequestId).Validate() != nil || domain.ID(m.Id).Validate() != nil {
		return nil, rpc.Error(domain.NativeCodeReviewUnavailable(), corr)
	}
	selection, err := rpc.NativeReviewSelection(req.Msg.Selection)
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	state := rpc.NativeReviewState(req.Msg.State)
	var reportedUsage *domain.NativeResponseUsage
	if req.Msg.Usage != nil {
		u, usageErr := rpc.NativeReviewUsage(req.Msg.Usage)
		if usageErr != nil {
			return nil, rpc.Error(usageErr, corr)
		}
		reportedUsage = &u
	}
	identity := struct {
		Job, Machine, Instance domain.ID
		Revision               uint64
		State                  domain.NativeCodeReviewState
		Selection              domain.NativeCodeReviewSelection
		Thread, Turn           domain.NativeIdentity
		Item                   string
		Actor                  domain.Principal
		Usage                  *pb.NativeCodeReviewResponseUsage
	}{domain.ID(m.Id), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), m.ExpectedRevision, state, selection, domain.NativeIdentity(req.Msg.ThreadId), domain.NativeIdentity(req.Msg.TurnId), req.Msg.ItemId, actor, req.Msg.Usage}
	receipt, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "native-code-review.publish", identity, func(tx *store.Tx) (any, error) {
		row, err := tx.Get(domain.JobKind, identity.Job)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](row)
		if err != nil || row.Revision != identity.Revision || job.State != domain.JobClaimed || job.MachineID != identity.Machine || job.InstanceID != identity.Instance || job.AssignedDeviceID != actor.DeviceID || actor.MachineID != identity.Machine {
			return nil, domain.NativeCodeReviewUnavailable()
		}
		if err := currentInstance(tx, identity.Machine, identity.Instance); err != nil {
			return nil, err
		}
		canceled, err := tx.JobCancellationRequested(row.ID)
		if err != nil || canceled {
			return nil, domain.NativeCodeReviewUnavailable()
		}
		input, err := nativeReviewAuthority(tx, row, job)
		if err != nil || input.Target != identity.Selection.Target {
			return nil, domain.NativeCodeReviewUnavailable()
		}
		grant, err := tx.ExecutionGrantForJob(row.ID)
		if err != nil || s.executionAuthority == nil || grant.ServerEpoch != s.executionAuthority.epoch || grant.ExecutionID != input.ActionID || grant.InstanceID != identity.Instance || grant.DeviceID != actor.DeviceID {
			return nil, domain.NativeCodeReviewUnavailable()
		}
		previous, found, err := tx.NativeCodeReviewProgress(row.ID)
		if err != nil {
			return nil, err
		}
		if reportedUsage != nil {
			if !found || (previous.State != domain.NativeReviewEntered && previous.State != domain.NativeReviewExited) || identity.State != previous.State || identity.Selection != previous.Selection || identity.Thread != previous.ThreadID || identity.Turn != previous.TurnID || identity.Item != "" {
				return nil, domain.NativeCodeReviewUnavailable()
			}
			usage := domain.ResponseUsageRecord{ContextRevision: input.ContextRevision, Purpose: domain.NativeCodeReviewUsage, SessionID: row.SessionID, ProjectID: row.ProjectID, ExecutionID: input.ActionID, AccountID: input.Source.AccountID, ConnectionID: input.Source.ConnectionID, ProviderID: input.Source.Configuration.ProviderID, SubscriptionService: input.Source.Configuration.SubscriptionService, ModelID: input.Source.Configuration.ModelID, Harness: domain.Codex, Version: identity.Usage.NativeVersion, ThreadID: string(identity.Thread), TurnID: string(identity.Turn), Sequence: identity.Usage.Sequence, Usage: *reportedUsage}
			if _, _, err := tx.PutNativeCodeReviewUsage(row.ID, domain.NewID(), usage); err != nil {
				return nil, err
			}
			return struct{ Revision uint64 }{previous.Revision}, nil
		}
		value := domain.NativeCodeReviewProgress{Version: 1, Revision: 1, State: identity.State, Selection: identity.Selection, ThreadID: identity.Thread, TurnID: identity.Turn}
		switch identity.State {
		case domain.NativeReviewReady:
			if found || identity.Item != "" || identity.Turn != "" {
				return nil, domain.NativeCodeReviewUnavailable()
			}
		case domain.NativeReviewEntered:
			if !found || previous.State != domain.NativeReviewReady || previous.Selection != identity.Selection || previous.ThreadID != identity.Thread {
				return nil, domain.NativeCodeReviewUnavailable()
			}
			value.Revision = previous.Revision + 1
			value.EnteredItemID = identity.Item
		case domain.NativeReviewExited:
			if !found || previous.State != domain.NativeReviewEntered || previous.Selection != identity.Selection || previous.ThreadID != identity.Thread || previous.TurnID != identity.Turn {
				return nil, domain.NativeCodeReviewUnavailable()
			}
			value.Revision = previous.Revision + 1
			value.EnteredItemID = previous.EnteredItemID
			value.ExitedItemID = identity.Item
		default:
			return nil, domain.NativeCodeReviewUnavailable()
		}
		if value.Validate() != nil {
			return nil, domain.NativeCodeReviewUnavailable()
		}
		return struct{ Revision uint64 }{value.Revision}, tx.PutNativeCodeReviewProgress(row.ID, value)
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	var result struct{ Revision uint64 }
	if json.Unmarshal(receipt.Data, &result) != nil {
		return nil, rpc.Error(domain.NativeCodeReviewUnavailable(), corr)
	}
	return connect.NewResponse(&pb.PublishNativeCodeReviewResponse{Revision: result.Revision}), nil
}
