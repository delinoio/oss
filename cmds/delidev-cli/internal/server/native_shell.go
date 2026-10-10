// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func nativeShellActor(ctx context.Context) error {
	a, ok := domain.PrincipalFrom(ctx)
	if !ok || a.Type != domain.OwnerDevice && a.Type != domain.ClientDevice {
		return domain.Fail(domain.PermissionDenied, "Only a human product client can run or cancel native shell commands.", "Use the authenticated owner or paired client; execution credentials cannot authorize this operation.")
	}
	return nil
}

func (s *Service) RunNativeShell(ctx context.Context, req *connect.Request[pb.RunNativeShellRequest]) (*connect.Response[pb.RunNativeShellResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if err := nativeShellActor(ctx); err != nil {
		return nil, rpc.Error(err, corr)
	}
	m := req.Msg.Mutation
	command := domain.NativeShellCommand{Command: req.Msg.Command, TimeoutMS: req.Msg.TimeoutMs, FullAccessConfirmed: req.Msg.FullAccessConfirmed}
	if m == nil || m.ExpectedRevision == 0 || domain.ID(m.Id).Validate() != nil || command.Validate() != nil {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Confirm an explicit full-access native shell action.", "Supply the original session revision and confirm that this command runs outside the native sandbox."), corr)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		Actor   domain.Principal
		Request *pb.RunNativeShellRequest
	}{actor, req.Msg}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "native-shell.run", identity, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, domain.ID(m.Id))
		if err != nil {
			return nil, err
		}
		if sr.Revision != m.ExpectedRevision {
			return nil, domain.Fail(domain.Conflict, "The selected session changed.", "Read the original session before a new explicit action.")
		}
		if session.IsSidechat() || session.InitialExecution == nil || session.InitialExecution.Configuration.Harness != domain.Codex {
			return nil, domain.Fail(domain.Unsupported, "Native shell requires an ordinary original Codex session.", "Keep independent terminals and read-only Sidechat separate.")
		}
		source, err := contextActionSource(tx, sr, session, domain.NewID(), false, true)
		if err != nil {
			return nil, err
		}
		source.Shell = &command
		if source.Validate() != nil {
			return nil, domain.NativeShellUncertain()
		}
		raw, err := json.Marshal(source)
		if err != nil {
			return nil, err
		}
		job, err := tx.PutJob(domain.ID(m.RequestId), 0, sr.ID, sr.ProjectID, domain.Job{Type: domain.NativeShellJob, State: domain.JobQueued, MachineID: session.MachineID, ParentID: source.SourceJobID, Input: raw, AcceptedAt: time.Now().UTC()})
		if err != nil {
			return nil, err
		}
		session.NativeShellJobID = job.ID
		session.Dispatch, session.NextExecutionIntent = domain.DispatchPaused, ""
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return job.ID, nil
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	job, err := s.nativeShellRecord(ctx, domain.ID(m.RequestId))
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	s.logger.InfoContext(ctx, "native_shell_accepted", "session_id", m.Id, "job_id", job.ID, "request_id", m.RequestId, "replayed", result.Replayed)
	return connect.NewResponse(&pb.RunNativeShellResponse{Job: rpc.Resource(job), RequestId: m.RequestId, Replayed: result.Replayed}), nil
}

