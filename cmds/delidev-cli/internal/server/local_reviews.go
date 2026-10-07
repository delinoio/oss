package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type localReviewReceipt struct {
	SessionID domain.ID `json:"session_id"`
	ReviewID  domain.ID `json:"review_id"`
	InputID   domain.ID `json:"input_id,omitempty"`
	Deleted   bool      `json:"deleted,omitempty"`
}

func localReviewActor(ctx context.Context) (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok {
		return actor, domain.Fail(domain.PermissionDenied, "Local reviews require an owner or paired client.", "Use an authorized product client.")
	}
	return actor, nil
}

func localReviewConflict() error {
	return domain.Fail(domain.Conflict, "The review or its original workspace changed.", "Refresh the selected comment revisions and Git comparison before trying again.")
}

func localReviewRecord(tx *store.Tx, session, id domain.ID, kind domain.LocalReviewType) (store.Record, domain.LocalReview, error) {
	r, err := tx.Get(domain.ReviewKind, id)
	if err != nil {
		return r, domain.LocalReview{}, err
	}
	v, err := store.Decode[domain.LocalReview](r)
	if err != nil {
		return r, v, err
	}
	if r.SessionID != session || v.Type != kind {
		return r, v, localReviewConflict()
	}
	return r, v, v.Validate()
}

// The private digest pins original preparation, including administrative path
// ownership, across the outside-transaction Worker read and database commit.
func localReviewScope(tx *store.Tx, session domain.ID) ([32]byte, error) {
	_, state, err := sessionRecord(tx, session)
	if err != nil {
		return [32]byte{}, err
	}
	input, manifest, err := workspaceReadScope(tx, session)
	if err != nil {
		return [32]byte{}, err
	}
	raw, err := json.Marshal(struct {
		Job      domain.ID
		Input    any
		Manifest any
	}{state.Preparation.JobID, input, manifest})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}

func (s *Service) localReviewDiff(ctx context.Context, session domain.ID, query domain.WorkspaceReadQuery, correlation string) (domain.WorkspaceDiff, error) {
	raw, _ := json.Marshal(query)
	request := connect.NewRequest(&pb.ReadSessionWorkspaceRequest{SessionId: string(session), QueryJson: raw})
	request.Header().Set(rpc.CorrelationHeader, correlation)
	r, err := s.ReadSessionWorkspace(ctx, request)
	if err != nil {
		return domain.WorkspaceDiff{}, rpc.ClientError(err)
	}
	var value domain.WorkspaceReadResult
	if err := domain.Decode(r.Msg.DocumentJson, &value); err != nil {
		return domain.WorkspaceDiff{}, err
	}
	if err := value.Validate(query); err != nil {
		return domain.WorkspaceDiff{}, err
	}
	return *value.Diff, nil
}

func (s *Service) localReviewResult(ctx context.Context, result store.Result, session domain.ID, kind domain.LocalReviewType) (*pb.Resource, localReviewReceipt, error) {
	var refs localReviewReceipt
	if kind == domain.LocalReviewSubmissionType {
		// Submission identity is its original request UUID. Store deletion can
		// redact receipts linked to an deleted selected comment; retain
		// read-only recovery of the immutable submission and accepted input.
		// Replay already checked the original actor and exact request digest.
		refs = localReviewReceipt{SessionID: session, ReviewID: result.RequestID}
	} else if domain.Decode(result.Data, &refs) != nil || refs.SessionID != session || refs.ReviewID.Validate() != nil || refs.Deleted {
		return nil, refs, domain.Fail(domain.NotFound, "The original local review is no longer retained.", "Refresh the session; an old receipt cannot recreate deleted comments.")
	}
	var resource *pb.Resource
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		if _, _, err := sessionRecord(tx, session); err != nil {
			return err
		}
		r, value, err := localReviewRecord(tx, session, refs.ReviewID, kind)
		if err != nil {
			return err
		}
		resource = rpc.Resource(r)
		if kind == domain.LocalReviewSubmissionType {
			refs.InputID = value.Submission.InputID
		}
		return nil
	})
	return resource, refs, err
}

