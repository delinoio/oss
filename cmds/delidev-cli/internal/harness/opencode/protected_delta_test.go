package opencode

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

const protectedDeltaSecret = "fixture-delta-secret/+é"

func protectedDeltaForms() []string {
	secret := []byte(protectedDeltaSecret)
	return []string{protectedDeltaSecret, base64.StdEncoding.EncodeToString(secret), base64.RawStdEncoding.EncodeToString(secret), base64.URLEncoding.EncodeToString(secret), base64.RawURLEncoding.EncodeToString(secret)}
}

func protectedDeltaPart(t *testing.T, f *observerFixture, kind PartKind, number int) map[string]any {
	t.Helper()
	part := f.assistantPart(kind, number, map[string]any{"text": "", "time": map[string]any{"start": 1236}})
	f.part(part)
	return part
}

func protectedDeltaEvent(f *observerFixture, part map[string]any, text string) map[string]any {
	return map[string]any{"sessionID": fixtureSessionID, "messageID": f.a["id"], "partID": part["id"], "field": "text", "delta": text}
}

func TestProtectedValueSplitAcrossPartDeltasIsRefusedBeforePublication(t *testing.T) {
	for _, kind := range []PartKind{TextPartKind, ReasoningPartKind} {
		for formIndex, form := range protectedDeltaForms() {
			body := "ok " + form + " end"
			for split := 1; split < len(body); split++ {
				if !utf8.RuneStart(body[split]) {
					continue
				}
				f := newProtectedObserverFixture(t, protectedDeltaSecret)
				f.start()
				f.part(f.assistantPart(StepStartPartKind, 99, nil))
				part := protectedDeltaPart(t, f, kind, 100)
				var published strings.Builder
				refused := false
				for _, piece := range []string{body[:split], body[split:]} {
					result, err := f.o.observe(t.Context(), f.event(MessagePartDeltaEvent, protectedDeltaEvent(f, part, piece)))
					if err != nil {
						refused = true
						break
					}
					published.WriteString(result.Delta.Text)
					thawed, thawErr := result.frozen.Thaw()
					if thawErr != nil || thawed.Delta.Text != result.Delta.Text {
						t.Fatal("deferred publication exposed text other than the guarded release", thawErr)
					}
				}
				if !refused {
					t.Fatalf("%s form %d split %d was not refused", kind, formIndex, split)
				}
				if strings.Contains(published.String(), form) || !strings.HasPrefix(body, published.String()) {
					t.Fatalf("%s form %d split %d published a reconstructible or reordered sequence", kind, formIndex, split)
				}
				if !f.o.snapshot().NeedsRecovery {
					t.Fatal("refusal did not latch reconciliation")
				}
				for _, state := range f.o.parts {
					if state.guard.Held() != 0 {
						t.Fatal("refusal retained a private holdback")
					}
				}
			}
		}
	}
}

func TestPartDeltaHoldbackKeepsSafeTextOrderedAndFinalSnapshotCloses(t *testing.T) {
	f := newProtectedObserverFixture(t, protectedDeltaSecret)
	f.start()
	f.part(f.assistantPart(StepStartPartKind, 99, nil))
	part := protectedDeltaPart(t, f, TextPartKind, 100)
	pieces := []string{"Harmless ", "fixture-delta ", "text ", "fixture-", "delta-", "other ", "fixture-delta-sec"}
	var published strings.Builder
	for _, piece := range pieces {
		result := f.observe(MessagePartDeltaEvent, protectedDeltaEvent(f, part, piece))
		published.WriteString(result.Delta.Text)
		if held := f.o.parts[part["id"].(string)].guard.Held(); held >= len(protectedDeltaSecret) {
			t.Fatalf("holdback exceeded the longest protected form: %d", held)
		}
	}
	full := strings.Join(pieces, "")
	state := f.o.parts[part["id"].(string)]
	if state.text != full || !strings.HasPrefix(full, published.String()) || published.String() == full || state.guard.Held() != len("fixture-delta-sec") {
		t.Fatal("observer lost the exact original text or released the unresolved suffix")
	}
	part["text"] = full
	part["time"].(map[string]any)["end"] = 1240
	f.part(part)
	if state = f.o.parts[part["id"].(string)]; state.guard.Held() != 0 || state.text != full {
		t.Fatal("final snapshot kept or released a delta holdback")
	}
}

