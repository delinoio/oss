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
	deleting, err := tx.SessionDeleting(id)
	if err != nil {
		return store.Record{}, domain.Session{}, err
	}
	if deleting {
		return store.Record{}, domain.Session{}, domain.SessionDeletionPending()
	}
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
			if recovery.SessionID != refs.SessionID || job.Type != domain.RecoverExecutionJob || change.ExecutionJob == nil || job.ParentID != domain.ID(change.ExecutionJob.Id) || (value.Execution != nil && job.ParentID != value.Execution.JobID) {
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
		if input.Workspace == domain.Worktree {
			if _, err := domain.ParseRepositoryCloneURL(repo.RemoteURL); err != nil {
				return err
			}
			if !slices.Contains(machine.WorkerCapabilities, domain.RemoteWorkspaceCloneV1) {
				return domain.Fail(domain.Unsupported, "The selected Runner Device does not support remote workspace cloning.", "Update and reconnect that Worker before starting a Worktree session.")
			}
		}
		if input.Workspace == domain.Local && !slices.ContainsFunc(repo.Checkouts, func(c domain.Checkout) bool { return c.MachineID == input.MachineID }) {
			return domain.Fail(domain.MissingInput, "A project repository has no checkout on the selected Worker.", "Inspect and configure every project repository on that machine.")
		}
	}
	return nil
}

func appendSessionInput(tx *store.Tx, id domain.ID, session *domain.Session, input domain.SessionInput, names ...map[domain.ID]string) (domain.ID, error) {
	if session.LastInputSequence >= 1<<63-2 || session.PendingInputs >= domain.MaxPendingInputs || session.PendingInputBytes > domain.MaxPendingInputBytes || session.PendingInputBytes+uint64(len(input.Prompt)) > domain.MaxPendingInputBytes {
		return "", domain.Fail(domain.ResourceExhausted, "The retained input queue is full.", "Remove undelivered input or wait for confirmed native acceptance before adding more.")
	}
	if len(input.Skills) > 0 {
		if err := tx.CheckSkillSnapshotCapacity(id, "", len(input.Skills)); err != nil {
			return "", err
		}
	}
	session.LastInputSequence++
	session.PendingInputs++
	session.PendingInputBytes += uint64(len(input.Prompt))
	itemID := domain.NewID()
	var skillNames map[domain.ID]string
	if len(names) > 0 {
		skillNames = names[0]
	}
	_, err := tx.Put(domain.QueueKind, itemID, 0, id, session.ProjectID, domain.QueuedInput{Sequence: session.LastInputSequence, ContentRevision: 1, Prompt: input.Prompt, Mode: input.Mode, Skills: input.Skills, Attachments: input.Attachments, SkillNames: skillNames, Delivery: domain.InputQueued})
	return itemID, err
}

func acceptedSession(input domain.CreateSession, origin *domain.LocalOrigin, creator domain.ID) domain.Session {
	session := domain.Session{EstimatedCostBudget: input.EstimatedCostBudget, LocalOrigin: origin, Name: input.Name, NameMode: input.NameMode, AgentID: input.AgentID, MachineID: input.MachineID, ProjectID: input.ProjectID, Workspace: input.Workspace, Starting: input.Starting, Source: input.Source, CreatedBy: creator, Outcome: domain.ExecutionNotStarted, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Dispatch: domain.DispatchBlocked, Problem: domain.InitialExecutionPending()}
	if input.NameMode == domain.AutomaticSessionName {
		session.Name = "New session"
		session.NameOwner = domain.AutomaticNameOwner
		session.NameGeneration = 1
		session.TitleState = domain.TitleWaiting
	}
	return session
}

