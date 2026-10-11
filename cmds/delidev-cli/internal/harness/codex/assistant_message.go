// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// This shared decoder validates live and retained original items without
// rebuilding history or granting embedded questions a synchronous reply owner.
func decodeAssistantMessage(raw json.RawMessage) (*Message, bool, error) {
	var item struct {
		Type           string          `json:"type"`
		ID             string          `json:"id"`
		Text           *string         `json:"text"`
		Phase          *MessagePhase   `json:"phase"`
		Delivery       json.RawMessage `json:"delivery"`
		Questions      json.RawMessage `json:"questions"`
		MemoryCitation json.RawMessage `json:"memoryCitation"`
	}
	if domain.DecodeBounded(raw, &item, nativewire.MaxFrame) != nil || item.Type != "agentMessage" || item.Text == nil || domain.Text(item.ID, "native message identity", 1024, true) != nil || domain.Text(*item.Text, "native message", nativewire.MaxFrame, false) != nil || item.Phase != nil && *item.Phase != CommentaryPhase && *item.Phase != FinalAnswerPhase {
		return nil, false, incompatible()
	}
	metadataFields := map[string]json.RawMessage{}
	if len(item.Delivery) > 0 {
		metadataFields["delivery"] = item.Delivery
	}
	if len(item.Questions) > 0 {
		metadataFields["questions"] = item.Questions
	}
	message := &Message{ID: item.ID, Role: AssistantRole, Text: *item.Text, Phase: item.Phase}
	if len(metadataFields) > 0 {
		encoded, err := json.Marshal(metadataFields)
		if err != nil {
			return nil, false, incompatible()
		}
		var metadata domain.CodexAssistantMetadata
		if domain.DecodeBounded(encoded, &metadata, 256<<10) != nil {
			return nil, false, incompatible()
		}
		message.CodexAssistant = &metadata
	}
	// Populated citations retain their independent unsupported adapter boundary.
	richCitation := len(item.MemoryCitation) > 0 && !bytes.Equal(bytes.TrimSpace(item.MemoryCitation), []byte("null"))
	return message, richCitation, nil
}
