// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// claudeDirectIdentity is the retained commitment newClaudeMemberFixture stores
// when its fixture login settles.
var claudeDirectIdentity = strings.Repeat("ab", 32)

// setClaudeInventory replaces the fixture Runner's inspected installations and
// the capability set the direct-startup admission depends on.
func setClaudeInventory(t *testing.T, f *memberFixture, installations []domain.Installation, startup bool) {
	t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claude-direct-inventory", nil, func(tx *store.Tx) (any, error) {
		mr, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine.Installations = installations
		machine.WorkerCapabilities = slices.DeleteFunc(machine.WorkerCapabilities, func(c domain.WorkerCapability) bool { return c == domain.ExecutionStartupV1 })
		if startup {
			machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.ExecutionStartupV1)
		}
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// setClaudeJobVersion rewrites one claimed Execute job as a version-4
// direct-startup assignment or leaves it as the historical version-1 one.
func setClaudeJobVersion(t *testing.T, f *memberFixture, job memberJob, direct bool) memberJob {
	t.Helper()
	var updated memberJob
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claude-direct-job", nil, func(tx *store.Tx) (any, error) {
		jr, err := tx.Get(domain.JobKind, job.ID)
		if err != nil {
			return nil, err
		}
		stored, err := store.Decode[domain.Job](jr)
		if err != nil {
			return nil, err
		}
		var input domain.ExecutionJobInput
		if err := domain.Decode(stored.Input, &input); err != nil {
			return nil, err
		}
		if direct {
			input.Version, input.Startup, input.Installation = 4, &domain.ExecutionStartupSelection{Harness: domain.ClaudeCode}, domain.Installation{}
		}
		stored.Input, _ = json.Marshal(input)
		next, err := tx.PutJob(jr.ID, jr.Revision, jr.SessionID, jr.ProjectID, stored)
		updated = memberJob{ID: next.ID, Revision: next.Revision}
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

// newClaudeDirectFixture connects a device-owned Claude account, prepares count+1
// claimed Execute jobs and rewrites each requested job as a v4 assignment.
func newClaudeDirectFixture(t *testing.T, negotiated bool, count int, direct ...bool) (*memberFixture, []memberJob) {
	t.Helper()
	f := newClaudeMemberFixture(t, negotiated)
	jobs := f.prepareJobs(count)
	for i := range jobs {
		jobs[i] = setClaudeJobVersion(t, f, jobs[i], i < len(direct) && direct[i] || len(direct) == 0)
	}
	return f, jobs
}

func claudeTakeCode(err error) domain.Code {
	return domain.SafeError(rpc.ClientError(err)).Code
}

func claudeOwnerFinish(f *memberFixture, lease *pb.TakeSubscriptionResponse, scope pb.SubscriptionFinishScope, leaseID string) error {
	request := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: leaseID, GenerationId: lease.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), CleanupConfirmed: true, Succeeded: true, FinishScope: scope, NativeIdentity: &pb.NativeSubscriptionIdentity{ProfileId: lease.NativeProfileId, IdentityCommitment: claudeDirectIdentity}}
	_, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, request))
	return err
}

func TestClaudeDirectStartupLeaseIgnoresInspectedInventory(t *testing.T) {
	unverified := domain.Installation{Harness: domain.ClaudeCode, State: domain.InstallationDetected, Version: domain.ClaudeProtocolVersion}
	duplicate := []domain.Installation{{Harness: domain.ClaudeCode}, {Harness: domain.ClaudeCode}}
	for _, tc := range []struct {
		name          string
		installations []domain.Installation
	}{
		{"empty inventory", nil},
		{"unverified entry", []domain.Installation{unverified}},
		{"ambiguous entries", duplicate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, jobs := newClaudeDirectFixture(t, false, 0)
			setClaudeInventory(t, f, tc.installations, true)
			lease, err := f.takeJob(jobs[0])
			if err != nil {
				t.Fatalf("v4 Take was blocked by inspected inventory: %v", err)
			}
			if len(lease.InstallationJson) != 0 || len(lease.Bundle) != 0 || lease.NativeProfileId == "" {
				t.Fatal("a direct-startup lease must carry the native profile and no inspected installation")
			}
			_, account := f.record()
			if account.Subscription.Lease == nil || !account.Subscription.Lease.DirectStartup || account.Subscription.Lease.Action != domain.SubscriptionExecute {
				t.Fatal("the granted lease did not retain its direct-startup binding")
			}
			if err := account.Validate(); err != nil {
				t.Fatal("stored direct-startup lease is invalid", err)
			}
			// Readback and Finish consult the stored binding and never the inventory.
			if err := claudeOwnerFinish(f, lease, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_UNSPECIFIED, lease.LeaseId); err != nil {
				t.Fatalf("Finish of a direct-startup lease reintroduced the inventory requirement: %v", err)
			}
			_, after := f.record()
			if after.Subscription.Lease != nil || after.Health != domain.AccountReady || after.Subscription.RecoveryRequired {
				t.Fatal("Finish did not release the direct-startup lease")
			}
		})
	}
}

