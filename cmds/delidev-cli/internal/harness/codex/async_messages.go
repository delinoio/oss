// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

var asyncCitationUnsupported = errors.New("unsupported private citation")

// Shared by live observations and protected complete history. Decode only;
// never reconstruct native items, question authority, or history digests.
func decodeAgentMessage(raw json.RawMessage) (*Message, error) {
	var item struct {
		Type           string          `json:"type"`
		ID             string          `json:"id"`
		Text           *string         `json:"text"`
		Phase          *MessagePhase   `json:"phase"`
		Delivery       json.RawMessage `json:"delivery"`
		Questions      json.RawMessage `json:"questions"`
		MemoryCitation json.RawMessage `json:"memoryCitation"`
	}
	if domain.Decode(raw, &item) != nil || item.Type != "agentMessage" || item.Text == nil || domain.Text(item.ID, "native message identity", 1024, true) != nil || domain.Text(*item.Text, "native message", nativewire.MaxFrame, false) != nil || item.Phase != nil && *item.Phase != CommentaryPhase && *item.Phase != FinalAnswerPhase {
		return nil, incompatible()
	}
	// Citation ownership has its own adapter. Populated citations remain private.
	if len(item.MemoryCitation) > 0 && !bytes.Equal(bytes.TrimSpace(item.MemoryCitation), []byte("null")) {
		return nil, asyncCitationUnsupported
	}
	m := &Message{ID: item.ID, Role: AssistantRole, Text: *item.Text, Phase: item.Phase}
	if len(item.Delivery) == 0 && len(item.Questions) == 0 {
		return m, nil
	}
	a := &domain.CodexAsyncMessage{Version: 1, DeliveryPresent: len(item.Delivery) > 0, QuestionsPresent: len(item.Questions) > 0}
	if len(item.Delivery) > 0 && domain.Decode(item.Delivery, &a.Delivery) != nil {
		return nil, incompatible()
	}
	if len(item.Questions) > 0 {
		if domain.Decode(item.Questions, &a.Questions) != nil {
			return nil, incompatible()
		}
		if a.Questions != nil {
			var questions []map[string]json.RawMessage
			if json.Unmarshal(item.Questions, &questions) != nil {
				return nil, incompatible()
			}
			for _, q := range questions {
				if q["title"] == nil || bytes.Equal(bytes.TrimSpace(q["title"]), []byte("null")) || q["options"] == nil {
					return nil, incompatible()
				}
			}
		}
	}
	if a.Validate() != nil {
		return nil, incompatible()
	}
	m.CodexAsyncMessage = a
	return m, nil
}
