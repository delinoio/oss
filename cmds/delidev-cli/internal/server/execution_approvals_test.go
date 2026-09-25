package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type approvalPublicationClient struct {
	delidevv1connect.WorkerServiceClient
	t *testing.T
}

func (c approvalPublicationClient) PublishExecution(ctx context.Context, req *connect.Request[pb.PublishExecutionRequest]) (*connect.Response[pb.PublishExecutionResponse], error) {
	response, err := c.WorkerServiceClient.PublishExecution(ctx, req)
	if err != nil {
		c.t.Logf("approval publication RPC rejected: %v", err)
	}
	return response, err
}

func publicationApproval(kind codex.ApprovalKind) *codex.ApprovalRequest {
	text := "/fixture"
	enabled, depth := true, uint32(3)
	profile := &codex.PermissionProfile{Network: &codex.AdditionalNetworkPermissions{Enabled: &enabled}, FileSystem: &codex.AdditionalFilePermissions{Read: []string{}, Write: []string{text}, GlobScanMaxDepth: &depth, Entries: []codex.FilePermissionEntry{{Access: codex.FilePermissionWrite, Path: codex.FilePermissionPathValue{Kind: codex.FilePermissionPath, Path: &text}}, {Access: codex.FilePermissionDeny, Path: codex.FilePermissionPathValue{Kind: codex.FilePermissionSpecial, Special: &codex.FilePermissionSpecialValue{Kind: codex.FilePermissionProjects, Subpath: &text}}}}}}
	a := &codex.ApprovalRequest{Kind: kind, StartedAtMS: 42}
	switch kind {
	case codex.CommandApproval:
		rule := codex.NetworkPolicyAmendment{Host: "fixture.invalid", Action: codex.NetworkAllow}
		prefix := []string{"printf", ""}
		a.Command = &codex.CommandApprovalRequest{Kind: codex.WriteStdinApproval, ApprovalID: &text, EnvironmentID: &text, Reason: &text, Command: &text, Cwd: &text, Actions: []codex.CommandAction{{Kind: codex.ListCommandAction, Command: "ls"}}, AdditionalPermissions: profile, Network: &codex.NetworkApprovalContext{Host: rule.Host, Protocol: codex.ApprovalHTTPS}, ProposedExecpolicy: prefix, ProposedNetworkPolicy: []codex.NetworkPolicyAmendment{rule}, AvailableDecisions: []codex.ApprovalDecision{{Kind: codex.ApprovalAccept}, {Kind: codex.ApprovalExecpolicy, Execpolicy: prefix}, {Kind: codex.ApprovalNetworkPolicy, NetworkPolicy: &rule}}}
	case codex.FileApproval:
		a.File = &codex.FileApprovalRequest{Reason: &text, GrantRoot: &text}
	case codex.PermissionsApproval:
		a.Permissions = &codex.PermissionsApprovalRequest{Cwd: text, EnvironmentID: &text, Reason: &text, Permissions: *profile}
	}
	return a
}

func TestExecutionApprovalsRetainScopesInboxAndUnansweredClosure(t *testing.T) {
	f := newPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	cfg.Client = approvalPublicationClient{WorkerServiceClient: cfg.Client, t: t}
	_, mapper := bindNativeMapper(t, f, cfg)
	for _, kind := range []codex.ApprovalKind{codex.CommandApproval, codex.FileApproval, codex.PermissionsApproval} {
		id := domain.NewID()
		request := &codex.Interaction{ID: id, Kind: codex.ApprovalInteraction, NativeID: codex.NativeRequestID{Kind: codex.TextRequestID, Text: string(id)}, Approval: publicationApproval(kind)}
		publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.InteractionRequestedEvent, ItemID: "shared-parent-tool", Interaction: request})
		r, original := readPublishedInteraction(t, f, id)
		if original.Type != domain.NativeApprovalInteraction || original.Questions != nil || original.Response != nil || original.Approval == nil || original.Approval.Harness != domain.Codex || original.Approval.Version != codex.SupportedVersion || original.Approval.Codex.Kind != domain.CodexApprovalKind(kind) || original.Approval.Validate() != nil {
			t.Fatal("approval persistence lost original typed scope")
		}
		if kind == codex.CommandApproval {
			c := original.Approval.Codex.Command
			if c.Kind != domain.CodexWriteStdinApproval || c.ApprovalID == nil || c.ProposedExecpolicy[1] != "" || len(c.AvailableDecisions) != 3 || c.Actions[0].Kind != domain.ListCommandAction || c.AdditionalPermissions.FileSystem.Read == nil || c.AdditionalPermissions.FileSystem.Entries[1].Access != domain.CodexFilePermissionDeny {
				t.Fatal("native command/permission choices changed in publication")
			}
		}
		inbox, _ := readExecutionInbox(t, f, domain.InteractionInbox, id)
		markFixtureInbox(t, f, inbox, domain.InboxRead)
		if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture-approval-question-refusal", id, func(tx *store.Tx) (any, error) {
			_, err := f.service.acceptQuestionResponse(tx, domain.NewID(), id, r.Revision, domain.QuestionResponseInput{Answers: map[string][]string{}})
			if err == nil {
				t.Fatal("question response authorized an approval")
			}
			return nil, err
		}); err == nil || domain.SafeError(err).Code != domain.Conflict {
			t.Fatal("approval did not reject question response before mutation", err)
		}
		publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.InteractionClosedEvent, ItemID: "shared-parent-tool", InteractionState: &codex.InteractionStatus{ID: id, TurnID: f.turn, ItemID: "shared-parent-tool", Closure: codex.InteractionNativeClosed, Delivery: codex.QuestionNotSent}})
		_, closed := readPublishedInteraction(t, f, id)
		_, entry := readExecutionInbox(t, f, domain.InteractionInbox, id)
		if closed.Closure != domain.InteractionNativeClosed || closed.Response != nil || !reflect.DeepEqual(closed.Approval, original.Approval) || entry.ReadState != domain.InboxRead {
			t.Fatal("closure or read state changed native approval meaning")
		}
	}
}

