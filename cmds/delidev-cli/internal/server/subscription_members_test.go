// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// memberFixture negotiates SharedSubscriptionOwnerV1 for the fixture Runner and
// prepares additional claimed execute jobs that share the account.
type memberFixture struct {
	*subscriptionFixture
}

func newMemberFixture(t *testing.T, negotiated bool) *memberFixture {
	t.Helper()
	f := &memberFixture{newSubscriptionFixture(t)}
	clear(f.login())
	if negotiated {
		_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.shared-owner-capability", nil, func(tx *store.Tx) (any, error) {
			mr, machine, err := activeMachine(tx, f.input.MachineID)
			if err != nil {
				return nil, err
			}
			machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.SharedSubscriptionOwnerV1)
			return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// prepareJobs rewrites the fixture job as managed and clones count extra claimed
// execute jobs that name the same account. It returns every job with its revision.
func (f *memberFixture) prepareJobs(count int) []memberJob {
	f.t.Helper()
	_, account := f.record()
	f.input.ConnectionID = account.Connection.ID
	f.input.Configuration.Subscription = true
	f.input.ConfigurationDigest, _ = f.input.Configuration.Digest()
	var jobs []memberJob
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.shared-owner-jobs", nil, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		session.InitialExecution.ConnectionID = f.input.ConnectionID
		session.InitialExecution.Configuration = f.input.Configuration
		session.InitialExecution.ConfigurationDigest = f.input.ConfigurationDigest
		if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.SessionID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		jr, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](jr)
		if err != nil {
			return nil, err
		}
		job.Input, _ = json.Marshal(f.input)
		updated, err := tx.PutJob(jr.ID, jr.Revision, jr.SessionID, jr.ProjectID, job)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, memberJob{ID: updated.ID, Revision: updated.Revision})
		for i := 0; i < count; i++ {
			clone, err := tx.PutJob(domain.NewID(), 0, jr.SessionID, jr.ProjectID, job)
			if err != nil {
				return nil, err
			}
			jobs = append(jobs, memberJob{ID: clone.ID, Revision: clone.Revision})
		}
		return nil, nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return jobs
}

type memberJob struct {
	ID       domain.ID
	Revision uint64
}

func (f *memberFixture) takeJob(job memberJob) (*pb.TakeSubscriptionResponse, error) {
	response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: job.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: string(job.ID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *memberFixture) finishScope(lease *pb.TakeSubscriptionResponse, revision uint64, scope pb.SubscriptionFinishScope, generation string, bundle []byte, success, cleanup bool) (*pb.FinishSubscriptionResponse, error) {
	response, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: revision}, LeaseId: lease.LeaseId, GenerationId: generation, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Bundle: bytes.Clone(bundle), Succeeded: success, CleanupConfirmed: cleanup, FinishScope: scope}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *memberFixture) members(t *testing.T) []domain.SubscriptionMember {
	t.Helper()
	_, account := f.record()
	if err := account.Validate(); err != nil {
		t.Fatal("stored owner/member set is invalid", err)
	}
	return account.Subscription.Members
}

// takeAll founds the owner with the first job and joins every other job.
func (f *memberFixture) takeAll(t *testing.T, jobs []memberJob) []*pb.TakeSubscriptionResponse {
	t.Helper()
	var leases []*pb.TakeSubscriptionResponse
	for index, job := range jobs {
		lease, err := f.takeJob(job)
		if err != nil {
			t.Fatalf("job %d was not admitted: %v", index, err)
		}
		if index == 0 && (len(lease.Bundle) == 0 || lease.OwnerLeaseId != "") {
			t.Fatal("the founding member must receive the one owner bundle and no owner reference")
		}
		if index > 0 && (len(lease.Bundle) != 0 || lease.OwnerLeaseId != leases[0].LeaseId || lease.GenerationId != leases[0].GenerationId) {
			t.Fatal("a joined member must receive no authentication and name its owner")
		}
		leases = append(leases, lease)
	}
	return leases
}

