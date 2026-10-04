// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"slices"
	"time"
)

func updateComponent(c pb.UpdateComponent) updates.Component {
	switch c {
	case pb.UpdateComponent_UPDATE_COMPONENT_DESKTOP:
		return updates.Desktop
	case pb.UpdateComponent_UPDATE_COMPONENT_WORKER:
		return updates.Worker
	}
	return ""
}
func updateTarget(t pb.UpdateTarget) updates.Target {
	if t >= 1 && t <= 6 {
		return updates.Targets[int(t)-1]
	}
	return ""
}
func (s *Service) CheckUpdate(ctx context.Context, req *connect.Request[pb.CheckUpdateRequest]) (*connect.Response[pb.CheckUpdateResponse], error) {
	actor, e := installationActor(ctx)
	c := req.Header().Get(rpc.CorrelationHeader)
	component, target := updateComponent(req.Msg.Component), updateTarget(req.Msg.Target)
	if e == nil {
		if domain.ID(req.Msg.RequestId).Validate() != nil || !component.Valid() || !target.Valid() {
			e = installationFailure(domain.InvalidArgument)
		} else {
			_, e = updates.Newer(req.Msg.CurrentVersion, "0.0.0")
		}
	}
	if e != nil {
		return nil, rpc.Error(e, c)
	}
	input := struct {
		Actor   domain.Principal
		Request *pb.CheckUpdateRequest
	}{actor, req.Msg}
	accepted, replayed, e := s.Store.Replay(ctx, domain.ID(req.Msg.RequestId), "installation.update.check", input)
	if e != nil {
		return nil, rpc.Error(e, c)
	}
	if !replayed {
		operation := updateOperation{ServerID: s.Identity.ServerID, Actor: actor, State: updates.Observed, Component: component, Target: target, CurrentVersion: req.Msg.CurrentVersion}
		e = s.Store.Read(ctx, func(tx *store.Tx) error {
			if e := tx.Authorize(); e != nil {
				return e
			}
			if component == updates.Desktop {
				if req.Msg.MachineId != "" || req.Msg.ExpectedMachineRevision != 0 {
					return installationFailure(domain.InvalidArgument)
				}
				return nil
			}
			r, m, e := activeMachine(tx, domain.ID(req.Msg.MachineId))
			if e != nil {
				return e
			}
			if r.Revision != req.Msg.ExpectedMachineRevision || m.Version != req.Msg.CurrentVersion || m.OS+"-"+m.Architecture != string(target) {
				return installationFailure(domain.Conflict)
			}
			if !slices.Contains(m.WorkerCapabilities, domain.SignedWorkerUpdatesV1) {
				return installationFailure(domain.Unsupported)
			}
			if e := tx.WorkerUpdateAdmission(r.ID); e != nil {
				return e
			}
			operation.DeviceID, e = tx.InstallationWorkerDevice(r.ID)
			if e != nil {
				return e
			}
			operation.MachineID = r.ID
			operation.MachineRevision = r.Revision
			return nil
		})
		if e != nil {
			return nil, rpc.Error(e, c)
		}
		client, e := s.releaseClient()
		if e != nil {
			return nil, rpc.Error(e, c)
		}
		defer client.Close()
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		candidate, e := client.Latest(bounded, operation.CurrentVersion, time.Now().UTC())
		if e != nil {
			return nil, rpc.Error(e, c)
		}
		if _, e = candidate.Artifact(component, target); e != nil {
			return nil, rpc.Error(e, c)
		}
		operation.Version = candidate.Payload.Version
		operation.Manifest = append([]byte(nil), candidate.Canonical...)
		operation.ManifestSHA256 = candidate.ManifestSHA256
		accepted, e = s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "installation.update.check", input, func(tx *store.Tx) (any, error) {
			if e := tx.Authorize(); e != nil {
				return nil, e
			}
			if operation.MachineID != "" {
				r, m, e := activeMachine(tx, operation.MachineID)
				if e != nil {
					return nil, e
				}
				if r.Revision != operation.MachineRevision || m.Version != operation.CurrentVersion {
					return nil, installationFailure(domain.Conflict)
				}
				if e := tx.WorkerUpdateAdmission(r.ID); e != nil {
					return nil, e
				}
			}
			_, e := tx.Put(domain.UpdateKind, domain.ID(req.Msg.RequestId), 0, "", "", operation)
			return installationReceipt{domain.ID(req.Msg.RequestId)}, e
		})
		if e != nil {
			return nil, rpc.Error(e, c)
		}
	}
	r, e := s.installationResult(ctx, domain.UpdateKind, accepted)
	if e != nil {
		return nil, rpc.Error(e, c)
	}
	s.logger.InfoContext(ctx, "update_candidate_observed", "operation_id", r.ID, "target", target, "component", component, "replayed", accepted.Replayed)
	response := connect.NewResponse(&pb.CheckUpdateResponse{Update: rpc.Resource(r), RequestId: string(accepted.RequestID), Replayed: accepted.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) GetUpdate(ctx context.Context, req *connect.Request[pb.GetUpdateRequest]) (*connect.Response[pb.GetUpdateResponse], error) {
	r, e := s.readInstallation(ctx, domain.UpdateKind, req.Msg.Id)
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.GetUpdateResponse{Update: rpc.Resource(r), ProductionRootReady: updates.ProductionReady()}), nil
}
func (s *Service) RequestWorkerUpdate(ctx context.Context, req *connect.Request[pb.RequestWorkerUpdateRequest]) (*connect.Response[pb.RequestWorkerUpdateResponse], error) {
	result, e := s.changeUpdate(ctx, req.Msg.Mutation, false)
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	r, e := s.installationResult(ctx, domain.UpdateKind, result)
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.RequestWorkerUpdateResponse{Update: rpc.Resource(r), RequestId: string(result.RequestID), Replayed: result.Replayed}), nil
}
func (s *Service) CancelUpdate(ctx context.Context, req *connect.Request[pb.CancelUpdateRequest]) (*connect.Response[pb.CancelUpdateResponse], error) {
	result, e := s.changeUpdate(ctx, req.Msg.Mutation, true)
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	r, e := s.installationResult(ctx, domain.UpdateKind, result)
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.CancelUpdateResponse{Update: rpc.Resource(r), RequestId: string(result.RequestID), Replayed: result.Replayed}), nil
}
func (s *Service) changeUpdate(ctx context.Context, m *pb.Mutation, cancel bool) (store.Result, error) {
	actor, e := installationActor(ctx)
	if e != nil {
		return store.Result{}, e
	}
	if e = checkInstallationMutation(m); e != nil {
		return store.Result{}, e
	}
	input := struct {
		Actor    domain.Principal
		Mutation *pb.Mutation
		Cancel   bool
	}{actor, m, cancel}
	return s.Store.Mutate(ctx, domain.ID(m.RequestId), "installation.update.accept", input, func(tx *store.Tx) (any, error) {
		r, e := installationRecord(tx, domain.UpdateKind, domain.ID(m.Id), actor, m.ExpectedRevision)
		if e != nil {
			return nil, e
		}
		o, e := store.Decode[updateOperation](r)
		if e != nil || o.ServerID != s.Identity.ServerID {
			return nil, installationFailure(domain.RecoveryRequired)
		}
		if cancel {
			if o.State != updates.Observed && o.State != updates.Waiting {
				return nil, installationFailure(domain.RecoveryRequired)
			}
			o.State = updates.Canceled
			now := time.Now().UTC()
			o.FinishedAt = &now
		} else {
			if o.State != updates.Observed || o.Component != updates.Worker || o.DeviceID.Validate() != nil {
				return nil, installationFailure(domain.Conflict)
			}
			mr, m, e := activeMachine(tx, o.MachineID)
			if e != nil {
				return nil, e
			}
			if mr.Revision != o.MachineRevision || m.Version != o.CurrentVersion {
				return nil, installationFailure(domain.Conflict)
			}
			if e := tx.WorkerUpdateAdmission(o.MachineID); e != nil {
				return nil, e
			}
			o.State = updates.Waiting
		}
		_, e = tx.Put(r.Kind, r.ID, r.Revision, "", "", o)
		return installationReceipt{r.ID}, e
	})
}
func updateWorker(ctx context.Context, instance string) (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.WorkerDevice || actor.DeviceID.Validate() != nil || actor.MachineID.Validate() != nil {
		return actor, installationFailure(domain.PermissionDenied)
	}
	return actor, workerActor(ctx, string(actor.MachineID), instance)
}
func workerUpdateRecord(tx *store.Tx, id domain.ID, actor domain.Principal, instance domain.ID) (store.Record, updateOperation, error) {
	if e := tx.Authorize(); e != nil {
		return store.Record{}, updateOperation{}, e
	}
	if e := currentInstance(tx, actor.MachineID, instance); e != nil {
		return store.Record{}, updateOperation{}, e
	}
	r, e := tx.Get(domain.UpdateKind, id)
	if e != nil {
		return r, updateOperation{}, e
	}
	o, e := store.Decode[updateOperation](r)
	if e != nil || o.MachineID != actor.MachineID || o.DeviceID != actor.DeviceID || o.Component != updates.Worker {
		return r, o, installationFailure(domain.PermissionDenied)
	}
	if e = originalInstallationActor(tx, o.Actor); e != nil {
		return r, o, e
	}
	return r, o, nil
}
func (s *Service) updateIdle(tx *store.Tx, machine domain.ID) (bool, error) {
	idle, e := tx.WorkerUpdateIdle(machine)
	if e != nil || !idle {
		return idle, e
	}
	idle, e = s.Store.WorkerDeletionIdle(tx, machine)
	if e != nil || !idle {
		return idle, e
	}
	s.workspaceReadsMu.Lock()
	defer s.workspaceReadsMu.Unlock()
	for _, reader := range s.workspaceReaders {
		if reader.machine == machine && reader.pending != nil {
			return false, nil
		}
	}
	return true, nil
}
func (s *Service) PollWorkerUpdate(ctx context.Context, req *connect.Request[pb.PollWorkerUpdateRequest]) (*connect.Response[pb.PollWorkerUpdateResponse], error) {
	actor, e := updateWorker(ctx, req.Msg.InstanceId)
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	var result *pb.Resource
	var idle bool
	e = s.Store.Read(ctx, func(tx *store.Tx) error {
		if e := tx.Authorize(); e != nil {
			return e
		}
		if e := currentInstance(tx, actor.MachineID, domain.ID(req.Msg.InstanceId)); e != nil {
			return e
		}
		rows, e := tx.InstallationUpdates(actor.MachineID, actor.DeviceID, "", true)
		if e != nil {
			return e
		}
		for _, r := range rows {
			o, e := store.Decode[updateOperation](r)
			if e != nil {
				return e
			}
			if o.MachineID == actor.MachineID && o.DeviceID == actor.DeviceID && (o.State == updates.Waiting || o.State == updates.Running || o.State == updates.Uncertain) {
				if result != nil {
					return installationFailure(domain.RecoveryRequired)
				}
				result = rpc.Resource(r)
			}
		}
		if result != nil {
			idle, e = s.updateIdle(tx, actor.MachineID)
		}
		return e
	})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.PollWorkerUpdateResponse{Update: result, Idle: idle}), nil
}
func (s *Service) ClaimWorkerUpdate(ctx context.Context, req *connect.Request[pb.ClaimWorkerUpdateRequest]) (*connect.Response[pb.ClaimWorkerUpdateResponse], error) {
	actor, e := updateWorker(ctx, req.Msg.InstanceId)
	if e == nil {
		e = checkInstallationMutation(req.Msg.Mutation)
	}
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	m := req.Msg.Mutation
	input := struct {
		Actor   domain.Principal
		Request *pb.ClaimWorkerUpdateRequest
	}{actor, req.Msg}
	result, e := s.Store.Mutate(ctx, domain.ID(m.RequestId), "installation.update.claim", input, func(tx *store.Tx) (any, error) {
		r, o, e := workerUpdateRecord(tx, domain.ID(m.Id), actor, domain.ID(req.Msg.InstanceId))
		if e != nil {
			return nil, e
		}
		if r.Revision != m.ExpectedRevision || o.State != updates.Waiting || o.ClaimRequestID != "" {
			return nil, installationFailure(domain.Conflict)
		}
		idle, e := s.updateIdle(tx, actor.MachineID)
		if e != nil {
			return nil, e
		}
		if !idle {
			return nil, installationFailure(domain.Conflict)
		}
		_, machine, e := activeMachine(tx, actor.MachineID)
		if e != nil {
			return nil, e
		}
		if machine.Version != o.CurrentVersion || !slices.Contains(machine.WorkerCapabilities, domain.SignedWorkerUpdatesV1) {
			return nil, installationFailure(domain.Unsupported)
		}
		o.State = updates.Running
		o.ClaimedInstance = domain.ID(req.Msg.InstanceId)
		o.ClaimRequestID = domain.ID(m.RequestId)
		o.ClaimedRevision = r.Revision + 1
		_, e = tx.Put(r.Kind, r.ID, r.Revision, "", "", o)
		return installationReceipt{r.ID}, e
	})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	var r store.Record
	e = s.Store.Read(ctx, func(tx *store.Tx) error {
		var problem error
		r, _, problem = workerUpdateRecord(tx, domain.ID(m.Id), actor, domain.ID(req.Msg.InstanceId))
		return problem
	})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.ClaimWorkerUpdateResponse{Update: rpc.Resource(r), RequestId: string(result.RequestID), Replayed: result.Replayed}), nil
}
func (s *Service) ReportWorkerUpdate(ctx context.Context, req *connect.Request[pb.ReportWorkerUpdateRequest]) (*connect.Response[pb.ReportWorkerUpdateResponse], error) {
	actor, e := updateWorker(ctx, req.Msg.InstanceId)
	if e == nil {
		e = checkInstallationMutation(req.Msg.Mutation)
	}
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	m := req.Msg.Mutation
	input := struct {
		Actor   domain.Principal
		Request *pb.ReportWorkerUpdateRequest
	}{actor, req.Msg}
	result, e := s.Store.Mutate(ctx, domain.ID(m.RequestId), "installation.update.report", input, func(tx *store.Tx) (any, error) {
		r, o, e := workerUpdateRecord(tx, domain.ID(m.Id), actor, domain.ID(req.Msg.InstanceId))
		if e != nil {
			return nil, e
		}
		if o.State != updates.Running && o.State != updates.Uncertain || o.ClaimRequestID == "" || o.ClaimedRevision != m.ExpectedRevision {
			return nil, installationFailure(domain.Conflict)
		}
		_, machine, e := activeMachine(tx, actor.MachineID)
		if e != nil {
			return nil, e
		}
		switch req.Msg.Outcome {
		case pb.WorkerUpdateOutcome_WORKER_UPDATE_OUTCOME_SUCCEEDED:
			if req.Msg.InstalledVersion != o.Version || machine.Version != o.Version || domain.ID(req.Msg.InstanceId) == o.ClaimedInstance {
				return nil, installationFailure(domain.RecoveryRequired)
			}
			o.State = updates.Succeeded
			o.ProblemCode = ""
		case pb.WorkerUpdateOutcome_WORKER_UPDATE_OUTCOME_FAILED:
			if req.Msg.InstalledVersion != o.CurrentVersion || machine.Version != o.CurrentVersion {
				return nil, installationFailure(domain.RecoveryRequired)
			}
			o.State = updates.Failed
			o.ProblemCode = domain.Unavailable
		case pb.WorkerUpdateOutcome_WORKER_UPDATE_OUTCOME_UNCERTAIN:
			o.State = updates.Uncertain
			o.ProblemCode = domain.RecoveryRequired
		default:
			return nil, installationFailure(domain.InvalidArgument)
		}
		now := time.Now().UTC()
		o.FinishedAt = &now
		_, e = tx.Put(r.Kind, r.ID, r.Revision, "", "", o)
		return installationReceipt{r.ID}, e
	})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	var r store.Record
	e = s.Store.Read(ctx, func(tx *store.Tx) error {
		var problem error
		r, _, problem = workerUpdateRecord(tx, domain.ID(m.Id), actor, domain.ID(req.Msg.InstanceId))
		return problem
	})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	s.logger.InfoContext(ctx, "worker_update_reported", "operation_id", r.ID, "outcome", req.Msg.Outcome.String(), "version", req.Msg.InstalledVersion, "replayed", result.Replayed)
	return connect.NewResponse(&pb.ReportWorkerUpdateResponse{Update: rpc.Resource(r), RequestId: string(result.RequestID), Replayed: result.Replayed}), nil
}

