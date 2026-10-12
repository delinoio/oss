// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"
)

type claudeSetupFixture struct {
	done        chan struct{}
	submissions int
	closeError  error
}

type blockingClaudeTokenSecrets struct {
	*accountTestSecrets
	entered chan struct{}
	release chan struct{}
}

func (v *blockingClaudeTokenSecrets) Get(ctx context.Context, ref credentials.Ref) ([]byte, error) {
	close(v.entered)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-v.release:
		return v.accountTestSecrets.Get(ctx, ref)
	}
}

func setupFixtureURL() string {
	u, _ := url.Parse(claudeFixtureURL())
	q := u.Query()
	q.Set("scope", "user:inference")
	q.Set("state", strings.Repeat("s", 43))
	u.RawQuery = q.Encode()
	return u.String()
}
func (n *claudeSetupFixture) Progress() (claude.AuthLoginProgress, error) {
	return claude.AuthLoginProgress{URL: setupFixtureURL(), Method: domain.SubscriptionBrowserCode}, nil
}
func (n *claudeSetupFixture) Submit(code []byte) error {
	defer clear(code)
	if !claude.SetupTokenCodeValid(code, setupFixtureURL()) || n.submissions != 0 {
		return subscriptionDenied()
	}
	n.submissions++
	close(n.done)
	return nil
}
func (n *claudeSetupFixture) Done() <-chan struct{} { return n.done }
func (n *claudeSetupFixture) Token() ([]byte, error) {
	return []byte("synthetic-server-claude-token"), nil
}
func (n *claudeSetupFixture) Close() error { return n.closeError }
func connectManagedClaudeFixture(t *testing.T, f *subscriptionFixture) *pb.RequestSubscriptionResponse {
	t.Helper()
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	n := &claudeSetupFixture{done: make(chan struct{})}
	f.service.claudeTokenOpen = func(context.Context, string, domain.ID, *slog.Logger) (serverClaudeTokenNative, error) { return n, nil }
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.service.runServerClaudeToken(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for f.progressFor(op.OperationId).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING {
		if time.Now().After(deadline) {
			t.Fatal("no original progress")
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, _ := f.record()
	code := &pb.SubmitSubscriptionLoginCodeRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, OperationId: op.OperationId, Code: []byte("original#" + strings.Repeat("s", 43))}
	if _, e := f.client.SubmitSubscriptionLoginCode(context.Background(), subscriptionRequest(f.service.Identity.Token, code)); e != nil {
		t.Fatal(e)
	}
	code.Code = []byte("original#" + strings.Repeat("s", 43))
	if _, e := f.client.SubmitSubscriptionLoginCode(context.Background(), subscriptionRequest(f.service.Identity.Token, code)); e != nil {
		t.Fatal("original submit receipt replay", e)
	}
	awaitServerFixture(t, done)
	if n.submissions != 1 {
		t.Fatal("native input replay")
	}
	if f.progressFor(op.OperationId).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED {
		t.Fatal("no settled generation")
	}
	return op
}
func TestManagedClaudeServerLoginZeroEligibleWorkersAndMetadataOnly(t *testing.T) {
	f := newClaudeSubscriptionFixture(t)
	_, e := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.no.eligible.workers", nil, func(tx *store.Tx) (any, error) {
		r, m, e := activeMachine(tx, f.input.MachineID)
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = nil
		_, e = tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
		return nil, e
	})
	if e != nil {
		t.Fatal(e)
	}
	op := connectManagedClaudeFixture(t, f)
	r, a := f.record()
	if a.Connection == nil || a.Health != domain.AccountReady || !a.Subscription.ManagedClaudeToken || a.Subscription.Generation == "" || a.Subscription.OwnerMachineID != "" || a.Subscription.NativeProfileID != "" {
		t.Fatal("server ownership not established")
	}
	for _, secret := range [][]byte{[]byte("synthetic-server-claude-token"), []byte(setupFixtureURL()), []byte("original#" + strings.Repeat("s", 43))} {
		if bytes.Contains(func() []byte { raw, _ := json.Marshal(a); return raw }(), secret) {
			t.Fatal("secret in public account")
		}
	}
	if len(f.service.claudeLoginCodes) != 0 || len(f.service.subscriptionProgress) != 0 {
		t.Fatal("private input not cleared")
	}
	if _, e := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH})); e == nil {
		t.Fatal("token renewal accepted")
	}
	if _, e := f.client.TakeSubscriptionLoginCode(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionLoginCodeRequest{AccountId: string(r.ID), OperationId: op.OperationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), LeaseId: string(domain.NewID())})); e == nil {
		t.Fatal("Worker received server code")
	}
}
func TestManagedClaudeUncertainCleanupNeverConnects(t *testing.T) {
	f := newClaudeSubscriptionFixture(t)
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	n := &claudeSetupFixture{done: make(chan struct{}), closeError: subscriptionDenied()}
	f.service.claudeTokenOpen = func(context.Context, string, domain.ID, *slog.Logger) (serverClaudeTokenNative, error) { return n, nil }
	ctx, cancel := context.WithCancel(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}))
	done := make(chan struct{})
	go func() { defer close(done); f.service.runServerClaudeToken(ctx, f.input.AccountID) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	awaitServerFixture(t, done)
	_, a := f.record()
	if a.Connection != nil || a.Subscription.Generation != "" || !a.Subscription.RecoveryRequired || !a.Subscription.ServerOperation.NativeStarted {
		t.Fatal("uncertain native ownership released")
	}
	if f.progressFor(op.OperationId).State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_RECOVERY_REQUIRED {
		t.Fatal("recovery not exposed")
	}
}
func managedClaudeExecution(t *testing.T, f *subscriptionFixture) (domain.ID, uint64) {
	t.Helper()
	_, a := f.record()
	input := f.input
	input.ConnectionID = a.Connection.ID
	input.Configuration.Harness = domain.ClaudeCode
	input.Configuration.SubscriptionService = domain.SubscriptionClaude
	input.Configuration.NativeModel = "claude-sonnet-4-6"
	input.Configuration.Options = domain.AgentOptions{Permission: domain.PermissionDefault}
	input.Configuration.ModelID = (domain.ModelIdentity{SubscriptionService: domain.SubscriptionClaude, NativeID: input.Configuration.NativeModel}).Key()
	input.Installation.Harness = domain.ClaudeCode
	input.Installation.Version = domain.ClaudeProtocolVersion
	input.Installation.Protocol.Protocol = domain.ProtocolFor(domain.ClaudeCode)
	input.ConfigurationDigest, _ = input.Configuration.Digest()
	if e := input.Validate(); e != nil {
		t.Fatal("fixture input", e)
	}
	raw, _ := json.Marshal(input)
	id := domain.NewID()
	var revision uint64
	_, e := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claude.execution", nil, func(tx *store.Tx) (any, error) {
		r, e := tx.PutJob(id, 0, "", input.SessionID, domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobClaimed, MachineID: input.MachineID, InstanceID: f.instance, AssignedDeviceID: f.device, Input: raw, AcceptedAt: time.Now().UTC()})
		revision = r.Revision
		return nil, e
	})
	if e != nil {
		t.Fatal(e)
	}
	return id, revision
}
func TestManagedClaudeExclusiveExecutionPreservesGeneration(t *testing.T) {
	f := newClaudeSubscriptionFixture(t)
	connectManagedClaudeFixture(t, f)
	_, e := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.token.capability", nil, func(tx *store.Tx) (any, error) {
		r, m, e := activeMachine(tx, f.input.MachineID)
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.ManagedClaudeTokensV1)
		_, e = tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
		return nil, e
	})
	if e != nil {
		t.Fatal(e)
	}
	job, revision := managedClaudeExecution(t, f)
	take := func(w *subscriptionFixture, job domain.ID, revision uint64) (*pb.TakeSubscriptionResponse, error) {
		response, e := f.client.TakeSubscription(context.Background(), subscriptionRequest(w.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: revision}, MachineId: string(w.input.MachineID), InstanceId: string(w.instance), OperationId: string(job), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}))
		if e != nil {
			return nil, e
		}
		return response.Msg, nil
	}
	first, e := take(f, job, revision)
	if e != nil {
		t.Fatal(e)
	}
	if first.NativeProfileId != "" || len(first.Bundle) == 0 {
		t.Fatal("personal profile substituted")
	}
	if _, e := take(f, job, revision); e == nil {
		t.Fatal("concurrent claim accepted")
	}
	generation := first.GenerationId
	connection := func() domain.ID { _, a := f.record(); return a.Connection.ID }()
	finish := &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: first.LeaseRevision}, LeaseId: first.LeaseId, GenerationId: first.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Bundle: bytes.Clone(first.Bundle), Succeeded: true, CleanupConfirmed: true}
	if _, e = f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, finish)); e != nil {
		t.Fatal(e)
	}
	finish.Bundle = bytes.Clone(first.Bundle)
	if _, e = f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, finish)); e != nil {
		t.Fatal("Finish receipt replay", e)
	}
	secondWorker := managedClaudeSecondWorker(t, f)
	secondJob, secondRevision := managedClaudeExecution(t, secondWorker)
	second, e := take(secondWorker, secondJob, secondRevision)
	if e != nil {
		t.Fatal(e)
	}
	if second.GenerationId != generation || !bytes.Equal(first.Bundle, second.Bundle) {
		t.Fatal("settled generation rotated")
	}
	if _, e = secondWorker.finish(second, second.Bundle, true, false, true); e != nil {
		t.Fatal(e)
	}
	_, a := f.record()
	if string(a.Subscription.Generation) != generation || a.Connection.ID != connection || a.Subscription.Lease != nil {
		t.Fatal("original generation or connection lost")
	}
}

