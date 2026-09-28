package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type workspaceObservation struct {
	request   workspace.ReadRequest
	delivered bool
	result    chan workspaceReadReply
}
type workspaceReadReply struct {
	document []byte
	problem  error
}
type workspaceReader struct {
	machine, instance, device, primary domain.ID
	done                               <-chan struct{}
	requests                           chan []byte
	pending                            *workspaceObservation // Protected by workspaceReadsMu.
}

func workspaceReadUnavailable() error {
	return domain.Fail(domain.Unavailable, "The workspace file reader is unavailable.", "Connect the owning Worker and refresh the workspace view.")
}

func (s *Service) primaryWorkspaceStream(machine, instance domain.ID) (workerStream, error) {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	value, ok := s.workerStreams[machine]
	if !ok || value.Instance != instance || value.Done == nil {
		return value, workspaceReadUnavailable()
	}
	select {
	case <-value.Done:
		return value, workspaceReadUnavailable()
	default:
	}
	return value, nil
}

func currentWorkspaceReader(tx *store.Tx, reader *workspaceReader) error {
	if err := tx.Authorize(); err != nil {
		return err
	}
	instance, seen, err := tx.WorkerInstance(reader.machine)
	if err != nil || instance != reader.instance || seen.After(time.Now().Add(time.Second)) || time.Since(seen) > domain.WorkerConnectionTimeout {
		return workspaceReadUnavailable()
	}
	if _, _, err := activeMachine(tx, reader.machine); err != nil {
		return err
	}
	row, err := tx.Get(domain.DeviceKind, reader.device)
	if err != nil {
		return workspaceReadUnavailable()
	}
	device, err := store.Decode[domain.Device](row)
	if err != nil || device.Revoked || device.Type != domain.WorkerDevice || device.MachineID != reader.machine {
		return workspaceReadUnavailable()
	}
	return nil
}

func workspaceReadScope(tx *store.Tx, id domain.ID) (workspace.PrepareRequest, workspace.Manifest, error) {
	var input workspace.PrepareRequest
	var manifest workspace.Manifest
	if err := tx.Authorize(); err != nil {
		return input, manifest, err
	}
	_, session, err := sessionRecord(tx, id)
	if err != nil {
		return input, manifest, err
	}
	if session.Preparation == nil || session.Preparation.State != domain.PreparationReady {
		return input, manifest, workspaceReadUnavailable()
	}
	_, machine, err := activeMachine(tx, session.MachineID)
	if err != nil {
		return input, manifest, err
	}
	row, err := tx.Get(domain.JobKind, session.Preparation.JobID)
	if err != nil {
		return input, manifest, err
	}
	job, err := store.Decode[domain.Job](row)
	if err != nil || row.SessionID != id || job.Type != domain.PrepareWorkspaceJob || job.State != domain.JobSucceeded || job.MachineID != session.MachineID || domain.Decode(job.Input, &input) != nil || domain.Decode(job.Output, &manifest) != nil || input.SessionID != id || input.MachineID != session.MachineID || input.Type != session.Workspace || workspace.ValidateResult(input, manifest, machine.OS) != nil {
		return input, manifest, workspace.ResultUncertain()
	}
	return input, manifest, nil
}

