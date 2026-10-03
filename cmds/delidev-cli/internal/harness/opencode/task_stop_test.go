// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"encoding/json"
	"testing"
)

func TestTaskInterruptedTitleRequiresOriginalStopAndMetadata(t *testing.T) {
	title, failure, end := "Original task title", "Tool execution aborted", uint64(200)
	old := &observedPart{value: NativePart{Tool: &NativeToolPart{Name: "task", CallID: "original", State: ToolRunning, Input: json.RawMessage(`{}`), Metadata: json.RawMessage(`{"original":true}`), Title: &title, Timing: &NativeTiming{Start: 100}}}}
	// The unchanged title alone is no Stop or child control authority.
	value := NativePart{ID: "original-part", Tool: &NativeToolPart{Name: "task", CallID: "original", State: ToolError, Input: json.RawMessage(`{}`), Metadata: json.RawMessage(`{"original":true,"interrupted":true}`), Title: &title, Error: &failure, Timing: &NativeTiming{Start: 100, End: &end}}}
	o := &inputObserver{}
	if o.tool(value, old) == nil {
		t.Fatal("unclaimed Stop acquired preserved task title")
	}
	// Other tool error states remain closed even when a task has a Stop.
	fields := map[string]json.RawMessage{"callID": json.RawMessage(`"original"`), "tool": json.RawMessage(`"read"`), "state": json.RawMessage(`{"status":"error","input":{},"title":"Original task title","error":"Tool execution aborted","metadata":{"interrupted":true},"time":{"start":100,"end":200}}`)}
	if _, err := decodeTool(fields, NativePart{}); err == nil {
		t.Fatal("task abort shape widened an ordinary tool")
	}
}
