package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func manualHistoryFixture(t *testing.T, failed bool) (domain.ID, string, []map[string]any, []HistoryMessageProof, []HistoryCompactionProof, []HistoryCompactionActionProof) {
	t.Helper()
	session, workspace, records, messages := historyFixture(t)
	prior := messages[len(messages)-1].NativeID
	var compactions []HistoryCompactionProof
	boundaryID := ""
	if !failed {
		var boundary HistoryCompactionProof
		records, messages, boundary = appendCompactedHistory(t, session, workspace, records, messages, prior, []string{prior})
		records = records[:len(records)-2]
		messages = messages[:len(messages)-1]
		metadata := records[len(records)-2]["compactMetadata"].(map[string]any)
		metadata["trigger"] = ManualCompaction
		raw, _ := json.Marshal(metadata)
		parsed, err := storedCompaction(raw)
		if err != nil {
			t.Fatal(err)
		}
		boundary.MetadataSHA256 = compactionDigest(parsed)
		compactions = append(compactions, boundary)
		boundaryID = boundary.NativeID
	}
	core, _ := lifecycleFixture(t)
	core.session = session
	echo := manualCommandReplay(t, core, string(core.input), "<command-name>/compact</command-name>\n<command-message>compact</command-message><command-args></command-args>")
	output := manualCommandReplay(t, core, string(domain.NewID()), "<local-command-stdout>Compacted </local-command-stdout>")
	status := CompactSucceeded
	if failed {
		status = CompactFailed
		output = manualCompactionEvents(t, core, true)[6]
	}
	proof, err := ObserveCompactionActionHistory(echo, output, session, core.input, prior, status, boundaryID)
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"sessionId": session, "cwd": workspace, "version": SupportedVersion, "isSidechain": false}
	meta := copyHistoryRecord(base)
	metaID := string(domain.NewID())
	meta["type"], meta["uuid"], meta["parentUuid"], meta["isMeta"] = "user", metaID, prior, true
	meta["message"] = map[string]any{"role": "user", "content": "Private unforwarded native caveat."}
	command := copyHistoryRecord(base)
	command["type"], command["uuid"], command["parentUuid"] = "user", string(proof.ActionID), metaID
	var fields map[string]any
	_ = json.Unmarshal(echo.Body, &fields)
	command["message"] = fields["message"]
	stored := copyHistoryRecord(base)
	storedID := proof.Output.NativeID
	stored["parentUuid"] = string(proof.ActionID)
	if failed {
		stored["type"], stored["subtype"], stored["isMeta"], stored["level"], stored["timestamp"] = "system", "local_command", false, "info", "2026-09-26T00:00:00Z"
		stored["content"] = "<local-command-stderr>Private compaction diagnostic.</local-command-stderr>"
	} else {
		stored["type"] = "user"
		_ = json.Unmarshal(output.Body, &fields)
		stored["message"] = fields["message"]
	}
	stored["uuid"] = storedID
	records = append(records, meta, command, stored, map[string]any{"type": "last-prompt", "sessionId": session, "leafUuid": storedID})
	return session, workspace, records, messages, compactions, []HistoryCompactionActionProof{proof}
}

func TestManualHistoryKeepsActionsSeparateFromConversation(t *testing.T) {
	for _, failed := range []bool{false, true} {
		session, workspace, records, messages, compactions, actions := manualHistoryFixture(t, failed)
		for _, stdout := range []bool{false, true} {
			if stdout && failed {
				records[len(records)-2]["content"] = "<local-command-stdout>Private compaction diagnostic.</local-command-stdout>"
			}
			raw := historyJSONL(t, records)
			unchanged := bytes.Clone(raw)
			observed, err := VerifyMainTranscriptWithActions(context.Background(), raw, session, workspace, messages, compactions, actions)
			if err != nil || observed.MatchedMessages != 2 || observed.AdditionalMessages != 1 || observed.CompactionActions != 1 || (observed.StoredDiagnostics == 1) != failed || (observed.ActionMessages == 1) != failed || (observed.CompactedMessages == 0) != failed || !bytes.Equal(raw, unchanged) {
				t.Fatal("action became conversation or lost native ancestry", err, observed)
			}
			if _, err := VerifyMainTranscript(context.Background(), raw, session, workspace, messages); err == nil {
				t.Fatal("unobserved command granted history authority")
			}
			encoded, _ := json.Marshal(actions)
			if bytes.Contains(encoded, []byte("Private")) || bytes.Contains(encoded, []byte(workspace)) {
				t.Fatal("action proof retained private text or path")
			}
		}
	}
}

