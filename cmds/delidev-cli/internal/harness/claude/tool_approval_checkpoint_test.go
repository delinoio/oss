package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func approvedBashContinuationFixture(t *testing.T) (*APISession, domain.ID) {
	t.Helper()
	s := inlineContinuationFixture(t, inlineBashTool)
	b := s.current
	tool := b.content.tools["toolu_original_bash"]
	tool.finished, b.finished = false, false
	b.content.tools["toolu_original_bash"] = tool
	raw, _ := json.Marshal(map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "tool_use_id": "toolu_original_bash", "input": map[string]any{"command": "printf private-original-bash"}, "permission_suggestions": nil, "decision_reason": "Private original permission explanation"})
	event := StreamEvent{Kind: NativeRequest, ArrivalID: domain.NewID(), RequestID: "original_permission_request", Body: raw}
	if _, err := b.observeInteraction(event); err != nil {
		t.Fatal(err)
	}
	_, reply, err := b.PreparePermissionReply(event.ArrivalID, PermissionReply{Behavior: PermissionAllow})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.observeInteraction(interactionEcho(event, reply)); err != nil {
		t.Fatal(err)
	}
	tool.finished, b.finished = true, true
	b.content.tools["toolu_original_bash"] = tool
	return s, event.ArrivalID
}

func TestBashCheckpointPreservesApprovedToolWithoutRepeatingReply(t *testing.T) {
	s, arrival := approvedBashContinuationFixture(t)
	closed, err := s.CloseForContinuation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, ref, err := closed.RetainCheckpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"printf private-original-bash", "Private original permission explanation", "Private original Bash content", s.config.Workspace} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("private command/approval content entered checkpoint")
		}
	}
	cfg := s.config
	cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
	restored, err := RestoreCheckpoint(context.Background(), cfg, raw, ref)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.current.closedToolApprovals()
	if err != nil {
		t.Fatal(err)
	}
	after, err := restored.previous.current.closedToolApprovals()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("original approval proof changed", err)
	}
	if len(restored.previous.current.content.tools) != 1 || restored.previous.current.content.tools["toolu_original_bash"].name != "Bash" {
		t.Fatal("original tool family changed")
	}
	if _, _, err := restored.previous.current.PreparePermissionReply(arrival, PermissionReply{Behavior: PermissionAllow}); err == nil {
		t.Fatal("historical approval was replayable")
	}
	if _, _, err := restored.RetainCheckpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestToolApprovalClosureRefusesUnsettledOrDifferentAuthority(t *testing.T) {
	for _, name := range []string{"nil", "unprepared", "unechoed", "canceled", "denied", "question", "plan", "input", "turn", "tool", "arrival", "request", "request-digest", "reply-digest", "retained-input", "retained-bytes", "retained-question"} {
		t.Run(name, func(t *testing.T) {
			s, arrival := approvedBashContinuationFixture(t)
			value := s.current.interactions[arrival]
			switch name {
			case "nil":
				s.current.interactions[arrival] = nil
			case "unprepared":
				value.prepared = false
			case "unechoed":
				value.echoed = false
			case "canceled":
				value.canceled = true
			case "denied":
				value.behavior = PermissionDeny
			case "question":
				value.request.Kind = UserQuestion
			case "plan":
				value.request.Kind = PlanApproval
			case "input":
				value.input = domain.NewID()
			case "turn":
				value.turn = string(domain.NewID())
			case "tool":
				value.request.ToolID = "foreign"
			case "arrival":
				value.event.ArrivalID = domain.NewID()
			case "request":
				value.event.RequestID = "foreign"
			case "request-digest":
				value.requestDigest = [32]byte{}
			case "reply-digest":
				value.reply = [32]byte{}
			case "retained-input":
				value.request.Input = json.RawMessage(`{}`)
			case "retained-bytes":
				value.retainedBytes = 1
			case "retained-question":
				value.questions = map[string]bool{"private": true}
			}
			if closed, err := s.CloseForContinuation(context.Background()); err == nil || closed != nil {
				t.Fatal("unproved approval granted replacement")
			}
		})
	}
}

func TestToolApprovalCheckpointRejectsChangedOwnership(t *testing.T) {
	for _, name := range []string{"arrival", "request", "tool", "input", "turn", "request-digest", "reply-digest", "duplicate", "wrong-family", "missing-tool", "duplicate-family"} {
		t.Run(name, func(t *testing.T) {
			s, _ := approvedBashContinuationFixture(t)
			closed, err := s.CloseForContinuation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			raw, ref, err := closed.RetainCheckpoint(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var cp sessionCheckpoint
			if json.Unmarshal(raw, &cp) != nil {
				t.Fatal("invalid checkpoint")
			}
			approval := &cp.ToolApprovals[0]
			switch name {
			case "arrival":
				approval.Arrival = "invalid"
			case "request":
				approval.Request = ""
			case "tool":
				approval.Tool = "foreign"
			case "input":
				approval.Input = domain.NewID()
			case "turn":
				approval.Turn = string(domain.NewID())
			case "request-digest":
				approval.RequestDigest = ""
			case "reply-digest":
				approval.ReplyDigest = ""
			case "duplicate":
				cp.ToolApprovals = append(cp.ToolApprovals, *approval)
			case "wrong-family":
				cp.ReadTools, cp.BashTools = cp.BashTools, nil
			case "missing-tool":
				cp.BashTools = nil
			case "duplicate-family":
				cp.ReadTools = cp.BashTools
			}
			raw, _ = json.Marshal(cp)
			ref.SHA256 = checkpointDigest(raw)
			cfg := s.config
			cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
			if restored, err := RestoreCheckpoint(context.Background(), cfg, raw, ref); err == nil || restored != nil {
				t.Fatal("changed proof accepted")
			}
		})
	}
}

func TestInlineBashMetadataRejectsAuxiliaryAndInterruptedResults(t *testing.T) {
	for _, name := range []string{"valid", "interrupted", "image", "background", "persisted-output", "missing", "null", "unknown", "wrong-type"} {
		t.Run(name, func(t *testing.T) {
			fields := map[string]any{"stdout": "fixture", "stderr": "", "interrupted": false, "isImage": false, "noOutputExpected": false}
			switch name {
			case "interrupted":
				fields["interrupted"] = true
			case "image":
				fields["isImage"] = true
			case "background":
				fields["backgroundTaskId"] = "task"
			case "persisted-output":
				fields["persistedOutputPath"] = "/private/result"
			case "missing":
				delete(fields, "interrupted")
			case "null":
				fields["stdout"] = nil
			case "unknown":
				fields["newField"] = true
			case "wrong-type":
				fields["stderr"] = 1
			}
			raw, _ := json.Marshal(fields)
			_, valid := inlineBashMetadata(raw)
			if valid != (name == "valid") {
				t.Fatal("Bash profile accepted unproved metadata")
			}
		})
	}
}