func TestSharedOwnerAdmitsManyConcurrentMembersWithoutCap(t *testing.T) {
	f := newMemberFixture(t, true)
	jobs := f.prepareJobs(7)
	leases := f.takeAll(t, jobs)
	defer func() {
		for _, lease := range leases {
			clear(lease.Bundle)
		}
	}()
	_, account := f.record()
	if !account.Subscription.SharedOwner() || len(account.Subscription.Members) != 8 || account.Subscription.RunnableMembers() != 8 {
		t.Fatal("eight independent sessions were not all admitted to one owner")
	}
	owner := account.Subscription.Lease
	if owner.ID != domain.ID(leases[0].LeaseId) || owner.Generation != account.Subscription.Generation || owner.MachineID != f.input.MachineID || owner.InstanceID != f.instance || owner.DeviceID != f.device || owner.Epoch != f.service.subscriptionServerEpoch() {
		t.Fatal("the owner lost its original Runner, device, instance, epoch or generation binding")
	}
	seen := map[domain.ID]bool{}
	for index, member := range f.members(t) {
		if member.OperationID != jobs[index].ID || member.LeaseID != domain.ID(leases[index].LeaseId) || member.Revision != leases[index].LeaseRevision || seen[member.OperationID] {
			t.Fatal("a member does not own exactly one job")
		}
		seen[member.OperationID] = true
	}
	// The same job cannot be admitted twice and a stale job revision cannot join.
	if _, err := f.takeJob(jobs[3]); err == nil {
		t.Fatal("one job owned two members")
	}
	if len(f.members(t)) != 8 {
		t.Fatal("a rejected admission changed the member set")
	}
}

func TestSharedOwnerStaysExclusiveWithoutNegotiation(t *testing.T) {
	f := newMemberFixture(t, false)
	jobs := f.prepareJobs(1)
	first, err := f.takeJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	defer clear(first.Bundle)
	_, err = f.takeJob(jobs[1])
	if domain.SafeError(rpc.ClientError(err)).Code != domain.ResourceExhausted {
		t.Fatal("an old peer shared a legacy singleton lease", err)
	}
	_, account := f.record()
	if account.Subscription.SharedOwner() || len(account.Subscription.Members) != 0 || account.Subscription.Lease == nil || account.Subscription.Lease.Shared {
		t.Fatal("an old peer produced the shared representation")
	}
}

func TestMemberFinishNeverTouchesCredentialsOrSiblings(t *testing.T) {
	f := newMemberFixture(t, true)
	jobs := f.prepareJobs(3)
	leases := f.takeAll(t, jobs)
	defer clear(leases[0].Bundle)
	_, before := f.record()
	f.secrets.mu.Lock()
	puts, deletes := f.secrets.puts, f.secrets.deletes
	f.secrets.mu.Unlock()
	// Completion of a non-founding member and of the founding member in turn.
	for _, index := range []int{2, 0} {
		bundle := subscriptionTestBundle("fixture-account", fmt.Sprintf("must-not-write-%d", index), time.Now().UTC())
		if _, err := f.finishScope(leases[index], leases[index].LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, leases[index].GenerationId, bundle, true, true); err == nil {
			t.Fatal("a member Finish accepted authentication bytes")
		}
		clear(bundle)
		if _, err := f.finishScope(leases[index], leases[index].LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, leases[index].GenerationId, nil, true, true); err != nil {
			t.Fatal(err)
		}
	}
	_, after := f.record()
	if after.Subscription.Generation != before.Subscription.Generation || after.Subscription.Lease == nil || after.Subscription.Lease.ID != before.Subscription.Lease.ID || after.Subscription.RecoveryRequired || after.Health != domain.AccountReady {
		t.Fatal("a member Finish changed credentials, the owner or account health")
	}
	if got := f.members(t); len(got) != 2 || got[0].OperationID != jobs[1].ID || got[1].OperationID != jobs[3].ID || after.Subscription.RunnableMembers() != 2 {
		t.Fatal("a member Finish removed or stopped a sibling")
	}
	f.secrets.mu.Lock()
	changed := f.secrets.puts != puts || f.secrets.deletes != deletes
	f.secrets.mu.Unlock()
	if changed {
		t.Fatal("a member Finish staged or deleted a vault generation")
	}
	// The released members cannot be reused, and the owner id is not a member.
	if _, err := f.finishScope(leases[2], leases[2].LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, leases[2].GenerationId, nil, true, true); err == nil {
		t.Fatal("a released member finished twice")
	}
}

