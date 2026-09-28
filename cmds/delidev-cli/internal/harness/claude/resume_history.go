package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// historyResumeProof is created only by a checked, closed live controller. The
// original complete file prefix proves prior command/diagnostic provenance;
// merely finding similar synthetic text in a later file grants no authority.
// This private in-memory proof is deliberately not a serializable checkpoint.
type historyResumeProof struct {
	transcript TranscriptObservation
	messages   int
	action     domain.ID
}

type historyResumeIndex struct {
	messages map[string]bool
	detached map[string]bool
}

func indexResumedHistory(ctx context.Context, raw []byte, proofs []HistoryMessageProof, actions []HistoryCompactionActionProof, resumes []historyResumeProof) (*historyResumeIndex, error) {
	index := &historyResumeIndex{messages: map[string]bool{}, detached: map[string]bool{}}
	if len(resumes) > maxStreamIdentities {
		return nil, historyUncertain()
	}
	byAction := make(map[domain.ID]HistoryCompactionActionProof, len(actions))
	for _, action := range actions {
		byAction[action.ActionID] = action
	}
	hash := sha256.New()
	previousBytes, previousMessages := uint64(0), 0
	seenActions := map[domain.ID]bool{}
	for n, resume := range resumes {
		if err := ctx.Err(); err != nil {
			return nil, domain.SafeError(err)
		}
		stored := resume.transcript
		action, ok := byAction[resume.action]
		if !ok || seenActions[resume.action] || stored.Bytes <= previousBytes || stored.Bytes > uint64(len(raw)) || raw[stored.Bytes-1] != '\n' || !validHistoryDigest(stored.SHA256) || resume.messages <= previousMessages || resume.messages > len(proofs) || stored.MatchedMessages != uint32(resume.messages) || !nativeUUID(stored.LeafID) {
			return nil, historyUncertain()
		}
		seenActions[resume.action] = true
		// Hash each prefix incrementally: repeated handoffs cannot multiply the
		// aggregate byte-reading bound by the number of retained process owners.
		for start := previousBytes; start < stored.Bytes; {
			if err := ctx.Err(); err != nil {
				return nil, domain.SafeError(err)
			}
			end := min(start+32<<10, stored.Bytes)
			_, _ = hash.Write(raw[start:end])
			start = end
		}
		if hex.EncodeToString(hash.Sum(nil)) != stored.SHA256 {
			return nil, historyUncertain()
		}
		previousBytes, previousMessages = stored.Bytes, resume.messages
		if resume.messages == len(proofs) {
			// Native initialization cannot manufacture a conversation before the
			// independently accepted next input has supplied its original proof.
			if n != len(resumes)-1 || uint64(len(raw)) != stored.Bytes {
				return nil, historyUncertain()
			}
			continue
		}
		next := proofs[resume.messages]
		if next.Role != HistoryUser {
			return nil, historyUncertain()
		}
		end := uint64(len(raw))
		if n+1 < len(resumes) {
			end = resumes[n+1].transcript.Bytes
			if end <= stored.Bytes || end > uint64(len(raw)) {
				return nil, historyUncertain()
			}
		}
		parent := stored.LeafID
		wantUser := action.Status == CompactSucceeded
		if action.Status == CompactFailed {
			if stored.LeafID != action.Output.NativeID {
				return nil, historyUncertain()
			}
			// The native loader excludes local-command diagnostics from model
			// context. Their original immutable prefix remains historical proof.
			parent = action.Echo.NativeID
		} else if !wantUser {
			return nil, historyUncertain()
		}
		assistant, foundInput := false, false
		for _, line := range bytes.Split(raw[stored.Bytes:end], []byte{'\n'}) {
			if err := ctx.Err(); err != nil {
				return nil, domain.SafeError(err)
			}
			if len(line) == 0 {
				continue
			}
			var fields map[string]json.RawMessage
			var kind, id, predecessor string
			if domain.Decode(line, &fields) != nil || json.Unmarshal(fields["type"], &kind) != nil {
				return nil, historyUncertain()
			}
			if kind != "user" && kind != "assistant" {
				continue // The ordinary transcript verifier still validates these.
			}
			if json.Unmarshal(fields["uuid"], &id) != nil || !nativeUUID(id) || json.Unmarshal(fields["parentUuid"], &predecessor) != nil || predecessor != parent {
				return nil, historyUncertain()
			}
			if id == next.NativeID {
				if !assistant || wantUser || kind != "user" {
					return nil, historyUncertain()
				}
				foundInput = true
				break
			}
			if index.messages[id] || assistant {
				return nil, historyUncertain()
			}
			if wantUser {
				if kind != "user" || !nativeResumeUser(fields) {
					return nil, historyUncertain()
				}
				wantUser = false
			} else {
				if kind != "assistant" || !nativeResumeAssistant(fields) {
					return nil, historyUncertain()
				}
				assistant = true
			}
			index.messages[id], parent = true, id
		}
		if !foundInput {
			return nil, historyUncertain()
		}
		if action.Status == CompactFailed {
			index.detached[action.Output.NativeID] = true
		}
	}
	return index, nil
}

func nativeResumeUser(fields map[string]json.RawMessage) bool {
	var meta bool
	var prompt string
	var message struct {
		Role    HistoryRole `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	return json.Unmarshal(fields["isMeta"], &meta) == nil && meta && json.Unmarshal(fields["promptId"], &prompt) == nil && nativeUUID(prompt) && fields["isCompactSummary"] == nil && decodeNativeObject(fields["message"], &message) == nil && message.Role == HistoryUser && len(message.Content) == 1 && message.Content[0].Type == "text" && message.Content[0].Text == "Continue from where you left off."
}

func nativeResumeAssistant(fields map[string]json.RawMessage) bool {
	var apiError *bool
	if json.Unmarshal(fields["isApiErrorMessage"], &apiError) != nil || apiError == nil || *apiError || fields["isMeta"] != nil || fields["isCompactSummary"] != nil {
		return false
	}
	var message map[string]json.RawMessage
	if domain.Decode(fields["message"], &message) != nil {
		return false
	}
	var id string
	if json.Unmarshal(message["id"], &id) != nil || !nativeUUID(id) {
		return false
	}
	// These are exact pinned native synthetic-context fields, not provider
	// inference or usage. Outcome remains the original live command status.
	delete(message, "id")
	expected := json.RawMessage(`{"diagnostics":null,"container":null,"model":"<synthetic>","role":"assistant","stop_details":null,"stop_reason":"stop_sequence","stop_sequence":"","type":"message","usage":{"output_tokens_details":null,"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":null,"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0},"inference_geo":null,"iterations":null,"speed":null},"content":[{"type":"text","text":"No response requested."}],"context_management":null}`)
	actual, err := json.Marshal(message)
	if err != nil {
		return false
	}
	a, err := streamReplyDigest(actual)
	b, other := streamReplyDigest(expected)
	return err == nil && other == nil && a == b
}
