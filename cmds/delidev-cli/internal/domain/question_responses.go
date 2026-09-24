package domain

import (
	"encoding/json"
	"slices"
	"time"
)

const MaxQuestionResponseBytes = 256 << 10

// This document represents explicitly non-secret answers only. Secret-marked
// native questions need a protected native retention/delivery capability and
// must never fall back to this ordinary persisted response shape.
type QuestionResponseInput struct {
	Answers map[string][]string `json:"answers"`
}

func (r *QuestionResponseInput) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Answers map[string][]*string `json:"answers"`
	}
	if Decode(raw, &wire) != nil || wire.Answers == nil {
		return invalidQuestionResponse()
	}
	r.Answers = map[string][]string{}
	for id, values := range wire.Answers {
		if values == nil {
			return invalidQuestionResponse()
		}
		answers := make([]string, 0, len(values))
		for _, value := range values {
			if value == nil {
				return invalidQuestionResponse()
			}
			answers = append(answers, *value)
		}
		r.Answers[id] = answers
	}
	return nil
}

func invalidQuestionResponse() error {
	return Fail(InvalidArgument, "The response does not match its original questions.", "Answer every original question identity with offered options or supported free text, using an explicit empty array for no answer; do not add approval or policy fields.")
}

func (r QuestionResponseInput) Validate(original *QuestionRequest) error {
	if original == nil || original.Validate() != nil || len(r.Answers) != len(original.Questions) {
		return invalidQuestionResponse()
	}
	for _, question := range original.Questions {
		if question.Secret {
			return Fail(Unsupported, "This native question requires protected answer delivery and retention.", "Do not send the secret through ordinary input or response fields; use a validated protected native capability when available.")
		}
		answers, ok := r.Answers[question.ID]
		if !ok || answers == nil || len(answers) > 128 {
			return invalidQuestionResponse()
		}
		seen := map[string]bool{}
		for _, answer := range answers {
			if Text(answer, "question answer", MaxQuestionResponseBytes, true) != nil || seen[answer] {
				return invalidQuestionResponse()
			}
			if len(question.Options) != 0 && !question.Other && !slices.ContainsFunc(question.Options, func(option QuestionOption) bool { return option.Label == answer }) {
				return invalidQuestionResponse()
			}
			seen[answer] = true
		}
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > MaxQuestionResponseBytes {
		return Fail(ResourceExhausted, "The complete question response exceeds its bound.", "Reduce the response without omitting question identities or truncating answers.")
	}
	return nil
}

type QuestionResponseState string

const (
	QuestionResponseQueued    QuestionResponseState = "queued"
	QuestionResponseClaimed   QuestionResponseState = "claimed"
	QuestionResponseUncertain QuestionResponseState = "uncertain"
	QuestionResponseCanceled  QuestionResponseState = "canceled"
)

// A claim authorizes only its original Worker attempt. It does not prove a
// native send, and another process must never adopt it to replay the answer.
type QuestionResponseClaim struct {
	ID         ID        `json:"id"`
	JobID      ID        `json:"job_id"`
	MachineID  ID        `json:"machine_id"`
	InstanceID ID        `json:"instance_id"`
	DeviceID   ID        `json:"device_id"`
	ClaimedAt  time.Time `json:"claimed_at"`
}

// Queued acceptance is a server fact only. Native ownership/delivery and
// semantic acceptance require separate state transitions and evidence.
type QuestionResponse struct {
	ID         ID                     `json:"id"`
	State      QuestionResponseState  `json:"state"`
	Input      QuestionResponseInput  `json:"input"`
	AcceptedAt time.Time              `json:"accepted_at"`
	Claim      *QuestionResponseClaim `json:"claim,omitempty"`
}