func TestOwnerRetiresOnlyAfterLastMemberWithOneWriteback(t *testing.T) {
	f := newMemberFixture(t, true)
	jobs := f.prepareJobs(2)
	leases := f.takeAll(t, jobs)
	defer clear(leases[0].Bundle)
	owner := leases[0]
	rotated := subscriptionTestBundle("fixture-account", "owner-writeback", time.Now().UTC())
	defer clear(rotated)
	// Siblings still run, so neither founding member nor owner id can retire it.
	for _, lease := range []*pb.TakeSubscriptionResponse{leases[0], leases[2]} {
		_, err := f.finishScope(lease, owner.LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, owner.GenerationId, rotated, true, true)
		if domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict {
			t.Fatal("the owner retired while other members were runnable", err)
		}
	}
	// A legacy-scoped Finish never settles a shared owner.
	if _, err := f.finish(owner, rotated, true, true, true); err == nil {
		t.Fatal("a legacy Finish settled a shared owner")
	}
	_, mid := f.record()
	if mid.Subscription.RecoveryRequired || mid.Subscription.Generation != domain.ID(owner.GenerationId) || len(mid.Subscription.Members) != 3 {
		t.Fatal("refused retirements changed the owner")
	}
	for _, index := range []int{1, 2} {
		if _, err := f.finishScope(leases[index], leases[index].LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, owner.GenerationId, nil, true, true); err != nil {
			t.Fatal(err)
		}
	}
	// The last runnable member retires the owner with exactly one writeback.
	f.secrets.mu.Lock()
	puts := f.secrets.puts
	f.secrets.mu.Unlock()
	if _, err := f.finishScope(leases[0], owner.LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, owner.GenerationId, rotated, true, true); err != nil {
		t.Fatal(err)
	}
	f.secrets.mu.Lock()
	written := f.secrets.puts - puts
	f.secrets.mu.Unlock()
	_, after := f.record()
	if written != 1 || after.Subscription.Lease != nil || len(after.Subscription.Members) != 0 || after.Subscription.Generation == domain.ID(owner.GenerationId) || after.Health != domain.AccountReady || after.Subscription.RecoveryRequired {
		t.Fatal("owner retirement did not write one new generation and release the account", written)
	}
}

func TestUnprovenMemberClosureIsRetainedUntilOwnerContainmentCloses(t *testing.T) {
	f := newMemberFixture(t, true)
	jobs := f.prepareJobs(2)
	leases := f.takeAll(t, jobs)
	defer clear(leases[0].Bundle)
	owner := leases[0]
	// Stop of member 2 without proven thread/closed and descendant closure.
	if _, err := f.finishScope(leases[2], leases[2].LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, owner.GenerationId, nil, false, false); err != nil {
		t.Fatal(err)
	}
	_, account := f.record()
	retained, ok := account.Subscription.Member(domain.ID(leases[2].LeaseId))
	if !ok || retained.State != domain.SubscriptionMemberRetained || account.Subscription.RunnableMembers() != 2 || account.Subscription.RecoveryRequired {
		t.Fatal("an unproven closure released its workspace ownership or fenced siblings")
	}
	// The retained member keeps no execution authority and cannot finish again.
	if _, ok := account.Subscription.LeaseForOperation(jobs[2].ID); ok {
		t.Fatal("a retained member kept execution authority")
	}
	if _, err := f.finishScope(leases[2], leases[2].LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, owner.GenerationId, nil, true, true); err == nil {
		t.Fatal("a retained member was released by its own report")
	}
	// The last runnable members finish; the owner then retires and settles the
	// retained member together with its own confirmed containment.
	if _, err := f.finishScope(leases[1], leases[1].LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, owner.GenerationId, nil, true, true); err != nil {
		t.Fatal(err)
	}
	rotated := subscriptionTestBundle("fixture-account", "retired", time.Now().UTC())
	defer clear(rotated)
	if _, err := f.finishScope(leases[0], owner.LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, owner.GenerationId, rotated, true, true); err != nil {
		t.Fatal(err)
	}
	_, after := f.record()
	if after.Subscription.Lease != nil || len(after.Subscription.Members) != 0 {
		t.Fatal("final owner containment did not release the retained member")
	}
}

