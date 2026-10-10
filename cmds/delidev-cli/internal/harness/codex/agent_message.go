// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// decodeAgentMessage is shared by live and original complete-history validation.
// It retains native presence/null/empty shapes; no synchronous request is made.
func decodeAgentMessage(raw json.RawMessage) (*Message, bool, error) {
	var item struct {
		Type           string          `json:"type"`
		ID             string          `json:"id"`
		Text           *string         `json:"text"`
		Phase          *MessagePhase   `json:"phase"`
		Delivery       json.RawMessage `json:"delivery"`
		MemoryCitation json.RawMessage `json:"memoryCitation"`
		Questions      json.RawMessage `json:"questions"`
	}
	if domain.Decode(raw, &item) != nil || item.Type != "agentMessage" || item.Text == nil || domain.Text(item.ID, "native item identity", 1024, true) != nil || domain.Text(*item.Text, "native message", domain.MaxMessageText, false) != nil || item.Phase != nil && *item.Phase != CommentaryPhase && *item.Phase != FinalAnswerPhase {
		return nil, false, incompatible()
	}
	content := &domain.CodexMessageContent{DeliveryPresent: len(item.Delivery) > 0, QuestionsPresent: len(item.Questions) > 0}
	if content.DeliveryPresent && string(item.Delivery) != "null" {
		var value domain.CodexMessageDelivery
		if json.Unmarshal(item.Delivery, &value) != nil || value != domain.CodexAsyncMessage {
			return nil, false, incompatible()
		}
		content.Delivery = &value
	}
	if content.QuestionsPresent && string(item.Questions) != "null" {
		var questions []json.RawMessage
		if json.Unmarshal(item.Questions, &questions) != nil || questions == nil || len(questions) > 128 {
			return nil, false, incompatible()
		}
		content.Questions = make([]domain.CodexEmbeddedQuestion, 0, len(questions))
		for _, rawQuestion := range questions {
			var value domain.CodexEmbeddedQuestion
			if domain.Decode(rawQuestion, &value) != nil {
				return nil, false, incompatible()
			}
			content.Questions = append(content.Questions, value)
		}
	}
	if !content.DeliveryPresent && !content.QuestionsPresent {
		content = nil
	}
	if content.Validate() != nil {
		return nil, false, incompatible()
	}
	if len(item.MemoryCitation) > 0 && string(item.MemoryCitation) != "null" {
		return nil, true, nil
	}
	// Keep legacy plain publication bytes unchanged for older strict peers.
	// Complete native history still retains omitted/null/empty source fields;
	// every actual async or populated-question observation retains its metadata.
	if content != nil && content.Delivery == nil && len(content.Questions) == 0 {
		content = nil
	}
	return &Message{ID: item.ID, Role: AssistantRole, Text: *item.Text, Phase: item.Phase, Codex: content}, false, nil
}
