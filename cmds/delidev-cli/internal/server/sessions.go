package server

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Receipts contain identities only. An exact retry returns current state and
// cannot reproduce input content removed or edited after its original receipt.
type sessionReceipt struct {
	SessionID domain.ID `json:"session_id"`
	InputID   domain.ID `json:"input_id,omitempty"`
}

func sessionRecord(tx *store.Tx, id domain.ID) (store.Record, domain.Session, error) {
	r, err := tx.Get(domain.SessionKind, id)
	if err != nil {
		return r, domain.Session{}, err
	}
	v, err := store.Decode[domain.Session](r)
	return r, v, err
}

func (s *Service) sessionResult(ctx context.Context, result store.Result) (*pb.SessionChange, error) {
	var refs sessionReceipt
	if err := json.Unmarshal(result.Data, &refs); err != nil {
		return nil, err
	}
	if refs.SessionID == "" {
		return nil, domain.Fail(domain.NotFound, "The accepted session was deleted.", "An old request cannot recreate deleted work.")
	}
	change := &pb.SessionChange{RequestId: string(result.RequestID), Replayed: result.Replayed}
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		session, err := tx.Get(domain.SessionKind, refs.SessionID)
		if err != nil {
			return err
		}
		change.Session = rpc.Resource(session)
		value, err := store.Decode[domain.Session](session)
		if err != nil {
			return err
		}
		if value.Preparation != nil {
			job, err := tx.Get(domain.JobKind, value.Preparation.JobID)
			if err != nil {
				return err
			}
			if job.SessionID != refs.SessionID {
				return domain.Fail(domain.RecoveryRequired, "Workspace ownership is inconsistent.", "Preserve the data scope and inspect recovery.")
			}
			change.WorkspaceJob = rpc.Resource(job)
			if value.Preparation.RecoveryJobID != "" {
				recovery, err := tx.Get(domain.JobKind, value.Preparation.RecoveryJobID)
				if err != nil {
					return err
				}
				if recovery.SessionID != refs.SessionID {
					return domain.Fail(domain.RecoveryRequired, "Workspace recovery ownership is inconsistent.", "Preserve the data scope for recovery.")
				}
				change.RecoveryJob = rpc.Resource(recovery)
			}
		}
		if value.InitialExecution != nil {
			job, err := tx.SessionExecutionJob(session.ID, value.ExecutionSelection().ID)
			if err != nil {
				return err
			}
			change.ExecutionJob = rpc.Resource(job)
		}
		if value.ExecutionRecoveryJobID != "" {
			recovery, err := tx.Get(domain.JobKind, value.ExecutionRecoveryJobID)
			if err != nil {
				return err
			}
			job, err := store.Decode[domain.Job](recovery)
			if err != nil {
				return err
			}
			if recovery.SessionID != refs.SessionID || job.Type != domain.RecoverExecutionJob || value.Execution == nil || job.ParentID != value.Execution.JobID {
				return domain.ExecutionRecoveryUncertain()
			}
			change.ExecutionRecoveryJob = rpc.Resource(recovery)
		}
		if refs.InputID != "" {
			input, err := tx.Get(domain.QueueKind, refs.InputID)
			if err != nil {
				return err
			}
			if input.SessionID != refs.SessionID {
				return domain.Fail(domain.RecoveryRequired, "Queue ownership is inconsistent.", "Preserve the data scope and inspect recovery.")
			}
			change.Input = rpc.Resource(input)
		}
		return nil
	})
	return change, err
}