func (s *Service) nativeShellRecord(ctx context.Context, id domain.ID) (record store.Record, returned error) {
	returned = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		r, err := tx.Get(domain.JobKind, id)
		if err != nil {
			return err
		}
		j, err := store.Decode[domain.Job](r)
		if err != nil || j.Type != domain.NativeShellJob {
			return domain.Fail(domain.NotFound, "The original native shell job is unavailable.", "Read the accepted request identity; this read never runs a command.")
		}
		record = r
		return nil
	})
	return
}
func (s *Service) GetNativeShell(ctx context.Context, req *connect.Request[pb.GetNativeShellRequest]) (*connect.Response[pb.GetNativeShellResponse], error) {
	if err := nativeShellActor(ctx); err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	r, err := s.nativeShellRecord(ctx, domain.ID(req.Msg.JobId))
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.GetNativeShellResponse{Job: rpc.Resource(r)}), nil
}
func (s *Service) CancelNativeShell(ctx context.Context, req *connect.Request[pb.CancelNativeShellRequest]) (*connect.Response[pb.CancelNativeShellResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if err := nativeShellActor(ctx); err != nil {
		return nil, rpc.Error(err, corr)
	}
	m := req.Msg.Mutation
	if m == nil || m.ExpectedRevision == 0 {
		return nil, rpc.Error(domain.NativeShellUncertain(), corr)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "native-shell.cancel", struct {
		Actor    domain.Principal
		Mutation *pb.Mutation
	}{actor, m}, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, domain.ID(m.Id))
		if err != nil {
			return nil, err
		}
		j, err := store.Decode[domain.Job](r)
		if err != nil || j.Type != domain.NativeShellJob || r.Revision != m.ExpectedRevision || j.State != domain.JobQueued && j.State != domain.JobClaimed {
			return nil, domain.Fail(domain.Conflict, "The native shell operation changed or already settled.", "Read the original operation; do not replace its cancellation identity.")
		}
		if err := tx.RequestJobCancellation(r.ID); err != nil {
			return nil, err
		}
		if j.State == domain.JobQueued {
			sr, session, err := sessionRecord(tx, r.SessionID)
			if err != nil || session.NativeShellJobID != r.ID {
				return nil, domain.NativeShellUncertain()
			}
			now := time.Now().UTC()
			j.State, j.FinishedAt = domain.JobCanceled, &now
			j.Problem = domain.Fail(domain.Canceled, "The shell command was canceled before dispatch.", "No native shell send was admitted.")
			session.NativeShellJobID = ""
			if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
				return nil, err
			}
			if _, err := tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, j); err != nil {
				return nil, err
			}
		}
		return r.ID, nil
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	r, err := s.nativeShellRecord(ctx, domain.ID(m.Id))
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	return connect.NewResponse(&pb.CancelNativeShellResponse{Job: rpc.Resource(r), RequestId: m.RequestId, Replayed: result.Replayed}), nil
}

// Live publication and final completion authenticate the immutable assignment,
// not the live job revision changed by earlier observation publications.
func nativeShellClaim(tx *store.Tx, r store.Record, j domain.Job, revision uint64) (domain.SessionCompactionInput, error) {
	var input domain.SessionCompactionInput
	assigned, err := tx.JobAssignment(r.ID)
	var original domain.Job
	if err != nil || assigned.Revision != revision || domain.DecodeCompactionJob(assigned.Data, &original) != nil || original.Type != domain.NativeShellJob || original.State != domain.JobClaimed || original.MachineID != j.MachineID || original.InstanceID != j.InstanceID || original.AssignedDeviceID != j.AssignedDeviceID || !bytes.Equal(original.Input, j.Input) || domain.DecodeCompactionInput(j.Input, &input) != nil || input.Validate() != nil || input.Shell == nil || input.Assignment.SessionID != r.SessionID {
		return input, domain.NativeShellUncertain()
	}
	return input, nil
}

func (s *Service) PublishNativeShell(ctx context.Context, req *connect.Request[pb.PublishNativeShellRequest]) (*connect.Response[pb.PublishNativeShellResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, corr)
	}
	m := req.Msg.Mutation
	var observation domain.NativeShellObservation
	if m == nil || domain.Decode(req.Msg.ObservationJson, &observation) != nil || observation.Validate() != nil || observation.CleanupVerified {
		return nil, rpc.Error(domain.NativeShellUncertain(), corr)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "native-shell.publish", struct {
		Actor   domain.Principal
		Request *pb.PublishNativeShellRequest
	}{actor, req.Msg}, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, domain.ID(m.Id))
		if err != nil {
			return nil, err
		}
		j, err := store.Decode[domain.Job](r)
		if err != nil || j.Type != domain.NativeShellJob || j.State != domain.JobClaimed || j.MachineID != domain.ID(req.Msg.MachineId) || j.InstanceID != domain.ID(req.Msg.InstanceId) || j.AssignedDeviceID != actor.DeviceID || currentInstance(tx, j.MachineID, j.InstanceID) != nil {
			return nil, domain.NativeShellUncertain()
		}
		input, err := nativeShellClaim(tx, r, j, m.ExpectedRevision)
		if err != nil || input.ActionID != observation.ActionID || input.Completion.NativeThreadID != domain.NativeIdentity(observation.NativeThreadID) {
			return nil, domain.NativeShellUncertain()
		}
		if len(j.Output) > 0 {
			var previous domain.NativeShellObservation
			if domain.Decode(j.Output, &previous) != nil || !nativeShellProgress(previous, observation) {
				return nil, domain.NativeShellUncertain()
			}
		} else if observation.Sequence != 1 {
			return nil, domain.NativeShellUncertain()
		}
		j.Output = req.Msg.ObservationJson
		return tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, j)
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	var r store.Record
	if domain.DecodeWithLimit(result.Data, &r, domain.MaxCompactionJobBytes) != nil {
		return nil, rpc.Error(domain.NativeShellUncertain(), corr)
	}
	return connect.NewResponse(&pb.PublishNativeShellResponse{Job: rpc.Resource(r)}), nil
}

