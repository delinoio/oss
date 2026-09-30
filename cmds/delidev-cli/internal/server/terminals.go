// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type terminalReceipt struct {
	TerminalID domain.ID `json:"terminal_id"`
}

func terminalRecord(tx *store.Tx, id domain.ID) (store.Record, domain.Terminal, error) {
	r, err := tx.Get(domain.TerminalKind, id)
	if err != nil {
		return r, domain.Terminal{}, err
	}
	value, err := store.Decode[domain.Terminal](r)
	return r, value, err
}

func terminalSize(rows, columns uint32) error {
	if rows > 500 || columns > 1000 {
		return domain.Fail(domain.InvalidArgument, "Terminal dimensions exceed their bounds.", "Use 1–500 rows and 1–1000 columns.")
	}
	return (process.TerminalSize{Rows: uint16(rows), Columns: uint16(columns)}).Validate()
}

func (s *Service) terminalResult(ctx context.Context, result store.Result) (*pb.CreateTerminalResponse, error) {
	var ref terminalReceipt
	if err := domain.Decode(result.Data, &ref); err != nil {
		return nil, err
	}
	r, err := s.Store.Get(ctx, domain.TerminalKind, ref.TerminalID)
	if err != nil {
		return nil, err
	}
	return &pb.CreateTerminalResponse{Terminal: rpc.Resource(r), Replayed: result.Replayed}, nil
}

func terminalClient(ctx context.Context) error {
	p, ok := domain.PrincipalFrom(ctx)
	if !ok || (p.Type != domain.OwnerDevice && p.Type != domain.ClientDevice) {
		return domain.Fail(domain.PermissionDenied, "Terminals require an owner or paired client.", "Use an authorized product client.")
	}
	return nil
}

func terminalMachine(tx *store.Tx, machine, instance domain.ID) error {
	if err := tx.Authorize(); err != nil {
		return err
	}
	_, m, err := activeMachine(tx, machine)
	if err != nil {
		return err
	}
	current, seen, err := tx.WorkerInstance(machine)
	if err != nil || current != instance || time.Since(seen) > domain.WorkerConnectionTimeout || seen.After(time.Now().Add(time.Second)) || !slices.Contains(m.WorkerCapabilities, domain.SessionTerminalsV1) {
		return domain.TerminalUnavailable()
	}
	return nil
}

func (s *Service) CreateTerminal(ctx context.Context, req *connect.Request[pb.CreateTerminalRequest]) (*connect.Response[pb.CreateTerminalResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.CreateTerminalResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	if err := terminalClient(ctx); err != nil {
		return fail(err)
	}
	if err := validateSessionMutation(req.Msg.Mutation); err != nil {
		return fail(err)
	}
	if err := terminalSize(req.Msg.Rows, req.Msg.Columns); err != nil {
		return fail(err)
	}
	if err := domain.Text(req.Msg.ShellOverride, "shell override", 4096, false); err != nil {
		return fail(err)
	}
	meta := req.Msg.Mutation
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "terminal.create", req.Msg, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, domain.ID(meta.Id))
		if err != nil {
			return nil, err
		}
		if sr.Revision != meta.ExpectedRevision || session.Archive != domain.NotArchived {
			return nil, domain.Fail(domain.Conflict, "The session changed or is archiving.", "Reload the active session before creating a terminal.")
		}
		if _, _, err := workspaceReadScope(tx, sr.ID); err != nil {
			return nil, err
		}
		instance, _, err := tx.WorkerInstance(session.MachineID)
		if err != nil {
			return nil, err
		}
		if err := terminalMachine(tx, session.MachineID, instance); err != nil {
			return nil, err
		}
		records, err := tx.SessionTerminals(sr.ID)
		if err != nil {
			return nil, err
		}
		active := 0
		for _, r := range records {
			t, err := store.Decode[domain.Terminal](r)
			if err != nil {
				return nil, err
			}
			if t.Live() {
				active++
			}
		}
		machineRecords, err := tx.ActiveMachineTerminals(session.MachineID)
		if err != nil {
			return nil, err
		}
		if active >= domain.MaxSessionTerminals || len(records) >= domain.MaxTerminalRecords || len(machineRecords) >= 32 {
			return nil, domain.Fail(domain.ResourceExhausted, "The terminal capacity is reached.", "Close unused terminals; each session permits eight live terminals and 128 retained records, and each Worker permits 32 live terminals.")
		}
		id := domain.NewID()
		value := domain.Terminal{OwnerInstanceID: instance, MachineID: session.MachineID, InstanceID: instance, ShellOverride: req.Msg.ShellOverride, Rows: uint16(req.Msg.Rows), Columns: uint16(req.Msg.Columns), State: domain.TerminalStarting, Pending: &domain.TerminalOperation{ID: domain.ID(meta.RequestId), Action: domain.TerminalCreate}}
		if _, err := tx.Put(domain.TerminalKind, id, 0, sr.ID, sr.ProjectID, value); err != nil {
			return nil, err
		}
		return terminalReceipt{id}, nil
	})
	if err != nil {
		return fail(err)
	}
	value, err := s.terminalResult(ctx, result)
	if err != nil {
		return fail(err)
	}
	s.logger.InfoContext(ctx, "terminal_creation_accepted", "terminal_id", value.Terminal.Id, "session_id", meta.Id, "replayed", result.Replayed)
	return connect.NewResponse(value), nil
}