func (s *Service) ReadSessionWorkspace(ctx context.Context, req *connect.Request[pb.ReadSessionWorkspaceRequest]) (*connect.Response[pb.ReadSessionWorkspaceResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.ReadSessionWorkspaceResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return fail(domain.Fail(domain.PermissionDenied, "Workspace files require an owner or paired client.", "Use an authorized product client."))
	}
	id := domain.ID(req.Msg.SessionId)
	var query domain.WorkspaceReadQuery
	if id.Validate() != nil || len(req.Msg.QueryJson) > 8192 || domain.Decode(req.Msg.QueryJson, &query) != nil || query.Validate() != nil {
		return fail(domain.Fail(domain.InvalidArgument, "Invalid workspace file query.", "Select a prepared session and a relative path."))
	}
	var input workspace.PrepareRequest
	var manifest workspace.Manifest
	err := s.Store.Read(ctx, func(tx *store.Tx) error { var err error; input, manifest, err = workspaceReadScope(tx, id); return err })
	if err != nil {
		return fail(err)
	}
	if query.Operation == domain.WorkspaceRoots {
		result := domain.WorkspaceReadResult{}
		if input.Type == domain.GeneralChat {
			result.Roots = []domain.WorkspaceRoot{{Name: "General Chat", Primary: true}}
		}
		err := s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := tx.Authorize(); err != nil {
				return err
			}
			for _, repo := range manifest.Repositories {
				name := "Repository " + string(repo.ID)
				if row, err := tx.Get(domain.RepositoryKind, repo.ID); err == nil {
					if value, err := store.Decode[domain.Repository](row); err == nil {
						name = value.Name
					}
				}
				result.Roots = append(result.Roots, domain.WorkspaceRoot{RepositoryID: repo.ID, Name: name, Primary: repo.ID == input.PrimaryRepository})
			}
			return nil
		})
		if err != nil {
			return fail(err)
		}
		raw, _ := json.Marshal(result)
		return connect.NewResponse(&pb.ReadSessionWorkspaceResponse{DocumentJson: raw}), nil
	}
	selected := input.Type == domain.GeneralChat && query.RepositoryID == ""
	for _, repo := range manifest.Repositories {
		selected = selected || repo.ID == query.RepositoryID
	}
	if !selected {
		return fail(domain.Fail(domain.InvalidArgument, "The repository is not part of this session workspace.", "Select one of this session's workspace roots."))
	}
	if query.Operation == domain.WorkspaceGitDiff && query.Comparison == domain.DiffCreation && input.Type != domain.Worktree {
		return fail(domain.Fail(domain.Unsupported, "Creation comparisons require a prepared Worktree.", "Choose working-tree or staged for Local repositories; no session-start filesystem baseline exists."))
	}
	raw, err := s.observeWorkerWorkspace(ctx, id, input, manifest, query, nil)
	if err != nil {
		return fail(err)
	}
	return connect.NewResponse(&pb.ReadSessionWorkspaceResponse{DocumentJson: raw}), nil
}

// Both profiles retain the same one-read-per-Worker bound and current primary
// stream, paired device and original preparation checks through publication.
func (s *Service) observeWorkerWorkspace(ctx context.Context, id domain.ID, input workspace.PrepareRequest, manifest workspace.Manifest, query domain.WorkspaceReadQuery, target *domain.PRGitTarget) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	deadline = deadline.UTC()
	observation := &workspaceObservation{request: workspace.ReadRequest{ID: domain.NewID(), Deadline: deadline, Preparation: input, Manifest: manifest, Query: query, PRCandidate: target}, result: make(chan workspaceReadReply, 1)}
	s.workspaceReadsMu.Lock()
	reader := s.workspaceReaders[input.MachineID]
	if reader == nil {
		s.workspaceReadsMu.Unlock()
		return nil, workspaceReadUnavailable()
	}
	if reader.pending != nil {
		s.workspaceReadsMu.Unlock()
		return nil, domain.Fail(domain.ResourceExhausted, "Another workspace read is in progress on this machine.", "Wait for it to finish and refresh.")
	}
	active := 0
	for _, current := range s.workspaceReaders {
		if current.pending != nil {
			active++
		}
	}
	if active >= 64 {
		s.workspaceReadsMu.Unlock()
		return nil, domain.Fail(domain.ResourceExhausted, "The workspace observation limit is reached.", "Wait for active reads to finish and refresh.")
	}
	reader.pending = observation
	s.workspaceReadsMu.Unlock()
	defer func() {
		s.workspaceReadsMu.Lock()
		defer s.workspaceReadsMu.Unlock()
		if reader.pending == observation {
			reader.pending = nil
		}
	}()
	primary, err := s.primaryWorkspaceStream(reader.machine, reader.instance)
	if err != nil || primary.ID != reader.primary {
		return nil, workspaceReadUnavailable()
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return currentWorkspaceReader(tx, reader) }); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(observation.request)
	select {
	case reader.requests <- raw:
	case <-reader.done:
		return nil, workspaceReadUnavailable()
	case <-primary.Done:
		return nil, workspaceReadUnavailable()
	case <-ctx.Done():
		return nil, workspaceReadUnavailable()
	}
	select {
	case <-ctx.Done():
		return nil, workspaceReadUnavailable()
	case <-reader.done:
		return nil, workspaceReadUnavailable()
	case <-primary.Done:
		return nil, workspaceReadUnavailable()
	case reply := <-observation.result:
		// A result cannot outlive session deletion, changed preparation, a
		// revoked client/Worker or replacement of the primary execution stream.
		current, err := s.primaryWorkspaceStream(reader.machine, reader.instance)
		if err != nil || current.ID != reader.primary {
			return nil, workspaceReadUnavailable()
		}
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := currentWorkspaceReader(tx, reader); err != nil {
				return err
			}
			freshInput, freshManifest, err := workspaceReadScope(tx, id)
			if err != nil {
				return err
			}
			before, _ := json.Marshal([]any{input, manifest})
			after, _ := json.Marshal([]any{freshInput, freshManifest})
			if !bytes.Equal(before, after) {
				return workspace.ResultUncertain()
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if reply.problem != nil {
			return nil, reply.problem
		}
		if s.logger != nil {
			s.logger.InfoContext(ctx, "workspace_read_completed", "read_id", observation.request.ID, "session_id", id, "machine_id", reader.machine, "operation", query.Operation, "pr_candidate", target != nil)
		}
		return reply.document, nil
	}
}

