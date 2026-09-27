package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestManualNativeClaudePublicAutomaticCompaction(t *testing.T) {
	nativeClaudePublicDispatch(t, domain.ExecuteMode, claudePublicCompaction)
}

func claudePublicCompactionResponse(t *testing.T, w http.ResponseWriter, raw []byte, n int32, model string) {
	t.Helper()
	var request struct {
		Model    string          `json:"model"`
		Stream   bool            `json:"stream"`
		Messages json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(raw, &request) != nil || request.Model != model || n < 1 || n > 5 {
		t.Error("original compaction provider scope changed")
		w.WriteHeader(400)
		return
	}
	if n >= 4 {
		body := string(request.Messages)
		if strings.Contains(body, strings.Repeat("fixture ", 100)) || strings.Contains(body, strings.Repeat("Private fixture conversation response. ", 100)) || !strings.Contains(body, fmt.Sprintf("continuation input %d", n-2)) {
			t.Error("native compaction did not preserve new input and reduced context")
			w.WriteHeader(400)
			return
		}
	}
	answer := "Original public Claude result."
	if n == 1 {
		answer = strings.Repeat("Private fixture conversation response. ", 4000)
	}
	if n == 3 {
		answer = "<summary>Private fixture conversation retained by native compaction.</summary>"
	}
	tokens := uint64(5)
	if n == 2 {
		tokens = 180000
	}
	message := map[string]any{"id": fmt.Sprintf("msg_compact_%d", n), "type": "message", "role": "assistant", "content": []any{}, "model": model, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": tokens, "output_tokens": 0}}
	if !request.Stream {
		message["content"] = []any{map[string]any{"type": "text", "text": answer}}
		message["stop_reason"] = "end_turn"
		message["usage"].(map[string]any)["output_tokens"] = 8
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(message)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range []map[string]any{
		{"type": "message_start", "message": message},
		{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
		{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": answer}},
		{"type": "content_block_stop", "index": 0},
		{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 8}},
		{"type": "message_stop"},
	} {
		encoded, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], encoded)
	}
}

func verifyClaudePublicCompaction(t *testing.T, ctx context.Context, f *firstDispatchFixture, completion domain.ExecutionCompletion) {
	t.Helper()
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: domain.ID(f.change.Session.Id), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var boundary, summary domain.ExecutionMessage
	var boundaryID, summaryID domain.ID
	boundaries, summaries := 0, 0
	originalInput, originalResponse := false, false
	for _, row := range rows {
		m, err := store.Decode[domain.ExecutionMessage](row)
		if err != nil {
			t.Fatal(err)
		}
		if m.Role == domain.UserMessage && m.Text == "first retained input "+strings.Repeat("fixture ", 30000) {
			originalInput = true
		}
		if m.Claude != nil {
			for _, block := range m.Claude.Blocks {
				if block.Block.Kind == domain.ClaudeText && block.Block.Text == strings.Repeat("Private fixture conversation response. ", 4000) {
					originalResponse = true
				}
			}
		}
		if m.ExecutionID != completion.ExecutionID || m.ClaudeProgress == nil {
			continue
		}
		p := m.ClaudeProgress
		if p.Validate() != nil {
			t.Fatal("invalid original compaction progress")
		}
		switch p.Kind {
		case domain.ClaudeCompactionProgress:
			boundaries++
			boundary = m
			boundaryID = row.ID
			if p.Compaction.Trigger != domain.ClaudeAutomaticCompaction {
				t.Fatal("native trigger changed")
			}
		case domain.ClaudeCompactionSummaryProgress:
			summaries++
			summary = m
			summaryID = row.ID
		}
	}
	if !originalInput || !originalResponse || boundaries != 1 || summaries != 1 || summary.FirstSequence <= boundary.FirstSequence || summary.ClaudeProgress.CompactionSummary.BoundaryID != boundary.NativeID || summary.ClaudeProgress.CompactionSummary.BoundaryMessageID != boundaryID || summary.InputID != "" || boundary.InputID != "" {
		t.Fatal("native compaction lost original independent boundary/summary", boundaries, summaries)
	}
	session, err := store.Decode[domain.Session](f.refresh(t))
	if err != nil || session.Execution.ClaudeCompaction == nil || !session.Execution.ClaudeCompaction.Closed() || session.Execution.ClaudeProgress.LatestCompactionID != boundaryID || session.Execution.ClaudeProgress.LatestCompactionSummaryID != summaryID {
		t.Fatal("compaction state did not retain original references", err)
	}
	t.Logf("original compaction before=%s after_reported=%t summary_blocks=%d", boundary.ClaudeProgress.Compaction.Before, boundary.ClaudeProgress.Compaction.After != nil, len(summary.ClaudeProgress.CompactionSummary.Blocks))
}

func TestManualNativeClaudePublicCompactionRecovery(t *testing.T) {
	nativeClaudePublicDispatch(t, domain.ExecuteMode, claudePublicCompaction, claudePublicRecoveryCase{turn: 3})
}
