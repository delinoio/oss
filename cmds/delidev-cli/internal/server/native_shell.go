// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"slices"
	"time"
)

func nativeShellActor(ctx context.Context) error {
	a, ok := domain.PrincipalFrom(ctx)
	if !ok || a.Type != domain.OwnerDevice && a.Type != domain.ClientDevice {
		return domain.Fail(domain.PermissionDenied, "Native shell execution requires an authenticated human action.", "Use the native-shell CLI or Connect action and confirm full access explicitly.")
	}
	return nil
}
func nativeShellRecord(tx *store.Tx, session, action domain.ID) (store.Record, domain.NativeShellAction, error) {
	r, err := tx.Get(domain.NativeActionKind, action)
	if err != nil {
		return r, domain.NativeShellAction{}, err
	}
	a, err := store.Decode[domain.NativeShellAction](r)
	if err != nil || a.Validate() != nil || r.SessionID != session || a.SessionID != session || a.ActionID != action {
		return r, a, domain.NativeShellRecovery()
	}
	return r, a, nil
}
func (s *Service) readNativeShell(ctx context.Context, session, action domain.ID) (store.Record, error) {
	if session.Validate() != nil || action.Validate() != nil {
		return store.Record{}, domain.NativeShellRecovery()
	}
	var r store.Record
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		r, _, err = nativeShellRecord(tx, session, action)
		return err
	})
	return r, err
}
func (s *Service) RunNativeShell(ctx context.Context, req *connect.Request[pb.RunNativeShellRequest]) (*connect.Response[pb.RunNativeShellResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if err := nativeShellActor(ctx); err != nil {
		return nil, rpc.Error(err, corr)
	}
	m := req.Msg.Mutation
	if err := validateSessionMutation(m); err != nil {
		return nil, rpc.Error(err, corr)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	command := domain.NativeShellCommand{Command: req.Msg.Command, FullAccess: req.Msg.Access == pb.NativeShellAccess_NATIVE_SHELL_ACCESS_FULL_ACCESS, TimeoutMS: req.Msg.TimeoutMs, ActorDeviceID: actor.DeviceID}
	if err := command.Validate(); err != nil {
		return nil, rpc.Error(err, corr)
	}
	identity := struct {
		Session  domain.ID
		Revision uint64
		Actor    domain.Principal
		Command  domain.NativeShellCommand
	}{domain.ID(m.Id), m.ExpectedRevision, actor, command}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "native-shell.run", identity, func(tx *store.Tx) (any, error) {
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
		input, err := contextActionSource(tx, sr, session, domain.ID(m.RequestId), false, true)
		if err != nil {
			return nil, err
		}
		_, machine, err := activeMachine(tx, session.MachineID)
		if err != nil {
			return nil, err
		}
		if input.Assignment.Configuration.Harness != domain.Codex || session.IsSidechat() || !slices.Contains(machine.WorkerCapabilities, domain.CodexNativeShellV1) {
			return nil, domain.Fail(domain.Unsupported, "The original Worker has no native-shell profile.", "Update the original Codex Worker; product terminals are independent.")
		}
		input.Version, input.NativeShell = 5, &command
		if input.Validate() != nil {
			return nil, domain.NativeShellRecovery()
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		job, err := tx.PutJob(domain.NewID(), 0, sr.ID, sr.ProjectID, domain.Job{Type: domain.CompactSessionJob, State: domain.JobQueued, MachineID: session.MachineID, ParentID: input.SourceJobID, Input: raw, AcceptedAt: time.Now().UTC()})
		if err != nil {
			return nil, err
		}
		action := domain.NativeShellAction{Version: 1, ActionID: input.ActionID, JobID: job.ID, SessionID: sr.ID, ExecutionID: input.Assignment.ExecutionID, Command: command, Disclosure: domain.NativeShellDisclosure, State: domain.NativeShellQueued}
		if _, err := tx.Put(domain.NativeActionKind, action.ActionID, 0, sr.ID, sr.ProjectID, action); err != nil {
			return nil, err
		}
		session.CompactionJobID, session.LastNativeShellJobID = job.ID, job.ID
		session.Dispatch = domain.DispatchPaused
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return struct{ ActionID domain.ID }{action.ActionID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	var ref struct{ ActionID domain.ID }
	if domain.Decode(result.Data, &ref) != nil {
		return nil, rpc.Error(domain.NativeShellRecovery(), corr)
	}
	record, err := s.readNativeShell(ctx, identity.Session, ref.ActionID)
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	s.logger.InfoContext(ctx, "native_shell_accepted", "session_id", m.Id, "action_id", ref.ActionID, "replayed", result.Replayed, "access", "full-access")
	response := connect.NewResponse(&pb.RunNativeShellResponse{Action: rpc.Resource(record), RequestId: m.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) GetNativeShell(ctx context.Context, req *connect.Request[pb.GetNativeShellRequest]) (*connect.Response[pb.GetNativeShellResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if err := nativeShellActor(ctx); err != nil {
		return nil, rpc.Error(err, corr)
	}
	r, err := s.readNativeShell(ctx, domain.ID(req.Msg.SessionId), domain.ID(req.Msg.ActionId))
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	response := connect.NewResponse(&pb.GetNativeShellResponse{Action: rpc.Resource(r)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) CancelNativeShell(ctx context.Context, req *connect.Request[pb.CancelNativeShellRequest]) (*connect.Response[pb.CancelNativeShellResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if err := nativeShellActor(ctx); err != nil {
		return nil, rpc.Error(err, corr)
	}
	m := req.Msg.Mutation
	if err := validateSessionMutation(m); err != nil {
		return nil, rpc.Error(err, corr)
	}
	sessionID := domain.ID(req.Msg.SessionId)
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		Session, Action domain.ID
		Revision        uint64
		Actor           domain.Principal
	}{sessionID, domain.ID(m.Id), m.ExpectedRevision, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "native-shell.cancel", identity, func(tx *store.Tx) (any, error) {
		if err := tx.Authorize(); err != nil {
			return nil, err
		}
		ar, a, err := nativeShellRecord(tx, sessionID, identity.Action)
		if err != nil {
			return nil, err
		}
		if ar.Revision != identity.Revision {
			return nil, continuationConflict()
		}
		jr, err := tx.Get(domain.JobKind, a.JobID)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](jr)
		if err != nil {
			return nil, err
		}
		sr, session, err := sessionRecord(tx, sessionID)
		if err != nil {
			return nil, err
		}
		if session.CompactionJobID != a.JobID || job.Type != domain.CompactSessionJob || job.State != domain.JobQueued && job.State != domain.JobClaimed {
			return nil, domain.NativeShellRecovery()
		}
		if err := tx.RequestJobCancellation(a.JobID); err != nil {
			return nil, err
		}
		if job.State == domain.JobQueued {
			now := time.Now().UTC()
			job.State, job.FinishedAt = domain.JobCanceled, &now
			job.Problem = domain.Fail(domain.Canceled, "The native shell action was canceled before dispatch.", "No command was sent.")
			if _, err := tx.PutJob(jr.ID, jr.Revision, jr.SessionID, jr.ProjectID, job); err != nil {
				return nil, err
			}
			a.State = domain.NativeShellCanceled
			session.CompactionJobID = ""
			session.Dispatch = domain.DispatchReady
			if _, err := tx.Put(domain.NativeActionKind, ar.ID, ar.Revision, ar.SessionID, ar.ProjectID, a); err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
				return nil, err
			}
		}
		return struct{ ActionID domain.ID }{a.ActionID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	r, err := s.readNativeShell(ctx, sessionID, identity.Action)
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	response := connect.NewResponse(&pb.CancelNativeShellResponse{Action: rpc.Resource(r), RequestId: m.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) currentNativeShellWorker(tx *store.Tx, action, machine, instance, device domain.ID) (store.Record, domain.NativeShellAction, domain.SessionCompactionInput, error) {
	ar, err := tx.Get(domain.NativeActionKind, action)
	if err != nil {
		return ar, domain.NativeShellAction{}, domain.SessionCompactionInput{}, err
	}
	ar, a, err := nativeShellRecord(tx, ar.SessionID, action)
	if err != nil {
		return ar, a, domain.SessionCompactionInput{}, err
	}
	jr, err := tx.Get(domain.JobKind, a.JobID)
	if err != nil {
		return ar, a, domain.SessionCompactionInput{}, err
	}
	j, err := store.Decode[domain.Job](jr)
	var i domain.SessionCompactionInput
	if err != nil || j.Type != domain.CompactSessionJob || j.State != domain.JobClaimed || j.MachineID != machine || j.InstanceID != instance || j.AssignedDeviceID != device || domain.DecodeCompactionInput(j.Input, &i) != nil || i.Validate() != nil || i.NativeShell == nil || i.ActionID != action {
		return ar, a, i, domain.NativeShellRecovery()
	}
	if err := currentInstance(tx, machine, instance); err != nil {
		return ar, a, i, err
	}
	_, session, err := sessionRecord(tx, ar.SessionID)
	if err != nil || session.CompactionJobID != a.JobID || !session.OwnsExecution(i.Assignment) {
		return ar, a, i, domain.NativeShellRecovery()
	}
	return ar, a, i, nil
}
func (s *Service) ClaimNativeShell(ctx context.Context, req *connect.Request[pb.ClaimNativeShellRequest]) (*connect.Response[pb.ClaimNativeShellResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, corr)
	}
	m := req.Msg.Mutation
	if err := validateSessionMutation(m); err != nil {
		return nil, rpc.Error(err, corr)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	claim := domain.NativeShellClaim{RequestID: domain.ID(m.RequestId), MachineID: domain.ID(req.Msg.MachineId), InstanceID: domain.ID(req.Msg.InstanceId), DeviceID: actor.DeviceID}
	identity := struct {
		Action   domain.ID
		Revision uint64
		Claim    domain.NativeShellClaim
	}{domain.ID(m.Id), m.ExpectedRevision, claim}
	result, err := s.Store.Mutate(ctx, claim.RequestID, "native-shell.claim", identity, func(tx *store.Tx) (any, error) {
		if err := tx.Authorize(); err != nil {
			return nil, err
		}
		ar, a, i, err := s.currentNativeShellWorker(tx, identity.Action, claim.MachineID, claim.InstanceID, claim.DeviceID)
		if err != nil {
			return nil, err
		}
		sr, session, e := sessionRecord(tx, ar.SessionID)
		_, machine, me := activeMachine(tx, claim.MachineID)
		if e != nil || me != nil || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || !session.WorkspaceAvailable() || !slices.Contains(machine.WorkerCapabilities, domain.CodexNativeShellV1) || checkedExecutionSource(tx, sr, session, machine, i.Assignment) != nil {
			return nil, domain.NativeShellRecovery()
		}
		canceled, err := tx.JobCancellationRequested(a.JobID)
		if err != nil || canceled || ar.Revision != identity.Revision || a.State != domain.NativeShellQueued || a.Claim != nil {
			return nil, domain.NativeShellRecovery()
		}
		a.Claim = &claim
		a.State = domain.NativeShellClaimed
		a.Observation = &domain.NativeShellObservation{ActionID: a.ActionID, SessionID: a.SessionID, ExecutionID: a.ExecutionID, NativeThreadID: i.Completion.NativeThreadID, State: domain.NativeShellClaimed, Sequence: 1}
		_, err = tx.Put(domain.NativeActionKind, ar.ID, ar.Revision, ar.SessionID, ar.ProjectID, a)
		return struct{ ActionID domain.ID }{a.ActionID}, err
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	var record store.Record
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		r, a, _, err := s.currentNativeShellWorker(tx, identity.Action, claim.MachineID, claim.InstanceID, claim.DeviceID)
		if err != nil || a.Claim == nil || *a.Claim != claim {
			return domain.NativeShellRecovery()
		}
		canceled, err := tx.JobCancellationRequested(a.JobID)
		if err != nil || canceled {
			return domain.NativeShellRecovery()
		}
		record = r
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	response := connect.NewResponse(&pb.ClaimNativeShellResponse{Action: rpc.Resource(record), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) PublishNativeShell(ctx context.Context, req *connect.Request[pb.PublishNativeShellRequest]) (*connect.Response[pb.PublishNativeShellResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, corr)
	}
	m := req.Msg.Mutation
	if err := validateSessionMutation(m); err != nil {
		return nil, rpc.Error(err, corr)
	}
	var observation domain.NativeShellObservation
	if domain.Decode(req.Msg.ObservationJson, &observation) != nil || observation.Validate() != nil || observation.CleanupVerified {
		return nil, rpc.Error(domain.NativeShellRecovery(), corr)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		Action, Machine, Instance, Device domain.ID
		Revision                          uint64
		Observation                       domain.NativeShellObservation
	}{domain.ID(m.Id), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), actor.DeviceID, m.ExpectedRevision, observation}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "native-shell.publish", identity, func(tx *store.Tx) (any, error) {
		if err := tx.Authorize(); err != nil {
			return nil, err
		}
		ar, a, _, err := s.currentNativeShellWorker(tx, identity.Action, identity.Machine, identity.Instance, identity.Device)
		if err != nil {
			return nil, err
		}
		if ar.Revision != identity.Revision || a.Claim == nil || a.Observation == nil || domain.CheckNativeShellTransition(*a.Observation, observation) != nil {
			return nil, domain.NativeShellRecovery()
		}
		a.Observation = &observation
		a.State = observation.State
		_, err = tx.Put(domain.NativeActionKind, ar.ID, ar.Revision, ar.SessionID, ar.ProjectID, a)
		return struct{ ActionID domain.ID }{a.ActionID}, err
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	r, err := s.readNativeShell(ctx, observation.SessionID, identity.Action)
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	response := connect.NewResponse(&pb.PublishNativeShellResponse{Action: rpc.Resource(r), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func finishNativeShell(tx *store.Tx, r store.Record, j domain.Job, revision uint64, raw json.RawMessage, problem *domain.Error) (store.Record, error) {
	var input domain.SessionCompactionInput
	if domain.DecodeCompactionInput(j.Input, &input) != nil || input.Validate() != nil || input.NativeShell == nil {
		return store.Record{}, domain.NativeShellRecovery()
	}
	sr, session, err := sessionRecord(tx, r.SessionID)
	if err != nil || session.CompactionJobID != r.ID || !session.OwnsExecution(input.Assignment) {
		return store.Record{}, domain.NativeShellRecovery()
	}
	ar, action, err := nativeShellRecord(tx, r.SessionID, input.ActionID)
	if err != nil {
		return store.Record{}, err
	}
	var result domain.NativeShellResult
	verified := problem == nil && domain.Decode(raw, &result) == nil && result.Validate() == nil && result.Observation.ActionID == action.ActionID && result.Observation.SessionID == action.SessionID && result.Observation.ExecutionID == action.ExecutionID && result.Observation.NativeThreadID == input.Completion.NativeThreadID && action.Observation != nil && domain.CheckNativeShellTransition(*action.Observation, result.Observation) == nil && (result.History == nil || result.History.JobID == r.ID)
	now := time.Now().UTC()
	j.FinishedAt = &now
	if !verified {
		action.State = domain.NativeShellUncertain
		if action.Observation != nil {
			copy := *action.Observation
			copy.State = domain.NativeShellUncertain
			copy.CleanupVerified = false
			action.Observation = &copy
		}
		j.State, j.Problem = domain.JobUncertain, domain.NativeShellRecovery()
		session.Recovery = domain.NeedsRecovery
		session.Dispatch = domain.DispatchPaused
	} else {
		action.State = result.Observation.State
		action.Observation = &result.Observation
		j.Output = raw
		j.State = domain.JobSucceeded
		j.Problem = nil
		session.CompactionJobID = ""
		session.NativeShell = result.History
		if result.History == nil {
			session.Dispatch = domain.DispatchPaused
			session.Recovery = domain.NeedsRecovery
		} else if session.Archive == domain.NotArchived && session.Recovery == domain.NoRecovery {
			session.Dispatch, session.NextExecutionIntent = input.Dispatch, input.Intent
		}
		if result.Observation.State == domain.NativeShellCanceled {
			j.State = domain.JobCanceled
			session.Dispatch = domain.DispatchPaused
			session.NextExecutionIntent = ""
		}
		if session.Archive == domain.ArchivePending && session.Recovery == domain.NoRecovery {
			session.Archive = domain.Archived
		}
	}
	if _, err := tx.Put(domain.NativeActionKind, ar.ID, ar.Revision, ar.SessionID, ar.ProjectID, action); err != nil {
		return store.Record{}, err
	}
	if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
		return store.Record{}, err
	}
	saved, err := tx.PutJob(r.ID, revision, r.SessionID, r.ProjectID, j)
	return store.Record{ID: saved.ID}, err
}

// The shared job lifecycle must retain the original native receipt even when
// Stop, Archive, revocation, or Worker loss handles the durable job first.
func settleLostNativeShell(tx *store.Tx, r store.Record, j domain.Job) error {
	var input domain.SessionCompactionInput
	if domain.DecodeCompactionInput(j.Input, &input) != nil || input.Validate() != nil {
		return domain.CompactionUncertain()
	}
	if input.NativeShell == nil {
		return nil
	}
	ar, action, err := nativeShellRecord(tx, r.SessionID, input.ActionID)
	if err != nil {
		return err
	}
	if j.State == domain.JobCanceled && action.Claim == nil {
		action.State = domain.NativeShellCanceled
	} else {
		action.State = domain.NativeShellUncertain
		if action.Observation != nil {
			retained := *action.Observation
			retained.State = domain.NativeShellUncertain
			retained.CleanupVerified = false
			action.Observation = &retained
		}
	}
	_, err = tx.Put(domain.NativeActionKind, ar.ID, ar.Revision, ar.SessionID, ar.ProjectID, action)
	return err
}