func validateSessionSelection(tx *store.Tx, input domain.CreateSession) error {
	r, err := tx.Get(domain.AgentKind, input.AgentID)
	if err != nil {
		return err
	}
	agent, err := store.Decode[domain.Agent](r)
	if err != nil {
		return err
	}
	if err := validateRelationships(tx, domain.AgentKind, r.ID, r.Revision, &agent); err != nil {
		return err
	}
	r, err = tx.Get(domain.MachineKind, input.MachineID)
	if err != nil {
		return err
	}
	machine, err := store.Decode[domain.Machine](r)
	if err != nil {
		return err
	}
	if machine.Disabled {
		return domain.Fail(domain.Unavailable, "The selected execution machine is disabled.", "Select an enabled Worker.")
	}
	if input.ProjectID == "" {
		return nil
	}
	r, err = tx.Get(domain.ProjectKind, input.ProjectID)
	if err != nil {
		return err
	}
	project, err := store.Decode[domain.Project](r)
	if err != nil {
		return err
	}
	if !project.Agents.Allows(input.AgentID) {
		return domain.Fail(domain.PermissionDenied, "This Agent Worker is excluded by the project.", "Select an allowed Agent Worker or update the project restrictions.")
	}
	for _, start := range input.Starting {
		if !slices.Contains(project.Repositories, start.RepositoryID) {
			return domain.Fail(domain.InvalidArgument, "A starting reference targets a repository outside the project.", "Select only the project's repositories.")
		}
	}
	for _, id := range project.Repositories {
		r, err := tx.Get(domain.RepositoryKind, id)
		if err != nil {
			return err
		}
		repo, err := store.Decode[domain.Repository](r)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(repo.Checkouts, func(c domain.Checkout) bool { return c.MachineID == input.MachineID }) {
			return domain.Fail(domain.MissingInput, "A project repository has no checkout on the selected Worker.", "Inspect and configure every project repository on that machine.")
		}
	}
	return nil
}

func appendSessionInput(tx *store.Tx, id domain.ID, session *domain.Session, input domain.SessionInput) (domain.ID, error) {
	if session.LastInputSequence >= 1<<63-2 || session.PendingInputs >= domain.MaxPendingInputs || session.PendingInputBytes > domain.MaxPendingInputBytes || session.PendingInputBytes+uint64(len(input.Prompt)) > domain.MaxPendingInputBytes {
		return "", domain.Fail(domain.ResourceExhausted, "The retained input queue is full.", "Remove undelivered input or wait for confirmed native acceptance before adding more.")
	}
	session.LastInputSequence++
	session.PendingInputs++
	session.PendingInputBytes += uint64(len(input.Prompt))
	itemID := domain.NewID()
	_, err := tx.Put(domain.QueueKind, itemID, 0, id, session.ProjectID, domain.QueuedInput{Sequence: session.LastInputSequence, ContentRevision: 1, Prompt: input.Prompt, Mode: input.Mode, Delivery: domain.InputQueued})
	return itemID, err
}

