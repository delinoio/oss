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

func requireExecutionStartupReady(tx *store.Tx, id domain.ID) error {
	r, err := tx.Get(domain.JobKind, id)
	if err != nil {
		return executionDenied()
	}
	j, err := store.Decode[domain.Job](r)
	if err != nil {
		return executionDenied()
	}
	if j.Type != domain.ExecuteSessionJob {
		return nil
	}
	var input domain.ExecutionJobInput
	if domain.Decode(j.Input, &input) != nil {
		return executionDenied()
	}
	if input.Version != 4 {
		return nil
	}
	_, session, err := sessionRecord(tx, input.SessionID)
	if err != nil || session.Startup == nil || session.Startup.JobID != id || session.Startup.ExecutionID != input.ExecutionID || session.Startup.Ready == nil || session.Startup.Ready.Validate() != nil || session.Startup.Failure != nil {
		return executionDenied()
	}
	return nil
}

func (s *Service) ReportExecutionStartup(ctx context.Context, req *connect.Request[pb.ReportExecutionStartupRequest]) (*connect.Response[pb.ReportExecutionStartupResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	o, err := rpc.StartupObservation(req.Msg.Observation)
	meta := req.Msg.Mutation
	if err != nil || meta == nil || meta.ExpectedRevision == 0 || o.CorrelationID != domain.ID(meta.Id) {
		return nil, rpc.Error(domain.StartupRejectionUncertain(), correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		Job, Machine, Instance, Device, Epoch domain.ID
		Revision                              uint64
		Observation                           domain.ExecutionStartupObservation
	}{domain.ID(meta.Id), domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId), actor.DeviceID, s.executionAuthority.epoch, meta.ExpectedRevision, o}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "execution.startup", identity, func(tx *store.Tx) (any, error) {
		if err := currentInstance(tx, identity.Machine, identity.Instance); err != nil {
			return nil, err
		}
		r, err := tx.Get(domain.JobKind, identity.Job)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil || r.Revision != identity.Revision || job.Type != domain.ExecuteSessionJob || job.State != domain.JobClaimed || job.InstanceID != identity.Instance || job.MachineID != identity.Machine || job.AssignedDeviceID != identity.Device {
			return nil, executionDenied()
		}
		input, sr, session, err := nativeExecutionScope(tx, r, job)
		if err != nil || input.Version != 4 || o.Harness != input.Configuration.Harness {
			return nil, executionDenied()
		}
		_, machine, err := activeMachine(tx, identity.Machine)
		if err != nil || !slices.Contains(machine.WorkerCapabilities, domain.ExecutionStartupV1) {
			return nil, executionDenied()
		}
		if session.Startup == nil || session.Startup.JobID != r.ID {
			session.Startup = &domain.ExecutionStartupRecord{JobID: r.ID, ExecutionID: input.ExecutionID}
		}
		if o.State == domain.StartupReady {
			grant, err := tx.ExecutionGrantForJob(r.ID)
			if err != nil {
				return nil, err
			}
			if _, err := s.executionAuthority.scope(tx, grant); err != nil {
				return nil, err
			}
			if session.Startup.Ready != nil || session.Startup.Failure != nil || session.Execution != nil && session.Execution.NativeTurnID != "" {
				return nil, executionDenied()
			}
			if input.Startup.ExecutableSHA256 != "" && input.Startup.ExecutableSHA256 != o.ExecutableSHA256 {
				return nil, executionDenied()
			}
			session.Startup.Ready = &o
		} else {
			if session.Startup.Failure != nil {
				return nil, executionDenied()
			}
			// Positive pre-send proof cannot contradict an acknowledged public input.
			if o.InputDelivery == domain.StartupNotSent && session.Execution != nil && session.Execution.NativeTurnID != "" {
				return nil, executionDenied()
			}
			if o.FailureKind == domain.StartupImageInputRejected {
				ready := session.Startup.Ready
				if len(input.Input.Attachments) == 0 || ready == nil || ready.Validate() != nil || ready.State != domain.StartupReady || ready.ExecutableSHA256 != o.ExecutableSHA256 || ready.NativeVersion != o.NativeVersion || session.Execution == nil || session.Execution.NativeThreadID == "" || session.Execution.NativeTurnID != "" {
					return nil, executionDenied()
				}
				queue, err := tx.Get(domain.QueueKind, input.InputID)
				if err != nil {
					return nil, err
				}
				claimed, err := store.Decode[domain.QueuedInput](queue)
				original := domain.SessionInput{Prompt: claimed.Prompt, Mode: claimed.Mode, Skills: claimed.Skills, Attachments: claimed.Attachments}
				if err != nil || queue.SessionID != sr.ID || claimed.Delivery != domain.InputClaimed || claimed.ExecutionID != input.ExecutionID || claimed.NativeRequestID != input.TurnRequestID || original.InputDigest() != input.Input.InputDigest() {
					return nil, executionDenied()
				}
			}
			session.Startup.Failure = &o
			session.Problem = startupFailureProblem(o)
		}
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return o, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return currentInstance(tx, identity.Machine, identity.Instance) }); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var saved domain.ExecutionStartupObservation
	if err := json.Unmarshal(result.Data, &saved); err != nil {
		return nil, rpc.Error(domain.StartupRejectionUncertain(), correlation)
	}
	s.logger.InfoContext(ctx, "execution_startup_reported", "job_id", identity.Job, "machine_id", identity.Machine, "phase", saved.Phase, "state", saved.State, "code", saved.ProblemCode, "input_delivery", saved.InputDelivery, "cleanup", saved.Cleanup, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ReportExecutionStartupResponse{Observation: rpc.StartupMessage(saved), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func startupFailureProblem(o domain.ExecutionStartupObservation) *domain.Error {
	if o.FailureKind == domain.StartupImageInputRejected && o.Validate() == nil {
		return domain.UnsupportedImageInput()
	}
	message, guidance := "The agent could not start.", "Open details to inspect the failure phase and original log reference."
	switch o.ProblemCode {
	case domain.NotFound:
		message, guidance = "The selected agent executable was not found.", "Install the harness on the selected Runner Device or correct its executable path."
	case domain.PermissionDenied:
		message, guidance = "The agent could not access its executable or runtime.", "Check the selected Runner Device's permissions."
	case domain.Unsupported:
		message, guidance = "The agent's protocol or applied settings are incompatible.", "Check the selected harness and settings; details identify the failing stage."
	case domain.Unavailable:
		message, guidance = "The agent failed to start or timed out.", "Check the selected Runner Device and its original log reference."
	}
	if o.InputDelivery != domain.StartupNotSent || o.Cleanup != domain.StartupCleanupConfirmed {
		guidance = "Recover the original execution before sending input again; delivery or cleanup is uncertain."
	}
	return domain.Fail(o.ProblemCode, message, guidance)
}