func TestExecutionApprovalLostAcknowledgmentReplaysOriginalPayload(t *testing.T) {
	f := newPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	client := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json"), dropAt: 3}
	cfg.Client = client
	publisher, mapper := bindNativeMapper(t, f, cfg)
	id := domain.NewID()
	request := &codex.Interaction{ID: id, Kind: codex.ApprovalInteraction, NativeID: codex.NativeRequestID{Kind: codex.TextRequestID, Text: "approval-original"}, Approval: publicationApproval(codex.CommandApproval)}
	e := codex.Event{Kind: codex.InteractionRequestedEvent, ThreadID: f.thread, TurnID: f.turn, Correlated: true, ItemID: "command", Interaction: request}
	if handled, err := mapper.PublishCore(context.Background(), e); !handled || err == nil {
		t.Fatal("lost approval publication acknowledgment was not retained")
	}
	*request.Approval.Command.Command = "changed"
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := worker.OpenExecutionPublisher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	r, value := readPublishedInteraction(t, f, id)
	if r.Revision != 1 || *value.Approval.Codex.Command.Command != "/fixture" || len(client.calls) != 4 || client.calls[2] != client.calls[3] {
		t.Fatal("approval replay replaced original content or arrival")
	}
	_, entry := readExecutionInbox(t, f, domain.InteractionInbox, id)
	if entry.ReadState != domain.InboxUnread {
		t.Fatal("approval replay changed unread state")
	}
}

func TestExecutionApprovalInvalidPublicationRollsBackEveryIndex(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	for _, bad := range []string{"wrong-harness", "wrong-version", "mixed-payload", "unoffered-amendment", "response"} {
		id := domain.NewID()
		e := f.event(domain.ExecutionInteractionRequested, 3)
		e.Interaction = &domain.ExecutionInteractionUpdate{ID: id, NativeItemID: "tool", NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: "unique"}, Type: domain.NativeApprovalInteraction, Approval: &domain.ApprovalRequest{Harness: domain.Codex, Version: codex.SupportedVersion, Codex: &domain.CodexApprovalRequest{Kind: domain.CodexCommandApproval, StartedAtMS: 1, Command: &domain.CodexCommandApprovalRequest{Kind: domain.CodexExecuteCommandApproval, AvailableDecisions: []domain.CodexApprovalDecision{{Kind: domain.CodexApprovalAccept}}}}}}
		switch bad {
		case "wrong-harness":
			e.Interaction.Approval.Harness = domain.ClaudeCode
		case "wrong-version":
			e.Interaction.Approval.Version = "future"
		case "mixed-payload":
			e.Interaction.Questions = publicationQuestion()
		case "unoffered-amendment":
			e.Interaction.Approval.Codex.Command.AvailableDecisions[0] = domain.CodexApprovalDecision{Kind: domain.CodexApprovalExecpolicy, Execpolicy: []string{"printf"}}
		}
		req := f.requestEvent(t, e)
		if bad == "response" {
			var fields map[string]any
			_ = json.Unmarshal(req.EventJson, &fields)
			fields["interaction"].(map[string]any)["response"] = map[string]any{"decision": "accept"}
			req.EventJson, _ = json.Marshal(fields)
		}
		if _, err := f.call(req); err == nil {
			t.Fatal("invalid approval publication succeeded", bad)
		}
		if _, err := f.service.Store.Get(context.Background(), domain.InteractionKind, id); err == nil {
			t.Fatal("rejected approval created a resource")
		}
		rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.InboxKind, Limit: 10})
		if err != nil || len(rows) != 0 {
			t.Fatal("rejected approval created inbox state", err)
		}
	}
}

func TestExecutionApprovalPayloadBytesShareQuestionRetentionBound(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	question := f.interactionEvent(3, domain.NewID(), "initial-question")
	question.Interaction.Questions.Questions[0].Text = strings.Repeat("q", domain.MaxMessageText)
	f.publish(t, question)
	reason := strings.Repeat("a", domain.MaxMessageText)
	for i := range 31 {
		id := domain.NewID()
		e := f.event(domain.ExecutionInteractionRequested, uint64(4+i))
		e.Interaction = &domain.ExecutionInteractionUpdate{ID: id, NativeItemID: "shared-tool", NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: "approval-" + strconv.Itoa(i)}, Type: domain.NativeApprovalInteraction, Approval: &domain.ApprovalRequest{Harness: domain.Codex, Version: codex.SupportedVersion, Codex: &domain.CodexApprovalRequest{Kind: domain.CodexFileApproval, File: &domain.CodexFileApprovalRequest{Reason: &reason}}}}
		if i < 30 {
			f.publish(t, e)
			continue
		}
		if _, err := f.call(f.requestEvent(t, e)); err == nil {
			t.Fatal("approval bytes did not share the original question bound")
		}
		if _, err := f.service.Store.Get(context.Background(), domain.InteractionKind, id); err == nil {
			t.Fatal("overflow retained a partial approval resource")
		}
		e.Kind, e.Interaction, e.Outcome = domain.ExecutionTurnFinished, nil, domain.ExecutionStopped
		f.publish(t, e)
	}
}
