package claude

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func childHistoryFixture(t *testing.T) (domain.ID, string, []map[string]any, map[string]any, ChildHistoryBinding, []HistoryMessageProof) {
	t.Helper()
	session, workspace, main, _ := historyFixture(t)
	binding := ChildHistoryBinding{TaskID: "native-child-task", ToolID: "toolu_original_agent", AgentType: "general-purpose", Description: "Private original child description", SpawnDepth: 1}
	records := main[1:4]
	proofs := []HistoryMessageProof{}
	for _, record := range records {
		record["isSidechain"] = true
		record["agentId"] = binding.TaskID
		if record["type"] == "attachment" {
			continue
		}
		raw, _ := json.Marshal(map[string]any{"type": record["type"], "uuid": record["uuid"], "session_id": session, "parent_tool_use_id": binding.ToolID, "message": record["message"]})
		proof, err := ObserveChildHistoryMessage(StreamEvent{Kind: NativeMessage, Type: record["type"].(string), Body: raw}, session, binding.ToolID)
		if err != nil {
			t.Fatal(err)
		}
		proofs = append(proofs, proof)
	}
	metadata := map[string]any{"toolUseId": binding.ToolID, "agentType": binding.AgentType, "description": "Private original child description", "spawnDepth": 1}
	return session, workspace, records, metadata, binding, proofs
}

func TestChildHistoryBindsOriginalTaskAndKeepsUnforwardedContentExplicit(t *testing.T) {
	session, workspace, records, metadata, binding, proofs := childHistoryFixture(t)
	raw := historyJSONL(t, records)
	sidecar, _ := json.Marshal(metadata)
	for _, matched := range []int{0, 1, 2} {
		observation, err := VerifyChildTranscript(context.Background(), raw, sidecar, session, workspace, binding, proofs[:matched])
		if err != nil || observation.Transcript.MatchedMessages != uint32(matched) || observation.Transcript.AdditionalMessages != uint32(2-matched) || observation.Transcript.LeafID != proofs[1].NativeID || observation.SpawnDepth != 1 || !validHistoryDigest(observation.MetadataSHA256) {
			t.Fatal("original child file became synthetic forwarded content", err)
		}
	}
	first, _ := VerifyChildTranscript(context.Background(), raw, sidecar, session, workspace, binding, proofs)
	metadata["description"] = "Updated private native description"
	binding.Description = metadata["description"].(string)
	sidecar, _ = json.Marshal(metadata)
	second, err := VerifyChildTranscript(context.Background(), raw, sidecar, session, workspace, binding, proofs)
	if err != nil || first.MetadataSHA256 == second.MetadataSHA256 || first.Transcript != second.Transcript {
		t.Fatal("native sidecar bytes were not pinned independently", err)
	}
	if _, err := VerifyMainTranscript(context.Background(), raw, session, workspace, proofs); err == nil {
		t.Fatal("child history was relabeled as main input")
	}
}

func TestChildHistoryRejectsChangedOwnershipAndDetachedProofs(t *testing.T) {
	for _, name := range []string{"foreign-agent", "missing-agent", "root-record", "foreign-tool", "foreign-type", "missing-description", "changed-description", "changed-nested-depth", "zero-depth", "deep-root", "parent-null", "foreign-parent", "missing-parent", "case-alias", "unknown-metadata", "foreign-proof-tool", "main-proof", "main-marker", "changed-payload", "detached-proof", "missing-file", "truncated", "duplicate-metadata", "metadata-limit"} {
		t.Run(name, func(t *testing.T) {
			session, workspace, records, metadata, binding, proofs := childHistoryFixture(t)
			switch name {
			case "foreign-agent":
				records[1]["agentId"] = "other"
			case "missing-agent":
				delete(records[0], "agentId")
			case "root-record":
				records[1]["isSidechain"] = false
			case "foreign-tool":
				metadata["toolUseId"] = "other"
			case "foreign-type":
				metadata["agentType"] = "other"
			case "changed-description":
				metadata["description"] = "Changed native description"
			case "changed-nested-depth":
				binding.ParentAgentID, binding.SpawnDepth = "original", 2
				metadata["parentAgentId"], metadata["spawnDepth"] = "original", 3
			case "missing-description":
				delete(metadata, "description")
			case "zero-depth":
				metadata["spawnDepth"] = 0
			case "deep-root":
				metadata["spawnDepth"] = 2
			case "parent-null":
				metadata["parentAgentId"] = nil
			case "foreign-parent":
				metadata["parentAgentId"] = "foreign"
				metadata["spawnDepth"] = 2
				binding.ParentAgentID = "original"
			case "missing-parent":
				metadata["spawnDepth"] = 2
				binding.ParentAgentID = "original"
			case "case-alias":
				metadata["ToolUseId"] = metadata["toolUseId"]
			case "unknown-metadata":
				metadata["unverified"] = true
			case "foreign-proof-tool":
				proofs[0].ParentToolID = "other"
			case "main-proof":
				proofs[0].ParentToolID = ""
			case "main-marker":
				records = append(records, map[string]any{"type": "last-prompt", "sessionId": session, "leafUuid": records[2]["uuid"]})
			case "changed-payload":
				records[0]["message"].(map[string]any)["content"] = "changed child prompt"
			case "detached-proof":
				records[2]["parentUuid"] = nil
			}
			raw := historyJSONL(t, records)
			sidecar, _ := json.Marshal(metadata)
			switch name {
			case "missing-file":
				raw = nil
			case "truncated":
				raw = raw[:len(raw)-1]
			case "duplicate-metadata":
				sidecar = append(sidecar[:len(sidecar)-1], []byte(`,"spawnDepth":1}`)...)
			case "metadata-limit":
				sidecar = make([]byte, (64<<10)+1)
			}
			observation, err := VerifyChildTranscript(context.Background(), raw, sidecar, session, workspace, binding, proofs)
			if err == nil || observation != (ChildTranscriptObservation{}) {
				t.Fatal("unverified child history produced partial or accepted evidence")
			}
		})
	}
}

