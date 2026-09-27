package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const retainedQuestion = "Which private original approach?"
const retainedAnswer = "First private answer"

func inlineQuestionFixtureValues() (map[string]any, map[string]any) {
	questions := []any{map[string]any{"question": retainedQuestion, "header": "Approach", "multiSelect": false, "options": []any{map[string]any{"label": retainedAnswer, "description": "Use the first approach."}, map[string]any{"label": "Second", "description": "Use the second approach."}}}}
	return map[string]any{"questions": questions}, map[string]any{"questions": questions, "answers": map[string]string{retainedQuestion: retainedAnswer}}
}

func answeredQuestionContinuationFixture(t *testing.T) (*APISession, domain.ID) {
	t.Helper()
	s := inlineContinuationFixture(t, inlineQuestionTool)
	b := s.current
	toolID := "toolu_original_askuserquestion"
	tool := b.content.tools[toolID]
	tool.finished, b.finished = false, false
	b.content.tools[toolID] = tool
	input, _ := inlineQuestionFixtureValues()
	raw, _ := json.Marshal(map[string]any{"subtype": "can_use_tool", "tool_name": "AskUserQuestion", "tool_use_id": toolID, "input": input})
	event := StreamEvent{Kind: NativeRequest, ArrivalID: domain.NewID(), RequestID: "original_question_request", Body: raw}
	if _, err := b.observeInteraction(event); err != nil {
		t.Fatal(err)
	}
	_, reply, err := b.PreparePermissionReply(event.ArrivalID, PermissionReply{Behavior: PermissionAllow, Answers: map[string]string{retainedQuestion: retainedAnswer}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.observeInteraction(interactionEcho(event, reply)); err != nil {
		t.Fatal(err)
	}
	tool.finished, b.finished = true, true
	b.content.tools[toolID] = tool
	return s, event.ArrivalID
}

func TestQuestionCheckpointRetainsOriginalAnswersWithoutReplay(t *testing.T) {
	s, arrival := answeredQuestionContinuationFixture(t)
	closed, err := s.CloseForContinuation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, ref, err := closed.RetainCheckpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{retainedQuestion, retainedAnswer, s.config.Home, s.config.Workspace} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("checkpoint contains private question content")
		}
	}
	cfg := s.config
	cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
	restored, err := RestoreCheckpoint(context.Background(), cfg, raw, ref)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.current.closedQuestionAnswers()
	if err != nil {
		t.Fatal(err)
	}
	after, err := restored.previous.current.closedQuestionAnswers()
	if err != nil || !reflect.DeepEqual(before, after) || len(after) != 1 {
		t.Fatal("original answer proof changed", err)
	}
	if restored.previous.current.interactions[arrival].request.Kind != UserQuestion {
		t.Fatal("question became a permission approval")
	}
	if _, _, err := restored.previous.current.PreparePermissionReply(arrival, PermissionReply{Behavior: PermissionAllow, Answers: map[string]string{retainedQuestion: retainedAnswer}}); err == nil {
		t.Fatal("historical answer could be replayed")
	}
	if _, _, err := restored.RetainCheckpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestQuestionContinuationRequiresOriginalAnswerAndResult(t *testing.T) {
	for _, name := range []string{"unprepared", "unechoed", "canceled", "denied", "missing-result", "changed-result", "permission", "duplicate", "missing", "retained-body"} {
		t.Run(name, func(t *testing.T) {
			s, arrival := answeredQuestionContinuationFixture(t)
			v := s.current.interactions[arrival]
			switch name {
			case "unprepared":
				v.prepared = false
			case "unechoed":
				v.echoed = false
			case "canceled":
				v.canceled = true
			case "denied":
				v.behavior = PermissionDeny
			case "missing-result":
				v.questionResult = [32]byte{}
			case "changed-result":
				v.questionResult[0] ^= 1
			case "permission":
				v.request.Kind = ToolPermission
			case "duplicate":
				s.current.interactions[domain.NewID()] = v
			case "missing":
				delete(s.current.interactions, arrival)
			case "retained-body":
				v.request.Input = json.RawMessage(`{}`)
			}
			if closed, err := s.CloseForContinuation(context.Background()); err == nil || closed != nil {
				t.Fatal("unproved original answer granted continuation")
			}
		})
	}
}

func TestQuestionCheckpointRejectsMissingOrForeignEvidence(t *testing.T) {
	for _, name := range []string{"missing-answer", "missing-tool", "foreign-result", "foreign-input", "foreign-turn", "permission", "duplicate", "request-digest", "reply-digest", "arrival", "request"} {
		t.Run(name, func(t *testing.T) {
			s, _ := answeredQuestionContinuationFixture(t)
			closed, err := s.CloseForContinuation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			raw, ref, err := closed.RetainCheckpoint(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var cp sessionCheckpoint
			if err := json.Unmarshal(raw, &cp); err != nil {
				t.Fatal(err)
			}
			answer := &cp.QuestionAnswers[0]
			switch name {
			case "missing-answer":
				cp.QuestionAnswers = nil
			case "missing-tool":
				cp.QuestionTools = nil
			case "foreign-result":
				answer.ResultDigest = checkpointDigest([]byte("foreign"))
			case "foreign-input":
				answer.Input = domain.NewID()
			case "foreign-turn":
				answer.Turn = string(domain.NewID())
			case "permission":
				cp.ToolApprovals, cp.QuestionAnswers = []checkpointToolApproval{answer.checkpointToolApproval}, nil
			case "duplicate":
				cp.QuestionAnswers = append(cp.QuestionAnswers, *answer)
			case "request-digest":
				answer.RequestDigest = ""
			case "reply-digest":
				answer.ReplyDigest = ""
			case "arrival":
				answer.Arrival = "invalid"
			case "request":
				answer.Request = ""
			}
			raw, _ = json.Marshal(cp)
			ref.SHA256 = checkpointDigest(raw)
			cfg := s.config
			cfg.API = APIConfig{ServerOrigin: s.serverOrigin}
			if restored, err := RestoreCheckpoint(context.Background(), cfg, raw, ref); err == nil || restored != nil {
				t.Fatal("invalid answer evidence restored")
			}
		})
	}
}

func TestQuestionMetadataPreservesExplicitEmptyAnswers(t *testing.T) {
	for _, name := range []string{"empty-map", "empty-string", "null-map", "null-answer", "unknown-answer", "unknown-field"} {
		t.Run(name, func(t *testing.T) {
			_, fields := inlineQuestionFixtureValues()
			switch name {
			case "empty-map":
				fields["answers"] = map[string]string{}
			case "empty-string":
				fields["answers"] = map[string]string{retainedQuestion: ""}
			case "null-map":
				fields["answers"] = nil
			case "null-answer":
				fields["answers"] = map[string]any{retainedQuestion: nil}
			case "unknown-answer":
				fields["answers"] = map[string]string{"foreign": "value"}
			case "unknown-field":
				fields["extra"] = true
			}
			raw, _ := json.Marshal(fields)
			_, valid := inlineQuestionMetadata(raw)
			if valid != (name == "empty-map" || name == "empty-string") {
				t.Fatal("question result metadata lost original answer semantics")
			}
		})
	}
}
