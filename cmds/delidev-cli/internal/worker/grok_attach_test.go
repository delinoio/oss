// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

const managedGrokCapability = pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_GROK_SUBSCRIPTIONS_V1

// Worker capability 21 is reported only when the Worker's own switch is on and
// the server echoed it, so the default build never advertises managed Grok.
func TestManagedGrokCapabilityIsReportedOnlyWhenEnabledAndEchoed(t *testing.T) {
	for _, tc := range []struct {
		name      string
		enabled   bool
		supported []pb.WorkerCapability
		reported  bool
	}{
		{"default build, server echoes", false, []pb.WorkerCapability{managedGrokCapability}, false},
		{"default build, server silent", false, nil, false},
		{"enabled, server silent", true, nil, false},
		{"enabled, server echoes", true, []pb.WorkerCapability{managedGrokCapability}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grokManagedExecutionEnabled = tc.enabled
			t.Cleanup(func() { grokManagedExecutionEnabled = false })
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			machine, _ := claudeInventory(t, func(string) []domain.Installation { return nil })
			fixture := &claudeAttachFixture{serverID: domain.NewID(), cancel: cancel, support: [][]pb.WorkerCapability{tc.supported, tc.supported}, machine: machine}
			_, handler := delidevv1connect.NewWorkerServiceHandler(fixture)
			server := httptest.NewServer(handler)
			defer server.Close()
			credential := Credential{Endpoint: server.URL, ServerID: fixture.serverID, MachineID: domain.NewID(), Token: "isolated-claude-attachment"}
			_ = runConnected(ctx, Config{Root: t.TempDir(), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, credential)
			fixture.Lock()
			defer fixture.Unlock()
			if len(fixture.requests) == 0 {
				t.Fatal("Worker never attached")
			}
			for _, request := range fixture.requests {
				if slices.Contains(request.Capabilities, managedGrokCapability) != (tc.reported && request == fixture.requests[len(fixture.requests)-1]) {
					t.Fatal("Worker capability 21 did not follow the Worker switch and the server echo")
				}
			}
		})
	}
}

func TestGrokExecutionRecoveryIsRecognizedOnlyForFencedExecuteLeases(t *testing.T) {
	lease := &domain.SubscriptionLease{Action: domain.SubscriptionExecute}
	account := domain.Account{Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionGrok, Subscription: &domain.SubscriptionState{RecoveryRequired: true, Lease: lease}}
	if !grokExecutionRecovery(account) {
		t.Fatal("a fenced execute lease was not recognized")
	}
	for name, edit := range map[string]func(*domain.Account){
		"not fenced": func(a *domain.Account) { a.Subscription.RecoveryRequired = false },
		"no lease":   func(a *domain.Account) { a.Subscription.Lease = nil },
		"lifecycle lease": func(a *domain.Account) {
			a.Subscription.Lease = &domain.SubscriptionLease{Action: domain.SubscriptionRefresh}
		},
		"other service": func(a *domain.Account) { a.SubscriptionService = domain.SubscriptionClaude },
	} {
		other := account
		state := *account.Subscription
		other.Subscription = &state
		edit(&other)
		if grokExecutionRecovery(other) {
			t.Fatalf("%s was recognized as a Grok execution recovery", name)
		}
	}
}
