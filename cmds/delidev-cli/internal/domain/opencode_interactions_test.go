package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestOpenCodeQuestionsRetainOrderedNativeMatrixAndExplicitEmpty(t *testing.T) {
	for _, raw := range []string{`{"text":"","header":"","options":[]}`, `{"text":"original","header":"Choice","options":[{"label":"","description":""},{"label":"First","description":"Original"}],"multiple":true,"custom":false}`} {
		var value OpenCodeQuestion
		if Decode([]byte(raw), &value) != nil {
			t.Fatal("valid original question changed")
		}
		encoded, _ := json.Marshal(value)
		var restored OpenCodeQuestion
		if Decode(encoded, &restored) != nil || !reflect.DeepEqual(value, restored) {
			t.Fatal("native question optional/empty shape changed")
		}
	}
	r := OpenCodeInteractionRequest{Version: OpenCodeProtocolVersion, NativeEventID: "evt_01960dcbe1faABCDEFGHIJKLMN", NativeMessageID: "msg_01960dcbe1faABCDEFGHIJKLMN", CallID: "original", Questions: []OpenCodeQuestion{}}
	encoded, _ := json.Marshal(r)
	var restored OpenCodeInteractionRequest
	if Decode(encoded, &restored) != nil || restored.Questions == nil || restored.Validate(UserQuestionInteraction, InteractionRequestID{Kind: InteractionTextID, Text: "que_01960dcbe1faABCDEFGHIJKLMN"}) != nil {
		t.Fatal("explicit empty native matrix became missing")
	}
	for _, raw := range []string{`null`, `{"text":"x","header":"h","options":null}`, `{"text":"x","header":"h","options":[null]}`, `{"text":"x","header":"h","options":[{"label":null,"description":""}]}`, `{"text":"x","header":"h","options":[],"multiple":null}`, `{"text":"x","header":"h","options":[],"custom":null}`, `{"Text":"x","header":"h","options":[]}`, `{"text":"x","header":"h","options":[],"id":"invented"}`} {
		var q OpenCodeQuestion
		if Decode([]byte(raw), &q) == nil {
			t.Fatalf("malformed native question accepted: %s", raw)
		}
	}
}

func TestOpenCodePermissionPreservesScopeAndRejectsMalformedEvidence(t *testing.T) {
	value := OpenCodePermission{Name: "read", Patterns: []string{"a/*.env", ""}, Always: []string{}, MetadataJSON: `{"exact":9007199254740993}`}
	encoded, _ := json.Marshal(value)
	var restored OpenCodePermission
	if Decode(encoded, &restored) != nil || !reflect.DeepEqual(value, restored) {
		t.Fatal("native permission scope or precision changed")
	}
	for _, raw := range []string{`{"name":"read","patterns":null,"always":[],"metadata_json":"{}"}`, `{"name":"read","patterns":[null],"always":[],"metadata_json":"{}"}`, `{"name":"read","patterns":[],"always":[],"metadata_json":"[]"}`, `{"name":"read","patterns":[],"Always":[],"metadata_json":"{}"}`} {
		if Decode([]byte(raw), &restored) == nil {
			t.Fatal("malformed permission accepted")
		}
	}
	value.Patterns = []string{strings.Repeat("a", 32769)}
	if value.Validate() == nil {
		t.Fatal("oversized native scope accepted")
	}
}