func nativeShellProgress(previous, next domain.NativeShellObservation) bool {
	if next.Sequence != previous.Sequence+1 || previous.ActionID != next.ActionID || previous.NativeThreadID != next.NativeThreadID || previous.NativeTurnID != "" && previous.NativeTurnID != next.NativeTurnID || previous.Terminal && !next.Terminal || previous.Delivery != domain.NativeShellUncertainDelivery && previous.Delivery != next.Delivery || len(next.Processes) < len(previous.Processes) {
		return false
	}
	for n, before := range previous.Processes {
		after := next.Processes[n]
		if before.ItemID != after.ItemID || before.Command != after.Command || before.Cwd != after.Cwd || before.ProcessID != nil && (after.ProcessID == nil || *before.ProcessID != *after.ProcessID) || before.Status != domain.NativeShellRunning && before.Status != after.Status || len(after.Output) < len(before.Output) || after.Output[:len(before.Output)] != before.Output || before.Status != domain.NativeShellRunning && (!reflect.DeepEqual(before.ExitCode, after.ExitCode) || !reflect.DeepEqual(before.AggregatedOutput, after.AggregatedOutput)) {
			return false
		}
	}
	return true
}

func finishNativeShell(tx *store.Tx, r store.Record, j domain.Job, revision uint64, raw json.RawMessage, problem *domain.Error) (store.Record, error) {
	input, err := nativeShellClaim(tx, r, j, revision)
	if err != nil {
		return store.Record{}, err
	}
	sr, session, err := sessionRecord(tx, r.SessionID)
	if err != nil || session.NativeShellJobID != r.ID || !session.OwnsExecution(input.Assignment) {
		return store.Record{}, domain.NativeShellUncertain()
	}
	var done domain.NativeShellObservation
	verified := problem == nil && domain.Decode(raw, &done) == nil && done.Validate() == nil && done.ActionID == input.ActionID && done.NativeThreadID == domain.ID(input.Completion.NativeThreadID) && done.Terminal && done.CleanupVerified && done.Delivery == domain.NativeShellAcknowledged
	if verified && len(j.Output) > 0 {
		var previous domain.NativeShellObservation
		verified = domain.Decode(j.Output, &previous) == nil && nativeShellProgress(previous, done)
	}
	canceled, err := tx.JobCancellationRequested(r.ID)
	if err != nil {
		return store.Record{}, err
	}
	now := time.Now().UTC()
	j.FinishedAt = &now
	if verified {
		j.Output, j.Problem, j.State = raw, nil, domain.JobSucceeded
		if canceled {
			j.State = domain.JobCanceled
		}
		session.NativeShellJobID = ""
		// Keep Stop/Archive's pause. A shell cannot Resume an ordinary session.
		if !canceled && session.Archive == domain.NotArchived {
			session.Dispatch, session.NextExecutionIntent = input.Dispatch, input.Intent
		}
		if session.Archive == domain.ArchivePending {
			session.Archive = domain.Archived
		}
	} else {
		j.State, j.Problem = domain.JobUncertain, domain.NativeShellUncertain()
		session.Recovery, session.Dispatch = domain.NeedsRecovery, domain.DispatchPaused
	}
	if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
		return store.Record{}, err
	}
	return tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, j)
}
