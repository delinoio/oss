// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCodexMessageShapesRoundTripAndProgress(t *testing.T) {
	delivery := CodexAsyncMessage
	original := &CodexMessageContent{DeliveryPresent: true, Delivery: &delivery, QuestionsPresent: true, Questions: []CodexEmbeddedQuestion{{Title: "first", Options: nil}, {Title: "empty", Options: []string{}}, {Title: "last", Options: []string{"one", "two"}}}}
	raw, _ := json.Marshal(original)
	var retained CodexMessageContent
	if Decode(raw, &retained) != nil || !reflect.DeepEqual(original, &retained) {
		t.Fatal("native order or nullable/empty metadata changed")
	}
	start := &CodexMessageContent{DeliveryPresent: true, QuestionsPresent: true}
	if !start.CanAdvance(original) || original.CanAdvance(start) {
		t.Fatal("observed populated metadata was erased")
	}
	changed := CloneCodexMessage(original)
	changed.Questions[0].Options = []string{}
	if original.CanAdvance(changed) {
		t.Fatal("original null choices became an empty list")
	}
	changed = CloneCodexMessage(original)
	changed.Delivery = nil
	if original.CanAdvance(changed) {
		t.Fatal("original async provenance disappeared")
	}
}
func TestCodexMessageRejectsMalformedPublicationJSON(t *testing.T) {
	for _, raw := range []string{`{}`, `{"delivery_present":true,"delivery":"async","questions_present":true,"questions":[{"title":"question","options":[null]}]}`, `{"delivery_present":false,"delivery":"async","questions_present":false,"questions":null}`, `{"delivery_present":true,"delivery":"async","questions_present":true,"questions":[{"title":"question"}]}`, `{"delivery_present":true,"delivery":"async","questions_present":true,"questions":[{"title":"question","options":null,"answer_id":"foreign"}]}`} {
		var observed CodexMessageContent
		if Decode([]byte(raw), &observed) == nil {
			t.Fatal("malformed native observation acquired publication authority")
		}
	}
}
