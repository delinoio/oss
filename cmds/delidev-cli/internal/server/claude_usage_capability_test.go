// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// usageStatus reads System capabilities through the real HTTP handler.
func usageStatus(t *testing.T, f *subscriptionFixture) []pb.SystemCapability {
	t.Helper()
	client := delidevv1connect.NewSystemServiceClient(http.DefaultClient, f.http.URL)
	response, err := client.GetStatus(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.Capabilities
}

func TestClaudeServerUsageCapabilityIsAdvertisedWithTheCompleteFeature(t *testing.T) {
	if !claudeServerUsageAdvertised {
		t.Fatal("the complete Claude usage feature must be advertised")
	}
	f := newSubscriptionFixture(t)
	if !f.service.claudeUsageActive() || !slices.Contains(usageStatus(t, f), pb.SystemCapability_SYSTEM_CAPABILITY_CLAUDE_SERVER_USAGE_V1) {
		t.Fatal("System capability 109 is not advertised")
	}
	// The capability stands next to, never instead of, the existing Claude lanes.
	for _, existing := range []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_CLAUDE_SERVER_LOGIN_V1, pb.SystemCapability_SYSTEM_CAPABILITY_CLAUDE_MANAGED_EXECUTION_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SERVER_SUBSCRIPTION_QUOTA_V2} {
		if !slices.Contains(usageStatus(t, f), existing) {
			t.Fatalf("capability %v disappeared", existing)
		}
	}
}

// TestClaudeServerUsageAdvertisementAndOperationsShareOneDecision proves that a
// deactivated server neither advertises capability 109 nor serves any of the six
// operations, so a peer can never see half of the feature.
func TestClaudeServerUsageAdvertisementAndOperationsShareOneDecision(t *testing.T) {
	f := newSubscriptionFixture(t)
	f.service.claudeUsageDisabledTest = true
	if slices.Contains(usageStatus(t, f), pb.SystemCapability_SYSTEM_CAPABILITY_CLAUDE_SERVER_USAGE_V1) {
		t.Fatal("a deactivated server advertises capability 109")
	}
	mutation := &pb.Mutation{Id: string(domain.NewID()), RequestId: string(domain.NewID()), ExpectedRevision: 1}
	ctx := context.Background()
	token := f.service.Identity.Token
	calls := map[string]func() error{
		"start": func() error {
			_, err := f.client.StartClaudeUsageLogin(ctx, subscriptionRequest(token, &pb.StartClaudeUsageLoginRequest{Mutation: mutation}))
			return err
		},
		"progress": func() error {
			_, err := f.client.GetClaudeUsageLoginProgress(ctx, subscriptionRequest(token, &pb.GetClaudeUsageLoginProgressRequest{AccountId: mutation.Id}))
			return err
		},
		"code": func() error {
			_, err := f.client.SubmitClaudeUsageApprovalCode(ctx, subscriptionRequest(token, &pb.SubmitClaudeUsageApprovalCodeRequest{Mutation: mutation, Code: []byte("synthetic")}))
			return err
		},
		"confirm": func() error {
			_, err := f.client.ConfirmClaudeUsageLink(ctx, subscriptionRequest(token, &pb.ConfirmClaudeUsageLinkRequest{Mutation: mutation}))
			return err
		},
		"cancel": func() error {
			_, err := f.client.CancelClaudeUsageLogin(ctx, subscriptionRequest(token, &pb.CancelClaudeUsageLoginRequest{Mutation: mutation}))
			return err
		},
		"disconnect": func() error {
			_, err := f.client.DisconnectClaudeUsage(ctx, subscriptionRequest(token, &pb.DisconnectClaudeUsageRequest{Mutation: mutation}))
			return err
		},
	}
	for name, call := range calls {
		if code := connect.CodeOf(call()); code != connect.CodeUnimplemented {
			t.Fatalf("%s returned %v on a deactivated server", name, code)
		}
	}
}

// TestClaudeUsageSixOperationsAreReachableEndToEnd drives the whole feature the
// way the desktop client does, through the HTTP handler with the generated
// Connect client: start, progress, approval code, Back (cancel), confirm,
// Refresh (observation request) and disconnect.
func TestClaudeUsageSixOperationsAreReachableEndToEnd(t *testing.T) {
	f := newReadFixture(t, nil)
	// The fixture already ran start, progress, code and confirm through the
	// handler; the section is linked and nothing was read yet.
	if section := f.section(); section.Connection != domain.ClaudeUsageLinked || !section.Published || len(section.Windows) != 0 {
		t.Fatalf("linked section = %+v", section)
	}
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	if section := f.section(); len(section.Windows) != 2 || section.LastSuccessAt == nil {
		t.Fatalf("refresh published %+v", section)
	}
	generation := string(f.section().Generation)
	if err := f.disconnect(generation); err != nil {
		t.Fatal(err)
	}
	if section := f.section(); section.Connection != domain.ClaudeUsageReconnectRequired || len(section.Windows) != 0 || section.OwesCleanup() {
		t.Fatalf("disconnect left %+v", section)
	}
	// A reconnect that the user abandons with Back/Cancel discards everything.
	f.usage = newFakeUsageNative()
	op, err := f.start()
	if err != nil {
		t.Fatal(err)
	}
	close(f.usage.ready)
	if progress := f.usageFixture.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING); progress.Url == "" || !progress.CodeRequested {
		t.Fatalf("waiting progress = %+v", progress)
	}
	if err := f.cancel(op); err != nil {
		t.Fatal(err)
	}
	if section := f.section(); section.Connection != domain.ClaudeUsageReconnectRequired || section.Published || section.OwesCleanup() {
		t.Fatalf("cancel left %+v", section)
	}
	if refs := f.usageRefs(); len(refs) != 0 {
		t.Fatalf("usage credentials remain after disconnect and cancel: %d", len(refs))
	}
}
