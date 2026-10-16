// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	claude bool
}

func newMemberFixture(t *testing.T, negotiated bool) *memberFixture {
	t.Helper()
	f := &memberFixture{subscriptionFixture: newSubscriptionFixture(t)}
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
	if f.claude {
		c := &f.input.Configuration
		c.Harness, c.SubscriptionService, c.ProviderID = domain.ClaudeCode, domain.SubscriptionClaude, ""
		c.ModelID = (domain.ModelIdentity{SubscriptionService: domain.SubscriptionClaude, NativeID: c.NativeModel}).Key()
		c.Options = domain.AgentOptions{Permission: domain.PermissionDefault}
		f.input.Installation = domain.Installation{Harness: domain.ClaudeCode, State: domain.InstallationDetected, Version: domain.ClaudeProtocolVersion, ProtocolVerified: true, Protocol: &domain.ProtocolObservation{Protocol: domain.ProtocolFor(domain.ClaudeCode), State: domain.ProtocolVerified}}
	}
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

func TestSharedCodexOwnerKeepsProcessWideImageProfileExclusive(t *testing.T) {
	f := newMemberFixture(t, true)
	f.input.NativeImageGeneration = true
	jobs := f.prepareJobs(1)
	first, err := f.takeJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	defer clear(first.Bundle)
	if first.OwnerLeaseId != "" {
		t.Fatal("singleton image profile joined a shared owner")
	}
	_, account := f.record()
	if account.Subscription.SharedOwner() || len(account.Subscription.Members) != 0 {
		t.Fatal("process-wide image profile founded a shared owner")
	}
	if _, err := f.takeJob(jobs[1]); err == nil {
		t.Fatal("second image session bypassed its singleton lease")
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

// claudeMemberFixture connects a device-owned Claude account through the real
// native login Take/Finish, negotiates ClaudeDeviceOwnedMembersV1 and prepares
// claimed Claude execute jobs on the same account.
func newClaudeMemberFixture(t *testing.T, negotiated bool) *memberFixture {
	t.Helper()
	f := &memberFixture{subscriptionFixture: newClaudeSubscriptionFixture(t), claude: true}
	op := claudeStart(t, f.subscriptionFixture, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	finish := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), CleanupConfirmed: true, Succeeded: true, NativeIdentity: &pb.NativeSubscriptionIdentity{ProfileId: lease.NativeProfileId, IdentityCommitment: strings.Repeat("ab", 32)}}
	if _, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, finish)); err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claude-member-capability", nil, func(tx *store.Tx) (any, error) {
		mr, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		if negotiated {
			machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.ClaudeDeviceOwnedMembersV1)
		}
		return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestDeviceOwnedClaudeMembersShareOneProfileOwnerWithoutCap(t *testing.T) {
	f := newClaudeMemberFixture(t, true)
	jobs := f.prepareJobs(5)
	var leases []*pb.TakeSubscriptionResponse
	for index, job := range jobs {
		lease, err := f.takeJob(job)
		if err != nil {
			t.Fatalf("Claude job %d was not admitted: %v", index, err)
		}
		if len(lease.Bundle) != 0 || lease.NativeProfileId == "" || (index == 0 && lease.OwnerLeaseId != "") || (index > 0 && lease.OwnerLeaseId != leases[0].LeaseId) {
			t.Fatal("Claude members must name the one canonical profile and carry no authentication")
		}
		leases = append(leases, lease)
	}
	_, account := f.record()
	if !account.Subscription.SharedOwner() || len(account.Subscription.Members) != 6 || account.Subscription.NativeProfileID == "" {
		t.Fatal("six Claude sessions were not admitted to one profile owner")
	}
	// Member Finish never reports identity and never moves the generation.
	generation, identity := account.Subscription.Generation, account.Subscription.IdentityCommitment
	for _, index := range []int{4, 0} {
		if _, err := f.finishScope(leases[index], leases[index].LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, leases[index].GenerationId, nil, true, true); err != nil {
			t.Fatal(err)
		}
	}
	// An identity or refresh confirmation on a member Finish is refused.
	rejected := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: leases[1].LeaseRevision}, LeaseId: leases[1].LeaseId, GenerationId: leases[1].GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), CleanupConfirmed: true, Succeeded: true, FinishScope: pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, NativeIdentity: &pb.NativeSubscriptionIdentity{ProfileId: leases[1].NativeProfileId, IdentityCommitment: strings.Repeat("ab", 32)}}
	if _, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, rejected)); err == nil {
		t.Fatal("a member Finish accepted identity authority")
	}
	for _, index := range []int{1, 2, 3} {
		if _, err := f.finishScope(leases[index], leases[index].LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, leases[index].GenerationId, nil, true, true); err != nil {
			t.Fatal(err)
		}
	}
	_, mid := f.record()
	if mid.Subscription.Generation != generation || mid.Subscription.IdentityCommitment != identity || len(mid.Subscription.Members) != 1 || mid.Subscription.Members[0].LeaseID != domain.ID(leases[5].LeaseId) {
		t.Fatal("member Finish disturbed credentials or the last member")
	}
	// The last member retires the owner with the confirmed native identity.
	owner := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: leases[0].LeaseRevision}, LeaseId: leases[5].LeaseId, GenerationId: leases[5].GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), CleanupConfirmed: true, Succeeded: true, FinishScope: pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, NativeIdentity: &pb.NativeSubscriptionIdentity{ProfileId: leases[0].NativeProfileId, IdentityCommitment: identity}}
	if _, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, owner)); err != nil {
		t.Fatal(err)
	}
	_, after := f.record()
	if after.Subscription.Lease != nil || len(after.Subscription.Members) != 0 || after.Subscription.RecoveryRequired || after.Health != domain.AccountReady || after.Subscription.Generation != generation {
		t.Fatal("Claude owner retirement did not release the account")
	}
}

