// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
)

// CodexAsyncMessage is inert provenance, never an interaction or response owner.
// Presence flags retain omitted fields separately from explicit native nulls.
type CodexAsyncMessage struct {
	Version          uint32               `json:"version"`
	DeliveryPresent  bool                 `json:"delivery_present"`
	Delivery         *string              `json:"delivery"`
	QuestionsPresent bool                 `json:"questions_present"`
	Questions        []CodexAsyncQuestion `json:"questions"`
}
type CodexAsyncQuestion struct {
	Title   string   `json:"title"`
	Options []string `json:"options"`
}

func (m *CodexAsyncMessage) Validate() error {
	if m == nil {
		return nil
	}
	if m.Version != 1 || (!m.DeliveryPresent && m.Delivery != nil) || (!m.QuestionsPresent && m.Questions != nil) || m.Delivery != nil && *m.Delivery != "async" || len(m.Questions) > 64 {
		return invalidAsyncMessage()
	}
	size := 0
	for _, q := range m.Questions {
		if Text(q.Title, "async question title", 4096, false) != nil || len(q.Options) > 32 {
			return invalidAsyncMessage()
		}
		size += len(q.Title)
		for _, o := range q.Options {
			if Text(o, "async option", 4096, false) != nil {
				return invalidAsyncMessage()
			}
			size += len(o)
		}
	}
	if size > 65536 {
		return invalidAsyncMessage()
	}
	return nil
}
func invalidAsyncMessage() error {
	return Fail(InvalidArgument, "Invalid asynchronous message metadata.", "Preserve bounded inert native provenance.")
}
func EqualCodexAsyncMessage(a, b *CodexAsyncMessage) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Metadata can arrive at completion after a null initial observation. Existing
// populated fields and presence must never disappear or change.
func CodexAsyncMessageAdvances(a, b *CodexAsyncMessage) bool {
	if a == nil {
		return b.Validate() == nil
	}
	if b == nil || b.Validate() != nil {
		return false
	}
	if a.DeliveryPresent && !b.DeliveryPresent || a.QuestionsPresent && !b.QuestionsPresent {
		return false
	}
	if a.Delivery != nil && (b.Delivery == nil || *a.Delivery != *b.Delivery) {
		return false
	}
	if a.Questions != nil {
		x, _ := json.Marshal(a.Questions)
		y, _ := json.Marshal(b.Questions)
		if string(x) != string(y) {
			return false
		}
	}
	return true
}

func (m *CodexAsyncMessage) UnmarshalJSON(raw []byte) error {
	type wire CodexAsyncMessage
	var fields map[string]json.RawMessage
	if Decode(raw, &fields) != nil || len(fields) != 5 || fields["version"] == nil || fields["delivery_present"] == nil || fields["delivery"] == nil || fields["questions_present"] == nil || fields["questions"] == nil || bytes.Equal(bytes.TrimSpace(fields["delivery_present"]), []byte("null")) || bytes.Equal(bytes.TrimSpace(fields["questions_present"]), []byte("null")) {
		return invalidAsyncMessage()
	}
	var value wire
	if Decode(raw, &value) != nil {
		return invalidAsyncMessage()
	}
	*m = CodexAsyncMessage(value)
	return m.Validate()
}
func (q *CodexAsyncQuestion) UnmarshalJSON(raw []byte) error {
	type wire CodexAsyncQuestion
	var fields map[string]json.RawMessage
	if Decode(raw, &fields) != nil || len(fields) != 2 || fields["title"] == nil || bytes.Equal(bytes.TrimSpace(fields["title"]), []byte("null")) || fields["options"] == nil {
		return invalidAsyncMessage()
	}
	var options []json.RawMessage
	if Decode(fields["options"], &options) != nil {
		return invalidAsyncMessage()
	}
	for _, option := range options {
		if bytes.Equal(bytes.TrimSpace(option), []byte("null")) {
			return invalidAsyncMessage()
		}
	}
	var value wire
	if Decode(raw, &value) != nil {
		return invalidAsyncMessage()
	}
	*q = CodexAsyncQuestion(value)
	return nil
}
