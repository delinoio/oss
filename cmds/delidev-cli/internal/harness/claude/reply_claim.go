package claude

import (
	"encoding/hex"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// No request or answer content enters the Worker ownership journal.
type PermissionReplyClaim struct {
	Version    uint32    `json:"version"`
	OwnerID    domain.ID `json:"owner_id"`
	SessionID  domain.ID `json:"session_id"`
	InputID    domain.ID `json:"input_id"`
	TurnID     string    `json:"native_turn_id"`
	ArrivalID  domain.ID `json:"arrival_id"`
	RequestID  string    `json:"native_request_id"`
	ToolID     string    `json:"native_tool_id"`
	BodyDigest string    `json:"body_sha256"`
}

func (b *ExecutionBinding) permissionReplyClaim(arrival domain.ID) (PermissionReplyClaim, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.interactions[arrival]
	if b.problem != nil || r == nil || !r.prepared || r.echoed || r.canceled || r.event.ArrivalID != arrival {
		return PermissionReplyClaim{}, lifecycleUncertain()
	}
	return PermissionReplyClaim{1, b.owner, b.session, r.input, r.turn, arrival, r.request.RequestID, r.request.ToolID, hex.EncodeToString(r.reply[:])}, nil
}