func TestChildProofRequiresExplicitOriginalParent(t *testing.T) {
	session, _, records, _, binding, _ := childHistoryFixture(t)
	for _, parent := range []any{nil, "foreign"} {
		raw, _ := json.Marshal(map[string]any{"type": "user", "uuid": records[0]["uuid"], "session_id": session, "parent_tool_use_id": parent, "message": records[0]["message"]})
		event := StreamEvent{Kind: NativeMessage, Type: "user", Body: raw}
		if _, err := ObserveChildHistoryMessage(event, session, binding.ToolID); err == nil {
			t.Fatal("child proof adopted a different native parent")
		}
		if _, err := ObserveChildHistoryMessage(event, session, ""); err == nil {
			t.Fatal("missing child parent became root authority")
		}
	}
}

func TestChildHistoryCancellationReturnsNoEvidence(t *testing.T) {
	session, workspace, records, metadata, binding, proofs := childHistoryFixture(t)
	sidecar, _ := json.Marshal(metadata)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	observation, err := VerifyChildTranscript(ctx, historyJSONL(t, records), sidecar, session, workspace, binding, proofs)
	if err == nil || observation != (ChildTranscriptObservation{}) {
		t.Fatal("canceled child verification returned evidence")
	}
}

func TestChildHistoryProjectsOnlySelectedAncestry(t *testing.T) {
	session, workspace, records, metadata, binding, proofs := childHistoryFixture(t)
	sibling := map[string]any{}
	for key, value := range records[2] {
		sibling[key] = value
	}
	sibling["uuid"], sibling["parentUuid"] = string(domain.NewID()), records[0]["uuid"]
	sibling["message"] = map[string]any{"role": "assistant", "id": "sibling-response", "model": "sibling-model", "content": []any{map[string]any{"type": "text", "text": "Abandoned output"}}, "usage": map[string]any{"input_tokens": 9, "output_tokens": 8}}
	leaf := map[string]any{}
	for key, value := range records[0] {
		leaf[key] = value
	}
	leaf["uuid"], leaf["parentUuid"] = string(domain.NewID()), records[2]["uuid"]
	leaf["message"] = map[string]any{"role": "user", "content": "Selected context"}
	records = append(records, sibling, leaf)
	sidecar, _ := json.Marshal(metadata)
	observed, err := VerifyChildTranscript(context.Background(), historyJSONL(t, records), sidecar, session, workspace, binding, proofs)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Transcript.LeafID != leaf["uuid"] || observed.Output == nil || observed.Output.NativeMessageID == "sibling-response" || observed.ObservedModel != nil && *observed.ObservedModel == "sibling-model" || observed.Usage != nil && observed.Usage.Output != nil && *observed.Usage.Output == "8" {
		t.Fatal("abandoned child branch supplied selected output/model/usage")
	}
}

func TestChildHistoryAcceptsOnlyNativeBareTextReplayEquivalence(t *testing.T) {
	for _, name := range []string{"bare-text", "extra-field", "two-blocks", "changed-text", "main-message"} {
		t.Run(name, func(t *testing.T) {
			session, workspace, records, metadata, binding, proofs := childHistoryFixture(t)
			message := records[0]["message"].(map[string]any)
			text := message["content"].(string)
			block := map[string]any{"type": "text", "text": text}
			blocks := []any{block}
			switch name {
			case "extra-field":
				block["cache_control"] = map[string]any{"type": "ephemeral"}
			case "two-blocks":
				blocks = append(blocks, map[string]any{"type": "text", "text": ""})
			case "changed-text":
				block["text"] = text + " changed"
			}
			message["content"] = blocks
			var err error
			if name == "main-message" {
				original, _ := json.Marshal(map[string]any{"role": "user", "content": text})
				updated, _ := json.Marshal(message)
				_, before, _ := canonicalHistoryPayload(original)
				_, after, _ := canonicalHistoryPayload(updated)
				if before == after {
					t.Fatal("child-only representation equivalence weakened main proofs")
				}
				return
			}
			sidecar, _ := json.Marshal(metadata)
			_, err = VerifyChildTranscript(context.Background(), historyJSONL(t, records), sidecar, session, workspace, binding, proofs)
			if (err == nil) != (name == "bare-text") {
				t.Fatal("child replay equivalence changed additional payload semantics", err)
			}
		})
	}
}