func (s *Service) ControlTerminal(ctx context.Context, req *connect.Request[pb.ControlTerminalRequest]) (*connect.Response[pb.ControlTerminalResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.ControlTerminalResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	if err := terminalClient(ctx); err != nil {
		return fail(err)
	}
	if err := validateSessionMutation(req.Msg.Mutation); err != nil {
		return fail(err)
	}
	var action domain.TerminalAction
	switch req.Msg.Action {
	case pb.TerminalAction_TERMINAL_ACTION_INPUT:
		action = domain.TerminalInput
		if len(req.Msg.Input) == 0 || len(req.Msg.Input) > domain.MaxTerminalInput || req.Msg.Rows != 0 || req.Msg.Columns != 0 {
			return fail(domain.Fail(domain.InvalidArgument, "Invalid terminal input.", "Send 1–32768 bytes without resize fields."))
		}
	case pb.TerminalAction_TERMINAL_ACTION_RESIZE:
		action = domain.TerminalResize
		if err := terminalSize(req.Msg.Rows, req.Msg.Columns); err != nil {
			return fail(err)
		}
		if len(req.Msg.Input) != 0 {
			return fail(domain.Fail(domain.InvalidArgument, "Resize cannot contain input.", "Send dimensions separately."))
		}
	case pb.TerminalAction_TERMINAL_ACTION_CLOSE:
		action = domain.TerminalClose
		if len(req.Msg.Input) != 0 || req.Msg.Rows != 0 || req.Msg.Columns != 0 {
			return fail(domain.Fail(domain.InvalidArgument, "Close cannot contain input or dimensions.", "Close the original terminal by identity."))
		}
	default:
		return fail(domain.Fail(domain.InvalidArgument, "Unknown terminal action.", "Select input, resize or close."))
	}
	meta := req.Msg.Mutation
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "terminal.control", req.Msg, func(tx *store.Tx) (any, error) {
		r, value, err := terminalRecord(tx, domain.ID(meta.Id))
		if err != nil {
			return nil, err
		}
		if r.Revision != meta.ExpectedRevision {
			return nil, domain.Fail(domain.Conflict, "The terminal revision changed.", "Read the original terminal before controlling it.")
		}
		_, session, err := sessionRecord(tx, r.SessionID)
		if err != nil {
			return nil, err
		}
		if action == domain.TerminalClose {
			if value.Live() && value.CloseRequestID == "" {
				value.CloseRequestID = domain.ID(meta.RequestId)
			}
		} else {
			if value.State != domain.TerminalRunning || value.Pending != nil || value.CloseRequestID != "" || session.Archive != domain.NotArchived {
				return nil, domain.Fail(domain.Conflict, "The terminal cannot accept another operation.", "Wait for the original pending operation or inspect cleanup.")
			}
			if err := terminalMachine(tx, value.MachineID, value.InstanceID); err != nil {
				return nil, err
			}
			value.Pending = &domain.TerminalOperation{ID: domain.ID(meta.RequestId), Action: action, Input: slices.Clone(req.Msg.Input), Rows: uint16(req.Msg.Rows), Columns: uint16(req.Msg.Columns)}
		}
		if _, err := tx.Put(domain.TerminalKind, r.ID, r.Revision, r.SessionID, r.ProjectID, value); err != nil {
			return nil, err
		}
		return terminalReceipt{r.ID}, nil
	})
	if err != nil {
		return fail(err)
	}
	value, err := s.terminalResult(ctx, result)
	if err != nil {
		return fail(err)
	}
	s.logger.InfoContext(ctx, "terminal_control_accepted", "terminal_id", meta.Id, "action", action, "replayed", result.Replayed)
	return connect.NewResponse(&pb.ControlTerminalResponse{Terminal: value.Terminal, Replayed: value.Replayed}), nil
}

