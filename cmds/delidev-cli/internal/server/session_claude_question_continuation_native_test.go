package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestManualNativeClaudePublicQuestionContinuation(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) { nativeClaudePublicDispatch(t, mode, claudePublicQuestionHistory) })
	}
}

func checkClaudePublicQuestionHistory(t *testing.T, raw []byte, call int32) bool {
	t.Helper()
	var request struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &request) != nil {
		t.Error("invalid original provider messages")
		return false
	}
	ordinary := 0
	for _, message := range request.Messages {
		if message.Role == "system" {
			continue
		}
		position, turn := ordinary%4, ordinary/4
		ordinary++
		role := "assistant"
		if position%2 == 0 {
			role = "user"
		}
		if message.Role != role {
			t.Error("original question conversation role changed", ordinary)
			return false
		}
		switch position {
		case 0:
			want := fmt.Sprintf("continuation input %d", turn)
			if turn == 0 {
				want = "first retained input"
			}
			if !strings.Contains(string(message.Content), want) {
				t.Error("original input lost")
				return false
			}
		case 1, 2:
			var blocks []struct {
				Type    string          `json:"type"`
				ID      string          `json:"id"`
				Name    string          `json:"name"`
				Tool    string          `json:"tool_use_id"`
				Error   *bool           `json:"is_error"`
				Content json.RawMessage `json:"content"`
			}
			if json.Unmarshal(message.Content, &blocks) != nil || len(blocks) != 1 {
				t.Error("original question block count changed")
				return false
			}
			b, id := blocks[0], fmt.Sprintf("toolu_public_question_%d", turn)
			if position == 1 {
				if b.Type != "tool_use" || b.ID != id || b.Name != "AskUserQuestion" {
					t.Error("original question call changed")
					return false
				}
			} else if b.Type != "tool_result" || b.Tool != id || (b.Error != nil && *b.Error) || !strings.Contains(string(b.Content), "Which original option?") || !strings.Contains(string(b.Content), "Two") {
				t.Error("original question result lost or reinterpreted")
				return false
			}
		case 3:
			if !strings.Contains(string(message.Content), "Original public Claude result.") {
				t.Error("original final answer lost")
				return false
			}
		}
	}
	if ordinary != int(2*call-1) {
		t.Error("question continuation lost or duplicated original messages", call, ordinary)
		return false
	}
	return true
}

func verifyClaudePublicQuestionResults(t *testing.T, ctx context.Context, f *firstDispatchFixture, count int) {
	t.Helper()
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: domain.ID(f.change.Session.Id), Limit: 10})
	if err != nil || len(rows) != count {
		t.Fatal("original question history changed", err, len(rows), count)
	}
	for _, row := range rows {
		value, err := store.Decode[domain.ExecutionInteraction](row)
		if err != nil || value.Claude == nil {
			t.Fatal("original question missing", err)
		}
		message, err := f.service.Store.Get(ctx, domain.MessageKind, value.Claude.Tool.ID)
		if err != nil {
			t.Fatal(err)
		}
		tool, err := store.Decode[domain.ExecutionMessage](message)
		if err != nil || !value.ClaudeQuestionContinuationEvidence(tool) {
			t.Fatal("original question settlement changed", err)
		}
	}
}
