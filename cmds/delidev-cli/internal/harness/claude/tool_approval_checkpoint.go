package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Only an original allowed tool request with exact native reply echo and a
// separately settled inline result is eligible. Approval is not tool success.
// The independent whole-checkpoint digest pins callback facts that native
// conversation files do not reproduce; those files cannot reconstruct them.
type checkpointToolApproval struct {
	Arrival       domain.ID `json:"arrival_id"`
	Request       string    `json:"native_request_id"`
	Tool          string    `json:"native_tool_id"`
	Input         domain.ID `json:"input_id"`
	Turn          string    `json:"native_turn_id"`
	RequestDigest string    `json:"request_sha256"`
	ReplyDigest   string    `json:"reply_sha256"`
}

func (b *ExecutionBinding) closedToolApprovals() ([]checkpointToolApproval, error) {
	if len(b.interactions) > maxStreamIdentities {
		return nil, continuationUnavailable()
	}
	var result []checkpointToolApproval
	for arrival, value := range b.interactions {
		if value == nil {
			return nil, continuationUnavailable()
		}
		tool, exists := b.content.tools[value.request.ToolID]
		if !exists || !tool.finished || tool.inline == nil || tool.parent != "" || value.request.Kind != ToolPermission || !value.prepared || !value.echoed || value.canceled || value.behavior != PermissionAllow || value.retainedBytes != 0 || value.request.Input != nil || len(value.questions) != 0 || value.input != tool.ownerInput || value.turn != tool.ownerTurn || value.event.ArrivalID != arrival || value.event.RequestID != value.request.RequestID || value.event.Kind != NativeRequest || value.requestDigest == ([sha256.Size]byte{}) || value.reply == ([sha256.Size]byte{}) {
			return nil, continuationUnavailable()
		}
		result = append(result, checkpointToolApproval{arrival, value.request.RequestID, value.request.ToolID, value.input, value.turn, hex.EncodeToString(value.requestDigest[:]), hex.EncodeToString(value.reply[:])})
	}
	slices.SortFunc(result, func(a, b checkpointToolApproval) int { return strings.Compare(string(a.Arrival), string(b.Arrival)) })
	return result, nil
}

func (cp sessionCheckpoint) validateToolApprovals() error {
	if len(cp.ToolApprovals) > maxStreamIdentities {
		return historyUncertain()
	}
	tools := map[string]checkpointInlineTool{}
	for _, tool := range cp.inlineTools().all() {
		tools[tool.ID] = tool.checkpointInlineTool
	}
	requests, approved := map[string]bool{}, map[string]bool{}
	for i, approval := range cp.ToolApprovals {
		tool, exists := tools[approval.Tool]
		if approval.Arrival.Validate() != nil || (i > 0 && cp.ToolApprovals[i-1].Arrival >= approval.Arrival) || domain.Text(approval.Request, "native request identity", 128, true) != nil || requests[approval.Request] || approved[approval.Tool] || !exists || approval.Input != tool.Input || approval.Turn != tool.Turn || !validHistoryDigest(approval.RequestDigest) || !validHistoryDigest(approval.ReplyDigest) || approval.RequestDigest == strings.Repeat("0", 64) || approval.ReplyDigest == strings.Repeat("0", 64) {
			return historyUncertain()
		}
		requests[approval.Request], approved[approval.Tool] = true, true
	}
	return nil
}

func restoreToolApprovals(b *ExecutionBinding, approvals []checkpointToolApproval) error {
	if len(approvals) != 0 {
		b.interactions = map[domain.ID]*interactionState{}
	}
	for _, approval := range approvals {
		value := &interactionState{input: approval.Input, turn: approval.Turn, request: NativeInteraction{Kind: ToolPermission, RequestID: approval.Request, ToolID: approval.Tool}, event: StreamEvent{Kind: NativeRequest, RequestID: approval.Request, ArrivalID: approval.Arrival}, prepared: true, echoed: true, behavior: PermissionAllow}
		request, _ := hex.DecodeString(approval.RequestDigest)
		reply, _ := hex.DecodeString(approval.ReplyDigest)
		copy(value.requestDigest[:], request)
		copy(value.reply[:], reply)
		b.interactions[approval.Arrival] = value
	}
	_, err := b.closedToolApprovals()
	return err
}
