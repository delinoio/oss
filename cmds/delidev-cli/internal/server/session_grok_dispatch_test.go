package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestGrokFirstDispatchRetainsUnsupportedSelectionsWithoutClaiming(t *testing.T) {
	for _, scenario := range []string{"missing-context", "unknown-context", "small-context", "large-context", "option", "repository"} {
		t.Run(scenario, func(t *testing.T) {
			mode := domain.ExecuteMode
			var f *firstDispatchFixture
			if scenario == "repository" {
				f = newFirstDispatchFixtureWorkspaceProfile(t, domain.GrokBuild, mode, "/fixture/grok", "", "fixture-model", domain.Worktree)
			} else {
				f = newFirstDispatchFixtureForHarness(t, domain.GrokBuild, mode)
			}
			if scenario == "option" {
				f.mutateAgent(t, func(a *domain.Agent) { a.Options.MaxConcurrency = 2 })
			}
			if scenario == "missing-context" || scenario == "unknown-context" || scenario == "small-context" || scenario == "large-context" {
				var agent domain.Agent
				if domain.Decode(f.agent.DocumentJson, &agent) != nil {
					t.Fatal("invalid fixture Agent")
				}
				r := currentCatalogResource(t, f.accountFixture, &pb.Resource{Kind: pb.EntityKind_ENTITY_KIND_MODEL, Id: string(agent.ModelID)})
				var m domain.Model
				if domain.Decode(r.DocumentJson, &m) != nil {
					t.Fatal("invalid fixture model")
				}
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
				raw, _ := json.Marshal(m)
				if _, err := f.config.SaveConfiguration(context.Background(), ownerRequest(f.identity, &pb.SaveConfigurationRequest{Mutation: acctMutation(r, domain.NewID()), Kind: pb.EntityKind_ENTITY_KIND_MODEL, SchemaVersion: 1, DocumentJson: raw})); err != nil {
					t.Fatal(err)
				}
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

func TestGrokFirstPlanDispatchKeepsOriginalAssignment(t *testing.T) {
	f := newFirstDispatchFixtureForHarness(t, domain.GrokBuild, domain.PlanMode)
	if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	r := f.refresh(t)
	s, err := store.Decode[domain.Session](r)
	if err != nil || s.InitialExecution == nil || s.InitialExecution.Configuration.Harness != domain.GrokBuild || s.InitialExecution.Configuration.GrokContext == nil || s.Dispatch != domain.DispatchClaimed {
		t.Fatal("Plan did not claim the original first-input profile", err)
	}
	replay, err := sessionClient(f.accountFixture).CreateSession(context.Background(), ownerRequest(f.identity, f.request))
	if err != nil || replay.Msg.Change.ExecutionJob == nil {
		t.Fatal("missing immutable Plan assignment", err)
	}
	var job domain.Job
	var input domain.ExecutionJobInput
	if domain.Decode(replay.Msg.Change.ExecutionJob.DocumentJson, &job) != nil || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.Input.Mode != domain.PlanMode || input.Continuation != nil || input.Configuration.Instructions != "" {
		t.Fatal("Plan assignment acquired common instructions or continuation")
	}
}
