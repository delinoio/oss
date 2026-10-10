// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"reflect"
)

type CodexMessageDelivery string

const CodexAsyncMessage CodexMessageDelivery = "async"

type CodexEmbeddedQuestion struct {
	Title   string   `json:"title"`
	Options []string `json:"options"`
}

// Presence and nullable values are separate native observations. Neither field
// is a request, answer receipt or permission to send another input.
type CodexMessageContent struct {
	DeliveryPresent  bool                    `json:"delivery_present"`
	Delivery         *CodexMessageDelivery   `json:"delivery"`
	QuestionsPresent bool                    `json:"questions_present"`
	Questions        []CodexEmbeddedQuestion `json:"questions"`
}

func (c *CodexMessageContent) Validate() error {
	if c == nil {
		return nil
	}
	if !c.DeliveryPresent && !c.QuestionsPresent || !c.DeliveryPresent && c.Delivery != nil || c.Delivery != nil && *c.Delivery != CodexAsyncMessage || !c.QuestionsPresent && c.Questions != nil || len(c.Questions) > 128 {
		return invalidCodexMessage()
	}
	for _, q := range c.Questions {
		if Text(q.Title, "embedded question title", 64<<10, false) != nil || len(q.Options) > 128 {
			return invalidCodexMessage()
		}
		for _, option := range q.Options {
			if Text(option, "embedded question option", 64<<10, false) != nil {
				return invalidCodexMessage()
			}
		}
	}
	b, err := json.Marshal(c)
	if err != nil || len(b) > 256<<10 {
		return invalidCodexMessage()
	}
	return nil
}
func invalidCodexMessage() *Error {
	return Fail(InvalidArgument, "Invalid native message observation.", "Preserve bounded original delivery and embedded questions without response authority.")
}

// Null choices and an empty choice list remain distinct; null list members do
// not become empty-string suggestions through encoding/json defaults.
func (q *CodexEmbeddedQuestion) UnmarshalJSON(raw []byte) error {
	var value struct {
		Title   *string         `json:"title"`
		Options json.RawMessage `json:"options"`
	}
	if Decode(raw, &value) != nil || value.Title == nil || len(value.Options) == 0 {
		return invalidCodexMessage()
	}
	q.Title = *value.Title
	q.Options = nil
	if string(value.Options) != "null" {
		var choices []json.RawMessage
		if json.Unmarshal(value.Options, &choices) != nil || choices == nil || len(choices) > 128 {
			return invalidCodexMessage()
		}
		q.Options = make([]string, 0, len(choices))
		for _, choice := range choices {
			var text *string
			if json.Unmarshal(choice, &text) != nil || text == nil {
				return invalidCodexMessage()
			}
			q.Options = append(q.Options, *text)
		}
	}
	return nil
}
func CloneCodexMessage(c *CodexMessageContent) *CodexMessageContent {
	if c == nil {
		return nil
	}
	copy := *c
	if c.Delivery != nil {
		v := *c.Delivery
		copy.Delivery = &v
	}
	if c.Questions != nil {
		copy.Questions = make([]CodexEmbeddedQuestion, len(c.Questions))
		for i, q := range c.Questions {
			copy.Questions[i] = q
			if q.Options != nil {
				copy.Questions[i].Options = append([]string{}, q.Options...)
			}
		}
	}
	return &copy
}

// Initial streaming omissions/null observations may acquire final metadata.
// Once original delivery or a question is observed, it cannot be replaced.
func (c *CodexMessageContent) CanAdvance(next *CodexMessageContent) bool {
	if c == nil {
		return true
	}
	if next == nil {
		return false
	}
	if c.DeliveryPresent && !next.DeliveryPresent || c.QuestionsPresent && !next.QuestionsPresent || c.Delivery != nil && (next.Delivery == nil || *c.Delivery != *next.Delivery) {
		return false
	}
	if c.Questions != nil {
		if next.Questions == nil || len(next.Questions) < len(c.Questions) {
			return false
		}
		for i, q := range c.Questions {
			if !reflect.DeepEqual(q, next.Questions[i]) {
				return false
			}
		}
	}
	return true
}
func (c *CodexMessageContent) UnmarshalJSON(raw []byte) error {
	var value struct {
		DeliveryPresent  *bool           `json:"delivery_present"`
		Delivery         json.RawMessage `json:"delivery"`
		QuestionsPresent *bool           `json:"questions_present"`
		Questions        json.RawMessage `json:"questions"`
	}
	if DecodeBounded(raw, &value, 256<<10) != nil || value.DeliveryPresent == nil || value.QuestionsPresent == nil || len(value.Delivery) == 0 || len(value.Questions) == 0 {
		return invalidCodexMessage()
	}
	c.DeliveryPresent = *value.DeliveryPresent
	c.QuestionsPresent = *value.QuestionsPresent
	c.Delivery = nil
	c.Questions = nil
	if json.Unmarshal(value.Delivery, &c.Delivery) != nil || json.Unmarshal(value.Questions, &c.Questions) != nil {
		return invalidCodexMessage()
	}
	return c.Validate()
}
