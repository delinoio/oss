package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type HistoryRole string

const (
	HistoryUser          HistoryRole = "user"
	HistoryAssistant     HistoryRole = "assistant"
	maxHistoryTranscript             = 64 << 20
	maxHistoryRecords                = 65536
)

// HistoryMessageProof retains only original native identity and a digest of
// conversation-bearing fields. Provider usage/stop metadata can be finalized
// after a completed block was streamed; neither snapshot is a billing source.
// This proof alone does not authorize loading a file or resuming execution.
type HistoryMessageProof struct {
	NativeID      string      `json:"native_id"`
	Role          HistoryRole `json:"role"`
	PayloadSHA256 string      `json:"payload_sha256"`
}

type MainTranscriptObservation struct {
	SHA256             string
	Bytes              uint64
	LeafID             string
	MatchedMessages    uint32
	AdditionalMessages uint32
}

func historyUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "Claude Code retained history does not match its observed conversation.", "Preserve the native runtime and original input evidence; do not replace history or resend input.")
}

func canonicalHistoryPayload(raw json.RawMessage) (HistoryRole, [sha256.Size]byte, error) {
	var fields map[string]json.RawMessage
	var role HistoryRole
	if domain.Decode(raw, &fields) != nil || json.Unmarshal(fields["role"], &role) != nil || (role != HistoryUser && role != HistoryAssistant) || len(fields["content"]) == 0 || bytes.Equal(fields["content"], []byte("null")) {
		return "", [sha256.Size]byte{}, historyUncertain()
	}
	var content any
	if json.Unmarshal(fields["content"], &content) != nil {
		return "", [sha256.Size]byte{}, historyUncertain()
	}
	switch content.(type) {
	case string, []any:
	default:
		return "", [sha256.Size]byte{}, historyUncertain()
	}
	selected := map[string]json.RawMessage{"role": fields["role"], "content": fields["content"]}
	if role == HistoryAssistant {
		for _, key := range []string{"id", "model"} {
			var value string
			if json.Unmarshal(fields[key], &value) != nil || domain.Text(value, "native history message identity", 1024, true) != nil {
				return "", [sha256.Size]byte{}, historyUncertain()
			}
			selected[key] = fields[key]
		}
	}
	canonical, err := json.Marshal(selected)
	if err != nil {
		return "", [sha256.Size]byte{}, historyUncertain()
	}
	digest, err := streamReplyDigest(canonical)
	if err != nil {
		return "", [sha256.Size]byte{}, historyUncertain()
	}
	return role, digest, nil
}

// ObserveMainHistoryMessage accepts only an already validated, complete native
// root user/assistant envelope. Child files need separate parent/task ownership;
// a child snapshot cannot be relabeled as a main conversation record.
func ObserveMainHistoryMessage(event StreamEvent, session domain.ID) (HistoryMessageProof, error) {
	var envelope struct {
		Type    HistoryRole     `json:"type"`
		ID      string          `json:"uuid"`
		Session domain.ID       `json:"session_id"`
		Message json.RawMessage `json:"message"`
	}
	// Other native envelope fields remain owned by the typed lifecycle adapter.
	var fields map[string]json.RawMessage
	if event.Kind != NativeMessage || (event.Type != "user" && event.Type != "assistant") || session.Validate() != nil || domain.Decode(event.Body, &fields) != nil {
		return HistoryMessageProof{}, historyUncertain()
	}
	if json.Unmarshal(fields["type"], &envelope.Type) != nil || json.Unmarshal(fields["uuid"], &envelope.ID) != nil || json.Unmarshal(fields["session_id"], &envelope.Session) != nil || envelope.Session != session || !nativeUUID(envelope.ID) || string(envelope.Type) != event.Type || !bytes.Equal(bytes.TrimSpace(fields["parent_tool_use_id"]), []byte("null")) {
		return HistoryMessageProof{}, historyUncertain()
	}
	envelope.Message = fields["message"]
	role, digest, err := canonicalHistoryPayload(envelope.Message)
	if err != nil || role != envelope.Type {
		return HistoryMessageProof{}, historyUncertain()
	}
	return HistoryMessageProof{NativeID: envelope.ID, Role: role, PayloadSHA256: hex.EncodeToString(digest[:])}, nil
}

func validHistoryDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size && hex.EncodeToString(raw) == value
}

