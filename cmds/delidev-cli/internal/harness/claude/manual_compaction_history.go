package claude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// HistoryCompactionActionProof describes a previously validated live command.
// Native command messages are not conversation inputs. Failed commands store a
// local_command representation with the original envelope UUID, rather than an
// assistant message with its separate provider-message UUID. Retain identity
// and content evidence without manufacturing the missing assistant record.
// These proofs do not establish completion, file ownership or Resume authority.
type HistoryCompactionActionProof struct {
	ActionID         domain.ID           `json:"action_id"`
	PriorMessageID   string              `json:"prior_message_id"`
	Status           CompactResult       `json:"status"`
	Echo             HistoryMessageProof `json:"echo"`
	Output           HistoryMessageProof `json:"output"`
	BoundaryID       string              `json:"boundary_id,omitempty"`
	DiagnosticSHA256 string              `json:"diagnostic_sha256,omitempty"`
}

// ObserveCompactionActionHistory accepts original envelopes from an already
// validated, settled manual action. Status comes from that action's native
// compact status, never from its outer success result or human-readable text.
func ObserveCompactionActionHistory(echo, output StreamEvent, session, action domain.ID, prior string, status CompactResult, boundary string) (HistoryCompactionActionProof, error) {
	echoProof, err := ObserveMainHistoryMessage(echo, session)
	if err != nil || action.Validate() != nil || echoProof.NativeID != string(action) || echoProof.Role != HistoryUser || !nativeUUID(prior) || prior == string(action) {
		return HistoryCompactionActionProof{}, historyUncertain()
	}
	var envelope struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(echo.Body, &envelope) != nil {
		return HistoryCompactionActionProof{}, historyUncertain()
	}
	parts, err := nativeCommandElements(envelope.Message.Content, []string{"command-name", "command-message", "command-args"})
	if err != nil || parts[0] != "/compact" || parts[1] != "compact" || parts[2] != "" {
		return HistoryCompactionActionProof{}, historyUncertain()
	}
	outputProof, err := ObserveMainHistoryMessage(output, session)
	if err != nil || outputProof.NativeID == string(action) || outputProof.NativeID == prior {
		return HistoryCompactionActionProof{}, historyUncertain()
	}
	proof := HistoryCompactionActionProof{ActionID: action, PriorMessageID: prior, Status: status, Echo: echoProof, Output: outputProof, BoundaryID: boundary}
	switch status {
	case CompactSucceeded:
		if outputProof.Role != HistoryUser || !nativeUUID(boundary) || boundary == string(action) || boundary == prior || boundary == outputProof.NativeID || json.Unmarshal(output.Body, &envelope) != nil {
			return HistoryCompactionActionProof{}, historyUncertain()
		}
		if _, err := nativeCommandElements(envelope.Message.Content, []string{"local-command-stdout"}); err != nil {
			return HistoryCompactionActionProof{}, historyUncertain()
		}
	case CompactFailed:
		binding := manualCompactionBinding{core: &ExecutionBinding{initialized: true}, status: CompactFailed}
		if outputProof.Role != HistoryAssistant || boundary != "" {
			return HistoryCompactionActionProof{}, historyUncertain()
		}
		if _, err := binding.observeDiagnostic(output.Body); err != nil {
			return HistoryCompactionActionProof{}, historyUncertain()
		}
		proof.DiagnosticSHA256 = hex.EncodeToString(binding.diagnosticDigest[:])
	default:
		return HistoryCompactionActionProof{}, historyUncertain()
	}
	return proof, nil
}

// VerifyMainTranscriptWithActions proves command ancestry separately from the
// original conversation and compaction context. Unforwarded native caveats stay
// additional messages. No synthetic live diagnostic is manufactured from disk.
func VerifyMainTranscriptWithActions(ctx context.Context, raw []byte, session domain.ID, workspace string, messages []HistoryMessageProof, compactions []HistoryCompactionProof, actions []HistoryCompactionActionProof) (TranscriptObservation, error) {
	if len(actions) == 0 || len(actions) > maxStreamIdentities {
		return TranscriptObservation{}, historyUncertain()
	}
	return verifyTranscript(ctx, raw, session, workspace, messages, nil, compactions, actions)
}

type historyActionIndex struct {
	proofs      []HistoryCompactionActionProof
	messages    map[string]HistoryMessageProof
	diagnostics map[string]HistoryCompactionActionProof
	boundaries  map[string]HistoryCompactionActionProof
	stored      map[string]string
	matched     map[string]bool
}

