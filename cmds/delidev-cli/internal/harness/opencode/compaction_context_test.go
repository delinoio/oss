// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func contextFixtureMessage(n int) string { return fmt.Sprintf("msg_%012xABCDEFGHIJKLMN", n) }
func contextFixturePart(n int) string    { return fmt.Sprintf("prt_%012xABCDEFGHIJKLMN", n) }
func contextFixtureCopy(v map[string]any) map[string]any {
	raw, _ := json.Marshal(v)
	var copy map[string]any
	_ = json.Unmarshal(raw, &copy)
	return copy
}
func contextFixtureFinish(f *observerFixture, n int) {
	start := f.assistantPart(StepStartPartKind, n, nil)
	f.part(start)
	f.part(f.assistantPart(StepFinishPartKind, n+1, map[string]any{"reason": FinishStop, "tokens": f.a["tokens"], "cost": 0}))
	f.a["finish"] = FinishStop
	f.message(f.a)
	f.a["time"].(map[string]any)["completed"] = uint64(1250 + n)
	f.message(f.a)
}
func contextFixtureSummary(f *observerFixture, auto bool) (string, string) {
	user := contextFixtureCopy(f.user)
	userID := contextFixtureMessage(50)
	user["id"] = userID
	f.message(user)
	part := fixturePart(CompactionPartKind, map[string]any{"auto": auto})
	part["id"], part["messageID"] = contextFixturePart(50), userID
	observed := f.part(part)
	if !observed.ContextOnly || auto && (observed.Compaction == nil || observed.Compaction.Stage != domain.NativeCompactionStarted) {
		f.t.Fatal("native compaction did not retain its distinct ordered metadata")
	}
	f.a = fixtureAssistant()
	f.a["id"], f.a["parentID"], f.a["mode"], f.a["agent"], f.a["summary"] = contextFixtureMessage(51), userID, "compaction", "compaction", true
	f.message(f.a)
	contextFixtureFinish(f, 150)
	return userID, part["id"].(string)
}

func TestOriginalAutomaticCompactionOwnsSummaryAndNativeContinuation(t *testing.T) {
	f := newObserverFixture(t)
	f.start()
	f.finish(FinishToolCalls)
	_, partID := contextFixtureSummary(f, true)
	if f.o.snapshot().TerminalObserved {
		t.Fatal("summary alone granted settlement")
	}
	user := contextFixtureCopy(f.user)
	userID := contextFixtureMessage(52)
	user["id"] = userID
	f.message(user)
	part := fixturePart(TextPartKind, map[string]any{"text": "Private native continuation.", "synthetic": true, "metadata": map[string]any{"compaction_continue": true}, "time": map[string]any{"start": 1500, "end": 1500}})
	part["id"], part["messageID"] = contextFixturePart(52), userID
	if !f.part(part).ContextOnly {
		t.Fatal("native continuation became a product input")
	}
	complete := f.observe(SessionCompactedEvent, map[string]any{"sessionID": fixtureSessionID})
	if complete.Compaction == nil || complete.Compaction.NativeItemID != partID || complete.Compaction.Stage != domain.NativeCompactionCompleted {
		t.Fatal("completion lost its independently observed source")
	}
	f.a = fixtureAssistant()
	f.a["id"], f.a["parentID"] = contextFixtureMessage(53), userID
	if f.message(f.a).ContextOnly {
		t.Fatal("ordinary successor output was hidden as context")
	}
	contextFixtureFinish(f, 250)
	f.status(NativeStatusIdle)
	f.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
	if !f.o.snapshot().SettledObserved || len(f.o.contextRecords) != 1 || f.o.contextRecords[0].ContinueID != userID {
		t.Fatal("complete native context did not preserve original input settlement")
	}
	// A completion event cannot be replayed with a fresh envelope identity.
	f.reject(SessionCompactedEvent, map[string]any{"sessionID": fixtureSessionID})
}

func TestNativeCompactionRefusesForeignAndUnsettledSummary(t *testing.T) {
	for _, mode := range []string{"premature", "foreign", "manual", "model", "missing-marker"} {
		t.Run(mode, func(t *testing.T) {
			f := newObserverFixture(t)
			f.start()
			f.finish(FinishToolCalls)
			if mode == "premature" {
				f.reject(SessionCompactedEvent, map[string]any{"sessionID": fixtureSessionID})
				return
			}
			user := contextFixtureCopy(f.user)
			user["id"] = contextFixtureMessage(50)
			if mode == "model" {
				user["model"].(map[string]any)["modelID"] = "foreign-model"
				f.reject(MessageUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "info": user})
				return
			}
			f.message(user)
			p := fixturePart(CompactionPartKind, map[string]any{"auto": mode != "manual"})
			p["id"], p["messageID"] = contextFixturePart(50), user["id"]
			if mode == "manual" {
				f.reject(MessagePartUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "part": p, "time": 1240})
				return
			}
			f.part(p)
			if mode == "foreign" {
				f.reject(SessionCompactedEvent, map[string]any{"sessionID": contextFixtureMessage(90)})
				return
			}
			if mode == "missing-marker" {
				p := fixturePart(TextPartKind, map[string]any{"text": "Private unauthorized followup."})
				p["id"], p["messageID"] = contextFixturePart(51), user["id"]
				f.reject(MessagePartUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "part": p, "time": 1240})
			}
		})
	}
}

func TestNativePruningPermitsOnlyOriginalCompletedMarker(t *testing.T) {
	for _, alter := range []bool{false, true} {
		t.Run(fmt.Sprint(alter), func(t *testing.T) {
			f := newObserverFixture(t)
			f.start()
			f.part(f.assistantPart(StepStartPartKind, 100, nil))
			pending := f.assistantPart(ToolPartKind, 200, map[string]any{"tool": "bash", "callID": "original-call", "state": map[string]any{"status": "pending", "input": map[string]any{}, "raw": "{}"}})
			f.part(pending)
			running := contextFixtureCopy(pending)
			running["state"] = map[string]any{"status": "running", "input": map[string]any{}, "time": map[string]any{"start": 1240}}
			f.part(running)
			done := contextFixtureCopy(running)
			done["state"] = map[string]any{"status": "completed", "input": map[string]any{}, "output": "private original output", "title": "private title", "metadata": map[string]any{}, "time": map[string]any{"start": 1240, "end": 1245}}
			f.part(done)
			compacted := contextFixtureCopy(done)
			state := compacted["state"].(map[string]any)
			state["time"].(map[string]any)["compacted"] = 1250
			if alter {
				state["output"] = "changed output"
				f.reject(MessagePartUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "part": compacted, "time": 1250})
				return
			}
			observed := f.part(compacted)
			if !observed.ContextOnly || len(f.o.contextPruned) != 1 {
				t.Fatal("original pruning lost its private separate proof")
			}
			frozen, _ := observed.Freeze()
			copy, _ := frozen.Thaw()
			if !copy.ContextOnly {
				t.Fatal("deferred pruning became canonical output")
			}
			if _, err := f.o.observe(context.Background(), f.event(MessagePartUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "part": done, "time": 1250})); err == nil {
				t.Fatal("pruning rollback accepted")
			}
		})
	}
}