// VerifyMainTranscript compares original message proofs against a complete
// bounded native JSONL snapshot and proves their order on the selected parent
// chain. It never rewrites native bytes or resolves a path. Additional native
// messages are explicit incomplete observation, not accepted product inputs.
// The caller must independently prove private file/process ownership, durable
// publication, every child/auxiliary file and current account/configuration.
func VerifyMainTranscript(ctx context.Context, raw []byte, session domain.ID, workspace string, proofs []HistoryMessageProof) (MainTranscriptObservation, error) {
	if err := ctx.Err(); err != nil {
		return MainTranscriptObservation{}, domain.SafeError(err)
	}
	if len(raw) == 0 || len(raw) > maxHistoryTranscript || raw[len(raw)-1] != '\n' || session.Validate() != nil || !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace || domain.Text(workspace, "native history workspace", 4096, true) != nil || len(proofs) == 0 || len(proofs) > maxStreamIdentities {
		return MainTranscriptObservation{}, historyUncertain()
	}
	expected := map[string]HistoryMessageProof{}
	for _, proof := range proofs {
		if !nativeUUID(proof.NativeID) || (proof.Role != HistoryUser && proof.Role != HistoryAssistant) || !validHistoryDigest(proof.PayloadSHA256) || expected[proof.NativeID].NativeID != "" {
			return MainTranscriptObservation{}, historyUncertain()
		}
		expected[proof.NativeID] = proof
	}
	lines := bytes.Split(raw[:len(raw)-1], []byte{'\n'})
	if len(lines) > maxHistoryRecords {
		return MainTranscriptObservation{}, historyUncertain()
	}
	type node struct {
		parent string
		role   HistoryRole
	}
	nodes := map[string]node{}
	matched := map[string]bool{}
	var leaf string
	additional := uint32(0)
	for _, line := range lines {
		if err := ctx.Err(); err != nil {
			return MainTranscriptObservation{}, domain.SafeError(err)
		}
		var fields map[string]json.RawMessage
		if domain.Decode(line, &fields) != nil || fields == nil {
			return MainTranscriptObservation{}, historyUncertain()
		}
		var kind string
		var storedSession domain.ID
		if json.Unmarshal(fields["type"], &kind) != nil || json.Unmarshal(fields["sessionId"], &storedSession) != nil || storedSession != session {
			return MainTranscriptObservation{}, historyUncertain()
		}
		switch kind {
		case "queue-operation", "atis-latch":
			// These records have no conversation identity or parent linkage. Keep
			// their exact bytes in the file digest rather than projecting contents.
			if fields["uuid"] != nil || fields["parentUuid"] != nil {
				return MainTranscriptObservation{}, historyUncertain()
			}
			continue
		case "last-prompt":
			if json.Unmarshal(fields["leafUuid"], &leaf) != nil || !nativeUUID(leaf) {
				return MainTranscriptObservation{}, historyUncertain()
			}
			continue
		case "user", "assistant", "attachment":
		default:
			return MainTranscriptObservation{}, historyUncertain()
		}
		var id, version, cwd string
		var parent *string
		var sidechain *bool
		if json.Unmarshal(fields["uuid"], &id) != nil || !nativeUUID(id) || nodes[id].role != "" || json.Unmarshal(fields["parentUuid"], &parent) != nil || fields["parentUuid"] == nil || json.Unmarshal(fields["isSidechain"], &sidechain) != nil || sidechain == nil || *sidechain || json.Unmarshal(fields["version"], &version) != nil || version != SupportedVersion || json.Unmarshal(fields["cwd"], &cwd) != nil || cwd != workspace {
			return MainTranscriptObservation{}, historyUncertain()
		}
		parentID := ""
		if parent != nil {
			parentID = *parent
			if !nativeUUID(parentID) || nodes[parentID].role == "" {
				return MainTranscriptObservation{}, historyUncertain()
			}
		}
		role := HistoryRole(kind)
		if kind != "attachment" {
			observed, digest, err := canonicalHistoryPayload(fields["message"])
			if err != nil || string(observed) != kind {
				return MainTranscriptObservation{}, historyUncertain()
			}
			if proof, ok := expected[id]; ok {
				if proof.Role != observed || proof.PayloadSHA256 != hex.EncodeToString(digest[:]) {
					return MainTranscriptObservation{}, historyUncertain()
				}
				matched[id] = true
			} else {
				additional++
			}
		} else if expected[id].NativeID != "" {
			return MainTranscriptObservation{}, historyUncertain()
		}
		nodes[id] = node{parent: parentID, role: role}
	}
	if len(matched) != len(proofs) || nodes[leaf].role == "" {
		return MainTranscriptObservation{}, historyUncertain()
	}
	// Walk once from the native selected leaf; a matched message on an abandoned
	// branch is insufficient. Parent-before-child validation excludes cycles.
	index := len(proofs) - 1
	for id := leaf; id != ""; id = nodes[id].parent {
		if matched[id] {
			if index < 0 || proofs[index].NativeID != id {
				return MainTranscriptObservation{}, historyUncertain()
			}
			index--
		}
	}
	if index != -1 {
		return MainTranscriptObservation{}, historyUncertain()
	}
	digest := sha256.Sum256(raw)
	return MainTranscriptObservation{SHA256: hex.EncodeToString(digest[:]), Bytes: uint64(len(raw)), LeafID: leaf, MatchedMessages: uint32(len(proofs)), AdditionalMessages: additional}, nil
}
