package grok

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func mixedFixtureEvents(t *testing.T) []nativewire.Event {
	t.Helper()
	var events []nativewire.Event
	sequence := 1
	for index, group := range [][]nativewire.Event{fileToolEvents(t, true), questionEvents(t), fileToolEvents(t, false)} {
		for _, event := range group {
			event.Params = []byte(strings.ReplaceAll(string(event.Params), "call_delidev_read", fmt.Sprintf("mixed-original-%d", index)))
			v := fixtureObject(event.Params)
			if meta, ok := v["_meta"].(map[string]any); ok {
				meta["eventId"] = fmt.Sprintf("%s-%d", turnFixtureSession, sequence)
				sequence++
			}
			if event.Kind == nativewire.ServerRequest {
				event.ID = json.RawMessage(fmt.Sprintf(`"mixed-request-%d"`, index))
			}
			event.Params, _ = json.Marshal(v)
			events = append(events, event)
		}
	}
	return events
}

func TestMixedToolsPreserveOriginalFamilyFacts(t *testing.T) {
	o, _ := newMixedTools(turnFixtureSession, turnFixturePrompt)
	files, questions, requests := 0, 0, 0
	for _, event := range mixedFixtureEvents(t) {
		fact, err := o.observe(event)
		if err != nil {
			t.Fatal("mixed original fact rejected", event.Method, err)
		}
		if (fact.File == nil) == (fact.Question == nil) {
			t.Fatal("mixed fact acquired two families")
		}
		if fact.File != nil && fact.File.Observation != nil && fact.File.Observation.Phase == fileToolCompleted {
			files++
		}
		if fact.Question != nil && fact.Question.Observation != nil && fact.Question.Observation.Phase == fileToolCompleted {
			questions++
		}
		if event.Kind == nativewire.ServerRequest {
			requests++
		}
	}
	if files != 2 || questions != 1 || requests != 2 || len(o.owners) != 3 || len(o.streaming) != 0 || len(o.arrivals) != 2 {
		t.Fatal("mixed family ownership was lost")
	}
}

func TestMixedToolsRejectCrossFamilyNamespaceReuse(t *testing.T) {
	for _, mutation := range []string{"tool", "arrival", "request", "event"} {
		t.Run(mutation, func(t *testing.T) {
			o, _ := newMixedTools(turnFixtureSession, turnFixturePrompt)
			var first nativewire.Event
			checked := false
			for _, original := range mixedFixtureEvents(t) {
				if first.Token == "" && original.Kind == nativewire.ServerRequest {
					first = original
				}
				v := fixtureObject(original.Params)
				update, _ := v["update"].(map[string]any)
				question := strings.Contains(string(original.Params), "mixed-original-1")
				candidate := original
				mutate := !checked && question && (mutation == "tool" && update["sessionUpdate"] == "tool_call_delta_chunk" || (mutation == "arrival" || mutation == "request") && original.Kind == nativewire.ServerRequest || mutation == "event" && update["sessionUpdate"] == "tool_call")
				if mutate {
					switch mutation {
					case "tool":
						candidate.Params = []byte(strings.ReplaceAll(string(original.Params), "mixed-original-1", "mixed-original-0"))
					case "arrival":
						candidate.Token = first.Token
					case "request":
						candidate.ID = first.ID
					case "event":
						v["_meta"].(map[string]any)["eventId"] = string(turnFixtureSession) + "-1"
						candidate.Params, _ = json.Marshal(v)
					}
					before := fmt.Sprintf("%#v/%#v/%#v/%#v/%d/%d/%#v/%#v", o.owners, o.streaming, o.arrivals, o.requests, o.bytes, o.lastEvent, o.files.tools, o.questions.tools)
					if _, err := o.observe(candidate); err == nil {
						t.Fatal("cross-family identity reuse accepted")
					}
					after := fmt.Sprintf("%#v/%#v/%#v/%#v/%d/%d/%#v/%#v", o.owners, o.streaming, o.arrivals, o.requests, o.bytes, o.lastEvent, o.files.tools, o.questions.tools)
					if before != after {
						t.Fatal("invalid mixed identity changed original evidence")
					}
					checked = true
				}
				if _, err := o.observe(original); err != nil {
					t.Fatal("invalid candidate poisoned original input", err)
				}
			}
			if !checked {
				t.Fatal("cross-family mutation not exercised")
			}
		})
	}
}

func TestMixedToolsShareArgumentIndicesAndBounds(t *testing.T) {
	o, _ := newMixedTools(turnFixtureSession, turnFixturePrompt)
	file := fileToolEvents(t, true)[0]
	question := questionEvents(t)[0]
	question.Params = []byte(strings.ReplaceAll(string(question.Params), "call_delidev_read", "original-question-tool"))
	if _, err := o.observe(file); err != nil {
		t.Fatal(err)
	}
	if _, err := o.observe(question); err == nil || len(o.questions.tools) != 0 {
		t.Fatal("concurrent cross-family stream reused the original index")
	}
	v := fixtureObject(question.Params)
	v["update"].(map[string]any)["tool_index"] = 1
	question.Params, _ = json.Marshal(v)
	if _, err := o.observe(question); err != nil {
		t.Fatal("independent original argument stream rejected", err)
	}
	// A later identity-free chunk must use its one still-streaming index;
	// it cannot borrow a declared tool or the other family's index.
	v["update"].(map[string]any)["arguments_delta"] = " "
	delete(v["update"].(map[string]any), "name")
	delete(v["update"].(map[string]any), "tool_call_id")
	question.Params, _ = json.Marshal(v)
	fact, err := o.observe(question)
	if err != nil || fact.Question == nil || fact.Question.Delta.Update.ID != nil || fact.Question.Delta.Update.Name != nil {
		t.Fatal("identity-free original chunk was not retained", err)
	}
	v["update"].(map[string]any)["tool_index"] = 2
	question.Params, _ = json.Marshal(v)
	if _, err := o.observe(question); err == nil {
		t.Fatal("unowned argument index accepted")
	}
	o.bytes = 4 << 20
	if _, err := o.observe(file); err == nil {
		t.Fatal("mixed cumulative byte bound exceeded")
	}
	bound, _ := newMixedTools(turnFixtureSession, turnFixturePrompt)
	for i := range 128 {
		bound.owners[fmt.Sprintf("original-%d", i)] = fileToolFamily
	}
	if _, err := bound.observe(file); err == nil || len(bound.files.tools) != 0 {
		t.Fatal("combined tool bound mutated a family observer")
	}
}