func TestOwnerWithoutRunnableMembersRefusesNewAdmission(t *testing.T) {
	f := newMemberFixture(t, true)
	jobs := f.prepareJobs(1)
	first, err := f.takeJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	defer clear(first.Bundle)
	if _, err := f.finishScope(first, first.LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, first.GenerationId, nil, true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.takeJob(jobs[1]); domain.SafeError(rpc.ClientError(err)).Code != domain.ResourceExhausted {
		t.Fatal("a draining owner admitted a new member", err)
	}
	// The Worker retires the draining owner by its own identifier.
	rotated := subscriptionTestBundle("fixture-account", "draining", time.Now().UTC())
	defer clear(rotated)
	if _, err := f.finishScope(first, first.LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, first.GenerationId, rotated, true, true); err != nil {
		t.Fatal(err)
	}
	next, err := f.takeJob(jobs[1])
	if err != nil {
		t.Fatal("a fresh owner was not founded after retirement", err)
	}
	defer clear(next.Bundle)
	if len(next.Bundle) == 0 || next.OwnerLeaseId != "" || next.GenerationId == first.GenerationId {
		t.Fatal("the next owner did not receive the new generation")
	}
}

func TestLifecycleClosesAdmissionAndWaitsForEveryMember(t *testing.T) {
	for _, action := range []pb.SubscriptionAction{pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT} {
		t.Run(action.String(), func(t *testing.T) {
			f := newMemberFixture(t, true)
			jobs := f.prepareJobs(2)
			leases := f.takeAll(t, jobs[:2])
			defer clear(leases[0].Bundle)
			op := f.start(action)
			// Fresh admission is closed from acceptance.
			if _, err := f.takeJob(jobs[2]); err == nil {
				t.Fatal("a queued lifecycle operation left admission open")
			}
			// The lifecycle Take waits behind the whole owner/member set.
			if _, err := f.take(op, action); domain.SafeError(rpc.ClientError(err)).Code != domain.ResourceExhausted {
				t.Fatal("lifecycle authority overlapped live members", err)
			}
			if _, err := f.finishScope(leases[1], leases[1].LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, leases[0].GenerationId, nil, true, true); err != nil {
				t.Fatal(err)
			}
			if _, err := f.take(op, action); err == nil {
				t.Fatal("lifecycle authority overlapped the remaining member")
			}
			rotated := subscriptionTestBundle("fixture-account", "lifecycle-drain", time.Now().UTC())
			defer clear(rotated)
			if action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT {
				_, account := f.record()
				if account.Health != domain.AccountRevoked {
					t.Fatal("logout did not revoke readiness immediately")
				}
			}
			if _, err := f.finishScope(leases[0], leases[0].LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, leases[0].GenerationId, rotated, true, true); err != nil {
				t.Fatal(err)
			}
			lifecycle, err := f.take(op, action)
			if err != nil {
				t.Fatal("lifecycle authority did not follow the drained owner", err)
			}
			clear(lifecycle.Bundle)
			_, account := f.record()
			if account.Subscription.Lease == nil || account.Subscription.Lease.Shared || account.Subscription.Lease.Action == domain.SubscriptionExecute {
				t.Fatal("lifecycle Take did not hold the exclusive singleton lease")
			}
		})
	}
}

func TestSharedOwnerLossFencesEveryMemberAndPreservesEvidence(t *testing.T) {
	f := newMemberFixture(t, true)
	jobs := f.prepareJobs(2)
	leases := f.takeAll(t, jobs)
	defer clear(leases[0].Bundle)
	if err := f.service.retainLostSubscriptionLeases(f.input.MachineID, f.instance, true); err != nil {
		t.Fatal(err)
	}
	record, before := f.record()
	beforeJSON, _ := json.Marshal(before)
	if !before.Subscription.RecoveryRequired || len(before.Subscription.Members) != 3 {
		t.Fatal("owner loss did not fence the owner/member set")
	}
	f.secrets.mu.Lock()
	puts, deletes := f.secrets.puts, f.secrets.deletes
	f.secrets.mu.Unlock()
	rotated := subscriptionTestBundle("fixture-account", "late", time.Now().UTC())
	defer clear(rotated)
	for index, lease := range leases {
		scope := pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER
		bundle := []byte(nil)
		if index == 0 {
			scope, bundle = pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, rotated
		}
		if _, err := f.finishScope(lease, lease.LeaseRevision, scope, lease.GenerationId, bundle, true, true); domain.SafeError(rpc.ClientError(err)).Code == "" || err == nil {
			t.Fatal("a fenced member finished", index, err)
		}
	}
	// A fenced owner admits no further member.
	if _, err := f.takeJob(jobs[2]); err == nil {
		t.Fatal("a fenced owner admitted a member")
	}
	afterRecord, after := f.record()
	afterJSON, _ := json.Marshal(after)
	if afterRecord.Revision != record.Revision || !bytes.Equal(beforeJSON, afterJSON) {
		t.Fatal("late member reports rewrote the recovery evidence")
	}
	f.secrets.mu.Lock()
	changed := f.secrets.puts != puts || f.secrets.deletes != deletes
	f.secrets.mu.Unlock()
	if changed {
		t.Fatal("a fenced report touched the vault")
	}
	// Deletion and recovery inspect the whole set and cannot clear live custody.
	if state := doctorSubscriptionState(after); state != domain.DiagnosticUnavailable {
		t.Fatal("Doctor reported a fenced owner/member set as healthy", state)
	}
}

func TestMemberAdmissionRejectsForeignInstanceAndDeviceBindings(t *testing.T) {
	f := newMemberFixture(t, true)
	jobs := f.prepareJobs(1)
	leases := f.takeAll(t, jobs)
	defer clear(leases[0].Bundle)
	_, err := f.finishScope(leases[1], leases[1].LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, "not-the-generation", nil, true, true)
	if err == nil {
		t.Fatal("a member finished against a foreign generation")
	}
	_, err = f.finishScope(leases[1], leases[1].LeaseRevision+1, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, leases[1].GenerationId, nil, true, true)
	if err == nil {
		t.Fatal("a member finished with a foreign revision")
	}
	if len(f.members(t)) != 2 {
		t.Fatal("a rejected member Finish changed the set")
	}
}

func TestMemberFinishReplayIsIdempotent(t *testing.T) {
	f := newMemberFixture(t, true)
	jobs := f.prepareJobs(1)
	leases := f.takeAll(t, jobs)
	defer clear(leases[0].Bundle)
	request := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: leases[1].LeaseRevision}, LeaseId: leases[1].LeaseId, GenerationId: leases[1].GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Succeeded: true, CleanupConfirmed: true, FinishScope: pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER}
	first, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, request))
	if err != nil || first.Msg.Replayed {
		t.Fatal(err)
	}
	second, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, request))
	if err != nil || !second.Msg.Replayed {
		t.Fatal("the original member receipt did not replay", err)
	}
	if got := f.members(t); len(got) != 1 || got[0].LeaseID != domain.ID(leases[0].LeaseId) {
		t.Fatal("receipt replay disturbed the owner/member set")
	}
}

