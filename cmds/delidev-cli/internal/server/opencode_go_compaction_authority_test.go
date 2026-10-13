// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// queuedOpenCodeGoCompaction accepts one manual compaction on an eligible
// completed OpenCode Go source and returns it claimed by the original Worker.
func queuedOpenCodeGoCompaction(t *testing.T) (*firstDispatchFixture, domain.SessionCompactionInput, store.Record) {
	t.Helper()
	ctx := context.Background()
	continuation := newOpenCodeGoContinuation(t)
	f := continuation.firstDispatchFixture
	f.workerStream.Close()
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.go-compaction-capabilities", nil, func(tx *store.Tx) (any, error) {
		r, machine, err := activeMachine(tx, domain.ID(f.machine.Id))
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.NativeSessionCompactionV1, domain.OpenCodeSessionCompactionV1)
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := sessionClient(f.accountFixture).CompactSession(ctx, ownerRequest(f.identity, &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(f.refresh(t)), domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	jobID := domain.ID(accepted.Msg.Job.Id)
	row, err := f.service.Store.Get(ctx, domain.JobKind, jobID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](row)
	var input domain.SessionCompactionInput
	if err != nil || domain.DecodeCompactionInput(job.Input, &input) != nil || !input.Assignment.Configuration.IsOpenCodeGo() {
		t.Fatal("compaction did not retain the OpenCode Go assignment", err)
	}
	claimed, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.claim-go-compaction", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, jobID)
		if err != nil {
			return nil, err
		}
		job.State, job.InstanceID, job.AssignedDeviceID = domain.JobClaimed, domain.ID(f.workerInstance), f.workerDevice
		return tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
	})
	if err != nil {
		t.Fatal(err)
	}
	var claim store.Record
	if err := json.Unmarshal(claimed.Data, &claim); err != nil {
		t.Fatal(err)
	}
	return f, input, claim
}

func registerOpenCodeGoCompaction(t *testing.T, f *firstDispatchFixture, claim store.Record) (string, error) {
	t.Helper()
	raw, err := security.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	token := apiproxy.TokenPrefix + raw
	digest := sha256.Sum256([]byte(token))
	_, err = f.workerClient.RegisterExecution(context.Background(), ownerRequest(f.workerIdentity, &pb.RegisterExecutionRequest{Mutation: acctMutation(resourceForTest(claim), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, CredentialDigest: digest[:]}))
	return token, err
}

func TestOpenCodeGoManualCompactionRegistersFixedKeyScope(t *testing.T) {
	f, input, claim := queuedOpenCodeGoCompaction(t)
	token, err := registerOpenCodeGoCompaction(t, f, claim)
	if err != nil {
		t.Fatal("eligible OpenCode Go compaction was denied registration", err)
	}
	lease, err := f.service.executionAuthority.Acquire(context.Background(), token)
	if err != nil {
		t.Fatal("registered OpenCode Go compaction could not acquire its relay lease", err)
	}
	defer lease.Release()
	scope := lease.Scope
	if scope.ExecutionID != input.ActionID || scope.SessionID != input.Assignment.SessionID || scope.AccountID != input.Assignment.AccountID || scope.ConnectionID != input.Assignment.ConnectionID || scope.SubscriptionService != domain.SubscriptionOpenCodeGo || scope.Harness != domain.OpenCode || scope.ProviderID != "" || scope.OpenCodeSession != string(input.Completion.NativeThreadID) || !reflect.DeepEqual(scope.Provider, domain.OpenCodeGoProvider()) || !reflect.DeepEqual(scope.Operations, []apiproxy.Operation{apiproxy.ChatCompletion}) {
		t.Fatal("OpenCode Go compaction did not receive the fixed key-backed scope", scope)
	}
}

func TestOpenCodeGoManualCompactionDeniesBeforeKeyUse(t *testing.T) {
	for _, scenario := range []string{"capability-removed", "connection-replaced", "key-removed", "account-disabled", "native-session-missing"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			f, input, claim := queuedOpenCodeGoCompaction(t)
			switch scenario {
			case "capability-removed":
				_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.remove-go-capability", nil, func(tx *store.Tx) (any, error) {
					r, machine, err := activeMachine(tx, domain.ID(f.machine.Id))
					if err != nil {
						return nil, err
					}
					machine.WorkerCapabilities = slices.DeleteFunc(machine.WorkerCapabilities, func(c domain.WorkerCapability) bool { return c == domain.OpenCodeGoSubscriptionsV1 })
					return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", machine)
				})
				if err != nil {
					t.Fatal(err)
				}
			case "connection-replaced", "key-removed", "account-disabled":
				_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.change-go-account", nil, func(tx *store.Tx) (any, error) {
					r, account, err := accountFromTx(tx, input.Assignment.AccountID, 0)
					if err != nil {
						return nil, err
					}
					switch scenario {
					case "connection-replaced":
						account.Connection.ID = domain.NewID()
					case "key-removed":
						account.Connection, account.Health = nil, domain.AccountDisconnected
					case "account-disabled":
						account.Enabled = false
					}
					return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, account)
				})
				if err != nil {
					t.Fatal(err)
				}
			case "native-session-missing":
				_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.drop-go-native-session", nil, func(tx *store.Tx) (any, error) {
					r, err := tx.Get(domain.JobKind, domain.ID(claim.ID))
					if err != nil {
						return nil, err
					}
					job, err := store.Decode[domain.Job](r)
					if err != nil {
						return nil, err
					}
					var stored domain.SessionCompactionInput
					if domain.DecodeCompactionInput(job.Input, &stored) != nil {
						return nil, domain.CompactionUncertain()
					}
					stored.Completion.NativeThreadID = ""
					raw, err := json.Marshal(stored)
					if err != nil {
						return nil, err
					}
					job.Input = raw
					return tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			current, err := f.service.Store.Get(ctx, domain.JobKind, claim.ID)
			if err != nil {
				t.Fatal(err)
			}
			token, err := registerOpenCodeGoCompaction(t, f, current)
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal("OpenCode Go compaction registered without its original authority", err)
			}
			if lease, acquireErr := f.service.executionAuthority.Acquire(ctx, token); acquireErr == nil {
				lease.Release()
				t.Fatal("OpenCode Go compaction acquired a relay lease without authority")
			}
		})
	}
}
