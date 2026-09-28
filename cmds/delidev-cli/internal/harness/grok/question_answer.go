package grok

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type QuestionOutcome string

const (
	QuestionAccepted      QuestionOutcome = "accepted"
	QuestionCancelled     QuestionOutcome = "cancelled"
	QuestionSkipInterview QuestionOutcome = "skip_interview"
)

type QuestionAnnotation struct {
	Notes string `json:"notes"`
}

// Answers preserve the native question-text keys and original answer strings.
// Do not infer selections by splitting free text or parsing rendered results.
type QuestionAnswer struct {
	Outcome        QuestionOutcome
	Answers        map[string]string
	Annotations    map[string]QuestionAnnotation
	PartialAnswers map[string]string
}

func (a QuestionAnswer) body(questions []questionItem) (json.RawMessage, error) {
	if validateQuestions(questions) != nil {
		return nil, incompatible()
	}
	keys := map[string]bool{}
	for _, question := range questions {
		keys[question.Question] = true
	}
	validate := func(answers map[string]string) bool {
		for question, answer := range answers {
			if !keys[question] || !text(answer, 64<<10) {
				return false
			}
		}
		return true
	}
	var value any
	switch a.Outcome {
	case QuestionAccepted:
		if len(a.Answers) != len(keys) || !validate(a.Answers) || a.PartialAnswers != nil {
			return nil, incompatible()
		}
		annotations := a.Annotations
		if annotations == nil {
			annotations = map[string]QuestionAnnotation{}
		}
		for key, annotation := range annotations {
			if !keys[key] || domain.Text(annotation.Notes, "native question notes", 64<<10, false) != nil {
				return nil, incompatible()
			}
		}
		value = struct {
			Outcome     QuestionOutcome               `json:"outcome"`
			Answers     map[string]string             `json:"answers"`
			Annotations map[string]QuestionAnnotation `json:"annotations"`
		}{a.Outcome, a.Answers, annotations}
	case QuestionCancelled:
		if a.Answers != nil || a.Annotations != nil || a.PartialAnswers != nil {
			return nil, incompatible()
		}
		value = struct {
			Outcome QuestionOutcome `json:"outcome"`
		}{a.Outcome}
	case QuestionSkipInterview:
		if a.Answers != nil || a.Annotations != nil || !validate(a.PartialAnswers) {
			return nil, incompatible()
		}
		partial := a.PartialAnswers
		if partial == nil {
			partial = map[string]string{}
		}
		value = struct {
			Outcome QuestionOutcome   `json:"outcome"`
			Answers map[string]string `json:"partial_answers"`
		}{a.Outcome, partial}
	default:
		// The observed chat_about_this response did not forward its content
		// to the model. It needs a separate verified native profile; silently
		// dropping that user text or sending a replacement prompt is unsafe.
		return nil, incompatible()
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 256<<10 {
		return nil, incompatible()
	}
	return raw, nil
}
