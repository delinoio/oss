// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"testing"
)

func TestAsyncMessageMonotonicMetadata(t *testing.T) {
	a := &CodexAsyncMessage{Version: 1, QuestionsPresent: true}
	b := &CodexAsyncMessage{Version: 1, QuestionsPresent: true, Questions: []CodexAsyncQuestion{{Title: "q", Options: []string{}}}}
	if !CodexAsyncMessageAdvances(a, b) || CodexAsyncMessageAdvances(b, a) || CodexAsyncMessageAdvances(b, nil) {
		t.Fatal("metadata completion changed existing provenance")
	}
	raw, _ := json.Marshal(b)
	var copy CodexAsyncMessage
	if Decode(raw, &copy) != nil || !EqualCodexAsyncMessage(b, &copy) || copy.Questions[0].Options == nil {
		t.Fatal("null and empty option distinction lost")
	}
	for _, raw := range []string{`{"version":1,"delivery_present":null,"delivery":null,"questions_present":false,"questions":null}`, `{"version":1,"delivery_present":false,"delivery":null,"questions_present":true,"questions":[{"title":"q","options":[null]}]}`, `{"version":1,"delivery_present":true,"delivery":"async","questions_present":true,"questions":[],"unknown":true}`, `{"version":1,"delivery_present":true,"delivery":"async","questions_present":true,"questions":[{"title":"q","options":null,"title":"other"}]}`} {
		var m CodexAsyncMessage
		if Decode([]byte(raw), &m) == nil {
			t.Fatal("open or duplicate public profile accepted")
		}
	}
}
