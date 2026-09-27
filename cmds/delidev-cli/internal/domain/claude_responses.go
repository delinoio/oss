package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type ClaudeReplyBehavior string

const (
	ClaudeReplyAllow ClaudeReplyBehavior = "allow"
	ClaudeReplyDeny  ClaudeReplyBehavior = "deny"
)

// Answers retain original question text keys and native string values. An
// explicit empty map skips every question; it is different from no answer map.
// This boundary cannot edit tool input or apply suggested permission updates.
type ClaudePermissionResponse struct {
	Behavior  ClaudeReplyBehavior `json:"behavior"`
	Answers   map[string]string   `json:"answers,omitempty"`
	Message   *string             `json:"message,omitempty"`
	Interrupt *bool               `json:"interrupt,omitempty"`
}

func (r *ClaudePermissionResponse) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Behavior  ClaudeReplyBehavior `json:"behavior"`
		Answers   map[string]*string  `json:"answers,omitempty"`
		Message   *string             `json:"message,omitempty"`
		Interrupt *bool               `json:"interrupt,omitempty"`
	}
	var fields map[string]json.RawMessage
	if Decode(raw, &wire) != nil || Decode(raw, &fields) != nil || fields["behavior"] == nil {
		return invalidClaudeResponse()
	}
	for _, raw := range fields {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return invalidClaudeResponse()
		}
	}
	value := ClaudePermissionResponse{Behavior: wire.Behavior, Message: wire.Message, Interrupt: wire.Interrupt}
	if wire.Answers != nil {
		value.Answers = map[string]string{}
		for key, answer := range wire.Answers {
			if answer == nil {
				return invalidClaudeResponse()
			}
			value.Answers[key] = *answer
		}
	}
	*r = value
	return nil
}

func (r ClaudePermissionResponse) MarshalJSON() ([]byte, error) {
	var answers *map[string]string
	if r.Answers != nil {
		answers = &r.Answers
	}
	return json.Marshal(struct {
		Behavior  ClaudeReplyBehavior `json:"behavior"`
		Answers   *map[string]string  `json:"answers,omitempty"`
		Message   *string             `json:"message,omitempty"`
		Interrupt *bool               `json:"interrupt,omitempty"`
	}{r.Behavior, answers, r.Message, r.Interrupt})
}

func invalidClaudeResponse() error {
	return Fail(InvalidArgument, "The response does not match the original Claude request.", "Keep original question text keys and choose one explicit original allow or deny response without changing tool input or permissions.")
}

func (r ClaudePermissionResponse) Validate(original ExecutionInteraction) error {
	o := original.Claude
	if o == nil || original.OpenCode != nil || original.OpenCodeStop != nil || original.OpenCodeClosure != nil || original.Questions != nil || original.Approval != nil || o.Validate(original.Type, original.NativeRequestID, original.NativeItemID) != nil {
		return invalidClaudeResponse()
	}
	switch r.Behavior {
	case ClaudeReplyAllow:
		if r.Message != nil || r.Interrupt != nil {
			return invalidClaudeResponse()
		}
		if o.Kind == ClaudeUserQuestion {
			questions, err := o.Questions()
			if err != nil || r.Answers == nil || len(r.Answers) > len(questions) {
				return invalidClaudeResponse()
			}
			keys := map[string]bool{}
			for _, q := range questions {
				keys[q.Text] = true
			}
			for key, answer := range r.Answers {
				if !keys[key] || Text(answer, "native answer", MaxQuestionResponseBytes, false) != nil {
					return invalidClaudeResponse()
				}
			}
		} else if r.Answers != nil {
			return invalidClaudeResponse()
		}
	case ClaudeReplyDeny:
		if r.Interrupt != nil && *r.Interrupt {
			return Fail(Unsupported, "Claude denial with interruption needs its original interruption-context adapter.", "Keep this request pending, send a non-interrupting denial, or use the separate original Stop control.")
		}
		if r.Answers != nil || r.Message == nil || Text(*r.Message, "native denial", 4096, true) != nil {
			return invalidClaudeResponse()
		}
	default:
		return invalidClaudeResponse()
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > MaxQuestionResponseBytes {
		return Fail(ResourceExhausted, "The complete Claude response exceeds its bound.", "Shorten the original answer or denial without truncating it.")
	}
	return nil
}

// ClaudeResponseDigest independently binds the exact native callback reply.
// Keep JSON number spellings intact; original input never passes through float64.
func ClaudeResponseDigest(original ExecutionInteraction, r ClaudePermissionResponse) (string, error) {
	if err := r.Validate(original); err != nil {
		return "", err
	}
	var body any
	if r.Behavior == ClaudeReplyAllow {
		input := json.RawMessage(original.Claude.InputJSON)
		if original.Claude.Kind == ClaudeUserQuestion {
			var fields map[string]json.RawMessage
			if Decode(input, &fields) != nil {
				return "", invalidClaudeResponse()
			}
			fields["answers"], _ = json.Marshal(r.Answers)
			input, _ = json.Marshal(fields)
		}
		body = struct {
			Behavior ClaudeReplyBehavior `json:"behavior"`
			Input    json.RawMessage     `json:"updatedInput"`
		}{r.Behavior, input}
	} else {
		body = struct {
			Behavior  ClaudeReplyBehavior `json:"behavior"`
			Message   string              `json:"message"`
			Interrupt bool                `json:"interrupt,omitempty"`
		}{r.Behavior, *r.Message, r.Interrupt != nil && *r.Interrupt}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", invalidClaudeResponse()
	}
	var canonical any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&canonical) != nil {
		return "", invalidClaudeResponse()
	}
	raw, err = json.Marshal(canonical)
	if err != nil {
		return "", invalidClaudeResponse()
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// Echo is transport evidence only, separate from semantic answer/approval
// acceptance, original tool result, callback cancellation and input outcome.
type ClaudeReplyEcho struct {
	ArrivalID  ID     `json:"arrival_id"`
	BodyDigest string `json:"body_sha256"`
	Sequence   uint64 `json:"sequence"`
}
type ExecutionClaudeReplyEcho struct {
	InteractionID ID     `json:"interaction_id"`
	ResponseID    ID     `json:"response_id"`
	ClaimID       ID     `json:"claim_id"`
	ArrivalID     ID     `json:"arrival_id"`
	NativeItemID  string `json:"native_item_id"`
	BodyDigest    string `json:"body_sha256"`
}

func (u ExecutionClaudeReplyEcho) Validate() error {
	for _, id := range []ID{u.InteractionID, u.ResponseID, u.ClaimID, u.ArrivalID} {
		if id.Validate() != nil {
			return invalidClaudeResponse()
		}
	}
	raw, err := hex.DecodeString(u.BodyDigest)
	if err != nil || len(raw) != sha256.Size || hex.EncodeToString(raw) != u.BodyDigest || Text(u.NativeItemID, "native callback tool", 1024, true) != nil {
		return invalidClaudeResponse()
	}
	return nil
}
