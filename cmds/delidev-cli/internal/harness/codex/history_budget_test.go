// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const (
	budgetImagePartBytes = 700 << 10
	budgetImageParts     = 7
	budgetPrompt         = "Original budget input"
)

// sevenImagePartTurn is one completed full-view turn whose single protected
// functionCallOutput carries seven 700 KiB input_image parts (about 4.79 MiB).
func sevenImagePartTurn(clientID domain.ID) largeHistoryTurn {
	parts := make([]int, budgetImageParts)
	for n := range parts {
		parts[n] = budgetImagePartBytes
	}
	return largeHistoryTurn{ID: domain.NewID(), Outputs: [][]int{parts}, ImagesOnly: true, ClientID: clientID, Prompt: budgetPrompt, ContextItemID: "original-context"}
}

// padBudgetHistory extends the history to 17 turns with unique item identities;
// the image-bounds fixture expands each failed image prompt to 256 KiB on read.
func padBudgetHistory(t *testing.T, turns []any) []any {
	t.Helper()
	for len(turns) < 17 {
		turn := fixtureTurn(domain.NewID(), TurnCompleted)
		turn["itemsView"] = "full"
		turn["items"] = []any{uniqueHistoryItem(t, generationItem("failed", ""), fmt.Sprintf("budget-image-%d", len(turns)))}
		turns = append(turns, turn)
	}
	return turns
}

func TestHistoryAggregateLimitFollowsNativeFrameProfile(t *testing.T) {
	c := &Client{}
	if got := c.historyAggregateLimit(); got != 4<<20 {
		t.Fatal("default profile aggregate budget changed", got)
	}
	c.imageObservations = true
	if got := c.historyAggregateLimit(); got != 16<<20 {
		t.Fatal("enlarged profile aggregate budget was not profile-aware", got)
	}
}

func TestHistoryBudgetAdmitsSevenImagePartOutputUnderEnlargedProfile(t *testing.T) {
	clientID := domain.NewID()
	spec := sevenImagePartTurn(clientID)
	c, bound := framePaddedBoundFixture(t, []largeHistoryTurn{spec}, 0)
	original := bytes.Repeat([]byte("a"), budgetImagePartBytes)

	turns, err := c.compactionTurnsLocked(context.Background())
	if err != nil || len(turns) != 1 {
		t.Fatal("complete history with a 4.79 MiB protected output was rejected", err)
	}
	if len(turns[0]) <= 4<<20 || len(turns[0]) > c.historyAggregateLimit() {
		t.Fatal("fixture does not exercise the range between the base and enlarged budgets", len(turns[0]))
	}
	if bytes.Count(turns[0], original) != budgetImageParts {
		t.Fatal("original protected bytes changed")
	}
	again, err := c.contextTurnsLocked(context.Background(), "asc", nil, false)
	if err != nil || historyDigest(again) != historyDigest(turns) {
		t.Fatal("context inspection rejected or changed the history", err)
	}
	// Legacy Revert scan joins the same complete full-turn history.
	reverted, err := c.revertHistoryLocked(context.Background(), "asc", nil, nil, false)
	if err != nil || historyDigest(reverted) != historyDigest(turns) {
		t.Fatal("legacy Revert history scan rejected or changed the history", err)
	}
	// A context-bearing continuation scans the same history before verification.
	checkpoint := ContinuationCheckpoint{
		ThreadID: bound.Thread.ID, SessionID: bound.Thread.SessionID, TurnID: spec.ID, Status: TurnCompleted, Mode: domain.ExecuteMode, Effective: *bound.Effective,
		Inputs:  []HistoricalInput{{ID: clientID, PromptDigest: sha256.Sum256([]byte(budgetPrompt))}},
		Context: &ContinuationContextCheckpoint{Version: 1, TurnsCount: 1, HistoryDigest: historyDigest(turns), RolloutPath: "/private/original", RolloutDigest: strings.Repeat("0", 64), Records: []ContextRecord{{Trigger: AutomaticCompaction, TurnID: spec.ID, LiveItemID: "original-live-context", HistoryItemID: "original-context"}}},
	}
	observed, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ContinueAfterSuccess)
	if err != nil || observed.ID != spec.ID {
		t.Fatal("context-bearing continuation rejected the admitted protected output", err)
	}
}

func TestHistoryBudgetKeepsBaseProfileFrameAndAggregateLimits(t *testing.T) {
	c := frameProfileFixture(t, []largeHistoryTurn{sevenImagePartTurn(domain.NewID())})
	// The base profile keeps its one-MiB native frame and 4 MiB aggregate.
	c.imageObservations = false
	if c.historyAggregateLimit() != 4<<20 {
		t.Fatal("base profile aggregate budget widened")
	}
	if _, err := c.compactionTurnsLocked(context.Background()); err == nil {
		t.Fatal("native frame above the selected profile was accepted")
	}
	if _, err := c.revertHistoryLocked(context.Background(), "asc", nil, nil, false); err == nil {
		t.Fatal("legacy Revert scan accepted a frame above the selected profile")
	}
}