func terminalAssignment(tx *store.Tx, r store.Record, value domain.Terminal) (terminal.Assignment, error) {
	assignment := terminal.Assignment{ID: r.ID, SessionID: r.SessionID, Terminal: value}
	if value.CloseRequestID != "" {
		assignment.Operation = domain.TerminalOperation{ID: value.CloseRequestID, Action: domain.TerminalClose}
		return assignment, nil
	}
	if value.Pending == nil {
		return assignment, domain.TerminalUnavailable()
	}
	assignment.Operation = *value.Pending
	if assignment.Operation.Action == domain.TerminalCreate {
		input, manifest, err := workspaceReadScope(tx, r.SessionID)
		if err != nil {
			return assignment, err
		}
		assignment.Preparation, assignment.Manifest = &input, &manifest
	}
	return assignment, nil
}

func loseTerminalAuthority(tx *store.Tx, machine domain.ID) error {
	records, err := tx.ActiveMachineTerminals(machine)
	if err != nil {
		return err
	}
	for _, record := range records {
		value, err := store.Decode[domain.Terminal](record)
		if err != nil {
			return err
		}
		value.State = domain.TerminalUncertain
		value.Problem = domain.Fail(domain.RecoveryRequired, "The original Worker has not confirmed terminal cleanup.", "Close and reconcile exact process ownership on the original paired Worker.")
		if _, err := tx.Put(domain.TerminalKind, record.ID, record.Revision, record.SessionID, record.ProjectID, value); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) WatchTerminals(ctx context.Context, req *connect.Request[pb.WatchTerminalsRequest], stream *connect.ServerStream[pb.WatchTerminalsResponse]) error {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return rpc.Error(err, correlation)
	}
	machine, instance := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)
	primary, err := s.primaryWorkspaceStream(machine, instance)
	if err != nil {
		return rpc.Error(err, correlation)
	}
	controller, ok := ctx.Value(writeControllerKey{}).(*http.ResponseController)
	if !ok {
		return rpc.Error(domain.TerminalUnavailable(), correlation)
	}
	send := func(message *pb.WatchTerminalsResponse) error {
		if err := controller.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
			return err
		}
		return stream.Send(message)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	seen := map[domain.ID]domain.ID{}
	nextHeartbeat := time.Time{}
	for {
		var assignments []terminal.Assignment
		err := s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := terminalMachine(tx, machine, instance); err != nil {
				return err
			}
			records, err := tx.ActiveMachineTerminals(machine)
			if err != nil {
				return err
			}
			for _, r := range records {
				value, err := store.Decode[domain.Terminal](r)
				if err != nil {
					return err
				}
				if value.CloseRequestID == "" && (value.InstanceID != instance || value.Pending == nil) {
					continue
				}
				assignment, err := terminalAssignment(tx, r, value)
				if err != nil {
					return err
				}
				assignments = append(assignments, assignment)
			}
			return nil
		})
		if err != nil {
			return rpc.Error(err, correlation)
		}
		for _, assignment := range assignments {
			if seen[assignment.ID] == assignment.Operation.ID {
				continue
			}
			raw, _ := json.Marshal(assignment)
			if err := send(&pb.WatchTerminalsResponse{AssignmentJson: raw}); err != nil {
				return err
			}
			seen[assignment.ID] = assignment.Operation.ID
		}
		// Retain only active identities so lifetime terminal history cannot grow
		// this per-stream deduplication map without bound.
		for id := range seen {
			found := false
			for _, a := range assignments {
				found = found || a.ID == id
			}
			if !found {
				delete(seen, id)
			}
		}
		if time.Now().After(nextHeartbeat) {
			if err := send(&pb.WatchTerminalsResponse{Heartbeat: true}); err != nil {
				return err
			}
			nextHeartbeat = time.Now().Add(10 * time.Second)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-primary.Done:
			return nil
		case <-ticker.C:
		}
	}
}

