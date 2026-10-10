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

type codexAppsWorkerIdentity struct {
	Actor                             domain.Principal
	Operation, Machine, Instance, Job domain.ID
	Revision                          uint64
	Output                            json.RawMessage
	Problem                           *domain.Error
}

func codexAppsWorkerMutation(ctx context.Context, m *pb.Mutation, machine, instance, job string) (codexAppsWorkerIdentity, error) {
	actor, _ := domain.PrincipalFrom(ctx)
	value := codexAppsWorkerIdentity{Actor: actor, Operation: domain.ID(m.GetId()), Machine: domain.ID(machine), Instance: domain.ID(instance), Job: domain.ID(job), Revision: m.GetExpectedRevision()}
	if err := workerActor(ctx, machine, instance); err != nil {
		return value, err
	}
	if err := validateSessionMutation(m); err != nil {
		return value, err
	}
	for _, id := range []domain.ID{value.Job, value.Actor.DeviceID} {
		if err := id.Validate(); err != nil {
			return value, err
		}
	}
	return value, nil
}
func (s *Service) originalCodexAppsWorker(tx *store.Tx, identity codexAppsWorkerIdentity) (*store.CodexAppsSnapshot, *domain.CodexAppsOperation, error) {
	if err := tx.Authorize(); err != nil {
		return nil, nil, err
	}
	operation, err := tx.CodexAppsOperation(identity.Operation)
	if err != nil || operation == nil {
		return nil, nil, codexAppsUnavailable()
	}
	if operation.ExecutionJobID != identity.Job || operation.MachineID != identity.Machine || operation.InstanceID != identity.Instance {
		return nil, nil, codexAppsUnavailable()
	}
	sr, session, err := sessionRecord(tx, operation.Original.SessionID)
	if err != nil {
		return nil, nil, err
	}
	record, job, input, err := s.codexAppsLiveSource(tx, sr, session, operation.Original, operation.Action == domain.CodexAppsInspect)
	if err != nil {
		return nil, nil, err
	}
	if record.ID != operation.ExecutionJobID || job.AssignedDeviceID != identity.Actor.DeviceID || input.ExecutionID != operation.ExecutionID || domain.NativeIdentity(session.Execution.NativeThreadID) != operation.NativeThreadID {
		return nil, nil, codexAppsUnavailable()
	}
	value, err := tx.CodexAppsSnapshot(sr.ID)
	return value, operation, err
}
func codexAppsControls(tx *store.Tx, machine, instance domain.ID) ([]*pb.CodexAppsControl, error) {
	operations, err := tx.CodexAppsQueued(machine, instance)
	if err != nil {
		return nil, err
	}
	controls := []*pb.CodexAppsControl{}
	for _, operation := range operations {
		sr, session, err := sessionRecord(tx, operation.Original.SessionID)
		if err != nil {
			return nil, err
		}
		record, err := tx.SessionExecutionJob(sr.ID, operation.ExecutionID)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](record)
		if err != nil {
			return nil, err
		}
		// References grant no delivery right: Claim independently checks the original
		// actor, current primary Worker, subscription lease and retained native thread.
		if record.ID != operation.ExecutionJobID || job.Type != domain.ExecuteSessionJob || job.State != domain.JobClaimed || job.MachineID != machine || job.InstanceID != instance || session.ActiveExecutionID != operation.ExecutionID || session.Execution == nil || domain.NativeIdentity(session.Execution.NativeThreadID) != operation.NativeThreadID {
			continue
		}
		controls = append(controls, &pb.CodexAppsControl{ExecutionJobId: string(record.ID), AppsOperationId: string(operation.ID), Revision: operation.Revision})
	}
	return controls, nil
}
func (s *Service) ClaimCodexAppsControl(ctx context.Context, req *connect.Request[pb.ClaimCodexAppsControlRequest]) (*connect.Response[pb.ClaimCodexAppsControlResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	identity, err := codexAppsWorkerMutation(ctx, m, req.Msg.MachineId, req.Msg.InstanceId, req.Msg.ExecutionJobId)
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	claim := domain.ID(m.RequestId)
	result, err := s.Store.Mutate(ctx, claim, "codex-apps.claim", identity, func(tx *store.Tx) (any, error) {
		value, operation, err := s.originalCodexAppsWorker(tx, identity)
		if err != nil {
			return nil, err
		}
		if operation.Revision != identity.Revision || operation.State != domain.CodexAppsQueued || operation.ClaimID != "" {
			return nil, codexAppsUnavailable()
		}
		operation.State, operation.ClaimID, operation.Revision = domain.CodexAppsClaimed, claim, operation.Revision+1
		value.Operation = operation
		if err := tx.PutCodexAppsSnapshot(value.SessionID, value.Revision, *value); err != nil {
			return nil, err
		}
		return struct{ Operation domain.ID }{operation.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	var operation *domain.CodexAppsOperation
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		_, current, err := s.originalCodexAppsWorker(tx, identity)
		if err != nil {
			return err
		}
		if current.ClaimID != claim {
			return codexAppsUnavailable()
		}
		operation = current
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	resource, err := codexAppsResource(operation.ID, operation.Revision, operation.Original.SessionID, operation)
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	var input []byte
	if !result.Replayed && operation.State == domain.CodexAppsClaimed {
		input, err = json.Marshal(operation)
		if err != nil {
			return nil, rpc.Error(err, corr)
		}
	}
	s.logger.InfoContext(ctx, "codex_apps_control_claimed", "operation_id", operation.ID, "job_id", operation.ExecutionJobID, "claim_id", claim, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ClaimCodexAppsControlResponse{Operation: resource, InputJson: input, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func codexAppsNegativeInventory(original, next domain.CodexAppConfiguration, inventory domain.CodexAppsInventory) bool {
	if !original.RemovalOnly(next) || inventory.Validate() != nil {
		return false
	}
	for _, app := range inventory.Apps {
		selected := slices.Contains(next.AppIDs, app.ID)
		if app.Selected != selected || !selected && (app.Enabled || app.Callable) {
			return false
		}
	}
	// Missing rows are interpreted only inside a full original native catalog
	// refresh proof, bound to the one immutable operation/claim below.
	return inventory.NativeCatalogRefreshVerified
}
func (s *Service) ReportCodexAppsControlResult(ctx context.Context, req *connect.Request[pb.ReportCodexAppsControlResultRequest]) (*connect.Response[pb.ReportCodexAppsControlResultResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	identity, err := codexAppsWorkerMutation(ctx, m, req.Msg.MachineId, req.Msg.InstanceId, req.Msg.ExecutionJobId)
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	identity.Output = slices.Clone(req.Msg.OutputJson)
	if req.Msg.Problem != nil {
		identity.Problem = workerProblem(req.Msg.Problem)
	}
	if len(identity.Output) > 4<<20 || identity.Problem != nil && len(identity.Output) != 0 {
		return nil, rpc.Error(codexAppsUnavailable(), corr)
	}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "codex-apps.report", identity, func(tx *store.Tx) (any, error) {
		value, operation, err := s.originalCodexAppsWorker(tx, identity)
		if err != nil {
			return nil, err
		}
		if operation.Revision != identity.Revision || operation.State != domain.CodexAppsClaimed || operation.ClaimID.Validate() != nil {
			return nil, codexAppsUnavailable()
		}
		operation.Revision++
		if identity.Problem != nil {
			// A transport/native problem has no positive no-send proof. Retain the once
			// obligation rather than manufacturing failure cleanup or rescheduling it.
			operation.State, operation.Problem = domain.CodexAppsUncertain, identity.Problem
		} else {
			var inventory domain.CodexAppsInventory
			if domain.DecodeBounded(identity.Output, &inventory, 4<<20) != nil || inventory.Validate() != nil {
				return nil, codexAppsUnavailable()
			}
			operation.State, operation.Inventory = domain.CodexAppsSucceeded, &inventory
			if operation.Validate() != nil {
				return nil, codexAppsUnavailable()
			}
			if operation.Action == domain.CodexAppsRevoke {
				if operation.Next == nil || !codexAppsNegativeInventory(operation.Original, *operation.Next, inventory) {
					return nil, codexAppsUnavailable()
				}
				next := operation.Next.Clone()
				value.Configuration = &next
			}
			value.Inventory = &inventory
		}
		if err := operation.Validate(); err != nil {
			return nil, err
		}
		value.Operation = operation
		if err := tx.PutCodexAppsSnapshot(value.SessionID, value.Revision, *value); err != nil {
			return nil, err
		}
		return struct{ Operation domain.ID }{operation.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	// Receipt reads disclose metadata only; they do not require another live
	// native controller or create a replacement command after terminal completion.
	var operation *domain.CodexAppsOperation
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		operation, err = tx.CodexAppsOperation(identity.Operation)
		if err != nil {
			return err
		}
		if operation == nil || operation.ExecutionJobID != identity.Job || operation.MachineID != identity.Machine || operation.InstanceID != identity.Instance {
			return codexAppsUnavailable()
		}
		record, err := tx.Get(domain.JobKind, identity.Job)
		if err != nil {
			return err
		}
		job, err := store.Decode[domain.Job](record)
		if err != nil || job.AssignedDeviceID != identity.Actor.DeviceID {
			return codexAppsUnavailable()
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	resource, err := codexAppsResource(operation.ID, operation.Revision, operation.Original.SessionID, operation)
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	s.logger.InfoContext(ctx, "codex_apps_control_reported", "operation_id", operation.ID, "job_id", operation.ExecutionJobID, "state", operation.State, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ReportCodexAppsControlResultResponse{Operation: resource, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
