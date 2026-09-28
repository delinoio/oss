package server

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type sessionPRReceipt struct {
	SessionID     domain.ID `json:"session_id"`
	AssociationID domain.ID `json:"association_id"`
	Deleted       bool      `json:"deleted,omitempty"`
}

// Association metadata may be managed after Archive without resuming execution.
// Current project membership is required only for a new link; retained links and
// unlinking remain available after repository/profile removal or reconfiguration.
func sessionPRScope(tx *store.Tx, session, repository domain.ID) (store.Record, error) {
	if err := tx.Authorize(); err != nil {
		return store.Record{}, err
	}
	r, state, err := sessionRecord(tx, session)
	if err != nil {
		return store.Record{}, err
	}
	if state.ProjectID == "" || r.ProjectID != state.ProjectID {
		return store.Record{}, domain.Fail(domain.InvalidArgument, "A PR association requires a project session.", "Select a session and a repository from its project.")
	}
	project, err := tx.Get(domain.ProjectKind, state.ProjectID)
	if err != nil {
		return store.Record{}, err
	}
	value, err := store.Decode[domain.Project](project)
	if err != nil {
		return store.Record{}, err
	}
	if !slices.Contains(value.Repositories, repository) {
		return store.Record{}, domain.Fail(domain.PermissionDenied, "The repository is outside this session's project.", "Select one of the current project's repositories.")
	}
	return project, nil
}

func sessionPRRecord(tx *store.Tx, session, id domain.ID) (store.Record, domain.SessionPullRequest, error) {
	r, err := tx.Get(domain.PullRequestKind, id)
	if err != nil {
		return r, domain.SessionPullRequest{}, err
	}
	v, err := store.Decode[domain.SessionPullRequest](r)
	if err != nil {
		return r, v, err
	}
	if r.SessionID != session {
		return r, v, domain.Fail(domain.Conflict, "The PR association belongs to another session.", "Select its original session.")
	}
	return r, v, v.Validate()
}

