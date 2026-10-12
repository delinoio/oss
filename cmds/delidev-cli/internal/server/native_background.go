// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"slices"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func backgroundRecord(tx *store.Tx, session, action domain.ID) (store.Record, domain.NativeBackgroundAction, error) {
	r, e := tx.Get(domain.NativeActionKind, action)
	if e != nil {
		return r, domain.NativeBackgroundAction{}, e
	}
	a, e := store.Decode[domain.NativeBackgroundAction](r)
	if e != nil || a.Validate() != nil || r.SessionID != session || a.SessionID != session || a.ActionID != action {
		return r, a, domain.NativeBackgroundRecovery()
	}
	return r, a, nil
}
func (s *Service) backgroundScope(tx *store.Tx, sr store.Record, session domain.Session, jobID, execution domain.ID, closing ...bool) (store.ExecutionGrant, error) {
	var empty store.ExecutionGrant
	if s.executionAuthority == nil || session.ActiveExecutionID != execution || session.Execution == nil || session.Execution.ExecutionID != execution || session.Execution.JobID != jobID || session.Execution.CleanupVerified || session.IsSidechat() {
		return empty, domain.NativeBackgroundRecovery()
	}
	allowClosure := len(closing) == 1 && closing[0]
	if !allowClosure && session.Execution.Outcome != domain.ExecutionNotStarted && session.Execution.Outcome != domain.ExecutionRunning && session.Execution.Subagents.Closed() {
		return empty, domain.NativeBackgroundRecovery()
	}
	grant, e := tx.ExecutionGrantForJob(jobID)
	if e != nil {
		return grant, e
	}
	if grant.ExecutionID != execution {
		return grant, executionDenied()
	}
	if _, e = s.executionAuthority.originalScope(tx, grant, true); e != nil {
		return grant, e
	}
	if e := requireExecutionStartupReady(tx, jobID); e != nil {
		return grant, e
	}
	jr, e := tx.Get(domain.JobKind, jobID)
	if e != nil {
		return grant, e
	}
	j, e := store.Decode[domain.Job](jr)
	var input domain.ExecutionJobInput
	if e != nil || j.Type != domain.ExecuteSessionJob || domain.Decode(j.Input, &input) != nil || input.Validate() != nil || input.Configuration.Harness != domain.Codex || !session.OwnsExecution(input) || jr.SessionID != sr.ID {
		return grant, executionDenied()
	}
	_, machine, e := activeMachine(tx, session.MachineID)
	if e != nil || !slices.Contains(machine.WorkerCapabilities, domain.CodexNativeBackgroundTerminalsV1) {
		return grant, domain.Fail(domain.Unsupported, "The original Worker has no native background-terminal profile.", "Update the original Codex Worker.")
	}
	return grant, nil
}
func (s *Service) GetNativeBackground(ctx context.Context, req *connect.Request[pb.GetNativeBackgroundRequest]) (*connect.Response[pb.GetNativeBackgroundResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if e := nativeShellActor(ctx); e != nil {
		return nil, rpc.Error(e, corr)
	}
	out := &pb.GetNativeBackgroundResponse{}
	e := s.Store.Read(ctx, func(tx *store.Tx) error {
		if e := tx.Authorize(); e != nil {
			return e
		}
		sr, session, e := sessionRecord(tx, domain.ID(req.Msg.SessionId))
		if e != nil {
			return e
		}
		out.Session = rpc.Resource(sr)
		if i := session.NativeBackground; i != nil && !i.OwnerLost {
			if _, scopeErr := s.backgroundScope(tx, sr, session, i.JobID, i.ExecutionID); scopeErr != nil {
				retained := *i
				retained.OwnerLost = true
				session.NativeBackground = &retained
				out.Session.DocumentJson, _ = json.Marshal(session)
			}
		}
		if req.Msg.ActionId != nil {
			r, _, e := backgroundRecord(tx, sr.ID, domain.ID(*req.Msg.ActionId))
			if e != nil {
				return e
			}
			out.Action = rpc.Resource(r)
		}
		return nil
	})
	if e != nil {
		return nil, rpc.Error(e, corr)
	}
	r := connect.NewResponse(out)
	rpc.CopyCorrelation(r, req.Header())
	return r, nil
}
func (s *Service) RequestNativeBackground(ctx context.Context, req *connect.Request[pb.RequestNativeBackgroundRequest]) (*connect.Response[pb.RequestNativeBackgroundResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if e := nativeShellActor(ctx); e != nil {
		return nil, rpc.Error(e, corr)
	}
	if e := validateSessionMutation(req.Msg.Mutation); e != nil {
		return nil, rpc.Error(e, corr)
	}
	m := req.Msg.Mutation
	actor, _ := domain.PrincipalFrom(ctx)
	op := map[pb.NativeBackgroundOperation]domain.NativeBackgroundOperation{pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_LIST: domain.NativeBackgroundList, pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_TERMINATE: domain.NativeBackgroundTerminate, pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_CLEAN: domain.NativeBackgroundClean}[req.Msg.Operation]
	identity := struct {
		Session, Execution domain.ID
		Revision, Sequence uint64
		Operation          domain.NativeBackgroundOperation
		Item, Process      string
		Actor              domain.Principal
	}{domain.ID(m.Id), domain.ID(req.Msg.ExecutionId), m.ExpectedRevision, req.Msg.InventorySequence, op, req.Msg.ItemId, req.Msg.ProcessId, actor}
	result, e := s.Store.Mutate(ctx, domain.ID(m.RequestId), "native-background.request", identity, func(tx *store.Tx) (any, error) {
		if e := tx.Authorize(); e != nil {
			return nil, e
		}
		sr, session, e := sessionRecord(tx, identity.Session)
		if e != nil {
			return nil, e
		}
		i := session.NativeBackground
		if sr.Revision != identity.Revision || i == nil || i.Validate() != nil || i.OwnerLost || i.ExecutionID != identity.Execution || i.Sequence != identity.Sequence || i.PendingActionID != "" || i.Actions >= domain.MaxNativeBackgroundActions || session.PendingSteerID != "" || session.CompactionJobID != "" {
			return nil, domain.NativeBackgroundRecovery()
		}
		if _, e := s.backgroundScope(tx, sr, session, i.JobID, i.ExecutionID); e != nil {
			return nil, e
		}
		targets := []domain.NativeBackgroundTerminal{}
		for _, t := range i.Terminals {
			if t.Present && (op == domain.NativeBackgroundClean || op == domain.NativeBackgroundTerminate && t.ItemID == identity.Item && t.ProcessID == identity.Process) {
				target := t
				target.Output = ""
				target.OutputTruncated = false
				target.ExitCode = nil
				targets = append(targets, target)
			}
		}
		a := domain.NativeBackgroundAction{Version: 1, ActionType: "native-background", ActionID: domain.ID(m.RequestId), SessionID: sr.ID, JobID: i.JobID, ExecutionID: i.ExecutionID, NativeThreadID: i.NativeThreadID, InventorySequence: i.Sequence, Operation: op, ItemID: identity.Item, ProcessID: identity.Process, Targets: targets, Actor: actor, State: domain.NativeBackgroundQueued}
		if a.Validate() != nil {
			return nil, domain.Fail(domain.Conflict, "The selected native background scope is stale or empty.", "Refresh its retained original inventory and select an exact native item/process pair.")
		}
		r, e := tx.Put(domain.NativeActionKind, a.ActionID, 0, sr.ID, sr.ProjectID, a)
		if e != nil {
			return nil, e
		}
		i.PendingActionID = a.ActionID
		i.Actions++
		if _, e = tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); e != nil {
			return nil, e
		}
		return struct{ ActionID domain.ID }{r.ID}, nil
	})
	if e != nil {
		return nil, rpc.Error(e, corr)
	}
	var ref struct{ ActionID domain.ID }
	if domain.Decode(result.Data, &ref) != nil || ref.ActionID != domain.ID(m.RequestId) {
		return nil, rpc.Error(domain.NativeBackgroundRecovery(), corr)
	}
	var r store.Record
	e = s.Store.Read(ctx, func(tx *store.Tx) error {
		if e := tx.Authorize(); e != nil {
			return e
		}
		var e error
		r, _, e = backgroundRecord(tx, identity.Session, ref.ActionID)
		return e
	})
	if e != nil {
		return nil, rpc.Error(e, corr)
	}
	s.logger.InfoContext(ctx, "native_background_control_queued", "session_id", identity.Session, "action_id", ref.ActionID, "operation", op, "replayed", result.Replayed)
	out := connect.NewResponse(&pb.RequestNativeBackgroundResponse{Action: rpc.Resource(r), RequestId: m.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(out, req.Header())
	return out, nil
}
func (s *Service) currentBackgroundClaim(tx *store.Tx, sessionID, actionID, jobID, machine, instance, device domain.ID) (store.Record, domain.NativeBackgroundAction, error) {
	r, a, e := backgroundRecord(tx, sessionID, actionID)
	if e != nil {
		return r, a, e
	}
	sr, session, e := sessionRecord(tx, sessionID)
	if e != nil {
		return r, a, e
	}
	i := session.NativeBackground
	if i == nil || i.PendingActionID != a.ActionID || i.OwnerLost || i.ExecutionID != a.ExecutionID || i.JobID != jobID || a.JobID != jobID || a.NativeThreadID != i.NativeThreadID {
		return r, a, domain.NativeBackgroundRecovery()
	}
	g, e := s.backgroundScope(tx, sr, session, jobID, a.ExecutionID)
	if e != nil {
		return r, a, e
	}
	if g.MachineID != machine || g.InstanceID != instance || g.DeviceID != device || tx.RequireForkActor(a.Actor) != nil {
		return r, a, executionDenied()
	}
	return r, a, nil
}
func (s *Service) ClaimNativeBackground(ctx context.Context, req *connect.Request[pb.ClaimNativeBackgroundRequest]) (*connect.Response[pb.ClaimNativeBackgroundResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if e := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); e != nil {
		return nil, rpc.Error(e, corr)
	}
	if e := validateSessionMutation(req.Msg.Mutation); e != nil {
		return nil, rpc.Error(e, corr)
	}
	m := req.Msg.Mutation
	actor, _ := domain.PrincipalFrom(ctx)
	machine, instance, job := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.JobId)
	identity := struct {
		Action, Machine, Instance, Job, Device domain.ID
		Revision                               uint64
	}{domain.ID(m.Id), machine, instance, job, actor.DeviceID, m.ExpectedRevision}
	result, e := s.Store.Mutate(ctx, domain.ID(m.RequestId), "native-background.claim", identity, func(tx *store.Tx) (any, error) {
		r, e := tx.Get(domain.NativeActionKind, identity.Action)
		if e != nil {
			return nil, e
		}
		_, a, e := s.currentBackgroundClaim(tx, r.SessionID, r.ID, job, machine, instance, actor.DeviceID)
		if e != nil {
			return nil, e
		}
		if r.Revision != m.ExpectedRevision || a.State != domain.NativeBackgroundQueued {
			return nil, domain.NativeBackgroundRecovery()
		}
		a.State = domain.NativeBackgroundClaimed
		a.Claim = &domain.NativeShellClaim{RequestID: domain.ID(m.RequestId), MachineID: machine, InstanceID: instance, DeviceID: actor.DeviceID}
		if _, e = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, a); e != nil {
			return nil, e
		}
		return struct{ ActionID domain.ID }{r.ID}, nil
	})
	if e != nil {
		return nil, rpc.Error(e, corr)
	}
	var r store.Record
	e = s.Store.Read(ctx, func(tx *store.Tx) error {
		var e error
		r, e = tx.Get(domain.NativeActionKind, identity.Action)
		if e != nil {
			return e
		}
		_, a, e := s.currentBackgroundClaim(tx, r.SessionID, r.ID, job, machine, instance, actor.DeviceID)
		if e != nil {
			return e
		}
		if a.State != domain.NativeBackgroundClaimed || a.Claim == nil || a.Claim.RequestID != domain.ID(m.RequestId) {
			return domain.NativeBackgroundRecovery()
		}
		return nil
	})
	if e != nil {
		return nil, rpc.Error(e, corr)
	}
	return connect.NewResponse(&pb.ClaimNativeBackgroundResponse{Action: rpc.Resource(r), Replayed: result.Replayed}), nil
}
func (s *Service) PublishNativeBackground(ctx context.Context, req *connect.Request[pb.PublishNativeBackgroundRequest]) (*connect.Response[pb.PublishNativeBackgroundResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if e := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); e != nil {
		return nil, rpc.Error(e, corr)
	}
	m := req.Msg.Mutation
	if m == nil || domain.ID(m.Id).Validate() != nil || domain.ID(m.RequestId).Validate() != nil {
		return nil, rpc.Error(domain.NativeBackgroundRecovery(), corr)
	}
	var p domain.NativeBackgroundPublication
	if domain.DecodeWithLimit(req.Msg.ObservationJson, &p, 4<<20) != nil || p.Inventory.Validate() != nil || p.Inventory.PendingActionID != "" || p.Inventory.Actions != 0 {
		return nil, rpc.Error(domain.NativeBackgroundRecovery(), corr)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		Session, Machine, Instance, Job, Device domain.ID
		Sequence                                uint64
		Publication                             domain.NativeBackgroundPublication
	}{domain.ID(m.Id), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), domain.ID(req.Msg.JobId), actor.DeviceID, m.ExpectedRevision, p}
	result, e := s.Store.Mutate(ctx, domain.ID(m.RequestId), "native-background.publish", identity, func(tx *store.Tx) (any, error) {
		sr, session, e := sessionRecord(tx, identity.Session)
		if e != nil {
			return nil, e
		}
		g, e := s.backgroundScope(tx, sr, session, identity.Job, p.Inventory.ExecutionID, p.Inventory.OwnerLost)
		if e != nil {
			return nil, e
		}
		if g.MachineID != identity.Machine || g.InstanceID != identity.Instance || g.DeviceID != identity.Device || p.Inventory.JobID != identity.Job || p.Inventory.NativeThreadID != domain.NativeIdentity(session.Execution.NativeThreadID) {
			return nil, executionDenied()
		}
		i := p.Inventory
		old := session.NativeBackground
		if old == nil || old.ExecutionID != i.ExecutionID {
			if m.ExpectedRevision != 0 || i.Sequence != 1 {
				return nil, domain.NativeBackgroundRecovery()
			}
		} else {
			if old.Sequence != m.ExpectedRevision || domain.CheckNativeBackgroundInventory(*old, i) != nil {
				return nil, domain.NativeBackgroundRecovery()
			}
			i.PendingActionID, i.Actions = old.PendingActionID, old.Actions
		}
		if p.Action != nil {
			if i.PendingActionID != p.Action.ActionID {
				return nil, domain.NativeBackgroundRecovery()
			}
			r, a, e := s.currentBackgroundClaim(tx, sr.ID, p.Action.ActionID, identity.Job, identity.Machine, identity.Instance, identity.Device)
			if e != nil {
				return nil, e
			}
			if a.State != domain.NativeBackgroundClaimed {
				return nil, domain.NativeBackgroundRecovery()
			}
			a.Observation = p.Action
			a.State = domain.NativeBackgroundObserved
			if p.Action.ProblemCode != "" {
				a.State = domain.NativeBackgroundUncertain
				if p.Action.Delivery == domain.NativeBackgroundNotSent {
					a.State = domain.NativeBackgroundRejected
				}
			}
			if a.Validate() != nil || p.Action.NativeEmpty != i.NativeEmpty {
				return nil, domain.NativeBackgroundRecovery()
			}
			if _, e = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, a); e != nil {
				return nil, e
			}
			if (a.State == domain.NativeBackgroundObserved && backgroundSettled(a, i)) || a.State == domain.NativeBackgroundRejected {
				i.PendingActionID = ""
			}
		}
		if p.Action == nil && i.PendingActionID != "" && !i.OwnerLost {
			_, a, e := backgroundRecord(tx, sr.ID, i.PendingActionID)
			if e != nil {
				return nil, e
			}
			if (a.State == domain.NativeBackgroundObserved && backgroundSettled(a, i)) || a.State == domain.NativeBackgroundRejected {
				i.PendingActionID = ""
			}
		}
		session.NativeBackground = &i
		if i.OwnerLost {
			if e := retireNativeBackground(tx, sr, &session); e != nil {
				return nil, e
			}
		}
		if _, e = tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); e != nil {
			return nil, e
		}
		return struct{ ActionID domain.ID }{i.PendingActionID}, nil
	})
	if e != nil {
		return nil, rpc.Error(e, corr)
	}
	out := &pb.PublishNativeBackgroundResponse{Replayed: result.Replayed}
	e = s.Store.Read(ctx, func(tx *store.Tx) error {
		sr, _, e := sessionRecord(tx, identity.Session)
		if e != nil {
			return e
		}
		out.Session = rpc.Resource(sr)
		if p.Action != nil {
			r, _, e := backgroundRecord(tx, sr.ID, p.Action.ActionID)
			if e != nil {
				return e
			}
			out.Action = rpc.Resource(r)
		}
		return nil
	})
	if e != nil {
		return nil, rpc.Error(e, corr)
	}
	s.logger.InfoContext(ctx, "native_background_observation_retained", "session_id", identity.Session, "execution_id", p.Inventory.ExecutionID, "sequence", p.Inventory.Sequence, "native_empty", p.Inventory.NativeEmpty, "replayed", result.Replayed)
	return connect.NewResponse(out), nil
}
func (s *Service) pendingBackground(tx *store.Tx, r store.Record, j domain.Job, sent map[domain.ID]bool) (*pb.NativeBackgroundControl, error) {
	if j.Type != domain.ExecuteSessionJob {
		return nil, nil
	}
	_, session, e := sessionRecord(tx, r.SessionID)
	if e != nil {
		return nil, e
	}
	i := session.NativeBackground
	if i == nil || i.PendingActionID == "" || sent[i.PendingActionID] {
		return nil, nil
	}
	ar, a, e := backgroundRecord(tx, r.SessionID, i.PendingActionID)
	if e != nil {
		return nil, e
	}
	if a.State != domain.NativeBackgroundQueued {
		return nil, nil
	}
	g, e := tx.ExecutionGrantForJob(r.ID)
	if e != nil {
		return nil, e
	}
	if _, _, e = s.currentBackgroundClaim(tx, r.SessionID, a.ActionID, r.ID, j.MachineID, j.InstanceID, g.DeviceID); e != nil {
		return nil, nil
	}
	return &pb.NativeBackgroundControl{JobId: string(r.ID), ActionId: string(ar.ID), Revision: ar.Revision}, nil
}

