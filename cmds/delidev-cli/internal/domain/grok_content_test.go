package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func grokTextFixture() (string, GrokTextUpdate) {
	thread := string(NewID())
	return thread, GrokTextUpdate{ID: NewID(), ResponseOrdinal: 1, Text: "Original 한글", Metadata: GrokTextMetadata{EventID: thread + "-10", ChunkID: "1", ContextTokens: "18446744073709551615", TimestampMS: "1", StreamStartMS: "0", TurnStartMS: "0"}}
}
func grokUsageFixture(n uint32) GrokResponseUsage {
	return GrokResponseUsage{Ordinal: n, Counts: GrokResponseCounts{"11", "5", "0", "0", "0"}}
}

func TestGrokContentPreservesOriginalOrderingAndExactCounters(t *testing.T) {
	thread, text := grokTextFixture()
	state, err := (GrokContentState{}).ObserveText(text, thread)
	if err != nil {
		t.Fatal(err)
	}
	text.Metadata.EventID, text.Metadata.ChunkID, text.Metadata.ContextTokens = thread+"-12", "3", "0"
	state, err = state.ObserveText(text, thread)
	if err != nil {
		t.Fatal(err)
	}
	state, err = state.ObserveResponse(grokUsageFixture(1))
	if err != nil || state.MessageID != "" || state.Responses != 1 || state.TextBytes != uint32(2*len(text.Text)) {
		t.Fatal(state, err)
	}
	// Responses may have no text, and identical counts never identify a response.
	state, err = state.ObserveResponse(grokUsageFixture(2))
	if err != nil || state.Responses != 2 {
		t.Fatal(state, err)
	}
	text.ID, text.ResponseOrdinal, text.Metadata.EventID, text.Metadata.ChunkID = NewID(), 3, thread+"-20", "8"
	if _, err = state.ObserveText(text, thread); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(text)
	var copy GrokTextUpdate
	if Decode(encoded, &copy) != nil || !reflect.DeepEqual(text, copy) || !strings.Contains(string(encoded), `"context_tokens":"0"`) {
		t.Fatal("exact Resource JSON changed")
	}
}

func TestGrokContentRejectsInvalidAndMixedFactsWithoutMutation(t *testing.T) {
	for _, mutation := range []string{"event-prefix", "event-alias", "event-overflow", "chunk-zero", "chunk-alias", "counter-overflow", "timestamp", "missing", "utf8", "ordinal"} {
		t.Run(mutation, func(t *testing.T) {
			thread, v := grokTextFixture()
			switch mutation {
			case "event-prefix":
				v.Metadata.EventID = string(NewID()) + "-10"
			case "event-alias":
				v.Metadata.EventID = thread + "-01"
			case "event-overflow":
				v.Metadata.EventID = thread + "-18446744073709551616"
			case "chunk-zero":
				v.Metadata.ChunkID = "0"
			case "chunk-alias":
				v.Metadata.ChunkID = "01"
			case "counter-overflow":
				v.Metadata.ContextTokens = "18446744073709551616"
			case "timestamp":
				v.Metadata.TimestampMS = "253402300800000"
			case "missing":
				v.Metadata.TurnStartMS = ""
			case "utf8":
				v.Text = string([]byte{255})
			case "ordinal":
				v.ResponseOrdinal = 2
			}
			state := GrokContentState{}
			next, err := state.ObserveText(v, thread)
			if err == nil || next != state {
				t.Fatal("invalid observation changed state")
			}
		})
	}
	thread, v := grokTextFixture()
	state, err := (GrokContentState{}).ObserveText(v, thread)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"duplicate", "event", "chunk", "message", "size", "response"} {
		t.Run(mutation, func(t *testing.T) {
			copy := v
			copy.Metadata.EventID = thread + "-11"
			copy.Metadata.ChunkID = "2"
			switch mutation {
			case "duplicate":
				copy = v
			case "event":
				copy.Metadata.EventID = v.Metadata.EventID
			case "chunk":
				copy.Metadata.ChunkID = v.Metadata.ChunkID
			case "message":
				copy.ID = NewID()
			case "size":
				copy.Text = strings.Repeat("a", MaxMessageText)
			case "response":
				copy.ResponseOrdinal = 2
			}
			next, err := state.ObserveText(copy, thread)
			if err == nil || next != state {
				t.Fatal("invalid transition changed state")
			}
		})
	}
	for _, value := range []string{"", "01", "-1", "1e3", "18446744073709551616"} {
		u := grokUsageFixture(1)
		u.Counts.Input = value
		if u.Validate() == nil {
			t.Fatal("invalid usage accepted", value)
		}
	}
	for _, ordinal := range []uint32{0, 2, 129} {
		if next, err := state.ObserveResponse(grokUsageFixture(ordinal)); err == nil || next != state {
			t.Fatal("invalid response changed state")
		}
	}
	for _, kind := range []ExecutionEventKind{ExecutionGrokTextObserved, ExecutionGrokUsageObserved} {
		e := ExecutionEvent{Version: 1, ExecutionID: NewID(), Sequence: 3, Kind: kind, NativeThreadID: thread, NativeTurnID: "e5833c4a-d764-4428-8bd8-6c2968a34b1b"}
		if kind == ExecutionGrokTextObserved {
			e.GrokText = &v
		} else {
			u := grokUsageFixture(1)
			e.GrokUsage = &u
			e.ObservationID = NewID()
		}
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
		e.Notice = NativeWarning
		if e.Validate() == nil {
			t.Fatal("mixed payload accepted")
		}
	}
}

func TestGrokContentBoundsRetainPriorEvidence(t *testing.T) {
	thread, v := grokTextFixture()
	for _, s := range []GrokContentState{{MessageChunks: 1024}, {TextBytes: 4 << 20}, {Responses: 128}} {
		next, err := s.ObserveText(v, thread)
		if err == nil || next != s {
			t.Fatal("full original content retention accepted more data")
		}
	}
	s := GrokContentState{Responses: 128}
	if next, err := s.ObserveResponse(grokUsageFixture(129)); err == nil || next != s {
		t.Fatal("response retention overflow")
	}
}
