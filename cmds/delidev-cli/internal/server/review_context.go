package server

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (s *Service) ReadSessionReviewContext(ctx context.Context, req *connect.Request[pb.ReadSessionReviewContextRequest]) (*connect.Response[pb.ReadSessionReviewContextResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	var query domain.WorkspaceReadQuery
	if len(req.Msg.QueryJson) > 8192 || domain.Decode(req.Msg.QueryJson, &query) != nil || query.Operation != domain.WorkspaceGitDiff || query.Validate() != nil {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Invalid review comparison.", "Select a prepared repository and an ordinary Git diff query."), correlation)
	}
	read := connect.NewRequest(&pb.ReadSessionWorkspaceRequest{SessionId: req.Msg.SessionId, QueryJson: req.Msg.QueryJson})
	read.Header().Set(rpc.CorrelationHeader, correlation)
	response, err := s.ReadSessionWorkspace(ctx, read)
	if err != nil {
		return nil, err
	}
	var observed domain.WorkspaceReadResult
	if err := domain.Decode(response.Msg.DocumentJson, &observed); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := observed.Validate(query); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	files, err := observed.Diff.ReviewFiles()
	if err != nil {
		s.logger.WarnContext(ctx, "session_review_context_unavailable", "correlation_id", correlation, "session_id", req.Msg.SessionId, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, correlation)
	}
	document, err := json.Marshal(domain.ReviewContext{Diff: *observed.Diff, Files: files})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if len(document) > domain.MaxReviewContextBytes {
		return nil, rpc.Error(domain.Fail(domain.ResourceExhausted, "The structured review exceeds its 1 MiB limit.", "Select a narrower relative path; no partial review was returned."), correlation)
	}
	result := connect.NewResponse(&pb.ReadSessionReviewContextResponse{DocumentJson: document})
	rpc.CopyCorrelation(result, req.Header())
	return result, nil
}