func TestLegacySingletonOwnershipRemainsValidAndMembersNeedASharedOwner(t *testing.T) {
	f := newMemberFixture(t, false)
	jobs := f.prepareJobs(0)
	lease, err := f.takeJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	defer clear(lease.Bundle)
	_, account := f.record()
	raw, _ := json.Marshal(account.Subscription)
	if bytes.Contains(raw, []byte(`"members"`)) || bytes.Contains(raw, []byte(`"shared"`)) {
		t.Fatal("a singleton lease serialized the new representation")
	}
	if err := account.Validate(); err != nil {
		t.Fatal("legacy singleton ownership became invalid", err)
	}
	account.Subscription.Members = []domain.SubscriptionMember{{LeaseID: domain.NewID(), OperationID: domain.NewID(), Revision: 1, State: domain.SubscriptionMemberRunning, AdmittedAt: time.Now().UTC()}}
	if account.Validate() == nil {
		t.Fatal("members were accepted on a singleton lease")
	}
	account.Subscription.Members = nil
	account.Subscription.Lease.Shared = true
	account.Subscription.Members = []domain.SubscriptionMember{{LeaseID: account.Subscription.Lease.ID, OperationID: account.Subscription.Lease.OperationID, Revision: 1, State: domain.SubscriptionMemberRunning, AdmittedAt: time.Now().UTC()}, {LeaseID: domain.NewID(), OperationID: account.Subscription.Lease.OperationID, Revision: 1, State: domain.SubscriptionMemberRunning, AdmittedAt: time.Now().UTC()}}
	if account.Validate() == nil {
		t.Fatal("two members owned one job")
	}
}

func TestConcurrentAdmissionJoinsEveryMemberExactlyOnce(t *testing.T) {
	f := newMemberFixture(t, true)
	jobs := f.prepareJobs(15)
	founding, err := f.takeJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	defer clear(founding.Bundle)
	type outcome struct {
		lease *pb.TakeSubscriptionResponse
		err   error
	}
	results := make(chan outcome, len(jobs))
	for _, job := range jobs[1:] {
		go func(job memberJob) {
			lease, err := f.takeJob(job)
			results <- outcome{lease, err}
		}(job)
	}
	revisions := map[uint64]bool{founding.LeaseRevision: true}
	for range jobs[1:] {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if len(result.lease.Bundle) != 0 || result.lease.OwnerLeaseId != founding.LeaseId || revisions[result.lease.LeaseRevision] {
			t.Fatal("a concurrent member received authentication, a foreign owner or a duplicate revision")
		}
		revisions[result.lease.LeaseRevision] = true
	}
	if got := f.members(t); len(got) != 16 {
		t.Fatal("concurrent admission lost or duplicated members", len(got))
	}
}
