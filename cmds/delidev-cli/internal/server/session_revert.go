// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"time"
)

func (s *Service) RevertSession(ctx context.Context, req *connect.Request[pb.RevertSessionRequest]) (*connect.Response[pb.RevertSessionResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := compactionActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	m := req.Msg.Mutation
	if err := validateSessionMutation(m); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	message := domain.ID(req.Msg.MessageId)
	turn := domain.NativeIdentity(req.Msg.BeforeTurnId)
	if message.Validate() != nil || turn.Validate(domain.Codex, domain.NativeTurnIdentity) != nil {
		return nil, rpc.Error(domain.CompactionUncertain(), correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		Session, Message          domain.ID
		Turn                      domain.NativeIdentity
		Revision, ContextRevision uint64
		Actor                     domain.Principal
	}{domain.ID(m.Id), message, turn, m.ExpectedRevision, req.Msg.ExpectedContextRevision, actor}
	receipt, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "session.revert", identity, func(tx *store.Tx) (any, error) {
		if err := tx.Authorize(); err != nil {
			return nil, err
		}
		sr, session, err := sessionRecord(tx, identity.Session)
		if err != nil {
			return nil, err
		}
		if sr.Revision != identity.Revision || session.ContextRevision != identity.ContextRevision || session.ContextRevision == ^uint64(0) || session.Fork != nil || session.InitialExecution == nil || session.Execution == nil || session.InitialExecution.Configuration.Harness != domain.Codex {
			return nil, continuationConflict()
		}
		mr, err := tx.Get(domain.MessageKind, message)
		if err != nil {
			return nil, err
		}
		target, err := store.Decode[domain.ExecutionMessage](mr)
		if err != nil || mr.SessionID != sr.ID || target.Role != domain.UserMessage || target.State != domain.MessageComplete || target.Inherited != nil || target.NativeTurnID != string(turn) || target.InputID.Validate() != nil || target.NativeThreadID != session.Execution.NativeThreadID {
			return nil, continuationConflict()
		}
		qr, err := tx.Get(domain.QueueKind, target.InputID)
		if err != nil {
			return nil, err
		}
		queued, err := store.Decode[domain.QueuedInput](qr)
		if err != nil || qr.SessionID != sr.ID || queued.Delivery != domain.InputAccepted || queued.ExecutionID != target.ExecutionID || queued.Prompt != target.Text {
			return nil, continuationConflict()
		}
		er, err := tx.SessionExecutionJob(sr.ID, target.ExecutionID)
		if err != nil {
			return nil, err
		}
		ej, err := store.Decode[domain.Job](er)
		var assigned domain.ExecutionJobInput
		var done domain.ExecutionCompletion
		if err != nil || domain.Decode(ej.Input, &assigned) != nil || assigned.Validate() != nil || assigned.InputID != target.InputID || assigned.ContextRevision > session.ContextRevision || assigned.ContextRevision < session.ContextRevision && (session.Revert == nil || !session.Revert.Contains(turn)) || domain.Decode(ej.Output, &done) != nil || done.NativeThreadID != domain.NativeIdentity(target.NativeThreadID) || done.NativeTurnID != turn {
			return nil, continuationConflict()
		}
		input, err := contextActionSource(tx, sr, session, domain.ID(m.RequestId), true)
		if err != nil {
			return nil, err
		}
		input.Version = 4
		input.Revert = &domain.SessionRevertTarget{MessageID: message, InputID: target.InputID, NativeTurnID: turn, Prompt: queuedSessionInput(queued), ContextRevision: session.ContextRevision}
		_, machine, err := activeMachine(tx, session.MachineID)
		if err != nil {
			return nil, err
		}
		if err := freezeRevertTargetProof(input.Revert, target.ExecutionID, session.Execution.ExecutionID, done, machine.WorkerCapabilities); err != nil {
			s.logger.WarnContext(ctx, "session_revert_target_proof_unavailable", "session_id", sr.ID, "machine_id", session.MachineID, "target_execution_id", target.ExecutionID, "source_execution_id", session.Execution.ExecutionID, "completion_version", done.Version)
			return nil, err
		}
		if input.Validate() != nil {
			return nil, domain.CompactionUncertain()
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
		if _, err := tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return struct{ JobID domain.ID }{job.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var ref struct{ JobID domain.ID }
	if json.Unmarshal(receipt.Data, &ref) != nil {
		return nil, rpc.Error(domain.CompactionUncertain(), correlation)
	}
	var job store.Record
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		job, err = tx.Get(domain.JobKind, ref.JobID)
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "session_revert_accepted", "session_id", m.Id, "job_id", ref.JobID, "request_id", m.RequestId, "replayed", receipt.Replayed)
	response := connect.NewResponse(&pb.RevertSessionResponse{Job: rpc.Resource(job), RequestId: m.RequestId, Replayed: receipt.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

// freezeRevertTargetProof enforces the proof capability for every skilled
// target and freezes the retained checkpoint reference of the target execution
// only for a skilled target that an earlier execution accepted, the one case the
// source checkpoint cannot prove. A text-only target keeps the original
// envelope, which any Worker advertising CodexSessionRevertV1 decodes. A Worker
// without CodexSessionRevertTargetProofV1 never authenticates selected skills
// and strictly rejects the extra fields, so every skilled target is refused here
// for such a Worker instead of pausing the session first.
func freezeRevertTargetProof(revert *domain.SessionRevertTarget, targetExecution, sourceExecution domain.ID, completion domain.ExecutionCompletion, capabilities []domain.WorkerCapability) error {
	if len(revert.Prompt.Skills) == 0 {
		return nil
	}
	if !machineCapabilityContains(capabilities, domain.CodexSessionRevertTargetProofV1) {
		return domain.Fail(domain.Unsupported, "The original Worker cannot revert to a turn that selected skills.", "Update the original Worker.")
	}
	if targetExecution == sourceExecution {
		return nil
	}
	if completion.Version != 2 {
		return domain.Fail(domain.Unsupported, "The original Worker cannot revert to an earlier turn that selected skills.", "Update the original Worker.")
	}
	revert.ExecutionID, revert.CheckpointDigest = targetExecution, completion.NativeCheckpointDigest
	return nil
}

// requireQueuedRevertCapabilities rechecks the current machine capability set
// when a queued version-4 Revert is about to be claimed. A Worker that
// reconnected without the Revert or target-proof capability would otherwise be
// assigned a job it cannot decode or authenticate, and the failure would
// quarantine the session as uncertain. The job is still unclaimed here, so the
// caller retires it with a typed unsupported failure and no native effect.
func requireQueuedRevertCapabilities(machine domain.Machine, job domain.Job) error {
	if job.Type != domain.CompactSessionJob {
		return nil
	}
	var input domain.SessionCompactionInput
	if domain.DecodeCompactionInput(job.Input, &input) != nil {
		return workspace.ResultUncertain()
	}
	if input.Revert == nil || compactionLeaseCapabilities(machine.WorkerCapabilities, input) {
		return nil
	}
	return domain.Fail(domain.Unsupported, "The original Worker no longer supports this Revert.", "Update and reconnect the original Worker.")
}
