package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func copyHistoryRecord(record map[string]any) map[string]any {
	raw, _ := json.Marshal(record)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	return result
}

func appendCompactedHistory(t *testing.T, session domain.ID, workspace string, records []map[string]any, proofs []HistoryMessageProof, parent string, preserved []string) ([]map[string]any, []HistoryMessageProof, HistoryCompactionProof) {
	t.Helper()
	boundary, summary, output := string(domain.NewID()), string(domain.NewID()), string(domain.NewID())
	metadata := map[string]any{"trigger": AutomaticCompaction, "preTokens": 123, "postTokens": 12, "durationMs": 1, "cumulativeDroppedTokens": 111, "preservedSegment": map[string]any{"headUuid": preserved[0], "anchorUuid": summary, "tailUuid": preserved[len(preserved)-1]}, "preservedMessages": map[string]any{"anchorUuid": summary, "uuids": preserved, "allUuids": preserved}}
	base := map[string]any{"sessionId": session, "cwd": workspace, "version": SupportedVersion, "isSidechain": false}
	boundaryRecord := copyHistoryRecord(base)
	boundaryRecord["type"], boundaryRecord["subtype"], boundaryRecord["uuid"], boundaryRecord["parentUuid"], boundaryRecord["logicalParentUuid"], boundaryRecord["compactMetadata"] = "system", "compact_boundary", boundary, nil, parent, metadata
	summaryRecord := copyHistoryRecord(base)
	summaryRecord["type"], summaryRecord["uuid"], summaryRecord["parentUuid"], summaryRecord["isCompactSummary"], summaryRecord["isVisibleInTranscriptOnly"] = "user", summary, boundary, true, true
	summaryRecord["message"] = map[string]any{"role": "user", "content": "Private native compaction summary."}
	outputRecord := copyHistoryRecord(base)
	outputRecord["type"], outputRecord["uuid"], outputRecord["parentUuid"] = "assistant", output, summary
	outputRecord["message"] = map[string]any{"role": "assistant", "id": "msg_" + output, "model": "fixture-model", "content": []any{map[string]any{"type": "text", "text": "Private post-compaction output."}}}
	rawMeta, _ := json.Marshal(metadata)
	meta, err := storedCompaction(rawMeta)
	if err != nil {
		t.Fatal(err)
	}
	boundaryRaw, _ := json.Marshal(map[string]any{"type": "system", "subtype": "compact_boundary", "uuid": boundary, "session_id": session, "logical_parent_uuid": parent, "compact_metadata": meta})
	summaryRaw, _ := json.Marshal(map[string]any{"type": "user", "uuid": summary, "session_id": session, "parent_tool_use_id": nil, "timestamp": "2026-09-26T00:00:00Z", "isSynthetic": true, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "Private native compaction summary."}}}})
	proof, err := ObserveMainCompaction(StreamEvent{Kind: NativeMessage, Type: "system", Body: boundaryRaw}, StreamEvent{Kind: NativeMessage, Type: "user", Body: summaryRaw}, session)
	if err != nil {
		t.Fatal(err)
	}
	outputRaw, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": output, "session_id": session, "parent_tool_use_id": nil, "message": outputRecord["message"]})
	outputProof, err := ObserveMainHistoryMessage(StreamEvent{Kind: NativeMessage, Type: "assistant", Body: outputRaw}, session)
	if err != nil {
		t.Fatal(err)
	}
	records = append(records, boundaryRecord, summaryRecord, outputRecord, map[string]any{"type": "last-prompt", "sessionId": session, "leafUuid": output})
	return records, append(proofs, outputProof), proof
}

func compactedHistoryFixture(t *testing.T) (domain.ID, string, []map[string]any, []HistoryMessageProof, []HistoryCompactionProof) {
	t.Helper()
	session, workspace, records, proofs := historyFixture(t)
	replay := copyHistoryRecord(records[3])
	replay["slug"] = "native-compaction-slug"
	records = append(records, replay)
	records, proofs, compact := appendCompactedHistory(t, session, workspace, records, proofs, proofs[1].NativeID, []string{proofs[1].NativeID})
	return session, workspace, records, proofs, []HistoryCompactionProof{compact}
}

