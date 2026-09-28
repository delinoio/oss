package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestManualNativeClaudePublicReadContinuation(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, scenario := range []claudePublicCase{claudePublicRead, claudePublicReadError} {
			t.Run(fmt.Sprintf("%s/%s", mode, scenario), func(t *testing.T) { nativeClaudePublicDispatch(t, mode, scenario) })
		}
	}
}

func checkClaudePublicReadHistory(t *testing.T, raw []byte, call int32, missing bool) bool {
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
			t.Error("original Read conversation role changed", ordinary)
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
				t.Error("original Read block count changed")
				return false
			}
			b, id := blocks[0], fmt.Sprintf("toolu_public_read_%d", turn)
			if position == 1 {
				if b.Type != "tool_use" || b.ID != id || b.Name != "Read" {
					t.Error("original Read call changed")
					return false
				}
			} else if b.Type != "tool_result" || b.Tool != id || (b.Error != nil && *b.Error) != missing || !missing && !strings.Contains(string(b.Content), fmt.Sprintf("Original retained Read %d.", turn)) {
				t.Error("original Read result lost or reinterpreted")
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
		t.Error("Read continuation lost or duplicated original messages", call, ordinary)
		return false
	}
	return true
}

func verifyClaudePublicReadResult(t *testing.T, ctx context.Context, f *firstDispatchFixture, proof domain.ExecutionCompletion, turn int, missing bool) {
	t.Helper()
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: domain.ID(f.change.Session.Id), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, row := range rows {
		m, err := store.Decode[domain.ExecutionMessage](row)
		if err != nil {
			t.Fatal(err)
		}
		if m.ExecutionID != proof.ExecutionID || m.ClaudeTool == nil {
			continue
		}
		found++
		v := m.ClaudeTool
		if !v.ClaudeReadContinuationCandidate() || v.Reference.NativeID != fmt.Sprintf("toolu_public_read_%d", turn) || (v.Result.Error != nil && *v.Result.Error) != missing || m.State != domain.MessageComplete {
			t.Fatal("public original Read result changed")
		}
		var input struct {
			Path string `json:"file_path"`
		}
		if json.Unmarshal([]byte(v.Proposal.Applied), &input) != nil || filepath.Base(input.Path) != fmt.Sprintf("original-%d.txt", turn) {
			t.Fatal("original applied Read input changed")
		}
		if missing {
			if _, err := os.Lstat(input.Path); !os.IsNotExist(err) {
				t.Fatal("failed Read created a file")
			}
		} else {
			raw, err := os.ReadFile(input.Path)
			if err != nil || string(raw) != fmt.Sprintf("Original retained Read %d.\n", turn) {
				t.Fatal("Read changed original file", err)
			}
			// A later external edit must not rewrite the previous native result.
			if err := os.WriteFile(input.Path, []byte("Later independent fixture content.\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if found != 1 {
		t.Fatal("original Read repeated or disappeared", found)
	}
}
