package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func appendResumeContextFixture(t *testing.T, session domain.ID, workspace string, records []map[string]any, messages []HistoryMessageProof, action HistoryCompactionActionProof, prefix TranscriptObservation) ([]map[string]any, []HistoryMessageProof) {
	t.Helper()
	base := map[string]any{"sessionId": session, "cwd": workspace, "version": SupportedVersion, "isSidechain": false}
	parent := prefix.LeafID
	if action.Status == CompactSucceeded {
		user := copyHistoryRecord(base)
		user["type"], user["uuid"], user["parentUuid"], user["isMeta"], user["promptId"] = "user", string(domain.NewID()), parent, true, string(domain.NewID())
		user["message"] = map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "Continue from where you left off."}}}
		records = append(records, user)
		parent = user["uuid"].(string)
	} else {
		parent = action.Echo.NativeID
	}
	synthetic := copyHistoryRecord(base)
	synthetic["type"], synthetic["uuid"], synthetic["parentUuid"], synthetic["isApiErrorMessage"] = "assistant", string(domain.NewID()), parent, false
	var message map[string]any
	if err := json.Unmarshal([]byte(`{"diagnostics":null,"container":null,"model":"<synthetic>","role":"assistant","stop_details":null,"stop_reason":"stop_sequence","stop_sequence":"","type":"message","usage":{"output_tokens_details":null,"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":null,"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0},"inference_geo":null,"iterations":null,"speed":null},"content":[{"type":"text","text":"No response requested."}],"context_management":null}`), &message); err != nil {
		t.Fatal(err)
	}
	message["id"] = string(domain.NewID())
	synthetic["message"] = message
	records = append(records, synthetic)
	user := copyHistoryRecord(base)
	user["type"], user["uuid"], user["parentUuid"] = "user", string(domain.NewID()), synthetic["uuid"]
	user["message"] = map[string]any{"role": "user", "content": "Private original next input."}
	envelope, _ := json.Marshal(map[string]any{"type": "user", "uuid": user["uuid"], "session_id": session, "parent_tool_use_id": nil, "message": user["message"]})
	proof, err := ObserveMainHistoryMessage(StreamEvent{Kind: NativeMessage, Type: "user", Body: envelope}, session)
	if err != nil {
		t.Fatal(err)
	}
	messages = append(messages, proof)
	records = append(records, user, map[string]any{"type": "last-prompt", "sessionId": session, "leafUuid": user["uuid"]})
	return records, messages
}

func resumedHistoryFixture(t *testing.T, failed bool) (domain.ID, string, []map[string]any, []HistoryMessageProof, []HistoryCompactionProof, []HistoryCompactionActionProof, []historyResumeProof, int) {
	t.Helper()
	session, workspace, records, messages, compactions, actions := manualHistoryFixture(t, failed)
	prefix, err := VerifyMainTranscriptWithActions(context.Background(), historyJSONL(t, records), session, workspace, messages, compactions, actions)
	if err != nil {
		t.Fatal(err)
	}
	resume := historyResumeProof{transcript: prefix, messages: len(messages), action: actions[0].ActionID}
	start := len(records)
	records, messages = appendResumeContextFixture(t, session, workspace, records, messages, actions[0], prefix)
	return session, workspace, records, messages, compactions, actions, []historyResumeProof{resume}, start
}

func TestResumedManualHistoryRequiresOriginalClosedPrefix(t *testing.T) {
	for _, failed := range []bool{false, true} {
		session, workspace, records, messages, compactions, actions, resumes, start := resumedHistoryFixture(t, failed)
		raw := historyJSONL(t, records)
		original := bytes.Clone(raw)
		observed, err := verifyResumedTranscript(context.Background(), raw, session, workspace, messages, nil, compactions, actions, resumes)
		count := uint32(2)
		if failed {
			count = 1
		}
		if err != nil || observed.MatchedMessages != 3 || observed.ResumeContextMessages != count || observed.AdditionalMessages != count+1 || observed.CompactionActions != 1 || !bytes.Equal(raw, original) {
			t.Fatal("resume context replaced original input or lost diagnostic provenance", err, observed)
		}
		// Initialization alone retains exact prior bytes and no new context authority.
		initial, err := verifyResumedTranscript(context.Background(), historyJSONL(t, records[:start]), session, workspace, messages[:2], nil, compactions, actions, resumes)
		if err != nil || initial != resumes[0].transcript {
			t.Fatal("native initialization changed original checkpoint", err)
		}
		encoded, _ := json.Marshal(resumes)
		if string(encoded) != "[{}]" {
			t.Fatal("private handoff prefix became serializable recovery authority")
		}
		if failed {
			if _, err := VerifyMainTranscriptWithActions(context.Background(), raw, session, workspace, messages, compactions, actions); err == nil {
				t.Fatal("detached diagnostic accepted without original prefix")
			}
		}
	}
}

