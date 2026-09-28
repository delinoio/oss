package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestClaudeFirstDispatchRetainsNativeSelectionAndOriginalInput(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) {
			f := newFirstDispatchFixtureForHarness(t, domain.ClaudeCode, mode)
			if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
				t.Fatal(err)
			}
			s, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil || s.InitialExecution == nil || s.Dispatch != domain.DispatchClaimed || s.PendingInputs != 1 {
				t.Fatal("original Claude assignment lost", err)
			}
			if !f.workerStream.Receive() || f.workerStream.Msg().Job == nil {
				t.Fatal("missing execution", f.workerStream.Err())
			}
			var job domain.Job
			var input domain.ExecutionJobInput
			if domain.Decode(f.workerStream.Msg().Job.DocumentJson, &job) != nil || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.Configuration.Harness != domain.ClaudeCode || input.Installation.Version != domain.ClaudeProtocolVersion || input.Input.Mode != mode || input.Input.Prompt != f.selection.Prompt || input.Version != 1 || input.Continuation != nil {
				t.Fatal("original Claude settings/input changed")
			}
			permission, err := input.Configuration.ClaudeAPIInputPermission(mode)
			if err != nil || (permission == domain.ClaudePermissionPlan) != (mode == domain.PlanMode) {
				t.Fatal("native permission mode changed", err)
			}
		})
	}
}

func TestClaudeFirstDispatchRejectsUnsupportedOptionsBeforeClaimingInput(t *testing.T) {
	for _, option := range []string{"subagent-model", "subagent-effort", "concurrency", "review-model", "service-tier"} {
		t.Run(option, func(t *testing.T) {
			f := newFirstDispatchFixtureForHarness(t, domain.ClaudeCode)
			f.mutateAgent(t, func(a *domain.Agent) {
				switch option {
				case "subagent-model":
					a.Options.SubagentModel = "other"
				case "subagent-effort":
					a.Options.SubagentEffort = "high"
				case "concurrency":
					a.Options.MaxConcurrency = 2
				case "review-model":
					a.Options.ApprovalReviewModel = "other"
				case "service-tier":
					a.Options.ServiceTier = "fast"
				}
			})
			before := f.refresh(t)
			if err := f.service.dispatchExecution(context.Background(), before); err == nil {
				t.Fatal("unsupported setting dispatched")
			}
			after := f.refresh(t)
			s, err := store.Decode[domain.Session](after)
			if err != nil || s.InitialExecution != nil || s.ActiveExecutionID != "" || s.PendingInputs != 1 || s.Dispatch != domain.DispatchBlocked || s.Problem == nil {
				t.Fatal("rejected selection consumed input", err)
			}
			queued := inputBody(t, currentCatalogResource(t, f.accountFixture, f.change.Input))
			if queued.Delivery != domain.InputQueued || queued.ExecutionID != "" {
				t.Fatal("rejected setting claimed native input")
			}
			if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
				route, _, err := tx.Routing(f.selection.AgentID)
				if route.ID != "" {
					t.Error("rejected setting advanced account routing")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
