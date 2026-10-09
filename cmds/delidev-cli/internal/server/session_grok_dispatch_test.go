package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestGrokFirstDispatchRetainsUnsupportedSelectionsWithoutClaiming(t *testing.T) {
	for _, scenario := range []string{"plan-options", "missing-context", "unknown-context", "small-context", "large-context", "option", "repository"} {
		t.Run(scenario, func(t *testing.T) {
			mode := domain.ExecuteMode
			if scenario == "plan-options" {
				mode = domain.PlanMode
			}
			var f *firstDispatchFixture
			if scenario == "repository" {
				f = newFirstDispatchFixtureWorkspaceProfile(t, domain.GrokBuild, mode, "/fixture/grok", "", "fixture-model", domain.Worktree)
			} else {
				f = newFirstDispatchFixtureForHarness(t, domain.GrokBuild, mode)
			}
			if scenario == "plan-options" {
				f.mutateAgent(t, func(a *domain.Agent) { a.Options.MaxConcurrency = 2 })
			}
			if scenario == "option" {
				f.mutateAgent(t, func(a *domain.Agent) { a.Options.MaxConcurrency = 2 })
			}
			if scenario == "missing-context" || scenario == "unknown-context" || scenario == "small-context" || scenario == "large-context" {
				f.mutateAgent(t, func(agent *domain.Agent) {
					m := agent.Routes[0].Model
					switch scenario {
					case "missing-context":
						m.ContextLimit = nil
					case "unknown-context":
						m.MetadataSource = domain.Unknown
					case "small-context":
						*m.ContextLimit = 1023
					case "large-context":
						*m.ContextLimit = 1_000_000_001
					}
				})
			}
			before := f.refresh(t)
			if err := f.service.dispatchExecution(context.Background(), before); domain.SafeError(err).Code != domain.Unsupported {
				t.Fatal("unsupported Grok selection dispatched", err)
			}
			state, err := store.Decode[domain.Session](f.refresh(t))
			input := inputBody(t, currentCatalogResource(t, f.accountFixture, f.change.Input))
			if err != nil || state.InitialExecution != nil || state.ActiveExecutionID != "" || input.Delivery != domain.InputQueued || input.ExecutionID != "" || state.PendingInputs != 1 || state.Problem == nil || state.Problem.Code != domain.Unsupported {
				t.Fatal("unsupported selection consumed original input/routing", err)
			}
			if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
				r, _, err := tx.Routing(f.selection.AgentID)
				if r.ID != "" {
					t.Error("rejected dispatch advanced routing")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
