package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestManualNativeClaudePublicEffectContinuation(t *testing.T) {
	for _, scenario := range []claudePublicCase{claudePublicBash, claudePublicWrite, claudePublicEdit} {
		t.Run(string(scenario), func(t *testing.T) { nativeClaudePublicDispatch(t, domain.ExecuteMode, scenario) })
	}
}

func claudePublicEffectName(scenario claudePublicCase) string {
	switch scenario {
	case claudePublicBash, claudePublicBashTask, claudePublicBashToolProgress:
		return "Bash"
	case claudePublicWrite:
		return "Write"
	case claudePublicEdit:
		return "Edit"
	}
	return ""
}

func claudePublicEffectCall(effect string, call, callsPerTurn int32, root string) (string, string, map[string]any) {
	turn, step := (call-1)/callsPerTurn, (call-1)%callsPerTurn
	id := fmt.Sprintf("toolu_public_effect_%d_%d", turn, step)
	path := filepath.Join(root, fmt.Sprintf("original-%d.txt", turn))
	content := fmt.Sprintf("Original effect %d.\n", turn)
	if effect == "Edit" && step == 0 {
		return "Read", id, map[string]any{"file_path": path}
	}
	switch effect {
	case "Bash":
		// Only fixture-controlled integer substitutions enter this native shell
		// command. Appending exposes any accidental original command replay.
		return effect, id, map[string]any{"command": fmt.Sprintf("printf 'Original effect %d.\\n' >> original-%d.txt; cat original-%d.txt", turn, turn, turn)}
	case "Write":
		return effect, id, map[string]any{"file_path": path, "content": content}
	case "Edit":
		return effect, id, map[string]any{"file_path": path, "old_string": fmt.Sprintf("Original retained Read %d.\n", turn), "new_string": content, "replace_all": false}
	}
	panic("unsupported fixture effect")
}

type claudePublicEffectHistory struct {
	mu      sync.Mutex
	results map[string]string
}

func (h *claudePublicEffectHistory) check(t *testing.T, raw []byte, call int32, effect string, callsPerTurn int32) bool {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.results == nil {
		h.results = map[string]string{}
	}
	var request struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &request) != nil {
		t.Error("invalid original provider history")
		return false
	}
	ordinary := 0
	for _, message := range request.Messages {
		if message.Role == "system" {
			continue
		}
		position, turn := ordinary%int(2*callsPerTurn), ordinary/int(2*callsPerTurn)
		ordinary++
		role := "assistant"
		if position%2 == 0 {
			role = "user"
		}
		if message.Role != role {
			t.Error("effect history role changed")
			return false
		}
		if position == 0 {
			input := fmt.Sprintf("continuation input %d", turn)
			if turn == 0 {
				input = "first retained input"
			}
			if !strings.Contains(string(message.Content), input) {
				t.Error("original input lost")
				return false
			}
			continue
		}
		if position == int(2*callsPerTurn)-1 {
			if !strings.Contains(string(message.Content), "Original public Claude result.") {
				t.Error("original assistant answer lost")
				return false
			}
			continue
		}
		step := (position - 1) / 2
		id := fmt.Sprintf("toolu_public_effect_%d_%d", turn, step)
		name := effect
		if effect == "Edit" && step == 0 {
			name = "Read"
		}
		var blocks []struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			Name    string          `json:"name"`
			Tool    string          `json:"tool_use_id"`
			Error   bool            `json:"is_error"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(message.Content, &blocks) != nil || len(blocks) != 1 {
			t.Error("original tool blocks changed")
			return false
		}
		b := blocks[0]
		if position%2 == 1 {
			if b.Type != "tool_use" || b.ID != id || b.Name != name {
				t.Error("original effect call changed")
				return false
			}
		} else {
			if b.Type != "tool_result" || b.Tool != id || b.Error || len(b.Content) == 0 {
				t.Error("original effect result lost or failed")
				return false
			}
			if previous, exists := h.results[id]; exists && previous != string(b.Content) {
				t.Error("original result changed after process replacement")
				return false
			}
			h.results[id] = string(b.Content)
		}
	}
	if ordinary != int(2*call-1) {
		t.Error("effect history duplicated or lost messages", ordinary, call)
		return false
	}
	return true
}

func verifyClaudePublicEffectFiles(t *testing.T, root string, turn int) {
	t.Helper()
	for i := 0; i <= turn; i++ {
		path := filepath.Join(root, fmt.Sprintf("original-%d.txt", i))
		want := "Later independent fixture edit.\n"
		if i == turn {
			want = fmt.Sprintf("Original effect %d.\n", turn)
		}
		raw, err := os.ReadFile(path)
		if err != nil || string(raw) != want {
			t.Fatal("native continuation repeated an effect or replaced a later edit", err, i)
		}
		if i == turn {
			if err := os.WriteFile(path, []byte("Later independent fixture edit.\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