func indexHistoryActions(proofs []HistoryCompactionActionProof, messages map[string]HistoryMessageProof, boundaries, summaries map[string]HistoryCompactionProof) (*historyActionIndex, error) {
	index := &historyActionIndex{proofs: proofs, messages: map[string]HistoryMessageProof{}, diagnostics: map[string]HistoryCompactionActionProof{}, boundaries: map[string]HistoryCompactionActionProof{}, stored: map[string]string{}, matched: map[string]bool{}}
	seen := map[string]bool{}
	for _, p := range proofs {
		if p.ActionID.Validate() != nil || messages[p.PriorMessageID].NativeID == "" || p.Echo.NativeID != string(p.ActionID) || p.Echo.Role != HistoryUser || p.Echo.NativeID == p.Output.NativeID {
			return nil, historyUncertain()
		}
		for _, m := range []HistoryMessageProof{p.Echo, p.Output} {
			if !nativeUUID(m.NativeID) || m.ParentToolID != "" || !validHistoryDigest(m.PayloadSHA256) || seen[m.NativeID] || messages[m.NativeID].NativeID != "" || boundaries[m.NativeID].NativeID != "" || summaries[m.NativeID].NativeID != "" {
				return nil, historyUncertain()
			}
			seen[m.NativeID] = true
		}
		index.messages[p.Echo.NativeID] = p.Echo
		switch p.Status {
		case CompactSucceeded:
			boundary := boundaries[p.BoundaryID]
			if p.Output.Role != HistoryUser || p.DiagnosticSHA256 != "" || boundary.NativeID == "" || boundary.LogicalParentID != p.PriorMessageID || index.boundaries[p.BoundaryID].ActionID != "" {
				return nil, historyUncertain()
			}
			index.messages[p.Output.NativeID], index.boundaries[p.BoundaryID] = p.Output, p
		case CompactFailed:
			if p.Output.Role != HistoryAssistant || !validHistoryDigest(p.DiagnosticSHA256) || p.BoundaryID != "" {
				return nil, historyUncertain()
			}
			index.diagnostics[p.Echo.NativeID] = p
		default:
			return nil, historyUncertain()
		}
	}
	return index, nil
}

func (index *historyActionIndex) observeDiagnostic(fields map[string]json.RawMessage, id, parent string) error {
	proof, ok := index.diagnostics[parent]
	var subtype, content, level, timestamp string
	var meta *bool
	if !ok || index.stored[parent] != "" || id != proof.Output.NativeID || json.Unmarshal(fields["subtype"], &subtype) != nil || subtype != "local_command" || json.Unmarshal(fields["content"], &content) != nil || json.Unmarshal(fields["level"], &level) != nil || level != "info" || json.Unmarshal(fields["isMeta"], &meta) != nil || meta == nil || *meta || json.Unmarshal(fields["timestamp"], &timestamp) != nil || fields["message"] != nil || fields["compactMetadata"] != nil || fields["logicalParentUuid"] != nil {
		return historyUncertain()
	}
	if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
		return historyUncertain()
	}
	// Native provider failure and insufficient-history diagnostics use different
	// wrapper tags. Neither tag supplies the already observed action outcome.
	parts, err := nativeCommandElements(content, []string{"local-command-stderr"})
	if err != nil {
		parts, err = nativeCommandElements(content, []string{"local-command-stdout"})
	}
	if err != nil {
		return historyUncertain()
	}
	digest := sha256.Sum256([]byte(parts[0]))
	if hex.EncodeToString(digest[:]) != proof.DiagnosticSHA256 {
		return historyUncertain()
	}
	index.stored[parent] = id
	return nil
}

func (index *historyActionIndex) verify(ctx context.Context, nodes map[string]historyNode, provenance map[string]bool, compactions []historyCompaction) error {
	boundaries := make(map[string]historyCompaction, len(compactions))
	for _, boundary := range compactions {
		boundaries[boundary.proof.NativeID] = boundary
		if boundary.metadata.Trigger == ManualCompaction && index.boundaries[boundary.proof.NativeID].ActionID == "" {
			return historyUncertain()
		}
	}
	last := -1
	for _, p := range index.proofs {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		echo := nodes[p.Echo.NativeID]
		meta := nodes[echo.parent]
		parentMatches := meta.parent == p.PriorMessageID
		if boundary, ok := boundaries[p.BoundaryID]; ok && p.Status == CompactSucceeded {
			// Native batched persistence can attach the caveat either to the
			// preserved prior tail or directly to this original summary. Both
			// resolve to the same context after the proved native relink.
			parentMatches = parentMatches || meta.parent == boundary.proof.SummaryID
		}
		if !index.matched[p.Echo.NativeID] || !provenance[p.Echo.NativeID] || echo.position <= last || meta.role != HistoryUser || !meta.meta || !parentMatches || index.messages[echo.parent].NativeID != "" {
			return historyUncertain()
		}
		output := p.Output.NativeID
		if p.Status == CompactSucceeded {
			if !index.matched[output] {
				return historyUncertain()
			}
			boundary, ok := boundaries[p.BoundaryID]
			if !ok || boundary.metadata.Trigger != ManualCompaction || nodes[boundary.proof.SummaryID].position >= meta.position {
				return historyUncertain()
			}
			// The exact preserved tail must still be the preceding conversation,
			// regardless of which of the two native caveat parents was persisted.
			if boundary.metadata.Segment != nil && boundary.metadata.Segment.Tail != p.PriorMessageID {
				return historyUncertain()
			}
			if m := boundary.metadata.Messages; m != nil && (len(m.IDs) == 0 || m.IDs[len(m.IDs)-1] != p.PriorMessageID) {
				return historyUncertain()
			}
		} else {
			output = index.stored[p.Echo.NativeID]
			if output == "" || nodes[p.Output.NativeID].role != "local-command" {
				return historyUncertain()
			}
		}
		if nodes[output].parent != p.Echo.NativeID || !provenance[output] {
			return historyUncertain()
		}
		last = nodes[output].position
	}
	return nil
}