func TestManagedClaudeTokenRevalidatesCancellationAfterVaultRead(t *testing.T) {
	f := newClaudeSubscriptionFixture(t)
	connectManagedClaudeFixture(t, f)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.token.capability", nil, func(tx *store.Tx) (any, error) {
		r, m, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.ManagedClaudeTokensV1)
		_, err = tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	job, revision := managedClaudeExecution(t, f)
	blocked := &blockingClaudeTokenSecrets{accountTestSecrets: f.secrets, entered: make(chan struct{}), release: make(chan struct{})}
	f.service.accountSecrets = blocked
	type takeResult struct {
		response *pb.TakeSubscriptionResponse
		err      error
	}
	done := make(chan takeResult, 1)
	go func() {
		response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{
			Mutation:  &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: revision},
			MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: string(job), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE,
		}))
		if response != nil {
			done <- takeResult{response: response.Msg, err: err}
			return
		}
		done <- takeResult{err: err}
	}()
	<-blocked.entered
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.token.stop-during-vault-read", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.RequestJobCancellation(job)
	})
	close(blocked.release)
	if err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err == nil || result.response != nil {
		t.Fatal("stopped execution received the managed Claude token", result.err)
	}
	_, account := f.record()
	if account.Subscription.Lease == nil || !account.Subscription.RecoveryRequired || account.Health != domain.AccountFailed {
		t.Fatal("refused token delivery did not retain cleanup recovery ownership")
	}
}

