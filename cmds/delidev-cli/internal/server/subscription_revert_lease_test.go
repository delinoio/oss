// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// TestManagedCompactionLeaseCapabilityPredicate proves that the protected
// subscription lease for a context job is gated by the capability set of its
// own operation: Revert (version 4) needs only CodexSessionRevertV1, while
// compaction (version 2) keeps the native/Codex compaction pair, and neither
// set authorizes the other operation.
func TestManagedCompactionLeaseCapabilityPredicate(t *testing.T) {
	const (
		revertOp  = "revert"
		compactOp = "compaction"
	)
	scenarios := []struct {
		name         string
		operation    string
		capabilities []domain.WorkerCapability
		corrupt      bool
		granted      bool
	}{
		{"revert-only-grants", revertOp, []domain.WorkerCapability{domain.CodexSessionRevertV1}, false, true},
		{"revert-with-legacy-pair-grants", revertOp, []domain.WorkerCapability{domain.CodexSessionRevertV1, domain.NativeSessionCompactionV1, domain.CodexSessionCompactionV1}, false, true},
		{"legacy-pair-cannot-authorize-revert", revertOp, []domain.WorkerCapability{domain.NativeSessionCompactionV1, domain.CodexSessionCompactionV1}, false, false},
		{"no-capability-denies-revert", revertOp, nil, false, false},
		{"malformed-revert-input-denies", revertOp, []domain.WorkerCapability{domain.CodexSessionRevertV1}, true, false},
		{"compaction-pair-grants", compactOp, []domain.WorkerCapability{domain.NativeSessionCompactionV1, domain.CodexSessionCompactionV1}, false, true},
		{"revert-cannot-authorize-compaction", compactOp, []domain.WorkerCapability{domain.CodexSessionRevertV1}, false, false},
		{"revert-with-native-only-denies-compaction", compactOp, []domain.WorkerCapability{domain.CodexSessionRevertV1, domain.NativeSessionCompactionV1}, false, false},
		{"revert-with-codex-only-denies-compaction", compactOp, []domain.WorkerCapability{domain.CodexSessionRevertV1, domain.CodexSessionCompactionV1}, false, false},
		{"malformed-compaction-input-denies", compactOp, []domain.WorkerCapability{domain.NativeSessionCompactionV1, domain.CodexSessionCompactionV1}, true, false},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			f, message := publicRevertFixture(t, domain.ExecutionSucceeded)
			configureLargeCompactionSubscription(t, f)
			managed := []domain.WorkerCapability{domain.NativeSessionCompactionV1, domain.CodexSessionCompactionV1, domain.CodexSessionRevertV1}
			setCapabilities := func(extra []domain.WorkerCapability) {
				t.Helper()
				_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.lease-capabilities", nil, func(tx *store.Tx) (any, error) {
					r, m, err := activeMachine(tx, f.input.MachineID)
					if err != nil {
						return nil, err
					}
					// Admission needs the full set; the Take predicate is then exercised alone.
					m.WorkerCapabilities = slices.DeleteFunc(m.WorkerCapabilities, func(c domain.WorkerCapability) bool { return slices.Contains(managed, c) })
					m.WorkerCapabilities = append(m.WorkerCapabilities, extra...)
					return tx.Put(r.Kind, r.ID, r.Revision, "", "", m)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			setCapabilities(managed)
			var jobID string
			client := sessionClient(f.accountFixture)
			if scenario.operation == revertOp {
				response, err := client.RevertSession(ctx, ownerRequest(f.identity, &pb.RevertSessionRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID()), MessageId: string(message), BeforeTurnId: string(f.turn)}))
				if err != nil {
					t.Fatal("revert admission", err)
				}
				jobID = response.Msg.Job.Id
			} else {
				response, err := client.CompactSession(ctx, ownerRequest(f.identity, &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID())}))
				if err != nil {
					t.Fatal("compaction admission", err)
				}
				jobID = response.Msg.Job.Id
			}
			claimed := watchWorkAssignment(t, f.workerClient, f.workerIdentity, f.selection.MachineID, domain.ID(f.workerInstance))
			if claimed.Id != jobID {
				t.Fatal("claimed a different context job", claimed.Id, jobID)
			}
			var input domain.SessionCompactionInput
			var job domain.Job
			if domain.DecodeCompactionJob(claimed.DocumentJson, &job) != nil || domain.DecodeCompactionInput(job.Input, &input) != nil {
				t.Fatal("claimed job is not a compaction context job")
			}
			wantVersion := uint32(2)
			if scenario.operation == revertOp {
				wantVersion = 4
			}
			if input.Version != wantVersion || (input.Revert != nil) != (scenario.operation == revertOp) {
				t.Fatal("unexpected context input kind", input.Version)
			}
			setCapabilities(scenario.capabilities)
			if scenario.corrupt {
				_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.lease-malformed-input", nil, func(tx *store.Tx) (any, error) {
					jr, err := tx.Get(domain.JobKind, domain.ID(jobID))
					if err != nil {
						return nil, err
					}
					stored, err := store.Decode[domain.Job](jr)
					if err != nil {
						return nil, err
					}
					stored.Input = []byte(`{"version":` + map[bool]string{true: "2", false: "4"}[scenario.operation == revertOp] + `}`)
					return tx.PutJob(jr.ID, jr.Revision, jr.SessionID, jr.ProjectID, stored)
				})
				if err != nil {
					t.Fatal(err)
				}
				jr, err := f.service.Store.Get(ctx, domain.JobKind, domain.ID(jobID))
				if err != nil {
					t.Fatal(err)
				}
				claimed.Revision = jr.Revision
			}
			subscriptions := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.endpoint.URL)
			taken, err := subscriptions.TakeSubscription(ctx, ownerRequest(f.workerIdentity, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(input.Assignment.AccountID), ExpectedRevision: claimed.Revision}, MachineId: f.machine.Id, InstanceId: f.workerInstance, OperationId: claimed.Id, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}))
			if !scenario.granted {
				if err == nil {
					clear(taken.Msg.Bundle)
					t.Fatal("lease was granted without the operation capability")
				}
				if r, getErr := f.service.Store.Get(ctx, domain.AccountKind, input.Assignment.AccountID); getErr == nil {
					if account, decodeErr := store.Decode[domain.Account](r); decodeErr == nil && account.Subscription != nil && account.Subscription.Lease != nil {
						t.Fatal("denied Take retained a lease")
					}
				}
				return
			}
			if err != nil {
				t.Fatal("original lease was denied", err)
			}
			clear(taken.Msg.Bundle)
			r, getErr := f.service.Store.Get(ctx, domain.AccountKind, input.Assignment.AccountID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			account, decodeErr := store.Decode[domain.Account](r)
			if decodeErr != nil || account.Subscription == nil || account.Subscription.Lease == nil || account.Subscription.Lease.OperationID != domain.ID(claimed.Id) || account.Subscription.Lease.Action != domain.SubscriptionExecute {
				t.Fatal("granted lease does not belong to the original context job", decodeErr)
			}
		})
	}
}
