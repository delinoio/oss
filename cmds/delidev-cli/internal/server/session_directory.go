// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func directoryActor(ctx context.Context) error {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice {
		return domain.Fail(domain.PermissionDenied, "Only an owner or paired client can change a session directory.", "Use an authenticated product client.")
	}
	return nil
}
func directoryFence(session domain.Session) error {
	if session.DirectoryJobID != "" {
		return domain.DirectoryUncertain()
	}
	return nil
}
func sameDirectoryRef(a, b *domain.SessionDirectoryRef) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// Verify the source independently of unrelated context-action capabilities.
func directorySource(tx *store.Tx, sr store.Record, session domain.Session, request, repository domain.ID, relative string, actor domain.Principal) (domain.SessionDirectoryInput, error) {
	var empty domain.SessionDirectoryInput
	p := session.Execution
	if !session.WorkspaceAvailable() || session.DirectoryJobID != "" || session.CompactionJobID != "" || session.IsSidechat() || session.ExecutionRecoveryJobID != "" || session.InitialExecution == nil || session.Preparation == nil || session.Preparation.State != domain.PreparationReady || session.ActiveExecutionID != "" || session.PendingSteerID != "" || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.Dispatch != domain.DispatchReady || session.Outcome != domain.ExecutionSucceeded || session.PendingInputs != 0 || session.PendingInputBytes != 0 || p == nil || !p.CleanupVerified || p.Waiting != (domain.NativeWaiting{}) || p.UnconfirmedResponses != 0 || len(p.Subagents) != 0 || !p.NativeCompactions.Closed() || !p.AutoReviews.Closed() || session.TitleState == domain.TitleQueued || session.TitleState == domain.TitleRunning || session.TitleState == domain.TitleUncertain {
		return empty, domain.DirectoryUncertain()
	}
	if session.InitialExecution.Configuration.Harness != domain.Codex {
		return empty, domain.Fail(domain.Unsupported, "This session has no supported directory transition profile.", "Use an original settled Codex root session.")
	}
	if err := tx.RequireNoSessionFork(sr.ID); err != nil {
		return empty, err
	}
	terminals, err := tx.SessionTerminalsPending(sr.ID)
	if err != nil {
		return empty, err
	}
	forwards, err := tx.SessionForwardsPending(sr.ID)
	if err != nil {
		return empty, err
	}
	if terminals || forwards {
		return empty, domain.DirectoryUncertain()
	}
	r, err := tx.SessionExecutionJob(sr.ID, p.ExecutionID)
	if err != nil {
		return empty, err
	}
	j, err := store.Decode[domain.Job](r)
	if err != nil {
		return empty, err
	}
	var original domain.ExecutionJobInput
	var done domain.ExecutionCompletion
	if j.State != domain.JobSucceeded || domain.Decode(j.Input, &original) != nil || original.Validate() != nil || !session.OwnsExecution(original) || domain.Decode(j.Output, &done) != nil || done.Version != 2 || done.Validate() != nil || done.Outcome != domain.ExecutionSucceeded {
		return empty, domain.DirectoryUncertain()
	}
	if err := verifyDirectoryContextAction(tx, sr.ID, directoryContextAction(session, original.ExecutionID)); err != nil {
		return empty, err
	}
	if err := checkContinuationInputs(tx, sr.ID, original, *p); err != nil {
		return empty, err
	}
	_, machine, err := activeMachine(tx, session.MachineID)
	if err != nil {
		return empty, err
	}
	if !slices.Contains(machine.WorkerCapabilities, domain.SessionDirectoryV1) {
		return empty, domain.Fail(domain.Unsupported, "The original Worker does not support session directory changes.", "Update and reconnect the original Worker.")
	}
	instance, seen, err := tx.WorkerInstance(session.MachineID)
	if err != nil {
		return empty, err
	}
	if instance.Validate() != nil || instance != j.InstanceID || time.Since(seen) > domain.WorkerConnectionTimeout || seen.After(time.Now().UTC().Add(time.Second)) {
		return empty, domain.DirectoryUncertain()
	}
	if err := checkedExecutionSource(tx, sr, session, machine, original); err != nil {
		return empty, err
	}
	// Worker validates this selection against the original complete manifest;
	// current project data cannot add a root to that immutable assignment.
	input := domain.SessionDirectoryInput{ContextRevision: session.ContextRevision, ContextAction: directoryContextAction(session, original.ExecutionID), Version: 1, RequestID: request, RequestingActor: actor, GenerationID: domain.NewID(), SourceJobID: r.ID, HistoryExecutionID: session.NativeExecutionRoot(), Assignment: original, Completion: done, PreviousExecution: p.NativePublication(), RepositoryID: repository, RelativePath: relative, Previous: session.Directory}
	return input, input.Validate()
}

