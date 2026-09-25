package codex

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const (
	ApprovedCommandEvidence ApprovalEvidence = "native-approved-command"
	ApprovedPatchEvidence   ApprovalEvidence = "native-approved-patch"
)

// Only the single-use accept decision has no remembered-policy side effect to
// verify. The pinned runtime cannot produce successful execution after that
// original pending approval without consuming an approving response. Decline,
// cancellation, failed tools, callback/network approvals and session/policy
// decisions need different evidence and deliberately remain unconfirmed.
func (owned *trackedInteraction) retainSingleUseApproval(raw []byte) {
	if owned.approval == nil {
		return
	}
	var response struct {
		Decision ApprovalDecision `json:"decision"`
	}
	if domain.Decode(raw, &response) != nil || response.Decision.Kind != ApprovalAccept {
		return
	}
	switch owned.approval.Kind {
	case CommandApproval:
		request := owned.approval.Command
		if request == nil || request.Kind != ExecuteCommandApproval || request.ApprovalID != nil || request.Network != nil || request.Command == nil || request.Cwd == nil {
			return
		}
		owned.approvedTool = CommandTool
		owned.approvedCommand = approvalCommandDigest(*request.Command, *request.Cwd)
	case FileApproval:
		owned.approvedTool = PatchTool
	}
}

func approvalCommandDigest(command, cwd string) [32]byte {
	raw, _ := json.Marshal([2]string{command, cwd})
	return sha256.Sum256(raw)
}

func (c *Client) observeSingleUseApprovalLocked(turn domain.ID, tool *Tool) {
	if tool.Status != ToolCompleted {
		return
	}
	var owned *trackedInteraction
	matches := 0
	for _, candidate := range c.execution.interactions.arrivals {
		if candidate.status.TurnID == turn && candidate.status.ItemID == tool.ID {
			matches++
			owned = candidate
		}
	}
	if matches != 1 || owned == nil || owned.kind != ApprovalInteraction || owned.approvedTool != tool.Kind || owned.status.Accepted || owned.status.Closure != InteractionNativeClosed || owned.status.ResponseID == "" || owned.status.Delivery == QuestionNotSent {
		return
	}
	var evidence ApprovalEvidence
	switch tool.Kind {
	case CommandTool:
		command := tool.Command
		if command == nil || command.Source != ExecStartupCommand || command.ExitCode == nil || *command.ExitCode != 0 || approvalCommandDigest(command.Command, command.Cwd) != owned.approvedCommand {
			return
		}
		evidence = ApprovedCommandEvidence
	case PatchTool:
		if len(tool.Changes) == 0 {
			return
		}
		evidence = ApprovedPatchEvidence
	default:
		return
	}
	owned.confirmAcceptance(evidence)
	if c.logger != nil {
		c.logger.Info("Codex single-use approval execution confirmed", "owner_id", c.ownerID, "interaction_id", owned.status.ID, "response_id", owned.status.ResponseID, "turn_id", turn, "evidence", evidence)
	}
}
