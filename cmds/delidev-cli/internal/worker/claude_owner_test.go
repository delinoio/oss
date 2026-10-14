// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

// sharedClaudeServer is a minimal in-test model of the server's owner/member
// rules for one account. It records each request and answers with the account
// document a real server would return.
type sharedClaudeServer struct {
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	mu       sync.Mutex
	t        *testing.T
	config   Config
	cred     Credential
	account  domain.ID
	profile  domain.ID
	gen      domain.ID
	owner    domain.ID
	revision uint64
	members  []domain.SubscriptionMember
	takes    []*pb.TakeSubscriptionRequest
	finishes []*pb.FinishSubscriptionRequest
	lockFree []bool
	// notAdmitted makes the next joined Take fail with the typed member proof.
	notAdmitted bool
	// legacy models a server that does not negotiate members.
	legacy bool
	// recovering models the fenced account: an owner-scope cleanup Finish is
	// accepted for the whole set.
	recovering bool
}

func (s *sharedClaudeServer) document() []byte {
	state := &domain.SubscriptionState{Generation: s.gen, NativeProfileID: s.profile, Members: append([]domain.SubscriptionMember(nil), s.members...)}
	if s.owner != "" {
		state.Lease = &domain.SubscriptionLease{ID: s.owner, Shared: !s.legacy, Revision: 3}
	}
	raw, _ := json.Marshal(domain.Account{Subscription: state})
	return raw
}

