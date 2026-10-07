// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestSubscriptionDispatchRequiresManagedWorkerCapability(t *testing.T) {
	for _, permission := range []domain.PermissionMode{domain.PermissionDefault, domain.PermissionReadOnly, domain.PermissionWorkspaceWrite, domain.PermissionFullAccess} {
		for _, capable := range []bool{false, true} {
			name := "unavailable"
			if capable {
				name = "negotiated"
			}
			t.Run(string(permission)+"/"+name, func(t *testing.T) {
				f := newFirstDispatchFixture(t)
				ctx := context.Background()
				_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.subscription-readiness", capable, func(tx *store.Tx) (any, error) {
					ar, a, err := accountFromTx(tx, domain.ID(f.account.Id), 0)
					if err != nil {
						return nil, err
					}

					agents, err := tx.List(store.Filter{Kind: domain.AgentKind, Limit: 1})
					if err != nil || len(agents) != 1 {
						return nil, err
					}
					agent, err := store.Decode[domain.Agent](agents[0])
					if err != nil {
						return nil, err
					}
					agent.Options.Permission = permission
					if _, err := tx.Put(domain.AgentKind, agents[0].ID, agents[0].Revision, "", "", agent); err != nil {
						return nil, err
					}

					// Synthetic server-owned readiness exercises admission only; no
					// bundle, native login or inference is created by this fixture.
					a.ProviderID, a.SubscriptionService = "", domain.SubscriptionChatGPT
					a.Type, a.Connection.Authentication, a.Validation = domain.SubscriptionAccount, domain.SubscriptionAuth, nil
					a.Subscription = &domain.SubscriptionState{Generation: domain.NewID(), IdentityCommitment: strings.Repeat("a", 64)}
					if _, err := tx.Put(domain.AccountKind, ar.ID, ar.Revision, "", "", a); err != nil {
						return nil, err
					}
					models, err := tx.List(store.Filter{Kind: domain.ModelKind, Limit: 1})
					if err != nil || len(models) != 1 {
						return nil, err
					}
					model, err := store.Decode[domain.Model](models[0])
					if err != nil {
						return nil, err
					}
					model.ProviderID, model.SourceKind, model.SubscriptionService = "", domain.SubscriptionModel, domain.SubscriptionChatGPT
					if _, err = tx.Put(domain.ModelKind, models[0].ID, models[0].Revision, "", "", model); err != nil {
						return nil, err
					}

					mr, machine, err := activeMachine(tx, domain.ID(f.machine.Id))
					if err != nil {
						return nil, err
					}
					machine.Installations = nil
					machine.WorkerCapabilities = []domain.WorkerCapability{domain.SessionForwardingV1, domain.ExecutionStartupV1}
					if capable {
						machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.ManagedCodexSubscriptionsV1)
					}
					_, err = tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
					return nil, err
				})
				if err != nil {
					t.Fatal(err)
				}
				err = f.service.dispatchExecution(ctx, f.refresh(t))
				state, decodeErr := store.Decode[domain.Session](f.refresh(t))
				input := inputBody(t, currentCatalogResource(t, f.accountFixture, f.change.Input))
				if decodeErr != nil {
					t.Fatal(decodeErr)
				}
				if !capable {
					if domain.SafeError(err).Code != domain.Unsupported || state.InitialExecution != nil || state.ActiveExecutionID != "" || input.Delivery != domain.InputQueued || input.ExecutionID != "" {
						t.Fatal("uncapable Worker consumed subscription execution ownership", err)
					}
				} else if err != nil || state.InitialExecution == nil || !state.InitialExecution.Configuration.Subscription || state.InitialExecution.Configuration.Options.Permission != permission || input.Delivery != domain.InputClaimed {
					t.Fatal("negotiated managed profile could not dispatch", err)
				}
			})
		}
	}
}
