package domain

import (
	"encoding/json"
	"testing"
)

func claudeSettlementTool(original ExecutionInteraction) ClaudeToolContent {
	r := original.Claude
	return ClaudeToolContent{Reference: r.Tool, MessageID: r.MessageID, NativeMessageID: r.NativeMessageID, Index: r.Index, Caller: r.Caller, Proposal: &ClaudeToolProposal{Proposed: r.InputJSON, Applied: r.InputJSON}, Result: &ClaudeToolResult{NativeEventID: string(NewID())}}
}

func TestClaudeCallbackSettlementRequiresExactOriginalAnswers(t *testing.T) {
	for _, answers := range []map[string]string{{}, {"Original?": ""}, {"Original?": "Two, One"}, {"Original?": "  Original custom\n"}} {
		o := claudeResponseOriginal(true)
		r := ClaudePermissionResponse{Behavior: ClaudeReplyAllow, Answers: answers}
		tool := claudeSettlementTool(o)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal([]byte(o.Claude.InputJSON), &fields)
		fields["answers"], _ = json.Marshal(answers)
		raw, _ := json.Marshal(fields)
		output := string(raw)
		tool.Result.Structured = &output
		if e, err := ClaudeCallbackResultEvidence(o, r, tool); err != nil || e != ClaudeAnswersProcessed {
			t.Fatal("original answer evidence lost", err)
		}
		for _, bad := range []string{`{}`, `{"questions":null,"answers":{}}`, `{"answers":{"Original?":null}}`, `{"questions":[],"answers":{}}`, `{"questions":[],"answers":{},"unknown":true}`} {
			tool.Result.Structured = &bad
			if _, err := ClaudeCallbackResultEvidence(o, r, tool); err == nil {
				t.Fatal("unverified answers accepted", bad)
			}
		}
		tool.Result.Structured = &output
		if len(answers) != 0 {
			fields["answers"] = json.RawMessage(`{"Original?":"foreign"}`)
			raw, _ = json.Marshal(fields)
			bad := string(raw)
			tool.Result.Structured = &bad
			if _, err := ClaudeCallbackResultEvidence(o, r, tool); err == nil {
				t.Fatal("changed native answer accepted")
			}
		}
	}
}

func TestClaudeCallbackSettlementKeepsToolFailureAndDenialSeparate(t *testing.T) {
	o := claudeResponseOriginal(false)
	tool := claudeSettlementTool(o)
	allow := ClaudePermissionResponse{Behavior: ClaudeReplyAllow}
	failed := true
	tool.Result.Error = &failed
	if e, err := ClaudeCallbackResultEvidence(o, allow, tool); err != nil || e != ClaudeToolProcessed {
		t.Fatal("tool failure erased original processing", err)
	}
	message := "Original denial"
	deny := ClaudePermissionResponse{Behavior: ClaudeReplyDeny, Message: &message}
	if _, err := ClaudeCallbackResultEvidence(o, deny, tool); err == nil {
		t.Fatal("generic error granted denial evidence")
	}
	tool.Result.NonExecution = &ClaudeToolNonExecution{NativeID: o.NativeItemID, Kind: ClaudePermissionRuleNonExecution}
	if e, err := ClaudeCallbackResultEvidence(o, deny, tool); err != nil || e != ClaudeDenialProcessed {
		t.Fatal("original denial lost", err)
	}
	if _, err := ClaudeCallbackResultEvidence(o, allow, tool); err == nil {
		t.Fatal("denial became allowed execution")
	}
	for _, change := range []string{"tool", "provider", "index", "caller", "input", "missing-result", "foreign-denial", "no-error", "family"} {
		copy := tool
		result := *tool.Result
		copy.Result = &result
		switch change {
		case "tool":
			copy.Reference.ID = NewID()
		case "provider":
			copy.MessageID = NewID()
		case "index":
			copy.Index++
		case "caller":
			c := ClaudeDirectToolCaller
			copy.Caller = &c
		case "input":
			copy.Proposal = &ClaudeToolProposal{Applied: `{}`}
		case "missing-result":
			copy.Result = nil
		case "foreign-denial":
			copy.Result.NonExecution = &ClaudeToolNonExecution{NativeID: "foreign", Kind: ClaudePermissionRuleNonExecution}
		case "no-error":
			copy.Result.Error = nil
		case "family":
			o.Claude.Kind, o.Claude.Tool.Name = ClaudePlanApproval, "ExitPlanMode"
		}
		if _, err := ClaudeCallbackResultEvidence(o, deny, copy); err == nil {
			t.Fatal("foreign settlement accepted", change)
		}
	}
}

func TestClaudePlanSettlementRequiresUnchangedRootPlanAndArtifact(t *testing.T) {
	for _, path := range []string{"", "/private/original-plan.md"} {
		o := claudeResponseOriginal(false)
		o.Claude.Kind, o.Claude.Tool.Name = ClaudePlanApproval, "ExitPlanMode"
		input := map[string]any{"plan": "# Original plan"}
		output := map[string]any{"plan": "# Original plan", "isAgent": false, "hasTaskTool": true, "planWasEdited": false}
		if path != "" {
			input["planFilePath"], output["filePath"] = path, path
		}
		raw, _ := json.Marshal(input)
		o.Claude.InputJSON = string(raw)
		tool := claudeSettlementTool(o)
		reply := ClaudePermissionResponse{Behavior: ClaudeReplyAllow}
		encoded, _ := json.Marshal(output)
		result := string(encoded)
		tool.Result.Structured = &result
		if evidence, err := ClaudeCallbackResultEvidence(o, reply, tool); err != nil || evidence != ClaudePlanProcessed {
			t.Fatal("original root Plan result lost", err)
		}
		for _, field := range []string{"plan", "isAgent", "filePath", "planWasEdited", "awaitingLeaderApproval", "requestId", "unknown", "null"} {
			bad := map[string]any{}
			for key, value := range output {
				bad[key] = value
			}
			switch field {
			case "plan":
				bad[field] = "# Changed plan"
			case "isAgent", "planWasEdited", "awaitingLeaderApproval":
				bad[field] = true
			case "filePath":
				bad[field] = "/private/foreign-plan.md"
			case "requestId":
				bad[field] = "foreign-leader-request"
			case "unknown":
				bad[field] = true
			case "null":
				bad["filePath"] = nil
			}
			encoded, _ = json.Marshal(bad)
			result = string(encoded)
			if _, err := ClaudeCallbackResultEvidence(o, reply, tool); err == nil {
				t.Fatal("unverified Plan result accepted", field)
			}
		}
	}
}