func TestClaudeHistoricalExecutionKeepsInspectedInventory(t *testing.T) {
	t.Run("take", func(t *testing.T) {
		f, jobs := newClaudeDirectFixture(t, false, 0, false)
		setClaudeInventory(t, f, nil, true)
		if _, err := f.takeJob(jobs[0]); claudeTakeCode(err) != domain.Unsupported {
			t.Fatalf("historical Take with empty inventory = %v, want Unsupported", err)
		}
		if _, account := f.record(); account.Subscription.Lease != nil {
			t.Fatal("a refused historical Take granted a lease")
		}
	})
	t.Run("readback and finish", func(t *testing.T) {
		f, jobs := newClaudeDirectFixture(t, false, 0, false)
		lease, err := f.takeJob(jobs[0])
		if err != nil || len(lease.InstallationJson) == 0 {
			t.Fatalf("historical Take with verified inventory failed or returned no installation: %v", err)
		}
		if _, account := f.record(); account.Subscription.Lease == nil || account.Subscription.Lease.DirectStartup {
			t.Fatal("a historical lease must not carry the direct-startup binding")
		}
		setClaudeInventory(t, f, nil, true)
		if err := claudeOwnerFinish(f, lease, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_UNSPECIFIED, lease.LeaseId); claudeTakeCode(err) != domain.Unsupported {
			t.Fatalf("historical Finish with empty inventory = %v, want Unsupported", err)
		}
	})
	t.Run("login", func(t *testing.T) {
		f := newClaudeSubscriptionFixture(t)
		_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claude-empty-inventory", nil, func(tx *store.Tx) (any, error) {
			mr, machine, err := activeMachine(tx, f.input.MachineID)
			if err != nil {
				return nil, err
			}
			machine.Installations = nil
			return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
		})
		if err != nil {
			t.Fatal(err)
		}
		r, _ := f.record()
		request := &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}
		if _, err := claudeTryLegacyRequest(f, request); domain.SafeError(err).Code != domain.Unsupported {
			t.Fatalf("Login with empty inventory = %v, want Unsupported", err)
		}
	})
}

func TestClaudeDirectStartupKeepsAssignmentFences(t *testing.T) {
	for _, tc := range []struct {
		name string
		want domain.Code
		run  func(t *testing.T, f *memberFixture, job memberJob) error
	}{
		{"startup capability lost", domain.Unsupported, func(t *testing.T, f *memberFixture, job memberJob) error {
			setClaudeInventory(t, f, nil, false)
			_, err := f.takeJob(job)
			return err
		}},
		{"stale job revision", domain.Unsupported, func(t *testing.T, f *memberFixture, job memberJob) error {
			_, err := f.takeJob(memberJob{ID: job.ID, Revision: job.Revision + 1})
			return err
		}},
		{"canceled job", domain.RecoveryRequired, func(t *testing.T, f *memberFixture, job memberJob) error {
			if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.cancel", nil, func(tx *store.Tx) (any, error) { return nil, tx.RequestJobCancellation(job.ID) }); err != nil {
				t.Fatal(err)
			}
			_, err := f.takeJob(job)
			return err
		}},
		{"foreign instance", domain.Conflict, func(t *testing.T, f *memberFixture, job memberJob) error {
			_, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: job.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(domain.NewID()), OperationId: string(job.ID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}))
			return err
		}},
		{"foreign profile owner", domain.RecoveryRequired, func(t *testing.T, f *memberFixture, job memberJob) error {
			if _, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claude-foreign-owner", nil, func(tx *store.Tx) (any, error) {
				r, a, err := accountFromTx(tx, f.input.AccountID, 0)
				if err != nil {
					return nil, err
				}
				a.Subscription.OwnerMachineID = domain.NewID()
				_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
				return nil, err
			}); err != nil {
				t.Fatal(err)
			}
			_, err := f.takeJob(job)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, jobs := newClaudeDirectFixture(t, false, 0)
			if tc.name != "startup capability lost" {
				setClaudeInventory(t, f, nil, true)
			}
			err := tc.run(t, f, jobs[0])
			if err == nil {
				t.Fatal("a broken fence admitted the direct-startup lease")
			}
			if got := claudeTakeCode(err); got != tc.want {
				t.Fatalf("error code = %s, want %s", got, tc.want)
			}
			if _, account := f.record(); account.Subscription.Lease != nil {
				t.Fatal("a refused Take granted a lease")
			}
		})
	}
}

