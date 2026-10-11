// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"testing"
)

func TestCodexAssistantPreservesOriginalOptionalStates(t *testing.T) {
	for _, raw := range []string{`{"delivery":null}`, `{"questions":null}`, `{"questions":[]}`, `{"delivery":"async","questions":[{"title":"Original","options":null},{"title":"Empty","options":[]},{"title":"Choices","options":["B","A"]}]}`} {
		var metadata CodexAssistantMetadata
		if Decode([]byte(raw), &metadata) != nil {
			t.Fatal("original shape rejected", raw)
		}
		restored, _ := json.Marshal(metadata)
		if string(restored) != raw || !SameCodexAssistantMetadata(&metadata, metadata.Clone()) {
			t.Fatal("original state changed", string(restored))
		}
	}
	for _, raw := range []string{`{}`, `{"delivery":"sync"}`, `{"questions":{}}`, `{"questions":[{"title":"q"}]}`, `{"questions":[{"title":null,"options":null}]}`, `{"questions":[{"title":"q","options":[null]}]}`, `{"questions":[{"title":"q","options":null,"extra":true}]}`, `{"delivery":"async","delivery":null}`} {
		var metadata CodexAssistantMetadata
		if Decode([]byte(raw), &metadata) == nil {
			t.Fatal("malformed metadata accepted", raw)
		}
	}
}
func TestCodexAssistantCompletionCannotReplaceOriginalEvidence(t *testing.T) {
	var first, last CodexAssistantMetadata
	_ = Decode([]byte(`{"delivery":"async","questions":[{"title":"q","options":["A","B"]}]}`), &first)
	_ = Decode([]byte(`{"delivery":"async","questions":[{"title":"q","options":["A","B"]},{"title":"new","options":null}]}`), &last)
	if !CodexAssistantMetadataExtends(&first, &last) {
		t.Fatal("native enrichment rejected")
	}
	(*last.Questions)[0].Options = nil
	if CodexAssistantMetadataExtends(&first, &last) {
		t.Fatal("nullable options were replaced")
	}
	clone := first.Clone()
	(*clone.Questions)[0].Title = "changed"
	if (*first.Questions)[0].Title != "q" {
		t.Fatal("caller changed original observation")
	}
}
