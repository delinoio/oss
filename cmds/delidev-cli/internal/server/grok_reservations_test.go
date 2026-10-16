// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// Login is active independently of the reserved managed-execution identifiers.
const (
	grokSystemLoginReservation     = pb.SystemCapability(39)
	grokSystemExecutionReservation = pb.SystemCapability(40)
	grokWorkerReservation          = pb.WorkerCapability(21)
)

func TestGrokReservationsAreDeclaredWithLedgerNumbers(t *testing.T) {
	if pb.SystemCapability_SYSTEM_CAPABILITY_GROK_SUBSCRIPTION_LOGIN_V1 != grokSystemLoginReservation || pb.SystemCapability_SYSTEM_CAPABILITY_GROK_SUBSCRIPTION_EXECUTION_V1 != grokSystemExecutionReservation || pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_GROK_SUBSCRIPTIONS_V1 != grokWorkerReservation {
		t.Fatal("Grok capability reservations drifted from the allocation ledger")
	}
}

func TestGrokStatusAdvertisesLoginWithoutExecution(t *testing.T) {
	f := newManagedClaudeFixture(t)
	status, err := f.service.GetStatus(context.Background(), connect.NewRequest(&pb.GetStatusRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range []pb.SystemCapability{grokSystemExecutionReservation} {
		if slices.Contains(status.Msg.Capabilities, capability) {
			t.Fatalf("status advertised the unactivated capability %d", capability)
		}
	}
	if !slices.Contains(status.Msg.Capabilities, grokSystemLoginReservation) {
		t.Fatal("server CLI login is not advertised")
	}
	if !slices.Contains(status.Msg.Capabilities, pb.SystemCapability_SYSTEM_CAPABILITY_CLAUDE_SUBSCRIPTIONS_V1) {
		t.Fatal("existing capability 38 is no longer advertised")
	}
}

func TestGrokWorkerReservationIsNotAdvertisedAndRejectedAsUnknown(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	worker, paired := pairedWorker(t, ctx, Endpoint{URL: f.url}, f.service.Identity)
	client := delidevv1connect.NewWorkerServiceClient(http.DefaultClient, f.url)
	attach := func(capabilities ...pb.WorkerCapability) (*connect.Response[pb.AttachWorkerResponse], error) {
		return client.AttachWorker(ctx, ownerRequest(worker, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: paired.Machine.Id, InstanceId: string(domain.NewID()), Version: rpc.Version, Capabilities: capabilities}))
	}
	attached, err := attach(pb.WorkerCapability_WORKER_CAPABILITY_REPOSITORY_CLONE_V1)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(attached.Msg.SupportedWorkerCapabilities, grokWorkerReservation) {
		t.Fatal("AttachWorker advertised the unactivated Worker capability 21")
	}
	if !slices.Contains(attached.Msg.SupportedWorkerCapabilities, pb.WorkerCapability_WORKER_CAPABILITY_NATIVE_CLAUDE_SUBSCRIPTIONS_V1) {
		t.Fatal("existing Worker capability 20 is no longer advertised")
	}
	if _, err := attach(grokWorkerReservation); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("Worker capability 21 was not rejected as unknown: %v", err)
	}
}

func TestGrokDiagnosticIsNeverPopulatedByProgress(t *testing.T) {
	f := newSubscriptionFixture(t)
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if f.progressFor(op.OperationId).GrokDiagnostic != nil {
		t.Fatal("pending progress populated grok_diagnostic")
	}
	n := &serverLoginFixture{started: make(chan struct{}), finish: make(chan struct{}), bundle: subscriptionTestBundle("grok-reservation-account", "first", time.Now().UTC())}
	done := f.serverRun(n)
	awaitServerFixture(t, n.started)
	if p := f.progressFor(op.OperationId); p.GrokDiagnostic != nil || p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING {
		t.Fatal("waiting progress populated grok_diagnostic")
	}
	close(n.finish)
	awaitServerFixture(t, done)
	if p := f.progressFor(op.OperationId); p.GrokDiagnostic != nil || p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
		t.Fatal("settled progress populated grok_diagnostic")
	}
}