func (s *Service) CreateLocalReviewComment(ctx context.Context, req *connect.Request[pb.CreateLocalReviewCommentRequest]) (*connect.Response[pb.CreateLocalReviewCommentResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.CreateLocalReviewCommentResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	actor, err := localReviewActor(ctx)
	if err != nil {
		return fail(err)
	}
	session, requestID := domain.ID(req.Msg.SessionId), domain.ID(req.Msg.RequestId)
	var input domain.CreateReviewComment
	if session.Validate() != nil || len(req.Msg.DocumentJson) > 64<<10 || domain.Decode(req.Msg.DocumentJson, &input) != nil || input.Validate() != nil {
		return fail(domain.Fail(domain.InvalidArgument, "Invalid local review comment.", "Provide a bounded body, original diff revision and file or line selection."))
	}
	identity := struct {
		Session domain.ID
		Input   domain.CreateReviewComment
		Actor   domain.Principal
	}{session, input, actor}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, replayed, err := s.Store.Replay(ctx, requestID, "review.create", identity)
	if err != nil {
		return fail(err)
	}
	if !replayed {
		var scope [32]byte
		err := s.Store.Read(ctx, func(tx *store.Tx) error { var err error; scope, err = localReviewScope(tx, session); return err })
		if err != nil {
			return fail(err)
		}
		diff, err := s.localReviewDiff(ctx, session, input.Query, correlation)
		if err != nil {
			return fail(err)
		}
		if diff.Revision != input.DiffRevision {
			return fail(localReviewConflict())
		}
		anchor, err := diff.ReviewAnchor(input.Selection)
		if err != nil {
			return fail(err)
		}
		result, err = s.Store.Mutate(ctx, requestID, "review.create", identity, func(tx *store.Tx) (any, error) {
			current, err := localReviewScope(tx, session)
			if err != nil {
				return nil, err
			}
			if current != scope {
				return nil, localReviewConflict()
			}
			if err := tx.CheckReviewCapacity(session); err != nil {
				return nil, err
			}
			sr, _, err := sessionRecord(tx, session)
			if err != nil {
				return nil, err
			}
			id := domain.NewID()
			value := domain.LocalReview{Version: 1, Type: domain.LocalReviewCommentType, Comment: &domain.ReviewComment{Anchor: anchor, Body: input.Body, ContentRevision: 1}}
			if err := value.Validate(); err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.ReviewKind, id, 0, session, sr.ProjectID, value); err != nil {
				return nil, err
			}
			return localReviewReceipt{SessionID: session, ReviewID: id}, nil
		})
		if err != nil {
			return fail(err)
		}
	}
	comment, _, err := s.localReviewResult(ctx, result, session, domain.LocalReviewCommentType)
	if err != nil {
		return fail(err)
	}
	s.logger.InfoContext(ctx, "local_review_comment_created", "correlation_id", correlation, "session_id", session, "review_id", comment.Id, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.CreateLocalReviewCommentResponse{Comment: comment, RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) changeLocalReviewComment(ctx context.Context, session domain.ID, meta *pb.Mutation, body string, remove bool) (store.Result, error) {
	actor, err := localReviewActor(ctx)
	if err != nil {
		return store.Result{}, err
	}
	if session.Validate() != nil || validateSessionMutation(meta) != nil || (!remove && domain.Text(body, "review comment", domain.MaxReviewBodyBytes, true) != nil) {
		return store.Result{}, domain.Fail(domain.InvalidArgument, "Invalid review edit.", "Provide the original comment ID/revision and a bounded replacement body.")
	}
	identity := struct {
		Session, ID domain.ID
		Revision    uint64
		Body        string
		Remove      bool
		Actor       domain.Principal
	}{session, domain.ID(meta.Id), meta.ExpectedRevision, body, remove, actor}
	return s.Store.Mutate(ctx, domain.ID(meta.RequestId), "review.change", identity, func(tx *store.Tx) (any, error) {
		if _, _, err := sessionRecord(tx, session); err != nil {
			return nil, err
		}
		r, v, err := localReviewRecord(tx, session, identity.ID, domain.LocalReviewCommentType)
		if err != nil {
			return nil, err
		}
		if r.Revision != identity.Revision {
			return nil, localReviewConflict()
		}
		if remove {
			if err := tx.Delete(domain.ReviewKind, r.ID, r.Revision); err != nil {
				return nil, err
			}
		} else {
			v.Comment.Body = body
			v.Comment.ContentRevision++
			if err := v.Validate(); err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.ReviewKind, r.ID, r.Revision, session, r.ProjectID, v); err != nil {
				return nil, err
			}
		}
		return localReviewReceipt{SessionID: session, ReviewID: r.ID, Deleted: remove}, nil
	})
}

func (s *Service) EditLocalReviewComment(ctx context.Context, req *connect.Request[pb.EditLocalReviewCommentRequest]) (*connect.Response[pb.EditLocalReviewCommentResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	result, err := s.changeLocalReviewComment(ctx, domain.ID(req.Msg.SessionId), req.Msg.Mutation, req.Msg.Body, false)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	comment, _, err := s.localReviewResult(ctx, result, domain.ID(req.Msg.SessionId), domain.LocalReviewCommentType)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "local_review_comment_edited", "correlation_id", correlation, "review_id", comment.Id, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.EditLocalReviewCommentResponse{Comment: comment, RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) DeleteLocalReviewComment(ctx context.Context, req *connect.Request[pb.DeleteLocalReviewCommentRequest]) (*connect.Response[pb.DeleteLocalReviewCommentResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	result, err := s.changeLocalReviewComment(ctx, domain.ID(req.Msg.SessionId), req.Msg.Mutation, "", true)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var refs localReviewReceipt
	if domain.Decode(result.Data, &refs) != nil || !refs.Deleted || refs.SessionID != domain.ID(req.Msg.SessionId) || refs.ReviewID != domain.ID(req.Msg.Mutation.Id) {
		return nil, rpc.Error(localReviewConflict(), correlation)
	}
	s.logger.InfoContext(ctx, "local_review_comment_deleted", "correlation_id", correlation, "review_id", refs.ReviewID, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.DeleteLocalReviewCommentResponse{Id: string(refs.ReviewID), RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
