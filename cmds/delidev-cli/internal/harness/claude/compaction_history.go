package claude

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// HistoryCompactionProof pins one already validated boundary and its original
// synthetic summary. No summary text or native private metadata is retained.
type HistoryCompactionProof struct {
	NativeID        string `json:"native_id"`
	LogicalParentID string `json:"logical_parent_id"`
	MetadataSHA256  string `json:"metadata_sha256"`
	SummaryID       string `json:"summary_id"`
	SummarySHA256   string `json:"summary_sha256"`
}

func ObserveMainCompaction(boundary, summary StreamEvent, session domain.ID) (HistoryCompactionProof, error) {
	var envelope struct {
		Type          string          `json:"type"`
		Subtype       string          `json:"subtype"`
		ID            string          `json:"uuid"`
		Session       domain.ID       `json:"session_id"`
		Metadata      json.RawMessage `json:"compact_metadata"`
		LogicalParent string          `json:"logical_parent_uuid"`
	}
	if session.Validate() != nil || boundary.Kind != NativeMessage || boundary.Type != "system" || summary.Kind != NativeMessage || summary.Type != "user" || decodeNativeObject(boundary.Body, &envelope) != nil || envelope.Type != "system" || envelope.Subtype != "compact_boundary" || envelope.Session != session || !nativeUUID(envelope.ID) || !nativeUUID(envelope.LogicalParent) {
		return HistoryCompactionProof{}, historyUncertain()
	}
	metadata, err := decodeCompaction(envelope.Metadata)
	if err != nil {
		return HistoryCompactionProof{}, historyUncertain()
	}
	anchor := ""
	if metadata.Segment != nil {
		anchor = metadata.Segment.Anchor
	}
	if metadata.Messages != nil {
		if anchor != "" && anchor != metadata.Messages.Anchor {
			return HistoryCompactionProof{}, historyUncertain()
		}
		anchor = metadata.Messages.Anchor
	}
	if anchor == "" || anchor == envelope.ID || anchor == envelope.LogicalParent || envelope.ID == envelope.LogicalParent {
		return HistoryCompactionProof{}, historyUncertain()
	}
	if _, err := decodeCompactionSummary(summary.Body, session, &compactionSummaryBinding{boundary: envelope.ID, anchor: anchor}); err != nil {
		return HistoryCompactionProof{}, historyUncertain()
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(summary.Body, &fields)
	// The pinned native summary is forwarded as bare text blocks but stored as
	// a string. This exact equivalence is restricted to a proved summary ID.
	_, summaryDigest, err := historyPayloadDigest(fields["message"], true)
	if err != nil {
		return HistoryCompactionProof{}, err
	}
	return HistoryCompactionProof{NativeID: envelope.ID, LogicalParentID: envelope.LogicalParent, MetadataSHA256: compactionDigest(metadata), SummaryID: anchor, SummarySHA256: hex.EncodeToString(summaryDigest[:])}, nil
}

func compactionDigest(metadata *NativeCompaction) string {
	raw, _ := json.Marshal(metadata)
	digest, _ := streamReplyDigest(raw)
	return hex.EncodeToString(digest[:])
}

// VerifyCompactedMainTranscript retains original conversation provenance and
// separately reports which proved messages remain in native active context.
// Native relinking is applied only to an in-memory index; exact file bytes are
// never rewritten. These observations do not authorize native Resume.
func VerifyCompactedMainTranscript(ctx context.Context, raw []byte, session domain.ID, workspace string, proofs []HistoryMessageProof, compactions []HistoryCompactionProof) (TranscriptObservation, error) {
	if len(compactions) == 0 || len(compactions) > maxStreamIdentities {
		return TranscriptObservation{}, historyUncertain()
	}
	return verifyTranscript(ctx, raw, session, workspace, proofs, nil, compactions)
}

// Stored JSONL uses camel-case keys, unlike the live SDK envelope. Rename only
// this closed verified vocabulary; aliases and unrecognized fields fail.
func storedCompaction(raw json.RawMessage) (*NativeCompaction, error) {
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil || fields == nil {
		return nil, historyUncertain()
	}
	names := map[string]string{"trigger": "trigger", "preTokens": "pre_tokens", "postTokens": "post_tokens", "durationMs": "duration_ms", "cumulativeDroppedTokens": "cumulative_dropped_tokens", "preservedSegment": "preserved_segment", "preservedMessages": "preserved_messages"}
	converted := map[string]json.RawMessage{}
	for key, value := range fields {
		name, ok := names[key]
		if !ok {
			return nil, historyUncertain()
		}
		if key == "preservedSegment" || key == "preservedMessages" {
			var nested map[string]json.RawMessage
			if domain.Decode(value, &nested) != nil || nested == nil {
				return nil, historyUncertain()
			}
			nestedNames := map[string]string{"headUuid": "head_uuid", "anchorUuid": "anchor_uuid", "tailUuid": "tail_uuid"}
			if key == "preservedMessages" {
				nestedNames = map[string]string{"anchorUuid": "anchor_uuid", "uuids": "uuids", "allUuids": "all_uuids"}
			}
			result := map[string]json.RawMessage{}
			for k, v := range nested {
				n, ok := nestedNames[k]
				if !ok {
					return nil, historyUncertain()
				}
				result[n] = v
			}
			value, _ = json.Marshal(result)
		}
		converted[name] = value
	}
	value, _ := json.Marshal(converted)
	return decodeCompaction(value)
}

type historyNode struct {
	parent       string
	role         HistoryRole
	position     int
	replayDigest string
}

type historyCompaction struct {
	proof    HistoryCompactionProof
	metadata *NativeCompaction
	position int
}

func historyReplayDigest(fields map[string]json.RawMessage) (string, error) {
	copy := make(map[string]json.RawMessage, len(fields))
	for k, v := range fields {
		copy[k] = v
	}
	// Compaction re-appends preserved records with a newly assigned native
	// slug. Every other field must match; both exact versions remain file-hashed.
	if raw := copy["slug"]; raw != nil {
		var slug string
		if json.Unmarshal(raw, &slug) != nil || domain.Text(slug, "native history slug", 256, true) != nil {
			return "", historyUncertain()
		}
	}
	delete(copy, "slug")
	raw, _ := json.Marshal(copy)
	digest, err := streamReplyDigest(raw)
	return hex.EncodeToString(digest[:]), err
}

// The SDK's preserved-message/segment operations rebuild context ancestry.
// Check every referenced record against the boundary's original prior chain
// before applying those operations to a separate index.
func relinkCompactedHistory(ctx context.Context, nodes map[string]historyNode, compactions []historyCompaction) (map[string]historyNode, error) {
	// Bound aggregate index work as well as input bytes/records. Repeated native
	// compactions must not turn a small snapshot into unbounded graph work.
	steps := 0
	advance := func() error {
		steps++
		if steps > 16*maxHistoryRecords {
			return historyUncertain()
		}
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		return nil
	}
	active := make(map[string]historyNode, len(nodes))
	for id, node := range nodes {
		if err := advance(); err != nil {
			return nil, err
		}
		if node.role == "system" {
			node.parent = ""
		}
		active[id] = node
	}
	for _, boundary := range compactions {
		if err := ctx.Err(); err != nil {
			return nil, domain.SafeError(err)
		}
		proof, meta := boundary.proof, boundary.metadata
		prior := map[string]int{}
		for id := proof.LogicalParentID; id != ""; id = active[id].parent {
			if err := advance(); err != nil {
				return nil, err
			}
			if prior[id] != 0 || nodes[id].role == "" || nodes[id].position >= boundary.position {
				return nil, historyUncertain()
			}
			prior[id] = len(prior) + 1
		}
		anchor := proof.SummaryID
		if nodes[anchor].parent != proof.NativeID || nodes[anchor].position <= boundary.position {
			return nil, historyUncertain()
		}
		var ids []string
		if meta.Messages != nil {
			if meta.Messages.Anchor != anchor {
				return nil, historyUncertain()
			}
			ids = meta.Messages.IDs
			for _, id := range meta.Messages.AllIDs {
				if prior[id] == 0 || nodes[id].position >= boundary.position {
					return nil, historyUncertain()
				}
			}
		} else if meta.Segment != nil {
			for id := meta.Segment.Tail; id != ""; id = active[id].parent {
				if err := advance(); err != nil {
					return nil, err
				}
				if len(ids) >= len(nodes) {
					return nil, historyUncertain()
				}
				ids = append(ids, id)
				if id == meta.Segment.Head {
					break
				}
			}
			slices.Reverse(ids)
		}
		if len(ids) == 0 {
			return nil, historyUncertain()
		}
		if meta.Segment != nil && (meta.Segment.Anchor != anchor || meta.Segment.Head != ids[0] || meta.Segment.Tail != ids[len(ids)-1]) {
			return nil, historyUncertain()
		}
		lastPosition := len(prior) + 1
		for _, id := range ids {
			if prior[id] == 0 || nodes[id].position >= boundary.position || prior[id] >= lastPosition {
				return nil, historyUncertain()
			}
			lastPosition = prior[id]
		}
		previous := anchor
		for _, id := range ids {
			n := active[id]
			n.parent = previous
			active[id] = n
			previous = id
		}
		for id, n := range active {
			if err := advance(); err != nil {
				return nil, err
			}
			if id != ids[0] && n.parent == anchor {
				n.parent = previous
				active[id] = n
			}
		}
		n := active[proof.NativeID]
		n.parent = ""
		active[proof.NativeID] = n
	}
	return active, nil
}