// Only original signed admissions can cross the ordinary exact-version gate.
func (s *Service) signedWorkerVersion(tx *store.Tx, machine, device domain.ID, version string) bool {
	rows, e := tx.InstallationUpdates(machine, device, version, false)
	if e != nil {
		return false
	}
	for _, r := range rows {
		o, e := store.Decode[updateOperation](r)
		if e == nil && o.MachineID == machine && o.DeviceID == device && o.ClaimRequestID != "" && (o.State == updates.Running || o.State == updates.Uncertain || o.State == updates.Succeeded) && ((version == o.Version) || (version == o.CurrentVersion && o.State != updates.Succeeded)) {
			if o.State == updates.Succeeded || originalInstallationActor(tx, o.Actor) == nil {
				signed, e := s.verifyRelease(o.Manifest, o.CurrentVersion, time.Now().UTC())
				if _, machineState, problem := activeMachine(tx, machine); problem != nil || machineState.OS+"-"+machineState.Architecture != string(o.Target) {
					continue
				}
				if e == nil && signed.ManifestSHA256 == o.ManifestSHA256 && signed.Payload.Version == o.Version && signed.Payload.ProtocolVersion == rpc.ProtocolVersion {
					return true
				}
			}
		}
	}
	return false
}

func (s *Service) verifyRelease(raw []byte, current string, now time.Time) (updates.Verified, error) {
	if s.releaseVerifier != nil {
		return s.releaseVerifier(raw, current, now)
	}
	v, e := updates.NewVerifier()
	if e != nil {
		return updates.Verified{}, e
	}
	return v.Verify(raw, current, now)
}

// Completed history can verify an installed version, but it cannot replace a
// live controller. Only a drained original in-progress claim admits handoff.
func (s *Service) signedWorkerTransition(tx *store.Tx, machine, previous, device domain.ID, version string) bool {
	if !s.signedWorkerVersion(tx, machine, device, version) {
		return false
	}
	rows, e := tx.InstallationUpdates(machine, device, "", true)
	if e != nil {
		return false
	}
	admitted := false
	for _, r := range rows {
		o, e := store.Decode[updateOperation](r)
		if e == nil && o.ClaimRequestID != "" && (o.State == updates.Running || o.State == updates.Uncertain) && ((version == o.Version) || (version == o.CurrentVersion)) {
			admitted = true
		}
	}
	if !admitted {
		return false
	}
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	for _, stream := range []workerStream{s.workerStreams[machine], s.auxiliaryStreams[machine]} {
		if stream.Instance == previous {
			select {
			case <-stream.Done:
			default:
				return false
			}
		}
	}
	return true
}