func (s *Service) CreateSession(ctx context.Context, req *connect.Request[pb.CreateSessionRequest]) (*connect.Response[pb.CreateSessionResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	var input domain.CreateSession
	if err := domain.Decode(req.Msg.DocumentJson, &input); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	input.ApplyDefaults()
	if err := input.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	origin, originDigest, err := s.authenticateLocalOrigin(ctx, input, req.Msg.LocalWorkerToken)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	// Bind receipt identity to the creating device as well as its selections.
	identity := struct {
		Input  domain.CreateSession
		Actor  domain.Principal
		Origin *domain.LocalOrigin `json:",omitempty"`
	}{input, actor, origin}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "session.create", identity, func(tx *store.Tx) (any, error) {
		if origin != nil {
			current, err := tx.Authenticate(originDigest[:])
			if err != nil {
				return nil, err
			}
			if current.Type != domain.WorkerDevice || current.DeviceID != origin.DeviceID || current.MachineID != origin.MachineID {
				return nil, localOriginRequired()
			}
		}
		if err := validateSessionSelection(tx, input); err != nil {
			return nil, err
		}
		id := domain.NewID()
		value := domain.Session{LocalOrigin: origin, Name: input.Name, AgentID: input.AgentID, MachineID: input.MachineID, ProjectID: input.ProjectID, Workspace: input.Workspace, Starting: input.Starting, Source: input.Source, CreatedBy: actor.DeviceID, Outcome: domain.ExecutionNotStarted, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Dispatch: domain.DispatchBlocked, Problem: domain.InitialExecutionPending()}
		preparation, err := sessionWorkspaceRequest(tx, id, value)
		if err != nil {
			return nil, err
		}
		if err := queueSessionWorkspace(tx, id, &value, preparation); err != nil {
			return nil, err
		}
		itemID, err := appendSessionInput(tx, id, &value, domain.SessionInput{Prompt: input.Prompt, Mode: input.Mode})
		if err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.SessionKind, id, 0, id, input.ProjectID, value); err != nil {
			return nil, err
		}
		return sessionReceipt{SessionID: id, InputID: itemID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	change, err := s.sessionResult(ctx, result)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.Info("session_accepted", "correlation_id", correlation, "session_id", change.Session.Id, "machine_id", input.MachineID, "workspace_type", input.Workspace, "source", input.Source, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.CreateSessionResponse{Change: change})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) EnqueueInput(ctx context.Context, req *connect.Request[pb.EnqueueInputRequest]) (*connect.Response[pb.EnqueueInputResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	var input domain.SessionInput
	if err := domain.Decode(req.Msg.DocumentJson, &input); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	input.ApplyDefaults()
	if err := input.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	identity := struct {
		SessionID string
		Input     domain.SessionInput
	}{req.Msg.SessionId, input}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "session.enqueue", identity, func(tx *store.Tx) (any, error) {
		r, value, err := sessionRecord(tx, domain.ID(req.Msg.SessionId))
		if err != nil {
			return nil, err
		}
		if value.Archive != domain.NotArchived {
			return nil, domain.Fail(domain.Conflict, "Archived or archiving sessions cannot accept new input.", "Restore the session first; restoration keeps execution paused.")
		}
		itemID, err := appendSessionInput(tx, r.ID, &value, input)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, value); err != nil {
			return nil, err
		}
		return sessionReceipt{SessionID: r.ID, InputID: itemID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	change, err := s.sessionResult(ctx, result)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.Info("session_input_accepted", "correlation_id", correlation, "session_id", req.Msg.SessionId, "input_id", change.Input.Id, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.EnqueueInputResponse{Change: change})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func validateSessionMutation(meta *pb.Mutation) error {
	if meta == nil || meta.ExpectedRevision == 0 {
		return domain.Fail(domain.MissingInput, "A mutation identity and current revision are required.", "Supply the exact entity ID, request ID and current revision.")
	}
	if err := domain.ID(meta.Id).Validate(); err != nil {
		return err
	}
	return domain.ID(meta.RequestId).Validate()
}

func (s *Service) changeQueuedInput(ctx context.Context, meta *pb.Mutation, sessionID domain.ID, prompt string, remove bool) (*pb.SessionChange, error) {
	if err := validateSessionMutation(meta); err != nil {
		return nil, err
	}
	if err := sessionID.Validate(); err != nil {
		return nil, err
	}
	if !remove {
		if err := domain.Text(prompt, "session input", domain.MaxPromptBytes, true); err != nil {
			return nil, err
		}
	}
	identity := struct {
		ID        domain.ID
		SessionID domain.ID
		Revision  uint64
		Prompt    string
		Remove    bool
	}{domain.ID(meta.Id), sessionID, meta.ExpectedRevision, prompt, remove}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "session.input.change", identity, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, sessionID)
		if err != nil {
			return nil, err
		}
		r, err := tx.Get(domain.QueueKind, domain.ID(meta.Id))
		if err != nil {
			return nil, err
		}
		if r.SessionID != sessionID {
			return nil, domain.Fail(domain.PermissionDenied, "The input belongs to another session.", "Use its actual owning session.")
		}
		if r.Revision != meta.ExpectedRevision {
			return nil, domain.Fail(domain.Conflict, "The input revision changed.", "Reload its current revision before editing or removing it.")
		}
		value, err := store.Decode[domain.QueuedInput](r)
		if err != nil {
			return nil, err
		}
		if value.Delivery != domain.InputQueued {
			return nil, domain.Fail(domain.Conflict, "Only undelivered queued input can be changed.", "Reconcile claimed or uncertain delivery before another operation.")
		}
		if session.PendingInputs == 0 || session.PendingInputBytes < uint64(len(value.Prompt)) {
			return nil, domain.Fail(domain.RecoveryRequired, "Queue accounting is inconsistent.", "Preserve the data scope and inspect recovery.")
		}
		pendingBytes := session.PendingInputBytes - uint64(len(value.Prompt)) + uint64(len(prompt))
		if pendingBytes > domain.MaxPendingInputBytes {
			return nil, domain.Fail(domain.ResourceExhausted, "The retained input queue is full.", "Use a smaller input or remove undelivered entries.")
		}
		value.Prompt = prompt
		value.ContentRevision++
		if remove {
			value.Prompt = ""
			value.Delivery = domain.InputRemoved
			session.PendingInputs--
		}
		session.PendingInputBytes = pendingBytes
		if _, err := tx.Put(domain.QueueKind, r.ID, r.Revision, r.SessionID, r.ProjectID, value); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return sessionReceipt{SessionID: sr.ID, InputID: r.ID}, nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info("session_input_changed", "request_id", meta.RequestId, "session_id", sessionID, "input_id", meta.Id, "removed", remove, "replayed", result.Replayed)
	return s.sessionResult(ctx, result)
}

func (s *Service) EditQueuedInput(ctx context.Context, req *connect.Request[pb.EditQueuedInputRequest]) (*connect.Response[pb.EditQueuedInputResponse], error) {
	change, err := s.changeQueuedInput(ctx, req.Msg.Mutation, domain.ID(req.Msg.SessionId), req.Msg.Prompt, false)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.EditQueuedInputResponse{Change: change})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) RemoveQueuedInput(ctx context.Context, req *connect.Request[pb.RemoveQueuedInputRequest]) (*connect.Response[pb.RemoveQueuedInputResponse], error) {
	change, err := s.changeQueuedInput(ctx, req.Msg.Mutation, domain.ID(req.Msg.SessionId), "", true)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.RemoveQueuedInputResponse{Change: change})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func sessionAction(value pb.SessionAction) (domain.SessionAction, error) {
	switch value {
	case pb.SessionAction_SESSION_ACTION_STOP:
		return domain.StopSession, nil
	case pb.SessionAction_SESSION_ACTION_ARCHIVE:
		return domain.ArchiveSession, nil
	case pb.SessionAction_SESSION_ACTION_RESTORE:
		return domain.RestoreSession, nil
	case pb.SessionAction_SESSION_ACTION_RESUME:
		return domain.ResumeSession, nil
	default:
		return "", domain.Fail(domain.InvalidArgument, "Unknown session control.", "Select Stop, Archive, Restore or Resume.")
	}
}

func (s *Service) ControlSession(ctx context.Context, req *connect.Request[pb.ControlSessionRequest]) (*connect.Response[pb.ControlSessionResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	meta := req.Msg.Mutation
	if err := validateSessionMutation(meta); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	action, err := sessionAction(req.Msg.Action)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	identity := struct {
		ID       string
		Revision uint64
		Action   domain.SessionAction
	}{meta.Id, meta.ExpectedRevision, action}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "session.control", identity, func(tx *store.Tx) (any, error) {
		r, value, err := sessionRecord(tx, domain.ID(meta.Id))
		if err != nil {
			return nil, err
		}
		if r.Revision != meta.ExpectedRevision {
			return nil, domain.Fail(domain.Conflict, "The session revision changed.", "Reload current state before controlling it.")
		}
		if action == domain.ResumeSession {
			if value.InitialExecution != nil {
				_, err := queueContinuation(tx, r, value, true)
				return sessionReceipt{SessionID: r.ID}, err
			}
			_, err := queueInitialExecution(tx, r, value, true)
			return sessionReceipt{SessionID: r.ID}, err
		}
		if value.InitialExecution != nil {
			if err := controlNativeSession(tx, r, &value, action); err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, value); err != nil {
				return nil, err
			}
			return sessionReceipt{SessionID: r.ID}, nil
		}
		if value.ActiveExecutionID != "" || value.Outcome != domain.ExecutionNotStarted || (value.Recovery != domain.NoRecovery && (value.Preparation == nil || value.Preparation.State != domain.PreparationUncertain)) {
			return nil, domain.Fail(domain.RecoveryRequired, "Native session ownership must be reconciled before this control can complete.", "Keep dispatch paused until owned native resources can be verified and stopped.")
		}
		switch action {
		case domain.StopSession:
			value.Dispatch = domain.DispatchPaused
			if _, err := stopSessionWorkspace(tx, &value); err != nil {
				return nil, err
			}
		case domain.ArchiveSession:
			value.Dispatch = domain.DispatchPaused
			stopped, err := stopSessionWorkspace(tx, &value)
			if err != nil {
				return nil, err
			}
			value.Archive = domain.ArchivePending
			if stopped && value.Recovery == domain.NoRecovery {
				value.Archive = domain.Archived
			}
		case domain.RestoreSession:
			if value.Archive != domain.Archived {
				return nil, domain.Fail(domain.Conflict, "The session is not archived.", "Inspect its current visibility state.")
			}
			value.Archive = domain.NotArchived
			value.Dispatch = domain.DispatchPaused
		}
		if _, err := tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, value); err != nil {
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
	s.logger.Info("session_control_committed", "correlation_id", correlation, "session_id", meta.Id, "action", action, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ControlSessionResponse{Change: change})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) RenameSession(ctx context.Context, req *connect.Request[pb.RenameSessionRequest]) (*connect.Response[pb.RenameSessionResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	meta := req.Msg.Mutation
	if err := validateSessionMutation(meta); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := domain.Text(req.Msg.Name, "session name", 256, true); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	identity := struct {
		ID       string
		Revision uint64
		Name     string
	}{meta.Id, meta.ExpectedRevision, req.Msg.Name}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "session.rename", identity, func(tx *store.Tx) (any, error) {
		r, value, err := sessionRecord(tx, domain.ID(meta.Id))
		if err != nil {
			return nil, err
		}
		value.Name = req.Msg.Name
		if _, err := tx.Put(domain.SessionKind, r.ID, meta.ExpectedRevision, r.ID, r.ProjectID, value); err != nil {
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
	response := connect.NewResponse(&pb.RenameSessionResponse{Change: change})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func sessionPageSize(size uint32) (int, error) {
	if size == 0 {
		return 50, nil
	}
	if size > store.MaxPage {
		return 0, domain.Fail(domain.InvalidArgument, "Invalid session page size.", "Use a page size from 1 through 200.")
	}
	return int(size), nil
}

func (s *Service) ListSessions(ctx context.Context, req *connect.Request[pb.ListSessionsRequest]) (*connect.Response[pb.ListSessionsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	limit, err := sessionPageSize(req.Msg.PageSize)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	f := store.SessionFilter{ProjectID: domain.ID(req.Msg.ProjectId), IncludeArchived: req.Msg.IncludeArchived, Limit: limit}
	scope := fmt.Sprintf("sessions:%s:%t", f.ProjectID, f.IncludeArchived)
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		f.After = cursor.After
	}
	records, more, err := s.Store.Sessions(ctx, f)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	result := &pb.ListSessionsResponse{}
	for _, r := range records {
		result.Sessions = append(result.Sessions, rpc.Resource(r))
	}
	if more {
		result.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: records[len(records)-1].ID})
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	response := connect.NewResponse(result)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) ListQueue(ctx context.Context, req *connect.Request[pb.ListQueueRequest]) (*connect.Response[pb.ListQueueResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	limit, err := sessionPageSize(req.Msg.PageSize)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	scope := "queue:" + req.Msg.SessionId
	var after uint64
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		after = cursor.Sequence
	}
	records, more, err := s.Store.Queue(ctx, domain.ID(req.Msg.SessionId), after, limit)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	result := &pb.ListQueueResponse{}
	for _, r := range records {
		result.Inputs = append(result.Inputs, rpc.Resource(r))
	}
	if more {
		value, err := store.Decode[domain.QueuedInput](records[len(records)-1])
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		result.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, Sequence: value.Sequence})
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	response := connect.NewResponse(result)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
