package server

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestOpenCodeFirstDispatchRetainsExactNativeSelection(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) {
			f := newFirstDispatchFixtureForHarness(t, domain.OpenCode, mode)
			if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil || session.InitialExecution == nil || session.InitialExecution.Configuration.Harness != domain.OpenCode || session.Dispatch != domain.DispatchClaimed || session.PendingInputs != 1 {
				t.Fatal("first dispatch lost original native selection")
			}
			if !f.workerStream.Receive() || f.workerStream.Msg().Job == nil {
				t.Fatal("original Worker did not receive the dispatched execution", f.workerStream.Err())
			}
			record := f.workerStream.Msg().Job
			var job domain.Job
			var input domain.ExecutionJobInput
			if domain.Decode(record.DocumentJson, &job) != nil || job.Type != domain.ExecuteSessionJob || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.Configuration.Harness != domain.OpenCode || input.Installation.Version != domain.OpenCodeProtocolVersion || input.Installation.Protocol.Protocol != domain.OpenCodeHTTP || input.Input.Mode != mode || input.Input.Prompt != f.selection.Prompt || input.ExecutionID != session.InitialExecution.ID || input.ConfigurationDigest != session.InitialExecution.ConfigurationDigest || input.Continuation != nil {
				t.Fatal("dispatched assignment changed first input/settings ownership")
			}
			agent, err := input.Configuration.OpenCodePrimaryForInput(mode)
			if err != nil || (mode == domain.PlanMode) != (agent == domain.OpenCodePlanAgent) {
				t.Fatal("Plan/Build selection changed")
			}
		})
	}
}

func TestOpenCodeFirstDispatchRefusalDoesNotConsumeRoutingOrInput(t *testing.T) {
	for _, failure := range []string{"effort", "subagent-model", "subagent-effort", "concurrency", "review-model", "service-tier", "version", "protocol", "validation", "worker-stale", "provider-protocol"} {
		t.Run(failure, func(t *testing.T) {
			f := newFirstDispatchFixtureForHarness(t, domain.OpenCode)
			switch failure {
			case "effort", "subagent-model", "subagent-effort", "concurrency", "review-model", "service-tier":
				f.mutateAgent(t, func(a *domain.Agent) {
					switch failure {
					case "effort":
						a.Effort = "high"
					case "subagent-model":
						a.Options.SubagentModel = "another-model"
					case "subagent-effort":
						a.Options.SubagentEffort = "high"
					case "concurrency":
						a.Options.MaxConcurrency = 2
					case "review-model":
						a.Options.ApprovalReviewModel = "another-model"
					case "service-tier":
						a.Options.ServiceTier = "fast"
					}
				})
			default:
				_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.invalidate-opencode-dispatch", failure, func(tx *store.Tx) (any, error) {
					switch failure {
					case "validation":
						r, a, err := accountFromTx(tx, domain.ID(f.account.Id), 0)
						if err != nil {
							return nil, err
						}
						a.Validation.ConnectionID = domain.NewID()
						return tx.Put(r.Kind, r.ID, r.Revision, "", "", a)
					case "worker-stale":
						instance, _, err := tx.WorkerInstance(f.selection.MachineID)
						if err != nil {
							return nil, err
						}
						return nil, tx.SetWorkerInstance(f.selection.MachineID, instance, time.Now().Add(-time.Hour))
					case "provider-protocol":
						_, a, err := accountFromTx(tx, domain.ID(f.account.Id), 0)
						if err != nil {
							return nil, err
						}
						r, err := tx.Get(domain.ProviderKind, a.ProviderID)
						if err != nil {
							return nil, err
						}
						p, err := store.Decode[domain.Provider](r)
						if err != nil {
							return nil, err
						}
						p.Protocol = domain.OpenAIResponses
						return tx.Put(r.Kind, r.ID, r.Revision, "", "", p)
					default:
						r, m, err := activeMachine(tx, f.selection.MachineID)
						if err != nil {
							return nil, err
						}
						for i := range m.Installations {
							v := &m.Installations[i]
							if v.Harness != domain.OpenCode {
								continue
							}
							if failure == "version" {
								v.Version = "1.18.31"
							}
							if failure == "protocol" {
								v.Protocol.Protocol = domain.CodexAppServer
							}
						}
						return tx.Put(r.Kind, r.ID, r.Revision, "", "", m)
					}
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err == nil {
				t.Fatal("unsupported OpenCode profile dispatched")
			}
			session, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil || session.InitialExecution != nil || session.ActiveExecutionID != "" || session.PendingInputs != 1 {
				t.Fatal("failed dispatch consumed original session/input")
			}
			queued := inputBody(t, currentCatalogResource(t, f.accountFixture, f.change.Input))
			if queued.Delivery != domain.InputQueued || queued.ExecutionID != "" {
				t.Fatal("failed dispatch claimed original input")
			}
			if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
				route, _, err := tx.Routing(f.selection.AgentID)
				if route.ID != "" {
					t.Error("failed dispatch advanced account routing")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