func TestDeviceOwnedClaudeStaysExclusiveForOldPeers(t *testing.T) {
	f := newClaudeMemberFixture(t, false)
	jobs := f.prepareJobs(1)
	first, err := f.takeJob(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.takeJob(jobs[1]); domain.SafeError(rpc.ClientError(err)).Code != domain.ResourceExhausted {
		t.Fatal("an old Claude peer shared the profile", err)
	}
	_, account := f.record()
	if account.Subscription.SharedOwner() || len(account.Subscription.Members) != 0 || first.OwnerLeaseId != "" {
		t.Fatal("an old Claude peer produced the shared representation")
	}
}

func TestClaudeOwnerLossAndLogoutJoinEveryMember(t *testing.T) {
	f := newClaudeMemberFixture(t, true)
	jobs := f.prepareJobs(2)
	var leases []*pb.TakeSubscriptionResponse
	for _, job := range jobs {
		lease, err := f.takeJob(job)
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
	}
	// Logout is accepted behind the members: readiness is revoked at once and the
	// lifecycle Take waits for every member.
	op := claudeStart(t, f.subscriptionFixture, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	if _, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT); domain.SafeError(rpc.ClientError(err)).Code != domain.ResourceExhausted {
		t.Fatal("Claude logout overlapped live members", err)
	}
	if _, err := f.takeJob(memberJob{ID: domain.NewID(), Revision: 1}); err == nil {
		t.Fatal("fresh Claude admission stayed open behind a queued logout")
	}
	if err := f.service.retainLostSubscriptionLeases(f.input.MachineID, f.instance, true); err != nil {
		t.Fatal(err)
	}
	_, account := f.record()
	if !account.Subscription.RecoveryRequired || len(account.Subscription.Members) != 3 {
		t.Fatal("owner loss did not fence the Claude owner/member set")
	}
	for _, lease := range leases {
		if _, err := f.finishScope(lease, lease.LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, lease.GenerationId, nil, true, true); err == nil {
			t.Fatal("a fenced Claude member finished")
		}
	}
}

// The retained request digest of every historical Finish receipt hashes the
// legacy input shape, so the new scope field must not appear unless it is used.
func TestLegacyFinishReceiptIdentityIsUnchanged(t *testing.T) {
	codex, _ := json.Marshal(subscriptionFinishInput{Account: "a", Lease: "l", Machine: "m", Instance: "i", Generation: "g", Commitment: "c", Revision: 3, Cleanup: true, Refresh: false, Success: true})
	if got, want := string(codex), `{"Account":"a","Lease":"l","Machine":"m","Instance":"i","Generation":"g","Commitment":"c","Revision":3,"Cleanup":true,"Refresh":false,"Success":true}`; got != want {
		t.Fatalf("Codex Finish identity changed:\n%s\n%s", got, want)
	}
	claude, _ := json.Marshal(claudeFinishInput{Account: "a", Lease: "l", Machine: "m", Instance: "i", Generation: "g", Profile: "p", Identity: "x", Revision: 3, Cleanup: true, Success: true})
	if got, want := string(claude), `{"Account":"a","Lease":"l","Machine":"m","Instance":"i","Generation":"g","Profile":"p","Identity":"x","Revision":3,"Cleanup":true,"Success":true}`; got != want {
		t.Fatalf("Claude Finish identity changed:\n%s\n%s", got, want)
	}
	scoped, _ := json.Marshal(subscriptionFinishInput{Scope: domain.SubscriptionFinishMember})
	if !strings.Contains(string(scoped), `"Scope":"member"`) {
		t.Fatal("a scoped Finish must bind its scope into the receipt identity")
	}
}

func TestUnknownFinishScopeFailsClosedForSingletonLease(t *testing.T) {
	if scope, err := subscriptionFinishScope(pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_UNSPECIFIED); err != nil || scope != domain.SubscriptionFinishLegacy {
		t.Fatal("UNSPECIFIED must keep the legacy singleton scope", scope, err)
	}
	for _, unknown := range []pb.SubscriptionFinishScope{3, 99, -1} {
		if _, err := subscriptionFinishScope(unknown); domain.SafeError(err).Code != domain.InvalidArgument {
			t.Fatal("an unknown finish scope was accepted", unknown, err)
		}
	}
	for name, f := range map[string]*memberFixture{"codex": newMemberFixture(t, false)} {
		jobs := f.prepareJobs(0)
		lease, err := f.takeJob(jobs[0])
		if err != nil {
			t.Fatal(name, err)
		}
		defer clear(lease.Bundle)
		_, before := f.record()
		f.secrets.mu.Lock()
		puts, deletes := f.secrets.puts, f.secrets.deletes
		f.secrets.mu.Unlock()
		bundle := subscriptionTestBundle("fixture-account", "must-not-write-unknown-scope", time.Now().UTC())
		_, err = f.finishScope(lease, lease.LeaseRevision, pb.SubscriptionFinishScope(99), lease.GenerationId, bundle, true, true)
		clear(bundle)
		if domain.SafeError(rpc.ClientError(err)).Code != domain.InvalidArgument {
			t.Fatal(name, "a singleton Finish with an unknown scope was not rejected", err)
		}
		_, after := f.record()
		f.secrets.mu.Lock()
		changed := f.secrets.puts != puts || f.secrets.deletes != deletes
		f.secrets.mu.Unlock()
		if changed || after.Subscription.Lease == nil || after.Subscription.Lease.ID != before.Subscription.Lease.ID || after.Subscription.Generation != before.Subscription.Generation {
			t.Fatal(name, "an unknown finish scope settled the singleton lease or touched credentials")
		}
	}
	// The device-owned Claude Finish path rejects the same value before any lease lookup.
	claude := newClaudeMemberFixture(t, false)
	if _, err := claude.finishScope(&pb.TakeSubscriptionResponse{LeaseId: string(domain.NewID())}, 1, pb.SubscriptionFinishScope(99), string(domain.NewID()), nil, true, true); domain.SafeError(rpc.ClientError(err)).Code != domain.InvalidArgument {
		t.Fatal("a Claude Finish with an unknown scope was not rejected", err)
	}
}

func TestClaudeRefreshQueuesBehindSharedOwnerAndClosesAdmission(t *testing.T) {
	f := newClaudeMemberFixture(t, true)
	jobs := f.prepareJobs(2)
	var leases []*pb.TakeSubscriptionResponse
	for index, job := range jobs[:2] {
		lease, err := f.takeJob(job)
		if err != nil {
			t.Fatalf("Claude job %d was not admitted: %v", index, err)
		}
		leases = append(leases, lease)
	}
	refresh := claudeStart(t, f.subscriptionFixture, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	_, account := f.record()
	if account.Subscription.Pending == nil || account.Subscription.Pending.Action != domain.SubscriptionRefresh || !account.Subscription.SharedOwner() || len(account.Subscription.Members) != 2 {
		t.Fatal("a refresh was not accepted behind the live shared owner")
	}
	if err := account.Validate(); err != nil {
		t.Fatal("queued refresh behind the owner produced an invalid document", err)
	}
	// Admission is closed from acceptance and the lifecycle Take waits.
	if _, err := f.takeJob(jobs[2]); err == nil {
		t.Fatal("a queued refresh left shared admission open")
	}
	if _, err := f.take(refresh, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH); domain.SafeError(rpc.ClientError(err)).Code != domain.ResourceExhausted {
		t.Fatal("refresh authority overlapped live members", err)
	}
	// The shared owner's identifier never advances the queued operation's native progress.
	progress := &pb.PublishSubscriptionProgressRequest{AccountId: string(f.input.AccountID), LeaseId: leases[0].LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), State: pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING, LoginMethod: pb.SubscriptionLoginMethod_SUBSCRIPTION_LOGIN_METHOD_BROWSER_CODE, Url: claudeFixtureURL()}
	if _, err := f.client.PublishSubscriptionProgress(context.Background(), subscriptionRequest(f.workerToken, progress)); err == nil {
		t.Fatal("the shared owner advanced a queued refresh")
	}
	taken := &pb.TakeSubscriptionLoginCodeRequest{AccountId: string(f.input.AccountID), LeaseId: leases[0].LeaseId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: refresh.OperationId}
	if _, err := f.client.TakeSubscriptionLoginCode(context.Background(), subscriptionRequest(f.workerToken, taken)); err == nil {
		t.Fatal("the shared owner read a queued refresh's login code")
	}
	if _, err := f.finishScope(leases[1], leases[1].LeaseRevision, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER, leases[0].GenerationId, nil, true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.take(refresh, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH); err == nil {
		t.Fatal("refresh authority overlapped the remaining member")
	}
	_, mid := f.record()
	owner := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: leases[0].LeaseRevision}, LeaseId: leases[0].LeaseId, GenerationId: leases[0].GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), CleanupConfirmed: true, Succeeded: true, FinishScope: pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER, NativeIdentity: &pb.NativeSubscriptionIdentity{ProfileId: leases[0].NativeProfileId, IdentityCommitment: mid.Subscription.IdentityCommitment}}
	if _, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, owner)); err != nil {
		t.Fatal(err)
	}
	_, drained := f.record()
	if drained.Subscription.Lease != nil || drained.Subscription.Pending == nil || drained.Subscription.Pending.Action != domain.SubscriptionRefresh {
		t.Fatal("owner retirement did not leave the queued refresh pending")
	}
	if _, err := f.take(refresh, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH); err != nil {
		t.Fatal("refresh authority did not follow the drained owner", err)
	}
	_, held := f.record()
	if held.Subscription.Lease == nil || held.Subscription.Lease.Shared || held.Subscription.Lease.Action != domain.SubscriptionRefresh || !claudeLifecycleLeaseHeld(held.Subscription) {
		t.Fatal("the refresh Take did not hold the exclusive lifecycle lease")
	}
}