func managedClaudeSecondWorker(t *testing.T, f *subscriptionFixture) *subscriptionFixture {
	t.Helper()
	w := *f
	authority := *f.authorityFixture
	w.authorityFixture = &authority
	w.input = f.input
	w.input.MachineID = domain.NewID()
	w.device = domain.NewID()
	w.instance = domain.NewID()
	w.workerToken = "fixture-second-worker-token"
	_, e := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.second.claude.worker", nil, func(tx *store.Tx) (any, error) {
		_, m, e := activeMachine(tx, f.input.MachineID)
		if e != nil {
			return nil, e
		}
		m.Name = "Second token Worker"
		if _, e = tx.Put(domain.MachineKind, w.input.MachineID, 0, "", "", m); e != nil {
			return nil, e
		}
		if _, e = tx.Put(domain.DeviceKind, w.device, 0, "", "", domain.Device{Name: "Second Worker", Type: domain.WorkerDevice, MachineID: w.input.MachineID}); e != nil {
			return nil, e
		}
		digest := sha256.Sum256([]byte(w.workerToken))
		if e = tx.PutCredential(w.device, digest[:]); e != nil {
			return nil, e
		}
		return nil, tx.SetWorkerInstance(w.input.MachineID, w.instance, time.Now().UTC())
	})
	if e != nil {
		t.Fatal(e)
	}
	return &w
}
func TestManagedClaudeOldWorkerGetsNoToken(t *testing.T) {
	f := newClaudeSubscriptionFixture(t)
	connectManagedClaudeFixture(t, f)
	job, revision := managedClaudeExecution(t, f)
	if _, e := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: string(job), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE})); e == nil {
		t.Fatal("old Worker received token")
	}
	_, a := f.record()
	if a.Subscription.Lease != nil {
		t.Fatal("unsupported Worker claimed generation")
	}
}

