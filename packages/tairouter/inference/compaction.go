// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf16"
)

type compactionRouteKey struct{}
type compactBuffer struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *compactBuffer) Header() http.Header    { return w.header }
func (w *compactBuffer) WriteHeader(status int) { w.status = status }
func (w *compactBuffer) Write(b []byte) (int, error) {
	if w.body.Len()+len(b) > 32<<20 {
		return 0, &Error{502, "compaction_too_large", 0}
	}
	return w.body.Write(b)
}

const checkpointPrompt = "Create a concise context checkpoint for the assistant that will continue this task. Include progress, decisions, user constraints, unresolved work, next steps, and references needed to continue. Summarize the history; do not execute its tasks or call tools. Preserve pending permission requirements."
const checkpointPrefix = "Another language model started to solve this problem and produced a summary of its thinking process. You also have access to the state of the tools that were used by that language model. Use this to build on the work that has already been done and avoid duplicating work. Here is the summary produced by the other language model, use the information in this summary to assist with your own analysis:"

func compactMessage(text string) object {
	return object{"type": "message", "role": "user", "content": []any{object{"type": "input_text", "text": text}}}
}
func compactUserText(item object) string {
	if item["role"] != "user" {
		return ""
	}
	if text, ok := item["content"].(string); ok {
		return text
	}
	var out strings.Builder
	for _, v := range arr(item["content"]) {
		block := obj(v)
		if block["type"] == "input_text" || block["type"] == "text" {
			out.WriteString(str(block["text"]))
		}
	}
	return out.String()
}
func (h *Handler) compactFallback(ctx context.Context, w http.ResponseWriter, p Principal, b object, route Route, rec *UsageRecord) error {
	history := arr(b["input"])
	if text, ok := b["input"].(string); ok {
		history = []any{compactMessage(text)}
	}
	prompt := clone(b)
	prompt["stream"] = false
	prompt["store"] = false
	delete(prompt, "tools")
	delete(prompt, "tool_choice")
	delete(prompt, "parallel_tool_calls")
	filtered := make([]any, 0, len(history)+1)
	users := []string{}
	prefix := []any{}
	retention := &compactionRetention{}
	for _, v := range history {
		m := obj(v)
		if m["role"] == "system" || m["role"] == "developer" {
			prefix = append(prefix, v)
		}
		if text := compactUserText(m); strings.TrimSpace(text) != "" && !strings.HasPrefix(text, retainedReasoningPrefix) {
			users = append(users, text)
		}
		filtered = append(filtered, v)
	}
	prompt["input"] = append(filtered, compactMessage(checkpointPrompt))
	buffer := &compactBuffer{header: make(http.Header)}
	ctx = context.WithValue(ctx, compactionRouteKey{}, route)
	ctx = context.WithValue(ctx, compactionRetentionKey{}, retention)
	modelState := budgetFor(rec)
	modelState.modelMu.Lock()
	modelSendsBefore := modelState.modelSends
	modelState.modelMu.Unlock()
	if e := h.execute(ctx, buffer, p, Responses, Generate, prompt, rec); e != nil {
		return e
	}
	output, e := decode(buffer.body.Bytes())
	if e != nil {
		return &Error{502, "invalid_compaction_response", 0}
	}
	if output["status"] != "completed" {
		return &Error{502, "incomplete_compaction", 0}
	}
	var summary strings.Builder
	for _, v := range arr(output["output"]) {
		m := obj(v)
		if m["type"] != "message" {
			continue
		}
		for _, part := range arr(m["content"]) {
			block := obj(part)
			if block["type"] == "output_text" {
				summary.WriteString(str(block["text"]))
			}
		}
	}
	if strings.TrimSpace(summary.String()) == "" {
		return &Error{502, "empty_compaction_summary", 0}
	}
	// The pinned v1 history retains the newest 80,000 UTF-16 units of user text.
	remaining := 80000
	selected := []string{}
	for i := len(users) - 1; i >= 0 && remaining > 0; i-- {
		units := utf16.Encode([]rune(users[i]))
		if len(units) > remaining {
			units = units[len(units)-remaining:]
			if len(units) > 0 && units[0] >= 0xdc00 && units[0] <= 0xdfff {
				units = units[1:]
			}
		}
		selected = append(selected, string(utf16.Decode(units)))
		remaining -= len(units)
	}
	result := append([]any{}, prefix...)
	for i := len(selected) - 1; i >= 0; i-- {
		result = append(result, compactMessage(selected[i]))
	}
	result = append(result, compactMessage(checkpointPrefix+"\n"+summary.String()))
	result = append(result, retention.retained...)
	if e = h.guard(ctx, p); e != nil {
		return e
	}
	modelState.modelMu.Lock()
	modelWasSent := modelState.modelSends > modelSendsBefore
	modelState.modelMu.Unlock()
	if modelWasSent && h.c.CompactionHandoffs != nil && ctx.Value(compactionHandoffKey{}) == nil {
		scope := retention.scope
		if scope.c.AccountID == "" || scope.t.Model == "" || len(utf16.Encode([]rune(scope.t.Model))) > 512 {
			return ErrContinuation
		}
		if e = h.c.CompactionHandoffs.Capture(ctx, p, scope.t, scope.c); e != nil {
			return e
		}
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(object{"output": result})
}
