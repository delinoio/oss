package domain

import (
	"encoding/json"
	"reflect"
)

type ClaudeCallbackEvidence string

const (
	ClaudeToolProcessed    ClaudeCallbackEvidence = "native-claude-tool-result"
	ClaudeAnswersProcessed ClaudeCallbackEvidence = "native-claude-question-answers"
	ClaudeDenialProcessed  ClaudeCallbackEvidence = "native-claude-permission-denial"
)

// Settlement binds processing of one echoed callback to its original tool
// result. It never means tool success, plan execution or root-input success.
type ClaudeCallbackSettlement struct {
	ArrivalID      ID                     `json:"arrival_id"`
	ToolMessageID  ID                     `json:"tool_message_id"`
	ResultNativeID string                 `json:"result_native_id"`
	Evidence       ClaudeCallbackEvidence `json:"evidence"`
	Sequence       uint64                 `json:"sequence"`
}

type ExecutionClaudeCallbackSettlement struct {
	ExecutionClaudeReplyEcho
	ToolMessageID  ID                     `json:"tool_message_id"`
	ResultNativeID string                 `json:"result_native_id"`
	Evidence       ClaudeCallbackEvidence `json:"evidence"`
}

func (u ExecutionClaudeCallbackSettlement) Validate() error {
	if u.ExecutionClaudeReplyEcho.Validate() != nil || u.ToolMessageID.Validate() != nil || NativeIdentity(u.ResultNativeID).Validate(ClaudeCode, NativeTurnIdentity) != nil {
		return invalidClaudeResponse()
	}
	switch u.Evidence {
	case ClaudeToolProcessed, ClaudeAnswersProcessed, ClaudeDenialProcessed:
		return nil
	default:
		return invalidClaudeResponse()
	}
}

// Native question output repeats the complete questions and exact answer map.
// Do not infer answer acceptance from display text, an echo or tool completion.
func ClaudeCallbackResultEvidence(original ExecutionInteraction, reply ClaudePermissionResponse, tool ClaudeToolContent) (ClaudeCallbackEvidence, error) {
	request, result := original.Claude, tool.Result
	if reply.Validate(original) != nil || request.Kind == ClaudePlanApproval || tool.Reference != request.Tool || tool.MessageID != request.MessageID || tool.NativeMessageID != request.NativeMessageID || tool.Index != request.Index || !reflect.DeepEqual(tool.Caller, request.Caller) || tool.Proposal == nil || !EqualClaudeToolInput(tool.Proposal.Applied, request.InputJSON) || result == nil || result.Validate() != nil {
		return "", invalidClaudeResponse()
	}
	if reply.Behavior == ClaudeReplyDeny {
		if result.NonExecution == nil || result.NonExecution.NativeID != request.Tool.NativeID || result.NonExecution.Kind != ClaudePermissionRuleNonExecution || result.Error == nil || !*result.Error {
			return "", invalidClaudeResponse()
		}
		return ClaudeDenialProcessed, nil
	}
	if result.NonExecution != nil {
		return "", invalidClaudeResponse()
	}
	if request.Kind == ClaudeToolPermission {
		return ClaudeToolProcessed, nil
	}
	if request.Kind != ClaudeUserQuestion || result.Error != nil && *result.Error || result.Structured == nil {
		return "", invalidClaudeResponse()
	}
	var input map[string]json.RawMessage
	var output struct {
		Questions json.RawMessage    `json:"questions"`
		Answers   map[string]*string `json:"answers"`
	}
	if Decode([]byte(request.InputJSON), &input) != nil || Decode([]byte(*result.Structured), &output) != nil || output.Answers == nil || !EqualClaudeToolInput(`{"questions":`+string(input["questions"])+`}`, `{"questions":`+string(output.Questions)+`}`) || len(output.Answers) != len(reply.Answers) {
		return "", invalidClaudeResponse()
	}
	for key, answer := range reply.Answers {
		if output.Answers[key] == nil || *output.Answers[key] != answer {
			return "", invalidClaudeResponse()
		}
	}
	return ClaudeAnswersProcessed, nil
}