func TestManualHistoryAcceptsOnlyItsOriginalSummaryAsAlternateCaveatParent(t *testing.T) {
	session, workspace, records, messages, compactions, actions := manualHistoryFixture(t, false)
	records[len(records)-4]["parentUuid"] = compactions[0].SummaryID
	observed, err := VerifyMainTranscriptWithActions(context.Background(), historyJSONL(t, records), session, workspace, messages, compactions, actions)
	if err != nil || observed.MatchedMessages != 2 || observed.ActiveMatchedMessages != 1 || observed.CompactedMessages != 1 || observed.AdditionalMessages != 1 || observed.ActionMessages != 2 {
		t.Fatal("original summary-parent persistence changed native context", err, observed)
	}
	records[len(records)-4]["parentUuid"] = compactions[0].NativeID
	if _, err := VerifyMainTranscriptWithActions(context.Background(), historyJSONL(t, records), session, workspace, messages, compactions, actions); err == nil {
		t.Fatal("arbitrary boundary parent acquired manual command authority")
	}
}

func TestManualHistoryRetainsActionsAcrossLaterAutomaticCompaction(t *testing.T) {
	for _, failed := range []bool{false, true} {
		session, workspace, records, messages, compactions, actions := manualHistoryFixture(t, failed)
		output := actions[0].Output.NativeID
		// Native can batch a prior local-command record with the next boundary.
		// Its original envelope identity and every field must remain exact.
		records = append(records, copyHistoryRecord(records[len(records)-2]))
		var next HistoryCompactionProof
		records, messages, next = appendCompactedHistory(t, session, workspace, records, messages, output, []string{output})
		compactions = append(compactions, next)
		observed, err := VerifyMainTranscriptWithActions(context.Background(), historyJSONL(t, records), session, workspace, messages, compactions, actions)
		if err != nil || observed.MatchedMessages != 3 || observed.ActiveMatchedMessages != 1 || observed.CompactedMessages != 2 || observed.CompactionActions != 1 || observed.ReplayedRecords != 1 {
			t.Fatal("later native context erased original command provenance", err, observed)
		}
	}
}

func TestManualHistoryDiagnosticRequiresOriginalNativeRepresentation(t *testing.T) {
	for _, name := range []string{"timestamp", "level", "subtype", "message", "metadata", "logical-parent", "meta", "unknown-tag", "nested-content", "provider-id", "stored-assistant", "missing-action", "no-actions", "null-meta"} {
		t.Run(name, func(t *testing.T) {
			session, workspace, records, messages, compactions, actions := manualHistoryFixture(t, true)
			output := records[len(records)-2]
			switch name {
			case "timestamp":
				output["timestamp"] = "invalid"
			case "level":
				output["level"] = "error"
			case "subtype":
				output["subtype"] = "future"
			case "message":
				output["message"] = map[string]any{"role": "assistant", "content": "Private compaction diagnostic."}
			case "metadata":
				output["compactMetadata"] = nil
			case "logical-parent":
				output["logicalParentUuid"] = messages[1].NativeID
			case "meta":
				output["isMeta"] = true
			case "unknown-tag":
				output["content"] = "<future>Private compaction diagnostic.</future>"
			case "nested-content":
				output["content"] = "<local-command-stderr><text>Private compaction diagnostic.</text></local-command-stderr>"
			case "provider-id":
				output["uuid"] = string(domain.NewID())
				records[len(records)-1]["leafUuid"] = output["uuid"]
			case "stored-assistant":
				output["type"] = "assistant"
				output["message"] = map[string]any{"role": "assistant", "id": string(domain.NewID()), "model": "<synthetic>", "content": "Private compaction diagnostic."}
			case "missing-action":
				records[len(records)-3]["uuid"] = string(domain.NewID())
				output["parentUuid"] = records[len(records)-3]["uuid"]
			case "no-actions":
				actions = nil
			case "null-meta":
				output["isMeta"] = nil
			}
			if observed, err := VerifyMainTranscriptWithActions(context.Background(), historyJSONL(t, records), session, workspace, messages, compactions, actions); err == nil || observed != (TranscriptObservation{}) {
				t.Fatal("changed diagnostic representation granted history proof", observed)
			}
		})
	}
}

