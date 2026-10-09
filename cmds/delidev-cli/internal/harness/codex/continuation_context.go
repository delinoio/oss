// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type ContextRecord struct {
	Trigger       CompactionTrigger `json:"trigger"`
	ActionID      domain.ID         `json:"action_id,omitempty"`
	TurnID        domain.ID         `json:"turn_id"`
	LiveItemID    string            `json:"live_item_id"`
	HistoryItemID string            `json:"history_item_id"`
}

type ContinuationContextCheckpoint struct {
	Version       uint32          `json:"version"`
	TurnsCount    uint32          `json:"turns_count"`
	HistoryDigest string          `json:"history_digest"`
	RolloutPath   string          `json:"rollout_path"`
	RolloutDigest string          `json:"rollout_digest"`
	Records       []ContextRecord `json:"records"`
}

func cloneContinuationContext(p *ContinuationContextCheckpoint) *ContinuationContextCheckpoint {
	if p == nil {
		return nil
	}
	copy := *p
	copy.Records = slices.Clone(p.Records)
	return &copy
}

func (p ContinuationContextCheckpoint) validate() error {
	if p.Version != 1 || p.TurnsCount == 0 || p.TurnsCount > maxForkTurns || len(p.Records) == 0 || len(p.Records) > maxTrackedTurns || !contextDigest(p.HistoryDigest) || !contextDigest(p.RolloutDigest) {
		return compactionUncertain()
	}
	seen := map[string]bool{}
	for _, r := range p.Records {
		if r.TurnID.Validate() != nil || domain.Text(r.LiveItemID, "native live context item", 1024, true) != nil || domain.Text(r.HistoryItemID, "native durable context item", 1024, true) != nil || r.Trigger != AutomaticCompaction && r.Trigger != ManualCompaction || r.Trigger == ManualCompaction && r.ActionID.Validate() != nil || r.Trigger == AutomaticCompaction && r.ActionID != "" || seen[r.LiveItemID] || seen["history:"+r.HistoryItemID] {
			return compactionUncertain()
		}
		seen[r.LiveItemID], seen["history:"+r.HistoryItemID] = true, true
	}
	return nil
}
func contextDigest(v string) bool {
	b, e := hex.DecodeString(v)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == v
}

// Verify retained bytes before a replacement can append native metadata.
// This is private evidence; account, process and workspace authority are separate.
func VerifyContinuationContextRollout(ctx context.Context, home string, p ContinuationCheckpoint) error {
	if p.Context == nil {
		return nil
	}
	c := p.Context
	if c.validate() != nil {
		return compactionUncertain()
	}
	digest, err := forkRolloutDigest(ctx, home, c.RolloutPath)
	if err != nil || hex.EncodeToString(digest[:]) != c.RolloutDigest {
		return compactionUncertain()
	}
	return nil
}

func contextMatchesHistory(p ContinuationContextCheckpoint, turns []json.RawMessage) error {
	if p.validate() != nil || uint32(len(turns)) != p.TurnsCount || historyDigest(turns) != p.HistoryDigest {
		return compactionUncertain()
	}
	records := map[string]ContextRecord{}
	for _, r := range p.Records {
		key := string(r.TurnID) + "/" + r.HistoryItemID
		if _, exists := records[key]; exists {
			return compactionUncertain()
		}
		records[key] = r
	}
	count := 0
	for _, raw := range turns {
		var turn turnWire
		if domain.DecodeBounded(raw, &turn, 16<<20) != nil || !turn.Status.terminal() {
			return compactionUncertain()
		}
		for _, rawItem := range turn.Items {
			var item struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			}
			if json.Unmarshal(rawItem, &item) != nil {
				return compactionUncertain()
			}
			if item.Type != "contextCompaction" {
				continue
			}
			key := string(turn.ID) + "/" + item.ID
			if _, exists := records[key]; !exists {
				return compactionUncertain()
			}
			if record := records[key]; record.Trigger == ManualCompaction {
				if verifyCompactionTurn(raw, CompactionRecord{ActionID: record.ActionID, TurnID: record.TurnID, ItemID: record.LiveItemID, HistoryItemID: record.HistoryItemID}) != nil {
					return compactionUncertain()
				}
			}
			delete(records, key)
			count++
		}
	}
	if len(records) != 0 || count != len(p.Records) {
		return compactionUncertain()
	}
	return nil
}

