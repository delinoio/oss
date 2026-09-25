package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func responseQuestions() *QuestionRequest {
	return &QuestionRequest{Blocking: true, Questions: []Question{
		{ID: "choice", Header: "Choice", Text: "Choose", Options: []QuestionOption{{Label: "First", Description: "First"}, {Label: "Second", Description: "Second"}}},
		{ID: "text", Header: "Text", Text: "Explain", Other: true},
	}}
}

func TestQuestionResponsePreservesExplicitAnswers(t *testing.T) {
	for _, raw := range []string{
		`{"answers":{"choice":["Second","First"],"text":["  Exact 답변\n"]}}`,
		`{"answers":{"choice":[],"text":[]}}`,
	} {
		var value QuestionResponseInput
		if err := Decode([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		if err := value.Validate(responseQuestions()); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var roundTrip QuestionResponseInput
		if Decode(encoded, &roundTrip) != nil || !reflect.DeepEqual(value, roundTrip) || roundTrip.Answers["text"] == nil {
			t.Fatal("response changed exact text/order or lost explicit empty answers")
		}
	}
}

func TestQuestionResponseRejectsMalformedAndCrossKindInput(t *testing.T) {
	for _, raw := range []string{
		`null`, `{}`, `{"answers":null}`, `{"answers":[]}`,
		`{"answers":{"choice":null,"text":[]}}`,
		`{"answers":{"choice":[null],"text":[]}}`,
		`{"answers":{"choice":["First"],"text":[]},"decision":"accept"}`,
		`{"answers":{"choice":["First"]}}`,
		`{"answers":{"choice":["First"],"foreign":[]}}`,
		`{"answers":{"choice":["First","First"],"text":[]}}`,
		`{"answers":{"choice":["Unlisted"],"text":[]}}`,
		`{"answers":{"choice":[],"text":[""]}}`,
		`{"answers":{"choice":[],"text":["invalid\u0000answer"]}}`,
	} {
		var value QuestionResponseInput
		if Decode([]byte(raw), &value) == nil && value.Validate(responseQuestions()) == nil {
			t.Fatalf("invalid response was accepted: %s", raw)
		}
	}
}

func TestQuestionResponseBoundsAndProtectedQuestions(t *testing.T) {
	questions := responseQuestions()
	input := QuestionResponseInput{Answers: map[string][]string{"choice": {"First"}, "text": {strings.Repeat("x", MaxQuestionResponseBytes)}}}
	if err := input.Validate(questions); SafeError(err).Code != ResourceExhausted {
		t.Fatalf("complete encoded response bound was not enforced: %v", err)
	}
	input.Answers["text"] = []string{"Private fixture answer"}
	questions.Questions[1].Secret = true
	if err := input.Validate(questions); SafeError(err).Code != Unsupported {
		t.Fatalf("secret request acquired ordinary persisted answer authority: %v", err)
	}
	questions.Questions[1].Secret = false
	questions.Questions[0].Other = true
	input.Answers["choice"] = []string{"Explicit free text"}
	if err := input.Validate(questions); err != nil {
		t.Fatal(err)
	}
}
