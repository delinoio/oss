// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenCodeInterruptedTaskPreservesOnlyOriginalRunningTitle(t *testing.T) {
	input := `{"description":"Original task","prompt":"Original prompt","subagent_type":"general"}`
	metadata := `{"parentSessionId":"ses_01960dcbe1faabcdefghijklmn","sessionId":"ses_01960dcbe1fbABCDEFGHIJKLMN","model":{"modelID":"original-model","providerID":"delidev"}}`
	title, failure, end := "Original title", "Tool execution aborted", uint64(200)
	running := ToolSnapshot{Kind: OpenCodeBuiltinTool, Status: ToolRunning, Builtin: &OpenCodeBuiltinObservation{Name: OpenCodeTask, CallID: "original-call", InputJSON: input, Title: &title, MetadataJSON: &metadata, Timing: &OpenCodeToolTiming{Start: 100}}}
	interrupted := strings.TrimSuffix(metadata, "}") + `,"interrupted":true}`
	failed := ToolSnapshot{Kind: OpenCodeBuiltinTool, Status: ToolFailed, Builtin: &OpenCodeBuiltinObservation{Name: OpenCodeTask, CallID: "original-call", InputJSON: input, Title: &title, MetadataJSON: &interrupted, Timing: &OpenCodeToolTiming{Start: 100, End: &end}, Error: &failure}}
	if ValidateOpenCodeBuiltinTransition(running, failed) != nil {
		t.Fatal("original interrupted task title was rejected")
	}
	changed := "Changed title"
	failed.Builtin.Title = &changed
	if ValidateOpenCodeBuiltinTransition(running, failed) == nil {
		t.Fatal("interruption changed the original running title")
	}
	failed.Builtin.Title, failed.Builtin.MetadataJSON = &title, &metadata
	if failed.Validate() == nil {
		t.Fatal("ordinary failure acquired interrupted title authority")
	}
	child := openCodeChildFixture()
	child.Source, child.Status = OpenCodeChildCleanupSource, SubagentInterrupted
	if child.Validate() == nil {
		t.Fatal("missing original scope cleanup created child interruption")
	}
}

func openCodeChildFixture() SubagentObservation {
	model := "original-model"
	return SubagentObservation{ID: NewID(), NativeID: "ses_01960dcbe1fbABCDEFGHIJKLMN", ParentID: "ses_01960dcbe1faabcdefghijklmn", ParentToolID: "prt_01960dcbe1fa1234567890ABCD", OpenCodeTool: &OpenCodeParentTool{ID: NewID(), MessageID: "msg_01960dcbe1faABCDEFGHIJKLMN", PartID: "prt_01960dcbe1fa1234567890ABCD", CallID: "original-call"}, Source: OpenCodeTaskSource, SourceID: "original-task", Status: SubagentPending, RequestedModel: &model}
}

func TestOpenCodeChildRequiresOriginalTaskAndUniqueRootTool(t *testing.T) {
	child := openCodeChildFixture()
	state, err := ApplySubagents(nil, child.ParentID, []SubagentObservation{child})
	if err != nil || len(state) != 1 {
		t.Fatal("original task ownership rejected", err)
	}
	other := child
	other.ID = NewID()
	other.NativeID = "ses_01960dcbe1fcABCDEFGHIJKLMN"
	if _, err := ApplySubagents(state, child.ParentID, []SubagentObservation{other}); err == nil {
		t.Fatal("second child reused original task")
	}
	child.Source, child.Status = OpenCodeChildHistorySource, SubagentCompleted
	child.ObservedModel = child.RequestedModel
	child.Output = &SubagentOutput{NativeMessageID: "msg_01960dcbe1fbABCDEFGHIJKLMN", Text: "Original response", Partial: true}
	input, output, total := "30", "6", "36"
	nativeTotal := uint64(36)
	child.Usage = &SubagentUsage{Scope: SubagentResponseUsage, Input: &input, Output: &output, Total: &total, NativeReport: OpenCodeChildUsageReport(30, 6, 0, 0, 0, &nativeTotal)}
	if _, err := ApplySubagents(state, child.ParentID, []SubagentObservation{child}); err != nil {
		t.Fatal("independent exact child response rejected", err)
	}
	child.Usage.Input = &total
	if _, err := ApplySubagents(state, child.ParentID, []SubagentObservation{child}); err == nil {
		t.Fatal("mismatched native counter entered child state")
	}
}

func TestOpenCodeForegroundTaskRejectsReuseBackgroundAndUnknownFields(t *testing.T) {
	base := map[string]any{"description": "Original task", "prompt": "Original prompt", "subagent_type": "general"}
	for _, field := range []string{"task_id", "background", "foreign"} {
		value := map[string]any{}
		for k, v := range base {
			value[k] = v
		}
		if field == "background" {
			value[field] = true
		} else {
			value[field] = ""
		}
		raw, _ := json.Marshal(value)
		if _, err := DecodeOpenCodeForegroundTask(raw); err == nil {
			t.Fatalf("unsupported %s acquired foreground ownership", field)
		}
	}
	child := openCodeChildFixture()
	child.Status = SubagentCompleted
	if child.Validate() == nil {
		t.Fatal("root task completion fabricated child settlement")
	}
}
