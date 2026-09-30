package worker

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func TestClaudeFailedCheckpointStillRequiresOriginalNativeEOFProof(t *testing.T) {
	for _, scenario := range []string{"eligible", "aborted-stream", "aborted-tools", "background-requested", "unfinished-tool", "unfinished-callback", "unsettled-callback"} {
		t.Run(scenario, func(t *testing.T) {
			c, _, command, idle := claudeTerminalFixture(t)
			// Retain the original acknowledged failed result, command and idle.
			c.resultBoundary.Error = true
			c.resultBoundary.Reason = claude.APIError
			command.Command = claude.CommandCancelled
			ctx := context.Background()
			if _, err := c.PublishBoundaryObservation(ctx, command); err != nil {
				t.Fatal(err)
			}
			if _, err := c.PublishBoundaryObservation(ctx, idle); err != nil {
				t.Fatal(err)
			}
			completion := domain.ExecutionCompletion{Version: 1, ExecutionID: c.binding.journal.ExecutionID, InputID: c.binding.journal.InputID, NativeThreadID: domain.NativeIdentity(c.binding.journal.SessionID), NativeTurnID: domain.NativeIdentity(c.binding.turn), LastSequence: c.binding.sequence, Outcome: domain.ExecutionFailed, CleanupVerified: true}
			// Model the caller's completed workspace report without fabricating
			// an original native controller. That controller must still reject it.
			c.completion = &completion
			c.messages["original"] = claudePublishedContent{}
			switch scenario {
			case "aborted-stream":
				c.terminal.Reason = domain.ClaudeAbortedStreaming
			case "aborted-tools":
				c.terminal.Reason = domain.ClaudeAbortedTools
			case "background-requested":
				c.terminal.Reason, c.terminal.Command = domain.ClaudeBackgroundRequested, domain.ClaudeCommandCompleted
				if c.terminal.Validate() != nil {
					t.Fatal("fixture must retain a valid background-requested terminal")
				}
			case "unfinished-tool":
				c.tools["original"] = claudePublishedTool{}
			case "unfinished-callback":
				c.interactions[domain.NewID()] = claudePublishedInteraction{}
			case "unsettled-callback":
				c.interactions[domain.NewID()] = claudePublishedInteraction{closed: true}
			}
			got, err := c.RetainCompletion(ctx, &claude.APISession{}, completion)
			if scenario == "eligible" {
				if err == nil || got != (domain.ExecutionCompletion{}) {
					t.Fatal("eligible failed input skipped independent native EOF verification")
				}
			} else if err != nil || got != completion {
				t.Fatal("unsupported failed history was promoted", err)
			}
			if c.checkpoint != nil {
				t.Fatal("unproved native cleanup acquired a checkpoint")
			}
		})
	}
}