func (s *Service) LinkSessionPullRequest(ctx context.Context, req *connect.Request[pb.LinkSessionPullRequestRequest]) (*connect.Response[pb.LinkSessionPullRequestResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.LinkSessionPullRequestResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	actor, err := integrationActor(ctx)
	if err != nil {
		return fail(err)
	}
	requestID, session, repository := domain.ID(req.Msg.RequestId), domain.ID(req.Msg.SessionId), domain.ID(req.Msg.RepositoryId)
	if requestID.Validate() != nil || session.Validate() != nil || repository.Validate() != nil || !domain.PositiveDecimal(req.Msg.Number) {
		return fail(domain.Fail(domain.InvalidArgument, "Invalid PR association request.", "Provide original session/repository UUIDs and a canonical positive PR number."))
	}
	identity := struct {
		Session, Repository domain.ID
		Number              string
		Actor               domain.Principal
	}{session, repository, req.Msg.Number, actor}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	result, replayed, err := s.Store.Replay(ctx, requestID, "session.pr.link", identity)
	if err != nil {
		return fail(err)
	}
	if !replayed {
		var scope store.Record
		err = s.Store.Read(ctx, func(tx *store.Tx) error { var e error; scope, e = sessionPRScope(tx, session, repository); return e })
		if err != nil {
			return fail(err)
		}
		query := domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: req.Msg.Number}
		raw, _ := json.Marshal(query)
		read := connect.NewRequest(&pb.QueryRepositoryIntegrationRequest{RepositoryId: string(repository), SchemaVersion: 1, QueryJson: raw})
		read.Header().Set(rpc.CorrelationHeader, correlation)
		response, err := s.QueryRepositoryIntegration(ctx, read)
		if err != nil {
			return fail(rpc.ClientError(err))
		}
		var observed domain.RepositoryQueryResult
		if response.Msg.SchemaVersion != 1 || domain.Decode(response.Msg.DocumentJson, &observed) != nil || observed.Validate() != nil || observed.RepositoryID != repository || observed.Query != query {
			return fail(domain.Fail(domain.RecoveryRequired, "The PR observation is inconsistent.", "Repeat the selected repository and PR query explicitly."))
		}
		pr := observed.Items[0]
		value := domain.SessionPullRequest{Version: 1, Provider: observed.Repository.Provider, RepositoryID: repository, RemoteRepositoryID: observed.Repository.ID, RepositoryNodeID: observed.Repository.NodeID, Owner: observed.Repository.Owner, Name: observed.Repository.Name, PullRequestID: pr.ID, PullRequestNodeID: pr.NodeID, Number: pr.Number, Title: pr.Title, ObservedAt: observed.ObservedAt}
		if err := value.Validate(); err != nil {
			return fail(err)
		}
		result, err = s.Store.Mutate(ctx, requestID, "session.pr.link", identity, func(tx *store.Tx) (any, error) {
			current, err := sessionPRScope(tx, session, repository)
			if err != nil {
				return nil, err
			}
			selected, err := repositoryIntegrationFromTx(tx, repository)
			if err != nil {
				return nil, err
			}
			if current.ID != scope.ID || current.Revision != scope.Revision || strconv.FormatUint(selected.record.Revision, 10) != observed.RepositoryRevision || selected.repository.IntegrationID != observed.ProfileID || selected.profile.Connection.GenerationID != observed.GenerationID {
				return nil, domain.Fail(domain.Conflict, "The project, repository or selected GitHub generation changed.", "Refresh the selection before linking this PR.")
			}
			links, err := tx.List(store.Filter{Kind: domain.PullRequestKind, SessionID: session, Limit: domain.MaxSessionPullRequests + 1})
			if err != nil {
				return nil, err
			}
			for _, link := range links {
				prior, err := store.Decode[domain.SessionPullRequest](link)
				if err != nil {
					return nil, err
				}
				if err := prior.Validate(); err != nil {
					return nil, err
				}
				if prior.SamePR(value) || (prior.Provider == value.Provider && prior.PullRequestNodeID == value.PullRequestNodeID) {
					return nil, domain.Fail(domain.Conflict, "This stable PR is already linked to the session.", "Inspect the existing association; no duplicate was created.")
				}
			}
			if len(links) >= domain.MaxSessionPullRequests {
				return nil, domain.Fail(domain.ResourceExhausted, "The session has reached its 100 PR association limit.", "Unlink an unneeded association before adding another.")
			}
			if _, err := tx.Put(domain.PullRequestKind, requestID, 0, session, scope.ID, value); err != nil {
				return nil, err
			}
			return sessionPRReceipt{SessionID: session, AssociationID: requestID}, nil
		})
		if err != nil {
			return fail(err)
		}
	}
	var refs sessionPRReceipt
	if domain.Decode(result.Data, &refs) != nil || refs.Deleted || refs.SessionID != session || refs.AssociationID != requestID {
		return fail(domain.Fail(domain.NotFound, "The original PR association is no longer retained.", "An old request cannot recreate an unlinked association."))
	}
	var association store.Record
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		if _, _, err := sessionRecord(tx, session); err != nil {
			return err
		}
		var e error
		association, _, e = sessionPRRecord(tx, session, refs.AssociationID)
		return e
	})
	if err != nil {
		return fail(err)
	}
	s.logger.InfoContext(ctx, "session_pull_request_linked", "session_id", session, "association_id", association.ID, "replayed", result.Replayed, "correlation_id", correlation)
	response := connect.NewResponse(&pb.LinkSessionPullRequestResponse{Association: rpc.Resource(association), RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) UnlinkSessionPullRequest(ctx context.Context, req *connect.Request[pb.UnlinkSessionPullRequestRequest]) (*connect.Response[pb.UnlinkSessionPullRequestResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := integrationActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	session := domain.ID(req.Msg.SessionId)
	if session.Validate() != nil || validateSessionMutation(req.Msg.Mutation) != nil {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Invalid unlink request.", "Provide the original session, association ID and expected revision."), correlation)
	}
	meta := req.Msg.Mutation
	identity := struct {
		Session, Association domain.ID
		Revision             uint64
		Actor                domain.Principal
	}{session, domain.ID(meta.Id), meta.ExpectedRevision, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "session.pr.unlink", identity, func(tx *store.Tx) (any, error) {
		if _, _, err := sessionRecord(tx, session); err != nil {
			return nil, err
		}
		r, _, err := sessionPRRecord(tx, session, identity.Association)
		if err != nil {
			return nil, err
		}
		if err := tx.Delete(domain.PullRequestKind, r.ID, identity.Revision); err != nil {
			return nil, err
		}
		return sessionPRReceipt{SessionID: session, AssociationID: r.ID, Deleted: true}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var refs sessionPRReceipt
	if domain.Decode(result.Data, &refs) != nil || !refs.Deleted || refs.SessionID != session || refs.AssociationID != identity.Association {
		return nil, rpc.Error(domain.Fail(domain.NotFound, "The original unlink result is no longer retained.", "Inspect current session associations; no link was recreated."), correlation)
	}
	s.logger.InfoContext(ctx, "session_pull_request_unlinked", "session_id", session, "association_id", refs.AssociationID, "replayed", result.Replayed, "correlation_id", correlation)
	response := connect.NewResponse(&pb.UnlinkSessionPullRequestResponse{Id: string(refs.AssociationID), RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
