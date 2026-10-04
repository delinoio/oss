// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Inventory pins every original native message/part after permitted pruning.
// Conversation histories retain their independent original input/outcome owners;
// native context messages never create another product input or receipt.
type nativeCheckpointContext struct {
	Version   uint32                `json:"version"`
	Records   []NativeContextRecord `json:"records"`
	Pruned    []NativePrunedPart    `json:"pruned"`
	Inventory []HistoryMessage      `json:"inventory"`
}

// InspectCompactionCheckpoint compares retained ordinary lineage against its
// independent source, then pins the last manual action. It never launches or
// reconstructs a missing native observation or once-only command claim.
func InspectCompactionCheckpoint(ctx context.Context, home string, raw []byte, ref CheckpointReference, sourceHome string, sourceRaw []byte, sourceRef CheckpointReference, action domain.ID) (NativeContextRecord, uint32, error) {
	empty := NativeContextRecord{}
	p, err := decodeCheckpoint(raw, ref, home)
	source, sourceErr := decodeCheckpoint(sourceRaw, sourceRef, sourceHome)
	if err != nil || sourceErr != nil || action.Validate() != nil || ref.RequiresResume || sourceRef.RequiresResume || p.Context == nil || len(p.Context.Records) == 0 || InspectReplacementCheckpoint(ctx, home, raw, ref) != nil || InspectReplacementCheckpoint(ctx, sourceHome, sourceRaw, sourceRef) != nil {
		return empty, 0, sessionUncertain()
	}
	left, _ := json.Marshal(checkpointHistories(p))
	right, _ := json.Marshal(checkpointHistories(source))
	if !bytes.Equal(left, right) || ref.CreationRequestID != sourceRef.CreationRequestID || ref.InputRequestID != sourceRef.InputRequestID || ref.SessionID != sourceRef.SessionID || ref.InputID != sourceRef.InputID || ref.PartID != sourceRef.PartID || ref.InputSHA256 != sourceRef.InputSHA256 || ref.HistorySHA256 != sourceRef.HistorySHA256 {
		return empty, 0, sessionUncertain()
	}
	var prior []NativeContextRecord
	if source.Context != nil {
		prior = source.Context.Records
	}
	if len(p.Context.Records) != len(prior)+1 {
		return empty, 0, sessionUncertain()
	}
	oldRecords, _ := json.Marshal(prior)
	newRecords, _ := json.Marshal(p.Context.Records[:len(prior)])
	if len(prior) > 0 && !bytes.Equal(oldRecords, newRecords) {
		return empty, 0, sessionUncertain()
	}
	record := p.Context.Records[len(p.Context.Records)-1]
	if record.ActionID != action || record.Auto || record.Overflow || record.ContinueID != "" || record.InputRequestID != sourceRef.InputRequestID || record.SourceInputID != sourceRef.InputID || !checkpointDigest(record.HistoryDigest) {
		return empty, 0, sessionUncertain()
	}
	return record, uint32(len(p.Context.Records)), nil
}

func checkpointInventory(c nativeCheckpoint) []HistoryMessage {
	var result []HistoryMessage
	if c.Context != nil {
		result = c.Context.Inventory
	} else {
		for _, h := range checkpointHistories(c) {
			result = append(result, h.Messages...)
		}
	}
	result = slices.Clone(result)
	for i := range result {
		result[i].Parts = slices.Clone(result[i].Parts)
	}
	return result
}

