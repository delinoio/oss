package opencode

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFrozenObservationPreservesPrivateOriginalAgainstCallerMutation(t *testing.T) {
	f := newObserverFixture(t)
	f.start()
	f.part(f.assistantPart(StepStartPartKind, 100, nil))
	observation := f.part(f.assistantPart(TextPartKind, 101, map[string]any{"text": "Original private text", "time": map[string]any{"start": 1236, "end": 1240}, "metadata": map[string]any{"exact": json.Number("9007199254740993")}}))
	frozen, err := observation.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	observation.Part.Text.Text = "Changed by caller"
	observation.Part.Text.Metadata[0] = '!'
	*observation.Part.Text.Timing.End = 9
	for range 2 {
		copy, err := frozen.Thaw()
		if err != nil || copy.Part.Text.Text != "Original private text" || *copy.Part.Text.Timing.End != 1240 || !strings.Contains(string(copy.Part.Text.Metadata), "9007199254740993") {
			t.Fatal("deferred original payload was mutated or normalized", err)
		}
		copy.Part.Text.Text = "Another caller"
		copy.Part.Text.Metadata[0] = '!'
	}
	raw, err := json.Marshal(frozen)
	if err != nil || string(raw) != "{}" {
		t.Fatal("frozen native payload became serializable diagnostic data")
	}
	if _, err := (Observation{EventID: observation.EventID, Kind: observation.Kind}).Freeze(); err == nil {
		t.Fatal("caller-created observation acquired original deferred authority")
	}
}
