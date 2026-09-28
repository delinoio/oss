package grok

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// Original pinned native observations, replacing only private fixture UUIDs.
//
//go:embed testdata/question-tool.json
var questionToolFixture []byte

func questionEvents(t *testing.T) []nativewire.Event {
	t.Helper()
	var rows []fixtureFileRow
	if json.Unmarshal(questionToolFixture, &rows) != nil {
		t.Fatal("invalid question fixture")
	}
	events := make([]nativewire.Event, 0, len(rows))
	for _, row := range rows {
		event := nativewire.Event{Kind: row.Kind, Method: row.Method, Params: row.Params}
		if event.Kind == nativewire.ServerRequest {
			event.ID, event.Token = json.RawMessage(`"original-question"`), domain.NewID()
		}
		events = append(events, event)
	}
	return events
}

func TestQuestionOriginalTwoInteractions(t *testing.T) {
	o, _ := newQuestionObserver(turnFixtureSession, turnFixturePrompt)
	var stages []questionInteractionStage
	requests, completed := 0, 0
	for _, event := range questionEvents(t) {
		fact, err := o.observe(event)
		if err != nil {
			t.Fatal(event.Method, err)
		}
		if v := fact.Interaction; v != nil {
			stages = append(stages, v.Stage)
		}
		if v := fact.Request; v != nil {
			requests++
			v.Questions[0].Question = "changed"
			v.Questions[0].Options[0].Label = "changed"
		}
		if v := fact.Observation; v != nil {
			if v.Phase == fileToolCompleted {
				completed++
			} else {
				v.Questions[0].Question = "changed"
				v.Questions[0].MultiSelect[0] = 'x'
			}
		}
	}
	if requests != 1 || completed != 1 || !reflect.DeepEqual(stages, []questionInteractionStage{questionPermissionPending, questionPermissionResolved, questionAnswerPending, questionAnswerResolved}) {
		t.Fatal("question lost distinct original interaction stages")
	}
}

func TestQuestionInvalidFactsPreserveOriginalState(t *testing.T) {
	for index, original := range questionEvents(t) {
		for _, mutation := range []string{"foreign-session", "unknown", "missing", "alias", "duplicate", "wrong-method", "foreign-tool", "replay"} {
			t.Run(fmt.Sprintf("%d/%s", index, mutation), func(t *testing.T) {
				events := questionEvents(t)
				o, _ := newQuestionObserver(turnFixtureSession, turnFixturePrompt)
				for _, event := range events[:index] {
					if _, err := o.observe(event); err != nil {
						t.Fatal(err)
					}
				}
				event := original
				value := fixtureObject(event.Params)
				switch mutation {
				case "foreign-session":
					value["sessionId"] = domain.NewID()
				case "unknown":
					value["grant"] = true
				case "missing":
					delete(value, "sessionId")
				case "alias":
					if update, ok := value["update"].(map[string]any); ok {
						update["SessionUpdate"] = update["sessionUpdate"]
					} else {
						value["Questions"] = value["questions"]
					}
				case "duplicate":
					event.Params = append([]byte(`{"sessionId":"`+string(turnFixtureSession)+`",`), event.Params[1:]...)
				case "wrong-method":
					event.Method = "foreign/method"
				case "foreign-tool":
					if index == 0 {
						return
					}
					event.Params = []byte(strings.ReplaceAll(string(event.Params), "call_delidev_read", "foreign-tool"))
				case "replay":
					if index == 0 {
						return
					}
					if _, err := o.observe(event); err != nil {
						t.Fatal(err)
					}
				}
				if mutation != "duplicate" && mutation != "foreign-tool" {
					event.Params, _ = json.Marshal(value)
				}
				before := fmt.Sprintf("%#v/%#v/%#v/%d/%d/%t", o.tools, o.arrivals, o.requests, o.bytes, o.lastEvent, o.seenEvent)
				if _, err := o.observe(event); err == nil {
					t.Fatal("accepted changed original question fact")
				}
				after := fmt.Sprintf("%#v/%#v/%#v/%d/%d/%t", o.tools, o.arrivals, o.requests, o.bytes, o.lastEvent, o.seenEvent)
				if before != after {
					t.Fatal("invalid question changed retained facts")
				}
			})
		}
	}
	for omitted := 1; omitted < len(questionEvents(t))-1; omitted++ {
		t.Run(fmt.Sprintf("missing-stage-%d", omitted), func(t *testing.T) {
			o, _ := newQuestionObserver(turnFixtureSession, turnFixturePrompt)
			var err error
			for index, event := range questionEvents(t) {
				if index == omitted {
					continue
				}
				if _, err = o.observe(event); err != nil {
					break
				}
			}
			if err == nil {
				t.Fatal("missing original stage granted completion")
			}
		})
	}
}

