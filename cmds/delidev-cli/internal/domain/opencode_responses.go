package domain

import "encoding/json"

// The original protocol identifies a question matrix, not individual rows.
// A selected empty string differs from an explicit empty (unanswered) row.
type OpenCodeQuestionResponse struct {
	Answers [][]string `json:"answers"`
}

func (r *OpenCodeQuestionResponse) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Answers [][]*string `json:"answers"`
	}
	var fields map[string]json.RawMessage
	if Decode(raw, &wire) != nil || Decode(raw, &fields) != nil || len(fields) != 1 || fields["answers"] == nil || wire.Answers == nil {
		return invalidQuestionResponse()
	}
	r.Answers = make([][]string, len(wire.Answers))
	for i, row := range wire.Answers {
		if row == nil {
			return invalidQuestionResponse()
		}
		r.Answers[i] = make([]string, len(row))
		for j, value := range row {
			if value == nil {
				return invalidQuestionResponse()
			}
			r.Answers[i][j] = *value
		}
	}
	return nil
}

func (r OpenCodeQuestionResponse) Validate(original *OpenCodeInteractionRequest) error {
	if original == nil || original.Permission != nil || original.Questions == nil || r.Answers == nil || len(r.Answers) != len(original.Questions) {
		return invalidQuestionResponse()
	}
	for i, q := range original.Questions {
		row := r.Answers[i]
		if q.Validate() != nil || row == nil || len(row) > 256 || (q.Multiple == nil || !*q.Multiple) && len(row) > 1 {
			return invalidQuestionResponse()
		}
		seen := map[string]bool{}
		for _, answer := range row {
			if Text(answer, "native question answer", 64<<10, false) != nil || seen[answer] {
				return invalidQuestionResponse()
			}
			seen[answer] = true
			matches := 0
			for _, option := range q.Options {
				if option.Label == answer {
					matches++
				}
			}
			if matches > 1 || matches == 0 && q.Custom != nil && !*q.Custom {
				return invalidQuestionResponse()
			}
		}
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > MaxQuestionResponseBytes {
		return Fail(ResourceExhausted, "The complete native answer matrix exceeds its bound.", "Shorten the response without omitting or reordering original question rows.")
	}
	return nil
}

type OpenCodePermissionDecision string

const OpenCodePermissionOnce OpenCodePermissionDecision = "once"

// This initial response profile grants one original request only. Native
// always/rejection policy and cascading closures require their own coordinator.
type OpenCodePermissionResponse struct {
	Decision OpenCodePermissionDecision `json:"decision"`
}

func (r *OpenCodePermissionResponse) UnmarshalJSON(raw []byte) error {
	type plain OpenCodePermissionResponse
	var value plain
	var fields map[string]json.RawMessage
	if Decode(raw, &value) != nil || Decode(raw, &fields) != nil || len(fields) != 1 || fields["decision"] == nil || value.Decision != OpenCodePermissionOnce {
		return invalidApprovalResponse()
	}
	*r = OpenCodePermissionResponse(value)
	return nil
}

func (r OpenCodePermissionResponse) Validate(original *OpenCodeInteractionRequest) error {
	if original == nil || original.Permission == nil || original.Permission.Validate() != nil || original.Questions != nil || r.Decision != OpenCodePermissionOnce {
		return invalidApprovalResponse()
	}
	return nil
}

func (r QuestionResponseInput) ValidateInteraction(original ExecutionInteraction) error {
	if original.Type != UserQuestionInteraction {
		return invalidQuestionResponse()
	}
	if original.OpenCode == nil {
		return r.Validate(original.Questions)
	}
	if original.Questions != nil || original.Approval != nil || original.OpenCode.Validate(original.Type, original.NativeRequestID) != nil || r.Answers != nil || r.OpenCode == nil {
		return invalidQuestionResponse()
	}
	if err := r.OpenCode.Validate(original.OpenCode); err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > MaxQuestionResponseBytes {
		return Fail(ResourceExhausted, "The complete native response exceeds its bound.", "Reduce the complete response without truncating original choices.")
	}
	return nil
}

func (r ApprovalResponseInput) ValidateInteraction(original ExecutionInteraction) error {
	if original.Type != NativeApprovalInteraction {
		return invalidApprovalResponse()
	}
	if original.OpenCode == nil {
		return r.Validate(original.Approval)
	}
	if original.Questions != nil || original.Approval != nil || original.OpenCode.Validate(original.Type, original.NativeRequestID) != nil || r.Decision != nil || r.Grant != nil || r.OpenCode == nil {
		return invalidApprovalResponse()
	}
	return r.OpenCode.Validate(original.OpenCode)
}
