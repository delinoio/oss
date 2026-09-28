package claude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// ChildHistoryBinding comes from the original task/tool ledger. A filename,
// sidecar, model-proposed agent ID or background level cannot supply ownership.
type ChildHistoryBinding struct {
	TaskID        string
	ToolID        string
	ParentAgentID string
	AgentType     string
}

type ChildTranscriptObservation struct {
	Transcript     TranscriptObservation
	MetadataSHA256 string
	SpawnDepth     uint32
}

// VerifyChildTranscript binds the original task to native child records and
// their sidecar. The pinned SDK selects the last user/assistant as child leaf;
// child files have no main last-prompt marker. Unforwarded final child messages
// remain explicit additional observations, not manufactured streaming events.
func VerifyChildTranscript(ctx context.Context, raw, metadata []byte, session domain.ID, workspace string, binding ChildHistoryBinding, proofs []HistoryMessageProof) (ChildTranscriptObservation, error) {
	if domain.Text(binding.TaskID, "native child task", 1024, true) != nil || domain.Text(binding.ToolID, "native child tool", 1024, true) != nil || domain.Text(binding.ParentAgentID, "native parent agent", 1024, false) != nil || domain.Text(binding.AgentType, "native child agent type", 256, true) != nil || len(metadata) > 64<<10 {
		return ChildTranscriptObservation{}, historyUncertain()
	}
	if err := ctx.Err(); err != nil {
		return ChildTranscriptObservation{}, domain.SafeError(err)
	}
	var value struct {
		ToolID        string  `json:"toolUseId"`
		ParentAgentID *string `json:"parentAgentId"`
		AgentType     string  `json:"agentType"`
		Description   *string `json:"description"`
		SpawnDepth    *uint32 `json:"spawnDepth"`
	}
	if decodeNativeObject(metadata, &value) != nil || value.ToolID != binding.ToolID || value.AgentType != binding.AgentType || value.Description == nil || domain.Text(*value.Description, "native child description", 16<<10, false) != nil || value.SpawnDepth == nil || *value.SpawnDepth == 0 || *value.SpawnDepth > 128 {
		return ChildTranscriptObservation{}, historyUncertain()
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(metadata, &fields)
	if binding.ParentAgentID == "" {
		if fields["parentAgentId"] != nil || *value.SpawnDepth != 1 {
			return ChildTranscriptObservation{}, historyUncertain()
		}
	} else if value.ParentAgentID == nil || *value.ParentAgentID != binding.ParentAgentID || *value.SpawnDepth < 2 {
		return ChildTranscriptObservation{}, historyUncertain()
	}
	transcript, err := verifyTranscript(ctx, raw, session, workspace, proofs, &binding, nil, nil)
	if err != nil {
		return ChildTranscriptObservation{}, err
	}
	digest := sha256.Sum256(metadata)
	return ChildTranscriptObservation{Transcript: transcript, MetadataSHA256: hex.EncodeToString(digest[:]), SpawnDepth: *value.SpawnDepth}, nil
}
