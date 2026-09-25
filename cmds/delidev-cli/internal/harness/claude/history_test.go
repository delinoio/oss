package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/google/uuid"
)

func historyFixture(t *testing.T) (domain.ID, string, []map[string]any, []HistoryMessageProof) {
	t.Helper()
	session := domain.NewID()
	workspace := filepath.Join(t.TempDir(), "workspace")
	user, attachment, assistant := string(domain.NewID()), uuid.NewString(), uuid.NewString()
	userBody := map[string]any{"role": "user", "content": "Private original transcript input."}
	assistantBody := map[string]any{"role": "assistant", "id": "msg_history_fixture", "model": "fixture-model", "content": []any{map[string]any{"type": "text", "text": "Private original transcript output."}}, "usage": map[string]any{"output_tokens": 3}, "stop_reason": "end_turn"}
	records := []map[string]any{
		{"type": "queue-operation", "operation": "enqueue", "sessionId": session},
		{"type": "user", "uuid": user, "parentUuid": nil, "sessionId": session, "cwd": workspace, "version": SupportedVersion, "isSidechain": false, "message": userBody},
		{"type": "attachment", "uuid": attachment, "parentUuid": user, "sessionId": session, "cwd": workspace, "version": SupportedVersion, "isSidechain": false, "attachment": map[string]any{"type": "fixture-context", "content": "Preserve native context"}},
		{"type": "assistant", "uuid": assistant, "parentUuid": attachment, "sessionId": session, "cwd": workspace, "version": SupportedVersion, "isSidechain": false, "message": assistantBody},
		{"type": "last-prompt", "sessionId": session, "leafUuid": assistant, "lastPrompt": "Private original transcript input."},
	}
	proofs := []HistoryMessageProof{}
	for _, record := range []map[string]any{records[1], records[3]} {
		raw, _ := json.Marshal(map[string]any{"type": record["type"], "uuid": record["uuid"], "session_id": session, "parent_tool_use_id": nil, "message": record["message"]})
		proof, err := ObserveMainHistoryMessage(StreamEvent{Kind: NativeMessage, Type: record["type"].(string), Body: raw}, session)
		if err != nil {
			t.Fatal(err)
		}
		proofs = append(proofs, proof)
	}
	return session, workspace, records, proofs
}
func historyJSONL(t *testing.T, records []map[string]any) []byte {
	t.Helper()
	var raw []byte
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, line...)
		raw = append(raw, '\n')
	}
	return raw
}

func TestMainHistoryProofPreservesBodiesAndPinsFinalizedMetadataSeparately(t *testing.T) {
	session, workspace, records, proofs := historyFixture(t)
	original := historyJSONL(t, records)
	first, err := VerifyMainTranscript(context.Background(), original, session, workspace, proofs)
	if err != nil || first.MatchedMessages != 2 || first.AdditionalMessages != 0 || first.LeafID != proofs[1].NativeID || first.Bytes != uint64(len(original)) {
		t.Fatal("native original chain was not validated", err)
	}
	message := records[3]["message"].(map[string]any)
	message["usage"] = map[string]any{"output_tokens": 7}
	message["stop_reason"] = nil
	message["context_management"] = nil
	finalized := historyJSONL(t, records)
	second, err := VerifyMainTranscript(context.Background(), finalized, session, workspace, proofs)
	if err != nil || second.SHA256 == first.SHA256 || second.MatchedMessages != first.MatchedMessages {
		t.Fatal("finalized metadata changed original content proof or escaped the file digest", err)
	}
	raw, _ := json.Marshal(proofs)
	if bytes.Contains(raw, []byte("Private original")) || bytes.Contains(raw, []byte(workspace)) {
		t.Fatal("history proof retained private content or path")
	}
}

