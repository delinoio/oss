package grok

import (
	"encoding/json"
	"testing"
)

func queueFixture(text string, stage int) []byte {
	value := map[string]any{"sessionId": turnFixtureSession, "entries": []any{}}
	if stage == 0 {
		value["entries"] = []any{map[string]any{"id": turnFixturePrompt, "version": 0, "kind": "prompt", "text": text, "position": 0}}
	}
	if stage == 1 {
		value["runningPromptId"] = turnFixturePrompt
		value["runningText"] = text
		value["runningKind"] = "prompt"
	}
	raw, _ := json.Marshal(value)
	return raw
}

func TestOriginalQueueBindingRejectsReplacementAndPrematureClear(t *testing.T) {
	const original = "Keep original 한글 input."
	for stage := 0; stage < 3; stage++ {
		q := inputQueue{session: turnFixtureSession, text: original}
		for previous := 0; previous < stage; previous++ {
			if err := q.observe(queueFixture(original, previous)); err != nil {
				t.Fatal(err)
			}
		}
		for _, wrong := range []struct {
			text  string
			stage int
		}{{"foreign", stage}, {original, 2}, {original, 0}} {
			if wrong.text == original && wrong.stage == stage {
				continue
			}
			if stage == 2 && wrong.stage == 2 {
				continue
			}
			before := q
			if err := q.observe(queueFixture(wrong.text, wrong.stage)); err == nil {
				t.Fatal("invalid queue transition accepted", stage, wrong.stage)
			}
			if q != before {
				t.Fatal("rejection changed original queue evidence")
			}
		}
		if err := q.observe(queueFixture(original, stage)); err != nil {
			t.Fatal(err)
		}
		before := q
		if err := q.observe(queueFixture(original, stage)); err == nil || q != before {
			t.Fatal("duplicate native transition replaced original evidence")
		}
	}
	q := inputQueue{session: turnFixtureSession, text: original}
	if err := q.observe(queueFixture(original, 1)); err == nil {
		t.Fatal("unclaimed running prompt accepted")
	}
	if err := q.observe(queueFixture(original, 0)); err != nil {
		t.Fatal(err)
	}
	raw := queueFixture(original, 1)
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	value["runningPromptId"] = nil
	raw, _ = json.Marshal(value)
	before := q
	if err := q.observe(raw); err == nil || q != before {
		t.Fatal("null running identity accepted")
	}
}