func (s *Service) CreateSession(ctx context.Context, req *connect.Request[pb.CreateSessionRequest]) (*connect.Response[pb.CreateSessionResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	var input domain.CreateSession
	if err := domain.Decode(req.Msg.DocumentJson, &input); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if len(input.Skills) > 0 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Skills require typed selections.", "Select skills through the supported composer."), correlation)
	}
	bindings, bindErr := skillBindings(req.Msg.Skills, req.Msg.RequestId)
	if bindErr != nil {
		return nil, rpc.Error(bindErr, correlation)
	}
	input.Skills = bindings

	if err := rejectDocumentAttachments(req.Msg.Attachments, input.Attachments); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var attachmentError error
	input.Attachments, attachmentError = requestImageAttachments(req.Msg.Attachments)
	if attachmentError != nil {
		return nil, rpc.Error(attachmentError, correlation)
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
	var preparedSkillScope *domain.SkillReadRequest
	var preparedSkillNames map[domain.ID]string
	if len(input.Skills) > 0 {
		previous, found, e := s.Store.Replay(ctx, domain.ID(req.Msg.RequestId), "session.create", identity)
		if e != nil {
			return nil, rpc.Error(e, correlation)
		}
		if found {
			change, e := s.sessionResult(ctx, previous)
			if e != nil {
				return nil, rpc.Error(e, correlation)
			}
			return connect.NewResponse(&pb.CreateSessionResponse{Change: change}), nil
		}
		prepared, finish, e := s.prepareSkills(ctx, domain.SkillReadRequest{ProjectID: input.ProjectID, MachineID: input.MachineID, AgentID: input.AgentID, Selections: input.Skills}, domain.ID(req.Msg.RequestId), "session.create", identity)
		defer finish()
		if e != nil {
			return nil, rpc.Error(e, correlation)
		}
		preparedSkillScope = prepared.Scope
		preparedSkillNames = skillEntryNames(prepared.Entries)
	}

	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "session.create", identity, func(tx *store.Tx) (any, error) {
		if err := acceptSkillScope(tx, preparedSkillScope); err != nil {
			return nil, err
		}
		if origin != nil {
			current, err := tx.Authenticate(originDigest[:])
			if err != nil {
				return nil, err
			}
			if current.Type != domain.WorkerDevice || current.DeviceID != origin.DeviceID || current.MachineID != origin.MachineID {
				return nil, localOriginRequired()
			}
		}
		if err := checkImageRoute(tx, input.AgentID, input.MachineID, input.Attachments); err != nil {
			return nil, err
		}
		if err := validateSessionSelection(tx, input); err != nil {
			return nil, err
		}
		id := domain.NewID()
		value := acceptedSession(input, origin, actor.DeviceID)
		preparation, err := sessionWorkspaceRequest(tx, id, value)
		if err != nil {
			return nil, err
		}
		if err := queueSessionWorkspace(tx, id, &value, preparation); err != nil {
			return nil, err
		}
		itemID, err := appendSessionInput(tx, id, &value, domain.SessionInput{Prompt: input.Prompt, Mode: input.Mode, Skills: input.Skills, Attachments: input.Attachments}, preparedSkillNames)
		if err != nil {
			return nil, err
		}
		if err := claimInputImages(tx, actor, domain.ID(req.Msg.RequestId), id, itemID, input.MachineID, input.Attachments, true); err != nil {
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
	if len(input.Skills) > 0 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Skills require typed selections.", "Select skills through the supported composer."), correlation)
	}
	bindings, bindErr := skillBindings(req.Msg.Skills, req.Msg.RequestId)
	if bindErr != nil {
		return nil, rpc.Error(bindErr, correlation)
	}
	input.Skills = bindings

	if err := rejectDocumentAttachments(req.Msg.Attachments, input.Attachments); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var attachmentError error
	input.Attachments, attachmentError = requestImageAttachments(req.Msg.Attachments)
	if attachmentError != nil {
		return nil, rpc.Error(attachmentError, correlation)
	}
	input.ApplyDefaults()
	if err := input.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	identity := struct {
		Actor     *domain.Principal `json:",omitempty"`
		SessionID string
		Input     domain.SessionInput
	}{SessionID: req.Msg.SessionId, Input: input}
	if len(input.Skills) > 0 {
		actor, _ := domain.PrincipalFrom(ctx)
		identity.Actor = &actor
	}
	var preparedSkillScope *domain.SkillReadRequest
	var preparedSkillNames map[domain.ID]string
	if len(input.Skills) > 0 {
		previous, found, e := s.Store.Replay(ctx, domain.ID(req.Msg.RequestId), "session.enqueue", identity)
		if e != nil {
			return nil, rpc.Error(e, correlation)
		}
		if found {
			change, e := s.sessionResult(ctx, previous)
			if e != nil {
				return nil, rpc.Error(e, correlation)
			}
			return connect.NewResponse(&pb.EnqueueInputResponse{Change: change}), nil
		}
		var session domain.Session
		e = s.Store.Read(ctx, func(tx *store.Tx) error {
			var e error
			_, session, e = sessionRecord(tx, domain.ID(req.Msg.SessionId))
			if e == nil {
				e = tx.CheckSkillSnapshotCapacity(domain.ID(req.Msg.SessionId), "", len(input.Skills))
			}
			return e
		})
		if e != nil {
			return nil, rpc.Error(e, correlation)
		}
		prepared, finish, e := s.prepareSkills(ctx, domain.SkillReadRequest{MachineID: session.MachineID, AgentID: session.AgentID, SessionID: domain.ID(req.Msg.SessionId), Selections: input.Skills}, domain.ID(req.Msg.RequestId), "session.enqueue", identity)
		defer finish()
		if e != nil {
			return nil, rpc.Error(e, correlation)
		}
		preparedSkillScope = prepared.Scope
		preparedSkillNames = skillEntryNames(prepared.Entries)
	}

	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "session.enqueue", identity, func(tx *store.Tx) (any, error) {
		if err := acceptSkillScope(tx, preparedSkillScope); err != nil {
			return nil, err
		}
		r, value, err := sessionRecord(tx, domain.ID(req.Msg.SessionId))
		if err != nil {
			return nil, err
		}
		if value.Archive != domain.NotArchived {
			return nil, domain.Fail(domain.Conflict, "Archived or archiving sessions cannot accept new input.", "Restore the session first; restoration keeps execution paused.")
		}
		if value.IsSidechat() && len(input.Attachments) > 0 {
			return nil, domain.UnsupportedImageInput()
		}
		if err := checkImageRoute(tx, value.AgentID, value.MachineID, input.Attachments); err != nil {
			return nil, err
		}
		itemID, err := appendSessionInput(tx, r.ID, &value, input, preparedSkillNames)
		if err != nil {
			return nil, err
		}
		if err := claimInputImages(tx, imageOperationActor(ctx), domain.ID(req.Msg.RequestId), r.ID, itemID, value.MachineID, input.Attachments, false); err != nil {
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

func (s *Service) changeQueuedInput(ctx context.Context, meta *pb.Mutation, sessionID domain.ID, prompt string, remove bool, selections *pb.SkillSelectionList) (*pb.SessionChange, error) {
	if err := validateSessionMutation(meta); err != nil {
		return nil, err
	}
	if err := sessionID.Validate(); err != nil {
		return nil, err
	}
	if !remove {
		if err := domain.Text(prompt, "session input", domain.MaxPromptBytes, false); err != nil {
			return nil, err
		}
	}
	identity := struct {
		ID        domain.ID
		SessionID domain.ID
		Revision  uint64
		Prompt    string
		Remove    bool
		Skills    *pb.SkillSelectionList `json:",omitempty"`
		Actor     *domain.Principal      `json:",omitempty"`
	}{ID: domain.ID(meta.Id), SessionID: sessionID, Revision: meta.ExpectedRevision, Prompt: prompt, Remove: remove, Skills: selections}
	var preparedScope *domain.SkillReadRequest
	var nextBindings []domain.SkillBinding
	var nextNames map[domain.ID]string
	if selections != nil && !remove {
		actor, _ := domain.PrincipalFrom(ctx)
		identity.Actor = &actor
		previous, found, err := s.Store.Replay(ctx, domain.ID(meta.RequestId), "session.input.change", identity)
		if err != nil {
			return nil, err
		}
		if found {
			return s.sessionResult(ctx, previous)
		}
		requested, err := skillBindings(selections, meta.RequestId)
		if err != nil {
			return nil, err
		}
		var session domain.Session
		var old domain.QueuedInput
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := tx.Authorize(); err != nil {
				return err
			}
			_, value, e := sessionRecord(tx, sessionID)
			if e != nil {
				return e
			}
			session = value
			row, e := tx.Get(domain.QueueKind, domain.ID(meta.Id))
			if e != nil {
				return e
			}
			if row.SessionID != sessionID || row.Revision != meta.ExpectedRevision {
				return domain.Fail(domain.Conflict, "The input revision changed.", "Reload the original input before editing.")
			}
			old, e = store.Decode[domain.QueuedInput](row)
			if e != nil {
				return e
			}
			if old.Delivery != domain.InputQueued {
				return domain.Fail(domain.Conflict, "Only queued input can be edited.", "Preserve claimed or uncertain input.")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		nextNames = map[domain.ID]string{}
		fresh := []domain.SkillBinding{}
		for _, selection := range requested {
			retained := false
			for _, binding := range old.Skills {
				if sameSelectedSkill(selection, binding) {
					selection = binding
					retained = true
					nextNames[binding.SkillID] = old.SkillNames[binding.SkillID]
					break
				}
			}
			nextBindings = append(nextBindings, selection)
			if !retained {
				fresh = append(fresh, selection)
			}
		}
		if len(fresh) > 0 {
			retired := append([]domain.SkillBinding{}, old.RetiredSkills...)
			for _, binding := range old.Skills {
				if !slices.Contains(nextBindings, binding) && !slices.Contains(retired, binding) {
					retired = append(retired, binding)
				}
			}
			if err := s.Store.Read(ctx, func(tx *store.Tx) error {
				return tx.CheckSkillSnapshotCapacity(sessionID, domain.ID(meta.Id), len(nextBindings)+len(retired))
			}); err != nil {
				return nil, err
			}
			prepared, finish, e := s.prepareSkills(ctx, domain.SkillReadRequest{MachineID: session.MachineID, AgentID: session.AgentID, SessionID: sessionID, Selections: fresh}, domain.ID(meta.RequestId), "session.input.change", identity)
			defer finish()
			if e != nil {
				return nil, e
			}
			preparedScope = prepared.Scope
			for id, name := range skillEntryNames(prepared.Entries) {
				nextNames[id] = name
			}
		}
	}
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
		fixRow, fix, hasFix, err := tx.PRRemediationForInput(r.ID)
		if err != nil {
			return nil, err
		}
		if hasFix && fix.GitTarget != nil && !remove {
			return nil, domain.Fail(domain.Conflict, "A manual PR fix retains its original input.", "Remove the queued fix explicitly and submit a new revision-bound request instead of editing its evidence.")
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
		if len(value.Skills) > 0 && !remove && selections == nil {
			return nil, domain.Fail(domain.Conflict, "Skill-bound input cannot be edited as plain text.", "Use a client that supports explicit skill selections.")
		}
		if selections != nil && !remove {
			if err := acceptSkillScope(tx, preparedScope); err != nil {
				return nil, err
			}
			for _, binding := range value.Skills {
				if !slices.ContainsFunc(nextBindings, func(next domain.SkillBinding) bool { return next == binding }) && !slices.Contains(value.RetiredSkills, binding) {
					value.RetiredSkills = append(value.RetiredSkills, binding)
				}
			}
			if err := tx.CheckSkillSnapshotCapacity(sessionID, r.ID, len(value.RetiredSkills)+len(nextBindings)); err != nil {
				return nil, err
			}
			value.Skills = nextBindings
			value.SkillNames = nextNames
		}
		if !remove && (domain.SessionInput{Prompt: prompt, Mode: value.Mode, Attachments: value.Attachments}).Validate() != nil {
			return nil, domain.InvalidImageInput()
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
		if remove && hasFix && fix.GitTarget != nil {
			if _, err := tx.CancelPRRemediation(fixRow.ID, fixRow.Revision); err != nil {
				return nil, err
			}
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
	change, err := s.changeQueuedInput(ctx, req.Msg.Mutation, domain.ID(req.Msg.SessionId), req.Msg.Prompt, false, req.Msg.Skills)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.EditQueuedInputResponse{Change: change})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) RemoveQueuedInput(ctx context.Context, req *connect.Request[pb.RemoveQueuedInputRequest]) (*connect.Response[pb.RemoveQueuedInputResponse], error) {
	change, err := s.changeQueuedInput(ctx, req.Msg.Mutation, domain.ID(req.Msg.SessionId), "", true, nil)
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
	var deniedProviderID, startupRetryJob domain.ID
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "session.control", identity, func(tx *store.Tx) (any, error) {
		r, value, err := sessionRecord(tx, domain.ID(meta.Id))
		if err != nil {
			return nil, err
		}
		if r.Revision != meta.ExpectedRevision {
			return nil, domain.Fail(domain.Conflict, "The session revision changed.", "Reload current state before controlling it.")
		}
		value.AutomaticRemediationStopped = action != domain.ResumeSession
		if action == domain.ArchiveSession {
			if err := tx.StopForwards(r.ID, ""); err != nil {
				return nil, err
			}
		}
		titleCleanupPending := false
		if action == domain.StopSession || action == domain.ArchiveSession {
			titleCleanupPending, err = cancelSessionTitleJob(tx, value.TitleJobID)
			if err != nil {
				return nil, err
			}
			if value.NameOwner == domain.AutomaticNameOwner && (value.TitleState == domain.TitleWaiting || value.TitleState == domain.TitleQueued || value.TitleState == domain.TitleRunning || value.TitleState == domain.TitleUncertain) {
				if value.NameGeneration == ^uint64(0) {
					return nil, domain.Fail(domain.ResourceExhausted, "Session title ownership reached its generation limit.", "Preserve the current title and inspect the original operation.")
				}
				value.NameGeneration++
				value.TitleState, value.TitleReason = domain.TitleSkipped, domain.TitleReasonCanceled
			}
		}
		if action == domain.ResumeSession {
			if !value.WorkspaceAvailable() {
				return nil, domain.Fail(domain.Conflict, "Workspace storage is pending, absent or uncertain.", "Restore or reconcile the original storage operation before Resume.")
			}
			deniedProviderID, err = requireSessionProviderEnabled(tx, value)
			if err != nil {
				return nil, err
			}
			if value.Startup != nil && value.Startup.Failure != nil && value.Startup.Failure.State == domain.StartupFailed {
				retry, err := queueExecutionStartupRetry(tx, r, value)
				startupRetryJob = retry.ID
				return sessionReceipt{SessionID: r.ID}, err
			}
			if value.StartupRejection != nil {
				return nil, domain.Fail(domain.Conflict, "This input was rejected before native startup and cannot be resumed.", "Preserve this attempt and create a fresh authorized PR fix after resolving its rejection.")
			}
			if ir, err := tx.OldestQueuedInput(r.ID); err == nil {
				_, fix, found, err := tx.PRRemediationForInput(ir.ID)
				if err != nil {
					return nil, err
				}
				if found && fix.GitTarget != nil {
					// Explicit Resume unpauses only. The dispatcher still owns
					// fresh remote gates and the original immutable assignment.
					if value.Dispatch != domain.DispatchPaused || value.Archive != domain.NotArchived || value.Recovery != domain.NoRecovery || value.ActiveExecutionID != "" || value.Preparation == nil || value.Preparation.State != domain.PreparationReady {
						return nil, firstDispatchConflict()
					}
					value.Dispatch, value.Problem = domain.DispatchReady, nil
					if _, err := tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, value); err != nil {
						return nil, err
					}
					return sessionReceipt{SessionID: r.ID}, nil
				}
			}
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
			if action == domain.ArchiveSession && titleCleanupPending {
				value.Archive = domain.ArchivePending
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
			if stopped && value.Recovery == domain.NoRecovery && !titleCleanupPending {
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
		if domain.SafeError(err).Code == domain.ProviderDisabled {
			s.logger.InfoContext(ctx, "session_dispatch_denied", "operation", "resume", "session_id", meta.Id, "session_revision", meta.ExpectedRevision, "provider_id", deniedProviderID, "reason", domain.ProviderDisabled, "enabled", false)
		}
		return nil, rpc.Error(err, correlation)
	}
	change, err := s.sessionResult(ctx, result)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if startupRetryJob != "" && !result.Replayed {
		s.logger.Info("execution_startup_retry_admitted", "session_id", meta.Id, "job_id", startupRetryJob, "request_id", meta.RequestId)
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
		if value.NameGeneration == ^uint64(0) {
			return nil, domain.Fail(domain.ResourceExhausted, "Session title ownership reached its generation limit.", "Preserve the current title and inspect the original operation.")
		}
		if _, err := cancelSessionTitleJob(tx, value.TitleJobID); err != nil {
			return nil, err
		}
		value.NameMode = domain.ManualSessionName
		value.NameOwner = domain.ManualNameOwner
		value.NameGeneration++
		value.TitleState = domain.TitleSkipped
		value.TitleReason = domain.TitleReasonManualRename
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

func sameSelectedSkill(a, b domain.SkillBinding) bool {
	return a.WorkerDeviceID == b.WorkerDeviceID && a.InventoryID == b.InventoryID && a.SkillID == b.SkillID && a.ContentRevision == b.ContentRevision
}
func skillEntryNames(entries []domain.SkillEntry) map[domain.ID]string {
	if len(entries) == 0 {
		return nil
	}
	names := map[domain.ID]string{}
	for _, entry := range entries {
		names[entry.SkillID] = entry.Name
	}
	return names
}