func TestManagedClaudeRestartNeverReplaysQueuedLogin(t *testing.T) {
	f := newClaudeSubscriptionFixture(t)
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	f.service.subscriptionEpoch = domain.NewID()
	if e := f.service.initializeServerSubscriptions(context.Background()); e != nil {
		t.Fatal(e)
	}
	launches := 0
	f.service.claudeTokenOpen = func(context.Context, string, domain.ID, *slog.Logger) (serverClaudeTokenNative, error) {
		launches++
		return nil, subscriptionDenied()
	}
	f.service.runServerClaudeToken(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
	_, a := f.record()
	if launches != 0 || a.Connection != nil || a.Subscription.Pending != nil || a.Subscription.ServerOperation.State != domain.SubscriptionCanceled {
		t.Fatal("queued previous-epoch login replayed", op.OperationId)
	}
}
func TestManagedClaudeVaultPublicationLossReconcilesOnlyCleanup(t *testing.T) {
	f := newClaudeSubscriptionFixture(t)
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	_, e := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claimed.server.token", nil, func(tx *store.Tx) (any, error) {
		r, a, e := claudeSubscriptionAccount(tx, f.input.AccountID, 0)
		if e != nil {
			return nil, e
		}
		a.Subscription.ServerOperation.NativeStarted = true
		a.Subscription.Pending.Phase = domain.SubscriptionClaimed
		_, e = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
		return nil, e
	})
	if e != nil {
		t.Fatal(e)
	}
	_, a := f.record()
	original := *a.Subscription.ServerOperation
	f.secrets.putError = subscriptionDenied()
	if e = f.service.finishServerClaudeToken(context.Background(), f.input.AccountID, original, []byte("synthetic-token"), true, true, nil); e == nil {
		t.Fatal("uncertain vault publication connected")
	}
	if e = f.service.markClaudeTokenRecovery(context.Background(), f.input.AccountID, domain.ID(op.OperationId)); e != nil {
		t.Fatal(e)
	}
	f.secrets.putError = nil
	if e = f.service.recoverServerClaudeToken(context.Background(), f.input.AccountID, domain.ID(op.OperationId)); e != nil {
		t.Fatal(e)
	}
	_, a = f.record()
	if a.Connection != nil || a.Subscription.Generation != "" || a.Subscription.RecoveryRequired || a.Subscription.ServerOperation.NativeStarted {
		t.Fatal("recovery published credentials")
	}
	if len(f.secrets.values) != 0 {
		t.Fatal("staged token reference retained")
	}
}
