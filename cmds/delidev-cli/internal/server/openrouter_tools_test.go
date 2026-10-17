// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestOpenRouterToolsRequireOriginalAssignmentAndNegotiatedWorker(t *testing.T) {
	for _, scenario := range []string{"selected", "legacy", "missing-capability", "other-provider"} {
		t.Run(scenario, func(t *testing.T) {
			endpoint := "https://openrouter.ai/api/v1"
			if scenario == "other-provider" {
				endpoint = "https://api.example.test/v1"
			}
			f := newConfiguredAuthorityFixture(t, endpoint, func(input *domain.ExecutionJobInput) {
				if scenario != "legacy" {
					input.NativeOpenRouterTools, input.NativeImageGeneration, input.NativeDynamicTools = true, true, true
				}
			}, false)
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.openrouter-tools", nil, func(tx *store.Tx) (any, error) {
				row, machine, err := activeMachine(tx, f.input.MachineID)
				if err != nil {
					return nil, err
				}
				machine.WorkerCapabilities = []domain.WorkerCapability{domain.NativeImageGenerationV1, domain.CodexDynamicToolsV1}
				if scenario != "missing-capability" {
					machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.CodexOpenRouterToolsV1)
				}
				return tx.Put(domain.MachineKind, row.ID, row.Revision, "", "", machine)
			})
			if err != nil {
				t.Fatal(err)
			}
			request := connect.NewRequest(f.register)
			request.Header().Set("Authorization", "Bearer "+f.workerToken)
			_, err = f.client.RegisterExecution(context.Background(), request)
			valid := scenario == "selected" || scenario == "legacy"
			if (err == nil) != valid {
				t.Fatal("tool grant eligibility mismatch", err)
			}
			if !valid {
				return
			}
			lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			if lease.Scope.NativeModel != f.input.Configuration.NativeModel || lease.Scope.AccountID != f.input.AccountID || lease.Scope.OpenRouterTools != (scenario == "selected") {
				t.Fatal("tool scope changed original account/model")
			}
			for _, operation := range []apiproxy.Operation{apiproxy.ImageCreate, apiproxy.ImageEdit, apiproxy.StandaloneSearch} {
				if slices.Contains(lease.Scope.Operations, operation) != (scenario == "selected") {
					t.Fatal("tool operation leaked across negotiation", operation)
				}
			}
		})
	}
}