func (s *Service) ChangeSessionDirectory(ctx context.Context, req *connect.Request[pb.ChangeSessionDirectoryRequest]) (*connect.Response[pb.ChangeSessionDirectoryResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if err := directoryActor(ctx); err != nil {
		return nil, rpc.Error(err, corr)
	}
	m := req.Msg.Mutation
	if err := validateSessionMutation(m); err != nil {
		return nil, rpc.Error(err, corr)
	}
	repository := domain.ID(req.Msg.RepositoryId)
	if err := repository.Validate(); repository != "" && err != nil {
		return nil, rpc.Error(err, corr)
	}
	if err := domain.ValidateSessionDirectoryPath(req.Msg.RelativePath); err != nil {
		return nil, rpc.Error(err, corr)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		Session, Repository domain.ID
		Revision            uint64
		RelativePath        string
		Actor               domain.Principal
	}{domain.ID(m.Id), repository, m.ExpectedRevision, req.Msg.RelativePath, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "session.directory", identity, func(tx *store.Tx) (any, error) {
		if err := tx.Authorize(); err != nil {
			return nil, err
		}
		sr, session, err := sessionRecord(tx, identity.Session)
		if err != nil {
			return nil, err
		}
		if sr.Revision != identity.Revision {
			return nil, continuationConflict()
		}
		input, err := directorySource(tx, sr, session, domain.ID(m.RequestId), repository, identity.RelativePath, actor)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		job, err := tx.PutJob(domain.NewID(), 0, sr.ID, sr.ProjectID, domain.Job{Type: domain.ChangeSessionDirectoryJob, State: domain.JobQueued, MachineID: session.MachineID, ParentID: input.SourceJobID, Input: raw, AcceptedAt: time.Now().UTC()})
		if err != nil {
			return nil, err
		}
		session.DirectoryJobID = job.ID
		session.Dispatch = domain.DispatchPaused
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return struct{ JobID domain.ID }{job.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	var ref struct{ JobID domain.ID }
	if json.Unmarshal(result.Data, &ref) != nil {
		return nil, rpc.Error(domain.DirectoryUncertain(), corr)
	}
	var operation *pb.SessionDirectoryOperation
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		var e error
		operation, e = readDirectoryOperation(tx, identity.Session, domain.ID(m.RequestId), ref.JobID, actor)
		return e
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	s.logger.InfoContext(ctx, "session_directory_accepted", "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ChangeSessionDirectoryResponse{Operation: operation})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func readDirectoryOperation(tx *store.Tx, session, request, jobID domain.ID, actor domain.Principal) (*pb.SessionDirectoryOperation, error) {
	if err := tx.Authorize(); err != nil {
		return nil, err
	}
	r, err := tx.Get(domain.JobKind, jobID)
	if err != nil {
		return nil, err
	}
	j, err := store.Decode[domain.Job](r)
	if err != nil {
		return nil, err
	}
	var input domain.SessionDirectoryInput
	if r.SessionID != session || j.Type != domain.ChangeSessionDirectoryJob || domain.DecodeWithLimit(j.Input, &input, domain.MaxCompactionInputBytes) != nil || input.Validate() != nil || input.RequestID != request || input.RequestingActor != actor {
		return nil, domain.DirectoryUncertain()
	}
	op := &pb.SessionDirectoryOperation{SessionId: string(session), RequestId: string(request)}
	// The original assignment contains private manifest paths and prompts. Never
	// send its raw Resource document to a product client.
	safe := j
	safe.Input = nil
	safe.Output = nil
	data, err := json.Marshal(safe)
	if err != nil {
		return nil, err
	}
	r.Data = data
	op.Job = rpc.Resource(r)
	if j.State == domain.JobSucceeded {
		var result domain.SessionDirectoryResult
		if domain.Decode(j.Output, &result) != nil || result.Validate() != nil {
			return nil, domain.DirectoryUncertain()
		}
		g := result.Checkpoint
		op.Generation = &pb.SessionDirectoryGeneration{GenerationId: string(g.GenerationID), JobId: string(g.JobID), RequestId: string(g.RequestID), SourceExecutionId: string(g.ExecutionID), RepositoryId: string(g.RepositoryID), RelativePath: g.RelativePath, PreviousGenerationId: string(g.PreviousGenerationID)}
	}
	return op, nil
}
func (s *Service) GetSessionDirectoryOperation(ctx context.Context, req *connect.Request[pb.GetSessionDirectoryOperationRequest]) (*connect.Response[pb.GetSessionDirectoryOperationResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if err := directoryActor(ctx); err != nil {
		return nil, rpc.Error(err, corr)
	}
	session, request := domain.ID(req.Msg.SessionId), domain.ID(req.Msg.RequestId)
	if session.Validate() != nil || request.Validate() != nil {
		return nil, rpc.Error(domain.DirectoryUncertain(), corr)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	var operation *pb.SessionDirectoryOperation
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var after domain.ID
		for {
			rows, err := tx.List(store.Filter{Kind: domain.JobKind, SessionID: session, After: after, Limit: store.MaxPage})
			if err != nil {
				return err
			}
			for _, r := range rows {
				j, err := store.Decode[domain.Job](r)
				if err != nil {
					return err
				}
				if j.Type == domain.ChangeSessionDirectoryJob {
					var i domain.SessionDirectoryInput
					if domain.DecodeWithLimit(j.Input, &i, domain.MaxCompactionInputBytes) != nil {
						return domain.DirectoryUncertain()
					}
					if i.RequestID == request {
						operation, err = readDirectoryOperation(tx, session, request, r.ID, actor)
						return err
					}
				}
				after = r.ID
			}
			if len(rows) < store.MaxPage {
				return domain.Fail(domain.NotFound, "The original directory operation was not found.", "Retain the original session and request identity; do not repeat the mutation.")
			}
		}
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	response := connect.NewResponse(&pb.GetSessionDirectoryOperationResponse{Operation: operation})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func directoryClaimSource(tx *store.Tx, r store.Record, j domain.Job) (domain.SessionDirectoryInput, store.Record, domain.Session, error) {
	var input domain.SessionDirectoryInput
	var empty store.Record
	var missing domain.Session
	if j.Type != domain.ChangeSessionDirectoryJob || domain.DecodeWithLimit(j.Input, &input, domain.MaxCompactionInputBytes) != nil || input.Validate() != nil {
		return input, empty, missing, domain.DirectoryUncertain()
	}
	sr, session, err := sessionRecord(tx, r.SessionID)
	if err != nil {
		return input, sr, session, err
	}
	p := session.Execution
	if !session.WorkspaceAvailable() || session.DirectoryJobID != r.ID || session.ActiveExecutionID != "" || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.PendingInputs != 0 || session.PendingSteerID != "" || p == nil || !session.OwnsExecution(input.Assignment) || session.ContextRevision != input.ContextRevision || !sameContextAction(directoryContextAction(session, input.Assignment.ExecutionID), input.ContextAction) || session.NativeExecutionRoot() != input.HistoryExecutionID || !sameDirectoryRef(session.Directory, input.Previous) {
		return input, sr, session, domain.DirectoryUncertain()
	}
	current, _ := json.Marshal(p.NativePublication())
	original, _ := json.Marshal(input.PreviousExecution)
	if !bytes.Equal(current, original) {
		return input, sr, session, domain.DirectoryUncertain()
	}
	_, machine, err := activeMachine(tx, j.MachineID)
	if err != nil {
		return input, sr, session, err
	}
	if !slices.Contains(machine.WorkerCapabilities, domain.SessionDirectoryV1) {
		return input, sr, session, domain.DirectoryUncertain()
	}
	if err := verifyDirectoryContextAction(tx, sr.ID, input.ContextAction); err != nil {
		return input, sr, session, err
	}
	if err := checkedExecutionSource(tx, sr, session, machine, input.Assignment); err != nil {
		return input, sr, session, err
	}
	return input, sr, session, nil
}
func finishSessionDirectory(tx *store.Tx, r store.Record, j domain.Job, revision uint64, raw json.RawMessage, problem *domain.Error) (store.Record, error) {
	input, sr, session, err := directoryClaimSource(tx, r, j)
	var result domain.SessionDirectoryResult
	verified := err == nil && problem == nil && domain.Decode(raw, &result) == nil && result.Validate() == nil && result.RequestID == input.RequestID && result.GenerationID == input.GenerationID && result.ExecutionID == input.Assignment.ExecutionID && result.Checkpoint.JobID == r.ID && result.Checkpoint.RepositoryID == input.RepositoryID && result.Checkpoint.RelativePath == input.RelativePath
	previous := domain.ID("")
	if input.Previous != nil {
		previous = input.Previous.GenerationID
	}
	verified = verified && result.Checkpoint.PreviousGenerationID == previous
	canceled, cancelErr := tx.JobCancellationRequested(r.ID)
	if cancelErr != nil {
		return store.Record{}, cancelErr
	}
	verified = verified && !canceled
	if err != nil {
		sr, session, err = sessionRecord(tx, r.SessionID)
		if err != nil {
			return store.Record{}, err
		}
		if session.DirectoryJobID != r.ID {
			return store.Record{}, domain.DirectoryUncertain()
		}
	}
	now := time.Now().UTC()
	j.FinishedAt = &now
	if verified {
		session.Directory = &result.Checkpoint
		session.DirectoryJobID = ""
		session.Dispatch = domain.DispatchReady
		j.State = domain.JobSucceeded
		j.Problem = nil
		j.Output = raw
	} else {
		session.Recovery = domain.NeedsRecovery
		session.Dispatch = domain.DispatchPaused
		session.NextExecutionIntent = ""
		j.State = domain.JobUncertain
		j.Problem = domain.DirectoryUncertain()
		j.Output = nil
	}
	if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
		return store.Record{}, err
	}
	saved, err := tx.PutJob(r.ID, revision, r.SessionID, r.ProjectID, j)
	return store.Record{ID: saved.ID}, err
}
func loseSessionDirectory(tx *store.Tx, r store.Record, j domain.Job) error {
	sr, session, err := sessionRecord(tx, r.SessionID)
	if err != nil {
		return err
	}
	if session.DirectoryJobID != r.ID {
		return domain.DirectoryUncertain()
	}
	// A canceled never-claimed job proves no Worker effects. Lost claimed work
	// keeps its pending identity and recovery fence; it cannot be replayed.
	if j.State == domain.JobCanceled && j.InstanceID == "" {
		session.DirectoryJobID = ""
	} else {
		session.Recovery = domain.NeedsRecovery
	}
	session.Dispatch = domain.DispatchPaused
	session.NextExecutionIntent = ""
	_, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session)
	return err
}

func directoryContextAction(session domain.Session, execution domain.ID) *domain.SessionCompactionRef {
	if session.Compaction != nil && session.Compaction.ExecutionID == execution {
		return session.Compaction
	}
	return nil
}
func sameContextAction(a, b *domain.SessionCompactionRef) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func verifyDirectoryContextAction(tx *store.Tx, session domain.ID, ref *domain.SessionCompactionRef) error {
	if ref == nil {
		return nil
	}
	r, err := tx.Get(domain.JobKind, ref.JobID)
	if err != nil {
		return err
	}
	j, err := store.Decode[domain.Job](r)
	if err != nil {
		return err
	}
	var result domain.SessionCompactionResult
	if r.SessionID != session || j.Type != domain.CompactSessionJob || j.State != domain.JobSucceeded || domain.Decode(j.Output, &result) != nil || result.Validate() != nil || !sameContextAction(&result.Checkpoint, ref) {
		return domain.DirectoryUncertain()
	}
	return nil
}