func (s *Service) ClaimTerminal(ctx context.Context, req *connect.Request[pb.ClaimTerminalRequest]) (*connect.Response[pb.ClaimTerminalResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.ClaimTerminalResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return fail(err)
	}
	for _, id := range []string{req.Msg.RequestId, req.Msg.TerminalId, req.Msg.OperationId} {
		if err := domain.ID(id).Validate(); err != nil {
			return fail(err)
		}
	}
	actor, _ := domain.PrincipalFrom(ctx)
	machine, instance := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "terminal.claim", req.Msg, func(tx *store.Tx) (any, error) {
		if err := terminalMachine(tx, machine, instance); err != nil {
			return nil, err
		}
		r, value, err := terminalRecord(tx, domain.ID(req.Msg.TerminalId))
		if err != nil {
			return nil, err
		}
		if value.MachineID != machine || !value.Live() || (value.DeviceID != "" && value.DeviceID != actor.DeviceID) {
			return nil, domain.TerminalUnavailable()
		}
		if value.CloseRequestID == domain.ID(req.Msg.OperationId) {
			value.InstanceID, value.DeviceID = instance, actor.DeviceID
		} else {
			_, session, err := sessionRecord(tx, r.SessionID)
			if err != nil {
				return nil, err
			}
			if value.InstanceID != instance || value.CloseRequestID != "" || session.Archive != domain.NotArchived || value.Pending == nil || value.Pending.ID != domain.ID(req.Msg.OperationId) || value.Pending.Claimed {
				return nil, domain.Fail(domain.RecoveryRequired, "This terminal operation cannot be claimed again.", "Reconcile the original request without repeating a native side effect.")
			}
			if _, err := terminalAssignment(tx, r, value); err != nil {
				return nil, err
			}
			value.Pending.Claimed, value.DeviceID = true, actor.DeviceID
		}
		if _, err := tx.Put(domain.TerminalKind, r.ID, r.Revision, r.SessionID, r.ProjectID, value); err != nil {
			return nil, err
		}
		return terminalReceipt{r.ID}, nil
	})
	if err != nil {
		return fail(err)
	}
	var ref terminalReceipt
	if err := domain.Decode(result.Data, &ref); err != nil {
		return fail(err)
	}
	var assignment terminal.Assignment
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := terminalMachine(tx, machine, instance); err != nil {
			return err
		}
		r, value, err := terminalRecord(tx, ref.TerminalID)
		if err != nil {
			return err
		}
		if value.InstanceID != instance || value.DeviceID != actor.DeviceID {
			return domain.TerminalUnavailable()
		}
		assignment, err = terminalAssignment(tx, r, value)
		if err != nil {
			return err
		}
		if assignment.Operation.ID != domain.ID(req.Msg.OperationId) {
			return domain.TerminalUnavailable()
		}
		return nil
	})
	if err != nil {
		return fail(err)
	}
	raw, _ := json.Marshal(assignment)
	return connect.NewResponse(&pb.ClaimTerminalResponse{AssignmentJson: raw}), nil
}

