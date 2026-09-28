package server

import (
	"context"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type selectedLocalReview struct {
	Record store.Record
	Value  domain.LocalReview
}

func (s *Service) SubmitLocalReview(ctx context.Context, req *connect.Request[pb.SubmitLocalReviewRequest]) (*connect.Response[pb.SubmitLocalReviewResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.SubmitLocalReviewResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	actor, err := localReviewActor(ctx)
	if err != nil {
		return fail(err)
	}
	session, requestID := domain.ID(req.Msg.SessionId), domain.ID(req.Msg.RequestId)
	var input domain.SubmitReviewComments
	if session.Validate() != nil || len(req.Msg.DocumentJson) > 16<<10 || domain.Decode(req.Msg.DocumentJson, &input) != nil || input.Validate() != nil {
		return fail(domain.Fail(domain.InvalidArgument, "Invalid local review submission.", "Select 1 through 25 distinct current comment revisions and an explicit input mode."))
	}
	identity := struct {
		Session domain.ID
		Input   domain.SubmitReviewComments
		Actor   domain.Principal
	}{session, input, actor}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	result, replayed, err := s.Store.Replay(ctx, requestID, "review.submit", identity)
	if err != nil {
		return fail(err)
	}
	if !replayed {
		var scope [32]byte
		selected := make([]selectedLocalReview, 0, len(input.Comments))
		err := s.Store.Read(ctx, func(tx *store.Tx) error {
			var err error
			scope, err = localReviewScope(tx, session)
			if err != nil {
				return err
			}
			for _, ref := range input.Comments {
				r, value, err := localReviewRecord(tx, session, ref.ID, domain.LocalReviewCommentType)
				if err != nil {
					return err
				}
				if r.Revision != ref.Revision {
					return localReviewConflict()
				}
				selected = append(selected, selectedLocalReview{r, value})
			}
			return nil
		})
		if err != nil {
			return fail(err)
		}
		observations := map[domain.WorkspaceReadQuery]domain.WorkspaceDiff{}
		submission := domain.ReviewSubmission{Mode: input.Mode, Comments: []domain.SubmittedReviewComment{}}
		for _, item := range selected {
			comment := item.Value.Comment
			anchor := comment.Anchor
			query := domain.WorkspaceReadQuery{Operation: domain.WorkspaceGitDiff, RepositoryID: anchor.RepositoryID, Comparison: anchor.Comparison, Path: anchor.QueryPath}
			diff, ok := observations[query]
			if !ok {
				if len(observations) >= 8 {
					return fail(domain.Fail(domain.ResourceExhausted, "A review submission spans too many comparisons.", "Select comments from at most eight repository/path/comparison groups."))
				}
				diff, err = s.localReviewDiff(ctx, session, query, correlation)
				if err != nil {
					return fail(err)
				}
				observations[query] = diff
			}
			freshness := domain.ReviewCurrent
			if !anchor.Matches(diff) {
				freshness = domain.ReviewStale
				if !input.AllowStale {
					return fail(domain.Fail(domain.Conflict, "Selected comments refer to a changed diff.", "Refresh the review; explicitly allow stale comments only after inspecting their original locations and context."))
				}
			}
			submission.Comments = append(submission.Comments, domain.SubmittedReviewComment{ID: item.Record.ID, ContentRevision: comment.ContentRevision, Anchor: anchor, Body: comment.Body, Freshness: freshness})
		}
		queued, err := submission.SessionInput()
		if err != nil {
			return fail(err)
		}
		result, err = s.Store.Mutate(ctx, requestID, "review.submit", identity, func(tx *store.Tx) (any, error) {
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
			sr, state, err := sessionRecord(tx, session)
			if err != nil {
				return nil, err
			}
			if state.Archive != domain.NotArchived {
				return nil, domain.Fail(domain.Conflict, "Archived or archiving sessions cannot accept review input.", "Restore the session first; restoration keeps execution paused.")
			}
			id := requestID
			for _, original := range selected {
				r, value, err := localReviewRecord(tx, session, original.Record.ID, domain.LocalReviewCommentType)
				if err != nil {
					return nil, err
				}
				if r.Revision != original.Record.Revision || *value.Comment != *original.Value.Comment {
					return nil, localReviewConflict()
				}
				value.Comment.LastSubmissionID, value.Comment.LastSubmittedContentRevision = id, value.Comment.ContentRevision
				if err := value.Validate(); err != nil {
					return nil, err
				}
				if _, err := tx.Put(domain.ReviewKind, r.ID, r.Revision, session, r.ProjectID, value); err != nil {
					return nil, err
				}
			}
			inputID, err := appendSessionInput(tx, session, &state, queued)
			if err != nil {
				return nil, err
			}
			submission.InputID = inputID
			value := domain.LocalReview{Version: 1, Type: domain.LocalReviewSubmissionType, Submission: &submission}
			if err := value.Validate(); err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.ReviewKind, id, 0, session, sr.ProjectID, value); err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.SessionKind, session, sr.Revision, session, sr.ProjectID, state); err != nil {
				return nil, err
			}
			return localReviewReceipt{SessionID: session, ReviewID: id, InputID: inputID}, nil
		})
		if err != nil {
			return fail(err)
		}
	}
	resource, refs, err := s.localReviewResult(ctx, result, session, domain.LocalReviewSubmissionType)
	if err != nil {
		return fail(err)
	}
	raw, _ := json.Marshal(sessionReceipt{SessionID: refs.SessionID, InputID: refs.InputID})
	change, err := s.sessionResult(ctx, store.Result{RequestID: result.RequestID, Data: raw, Replayed: result.Replayed})
	if err != nil {
		return fail(err)
	}
	s.logger.InfoContext(ctx, "local_review_submitted", "correlation_id", correlation, "session_id", session, "review_id", refs.ReviewID, "input_id", refs.InputID, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.SubmitLocalReviewResponse{Submission: resource, Change: change, RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