// Owner loss is terminal for delivery authority. Retained facts cannot become
// another send claim after reconnect, Stop, restore, or ordinary completion.
func retireNativeBackground(tx *store.Tx, sr store.Record, session *domain.Session) error {
	i := session.NativeBackground
	if i == nil {
		return nil
	}
	i.OwnerLost = true
	if i.PendingActionID == "" {
		return nil
	}
	r, a, e := backgroundRecord(tx, sr.ID, i.PendingActionID)
	if e != nil {
		return e
	}
	if a.State == domain.NativeBackgroundQueued {
		a.State = domain.NativeBackgroundCanceled
		i.PendingActionID = ""
	} else if a.State == domain.NativeBackgroundClaimed {
		a.State = domain.NativeBackgroundUncertain
		a.Observation = &domain.NativeBackgroundObservation{ActionID: a.ActionID, ProblemCode: domain.RecoveryRequired, Delivery: domain.NativeBackgroundSendUnknown}
	}
	if a.Validate() != nil {
		return domain.NativeBackgroundRecovery()
	}
	_, e = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, a)
	return e
}

func backgroundSettled(a domain.NativeBackgroundAction, i domain.NativeBackgroundInventory) bool {
	if a.Operation == domain.NativeBackgroundList {
		return true
	}
	if a.Observation == nil || !a.Observation.Acknowledged {
		return false
	}
	if a.Operation == domain.NativeBackgroundClean {
		return i.NativeEmpty
	}
	if a.Observation.Terminated != nil && !*a.Observation.Terminated {
		return true
	}
	for _, t := range i.Terminals {
		if t.ItemID == a.ItemID && t.ProcessID == a.ProcessID {
			return !t.Present
		}
	}
	return false
}
