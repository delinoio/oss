// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
	"reflect"
)

const CodexAsyncMessagesV1 WorkerCapability = "codex-async-messages-v1"

type CodexMessageDelivery string

const CodexAsyncDelivery CodexMessageDelivery = "async"

type CodexEmbeddedQuestion struct {
	Title   string    `json:"title"`
	Options *[]string `json:"options"`
}

func (q CodexEmbeddedQuestion) Validate() error {
	if Text(q.Title, "embedded question title", 16<<10, false) != nil {
		return invalidAssistantMetadata()
	}
	if q.Options != nil {
		if *q.Options == nil || len(*q.Options) > 128 {
			return invalidAssistantMetadata()
		}
		for _, option := range *q.Options {
			if Text(option, "embedded question option", 16<<10, false) != nil {
				return invalidAssistantMetadata()
			}
		}
	}
	return nil
}
func (q *CodexEmbeddedQuestion) UnmarshalJSON(raw []byte) error {
	type plain CodexEmbeddedQuestion
	var value plain
	var fields map[string]json.RawMessage
	if Decode(raw, &value) != nil || Decode(raw, &fields) != nil || len(fields["title"]) == 0 || bytes.Equal(bytes.TrimSpace(fields["title"]), []byte("null")) || len(fields["options"]) == 0 {
		return invalidAssistantMetadata()
	}
	// encoding/json otherwise coerces a null string-array entry to "".
	// Native options are strings; preserve that strict distinction.
	if !bytes.Equal(bytes.TrimSpace(fields["options"]), []byte("null")) {
		var options []json.RawMessage
		if Decode(fields["options"], &options) != nil {
			return invalidAssistantMetadata()
		}
		for _, option := range options {
			var original string
			if bytes.Equal(bytes.TrimSpace(option), []byte("null")) || json.Unmarshal(option, &original) != nil {
				return invalidAssistantMetadata()
			}
		}
	}
	*q = CodexEmbeddedQuestion(value)
	return q.Validate()
}

// Presence is separate from nullable values. JSON keeps native omission, null,
// empty arrays and ordered values; embedded questions have no reply identity.
type CodexAssistantMetadata struct {
	DeliveryPresent  bool
	Delivery         *CodexMessageDelivery
	QuestionsPresent bool
	Questions        *[]CodexEmbeddedQuestion
}

func invalidAssistantMetadata() error {
	return Fail(InvalidArgument, "Invalid native assistant metadata.", "Preserve original bounded delivery and ordered nullable question observations.")
}
func (m CodexAssistantMetadata) MarshalJSON() ([]byte, error) {
	fields := map[string]any{}
	if m.DeliveryPresent {
		fields["delivery"] = m.Delivery
	}
	if m.QuestionsPresent {
		fields["questions"] = m.Questions
	}
	return json.Marshal(fields)
}
func (m *CodexAssistantMetadata) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if DecodeBounded(raw, &fields, 256<<10) != nil || fields == nil {
		return invalidAssistantMetadata()
	}
	var value CodexAssistantMetadata
	for key, raw := range fields {
		switch key {
		case "delivery":
			value.DeliveryPresent = true
			if json.Unmarshal(raw, &value.Delivery) != nil {
				return invalidAssistantMetadata()
			}
		case "questions":
			value.QuestionsPresent = true
			if DecodeBounded(raw, &value.Questions, 256<<10) != nil {
				return invalidAssistantMetadata()
			}
		default:
			return invalidAssistantMetadata()
		}
	}
	*m = value
	return m.Validate()
}
func (m *CodexAssistantMetadata) Validate() error {
	if m == nil {
		return nil
	}
	if !m.DeliveryPresent && !m.QuestionsPresent || !m.DeliveryPresent && m.Delivery != nil || !m.QuestionsPresent && m.Questions != nil || m.Delivery != nil && *m.Delivery != CodexAsyncDelivery {
		return invalidAssistantMetadata()
	}
	if m.Questions != nil {
		if *m.Questions == nil || len(*m.Questions) > 128 {
			return invalidAssistantMetadata()
		}
		for _, q := range *m.Questions {
			if q.Validate() != nil {
				return invalidAssistantMetadata()
			}
		}
	}
	raw, err := json.Marshal(m)
	if err != nil || len(raw) > 256<<10 {
		return invalidAssistantMetadata()
	}
	return nil
}
func (m *CodexAssistantMetadata) Populated() bool {
	return m != nil && (m.Delivery != nil || m.Questions != nil && len(*m.Questions) > 0)
}
func (m *CodexAssistantMetadata) Clone() *CodexAssistantMetadata {
	if m == nil {
		return nil
	}
	v := *m
	if m.Delivery != nil {
		d := *m.Delivery
		v.Delivery = &d
	}
	if m.Questions != nil {
		qs := make([]CodexEmbeddedQuestion, len(*m.Questions))
		copy(qs, *m.Questions)
		for i, q := range qs {
			if q.Options != nil {
				options := append([]string{}, (*q.Options)...)
				qs[i].Options = &options
			}
		}
		v.Questions = &qs
	}
	return &v
}
func SameCodexAssistantMetadata(a, b *CodexAssistantMetadata) bool { return reflect.DeepEqual(a, b) }

// Completion may add native evidence, but cannot replace an observed value or
// reorder/remove an already observed question. Started and completed facts stay distinct.
func CodexAssistantMetadataExtends(first, last *CodexAssistantMetadata) bool {
	if first.Validate() != nil || last.Validate() != nil {
		return false
	}
	if first == nil {
		return true
	}
	if last == nil {
		return false
	}
	if first.DeliveryPresent && !last.DeliveryPresent || first.QuestionsPresent && !last.QuestionsPresent {
		return false
	}
	if first.Delivery != nil && (last.Delivery == nil || *first.Delivery != *last.Delivery) {
		return false
	}
	if first.Questions != nil && len(*first.Questions) > 0 {
		if last.Questions == nil || len(*last.Questions) < len(*first.Questions) {
			return false
		}
		for i, q := range *first.Questions {
			if !reflect.DeepEqual(q, (*last.Questions)[i]) {
				return false
			}
		}
	}
	return true
}

type CodexAssistantContent struct {
	Started   *CodexAssistantMetadata `json:"started,omitempty"`
	Completed *CodexAssistantMetadata `json:"completed,omitempty"`
}

func (c *CodexAssistantContent) UnmarshalJSON(raw []byte) error {
	type plain CodexAssistantContent
	var value plain
	if DecodeBounded(raw, &value, 512<<10) != nil || value.Started == nil && value.Completed == nil || value.Started.Validate() != nil || value.Completed.Validate() != nil || value.Completed != nil && !CodexAssistantMetadataExtends(value.Started, value.Completed) {
		return invalidAssistantMetadata()
	}
	*c = CodexAssistantContent(value)
	return nil
}
