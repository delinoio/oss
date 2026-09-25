package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const workerLease = domain.WorkerConnectionTimeout

type workerStream struct {
	ID     domain.ID
	Cancel context.CancelFunc
}

func workerActor(ctx context.Context, machine, instance string) error {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.WorkerDevice || string(actor.MachineID) != machine {
		return domain.Fail(domain.PermissionDenied, "This credential does not own the execution machine.", "Use the selected Worker's paired credential.")
	}
	return domain.ID(instance).Validate()
}
func currentInstance(tx *store.Tx, machine, instance domain.ID) error {
	current, _, err := tx.WorkerInstance(machine)
	if err != nil {
		return err
	}
	if current != instance {
		return domain.Fail(domain.Conflict, "The Worker process no longer owns this machine.", "Reattach and reconcile previous execution before continuing.")
	}
	return nil
}
func activeMachine(tx *store.Tx, id domain.ID) (store.Record, domain.Machine, error) {
	r, err := tx.Get(domain.MachineKind, id)
	if err != nil {
		return r, domain.Machine{}, err
	}
	m, err := store.Decode[domain.Machine](r)
	if err != nil {
		return r, m, err
	}
	if m.Disabled {
		return r, m, domain.Fail(domain.PermissionDenied, "The execution machine is disabled.", "Pair an authorized Worker before requesting work.")
	}
	return r, m, nil
}
func (s *Service) AttachWorker(ctx context.Context, req *connect.Request[pb.AttachWorkerRequest]) (*connect.Response[pb.AttachWorkerResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if req.Msg.Version != rpc.Version {
		return nil, rpc.Error(domain.Fail(domain.Unsupported, "Worker and server versions differ.", "Install a matching Worker version."), correlation)
	}
	machine, instance := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)
	input := struct {
		Machine, Instance domain.ID
		Version           string
	}{machine, instance, req.Msg.Version}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "worker.attach", input, func(tx *store.Tx) (any, error) {
		r, m, err := activeMachine(tx, machine)
		if err != nil {
			return nil, err
		}
		previous, seen, err := tx.WorkerInstance(machine)
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		if previous != "" && previous != instance && now.Sub(seen) < workerLease {
			return nil, domain.Fail(domain.Conflict, "Another Worker instance still owns this machine.", "Stop that instance and wait for its connection lease to expire.")
		}
		if previous != "" && previous != instance {
			// A missing connection is not proof that execution never began. Preserve
			// accepted ownership and require explicit reconciliation, never redispatch.
			for {
				jobs, err := tx.Jobs(machine, "", domain.JobClaimed, "", store.MaxPage)
				if err != nil {
					return nil, err
				}
				for _, record := range jobs {
					job, err := store.Decode[domain.Job](record)
					if err != nil {
						return nil, err
					}
					job.State = domain.JobUncertain
					job.Problem = domain.Fail(domain.RecoveryRequired, "The previous Worker process did not confirm completion.", "Inspect the Worker journal and reconcile this accepted job before retrying.")
					if _, err := tx.PutJob(record.ID, record.Revision, record.SessionID, record.ProjectID, job); err != nil {
						return nil, err
					}
					if err := finishLostNativeExecution(tx, record, job); err != nil {
						return nil, err
					}
					if err := finishSessionWorkspace(tx, record.ID); err != nil {
						return nil, err
					}
					if err := finishRepositorySave(tx, job.ParentID); err != nil {
						return nil, err
					}
				}
				if len(jobs) < store.MaxPage {
					break
				}
			}
		}
		if err := tx.SetWorkerInstance(machine, instance, now); err != nil {
			return nil, err
		}
		m.LastSeen, m.Version = now, req.Msg.Version
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	// Receipts acknowledge acceptance, not an indefinite lease. A retry of an
	// older attachment must not revive a replaced process.
	if err := s.Store.Heartbeat(ctx, machine, instance); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var record store.Record
	if err := domain.Decode(result.Data, &record); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "worker attached", "machine_id", machine, "instance_id", instance, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.AttachWorkerResponse{Machine: rpc.Resource(record), ServerId: string(s.Identity.ServerID)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) WatchWork(ctx context.Context, req *connect.Request[pb.WatchWorkRequest], stream *connect.ServerStream[pb.WatchWorkResponse]) error {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return rpc.Error(err, correlation)
	}
	machine, instance := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)
	if err := s.Store.Heartbeat(ctx, machine, instance); err != nil {
		return rpc.Error(err, correlation)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	id := domain.NewID()
	s.connectionsMu.Lock()
	if s.workerStreams == nil {
		s.workerStreams = map[domain.ID]workerStream{}
	}
	if previous, ok := s.workerStreams[machine]; ok {
		previous.Cancel()
	}
	s.workerStreams[machine] = workerStream{ID: id, Cancel: cancel}
	s.connectionsMu.Unlock()
	defer func() {
		s.connectionsMu.Lock()
		defer s.connectionsMu.Unlock()
		if s.workerStreams[machine].ID == id {
			delete(s.workerStreams, machine)
		}
	}()
	controller, ok := ctx.Value(writeControllerKey{}).(*http.ResponseController)
	if !ok {
		return rpc.Error(domain.Fail(domain.Internal, "Streaming deadline controller is unavailable.", "Reconnect to the server."), correlation)
	}
	stream.ResponseHeader().Set(rpc.CorrelationHeader, correlation)
	send := func(message *pb.WatchWorkResponse) error {
		if err := controller.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
			return err
		}
		return stream.Send(message)
	}
	if err := send(&pb.WatchWorkResponse{Heartbeat: true}); err != nil {
		return err
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var after domain.ID
	var inFlight domain.ID
	var cancellationSent domain.ID
	responseControlsSent := map[domain.ID]bool{}
	steerControlsSent := map[domain.ID]bool{}
	for {
		changed := s.Store.Changed()
		var records []store.Record
		var cancelJob domain.ID
		var responseControls []*pb.QuestionResponseControl
		var approvalControls []*pb.ApprovalResponseControl
		var steerControl *pb.SteerInputControl
		err := s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := currentInstance(tx, machine, instance); err != nil {
				return err
			}
			// Keep receiving heartbeats while the Worker performs this operation,
			// but do not preclaim later jobs into a blocked native-input backlog.
			if inFlight != "" {
				record, err := tx.Get(domain.JobKind, inFlight)
				if err != nil {
					return err
				}
				job, err := store.Decode[domain.Job](record)
				if err != nil {
					return err
				}
				if job.State == domain.JobClaimed && job.InstanceID == instance {
					requested, err := tx.JobCancellationRequested(inFlight)
					if err != nil {
						return err
					}
					if requested && cancellationSent != inFlight {
						cancelJob = inFlight
					}
					if !requested {
						responseControls, err = s.pendingQuestionResponses(tx, record, job, responseControlsSent)
						if err == nil {
							approvalControls, err = s.pendingApprovalResponses(tx, record, job, responseControlsSent)
						}
						if err == nil {
							steerControl, err = s.pendingSteer(tx, record, job, steerControlsSent)
						}
					}
					return err
				}
				inFlight = ""
				cancellationSent = ""
				responseControlsSent = map[domain.ID]bool{}
				steerControlsSent = map[domain.ID]bool{}
			}
			var err error
			records, err = tx.Jobs(machine, "", "", after, store.MaxPage)
			return err
		})
		if err != nil {
			return rpc.Error(err, correlation)
		}
		if cancelJob != "" {
			if err := send(&pb.WatchWorkResponse{CancelJobId: string(cancelJob)}); err != nil {
				return err
			}
			cancellationSent = cancelJob
		}
		for _, control := range responseControls {
			if len(responseControlsSent) >= domain.MaxExecutionInteractions {
				return rpc.Error(domain.Fail(domain.ResourceExhausted, "The execution response control bound is reached.", "Reconcile its original interactions without replaying native answers."), correlation)
			}
			if err := send(&pb.WatchWorkResponse{QuestionResponse: control}); err != nil {
				return err
			}
			responseControlsSent[domain.ID(control.ResponseId)] = true
		}
		for _, control := range approvalControls {
			if len(responseControlsSent) >= domain.MaxExecutionInteractions {
				return rpc.Error(domain.Fail(domain.ResourceExhausted, "The execution response control bound is reached.", "Reconcile its original interactions without replaying native answers."), correlation)
			}
			if err := send(&pb.WatchWorkResponse{ApprovalResponse: control}); err != nil {
				return err
			}
			responseControlsSent[domain.ID(control.ResponseId)] = true
		}
		if steerControl != nil {
			if len(steerControlsSent) >= domain.MaxExecutionSteers {
				return rpc.Error(steerConflict(), correlation)
			}
			if err := send(&pb.WatchWorkResponse{SteerInput: steerControl}); err != nil {
				return err
			}
			steerControlsSent[domain.ID(steerControl.SteerId)] = true
		}
		assigned := false
		for _, record := range records {
			job, err := store.Decode[domain.Job](record)
			if err != nil {
				return rpc.Error(err, correlation)
			}
			if job.State == domain.JobQueued {
				result, err := s.Store.Mutate(ctx, domain.NewID(), "worker.claim", struct{ Job, Instance domain.ID }{record.ID, instance}, func(tx *store.Tx) (any, error) {
					if err := currentInstance(tx, machine, instance); err != nil {
						return nil, err
					}
					r, err := tx.Get(domain.JobKind, record.ID)
					if err != nil {
						return nil, err
					}
					j, err := store.Decode[domain.Job](r)
					if err != nil {
						return nil, err
					}
					if j.State != domain.JobQueued {
						return r, nil
					}
					j.State, j.InstanceID = domain.JobClaimed, instance
					return tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, j)
				})
				if err != nil {
					return rpc.Error(err, correlation)
				}
				if err := domain.Decode(result.Data, &record); err != nil {
					return rpc.Error(err, correlation)
				}
				job, err = store.Decode[domain.Job](record)
				if err != nil {
					return rpc.Error(err, correlation)
				}
			}
			if job.State == domain.JobClaimed && job.InstanceID == instance {
				var cancelRequested bool
				if err := s.Store.Read(ctx, func(tx *store.Tx) error {
					var err error
					cancelRequested, err = tx.JobCancellationRequested(record.ID)
					return err
				}); err != nil {
					return rpc.Error(err, correlation)
				}
				if err := send(&pb.WatchWorkResponse{Job: rpc.Resource(record), CancelRequested: cancelRequested}); err != nil {
					return err
				}
				inFlight = record.ID
				assigned = true
				if cancelRequested {
					cancellationSent = record.ID
				}
				after = record.ID
				break
			}
			after = record.ID
		}
		if assigned || len(records) == store.MaxPage {
			continue
		}
		select {
		case <-ctx.Done():
			return rpc.Error(ctx.Err(), correlation)
		case <-changed:
		case <-ticker.C:
			if err := s.Store.Heartbeat(ctx, machine, instance); err != nil {
				return rpc.Error(err, correlation)
			}
			if err := send(&pb.WatchWorkResponse{Heartbeat: true}); err != nil {
				return err
			}
		}
	}
}
func (s *Service) InspectRepository(ctx context.Context, req *connect.Request[pb.InspectRepositoryRequest]) (*connect.Response[pb.InspectRepositoryResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	input := domain.RepositoryInspectionInput{Path: req.Msg.Path, PreferredRemote: req.Msg.PreferredRemote}
	if err := domain.Text(input.Path, "checkout path", 4096, true); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := domain.Text(input.PreferredRemote, "preferred remote", 256, false); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	machine := domain.ID(req.Msg.MachineId)
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "repository.inspect", struct {
		Machine domain.ID
		Input   domain.RepositoryInspectionInput
	}{machine, input}, func(tx *store.Tx) (any, error) {
		if _, _, err := activeMachine(tx, machine); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		return tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobQueued, MachineID: machine, Input: raw, AcceptedAt: time.Now().UTC()})
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var record store.Record
	if err := domain.Decode(result.Data, &record); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.InspectRepositoryResponse{Job: rpc.Resource(record), RequestId: req.Msg.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func workerProblem(detail *pb.ErrorDetail) *domain.Error {
	// Remote diagnostics are an untrusted data boundary. Persist only a closed
	// classification and our own messages; never forward paths or native stderr.
	code := domain.Code(detail.Code)
	switch code {
	case domain.InvalidArgument, domain.NotFound, domain.PermissionDenied, domain.Unavailable, domain.MissingInput, domain.Unsupported, domain.RecoveryRequired, domain.ResourceExhausted, domain.Canceled:
	default:
		code = domain.Internal
	}
	return domain.Fail(code, "The Worker could not complete the accepted operation.", "Inspect the selected Worker and the operation's typed diagnostic before retrying.")
}
func (s *Service) ReportWork(ctx context.Context, req *connect.Request[pb.ReportWorkRequest]) (*connect.Response[pb.ReportWorkResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	if meta == nil || meta.ExpectedRevision == 0 {
		return nil, rpc.Error(domain.Fail(domain.MissingInput, "The claimed job and revision are required.", "Report the exact accepted job."), correlation)
	}
	machine, instance := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)
	var problem *domain.Error
	if req.Msg.Problem != nil {
		problem = workerProblem(req.Msg.Problem)
	}
	if problem != nil && len(req.Msg.OutputJson) > 0 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "A result cannot contain both failure and output.", "Report one typed outcome."), correlation)
	}
	input := struct {
		ID                domain.ID
		Revision          uint64
		Machine, Instance domain.ID
		Output            json.RawMessage
		Problem           *domain.Error
	}{domain.ID(meta.Id), meta.ExpectedRevision, machine, instance, req.Msg.OutputJson, problem}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "worker.report", input, func(tx *store.Tx) (any, error) {
		if err := currentInstance(tx, machine, instance); err != nil {
			return nil, err
		}
		record, err := tx.Get(domain.JobKind, domain.ID(meta.Id))
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](record)
		if err != nil {
			return nil, err
		}
		if job.MachineID != machine || job.InstanceID != instance {
			return nil, domain.Fail(domain.PermissionDenied, "The Worker does not own this job.", "Report only work assigned to this machine and process.")
		}
		if job.State != domain.JobClaimed {
			return nil, domain.Fail(domain.Conflict, "The job is no longer awaiting this result.", "Inspect its current accepted outcome.")
		}
		if job.Type == domain.ExecuteSessionJob {
			return finishNativeExecution(tx, record, job, meta.ExpectedRevision, req.Msg.OutputJson, problem)
		}
		if problem == nil {
			outputJSON := req.Msg.OutputJson
			switch job.Type {
			case domain.RecoverExecutionJob:
				if validateExecutionRecoveryResult(tx, record, job, req.Msg.OutputJson) != nil {
					problem = domain.ExecutionRecoveryUncertain()
				}
			case domain.RecoverWorkspaceJob:
				var expected workspace.RecoveryRequest
				if err := domain.Decode(job.Input, &expected); err != nil {
					return nil, err
				}
				machineRecord, err := tx.Get(domain.MachineKind, machine)
				if err != nil {
					return nil, err
				}
				machineValue, err := store.Decode[domain.Machine](machineRecord)
				if err != nil {
					return nil, err
				}
				var output workspace.RecoveryResult
				_, session, sessionErr := sessionRecord(tx, record.SessionID)
				if sessionErr != nil || validateRecoveryPreparation(tx, record.SessionID, session, expected.Preparation) != nil || domain.Decode(req.Msg.OutputJson, &output) != nil || workspace.ValidateRecoveryResult(expected, output, machineValue.OS) != nil {
					problem = workspace.ResultUncertain()
				}
			case domain.PrepareWorkspaceJob:
				var expected workspace.PrepareRequest
				if err := domain.Decode(job.Input, &expected); err != nil {
					return nil, err
				}
				machineRecord, err := tx.Get(domain.MachineKind, machine)
				if err != nil {
					return nil, err
				}
				machineValue, err := store.Decode[domain.Machine](machineRecord)
				if err != nil {
					return nil, err
				}
				var manifest workspace.Manifest
				if err := domain.Decode(req.Msg.OutputJson, &manifest); err != nil {
					problem = workspace.ResultUncertain()
				} else if err := workspace.ValidateResult(expected, manifest, machineValue.OS); err != nil {
					problem = workspace.ResultUncertain()
				}
			case domain.HarnessDiscoveryJob:
				outputJSON, problem, err = finishDiscovery(tx, job, req.Msg.OutputJson)
				if err != nil {
					return nil, err
				}
			case domain.InspectRepositoryJob:
				var output workspace.Inspection
				if err := domain.Decode(req.Msg.OutputJson, &output); err != nil {
					return nil, err
				}
				if err := domain.Text(output.Root, "canonical checkout", 4096, true); err != nil {
					return nil, err
				}
				if err := domain.Text(output.Name, "repository name", 256, true); err != nil {
					return nil, err
				}
				if len(output.Remotes) > 128 || len(output.DefaultRefs) > 128 {
					return nil, domain.Fail(domain.InvalidArgument, "Repository metadata exceeds its bound.", "Reduce the remote metadata.")
				}
				for _, remote := range output.Remotes {
					if err := domain.Text(remote, "remote name", 256, true); err != nil {
						return nil, err
					}
				}
				for remote, ref := range output.DefaultRefs {
					if !slices.Contains(output.Remotes, remote) {
						return nil, domain.Fail(domain.InvalidArgument, "Default reference has no corresponding remote.", "Reinspect the repository.")
					}
					if err := domain.Text(ref, "default reference", 4096, true); err != nil {
						return nil, err
					}
				}
				var expected domain.RepositoryInspectionInput
				if err := domain.Decode(job.Input, &expected); err != nil {
					return nil, err
				}
				remotes := append([]string{}, expected.RequiredRemotes...)
				if expected.PreferredRemote != "" {
					remotes = append(remotes, expected.PreferredRemote)
				}
				for _, remote := range remotes {
					if !slices.Contains(output.Remotes, remote) {
						problem = domain.Fail(domain.InvalidArgument, "The selected remote no longer exists on the Worker.", "Refresh repository inspection and choose an existing remote.")
						break
					}
				}
			default:
				return nil, domain.Fail(domain.Unsupported, "This job has no supported completion protocol.", "Use a compatible Worker and server.")
			}
			job.Output = outputJSON
		}
		now := time.Now().UTC()
		job.FinishedAt = &now
		if problem == nil {
			job.State = domain.JobSucceeded
		} else {
			job.Output = nil
			job.State, job.Problem = domain.JobFailed, problem
			if problem.Code == domain.Canceled {
				job.State = domain.JobCanceled
			}
			if problem.Code == domain.RecoveryRequired {
				job.State = domain.JobUncertain
			}
		}
		saved, err := tx.PutJob(record.ID, meta.ExpectedRevision, record.SessionID, record.ProjectID, job)
		if err != nil {
			return nil, err
		}
		if err := finishSessionWorkspace(tx, record.ID); err != nil {
			return nil, err
		}
		if err := finishRepositorySave(tx, job.ParentID); err != nil {
			return nil, err
		}
		if job.Type == domain.RecoverExecutionJob {
			return store.Record{ID: saved.ID}, nil
		}
		return saved, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var record store.Record
	if err := domain.Decode(result.Data, &record); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if record.Kind == "" {
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := currentInstance(tx, machine, instance); err != nil {
				return err
			}
			var err error
			record, err = tx.Get(domain.JobKind, record.ID)
			if err != nil {
				return err
			}
			job, err := store.Decode[domain.Job](record)
			if err != nil {
				return err
			}
			if job.MachineID != machine || job.InstanceID != instance || (job.Type != domain.ExecuteSessionJob && job.Type != domain.RecoverExecutionJob) {
				return executionEventConflict()
			}
			return nil
		})
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	s.logger.InfoContext(ctx, "worker job reported", "machine_id", machine, "job_id", meta.Id, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ReportWorkResponse{Job: rpc.Resource(record), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func revokeMachineJobs(tx *store.Tx, machine domain.ID) error {
	for _, state := range []domain.JobState{domain.JobQueued, domain.JobClaimed} {
		for {
			records, err := tx.Jobs(machine, "", state, "", store.MaxPage)
			if err != nil {
				return err
			}
			for _, record := range records {
				job, err := store.Decode[domain.Job](record)
				if err != nil {
					return err
				}
				if state == domain.JobQueued {
					now := time.Now().UTC()
					job.State, job.FinishedAt = domain.JobCanceled, &now
					job.Problem = domain.Fail(domain.PermissionDenied, "The Worker was revoked before dispatch.", "Pair an authorized Worker and submit new work explicitly.")
				} else {
					// Revocation cannot prove that an already accepted native operation had
					// no side effects, even when the connection is canceled immediately.
					job.State, job.FinishedAt = domain.JobUncertain, nil
					job.Problem = domain.Fail(domain.RecoveryRequired, "The Worker was revoked before completion was acknowledged.", "Reconcile its private execution journal before retrying.")
				}
				if _, err := tx.PutJob(record.ID, record.Revision, record.SessionID, record.ProjectID, job); err != nil {
					return err
				}
				if err := finishLostNativeExecution(tx, record, job); err != nil {
					return err
				}
				if err := finishSessionWorkspace(tx, record.ID); err != nil {
					return err
				}
				if err := finishRepositorySave(tx, job.ParentID); err != nil {
					return err
				}
			}
			if len(records) < store.MaxPage {
				break
			}
		}
	}
	return nil
}