func TestPartDeltaGuardStateIsScopedToExactPart(t *testing.T) {
	f := newProtectedObserverFixture(t, protectedDeltaSecret)
	f.start()
	f.part(f.assistantPart(StepStartPartKind, 99, nil))
	first := protectedDeltaPart(t, f, TextPartKind, 100)
	second := protectedDeltaPart(t, f, ReasoningPartKind, 101)
	f.observe(MessagePartDeltaEvent, protectedDeltaEvent(f, first, "tail fixture-delta-"))
	result := f.observe(MessagePartDeltaEvent, protectedDeltaEvent(f, second, "secret/+é continues"))
	if result.Delta.Text != "secret/+é continues" {
		t.Fatal("an independent part inherited another part's guard state")
	}
}

func TestPartSnapshotExtendsMatchingWithoutRepublishingHoldback(t *testing.T) {
	f := newProtectedObserverFixture(t, protectedDeltaSecret)
	f.start()
	f.part(f.assistantPart(StepStartPartKind, 99, nil))
	part := protectedDeltaPart(t, f, TextPartKind, 100)
	first := f.observe(MessagePartDeltaEvent, protectedDeltaEvent(f, part, "start fixture-delta-"))
	if !strings.HasPrefix("start fixture-delta-", first.Delta.Text) || first.Delta.Text == "start fixture-delta-" {
		t.Fatal("expected a private suffix", first.Delta.Text)
	}
	// A verified open snapshot lets its consumer publish the complete text.
	part["text"] = "start fixture-delta-"
	f.part(part)
	state := f.o.parts[part["id"].(string)]
	if state.guard.Held() != 0 {
		t.Fatal("snapshot-published suffix stayed withheld and would be duplicated")
	}
	// The published suffix still participates in matching.
	if _, err := f.o.observe(t.Context(), f.event(MessagePartDeltaEvent, protectedDeltaEvent(f, part, "secret/+é"))); err == nil {
		t.Fatal("snapshot-published text no longer participated in matching")
	}
}

func TestReconciliationCopyOwnsIndependentPartGuard(t *testing.T) {
	f := newProtectedObserverFixture(t, protectedDeltaSecret)
	f.start()
	f.part(f.assistantPart(StepStartPartKind, 99, nil))
	part := protectedDeltaPart(t, f, TextPartKind, 100)
	f.observe(MessagePartDeltaEvent, protectedDeltaEvent(f, part, "x fixture-delta-"))
	f.o.mu.Lock()
	copy := f.o.reconciliationCopy()
	f.o.mu.Unlock()
	id := part["id"].(string)
	before := f.o.parts[id].guard.Held()
	copy.discardHoldbacks()
	if copy.parts[id].guard.Held() != 0 || f.o.parts[id].guard.Held() != before || before == 0 {
		t.Fatal("rehearsal shared holdback state with the original observer")
	}
	if !reflect.DeepEqual(copy.protected, f.o.protected) {
		t.Fatal("rehearsal lost protected forms")
	}
}

func TestUnprotectedPartDeltasAreNeverDelayed(t *testing.T) {
	f := newObserverFixture(t)
	f.start()
	f.part(f.assistantPart(StepStartPartKind, 99, nil))
	part := protectedDeltaPart(t, f, TextPartKind, 100)
	if result := f.observe(MessagePartDeltaEvent, protectedDeltaEvent(f, part, "fixture-delta-")); result.Delta.Text != "fixture-delta-" {
		t.Fatal("unconfigured protection delayed a delta")
	}
}