func TestCompactedHistorySeparatesRetainedConversationFromActiveContext(t *testing.T) {
	session, workspace, records, proofs, compactions := compactedHistoryFixture(t)
	raw := historyJSONL(t, records)
	unchanged := bytes.Clone(raw)
	observed, err := VerifyCompactedMainTranscript(context.Background(), raw, session, workspace, proofs, compactions)
	if err != nil || observed.MatchedMessages != 3 || observed.ActiveMatchedMessages != 2 || observed.CompactedMessages != 1 || observed.AdditionalMessages != 0 || observed.Compactions != 1 || observed.SummaryMessages != 1 || observed.ReplayedRecords != 1 || !bytes.Equal(raw, unchanged) {
		t.Fatal("compaction did not retain distinct original and context evidence", err, observed)
	}
	if _, err := VerifyMainTranscript(context.Background(), raw, session, workspace, proofs); err == nil {
		t.Fatal("unobserved compaction granted main history authority")
	}
	encoded, _ := json.Marshal(compactions)
	if bytes.Contains(encoded, []byte("Private")) || bytes.Contains(encoded, []byte(workspace)) {
		t.Fatal("compaction proof exposed private bytes")
	}
}

func TestCompactedHistoryMultipleBoundariesUsePriorNativeContextOrder(t *testing.T) {
	session, workspace, records, proofs, compactions := compactedHistoryFixture(t)
	// A previous summary precedes an older preserved message in native context,
	// although it was written later in the append-only source file.
	records = append(records, copyHistoryRecord(records[7]))
	records, proofs, next := appendCompactedHistory(t, session, workspace, records, proofs, proofs[2].NativeID, []string{compactions[0].SummaryID, proofs[1].NativeID, proofs[2].NativeID})
	compactions = append(compactions, next)
	observed, err := VerifyCompactedMainTranscript(context.Background(), historyJSONL(t, records), session, workspace, proofs, compactions)
	if err != nil || observed.Compactions != 2 || observed.SummaryMessages != 2 || observed.MatchedMessages != 4 || observed.ActiveMatchedMessages != 3 || observed.CompactedMessages != 1 || observed.ReplayedRecords != 2 {
		t.Fatal("later compaction lost original provenance or prior context order", err, observed)
	}
}

func TestCompactedHistoryRetainsNativeBatchedReplaysOutsidePreservedContext(t *testing.T) {
	for _, replay := range []bool{false, true} {
		session, workspace, records, proofs, compactions := compactedHistoryFixture(t)
		if replay {
			// A pending native write batch may re-append the older input too,
			// even though it is absent from the preserved active message list.
			records[5] = copyHistoryRecord(records[1])
			records[5]["slug"] = "native-late-slug"
		} else {
			records = append(records[:5], records[6:]...)
		}
		observed, err := VerifyCompactedMainTranscript(context.Background(), historyJSONL(t, records), session, workspace, proofs, compactions)
		if err != nil || observed.CompactedMessages != 1 || observed.ActiveMatchedMessages != 2 || (observed.ReplayedRecords == 1) != replay {
			t.Fatal("batched persistence changed compaction meaning", err, observed)
		}
	}
}