// RetainContinuationContext pins all original automatic/manual context lineage
// when the current settled ordinary execution has used native compaction.
// No history is reconstructed or emitted as new content/accounting observations.
func (c *Client) RetainContinuationContext(ctx context.Context, source ContinuationCheckpoint) (result *ContinuationContextCheckpoint, returned error) {
	phase := "admission"
	defer func() {
		if returned != nil && c.logger != nil {
			c.logger.WarnContext(ctx, "codex_context_retention_uncertain", "owner_id", c.ownerID, "phase", phase, "code", domain.SafeError(returned).Code)
		}
	}()
	if source.validate(ResumeAfterTerminal) != nil {
		return nil, compactionUncertain()
	}
	if err := c.acquireControl(ctx); err != nil {
		return nil, err
	}
	defer func() { <-c.control }()
	s := c.execution
	if s == nil || c.problem != nil || source.Status == TurnCompleted && s.paused || s.active != "" || s.compaction != nil || len(s.pending) != 0 || s.interactions.blocksInput() || len(c.subagents) != 0 || source.ThreadID != c.thread || source.SessionID != s.thread.SessionID || !sameEffectiveSettings(source.Effective, s.settings) {
		return nil, compactionUncertain()
	}
	if len(s.contextOrder) == 0 && s.contextBase == nil {
		return nil, nil
	}
	tracked, known := s.turns[source.TurnID]
	if !known || tracked.Turn.Status != source.Status || tracked.Mode != source.Mode {
		return nil, compactionUncertain()
	}
	if err := c.checkNativeStateLocked(ctx, true); err != nil {
		return nil, err
	}
	phase = "history"
	turns, err := c.compactionTurnsLocked(ctx)
	if err != nil {
		return nil, err
	}
	turn, inputs, err := decodeLatestTurnInputs(marshalForkPage(turns[len(turns)-1:]), c.nativeImageInput)
	if err != nil || turn.ID != source.TurnID || !slices.Equal(inputs, source.Inputs) {
		return nil, compactionUncertain()
	}
	phase = "lineage"
	records := []ContextRecord{}
	if p := s.contextBase; p != nil {
		if p.validate() != nil || int(p.TurnsCount) >= len(turns) || contextMatchesHistory(*p, turns[:p.TurnsCount]) != nil {
			return nil, compactionUncertain()
		}
		records = slices.Clone(p.Records)
	}
	// History has replay-position IDs. Pair ordered live items only within their
	// exact original turn and preserve their independently observed durable IDs.
	current := map[domain.ID][]string{}
	for _, owned := range s.contextOrder {
		state, ok := s.compactionItems[string(owned.TurnID)+"/"+owned.LiveItemID]
		if !ok || state.completedAt == nil || state.observation.Trigger != AutomaticCompaction || state.observation.ActionID != "" || owned.TurnID != source.TurnID {
			return nil, compactionUncertain()
		}
		current[owned.TurnID] = append(current[owned.TurnID], owned.LiveItemID)
	}
	for _, raw := range turns {
		var wire turnWire
		_ = domain.DecodeBounded(raw, &wire, 16<<20)
		live, owned := current[wire.ID]
		if !owned {
			continue
		}
		index := 0
		for _, rawItem := range wire.Items {
			var item struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			}
			_ = json.Unmarshal(rawItem, &item)
			if item.Type != "contextCompaction" {
				continue
			}
			if index >= len(live) {
				return nil, compactionUncertain()
			}
			records = append(records, ContextRecord{Trigger: AutomaticCompaction, TurnID: wire.ID, LiveItemID: live[index], HistoryItemID: item.ID})
			index++
		}
		if index != len(live) {
			return nil, compactionUncertain()
		}
		delete(current, wire.ID)
	}
	if len(current) != 0 {
		return nil, compactionUncertain()
	}
	phase = "rollout"
	wire, err := c.readThreadLocked(ctx, domain.NewID(), c.thread)
	var path string
	if err != nil || !forkableMetadata(wire, source) || json.Unmarshal(wire.Path, &path) != nil {
		return nil, compactionUncertain()
	}
	digest, err := forkRolloutDigest(ctx, c.home, path)
	if err != nil {
		return nil, err
	}
	p := &ContinuationContextCheckpoint{Version: 1, TurnsCount: uint32(len(turns)), HistoryDigest: historyDigest(turns), RolloutPath: path, RolloutDigest: hex.EncodeToString(digest[:]), Records: records}
	if contextMatchesHistory(*p, turns) != nil {
		return nil, compactionUncertain()
	}
	if c.logger != nil {
		c.logger.InfoContext(ctx, "codex_context_history_retained", "owner_id", c.ownerID, "turn_id", source.TurnID, "context_items", len(records), "turns", len(turns))
	}
	return p, nil
}
