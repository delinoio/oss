// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestProjectRoutingSnapshotMatchesSelectedRoute(t *testing.T) {
	for _, tc := range []struct {
		name           string
		project, agent *domain.RoutingPolicy
		ordered        bool
		want           domain.RoutingPolicy
	}{
		{name: "inherited", want: domain.RoundRobin},
		{name: "project", project: routingPolicyPointer(domain.Priority), want: domain.Priority},
		{name: "agent", project: routingPolicyPointer(domain.Priority), agent: routingPolicyPointer(domain.SequentialExhaustion), want: domain.SequentialExhaustion},
		{name: "ordered inherited", project: routingPolicyPointer(domain.Priority), ordered: true, want: domain.Priority},
		{name: "ordered explicit", project: routingPolicyPointer(domain.Priority), agent: routingPolicyPointer(domain.SequentialExhaustion), ordered: true, want: domain.SequentialExhaustion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := openTest(t)
			f := newExecutionFixture(t, s)
			projectID, repositoryID := domain.NewID(), domain.NewID()
			_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.project-routing-snapshot", projectID, func(tx *Tx) (any, error) {
				settings := domain.DefaultSettings()
				settings.DefaultRouting = domain.RoundRobin
				if _, err := tx.Put(domain.SettingsKind, domain.NewID(), 0, "", "", settings); err != nil {
					return nil, err
				}
				if _, err := tx.Put(domain.ProjectKind, projectID, 0, "", "", domain.Project{Name: "Project", Repositories: []domain.ID{repositoryID}, PrimaryRepository: repositoryID, Settings: &domain.ProjectBehavior{Routing: tc.project}}); err != nil {
					return nil, err
				}
				r, agent, err := decodeEntity[domain.Agent](tx, domain.AgentKind, f.agent)
				if err != nil {
					return nil, err
				}
				agent.Routing = tc.agent
				if tc.ordered {
					agent.Routes = []domain.AgentSourceRoute{{ModelID: agent.ModelID, Accounts: agent.Accounts, Routing: tc.agent}}
					agent.ModelID, agent.Accounts, agent.Routing = "", nil, nil
					for _, id := range f.accounts {
						ar, account, err := decodeEntity[domain.Account](tx, domain.AccountKind, id)
						if err != nil {
							return nil, err
						}
						account.Validation = &domain.AccountValidation{RequestID: domain.NewID(), ConnectionID: account.Connection.ID, ObservedAt: time.Now().UTC(), State: domain.Observed, Authentication: domain.KeylessEndpoint}
						if _, err := tx.Put(ar.Kind, ar.ID, ar.Revision, "", "", account); err != nil {
							return nil, err
						}
					}
				}
				return tx.Put(r.Kind, r.ID, r.Revision, "", "", agent)
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Read(context.Background(), func(tx *Tx) error {
				preview, err := tx.PreviewInitialExecution(domain.Session{AgentID: f.agent, MachineID: f.machine, ProjectID: projectID})
				if err != nil {
					return err
				}
				if preview.Route.Policy != tc.want || preview.Configuration.Routing != preview.Route.Policy {
					t.Fatalf("selected route %s, snapshot %s, want %s", preview.Route.Policy, preview.Configuration.Routing, tc.want)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func routingPolicyPointer(policy domain.RoutingPolicy) *domain.RoutingPolicy { return &policy }