func TestHistoryBudgetWidensAcrossPagesWithEnlargedProfile(t *testing.T) {
	// Pages of the image-bounds fixture total more than 4 MiB but far less than
	// 16 MiB: the base profile fails safely while the enlarged profile admits them.
	build := func() (*Client, string) {
		c, capture, _, ids, inputs := revertFixture(t, "image-bounds")
		turns := generatedHistoryTurns(t, ids, inputs, generationItem("failed", ""))
		turns = padBudgetHistory(t, turns)
		setGeneratedHistory(t, c, turns)
		return c, capture
	}
	c, _ := build()
	if _, err := c.contextTurnsLocked(context.Background(), "asc", nil, false); err == nil {
		t.Fatal("base profile aggregate budget was bypassed")
	} else {
		assertCode(t, err, domain.RecoveryRequired)
	}
	c, _ = build()
	c.imageObservations = true
	turns, err := c.contextTurnsLocked(context.Background(), "asc", nil, false)
	if err != nil || len(turns) != 17 {
		t.Fatal("enlarged profile rejected a history within its aggregate budget", err, len(turns))
	}
	total := 0
	for _, turn := range turns {
		total += len(turn)
	}
	if total <= 4<<20 || total > 16<<20 {
		t.Fatal("fixture does not exercise the range between the base and enlarged budgets", total)
	}
}

func TestRevertHistoryJoinBudgetIsExactAtTheLimit(t *testing.T) {
	id := domain.NewID()
	item, _ := json.Marshal(map[string]any{"id": "bounded", "type": "agentMessage", "text": strings.Repeat("x", 1<<10)})
	turn := fixtureTurn(id, TurnCompleted)
	turn["itemsView"], turn["items"] = "full", []json.RawMessage{item}
	raw, _ := json.Marshal(turn)
	turns, entries := []json.RawMessage{raw}, []revertHistoryItem{{TurnID: id, Item: item}}
	joined, err := joinRevertHistory(turns, entries, enlargedNativeFrameBytes)
	if err != nil || len(joined) != 1 {
		t.Fatal(err)
	}
	size := len(joined[0])
	if _, err := joinRevertHistory(turns, entries, size); err != nil {
		t.Fatal("history exactly at the budget was rejected", err)
	}
	_, err = joinRevertHistory(turns, entries, size-1)
	assertCode(t, err, domain.RecoveryRequired)
}

func TestVerifyRevertIntentBudgetIsExactAndProfileCeiled(t *testing.T) {
	c, _, source, ids, inputs := revertFixture(t, "ready")
	var intent RevertIntent
	proof, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[2], ids[2], func(value RevertIntent) error { intent = value; return nil })
	if err != nil || len(intent.ExpectedHistory) != 2 {
		t.Fatal(err)
	}
	size := 0
	for _, raw := range intent.ExpectedHistory {
		size += len(raw)
	}
	if verifyRevertIntentWithin(intent, proof, size) != nil || VerifyRevertIntent(intent, proof) != nil {
		t.Fatal("expected history exactly at the budget was rejected")
	}
	assertCode(t, verifyRevertIntentWithin(intent, proof, size-1), domain.RecoveryRequired)

	// The package verifier accepts at most the enlarged profile budget.
	padded := intent
	padded.ExpectedHistory = append([]json.RawMessage(nil), intent.ExpectedHistory...)
	filler := enlargedNativeFrameBytes - size + 1
	var turn map[string]any
	if json.Unmarshal(padded.ExpectedHistory[0], &turn) != nil {
		t.Fatal("fixture history is not an object")
	}
	turn["padding"] = strings.Repeat("p", filler)
	padded.ExpectedHistory[0], _ = json.Marshal(turn)
	if err := VerifyRevertIntent(padded, proof); err == nil {
		t.Fatal("expected history above the enlarged profile budget was accepted")
	}
}

func TestRevertAcceptsAggregateAboveBaseBudgetUnderEnlargedProfile(t *testing.T) {
	c, capture, source, ids, inputs := revertFixture(t, "image-bounds")
	turns := generatedHistoryTurns(t, ids, inputs, generationItem("failed", ""))
	turns = padBudgetHistory(t, turns)
	setGeneratedHistory(t, c, turns)
	c.imageObservations = true
	var intent RevertIntent
	proof, err := c.RevertThread(context.Background(), domain.NewID(), source, inputs[2], ids[2], func(value RevertIntent) error { intent = value; return nil })
	if err != nil || len(intent.ExpectedHistory) != 2 || len(requestsOf(t, capture, "thread/revert")) != 1 {
		t.Fatal("legacy Revert rejected a history within the enlarged aggregate budget", err)
	}
	if VerifyRevertIntent(intent, proof) != nil {
		t.Fatal("intent verification rejected the retained prefix")
	}
}

func TestRevertScanBudgetCountsRetainedItemsNotPageEnvelopes(t *testing.T) {
	item := func(size int) revertHistoryItem {
		return revertHistoryItem{TurnID: domain.NewID(), Item: json.RawMessage(strings.Repeat("a", size))}
	}
	for _, limit := range []int{4 << 20, 16 << 20} {
		// Item bytes exactly at the budget pass even though every page result
		// carries extra transport structure beyond the retained items.
		budget := revertScanBudget{history: limit}
		const envelope = 8 << 10
		for range limit / (1 << 20) {
			if !budget.admit(1<<20+envelope, []revertHistoryItem{item(1 << 20)}) {
				t.Fatal("page envelopes were counted against the retained-history budget", limit)
			}
		}
		if budget.items != limit {
			t.Fatal("unexpected retained item total", budget.items)
		}
		// One more retained byte fails, as does a separately oversized envelope.
		if budget.admit(1+envelope, []revertHistoryItem{item(1)}) {
			t.Fatal("retained items above the budget were admitted", limit)
		}
	}
	envelopeOnly := revertScanBudget{history: 16 << 20}
	if !envelopeOnly.admit(maxRevertEnvelopeBytes+10, []revertHistoryItem{item(10)}) {
		t.Fatal("envelope at its own bound was rejected")
	}
	if envelopeOnly.admit(1, nil) {
		t.Fatal("transport envelopes above their own bound were admitted")
	}
}