func applyContextPruning(inventory []HistoryMessage, pruned []NativePrunedPart) bool {
	for _, p := range pruned {
		found := false
		for i := range inventory {
			if inventory[i].ID == p.MessageID {
				for j := range inventory[i].Parts {
					part := &inventory[i].Parts[j]
					if part.ID == p.ID && part.Kind == ToolPartKind && (part.Digest == p.OriginalSHA256 || part.Digest == p.SHA256) {
						part.Digest = p.SHA256
						found = true
					}
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (s *sessionAPI) retainContext(history HistoryObservation) *nativeCheckpointContext {
	o := s.observer
	o.mu.Lock()
	defer o.mu.Unlock()
	records := []NativeContextRecord{}
	pruned := []NativePrunedPart{}
	var inventory []HistoryMessage
	if s.predecessor != nil {
		inventory = checkpointInventory(*s.predecessor)
		if s.predecessor.Context != nil {
			records = cloneContextRecords(s.predecessor.Context.Records)
			pruned = slices.Clone(s.predecessor.Context.Pruned)
		}
	}
	if len(records)+len(o.contextRecords) == 0 && len(pruned)+len(o.contextPruned) == 0 {
		return nil
	}
	if !o.contextClosed() {
		return nil
	}
	records = append(records, o.contextRecords...)
	if o.contextManual != "" && len(records) > 0 {
		records[len(records)-1].HistoryDigest = history.Digest
	}
	pruned = append(pruned, o.contextPruned...)
	// Original current tool parts already carry the validated final digest. Only
	// earlier input inventories need the independently observed pruning overlay.
	priorPruned := []NativePrunedPart{}
	for _, p := range o.contextPruned {
		for _, m := range inventory {
			if m.ID == p.MessageID {
				priorPruned = append(priorPruned, p)
			}
		}
	}
	if !applyContextPruning(inventory, priorPruned) {
		return nil
	}
	inventory = append(inventory, copyHistoryObservation(history).Messages...)
	return &nativeCheckpointContext{Version: 1, Records: records, Pruned: pruned, Inventory: inventory}
}

func validCheckpointContext(c nativeCheckpoint) bool {
	proof := c.Context
	if proof == nil {
		for _, h := range checkpointHistories(c) {
			for i, m := range h.Messages {
				if i > 0 && m.Role == UserMessageRole {
					return false
				}
				for _, p := range m.Parts {
					if p.Kind == CompactionPartKind {
						return false
					}
				}
			}
		}
		return true
	}
	if proof.Version != 1 || proof.Records == nil || proof.Pruned == nil || len(proof.Records)+len(proof.Pruned) == 0 || len(proof.Records) > 128 || len(proof.Pruned) > maxObservedParts || len(proof.Inventory) < 2 || len(proof.Inventory) > maxObservedMessages {
		return false
	}
	positions := map[string]int{}
	parts := map[string]HistoryPart{}
	partOwners := map[string]string{}
	for i, m := range proof.Inventory {
		if !nativeID(m.ID, "msg") || positions[m.ID] != 0 || !checkpointDigest(m.Digest) || m.Role != UserMessageRole && m.Role != AssistantMessageRole || m.Parts == nil {
			return false
		}
		positions[m.ID] = i + 1
		for _, p := range m.Parts {
			if !nativeID(p.ID, "prt") || parts[p.ID].ID != "" || !checkpointDigest(p.Digest) || !validCheckpointPart(p.Kind) || len(parts) >= maxObservedParts {
				return false
			}
			parts[p.ID] = p
			partOwners[p.ID] = m.ID
		}
	}
	inputs := map[string]domain.ID{}
	conversationMessages := map[string]bool{}
	for _, h := range checkpointHistories(c) {
		inputs[h.InputID] = h.RequestID
		for _, m := range h.Messages {
			conversationMessages[m.ID] = true
			if positions[m.ID] == 0 {
				return false
			}
			actual := proof.Inventory[positions[m.ID]-1]
			if actual.Digest != m.Digest || actual.Role != m.Role || len(actual.Parts) != len(m.Parts) {
				return false
			}
			for i, p := range m.Parts {
				a := actual.Parts[i]
				if a.ID != p.ID || a.Kind != p.Kind || a.Digest != p.Digest && !contextPrunedMatches(proof.Pruned, m.ID, p, a) {
					return false
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, r := range proof.Records {
		if r.Overflow && (!nativeID(r.OverflowAssistantID, "msg") || !nativeID(r.OverflowEventID, "evt") || positions[r.OverflowAssistantID] == 0 || positions[r.OverflowAssistantID] >= positions[r.UserID]) || !r.Overflow && (r.OverflowAssistantID != "" || r.OverflowEventID != "") {
			return false
		}
		if r.InputRequestID.Validate() != nil || inputs[r.SourceInputID] != r.InputRequestID || !nativeID(r.UserID, "msg") || !nativeID(r.SummaryID, "msg") || !nativeID(r.PartID, "prt") || !nativeID(r.CompletedEventID, "evt") || seen[r.UserID] || seen[r.SummaryID] || seen[r.PartID] || seen[r.CompletedEventID] || positions[r.UserID] == 0 || positions[r.SummaryID] <= positions[r.UserID] || proof.Inventory[positions[r.UserID]-1].Role != UserMessageRole || proof.Inventory[positions[r.SummaryID]-1].Role != AssistantMessageRole || len(proof.Inventory[positions[r.UserID]-1].Parts) != 1 || parts[r.PartID].Kind != CompactionPartKind || partOwners[r.PartID] != r.UserID || r.Auto && (r.ActionID != "" || r.HistoryDigest != "") || !r.Auto && (r.ActionID.Validate() != nil || !checkpointDigest(r.HistoryDigest)) || r.TailStartID != "" && positions[r.TailStartID] == 0 || r.ContinueID != "" && (!r.Auto || positions[r.ContinueID] <= positions[r.SummaryID] || proof.Inventory[positions[r.ContinueID]-1].Role != UserMessageRole) {
			return false
		}
		seen[r.UserID], seen[r.SummaryID], seen[r.PartID], seen[r.CompletedEventID] = true, true, true, true
		if r.ContinueID != "" {
			if seen[r.ContinueID] {
				return false
			}
			seen[r.ContinueID] = true
		}
	}
	for _, m := range proof.Inventory {
		if !conversationMessages[m.ID] && !seen[m.ID] {
			return false
		}
		for _, p := range m.Parts {
			if p.Kind == CompactionPartKind && !seen[p.ID] {
				return false
			}
		}
	}
	pruneSeen := map[string]bool{}
	for _, p := range proof.Pruned {
		if pruneSeen[p.ID] || parts[p.ID].Kind != ToolPartKind || partOwners[p.ID] != p.MessageID || parts[p.ID].Digest != p.SHA256 || !checkpointDigest(p.OriginalSHA256) || p.OriginalSHA256 == p.SHA256 || p.Compacted == 0 || p.Compacted > 9007199254740991 {
			return false
		}
		pruneSeen[p.ID] = true
	}
	return true
}

func contextPrunedMatches(pruned []NativePrunedPart, message string, old, next HistoryPart) bool {
	for _, p := range pruned {
		if p.ID == old.ID && p.MessageID == message && p.OriginalSHA256 == old.Digest && p.SHA256 == next.Digest && old.Kind == ToolPartKind {
			return true
		}
	}
	return false
}

func contextInventoryDigest(c nativeCheckpointContext) string {
	raw, _ := json.Marshal(c)
	return mutationDigest(raw)
}

func cloneHistoryInventory(v []HistoryMessage) []HistoryMessage {
	result := slices.Clone(v)
	for i := range result {
		result[i].Parts = slices.Clone(result[i].Parts)
	}
	return result
}
