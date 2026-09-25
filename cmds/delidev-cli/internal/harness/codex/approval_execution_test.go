package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSingleUseApprovalExecutionRequiresExactOriginalSingleResponse(t *testing.T) {
	for _, change := range []string{"accepted", "prior-recovery", "session", "amendment", "cancel", "callback", "network", "command", "cwd", "source", "no-exit", "nonzero", "failed", "open", "no-send", "ambiguous", "foreign-item", "foreign-turn", "patch", "empty-patch"} {
		t.Run(change, func(t *testing.T) {
			fields := commandApprovalFixture()
			fields["availableDecisions"] = []any{"accept", "acceptForSession", "cancel"}
			method := "item/commandExecution/requestApproval"
			if change == "amendment" {
				fields["proposedExecpolicyAmendment"] = []string{"printf"}
				fields["availableDecisions"] = []ApprovalDecision{{Kind: ApprovalExecpolicy, Execpolicy: []string{"printf"}}}
			}
			if change == "callback" {
				fields["approvalId"] = "callback"
			}
			if change == "network" {
				fields["networkApprovalContext"] = NetworkApprovalContext{Host: "fixture.invalid", Protocol: ApprovalHTTPS}
			}
			patch := change == "patch" || change == "empty-patch"
			if patch {
				method = "item/fileChange/requestApproval"
				fields = map[string]any{"itemId": "approval-tool", "startedAtMs": 1}
			}
			c, _, turn, request := ownedApprovalFixture(t, "approvals", method, fields)
			decision := ApprovalDecision{Kind: ApprovalAccept}
			if change == "session" {
				decision.Kind = ApprovalAcceptSession
			}
			if change == "amendment" {
				decision = ApprovalDecision{Kind: ApprovalExecpolicy, Execpolicy: []string{"printf"}}
			}
			if change == "cancel" {
				decision.Kind = ApprovalCancel
			}
			if change != "no-send" {
				if _, err := c.RespondApproval(context.Background(), domain.NewID(), request.Interaction.ID, turn, decision); err != nil {
					t.Fatal(err)
				}
				if change != "open" {
					nextKind(t, c, InteractionClosedEvent)
				}
			}
			if change == "ambiguous" {
				other := approvalEvent(t, c, turn, method, commandApprovalFixture())
				other.ID = json.RawMessage(`8`)
				if _, err := c.observeEventLocked(other); err != nil {
					t.Fatal(err)
				}
			}
			if change == "prior-recovery" {
				c.problem, c.execution.paused = interactionUncertain(), true
			}
			item := commandFixture()
			item["id"], item["command"], item["status"], item["source"], item["exitCode"] = "approval-tool", "printf '%s' fixture", "completed", ExecStartupCommand, 0
			if patch {
				item = map[string]any{"type": "fileChange", "id": "approval-tool", "status": "completed", "changes": []any{map[string]any{"path": "/fixture/a", "diff": "+a", "kind": map[string]any{"type": "add"}}}}
			}
			params := map[string]any{"threadId": c.thread, "turnId": turn, "completedAtMs": 2, "item": item}
			switch change {
			case "command":
				item["command"] = "other"
			case "cwd":
				item["cwd"] = "/other"
			case "source":
				item["source"] = AgentCommand
			case "no-exit":
				item["exitCode"] = nil
			case "nonzero":
				item["exitCode"] = 1
			case "failed":
				item["status"] = "failed"
			case "foreign-item":
				item["id"] = "foreign"
			case "foreign-turn":
				params["turnId"] = domain.NewID()
			case "empty-patch":
				item["changes"] = []any{}
			}
			event, err := observeFixture(c, "item/completed", params)
			if change == "foreign-turn" {
				if err == nil {
					t.Fatal("unknown turn accepted")
				}
			} else if err != nil || event.Kind != ToolCompletedEvent {
				t.Fatal("tool observation lost", err)
			}
			status, err := c.InspectInteraction(context.Background(), request.Interaction.ID)
			expected := change == "accepted" || change == "prior-recovery" || change == "patch"
			if err != nil || status.Accepted != expected || c.execution.interactions.blocksInput() == expected {
				t.Fatal("single-use execution acceptance violated original scope", err)
			}
			if expected {
				evidence := ApprovedCommandEvidence
				if patch {
					evidence = ApprovedPatchEvidence
				}
				if status.ApprovalEvidence != evidence {
					t.Fatal("wrong acceptance profile")
				}
			}
			if change == "prior-recovery" && (c.problem == nil || !c.execution.paused) {
				t.Fatal("acceptance cleared prior recovery")
			}
		})
	}
}