func TestQuestionAnswerOriginalKeysAndVariants(t *testing.T) {
	questions := []questionItem{{"Original question?", []questionOption{{"One", "First"}, {"Two", "Second"}}, json.RawMessage(`null`)}}
	valid := []QuestionAnswer{
		{Outcome: QuestionAccepted, Answers: map[string]string{"Original question?": "A custom answer, unchanged."}, Annotations: map[string]QuestionAnnotation{"Original question?": {Notes: "A note"}}},
		{Outcome: QuestionCancelled},
		{Outcome: QuestionSkipInterview},
		{Outcome: QuestionSkipInterview, PartialAnswers: map[string]string{"Original question?": "Two"}},
	}
	for _, answer := range valid {
		body, err := answer.body(questions)
		if err != nil || !json.Valid(body) {
			t.Fatal("original answer rejected", err)
		}
	}
	for _, answer := range []QuestionAnswer{
		{Outcome: QuestionAccepted},
		{Outcome: QuestionAccepted, Answers: map[string]string{"foreign": "One"}},
		{Outcome: QuestionAccepted, Answers: map[string]string{"Original question?": ""}},
		{Outcome: QuestionAccepted, Answers: map[string]string{"Original question?": "One"}, PartialAnswers: map[string]string{}},
		{Outcome: QuestionAccepted, Answers: map[string]string{"Original question?": "One"}, Annotations: map[string]QuestionAnnotation{"foreign": {Notes: "changed"}}},
		{Outcome: QuestionCancelled, Answers: map[string]string{}},
		{Outcome: QuestionSkipInterview, PartialAnswers: map[string]string{"foreign": "One"}},
		{Outcome: "chat_about_this"},
	} {
		if _, err := answer.body(questions); err == nil {
			t.Fatal("foreign, incomplete or unsupported answer accepted")
		}
	}
	questions = append(questions, questions[0])
	if _, err := valid[0].body(questions); err == nil {
		t.Fatal("duplicate question keys lost response identity")
	}
}

func TestQuestionNestedSchemasAndOriginalRequestNamespaces(t *testing.T) {
	events := questionEvents(t)
	original := events[len(events)-3]
	for _, change := range []func(map[string]any){
		func(v map[string]any) { v["mode"] = "plan" },
		func(v map[string]any) { v["questions"] = nil },
		func(v map[string]any) { v["questions"].([]any)[0].(map[string]any)["Question"] = "alias" },
		func(v map[string]any) { delete(v["questions"].([]any)[0].(map[string]any), "multiSelect") },
		func(v map[string]any) { v["questions"].([]any)[0].(map[string]any)["multiSelect"] = "false" },
		func(v map[string]any) {
			v["questions"].([]any)[0].(map[string]any)["options"].([]any)[0].(map[string]any)["Label"] = "alias"
		},
		func(v map[string]any) {
			v["questions"].([]any)[0].(map[string]any)["options"].([]any)[0].(map[string]any)["description"] = nil
		},
		func(v map[string]any) { q := v["questions"].([]any)[0]; v["questions"] = []any{q, q} },
	} {
		v := fixtureObject(original.Params)
		change(v)
		raw, _ := json.Marshal(v)
		if _, err := parseQuestionRequest(raw, turnFixtureSession); err == nil {
			t.Fatal("unknown nested question schema accepted")
		}
	}
	request, err := parseQuestionRequest(original.Params, turnFixtureSession)
	if err != nil {
		t.Fatal(err)
	}
	request.Questions[0].MultiSelect = json.RawMessage(`false`)
	if validateQuestions(request.Questions) != nil {
		t.Fatal("explicit false rejected")
	}
	for _, id := range []json.RawMessage{json.RawMessage(`"7"`), json.RawMessage(`7`)} {
		o, _ := newQuestionObserver(turnFixtureSession, turnFixturePrompt)
		for _, event := range events[:len(events)-3] {
			if _, err := o.observe(event); err != nil {
				t.Fatal(err)
			}
		}
		event := original
		event.ID = id
		if _, err := o.observe(event); err != nil {
			t.Fatal("original request namespace lost", err)
		}
		event.Token = domain.NewID()
		if _, err := o.observe(event); err == nil {
			t.Fatal("native request was reused with a new arrival")
		}
	}
	o, _ := newQuestionObserver(turnFixtureSession, turnFixturePrompt)
	o.bytes = 4 << 20
	if _, err := o.observe(events[0]); err == nil || len(o.tools) != 0 {
		t.Fatal("question observation bound changed state")
	}
}
