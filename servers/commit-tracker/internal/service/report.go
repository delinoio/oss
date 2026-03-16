package service

import (
	"context"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"

	committrackerv1 "github.com/delinoio/oss/servers/commit-tracker/gen/proto/committracker/v1"
	"github.com/delinoio/oss/servers/commit-tracker/internal/contracts"
)

// PublishPullRequestReport is a stub that returns UNIMPLEMENTED.
// GitHub API integration is deferred to a future implementation.
func (s *Service) PublishPullRequestReport(
	ctx context.Context,
	req *connect.Request[committrackerv1.PublishPullRequestReportRequest],
) (*connect.Response[committrackerv1.PublishPullRequestReportResponse], error) {
	s.logger.Info("publish pull request report called (unimplemented)",
		slog.String("event", contracts.EventPublishReport),
		slog.String("repository", req.Msg.GetRepository()),
		slog.Int64("pull_request", req.Msg.GetPullRequest()),
	)

	return nil, connect.NewError(
		connect.CodeUnimplemented,
		fmt.Errorf("PublishPullRequestReport is not yet implemented"),
	)
}
