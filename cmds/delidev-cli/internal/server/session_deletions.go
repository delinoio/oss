// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func sessionDeletionMessage(v store.SessionDeletion) *pb.SessionDeletionJob {
	r := &pb.SessionDeletionJob{Id: string(v.ID), SessionId: string(v.SessionID), Revision: v.Revision, State: pb.SessionDeletionState_SESSION_DELETION_STATE_PENDING, AcceptedAt: v.AcceptedAt.Format(time.RFC3339Nano), DatabaseRemoved: v.DatabaseRemoved, BackupsRemoved: v.BackupsRemoved}
	for _, w := range v.Workers {
		if !w.Acknowledged {
			r.WorkersPending++
		}
	}
	if v.FinishedAt != nil {
		r.State = pb.SessionDeletionState_SESSION_DELETION_STATE_SUCCEEDED
		r.FinishedAt = v.FinishedAt.Format(time.RFC3339Nano)
	}
	return r
}
func (s *Service) DeleteSession(ctx context.Context, req *connect.Request[pb.DeleteSessionRequest]) (*connect.Response[pb.DeleteSessionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	if e := s.authorizeBackups(ctx); e != nil {
		return nil, rpc.Error(e, c)
	}
	m := req.Msg.Mutation
	if m == nil {
		return nil, rpc.Error(domain.Fail(domain.MissingInput, "Deletion requires the exact session revision.", "Supply the original request, session ID and revision."), c)
	}
	v, replay, e := s.Store.DeleteSession(ctx, domain.ID(m.RequestId), domain.ID(m.Id), s.Identity.ServerID, m.ExpectedRevision)
	if e != nil {
		return nil, rpc.Error(e, c)
	}
	s.logger.InfoContext(ctx, "session_deletion_accepted", "session_id", v.SessionID, "deletion_id", v.ID, "replayed", replay, "correlation_id", c)
	r := connect.NewResponse(&pb.DeleteSessionResponse{Job: sessionDeletionMessage(v), RequestId: m.RequestId, Replayed: replay})
	rpc.CopyCorrelation(r, req.Header())
	return r, nil
}
func (s *Service) GetSessionDeletion(ctx context.Context, req *connect.Request[pb.GetSessionDeletionRequest]) (*connect.Response[pb.GetSessionDeletionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	if e := s.authorizeBackups(ctx); e != nil {
		return nil, rpc.Error(e, c)
	}
	v, e := s.Store.GetSessionDeletion(ctx, domain.ID(req.Msg.SessionId))
	if e != nil {
		return nil, rpc.Error(e, c)
	}
	if e := s.authorizeBackups(ctx); e != nil {
		return nil, rpc.Error(e, c)
	}
	r := connect.NewResponse(&pb.GetSessionDeletionResponse{Job: sessionDeletionMessage(v)})
	rpc.CopyCorrelation(r, req.Header())
	return r, nil
}
func (s *Service) ListSessionDeletionWork(ctx context.Context, req *connect.Request[pb.ListSessionDeletionWorkRequest]) (*connect.Response[pb.ListSessionDeletionWorkResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.WorkerDevice || actor.MachineID != domain.ID(req.Msg.MachineId) {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Deletion work is owning-Worker-only.", "Use the original paired Worker."), c)
	}
	check := func(tx *store.Tx) error {
		if e := tx.Authorize(); e != nil {
			return e
		}
		return currentInstance(tx, actor.MachineID, domain.ID(req.Msg.InstanceId))
	}
	if e := s.Store.Read(ctx, check); e != nil {
		return nil, rpc.Error(e, c)
	}
	if req.Msg.AfterSessionId != "" && domain.ID(req.Msg.AfterSessionId).Validate() != nil {
		return nil, rpc.Error(domain.SessionDeletionPending(), c)
	}
	items, e := s.Store.SessionDeletions(ctx)
	if e != nil {
		return nil, rpc.Error(e, c)
	}
	out := &pb.ListSessionDeletionWorkResponse{}
	size := 0
	for _, v := range items {
		if string(v.SessionID) <= req.Msg.AfterSessionId {
			continue
		}
		// Workspace removal may run on a separate Worker lane. Do not dispatch
		// it until the original terminal lane has joined every owned process.
		// Deletion admission is already closed, so a clean terminal cannot gain
		// a replacement shell while this immutable work is in transit.
		var terminalsPending bool
		if e := s.Store.Read(ctx, func(tx *store.Tx) error {
			if e := check(tx); e != nil {
				return e
			}
			var e error
			terminalsPending, e = tx.SessionTerminalsPending(v.SessionID)
			return e
		}); e != nil {
			return nil, rpc.Error(e, c)
		}
		if terminalsPending {
			continue
		}
		for _, w := range v.Workers {
			if w.Work.DeviceID == actor.DeviceID && w.Work.MachineID == actor.MachineID && !w.Acknowledged {
				b, _ := json.Marshal(w.Work)
				if len(out.WorkJson) == 20 || size+len(b) > 1<<20 {
					if len(out.WorkJson) == 0 {
						return nil, rpc.Error(domain.SessionDeletionPending(), c)
					}
					out.NextSessionId = string(lastDeletionSession(out.WorkJson))
					break
				}
				out.WorkJson = append(out.WorkJson, b)
				size += len(b)
			}
		}
		if out.NextSessionId != "" {
			break
		}
	}
	if e := s.Store.Read(ctx, check); e != nil {
		return nil, rpc.Error(e, c)
	}
	r := connect.NewResponse(out)
	rpc.CopyCorrelation(r, req.Header())
	return r, nil
}
func lastDeletionSession(items [][]byte) domain.ID {
	var w domain.SessionDeletionWork
	_ = json.Unmarshal(items[len(items)-1], &w)
	return w.SessionID
}

func (s *Service) ReportSessionDeletion(ctx context.Context, req *connect.Request[pb.ReportSessionDeletionRequest]) (*connect.Response[pb.ReportSessionDeletionResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.WorkerDevice || actor.MachineID != domain.ID(req.Msg.MachineId) {
		return nil, rpc.Error(domain.SessionDeletionPending(), c)
	}
	v, e := s.Store.AcknowledgeSessionDeletion(ctx, domain.ID(req.Msg.SessionId), domain.ID(req.Msg.DeletionId), domain.ID(req.Msg.RequestId), domain.ID(req.Msg.InstanceId), req.Msg.WorkDigest)
	if e != nil {
		return nil, rpc.Error(e, c)
	}
	s.logger.InfoContext(ctx, "session_deletion_worker_acknowledged", "session_id", v.SessionID, "deletion_id", v.ID, "device_id", actor.DeviceID, "correlation_id", c)
	r := connect.NewResponse(&pb.ReportSessionDeletionResponse{Job: sessionDeletionMessage(v)})
	rpc.CopyCorrelation(r, req.Header())
	return r, nil
}

func (s *Service) runSessionDeletions(parent context.Context) {
	ctx := domain.WithPrincipal(parent, domain.Principal{Type: domain.OwnerDevice})
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if s.Store.SessionDeletionRecoveryRequired() {
			if e := s.Store.RestoreSessionDeletionIntents(ctx, s.Identity.ServerID); e != nil {
				s.logger.WarnContext(ctx, "session_deletion_intent_recovery_failed", "code", domain.SafeError(e).Code)
			}
		}
		items, e := s.Store.SessionDeletions(ctx)
		if e != nil && ctx.Err() == nil {
			s.logger.WarnContext(ctx, "session_deletion_scan_failed", "code", domain.SafeError(e).Code)
		}
		for _, v := range items {
			if ctx.Err() != nil {
				return
			}
			pending := false
			for _, w := range v.Workers {
				if !w.Acknowledged {
					pending = true
				}
			}
			if pending {
				continue
			}
			bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
			current, e := s.Store.PurgeDeletedSession(bounded, v.SessionID)
			if e == nil {
				e = s.Store.RemoveSessionBackups(bounded, current)
			}
			if e == nil {
				current, e = s.Store.CompleteSessionDeletion(bounded, v.SessionID)
			}
			cancel()
			if e != nil && ctx.Err() == nil {
				s.logger.WarnContext(ctx, "session_deletion_pending", "deletion_id", v.ID, "code", domain.SafeError(e).Code)
			} else if e == nil && current.Revision != v.Revision {
				s.logger.InfoContext(ctx, "session_deletion_progress", "deletion_id", v.ID, "revision", current.Revision, "completed", current.FinishedAt != nil)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