func TestMainHistoryRejectsForeignChangedTruncatedAndDetachedEvidence(t *testing.T) {
	for _, name := range []string{"changed-body", "changed-model", "changed-provider-id", "changed-role", "foreign-session", "foreign-cwd", "foreign-version", "sidechain", "missing-parent", "forward-parent", "cycle", "detached-proof", "changed-leaf", "unknown-record", "duplicate-uuid", "duplicate-proof", "reversed-proofs", "invalid-digest", "missing-proof", "truncated", "blank-line", "duplicate-json-key", "empty", "null-content", "invalid-content", "missing-id"} {
		t.Run(name, func(t *testing.T) {
			session, workspace, records, proofs := historyFixture(t)
			assistant := records[3]
			message := assistant["message"].(map[string]any)
			switch name {
			case "changed-body":
				message["content"] = []any{map[string]any{"type": "text", "text": "changed"}}
			case "changed-model":
				message["model"] = "changed"
			case "changed-provider-id":
				message["id"] = "changed"
			case "changed-role":
				message["role"] = "user"
			case "foreign-session":
				assistant["sessionId"] = domain.NewID()
			case "foreign-cwd":
				assistant["cwd"] = workspace + "-foreign"
			case "foreign-version":
				assistant["version"] = "other"
			case "sidechain":
				assistant["isSidechain"] = true
			case "missing-parent":
				delete(assistant, "parentUuid")
			case "forward-parent":
				records[1]["parentUuid"] = assistant["uuid"]
			case "cycle":
				assistant["parentUuid"] = assistant["uuid"]
			case "detached-proof":
				assistant["parentUuid"] = nil
			case "changed-leaf":
				records[4]["leafUuid"] = records[1]["uuid"]
			case "unknown-record":
				records[2]["type"] = "unverified-extension"
			case "duplicate-uuid":
				records = append(records, assistant)
			case "duplicate-proof":
				proofs = append(proofs, proofs[0])
			case "reversed-proofs":
				proofs[0], proofs[1] = proofs[1], proofs[0]
			case "invalid-digest":
				proofs[1].PayloadSHA256 = "invalid"
			case "missing-proof":
				proofs[1].NativeID = uuid.NewString()
			case "null-content":
				message["content"] = nil
			case "invalid-content":
				message["content"] = 12
			case "missing-id":
				delete(message, "id")
			}
			raw := historyJSONL(t, records)
			switch name {
			case "truncated":
				raw = raw[:len(raw)-1]
			case "blank-line":
				raw = append(raw, '\n')
			case "duplicate-json-key":
				raw = bytes.Replace(raw, []byte(`"operation":"enqueue"`), []byte(`"operation":"enqueue","operation":"dequeue"`), 1)
			case "empty":
				raw = nil
			}
			if _, err := VerifyMainTranscript(context.Background(), raw, session, workspace, proofs); err == nil {
				t.Fatal("unverified transcript became retained evidence")
			}
		})
	}
}

func TestMainHistoryReportsUnobservedMessagesOnEveryBranch(t *testing.T) {
	session, workspace, records, proofs := historyFixture(t)
	extra := map[string]any{"type": "user", "uuid": string(domain.NewID()), "parentUuid": records[1]["uuid"], "sessionId": session, "cwd": workspace, "version": SupportedVersion, "isSidechain": false, "message": map[string]any{"role": "user", "content": "Unobserved branch input"}}
	records = append(records, extra)
	observed, err := VerifyMainTranscript(context.Background(), historyJSONL(t, records), session, workspace, proofs)
	if err != nil || observed.AdditionalMessages != 1 {
		t.Fatal("unobserved branch became complete original-input evidence", err)
	}
}

func TestHistoryMessageProofRejectsForeignChildAndIncompleteEnvelopes(t *testing.T) {
	for _, name := range []string{"child", "missing-parent", "foreign-session", "invalid-uuid", "wrong-role", "wrong-kind", "case-alias"} {
		t.Run(name, func(t *testing.T) {
			session, _, records, _ := historyFixture(t)
			fields := map[string]any{"type": "user", "uuid": records[1]["uuid"], "session_id": session, "parent_tool_use_id": nil, "message": records[1]["message"]}
			kind := NativeMessage
			switch name {
			case "child":
				fields["parent_tool_use_id"] = "parent-tool"
			case "missing-parent":
				delete(fields, "parent_tool_use_id")
			case "foreign-session":
				fields["session_id"] = domain.NewID()
			case "invalid-uuid":
				fields["uuid"] = "unknown"
			case "wrong-role":
				fields["message"] = records[3]["message"]
			case "wrong-kind":
				kind = NativeRequest
			case "case-alias":
				fields["UUID"] = fields["uuid"]
				delete(fields, "uuid")
			}
			raw, _ := json.Marshal(fields)
			if _, err := ObserveMainHistoryMessage(StreamEvent{Kind: kind, Type: "user", Body: raw}, session); err == nil {
				t.Fatal("foreign or incomplete history source accepted")
			}
		})
	}
}

func TestMainHistoryCancellationReturnsNoPartialProof(t *testing.T) {
	session, workspace, records, proofs := historyFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	observed, err := VerifyMainTranscript(ctx, historyJSONL(t, records), session, workspace, proofs)
	if err == nil || observed != (TranscriptObservation{}) {
		t.Fatal("canceled history read returned a partial checkpoint")
	}
}
