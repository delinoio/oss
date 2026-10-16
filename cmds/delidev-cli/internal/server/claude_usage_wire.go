// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// The six usage operations are served exactly while System capability 108 is
// advertised (claudeUsageActive). A server that deactivates the feature answers
// every operation with Unimplemented, like a server that lacks the capability,
// so no peer can observe a partial feature and no usage state is written.

func claudeUsageUnimplemented() error {
	return connect.NewError(connect.CodeUnimplemented, errors.New("Claude usage tracking is not available on this server"))
}

func (s *Service) StartClaudeUsageLogin(ctx context.Context, req *connect.Request[pb.StartClaudeUsageLoginRequest]) (*connect.Response[pb.StartClaudeUsageLoginResponse], error) {
	if !s.claudeUsageActive() {
		return nil, claudeUsageUnimplemented()
	}
	return s.startClaudeUsageLogin(ctx, req)
}

func (s *Service) GetClaudeUsageLoginProgress(ctx context.Context, req *connect.Request[pb.GetClaudeUsageLoginProgressRequest]) (*connect.Response[pb.GetClaudeUsageLoginProgressResponse], error) {
	if !s.claudeUsageActive() {
		return nil, claudeUsageUnimplemented()
	}
	return s.claudeUsageProgress(ctx, req)
}

func (s *Service) SubmitClaudeUsageApprovalCode(ctx context.Context, req *connect.Request[pb.SubmitClaudeUsageApprovalCodeRequest]) (*connect.Response[pb.SubmitClaudeUsageApprovalCodeResponse], error) {
	if !s.claudeUsageActive() {
		return nil, claudeUsageUnimplemented()
	}
	return s.submitClaudeUsageCode(ctx, req)
}

func (s *Service) ConfirmClaudeUsageLink(ctx context.Context, req *connect.Request[pb.ConfirmClaudeUsageLinkRequest]) (*connect.Response[pb.ConfirmClaudeUsageLinkResponse], error) {
	if !s.claudeUsageActive() {
		return nil, claudeUsageUnimplemented()
	}
	return s.confirmClaudeUsageLink(ctx, req)
}

func (s *Service) CancelClaudeUsageLogin(ctx context.Context, req *connect.Request[pb.CancelClaudeUsageLoginRequest]) (*connect.Response[pb.CancelClaudeUsageLoginResponse], error) {
	if !s.claudeUsageActive() {
		return nil, claudeUsageUnimplemented()
	}
	return s.cancelClaudeUsageLogin(ctx, req)
}

func (s *Service) DisconnectClaudeUsage(ctx context.Context, req *connect.Request[pb.DisconnectClaudeUsageRequest]) (*connect.Response[pb.DisconnectClaudeUsageResponse], error) {
	if !s.claudeUsageActive() {
		return nil, claudeUsageUnimplemented()
	}
	return s.disconnectClaudeUsage(ctx, req)
}
