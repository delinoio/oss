package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (s *Service) RecoverSessionWorkspace(ctx context.Context, req *connect.Request[pb.RecoverSessionWorkspaceRequest]) (*connect.Response[pb.RecoverSessionWorkspaceResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	meta := req.Msg.Mutation
	if err := validateSessionMutation(meta); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	identity := struct {
		ID       string
		Revision uint64
		Cleanup  bool
	}{meta.Id, meta.ExpectedRevision, req.Msg.Cleanup}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "session.workspace.recover", identity, func(tx *store.Tx) (any, error) {
		r, session, err := sessionRecord(tx, domain.ID(meta.Id))
		if err != nil {
			return nil, err
		}
		if r.Revision != meta.ExpectedRevision {
			return nil, domain.Fail(domain.Conflict, "The session revision changed.", "Reload before requesting workspace recovery.")
		}
		if session.Preparation == nil || session.Preparation.State != domain.PreparationUncertain || session.Outcome != domain.ExecutionNotStarted || session.ActiveExecutionID != "" || session.Archive == domain.Archived {
			return nil, domain.Fail(domain.Conflict, "This session has no uncertain preparation to recover.", "Inspect its current native ownership and workspace state.")
		}
		if _, _, err := activeMachine(tx, session.MachineID); err != nil {
			return nil, err
		}
		action := workspace.InspectPreparation
		if req.Msg.Cleanup {
			action = workspace.CleanupPreparation
		}
		if session.Preparation.RecoveryJobID != "" {
			previous, err := tx.Get(domain.JobKind, session.Preparation.RecoveryJobID)
			if err != nil {
				return nil, err
			}
			job, err := store.Decode[domain.Job](previous)
			if err != nil {
				return nil, err
			}
			if job.State == domain.JobQueued || job.State == domain.JobClaimed {
				var input workspace.RecoveryRequest
				if err := domain.Decode(job.Input, &input); err != nil {
					return nil, err
				}
				if input.Action != action {
					return nil, domain.Fail(domain.Conflict, "A different workspace recovery is already pending.", "Wait for its result before changing the requested recovery action.")
				}
				return sessionReceipt{SessionID: r.ID}, nil
			}
		}
		original, err := tx.Get(domain.JobKind, session.Preparation.JobID)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](original)
		if err != nil {
			return nil, err
		}
		if job.State != domain.JobUncertain || job.Type != domain.PrepareWorkspaceJob || original.SessionID != r.ID || job.MachineID != session.MachineID {
			return nil, workspace.ResultUncertain()
		}
		assigned, err := tx.JobAssignment(original.ID)
		if err != nil {
			return nil, err
		}
		claim, err := store.Decode[domain.Job](assigned)
		if err != nil {
			return nil, err
		}
		if assigned.SessionID != r.ID || claim.Type != domain.PrepareWorkspaceJob || claim.State != domain.JobClaimed || claim.MachineID != session.MachineID || claim.InstanceID != job.InstanceID {
			return nil, workspace.ResultUncertain()
		}
		var preparation workspace.PrepareRequest
		if err := domain.Decode(claim.Input, &preparation); err != nil {
			return nil, err
		}
		digest := sha256.Sum256(assigned.Data)
		input := workspace.RecoveryRequest{JobID: original.ID, InstanceID: claim.InstanceID, Revision: assigned.Revision, AssignmentDigest: hex.EncodeToString(digest[:]), Preparation: preparation, Action: action}
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		recoveryID := domain.NewID()
		if _, err := tx.PutJob(recoveryID, 0, r.ID, r.ProjectID, domain.Job{Type: domain.RecoverWorkspaceJob, State: domain.JobQueued, MachineID: session.MachineID, ParentID: original.ID, Input: raw, AcceptedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		session.Preparation.RecoveryJobID = recoveryID
		session.Recovery = domain.Reconciling
		session.Dispatch = domain.DispatchPaused
		if _, err := tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, session); err != nil {
			return nil, err
		}
		return sessionReceipt{SessionID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	change, err := s.sessionResult(ctx, result)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.Info("session_workspace_recovery_accepted", "session_id", meta.Id, "cleanup", req.Msg.Cleanup, "replayed", result.Replayed, "correlation_id", correlation)
	response := connect.NewResponse(&pb.RecoverSessionWorkspaceResponse{Change: change})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func finishWorkspaceRecovery(tx *store.Tx, record store.Record, job domain.Job) error {
	r, session, err := sessionRecord(tx, record.SessionID)
	if err != nil {
		return err
	}
	if session.Preparation == nil || session.Preparation.RecoveryJobID != record.ID || session.Preparation.JobID != job.ParentID {
		return nil
	}
	if job.State == domain.JobQueued || job.State == domain.JobClaimed {
		return nil
	}
	session.Dispatch = domain.DispatchPaused
	if job.State != domain.JobSucceeded {
		session.Recovery = domain.NeedsRecovery
		session.Problem = domain.Fail(domain.RecoveryRequired, "Workspace recovery did not confirm a usable or fully cleaned preparation.", "Inspect the recovery job and retry explicitly on the owning Worker.")
		_, err = tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, session)
		return err
	}
	var result workspace.RecoveryResult
	if err := domain.Decode(job.Output, &result); err != nil {
		return err
	}
	original, err := tx.Get(domain.JobKind, job.ParentID)
	if err != nil {
		return err
	}
	preparation, err := store.Decode[domain.Job](original)
	if err != nil {
		return err
	}
	if preparation.State != domain.JobUncertain || original.SessionID != r.ID {
		return workspace.ResultUncertain()
	}
	now := time.Now().UTC()
	preparation.FinishedAt = &now
	switch result.Outcome {
	case workspace.RecoveredReady:
		preparation.State = domain.JobSucceeded
		preparation.Problem = nil
		preparation.Output, err = json.Marshal(result.Manifest)
		if err != nil {
			return err
		}
		session.Preparation.State = domain.PreparationReady
		session.Problem = domain.InitialExecutionPending()
	case workspace.RecoveredClean:
		preparation.State = domain.JobCanceled
		preparation.Output = nil
		preparation.Problem = domain.Fail(domain.Canceled, "The incomplete workspace preparation has been fully cleaned.", "Prepare explicitly to start another attempt; input remains paused.")
		session.Preparation.State = domain.PreparationCanceled
		session.Problem = preparation.Problem
	default:
		return workspace.ResultUncertain()
	}
	if _, err := tx.PutJob(original.ID, original.Revision, original.SessionID, original.ProjectID, preparation); err != nil {
		return err
	}
	session.Recovery = domain.NoRecovery
	if session.Archive == domain.ArchivePending {
		session.Archive = domain.Archived
	}
	_, err = tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, session)
	return err
}
