// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestClaudeServerUsageCapabilityStaysUnadvertised(t *testing.T) {
	if claudeServerUsageAdvertised {
		t.Skip("the capability is activated")
	}
	f := newGrokFixture(t)
	response, err := f.service.GetStatus(context.Background(), connect.NewRequest(&pb.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range response.Msg.Capabilities {
		if capability == pb.SystemCapability_SYSTEM_CAPABILITY_CLAUDE_SERVER_USAGE_V1 {
			t.Fatal("Claude usage tracking is advertised before the complete feature is active")
		}
	}
}

func TestClaudeServerUsageRPCsAreUnimplementedUntilActivation(t *testing.T) {
	if claudeServerUsageAdvertised {
		t.Skip("the capability is activated")
	}
	f := newGrokFixture(t)
	mutation := &pb.Mutation{Id: string(domain.NewID()), RequestId: string(domain.NewID()), ExpectedRevision: 1}
	ctx := context.Background()
	calls := map[string]func() error{
		"start": func() error {
			_, err := f.service.StartClaudeUsageLogin(ctx, connect.NewRequest(&pb.StartClaudeUsageLoginRequest{Mutation: mutation}))
			return err
		},
		"progress": func() error {
			_, err := f.service.GetClaudeUsageLoginProgress(ctx, connect.NewRequest(&pb.GetClaudeUsageLoginProgressRequest{AccountId: mutation.Id}))
			return err
		},
		"code": func() error {
			_, err := f.service.SubmitClaudeUsageApprovalCode(ctx, connect.NewRequest(&pb.SubmitClaudeUsageApprovalCodeRequest{Mutation: mutation, Code: []byte("synthetic")}))
			return err
		},
		"confirm": func() error {
			_, err := f.service.ConfirmClaudeUsageLink(ctx, connect.NewRequest(&pb.ConfirmClaudeUsageLinkRequest{Mutation: mutation}))
			return err
		},
		"cancel": func() error {
			_, err := f.service.CancelClaudeUsageLogin(ctx, connect.NewRequest(&pb.CancelClaudeUsageLoginRequest{Mutation: mutation}))
			return err
		},
		"disconnect": func() error {
			_, err := f.service.DisconnectClaudeUsage(ctx, connect.NewRequest(&pb.DisconnectClaudeUsageRequest{Mutation: mutation}))
			return err
		},
	}
	for name, call := range calls {
		if code := connect.CodeOf(call()); code != connect.CodeUnimplemented {
			t.Fatalf("%s returned %v before activation", name, code)
		}
	}
}