func TestManualHistoryRejectsChangedAndDetachedActions(t *testing.T) {
	for _, failed := range []bool{false, true} {
		for _, name := range []string{"echo-content", "output-content", "echo-role", "output-role", "output-parent", "meta-parent", "meta-flag", "meta-role", "echo-meta", "detached", "missing-output", "duplicate-output", "duplicate-action", "foreign-action", "prior-message", "status", "output-identity", "output-digest", "ordinary-alias", "boundary", "cancel"} {
			t.Run(string(map[bool]CompactResult{false: CompactSucceeded, true: CompactFailed}[failed])+"/"+name, func(t *testing.T) {
				session, workspace, records, messages, compactions, actions := manualHistoryFixture(t, failed)
				meta, echo, output := records[len(records)-4], records[len(records)-3], records[len(records)-2]
				ctx := context.Background()
				switch name {
				case "echo-content":
					echo["message"].(map[string]any)["content"] = "changed"
				case "output-content":
					if failed {
						output["content"] = "<local-command-stderr>changed</local-command-stderr>"
					} else {
						output["message"].(map[string]any)["content"] = "changed"
					}
				case "echo-role":
					echo["type"] = "attachment"
				case "output-role":
					output["type"] = "attachment"
				case "output-parent":
					output["parentUuid"] = meta["uuid"]
				case "meta-parent":
					meta["parentUuid"] = messages[0].NativeID
				case "meta-flag":
					meta["isMeta"] = false
				case "meta-role":
					meta["type"] = "attachment"
				case "echo-meta":
					echo["isMeta"] = true
				case "detached":
					records[len(records)-1]["leafUuid"] = messages[1].NativeID
				case "missing-output":
					records = append(records[:len(records)-2], records[len(records)-1])
				case "duplicate-output":
					records = append(records, copyHistoryRecord(output))
				case "duplicate-action":
					actions = append(actions, actions[0])
				case "foreign-action":
					actions[0].ActionID = domain.NewID()
				case "prior-message":
					actions[0].PriorMessageID = messages[0].NativeID
				case "status":
					if failed {
						actions[0].Status = CompactSucceeded
					} else {
						actions[0].Status = CompactFailed
					}
				case "output-identity":
					if failed {
						output["uuid"] = string(domain.NewID())
						records[len(records)-1]["leafUuid"] = output["uuid"]
					} else {
						actions[0].Output.NativeID = string(domain.NewID())
					}
				case "output-digest":
					if failed {
						actions[0].DiagnosticSHA256 = messages[0].PayloadSHA256
					} else {
						actions[0].Output.PayloadSHA256 = messages[0].PayloadSHA256
					}
				case "ordinary-alias":
					messages = append(messages, actions[0].Echo)
				case "boundary":
					if failed {
						actions[0].BoundaryID = string(domain.NewID())
					} else {
						records[5]["compactMetadata"].(map[string]any)["trigger"] = AutomaticCompaction
						raw, _ := json.Marshal(records[5]["compactMetadata"])
						parsed, _ := storedCompaction(raw)
						compactions[0].MetadataSHA256 = compactionDigest(parsed)
					}
				case "cancel":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				observed, err := VerifyMainTranscriptWithActions(ctx, historyJSONL(t, records), session, workspace, messages, compactions, actions)
				if err == nil || observed != (TranscriptObservation{}) {
					t.Fatal("unproved manual history authorized a partial observation", observed)
				}
			})
		}
	}
}