func (s *sharedClaudeServer) TakeSubscription(_ context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (*connect.Response[pb.TakeSubscriptionResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.takes = append(s.takes, proto.Clone(req.Msg).(*pb.TakeSubscriptionRequest))
	lease := domain.ID(req.Msg.Mutation.RequestId)
	installation, _ := json.Marshal(domain.Installation{Harness: domain.ClaudeCode})
	response := &pb.TakeSubscriptionResponse{LeaseId: string(lease), GenerationId: string(s.gen), NativeProfileId: string(s.profile), InstallationJson: installation}
	if s.owner == "" {
		s.owner, s.revision = lease, 3
		response.LeaseRevision = 3
	} else {
		if s.notAdmitted {
			s.notAdmitted = false
			problem := connect.NewError(connect.CodeCanceled, nil)
			detail, _ := connect.NewErrorDetail(&pb.ErrorDetail{Code: string(domain.Canceled), Cause: subscriptionMemberNotAdmittedCause})
			problem.AddDetail(detail)
			return nil, problem
		}
		s.revision++
		response.LeaseRevision, response.OwnerLeaseId = s.revision, string(s.owner)
	}
	s.members = append(s.members, domain.SubscriptionMember{LeaseID: lease, OperationID: domain.ID(req.Msg.OperationId), Revision: response.LeaseRevision, State: domain.SubscriptionMemberRunning, AdmittedAt: time.Now().UTC()})
	return connect.NewResponse(response), nil
}

func (s *sharedClaudeServer) FinishSubscription(_ context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishes = append(s.finishes, proto.Clone(req.Msg).(*pb.FinishSubscriptionRequest))
	profileRoot, _ := claudeProfileRoot(s.config.Root, s.cred, s.account, s.profile)
	lock, err := security.TryLock(profileRoot + ".lock")
	s.lockFree = append(s.lockFree, err == nil)
	if err == nil {
		_ = lock.Close()
	}
	lease := domain.ID(req.Msg.LeaseId)
	switch req.Msg.FinishScope {
	case pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER:
		for i := range s.members {
			if s.members[i].LeaseID != lease {
				continue
			}
			if req.Msg.CleanupConfirmed {
				s.members = append(s.members[:i], s.members[i+1:]...)
			} else {
				s.members[i].State = domain.SubscriptionMemberRetained
			}
			break
		}
	case pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER:
		runnable := 0
		for _, m := range s.members {
			if m.State == domain.SubscriptionMemberRunning && m.LeaseID != lease {
				runnable++
			}
		}
		if runnable != 0 && !s.recovering {
			return nil, connect.NewError(connect.CodeAborted, nil)
		}
		s.owner, s.members = "", nil
	default:
		if !s.legacy {
			s.t.Error("a shared owner member used the legacy Finish scope")
		}
		s.owner, s.members = "", nil
	}
	return connect.NewResponse(&pb.FinishSubscriptionResponse{Account: &pb.Resource{Id: string(s.account), Revision: 9, DocumentJson: s.document()}}), nil
}

type sharedClaudeFixture struct {
	*sharedClaudeServer
	config      Config
	installaton domain.Installation
	profileRoot string
}

// newSharedClaudeFixture creates a pinned native profile on disk with the test
// binary standing in for the original CLI, and a Worker configured with the
// negotiated capability.
func newSharedClaudeFixture(t *testing.T, negotiated bool) *sharedClaudeFixture {
	t.Helper()
	base, err := os.MkdirTemp("", "dc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "worker-native-claude-fixture")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	config := Config{Root: root, Logger: slog.New(slog.NewJSONHandler(&logs, nil)), claudeMembers: negotiated, claudeOwners: newClaudeOwnerRegistry()}
	credential := Credential{ServerID: domain.NewID(), MachineID: domain.NewID()}
	installation := domain.Installation{Harness: domain.ClaudeCode, Version: domain.ClaudeProtocolVersion, ResolvedPath: executable}
	account, profileID := domain.NewID(), domain.NewID()
	profile, err := openClaudeProfile(config, credential, account, profileID, installation, domain.NewID(), true)
	if err != nil {
		t.Fatal(err)
	}
	marker := `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","email":"worker@example.invalid","orgId":"fixture-org","subscriptionType":"pro"}`
	if err := os.WriteFile(filepath.Join(profile.home, "fictional-account.json"), []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := profile.identity(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	profileRoot := profile.root
	if err := profile.close(); err != nil {
		t.Fatal(err)
	}
	server := &sharedClaudeServer{t: t, config: config, cred: credential, account: account, profile: profileID, gen: domain.NewID()}
	_, handler := delidevv1connect.NewSubscriptionServiceHandler(server)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	credential.Endpoint = httpServer.URL
	server.cred = credential
	config.execution = &PublicationConfig{Credential: credential, Instance: domain.NewID(), Assignment: &pb.Resource{Revision: 3}}
	return &sharedClaudeFixture{sharedClaudeServer: server, config: config, installaton: installation, profileRoot: profileRoot}
}

func (f *sharedClaudeFixture) input() domain.ExecutionJobInput {
	return domain.ExecutionJobInput{AccountID: f.account, Installation: f.installaton}
}

func (f *sharedClaudeFixture) profileLocked() bool {
	lock, err := security.TryLock(f.profileRoot + ".lock")
	if err == nil {
		_ = lock.Close()
		return false
	}
	return true
}

type startedMember struct {
	profile *nativeClaudeProfile
	finish  func(bool) error
	job     domain.ID
}

func (f *sharedClaudeFixture) start(t *testing.T, count int) []startedMember {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var members []startedMember
	for i := 0; i < count; i++ {
		job := domain.NewID()
		profile, token, finish, err := beginClaudeSubscriptionExecution(ctx, f.config, job, f.input())
		if err != nil || profile == nil || token != nil {
			t.Fatalf("member %d was not admitted: %v", i, err)
		}
		members = append(members, startedMember{profile, finish, job})
	}
	return members
}

func TestClaudeSharedOwnerAdmitsManySessionsOnOneProfileLock(t *testing.T) {
	f := newSharedClaudeFixture(t, true)
	members := f.start(t, 8)
	f.mu.Lock()
	if len(f.takes) != 8 || f.owner != domain.ID(f.takes[0].Mutation.RequestId) {
		t.Fatal("the first session did not found the owner")
	}
	f.mu.Unlock()
	if !f.profileLocked() {
		t.Fatal("the owner did not hold the canonical profile lock")
	}
	seen := map[string]bool{}
	for index, member := range members {
		if member.profile.lock != nil && index > 0 || member.profile.historyVersion != claudeProfileV2 || member.profile.managedLease == nil || seen[member.profile.managedLease.response.LeaseId] || member.profile.home != members[0].profile.home {
			t.Fatal("a member did not get its own lease over the one canonical profile")
		}
		seen[member.profile.managedLease.response.LeaseId] = true
		if index > 0 && member.profile.managedLease.response.OwnerLeaseId != members[0].profile.managedLease.response.LeaseId {
			t.Fatal("a member does not name the owner")
		}
	}
	for index := len(members) - 1; index > 0; index-- {
		if err := members[index].finish(true); err != nil {
			t.Fatal(err)
		}
		if !f.profileLocked() {
			t.Fatal("a sibling's completion released the profile lock", index)
		}
	}
	f.mu.Lock()
	if len(f.finishes) != 7 || f.owner == "" || len(f.members) != 1 {
		t.Fatal("sibling completion retired the owner or stopped the last member")
	}
	for _, finish := range f.finishes {
		if finish.FinishScope != pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER || finish.NativeIdentity != nil || len(finish.Bundle) != 0 || !finish.CleanupConfirmed {
			t.Fatal("a member Finish carried authority beyond its own release")
		}
	}
	f.mu.Unlock()
	if err := members[0].finish(true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	last := f.finishes[len(f.finishes)-1]
	if last.FinishScope != pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER || last.NativeIdentity == nil || last.NativeIdentity.ProfileId != string(f.profile) || !last.CleanupConfirmed || last.Mutation.ExpectedRevision != 3 || f.owner != "" {
		t.Fatal("the last member did not retire the owner with the confirmed identity and owner revision")
	}
	if !f.lockFree[len(f.lockFree)-1] {
		t.Fatal("the owner Finish was reported before the local profile lock was released")
	}
	if f.profileLocked() {
		t.Fatal("the retired owner kept the profile lock")
	}
}

func TestClaudeSharedOwnerUnprovenMemberIsRetainedUntilOwnerClose(t *testing.T) {
	f := newSharedClaudeFixture(t, true)
	members := f.start(t, 3)
	if err := members[1].finish(false); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	retained := false
	for _, m := range f.members {
		retained = retained || m.State == domain.SubscriptionMemberRetained && m.LeaseID == domain.ID(members[1].profile.managedLease.response.LeaseId)
	}
	count := len(f.members)
	f.mu.Unlock()
	if !retained || count != 3 || !f.profileLocked() {
		t.Fatal("an unproven closure released its ownership instead of being retained")
	}
	if err := members[2].finish(true); err != nil {
		t.Fatal(err)
	}
	if err := members[0].finish(true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	last := f.finishes[len(f.finishes)-1]
	if last.FinishScope != pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER || !last.CleanupConfirmed || last.NativeIdentity == nil || f.owner != "" {
		t.Fatal("owner containment did not release the retained member")
	}
}

func TestClaudeSharedOwnerUnprovenFinalClosureFencesInsteadOfClaimingCleanup(t *testing.T) {
	f := newSharedClaudeFixture(t, true)
	members := f.start(t, 2)
	if err := members[0].finish(true); err != nil {
		t.Fatal(err)
	}
	if err := members[1].finish(false); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	last := f.finishes[len(f.finishes)-1]
	if last.FinishScope != pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER || last.CleanupConfirmed || last.Succeeded || last.NativeIdentity != nil {
		t.Fatal("an unproven final closure reported clean owner containment")
	}
}

func TestClaudeOldPeerKeepsExclusiveSingletonLease(t *testing.T) {
	f := newSharedClaudeFixture(t, false)
	f.config.claudeOwners = nil
	f.legacy = true
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	profile, _, finish, err := beginClaudeSubscriptionExecution(ctx, f.config, domain.NewID(), f.input())
	if err != nil || profile.lock == nil || profile.historyVersion != 0 {
		t.Fatal("a non-negotiated Worker did not take the exclusive profile lock", err)
	}
	if err := finish(true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.finishes) != 1 || f.finishes[0].FinishScope != pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_UNSPECIFIED {
		t.Fatal("a non-negotiated Worker sent a scoped Finish")
	}
}

func TestClaudeMemberNotAdmittedIsADefiniteFailureWithoutFence(t *testing.T) {
	f := newSharedClaudeFixture(t, true)
	members := f.start(t, 1)
	f.mu.Lock()
	f.notAdmitted = true
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, _, _, err := beginClaudeSubscriptionExecution(ctx, f.config, domain.NewID(), f.input())
	var delivered *managedTakeNotDelivered
	if err == nil || !asManagedNotDelivered(err, &delivered) {
		t.Fatal("a released member was not reported as a definite no-delivery failure", err)
	}
	f.mu.Lock()
	finishes := len(f.finishes)
	f.mu.Unlock()
	if finishes != 0 || !f.profileLocked() {
		t.Fatal("a released member fenced the owner or its siblings")
	}
	if err := members[0].finish(true); err != nil {
		t.Fatal(err)
	}
}

func asManagedNotDelivered(err error, target **managedTakeNotDelivered) bool {
	for err != nil {
		if typed, ok := err.(*managedTakeNotDelivered); ok {
			*target = typed
			return true
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}

func TestClaudeLegacyHistoryExecutionsAreSerialized(t *testing.T) {
	registry := newClaudeOwnerRegistry()
	gate := registry.gate(domain.NewID())
	release, err := gate.acquireLegacyHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := gate.acquireLegacyHistory(ctx); err == nil {
		t.Fatal("two canonical-profile histories ran at once")
	}
	release()
	again, err := gate.acquireLegacyHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	again()
	if registry.gate(gate2ID(registry, gate)) != gate {
		t.Fatal("the registry did not return the same gate for one account")
	}
}

func gate2ID(registry *claudeOwnerRegistry, gate *claudeAccountGate) domain.ID {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for id, candidate := range registry.accounts {
		if candidate == gate {
			return id
		}
	}
	return ""
}

func TestClaudeCheckpointProfileVersionKeepsVersion1Bytes(t *testing.T) {
	p := claudeExecutionCheckpoint{Version: 1}
	raw, err := json.Marshal(p)
	if err != nil || bytes.Contains(raw, []byte("profile_version")) {
		t.Fatal("a version 1 checkpoint serialized the version 2 field")
	}
	p.ProfileVersion = uint32(claudeProfileV2)
	raw, _ = json.Marshal(p)
	if !bytes.Contains(raw, []byte(`"profile_version":2`)) {
		t.Fatal("a version 2 checkpoint lost its profile version")
	}
	version, err := claudeProfileVersionFor(t.TempDir(), domain.ExecutionJobInput{})
	if err != nil || version != claudeProfileV2 {
		t.Fatal("a fresh session must use the session-private history", err)
	}
}

func TestClaudeSharedOwnerConcurrentAdmissionAndCompletion(t *testing.T) {
	f := newSharedClaudeFixture(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const sessions = 10
	started := make(chan startedMember, sessions)
	failures := make(chan error, sessions*2)
	var wg sync.WaitGroup
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job := domain.NewID()
			profile, _, finish, err := beginClaudeSubscriptionExecution(ctx, f.config, job, f.input())
			if err != nil {
				failures <- err
				return
			}
			started <- startedMember{profile, finish, job}
		}()
	}
	wg.Wait()
	close(started)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var members []startedMember
	for member := range started {
		members = append(members, member)
	}
	if len(members) != sessions {
		t.Fatal("not every concurrent session was admitted", len(members))
	}
	f.mu.Lock()
	founders := 0
	for _, member := range members {
		if member.profile.managedLease.response.OwnerLeaseId == "" {
			founders++
		}
	}
	f.mu.Unlock()
	if founders != 1 || !f.profileLocked() {
		t.Fatal("concurrent admission founded more than one owner", founders)
	}
	finishes := make(chan error, sessions)
	for _, member := range members {
		wg.Add(1)
		go func() {
			defer wg.Done()
			finishes <- member.finish(true)
		}()
	}
	wg.Wait()
	close(finishes)
	for err := range finishes {
		if err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	owners := 0
	for _, finish := range f.finishes {
		if finish.FinishScope == pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER {
			owners++
		}
	}
	if owners != 1 || len(f.finishes) != sessions || f.owner != "" || f.profileLocked() {
		t.Fatal("concurrent completion did not retire the owner exactly once", owners, len(f.finishes))
	}
}

func TestClaudeSharedOwnerRecoveryJoinsEveryMemberBeforeOneOwnerCleanup(t *testing.T) {
	f := newSharedClaudeFixture(t, true)
	members := f.start(t, 3)
	owner := members[0].profile.managedLease.response
	// Simulate a Worker restart: the owner and its members are lost locally, the
	// server fenced the account and only the durable claims remain.
	state := &domain.SubscriptionState{RecoveryRequired: true, NativeProfileID: f.profile, OwnerMachineID: f.cred.MachineID, Generation: f.gen}
	state.Lease = &domain.SubscriptionLease{ID: domain.ID(owner.LeaseId), OperationID: members[0].job, InstanceID: f.config.execution.Instance, Generation: f.gen, Revision: owner.LeaseRevision, Action: domain.SubscriptionExecute, Shared: true}
	for _, member := range members {
		state.Members = append(state.Members, domain.SubscriptionMember{LeaseID: domain.ID(member.profile.managedLease.response.LeaseId), OperationID: member.job, Revision: member.profile.managedLease.response.LeaseRevision, State: domain.SubscriptionMemberRunning, AdmittedAt: time.Now().UTC()})
	}
	// The local owner is gone; release its lock the way a process exit would.
	gate := f.config.claudeOwners.gate(f.account)
	gate.mu.Lock()
	if gate.owner != nil {
		_ = gate.owner.profile.close()
	}
	gate.mu.Unlock()
	f.config.nativeClaudeInstallation = &f.installaton
	client := delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.cred.Endpoint)
	f.mu.Lock()
	f.recovering = true
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runClaudeRecovery(ctx, f.config, client, f.cred, f.account, domain.Account{Subscription: state}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	recovery := f.finishes[len(f.finishes)-1]
	if recovery.FinishScope != pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER || recovery.LeaseId != owner.LeaseId || recovery.Succeeded || !recovery.CleanupConfirmed || recovery.NativeIdentity != nil || recovery.Mutation.ExpectedRevision != owner.LeaseRevision {
		t.Fatal("recovery did not clean the whole owner/member set through one owner Finish")
	}
	for _, member := range members {
		raw, err := security.ReadPrivate(filepath.Join(f.config.Root, "managed-auth", member.profile.managedLease.response.LeaseId+".json"), 16<<10)
		var journal managedSubscriptionJournal
		if err != nil || domain.Decode(raw, &journal) != nil || journal.State != managedReported {
			t.Fatal("a member's original claim stayed unsettled after owner recovery", err)
		}
	}
}
