package claude

import (
	"context"
	"testing"
)

func TestFailedCheckpointRejectsChangedSettingsWithoutNativeReplay(t *testing.T) {
	for _, change := range []string{"model", "effort", "permission", "instructions"} {
		t.Run(change, func(t *testing.T) {
			s, transport := continuationFixture(t)
			s.current.terminal.Error = true
			for _, message := range s.history.messages {
				s.current.seen[message.NativeID] = true
			}
			ctx := context.Background()
			if _, err := s.FinishOriginalInput(ctx, s.config.Process.OwnerID, s.config.SessionID, s.current.input, s.current.turnID); err != nil {
				t.Fatal(err)
			}
			closed, err := s.RetainOriginalCompletion(ctx, s.config.Process.OwnerID, s.config.SessionID, s.current.input, s.current.turnID)
			if err != nil {
				t.Fatal(err)
			}
			raw, ref, err := closed.RetainCheckpoint(ctx)
			if err != nil || !ref.RequiresResume {
				t.Fatal("failed checkpoint lost its explicit Resume requirement", err)
			}
			config := s.config
			config.API = APIConfig{ServerOrigin: s.serverOrigin}
			switch change {
			case "model":
				config.Model += "-changed"
			case "effort":
				config.Effort = LowEffort
				if s.config.Effort == LowEffort {
					config.Effort = HighEffort
				}
			case "permission":
				config.Permission = DefaultPermission
				if s.config.Permission == DefaultPermission {
					config.Permission = PlanPermission
				}
			case "instructions":
				config.Instructions += "Changed instructions"
			}
			if restored, err := RestoreCheckpoint(ctx, config, raw, ref); err == nil || restored != nil {
				t.Fatal("changed settings acquired failed-history continuation")
			}
			inspection := config
			inspection.Instructions = ""
			if err := InspectCheckpoint(ctx, inspection, checkpointDigest([]byte(config.Instructions)), raw, ref); err == nil {
				t.Fatal("comparison-only inspection accepted changed settings")
			}
			if transport.sends.Load() != 0 || transport.replies.Load() != 0 || transport.interrupts.Load() != 0 {
				t.Fatal("failed-history comparison replayed native work")
			}
		})
	}
}