func (s *Service) WatchWorkspaceReads(ctx context.Context, req *connect.Request[pb.WatchWorkspaceReadsRequest], stream *connect.ServerStream[pb.WatchWorkspaceReadsResponse]) error {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return rpc.Error(err, correlation)
	}
	machine, instance := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)
	primary, err := s.primaryWorkspaceStream(machine, instance)
	if err != nil {
		return rpc.Error(err, correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	reader := &workspaceReader{machine: machine, instance: instance, device: actor.DeviceID, primary: primary.ID, done: ctx.Done(), requests: make(chan []byte, 1)}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return currentWorkspaceReader(tx, reader) }); err != nil {
		return rpc.Error(err, correlation)
	}
	s.workspaceReadsMu.Lock()
	if s.workspaceReaders == nil {
		s.workspaceReaders = map[domain.ID]*workspaceReader{}
	}
	if s.workspaceReaders[machine] != nil {
		s.workspaceReadsMu.Unlock()
		return rpc.Error(domain.Fail(domain.Conflict, "A workspace reader is already connected.", "Wait for the previous reader to disconnect."), correlation)
	}
	if len(s.workspaceReaders) >= 1024 {
		s.workspaceReadsMu.Unlock()
		return rpc.Error(domain.Fail(domain.ResourceExhausted, "The connected workspace reader limit is reached.", "Disconnect unused Workers before retrying."), correlation)
	}
	s.workspaceReaders[machine] = reader
	s.workspaceReadsMu.Unlock()
	defer func() {
		s.workspaceReadsMu.Lock()
		defer s.workspaceReadsMu.Unlock()
		if s.workspaceReaders[machine] == reader {
			delete(s.workspaceReaders, machine)
		}
	}()
	controller, ok := ctx.Value(writeControllerKey{}).(*http.ResponseController)
	if !ok {
		return rpc.Error(workspaceReadUnavailable(), correlation)
	}
	stream.ResponseHeader().Set(rpc.CorrelationHeader, correlation)
	send := func(message *pb.WatchWorkspaceReadsResponse) error {
		if err := controller.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
			return err
		}
		return stream.Send(message)
	}
	if err := send(&pb.WatchWorkspaceReadsResponse{Heartbeat: true}); err != nil {
		return err
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-primary.Done:
			return nil
		case raw := <-reader.requests:
			if err := s.Store.Read(ctx, func(tx *store.Tx) error { return currentWorkspaceReader(tx, reader) }); err != nil {
				return rpc.Error(err, correlation)
			}
			if err := send(&pb.WatchWorkspaceReadsResponse{RequestJson: raw}); err != nil {
				return err
			}
		case <-ticker.C:
			if err := s.Store.Read(ctx, func(tx *store.Tx) error { return currentWorkspaceReader(tx, reader) }); err != nil {
				return rpc.Error(err, correlation)
			}
			if err := send(&pb.WatchWorkspaceReadsResponse{Heartbeat: true}); err != nil {
				return err
			}
		}
	}
}