func TestCompactedHistoryRejectsChangedMissingForeignAndUnprovedRecords(t *testing.T) {
	for _, name := range []string{"metadata", "logical-parent", "boundary-parent", "summary-text", "summary-role", "summary-parent", "summary-marker", "summary-visibility", "summary-fields", "duplicate-boundary", "duplicate-summary", "duplicate-proof", "no-proof", "missing-summary", "changed-replay", "changed-replay-parent", "bad-slug", "undeclared-replay", "trailing-replay", "repeated-replay", "detached-leaf", "case-alias", "unknown-metadata", "summary-as-input", "empty-compactions", "partial", "canceled"} {
		t.Run(name, func(t *testing.T) {
			session, workspace, records, proofs, compactions := compactedHistoryFixture(t)
			boundary, summary := records[6], records[7]
			ctx := context.Background()
			switch name {
			case "metadata":
				boundary["compactMetadata"].(map[string]any)["preTokens"] = 124
			case "logical-parent":
				boundary["logicalParentUuid"] = records[1]["uuid"]
			case "boundary-parent":
				boundary["parentUuid"] = records[3]["uuid"]
			case "summary-text":
				summary["message"].(map[string]any)["content"] = "changed"
			case "summary-role":
				summary["type"] = "attachment"
			case "summary-parent":
				summary["parentUuid"] = records[3]["uuid"]
			case "summary-marker":
				delete(summary, "isCompactSummary")
			case "summary-visibility":
				summary["isVisibleInTranscriptOnly"] = false
			case "summary-fields":
				summary["message"].(map[string]any)["content"] = []any{map[string]any{"type": "text", "text": "Private native compaction summary.", "cache_control": map[string]any{"type": "ephemeral"}}}
			case "duplicate-boundary":
				records = append(records, copyHistoryRecord(boundary))
			case "duplicate-summary":
				records = append(records, copyHistoryRecord(summary))
			case "duplicate-proof":
				compactions = append(compactions, compactions[0])
			case "no-proof":
				compactions[0].NativeID = string(domain.NewID())
			case "missing-summary":
				records = append(records[:7], records[8:]...)
			case "changed-replay":
				records[5]["message"].(map[string]any)["content"] = "changed"
			case "changed-replay-parent":
				records[5]["parentUuid"] = records[1]["uuid"]
			case "bad-slug":
				records[5]["slug"] = 1
			case "undeclared-replay":
				// The repeated record is byte-identical but belongs to a detached
				// branch, not the boundary's original prior conversation.
				records[5] = copyHistoryRecord(records[1])
				records[3]["parentUuid"] = nil
			case "trailing-replay":
				records = append(records, copyHistoryRecord(records[3]))
			case "repeated-replay":
				records = append(records[:6], append([]map[string]any{copyHistoryRecord(records[5])}, records[6:]...)...)
			case "detached-leaf":
				records[9]["leafUuid"] = records[3]["uuid"]
			case "case-alias":
				boundary["compactMetadata"].(map[string]any)["pre_tokens"] = 123
			case "unknown-metadata":
				boundary["compactMetadata"].(map[string]any)["future"] = true
			case "summary-as-input":
				proofs = append(proofs, HistoryMessageProof{NativeID: compactions[0].SummaryID, Role: HistoryUser, PayloadSHA256: compactions[0].SummarySHA256})
			case "empty-compactions":
				compactions = nil
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			raw := historyJSONL(t, records)
			if name == "partial" {
				raw = raw[:len(raw)-1]
			}
			observed, err := VerifyCompactedMainTranscript(ctx, raw, session, workspace, proofs, compactions)
			if err == nil || observed != (TranscriptObservation{}) {
				t.Fatal("uncertain compaction produced a partial checkpoint", observed)
			}
		})
	}
}

func TestCompactedHistoryRejectsInvalidRelinkingEvenWithOriginalMetadataProof(t *testing.T) {
	for _, name := range []string{"missing", "future", "foreign-branch", "reverse", "head", "tail", "anchor", "all-ids", "segment-only", "messages-only"} {
		t.Run(name, func(t *testing.T) {
			session, workspace, records, proofs, compactions := compactedHistoryFixture(t)
			meta := records[6]["compactMetadata"].(map[string]any)
			messages := meta["preservedMessages"].(map[string]any)
			segment := meta["preservedSegment"].(map[string]any)
			switch name {
			case "missing":
				messages["uuids"] = []string{string(domain.NewID())}
			case "future":
				messages["uuids"] = []string{proofs[2].NativeID}
			case "foreign-branch":
				records[1]["parentUuid"] = nil
				records[3]["parentUuid"] = nil
				records[5]["parentUuid"] = nil
				messages["uuids"] = []string{proofs[0].NativeID}
			case "reverse":
				messages["uuids"] = []string{proofs[1].NativeID, proofs[0].NativeID}
				segment["tailUuid"] = proofs[0].NativeID
			case "head":
				segment["headUuid"] = proofs[0].NativeID
			case "tail":
				segment["tailUuid"] = proofs[0].NativeID
			case "anchor":
				messages["anchorUuid"] = string(domain.NewID())
			case "all-ids":
				messages["allUuids"] = []string{string(domain.NewID())}
			case "segment-only":
				delete(meta, "preservedMessages")
			case "messages-only":
				delete(meta, "preservedSegment")
			}
			raw, _ := json.Marshal(meta)
			parsed, err := storedCompaction(raw)
			if err != nil {
				t.Fatal(err)
			}
			compactions[0].MetadataSHA256 = compactionDigest(parsed)
			_, err = VerifyCompactedMainTranscript(context.Background(), historyJSONL(t, records), session, workspace, proofs, compactions)
			if name == "segment-only" || name == "messages-only" {
				if err != nil {
					t.Fatal("original independent relink vocabulary rejected", err)
				}
			} else if err == nil {
				t.Fatal("unproved context relink accepted")
			}
		})
	}
}