func TestClaudeRefreshStaysDeniedBehindExclusiveLease(t *testing.T) {
	f := newClaudeMemberFixture(t, false)
	jobs := f.prepareJobs(0)
	if _, err := f.takeJob(jobs[0]); err != nil {
		t.Fatal(err)
	}
	r, _ := f.record()
	_, err := claudeTryLegacyRequest(f.subscriptionFixture, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH})
	if err == nil {
		t.Fatal("a refresh was accepted behind an exclusive singleton lease")
	}
}

// A shared owner never retains more members than one recovery settlement request
// can carry, so every valid fenced owner stays recoverable.
func TestSharedMemberAdmissionStopsAtTheRecoverySettlementBound(t *testing.T) {
	service := &Service{}
	machine, instance, device := domain.NewID(), domain.NewID(), domain.NewID()
	generation := domain.NewID()
	lease := &domain.SubscriptionLease{ID: domain.NewID(), MachineID: machine, InstanceID: instance, DeviceID: device, Epoch: service.subscriptionServerEpoch(), Generation: generation, Action: domain.SubscriptionExecute, Shared: true}
	state := &domain.SubscriptionState{Generation: generation, Lease: lease}
	for i := 0; i < domain.MaxSubscriptionMembers-1; i++ {
		state.AdmitMember(domain.SubscriptionMember{LeaseID: domain.NewID(), OperationID: domain.NewID(), Revision: 1, State: domain.SubscriptionMemberRunning})
	}
	if err := service.sharedMemberAdmissible(state, machine, instance, device, domain.NewID()); err != nil {
		t.Fatal("a member below the bound was refused", err)
	}
	state.AdmitMember(domain.SubscriptionMember{LeaseID: domain.NewID(), OperationID: domain.NewID(), Revision: 1, State: domain.SubscriptionMemberRetained})
	state.Members[0].State = domain.SubscriptionMemberRunning
	err := service.sharedMemberAdmissible(state, machine, instance, device, domain.NewID())
	if domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("a member beyond the settlement bound was admitted", err)
	}
	if maxRecoveryMembers != domain.MaxSubscriptionMembers {
		t.Fatal("recovery and admission bounds diverged")
	}
}
