// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type sharedCodexServer struct {
	*sharedClaudeServer
	bundle []byte
}

func (s *sharedCodexServer) TakeSubscription(ctx context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (*connect.Response[pb.TakeSubscriptionResponse], error) {
	response, err := s.sharedClaudeServer.TakeSubscription(ctx, req)
	if err == nil && response.Msg.OwnerLeaseId == "" {
		response.Msg.Bundle = append([]byte(nil), s.bundle...)
	}
	return response, err
}

func (s *sharedCodexServer) FinishSubscription(ctx context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	if req.Msg.FinishScope == pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER && len(req.Msg.Bundle) != 0 {
		s.t.Error("member carried authentication")
	}
	return s.sharedClaudeServer.FinishSubscription(ctx, req)
}

func TestSharedCodexWorkerRunsTwoAndEightSessions(t *testing.T) {
	for _, count := range []int{2, 8} {
		t.Run(fmt.Sprintf("members-%d", count), func(t *testing.T) {
			testSharedCodexWorkerSessions(t, count, false, false)
		})
	}
}

func TestSharedCodexWorkerRetriesScopedFinishes(t *testing.T) {
	testSharedCodexWorkerSessions(t, 2, true, false)
}

func TestSharedCodexWorkerFencesOwnerLoss(t *testing.T) {
	testSharedCodexWorkerSessions(t, 2, false, true)
}

func TestSharedCodexFinishWaitsForAccountGate(t *testing.T) {
	gate := newCodexOwnerRegistry().gate(domain.NewID())
	if err := gate.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	acquired := make(chan struct{})
	go func() {
		gate.acquireForFinish()
		close(acquired)
		gate.release()
	}()
	select {
	case <-acquired:
		t.Fatal("Finish entered while another account operation held the gate")
	case <-time.After(50 * time.Millisecond):
	}
	gate.release()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("Finish did not enter after the account gate was released")
	}
}

