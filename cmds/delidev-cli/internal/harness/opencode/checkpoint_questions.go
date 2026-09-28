package opencode

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type checkpointQuestionReply struct {
	Claim        SessionClaim `json:"claim"`
	ReplyEventID string       `json:"reply_event_id"`
}

// The native Question tool retains its exact answers in completed metadata.
// Only the original accepted response can bind that result to a closed request;
// an absent pending request after replacement is not an answer acknowledgment.
func (o *inputObserver) checkpointQuestion(value *observedInteraction) (checkpointQuestionReply, bool) {
	var empty checkpointQuestionReply
	if value == nil || value.value.Kind != QuestionInteraction || value.value.Tool == nil || value.value.Permission != nil || value.alwaysAccepted || !nativeID(value.replyEvent, "evt") || value.replyEvent == value.arrival {
		return empty, false
	}
	part := o.parts[o.calls[value.value.Tool.CallID]]
	if part == nil || part.value.Tool == nil || part.value.Tool.Name != "question" || !checkpointInlineTool(part.value.Tool) {
		return empty, false
	}
	if value.attempt == nil || value.attempt.permission != nil {
		return empty, false
	}
	if value.rejected {
		if !checkpointDismissedQuestionTool(part.value.Tool) {
			return empty, false
		}
		claim, valid := o.checkpointDirectClaim(value, RejectQuestionMutation, nil)
		return checkpointQuestionReply{Claim: claim, ReplyEventID: value.replyEvent}, valid
	}
	answers, valid := checkpointQuestionAnswers(part.value.Tool.Metadata)
	if !valid || !validQuestionAnswers(value.value.Questions, answers) {
		return empty, false
	}
	body, err := json.Marshal(struct {
		Answers [][]string `json:"answers"`
	}{answers})
	if err != nil {
		return empty, false
	}
	claim, valid := o.checkpointDirectClaim(value, ReplyQuestionMutation, body)
	if !valid {
		return empty, false
	}
	return checkpointQuestionReply{Claim: claim, ReplyEventID: value.replyEvent}, true
}

func checkpointQuestionAnswers(raw json.RawMessage) ([][]string, bool) {
	var metadata struct {
		Answers     []json.RawMessage `json:"answers"`
		Truncated   *bool             `json:"truncated"`
		Interrupted *bool             `json:"interrupted"`
	}
	if len(raw) > maxHTTPBody || domain.Decode(raw, &metadata) != nil || metadata.Answers == nil || len(metadata.Answers) > 128 || metadata.Truncated == nil || *metadata.Truncated || metadata.Interrupted != nil && *metadata.Interrupted {
		return nil, false
	}
	answers := make([][]string, len(metadata.Answers))
	for i, row := range metadata.Answers {
		var valid bool
		answers[i], valid = nativeStrings(row, 256, 64<<10)
		if !valid {
			return nil, false
		}
	}
	return answers, true
}

// A native failed Question has no answer or auxiliary result state. This shape
// alone never proves dismissal: checkpointQuestion requires the original exact
// reject claim, HTTP/native acceptance and independent closure event as well.
func checkpointDismissedQuestionTool(tool *NativeToolPart) bool {
	if tool == nil || tool.Name != string(checkpointQuestionTool) || tool.State != ToolError || tool.Error == nil || tool.Output != nil {
		return false
	}
	if tool.Metadata == nil {
		return true
	}
	_, err := shape(tool.Metadata, nil, nil)
	return err == nil
}

func checkpointHasQuestionDismissal(proof *checkpointToolHistory) bool {
	for _, reply := range proof.Questions {
		if reply.Claim.Kind == RejectQuestionMutation {
			return true
		}
	}
	return false
}
