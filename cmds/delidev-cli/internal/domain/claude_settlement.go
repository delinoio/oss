package domain

import (
	"bytes"
	"encoding/json"
	"reflect"
)

type ClaudeCallbackEvidence string

const (
	ClaudeInterruptedDenialProcessed ClaudeCallbackEvidence = "native-claude-interrupted-denial"
	ClaudePlanProcessed              ClaudeCallbackEvidence = "native-claude-plan-approval"
	ClaudeToolProcessed              ClaudeCallbackEvidence = "native-claude-tool-result"
	ClaudeAnswersProcessed           ClaudeCallbackEvidence = "native-claude-question-answers"
	ClaudeDenialProcessed            ClaudeCallbackEvidence = "native-claude-permission-denial"
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
	case ClaudeToolProcessed, ClaudeAnswersProcessed, ClaudeDenialProcessed, ClaudePlanProcessed, ClaudeInterruptedDenialProcessed:
		return nil
	default:
		return invalidClaudeResponse()
	}
}

// Native question output repeats the complete questions and exact answer map.
// Do not infer answer acceptance from display text, an echo or tool completion.
func ClaudeCallbackResultEvidence(original ExecutionInteraction, reply ClaudePermissionResponse, tool ClaudeToolContent) (ClaudeCallbackEvidence, error) {
	request, result := original.Claude, tool.Result
	if reply.Validate(original) != nil || tool.Reference != request.Tool || tool.MessageID != request.MessageID || tool.NativeMessageID != request.NativeMessageID || tool.Index != request.Index || !reflect.DeepEqual(tool.Caller, request.Caller) || tool.Proposal == nil || !EqualClaudeToolInput(tool.Proposal.Applied, request.InputJSON) || result == nil || result.Validate() != nil {
		return "", invalidClaudeResponse()
	}
	if reply.Behavior == ClaudeReplyDeny {
		kind, evidence := ClaudePermissionRuleNonExecution, ClaudeDenialProcessed
		if reply.Interrupt != nil && *reply.Interrupt {
			kind, evidence = ClaudeUserRejectedNonExecution, ClaudeInterruptedDenialProcessed
		}
		if result.NonExecution == nil || result.NonExecution.NativeID != request.Tool.NativeID || result.NonExecution.Kind != kind || result.Error == nil || !*result.Error {
			return "", invalidClaudeResponse()
		}
		return evidence, nil
	}
	if result.NonExecution != nil {
		return "", invalidClaudeResponse()
	}
	if request.Kind == ClaudeToolPermission {
		return ClaudeToolProcessed, nil
	}
	if request.Kind == ClaudePlanApproval {
		if result.Error != nil && *result.Error || result.Structured == nil {
			return "", invalidClaudeResponse()
		}
		var input struct {
			Plan string  `json:"plan"`
			Path *string `json:"planFilePath"`
		}
		var fields map[string]json.RawMessage
		var output struct {
			Plan     *string `json:"plan"`
			Agent    *bool   `json:"isAgent"`
			Path     *string `json:"filePath,omitempty"`
			Task     *bool   `json:"hasTaskTool,omitempty"`
			Edited   *bool   `json:"planWasEdited,omitempty"`
			Awaiting *bool   `json:"awaitingLeaderApproval,omitempty"`
			Request  *string `json:"requestId,omitempty"`
		}
		// The original callback may include native plan-tool options. Read the
		// plan fields without dropping any of the retained original input.
		if Decode([]byte(request.InputJSON), &fields) != nil || json.Unmarshal(fields["plan"], &input.Plan) != nil {
			return "", invalidClaudeResponse()
		}
		if raw, ok := fields["planFilePath"]; ok && json.Unmarshal(raw, &input.Path) != nil {
			return "", invalidClaudeResponse()
		}
		var outputFields map[string]json.RawMessage
		if Decode([]byte(*result.Structured), &outputFields) != nil {
			return "", invalidClaudeResponse()
		}
		for _, raw := range outputFields {
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return "", invalidClaudeResponse()
			}
		}
		if Decode([]byte(*result.Structured), &output) != nil || output.Plan == nil || *output.Plan != input.Plan || output.Agent == nil || *output.Agent || !reflect.DeepEqual(output.Path, input.Path) || output.Edited != nil && *output.Edited || output.Awaiting != nil && *output.Awaiting || output.Request != nil {
			return "", invalidClaudeResponse()
		}
		return ClaudePlanProcessed, nil
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