func testSharedCodexWorkerSessions(t *testing.T, count int, retry, loss bool) {
	first := newCheckpointFixture(t)
	root := first.root
	home, err := codexAccountHistoryHome(root, first.input.AccountID)
	if err != nil || security.PrivateDir(home) != nil {
		t.Fatal("account home", err)
	}
	mode := fmt.Sprint(count)
	if loss {
		mode = "loss-" + mode
	}
	if err := os.WriteFile(filepath.Join(home, "shared-fixture"), []byte(mode), 0600); err != nil {
		t.Fatal(err)
	}
	bundle := workerSubscriptionBundle("first")
	defer clear(bundle)
	serverModel := &sharedClaudeServer{t: t, config: Config{Root: root}, account: first.input.AccountID, profile: domain.NewID(), gen: domain.NewID()}
	if retry {
		serverModel.failMemberFinishes = 1
		serverModel.dropOwnerFinishes = 1
	}
	server := &sharedCodexServer{sharedClaudeServer: serverModel, bundle: bundle}
	_, handler := delidevv1connect.NewSubscriptionServiceHandler(server)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	token, err := security.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	credential := Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: httpServer.URL, ServerID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID(), MachineID: first.input.MachineID, Token: token}
	serverModel.cred = credential
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	registry := newCodexOwnerRegistry()
	manager := &workspace.Manager{Root: root}
	type result struct {
		completion domain.ExecutionCompletion
		err        error
	}
	results := make([]result, count)
	configs := make([]Config, count)
	jobs := make([]domain.Job, count)
	jobIDs := make([]domain.ID, count)
	for i := 0; i < count; i++ {
		input := first.input
		input.SessionID, input.ExecutionID, input.InputID = domain.NewID(), domain.NewID(), domain.NewID()
		input.ThreadRequestID, input.TurnRequestID = domain.NewID(), domain.NewID()
		input.Input.Prompt = fmt.Sprintf("Independent fixture session %d", i)
		input.Configuration.Subscription = true
		input.ConfigurationDigest, err = input.Configuration.Digest()
		if err != nil {
			t.Fatal(err)
		}
		input.Installation.ResolvedPath = binary
		preparation := workspace.PrepareRequest{SessionID: input.SessionID, MachineID: input.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
		manifest, prepareErr := manager.Prepare(ctx, preparation)
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		input.Preparation, _ = json.Marshal(preparation)
		input.Manifest, _ = json.Marshal(manifest)
		jobIDs[i] = domain.NewID()
		jobs[i] = domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobClaimed, MachineID: input.MachineID, InstanceID: domain.NewID(), AcceptedAt: time.Now().UTC()}
		jobs[i].Input, _ = json.Marshal(input)
		document, _ := json.Marshal(jobs[i])
		resource := &pb.Resource{Id: string(jobIDs[i]), Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, Revision: 9, SessionId: string(input.SessionID), DocumentJson: document}
		publication := &PublicationConfig{Credential: credential, Instance: jobs[i].InstanceID, Assignment: resource, Client: &managedExecutionPublicationRPC{nativeHome: home}}
		configs[i] = Config{Root: root, Logger: slog.New(slog.NewJSONHandler(os.Stderr, nil)), execution: publication, executionContext: ctx, codexMembers: true, codexOwners: registry}
	}
	var wg sync.WaitGroup
	for i := range configs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			output, runErr := executeSession(ctx, configs[i], jobIDs[i], jobs[i])
			results[i].err = runErr
			if runErr == nil {
				_ = domain.Decode(output, &results[i].completion)
			}
		}()
	}
	wg.Wait()
	if loss {
		for i, result := range results {
			if result.err == nil {
				t.Fatalf("member %d continued after owner loss", i)
			}
		}
		if _, err := os.Lstat(filepath.Join(home, "auth.json")); err != nil {
			t.Fatal("owner loss removed authentication", err)
		}
		return
	}
	for i, result := range results {
		if result.err != nil || result.completion.Outcome != domain.ExecutionSucceeded || !result.completion.CleanupVerified || result.completion.NativeCheckpointDigest == "" {
			t.Fatalf("member %d failed: %#v, %v", i, result.completion, result.err)
		}
	}
	serverModel.mu.Lock()
	defer serverModel.mu.Unlock()
	wantFinishes := count
	if retry {
		wantFinishes += 2
	}
	if len(serverModel.takes) != count || len(serverModel.finishes) != wantFinishes || serverModel.owner != "" {
		t.Fatalf("owner/member lease counts: takes=%d finishes=%d owner=%s", len(serverModel.takes), len(serverModel.finishes), serverModel.owner)
	}
	owners, members := 0, 0
	for _, finish := range serverModel.finishes {
		switch finish.FinishScope {
		case pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER:
			owners++
			if !finish.CleanupConfirmed || !finish.Succeeded || subscription.Refreshed(bundle, finish.Bundle) != nil {
				t.Fatal("owner did not write back refreshed authentication")
			}
		case pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER:
			members++
			if !finish.CleanupConfirmed || len(finish.Bundle) != 0 {
				t.Fatal("member completion changed authentication")
			}
		default:
			t.Fatal("shared execution used singleton Finish")
		}
	}
	if owners != 1 || members != count-1 {
		if !retry {
			t.Fatalf("finish scopes: owners=%d members=%d", owners, members)
		}
	}
	if retry {
		for scope, expected := range map[pb.SubscriptionFinishScope]int{pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_MEMBER: 2, pb.SubscriptionFinishScope_SUBSCRIPTION_FINISH_SCOPE_OWNER: 2} {
			var id string
			attempts := 0
			for _, finish := range serverModel.finishes {
				if finish.FinishScope != scope {
					continue
				}
				attempts++
				if id != "" && id != finish.Mutation.RequestId {
					t.Fatal("Finish retry changed its request identity")
				}
				id = finish.Mutation.RequestId
			}
			if attempts != expected {
				t.Fatalf("scope %v attempts = %d", scope, attempts)
			}
		}
	}
	if _, err := os.Lstat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("account authentication remained after owner retirement")
	}
	// The shared account home is a registered in-place profile whose lock the
	// founding owner released on retirement, with the history left in place.
	profile, err := acquireSharedCodexHome(ctx, Config{Root: root}, first.input.MachineID, first.input.AccountID)
	if err != nil {
		t.Fatalf("retired owner left the shared account home locked or unregistered: %v", err)
	}
	defer profile.Close()
	if profile.Root != home || profile.Origin != nativeprofile.OriginLegacy || profile.Key.Purpose != nativeprofile.PurposeSharedOwner {
		t.Fatalf("shared account home is not registered in place: %s", profile.Root)
	}
}
