package cli

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func sessionReview(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	if len(args) == 0 {
		return nil, domain.Fail(domain.MissingInput, "Select a local review operation.", "Use create, edit, delete, submit, list or get.")
	}
	action := args[0]
	f := flags("session review " + action)
	session := f.String("id", "", "session UUID")
	comment := f.String("review-id", "", "comment or submission UUID")
	revision := f.Uint64("revision", 0, "expected comment entity revision")
	input := f.String("input", "-", "closed JSON document, or - for stdin")
	page := f.String("page-token", "", "next page from review list")
	limit := f.Uint("limit", 50, "review list page size")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if err := domain.ID(*session).Validate(); err != nil {
		return nil, err
	}
	if action == "list" {
		if *limit == 0 || *limit > 200 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid review page size.", "Use 1 through 200.")
		}
		r, err := c.resources.ListResources(ctx, request(c, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_REVIEW, SessionId: *session, PageSize: uint32(*limit), PageToken: *page}}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"reviews": resourcesJSON(r.Msg.Resources), "next_page_token": r.Msg.NextPageToken}, nil
	}
	if action == "get" || action == "edit" || action == "delete" {
		if err := domain.ID(*comment).Validate(); err != nil {
			return nil, err
		}
	}
	if action == "get" {
		r, err := c.resources.GetResource(ctx, request(c, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_REVIEW, Id: *comment}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		if r.Msg.Resource == nil || r.Msg.Resource.SessionId != *session {
			return nil, domain.Fail(domain.Conflict, "The review belongs to another session.", "Select its original session.")
		}
		return resourceJSON(r.Msg.Resource), nil
	}
	ensureRequest(&o)
	meta := &pb.Mutation{RequestId: string(o.requestID), Id: *comment, ExpectedRevision: *revision}
	if action == "delete" {
		r, err := c.sessions.DeleteLocalReviewComment(ctx, request(c, &pb.DeleteLocalReviewCommentRequest{Mutation: meta, SessionId: *session}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"id": r.Msg.Id, "deleted": true, "replayed": r.Msg.Replayed}, nil
	}
	if action != "create" && action != "edit" && action != "submit" {
		return nil, domain.Fail(domain.InvalidArgument, "Unknown local review operation.", "Use create, edit, delete, submit, list or get.")
	}
	if o.tokenStdin && *input == "-" {
		return nil, domain.Fail(domain.InvalidArgument, "Credential and document stdin cannot share one stream.", "Pass the review document with --input PATH.")
	}
	raw, err := readDocument(*input, streams.In)
	if err != nil {
		return nil, err
	}
	switch action {
	case "create":
		r, err := c.sessions.CreateLocalReviewComment(ctx, request(c, &pb.CreateLocalReviewCommentRequest{RequestId: string(o.requestID), SessionId: *session, DocumentJson: raw}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"comment": resourceJSON(r.Msg.Comment), "replayed": r.Msg.Replayed}, nil
	case "edit":
		var body struct {
			Body string `json:"body"`
		}
		if err := domain.Decode(raw, &body); err != nil {
			return nil, err
		}
		r, err := c.sessions.EditLocalReviewComment(ctx, request(c, &pb.EditLocalReviewCommentRequest{Mutation: meta, SessionId: *session, Body: body.Body}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"comment": resourceJSON(r.Msg.Comment), "replayed": r.Msg.Replayed}, nil
	default:
		r, err := c.sessions.SubmitLocalReview(ctx, request(c, &pb.SubmitLocalReviewRequest{RequestId: string(o.requestID), SessionId: *session, DocumentJson: raw}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		if r.Msg.Submission == nil || r.Msg.Change == nil || r.Msg.Change.Input == nil {
			return nil, domain.Fail(domain.RecoveryRequired, "The acknowledged review result is unavailable.", "Inspect the session and retry only the original request ID; do not create another submission to recover the response.")
		}
		return map[string]any{"submission": resourceJSON(r.Msg.Submission), "change": sessionChangeJSON(r.Msg.Change), "replayed": r.Msg.Replayed}, nil
	}
}