func TestClaudeDirectStartupSharedMembersTrackTheirOwnAssignment(t *testing.T) {
	f, jobs := newClaudeDirectFixture(t, true, 2, true, true, false)
	setClaudeInventory(t, f, nil, true)
	var leases []*pb.TakeSubscriptionResponse
	for _, job := range jobs[:2] {
		lease, err := f.takeJob(job)
		if err != nil {
			t.Fatalf("direct member was blocked by inspected inventory: %v", err)
		}
		leases = append(leases, lease)
	}
	// A historical assignment cannot join through the direct owner.
	if _, err := f.takeJob(jobs[2]); claudeTakeCode(err) != domain.Unsupported {
		t.Fatalf("historical member joined without inventory: %v", err)
	}
	_, account := f.record()
	if !account.Subscription.SharedOwner() || len(account.Subscription.Members) != 2 {
		t.Fatal("two direct members were not admitted to one owner")
	}
	for _, member := range account.Subscription.Members {
		if !member.DirectStartup {
			t.Fatal("a direct member lost its assignment binding")
		}
	}
	if err := account.Validate(); err != nil {
		t.Fatal(err)
	}
	member := pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER
	if _, err := f.finishScope(leases[0], leases[0].LeaseRevision, member, leases[0].GenerationId, nil, true, true); err != nil {
		t.Fatalf("direct member Finish needed inspected inventory: %v", err)
	}
	owner := pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER
	if err := claudeOwnerFinish(f, leases[0], owner, leases[1].LeaseId); err != nil {
		t.Fatalf("last direct member could not retire the owner: %v", err)
	}
	if _, after := f.record(); after.Subscription.Lease != nil || len(after.Subscription.Members) != 0 {
		t.Fatal("direct owner was not retired")
	}
}

func TestClaudeRecoveryCleanupFollowsTheLeaseAssignment(t *testing.T) {
	for _, direct := range []bool{true, false} {
		name := "historical"
		if direct {
			name = "direct"
		}
		t.Run(name, func(t *testing.T) {
			f, jobs := newClaudeDirectFixture(t, false, 0, direct)
			if direct {
				setClaudeInventory(t, f, nil, true)
			}
			lease, err := f.takeJob(jobs[0])
			if err != nil {
				t.Fatal(err)
			}
			setClaudeInventory(t, f, nil, true)
			if err := f.service.markSubscriptionRecovery(f.input.AccountID); err != nil {
				t.Fatal(err)
			}
			_, err = f.finish(lease, nil, true, false, true)
			_, account := f.record()
			if direct {
				if err != nil || account.Subscription.Lease != nil || account.Subscription.RecoveryRequired || account.Health != domain.AccountReady {
					t.Fatalf("recovery cleanup of a direct lease needed inventory: %v", err)
				}
				return
			}
			if claudeTakeCode(err) != domain.Unsupported || account.Subscription.Lease == nil || !account.Subscription.RecoveryRequired {
				t.Fatalf("recovery cleanup of a historical lease = %v, want Unsupported with the lease retained", err)
			}
		})
	}
}