func TestResumedManualHistoryRejectsChangedPrefixAndSyntheticContext(t *testing.T) {
	for _, failed := range []bool{false, true} {
		for _, name := range []string{"prefix-content", "prefix-size", "prefix-digest", "prefix-count", "prefix-leaf", "prefix-action", "duplicate-prefix", "missing-input-proof", "wrong-input-role", "input-parent", "synthetic-parent", "synthetic-content", "synthetic-model", "synthetic-usage", "synthetic-api-error", "synthetic-null-error", "synthetic-id", "synthetic-extra", "synthetic-meta", "detached-context", "duplicate-context", "user-meta", "user-content", "user-prompt", "user-extra", "cancel"} {
			t.Run(string(map[bool]CompactResult{false: CompactSucceeded, true: CompactFailed}[failed])+"/"+name, func(t *testing.T) {
				session, workspace, records, messages, compactions, actions, resumes, start := resumedHistoryFixture(t, failed)
				assistant := records[start]
				if !failed {
					assistant = records[start+1]
				}
				content := assistant["message"].(map[string]any)
				next := records[len(records)-2]
				ctx := context.Background()
				switch name {
				case "prefix-content":
					records[0]["private-change"] = true
				case "prefix-size":
					resumes[0].transcript.Bytes--
				case "prefix-digest":
					resumes[0].transcript.SHA256 = string(bytes.Repeat([]byte{'0'}, 64))
				case "prefix-count":
					resumes[0].messages--
				case "prefix-leaf":
					resumes[0].transcript.LeafID = string(domain.NewID())
				case "prefix-action":
					resumes[0].action = domain.NewID()
				case "duplicate-prefix":
					resumes = append(resumes, resumes[0])
				case "missing-input-proof":
					messages = messages[:2]
				case "wrong-input-role":
					messages[2].Role = HistoryAssistant
				case "input-parent":
					next["parentUuid"] = actions[0].Echo.NativeID
				case "synthetic-parent":
					assistant["parentUuid"] = messages[0].NativeID
				case "synthetic-content":
					content["content"] = []any{map[string]any{"type": "text", "text": "Different private context"}}
				case "synthetic-model":
					content["model"] = "fixture-provider"
				case "synthetic-usage":
					content["usage"].(map[string]any)["output_tokens"] = 1
				case "synthetic-api-error":
					assistant["isApiErrorMessage"] = true
				case "synthetic-null-error":
					assistant["isApiErrorMessage"] = nil
				case "synthetic-id":
					content["id"] = "not-an-original-id"
				case "synthetic-extra":
					content["future"] = true
				case "synthetic-meta":
					assistant["isMeta"] = true
				case "detached-context":
					next["parentUuid"] = messages[1].NativeID
				case "duplicate-context":
					records = append(records[:len(records)-2], append([]map[string]any{copyHistoryRecord(assistant)}, records[len(records)-2:]...)...)
				case "user-meta", "user-content", "user-prompt", "user-extra":
					if failed {
						t.Skip("native failed command has no synthetic user")
					}
					user := records[start]
					switch name {
					case "user-meta":
						user["isMeta"] = false
					case "user-content":
						user["message"] = map[string]any{"role": "user", "content": "Continue from where you left off."}
					case "user-prompt":
						user["promptId"] = nil
					case "user-extra":
						user["message"].(map[string]any)["future"] = true
					}
				case "cancel":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				result, err := verifyResumedTranscript(ctx, historyJSONL(t, records), session, workspace, messages, nil, compactions, actions, resumes)
				if err == nil || result != (TranscriptObservation{}) {
					t.Fatal("changed retained resume evidence granted history authority", result)
				}
			})
		}
	}
}

func TestResumedManualHistoryRetainsOriginalBranchAcrossLaterCompaction(t *testing.T) {
	for _, failed := range []bool{false, true} {
		session, workspace, records, messages, compactions, actions, resumes, _ := resumedHistoryFixture(t, failed)
		prior := messages[len(messages)-1].NativeID
		var boundary HistoryCompactionProof
		records, messages, boundary = appendCompactedHistory(t, session, workspace, records, messages, prior, []string{prior})
		compactions = append(compactions, boundary)
		observed, err := verifyResumedTranscript(context.Background(), historyJSONL(t, records), session, workspace, messages, nil, compactions, actions, resumes)
		if err != nil || observed.MatchedMessages != 4 || observed.ActiveMatchedMessages != 2 || observed.CompactedMessages != 2 || observed.CompactionActions != 1 {
			t.Fatal("later compaction erased original resume provenance", err, observed)
		}
	}
}

func TestResumedManualHistoryPinsEveryOriginalPrefixInOrder(t *testing.T) {
	session, workspace, records, messages, compactions, actions, resumes, _ := resumedHistoryFixture(t, true)
	_, _, second, _, _, nextActions := manualHistoryFixture(t, true)
	nextAction := nextActions[0]
	nextAction.PriorMessageID = messages[len(messages)-1].NativeID
	second = second[len(second)-4:]
	for _, record := range second {
		record["sessionId"] = session
		if record["cwd"] != nil {
			record["cwd"] = workspace
		}
	}
	second[0]["parentUuid"] = nextAction.PriorMessageID
	records = append(records, second...)
	actions = append(actions, nextAction)
	prefix, err := verifyResumedTranscript(context.Background(), historyJSONL(t, records), session, workspace, messages, nil, compactions, actions, resumes)
	if err != nil {
		t.Fatal("second original command lost first retained branch", err)
	}
	resumes = append(resumes, historyResumeProof{transcript: prefix, messages: len(messages), action: nextAction.ActionID})
	records, messages = appendResumeContextFixture(t, session, workspace, records, messages, nextAction, prefix)
	observed, err := verifyResumedTranscript(context.Background(), historyJSONL(t, records), session, workspace, messages, nil, compactions, actions, resumes)
	if err != nil || observed.MatchedMessages != 4 || observed.ResumeContextMessages != 2 || observed.AdditionalMessages != 4 || observed.StoredDiagnostics != 2 {
		t.Fatal("later handoff erased an earlier original prefix", err, observed)
	}
	resumes[0], resumes[1] = resumes[1], resumes[0]
	if _, err := verifyResumedTranscript(context.Background(), historyJSONL(t, records), session, workspace, messages, nil, compactions, actions, resumes); err == nil {
		t.Fatal("reordered original process history accepted")
	}
}