func (s *Service) ReportTerminal(ctx context.Context, req *connect.Request[pb.ReportTerminalRequest]) (*connect.Response[pb.ReportTerminalResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.ReportTerminalResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return fail(err)
	}
	for _, id := range []string{req.Msg.RequestId, req.Msg.TerminalId} {
		if err := domain.ID(id).Validate(); err != nil {
			return fail(err)
		}
	}
	if req.Msg.OperationId != "" {
		if err := domain.ID(req.Msg.OperationId).Validate(); err != nil {
			return fail(err)
		}
	}
	var output terminal.Result
	if len(req.Msg.ResultJson) > terminal.MaxResultBytes || domain.Decode(req.Msg.ResultJson, &output) != nil || output.Validate() != nil {
		return fail(domain.Fail(domain.InvalidArgument, "Invalid terminal report.", "Report bounded original native facts."))
	}
	actor, _ := domain.PrincipalFrom(ctx)
	machine, instance := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "terminal.report", req.Msg, func(tx *store.Tx) (any, error) {
		if err := terminalMachine(tx, machine, instance); err != nil {
			return nil, err
		}
		r, value, err := terminalRecord(tx, domain.ID(req.Msg.TerminalId))
		if err != nil {
			return nil, err
		}
		if value.MachineID != machine || value.InstanceID != instance || value.DeviceID != actor.DeviceID || !value.Live() {
			return nil, domain.TerminalUnavailable()
		}
		op := domain.ID(req.Msg.OperationId)
		creating := value.Pending != nil && value.Pending.Action == domain.TerminalCreate && (op == value.Pending.ID || op == value.CloseRequestID)
		if !creating {
			if output.Shell != value.Shell || output.Cwd != value.Cwd || output.Rows != value.Rows || output.Columns != value.Columns {
				if value.Pending == nil || value.Pending.Action != domain.TerminalResize || (op != value.Pending.ID && op != value.CloseRequestID) || output.Rows != value.Pending.Rows || output.Columns != value.Pending.Columns || output.Shell != value.Shell || output.Cwd != value.Cwd {
					return nil, domain.TerminalUnavailable()
				}
			}
		} else if output.Rows != value.Rows || output.Columns != value.Columns {
			return nil, domain.TerminalUnavailable()
		}

		if op == "" {
			if (value.Pending != nil && (value.Pending.Claimed || value.Pending.Action == domain.TerminalCreate)) || (output.State != domain.TerminalExited && output.State != domain.TerminalUncertain) {
				return nil, domain.TerminalUnavailable()
			}
			value.Pending = nil
		} else if op == value.CloseRequestID {
			if output.State != domain.TerminalClosed && output.State != domain.TerminalUncertain {
				return nil, domain.TerminalUnavailable()
			}
			value.CloseRequestID = ""
			value.Pending = nil
		} else {
			if value.Pending == nil || value.Pending.ID != op || !value.Pending.Claimed {
				return nil, domain.TerminalUnavailable()
			}
			if value.Pending.Action != domain.TerminalCreate && (output.Shell != value.Shell || output.Cwd != value.Cwd) {
				return nil, domain.TerminalUnavailable()
			}
			value.Pending = nil
		}
		value.OutputLost = value.OutputLost || output.OutputLost
		value.State, value.CleanupVerified, value.ExitCode, value.Problem = output.State, output.CleanupVerified, output.ExitCode, output.Problem
		value.Shell, value.Cwd, value.Rows, value.Columns = output.Shell, output.Cwd, output.Rows, output.Columns
		if _, err := tx.Put(domain.TerminalKind, r.ID, r.Revision, r.SessionID, r.ProjectID, value); err != nil {
			return nil, err
		}
		if output.CleanupVerified {
			if err := tx.CompleteTerminalArchive(r.SessionID); err != nil {
				return nil, err
			}
		}
		return terminalReceipt{r.ID}, nil
	})
	if err != nil {
		return fail(err)
	}
	// Receipt replay still requires the current Worker authority. A report that
	// once committed cannot project terminal metadata to a replaced instance.
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := terminalMachine(tx, machine, instance); err != nil {
			return err
		}
		_, value, err := terminalRecord(tx, domain.ID(req.Msg.TerminalId))
		if err != nil {
			return err
		}
		if value.MachineID != machine || value.InstanceID != instance || value.DeviceID != actor.DeviceID {
			return domain.TerminalUnavailable()
		}
		return nil
	})
	if err != nil {
		return fail(err)
	}
	current, err := s.terminalResult(ctx, result)
	if err != nil {
		return fail(err)
	}
	s.logger.InfoContext(ctx, "terminal_result_committed", "terminal_id", req.Msg.TerminalId, "state", output.State, "cleanup_verified", output.CleanupVerified, "replayed", result.Replayed)
	return connect.NewResponse(&pb.ReportTerminalResponse{Terminal: current.Terminal}), nil
}
