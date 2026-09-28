package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenCodeReadRetainsEmptyDirectoryAndExactCounters(t *testing.T) {
	raw := []byte(`{"kind":"opencode-read","status":"completed","changes":null,"read":{"call_id":"original","input":{"filePath":"/original","offset":0,"limit":0},"title":"","output":"","time":{"start":0,"end":0},"metadata":{"preview":"","truncated":false,"loaded":[],"display":{"type":"directory","path":"/original","entries":[],"offset":0,"totalEntries":0,"truncated":false}}}}`)
	var value ToolSnapshot
	if Decode(raw, &value) != nil || value.Validate() != nil {
		t.Fatal("empty native Read evidence was rejected")
	}
	encoded, err := json.Marshal(value)
	var retained ToolSnapshot
	if err != nil || Decode(encoded, &retained) != nil || retained.Validate() != nil || retained.Read.Metadata.Display.Entries == nil || retained.Read.Metadata.Loaded == nil || retained.Read.Input.Offset == nil || *retained.Read.Input.Offset != 0 {
		t.Fatal("Read roundtrip invented absent/empty values")
	}
	for _, replacement := range []string{"9007199254740992", "-1", "0.5"} {
		changed := strings.Replace(string(raw), `"offset":0`, `"offset":`+replacement, 1)
		var bad ToolSnapshot
		if Decode([]byte(changed), &bad) == nil && bad.Validate() == nil {
			t.Fatal("Read accepted an inexact native counter")
		}
	}
}

func TestOpenCodeReadDoesNotBroadenOtherToolEventProfiles(t *testing.T) {
	raw := []byte(`{"kind":"command","status":"pending","changes":null}`)
	var value ToolSnapshot
	if Decode(raw, &value) == nil && value.Validate() == nil {
		t.Fatal("pending Read state broadened command semantics")
	}
	value = ToolSnapshot{Kind: OpenCodeReadTool, Status: ToolPending, Read: &OpenCodeReadObservation{CallID: "original"}}
	if value.Validate() == nil {
		t.Fatal("missing native proposal became empty raw text")
	}
	text := ""
	value.Read.Raw = &text
	update := ExecutionToolUpdate{ID: NewID(), NativeID: "original", Snapshot: &value}
	if update.Validate(ExecutionToolStarted) != nil || update.Validate(ExecutionToolCompleted) == nil {
		t.Fatal("Read proposal acquired terminal authority")
	}
	value.Status = ToolCompleted
	if value.Validate() == nil {
		t.Fatal("missing successful result was inferred")
	}
}
