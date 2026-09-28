package claude

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestClaudeReplyClaimPrecedesNativeWriteAndCannotReplay(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s, transport := sessionFixture(t)
		b := contentFixture(t)
		s.current = b
		event := interactionTool(t, b, "Bash", map[string]any{"command": "original"})
		lifecycleObserve(t, b, event)
		calls := 0
		claim := func(_ context.Context, c PermissionReplyClaim) error {
			calls++
			if transport.replies.Load() != 0 || c.Version != 1 || c.OwnerID != b.owner || c.SessionID != b.session || c.InputID != b.input || c.TurnID != b.turnID || c.ArrivalID != event.ArrivalID || c.RequestID != event.RequestID || c.ToolID != "toolu_callback" || len(c.BodyDigest) != 64 {
				t.Fatal("claim did not precede original send")
			}
			if fail {
				return lifecycleUncertain()
			}
			return nil
		}
		err := s.ReplyClaimed(context.Background(), event.ArrivalID, PermissionReply{Behavior: PermissionAllow}, claim)
		if (err != nil) != fail || calls != 1 || transport.replies.Load() != int64(1-boolInt(fail)) {
			t.Fatal("claim failure permitted transmission", err)
		}
		if s.ReplyClaimed(context.Background(), event.ArrivalID, PermissionReply{Behavior: PermissionAllow}, claim) == nil || calls != 1 {
			t.Fatal("response claim was replayed")
		}
		if s.ReplyClaimed(context.Background(), domain.NewID(), PermissionReply{Behavior: PermissionAllow}, nil) == nil {
			t.Fatal("missing claim barrier accepted")
		}
	}
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
