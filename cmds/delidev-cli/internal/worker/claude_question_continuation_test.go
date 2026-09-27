package worker

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type claudeQuestionSettlementLossRPC struct {
	*openCodeBindingRPC
	loseSettlement bool
}

func (f *claudeQuestionSettlementLossRPC) PublishExecution(ctx context.Context, req *connect.Request[pb.PublishExecutionRequest]) (*connect.Response[pb.PublishExecutionResponse], error) {
	var event domain.ExecutionEvent
	if domain.Decode(req.Msg.EventJson, &event) != nil {
		f.t.Fatal("invalid fixture event")
	}
	f.lose = f.loseSettlement && event.ClaudeSettlement != nil
	return f.openCodeBindingRPC.PublishExecution(ctx, req)
}

func TestClaudeQuestionContinuationWaitsForOriginalResultAndSettlementReceipts(t *testing.T) {
	ctx := context.Background()
	c, rpc, _ := claudeToolFixture(t, "")
	tool := c.tools["tool_original_one"].content
	tool.Reference.Name = "AskUserQuestion"
	input := `{"questions":[{"question":"Original?","header":"Choice","multiSelect":false,"options":[{"label":"One","description":"First"},{"label":"Two","description":"Second"}]}]}`
	tool.Proposal = &domain.ClaudeToolProposal{Proposed: input, Applied: input}
	o := claudeInteractionObservation(c)
	o.Interaction.Request.Kind = claude.UserQuestion
	if _, err := c.PublishInteractionObservation(ctx, o); err != nil {
		t.Fatal(err)
	}
	arrival := o.Interaction.ArrivalID
	original := c.interactions[arrival].update
	reply := domain.ClaudePermissionResponse{Behavior: domain.ClaudeReplyAllow, Answers: map[string]string{"Original?": "Two"}}
	digest, err := domain.ClaudeResponseDigest(domain.ExecutionInteraction{Claude: original.Claude, Type: original.Type, NativeRequestID: original.NativeRequestID, NativeItemID: original.NativeItemID}, reply)
	if err != nil {
		t.Fatal(err)
	}
	c.responses[arrival] = &claudeResponseAttempt{input: reply, journal: claudeResponseJournal{State: responseObserved, ClaimID: domain.NewID(), Control: responseControlIdentity{ResponseID: domain.NewID()}, Native: claude.PermissionReplyClaim{BodyDigest: digest}}}
	o.Interaction.Kind, o.Interaction.Request, o.Interaction.ReplyDigest = claude.InteractionReplyEchoed, nil, digest
	if _, err := c.PublishInteractionObservation(ctx, o); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal([]byte(input), &fields)
	fields["answers"], _ = json.Marshal(reply.Answers)
	metadata, _ := json.Marshal(fields)
	index := tool.Index
	result := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ToolResultObserved, MessageID: tool.NativeMessageID, Index: &index, ToolResult: &claude.NativeToolResult{ID: tool.Reference.NativeID, Name: tool.Reference.Name, Structured: metadata}})
	before := len(rpc.events)
	rpc.lose = true
	if _, err := c.PublishObservation(ctx, result); err == nil {
		t.Fatal("result receipt was not lost")
	}
	if c.tools[tool.Reference.NativeID].historyKind != "" || c.interactions[arrival].continuation != "" {
		t.Fatal("unacknowledged result granted continuation")
	}
	rpc.lose = false
	loss := &claudeQuestionSettlementLossRPC{openCodeBindingRPC: rpc, loseSettlement: true}
	c.binding.publisher.config.Client = loss
	if err := c.ReplayPending(ctx); err == nil {
		t.Fatal("settlement acknowledgment was not lost")
	}
	if c.tools[tool.Reference.NativeID].historyKind != domain.ClaudeQuestionHistory || c.interactions[arrival].continuation != "" || c.interactions[arrival].closed {
		t.Fatal("tool result substituted for acknowledged original settlement")
	}
	settlement := len(rpc.events) - 1
	loss.loseSettlement = false
	if err := c.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if rpc.requests[settlement] != rpc.requests[settlement+1] || !bytes.Equal(rpc.events[settlement], rpc.events[settlement+1]) {
		t.Fatal("original settlement receipt changed")
	}
	if rpc.requests[before] != rpc.requests[before+1] || !bytes.Equal(rpc.events[before], rpc.events[before+1]) {
		t.Fatal("original result receipt changed")
	}
	value := c.interactions[arrival]
	if !value.closed || value.continuation != domain.ClaudeAnswersProcessed || value.update.Claude != nil || value.bytes != 0 || c.tools[tool.Reference.NativeID].historyKind != domain.ClaudeQuestionHistory || c.tools[tool.Reference.NativeID].content != nil || c.responses[arrival].input.Answers != nil {
		t.Fatal("acknowledged question evidence or payload release lost")
	}
}