func (s *Service) ReportWorkspaceRead(ctx context.Context, req *connect.Request[pb.ReportWorkspaceReadRequest]) (*connect.Response[pb.ReportWorkspaceReadResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.ReportWorkspaceReadResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return fail(err)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	s.workspaceReadsMu.Lock()
	reader := s.workspaceReaders[domain.ID(req.Msg.MachineId)]
	var pending *workspaceObservation
	if reader != nil {
		pending = reader.pending
	}
	s.workspaceReadsMu.Unlock()
	if reader == nil || reader.instance != domain.ID(req.Msg.InstanceId) || reader.device != actor.DeviceID || pending == nil || pending.request.ID != domain.ID(req.Msg.ReadId) || time.Now().After(pending.request.Deadline) {
		return fail(workspaceReadUnavailable())
	}
	primary, err := s.primaryWorkspaceStream(reader.machine, reader.instance)
	if err != nil || primary.ID != reader.primary {
		return fail(workspaceReadUnavailable())
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return currentWorkspaceReader(tx, reader) }); err != nil {
		return fail(err)
	}
	var result domain.WorkspaceReadResult
	reply := workspaceReadReply{}
	if req.Msg.ProblemCode != "" {
		if len(req.Msg.DocumentJson) != 0 {
			return fail(workspaceReadUnavailable())
		}
		code := domain.Code(req.Msg.ProblemCode)
		switch code {
		case domain.InvalidArgument, domain.NotFound, domain.Conflict, domain.PermissionDenied, domain.Unavailable, domain.Unsupported, domain.ResourceExhausted, domain.Canceled, domain.RecoveryRequired, domain.Internal:
		default:
			return fail(workspaceReadUnavailable())
		}
		reply.problem = domain.Fail(code, "The execution machine could not provide this workspace view.", "Refresh the view; check Worker access and workspace recovery if the problem persists.")
		if pending.request.Query.Operation == domain.WorkspaceGitDiff && code == domain.Unsupported {
			reply.problem = domain.Fail(code, "This Git comparison is unavailable on the execution machine.", "Select a prepared Git repository and supported comparison. Working-tree comparisons cannot run configured clean/process filters; use the staged comparison to inspect stored changes.")
		}
	} else if pending.request.PRCandidate != nil {
		var match workspace.PRWorkspaceMatch
		if len(req.Msg.DocumentJson) > 4096 || domain.Decode(req.Msg.DocumentJson, &match) != nil || workspace.ValidatePRWorkspaceMatch(pending.request, match) != nil {
			return fail(workspaceReadUnavailable())
		}
		reply.document, _ = json.Marshal(match)
	} else {
		if len(req.Msg.DocumentJson) > 512<<10 || domain.Decode(req.Msg.DocumentJson, &result) != nil || result.Validate(pending.request.Query) != nil {
			return fail(workspaceReadUnavailable())
		}
		reply.document, _ = json.Marshal(result)
	}
	s.workspaceReadsMu.Lock()
	defer s.workspaceReadsMu.Unlock()
	if s.workspaceReaders[reader.machine] != reader || reader.pending != pending || pending.delivered {
		return fail(workspaceReadUnavailable())
	}
	select {
	case pending.result <- reply:
		pending.delivered = true
		return connect.NewResponse(&pb.ReportWorkspaceReadResponse{}), nil
	default:
		return fail(workspaceReadUnavailable())
	}
}
