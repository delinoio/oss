package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func subscriptionTestBundle(account, rotation string, at time.Time) []byte {
	claims, _ := json.Marshal(map[string]any{"email": "fixture@example.invalid", "nonce": rotation, "https://api.openai.com/auth": map[string]string{"chatgpt_account_id": account, "chatgpt_user_id": "fixture-user", "chatgpt_plan_type": "plus"}})
	token := "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(claims) + ".synthetic-signature"
	raw, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "OPENAI_API_KEY": nil, "tokens": map[string]string{"id_token": token, "access_token": token, "refresh_token": "synthetic-refresh-" + rotation, "account_id": account}, "last_refresh": at})
	return raw
}

type subscriptionFixture struct {
	*authorityFixture
	t       *testing.T
	client  delidevv1connect.SubscriptionServiceClient
	secrets *accountTestSecrets
}

func newSubscriptionFixture(t *testing.T) *subscriptionFixture {
	t.Helper()
	f := &subscriptionFixture{authorityFixture: newAuthorityFixture(t, "http://127.0.0.1:46311"), t: t, secrets: &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}}
	f.service.accountSecrets = f.secrets
	f.client = delidevv1connect.NewSubscriptionServiceClient(http.DefaultClient, f.http.URL)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.subscription", nil, func(tx *store.Tx) (any, error) {

		ar, err := tx.Get(domain.AccountKind, f.input.AccountID)
		if err != nil {
			return nil, err
		}
		a, err := store.Decode[domain.Account](ar)
		if err != nil {
			return nil, err
		}
		a.Type = domain.SubscriptionAccount
		a.ProviderID, a.SubscriptionService = "", domain.SubscriptionChatGPT
		a.Connection = nil
		a.Validation = nil
		a.Catalog = nil
		a.Health = domain.AccountDisconnected
		a.Subscription = nil
		if _, err := tx.Put(domain.AccountKind, ar.ID, ar.Revision, "", "", a); err != nil {
			return nil, err
		}
		modelRecord, err := tx.Get(domain.ModelKind, f.input.Configuration.ModelID)
		if err != nil {
			return nil, err
		}
		model, err := store.Decode[domain.Model](modelRecord)
		if err != nil {
			return nil, err
		}
		model.ProviderID, model.SourceKind, model.SubscriptionService = "", domain.SubscriptionModel, domain.SubscriptionChatGPT
		if _, err := tx.Put(domain.ModelKind, modelRecord.ID, modelRecord.Revision, "", "", model); err != nil {
			return nil, err
		}
		f.input.Configuration.ProviderID, f.input.Configuration.SubscriptionService, f.input.Configuration.Subscription = "", domain.SubscriptionChatGPT, true
		f.input.ConfigurationDigest, err = f.input.Configuration.Digest()
		if err != nil {
			return nil, err
		}

		mr, err := tx.Get(domain.MachineKind, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		machine, err := store.Decode[domain.Machine](mr)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = []domain.WorkerCapability{domain.ManagedCodexSubscriptionsV1}
		machine.Installations = []domain.Installation{f.input.Installation}
		machine.Installations[0].ResolvedPath = "/controlled/native/codex"
		observed := time.Now().UTC()
		machine.Installations[0].ObservedAt = &observed
		_, err = tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func subscriptionRequest[T any](token string, value *T) *connect.Request[T] {
	r := connect.NewRequest(value)
	r.Header().Set("Authorization", "Bearer "+token)
	return r
}

func (f *subscriptionFixture) record() (store.Record, domain.Account) {
	f.t.Helper()
	var r store.Record
	var a domain.Account
	err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		var err error
		r, a, err = accountFromTx(tx, f.input.AccountID, 0)
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return r, a
}

func (f *subscriptionFixture) start(action pb.SubscriptionAction) *pb.RequestSubscriptionResponse {
	f.t.Helper()
	r, _ := f.record()
	response, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: action, DeviceCode: action == pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}))
	if err != nil {
		f.t.Fatal(err)
	}
	return response.Msg
}

func (f *subscriptionFixture) take(op *pb.RequestSubscriptionResponse, action pb.SubscriptionAction) (*pb.TakeSubscriptionResponse, error) {
	r, _ := f.record()
	response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: op.OperationId, Action: action}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *subscriptionFixture) finish(lease *pb.TakeSubscriptionResponse, bundle []byte, success, refresh, cleanup bool) (*pb.FinishSubscriptionResponse, error) {
	response, err := f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: lease.LeaseRevision}, LeaseId: lease.LeaseId, GenerationId: lease.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Bundle: bytes.Clone(bundle), Succeeded: success, RefreshConfirmed: refresh, CleanupConfirmed: cleanup}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (f *subscriptionFixture) login() []byte {
	f.t.Helper()
	op := f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		f.t.Fatal(err)
	}
	if len(lease.Bundle) != 0 {
		f.t.Fatal("fresh login borrowed existing credentials")
	}
	raw := subscriptionTestBundle("fixture-account", "first", time.Now().UTC().Add(-time.Minute))
	if _, err := f.finish(lease, raw, true, false, true); err != nil {
		f.t.Fatal(err)
	}
	return raw
}

func TestSubscriptionLifecycleRotationLogoutAndSecretFreeRecords(t *testing.T) {
	f := newSubscriptionFixture(t)
	before := f.login()
	defer clear(before)
	op := f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(lease.Bundle, before) {
		t.Fatal("wrong generation delivered")
	}
	clear(lease.Bundle)
	after := subscriptionTestBundle("fixture-account", "second", time.Now().UTC())
	defer clear(after)
	response, err := f.finish(lease, after, true, true, true)
	if err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if a.Subscription.Lease != nil || a.Subscription.Generation == domain.ID(lease.GenerationId) || a.Health != domain.AccountReady {
		t.Fatal("rotation did not release the exact new generation")
	}
	for _, raw := range [][]byte{before, after} {
		b, _, _ := subscription.Parse(raw)
		for _, secret := range []string{b.Tokens.ID, b.Tokens.Access, b.Tokens.Refresh} {
			ordinary, _ := json.Marshal(response)
			if bytes.Contains(ordinary, []byte(secret)) {
				t.Fatal("secret in ordinary response")
			}
			for _, name := range []string{"state.sqlite", "state.sqlite-wal", "state.sqlite-shm"} {
				data, err := os.ReadFile(filepath.Join(f.service.Store.Root(), name))
				if err == nil && bytes.Contains(data, []byte(secret)) {
					t.Fatal("secret in durable metadata")
				}
			}
		}
	}
	op = f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	lease, err = f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT)
	if err != nil {
		t.Fatal(err)
	}
	clear(lease.Bundle)
	if _, err := f.finish(lease, nil, true, false, true); err != nil {
		t.Fatal(err)
	}
	_, a = f.record()
	if a.Connection != nil || a.Health != domain.AccountDisconnected || a.Subscription.Generation != "" || !a.Enabled {
		t.Fatal("logout changed configuration or retained usable authentication")
	}
	refs, err := f.secrets.UnremovedReferences(context.Background(), f.input.AccountID)
	if err != nil || len(refs) != 0 {
		t.Fatal("logout left protected generations")
	}
}

func TestSubscriptionLeaseRaceAndCanceledCommit(t *testing.T) {
	f := newSubscriptionFixture(t)
	op := f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	var wg sync.WaitGroup
	leases := make(chan *pb.TakeSubscriptionResponse, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			if err == nil {
				leases <- lease
			}
		}()
	}
	wg.Wait()
	close(leases)
	if len(leases) != 1 {
		t.Fatal("account had multiple native owners")
	}
	lease := <-leases
	r, _ := f.record()
	_, err := f.client.CancelSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}}))
	if err != nil {
		t.Fatal(err)
	}
	raw := subscriptionTestBundle("fixture-account", "first", time.Now().UTC().Add(-time.Minute))
	defer clear(raw)
	if _, err := f.finish(lease, raw, true, false, true); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if a.Connection != nil || a.Subscription.Generation != "" || a.Subscription.Lease != nil {
		t.Fatal("cancel raced into a usable connection")
	}
	refs, _ := f.secrets.UnremovedReferences(context.Background(), r.ID)
	if len(refs) != 0 {
		t.Fatal("canceled staging was not cleaned")
	}
}

func TestSubscriptionRevocationBeforePublicationCannotCreateConnection(t *testing.T) {
	f := newSubscriptionFixture(t)
	op := f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	f.secrets.afterPut = func() {
		_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.revoke-subscription-worker", nil, func(tx *store.Tx) (any, error) {
			r, err := tx.Get(domain.DeviceKind, f.device)
			if err != nil {
				return nil, err
			}
			d, err := store.Decode[domain.Device](r)
			if err != nil {
				return nil, err
			}
			d.Revoked = true
			_, err = tx.Put(domain.DeviceKind, r.ID, r.Revision, "", "", d)
			return nil, err
		})
		if err != nil {
			t.Error(err)
		}
	}
	raw := subscriptionTestBundle("fixture-account", "first", time.Now().Add(-time.Minute))
	defer clear(raw)
	if _, err := f.finish(lease, raw, true, false, true); err == nil {
		t.Fatal("revoked Worker committed a connection")
	}
	_, a := f.record()
	if a.Connection != nil || a.Subscription.Generation != "" || !a.Subscription.RecoveryRequired || a.Subscription.Lease == nil {
		t.Fatal("revocation released uncertain credential ownership")
	}
}

func TestSubscriptionUnchangedRefreshAndLostWriteBackBlockOldGeneration(t *testing.T) {
	for _, mode := range []string{"unchanged", "lost-upload", "uncertain-cleanup"} {
		t.Run(mode, func(t *testing.T) {
			f := newSubscriptionFixture(t)
			before := f.login()
			defer clear(before)
			op := f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
			lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
			if err != nil {
				t.Fatal(err)
			}
			clear(lease.Bundle)
			switch mode {
			case "unchanged":
				if _, err := f.finish(lease, before, true, true, true); err == nil {
					t.Fatal("successful RPC without changed native evidence was accepted")
				}
			case "lost-upload":
				if err := f.service.retainLostSubscriptionLeases(f.input.MachineID, f.instance, false); err != nil {
					t.Fatal(err)
				}
			case "uncertain-cleanup":
				if _, err := f.finish(lease, nil, false, false, false); err != nil {
					t.Fatal(err)
				}
			}
			_, a := f.record()
			if a.Subscription.Lease == nil {
				t.Fatal("unsafe generation was redistributed")
			}
			if mode != "unchanged" && (!a.Subscription.RecoveryRequired || a.Health != domain.AccountFailed) {
				t.Fatal("lost ownership was not reported as recovery required")
			}
			if _, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH); err == nil {
				t.Fatal("old bundle granted again")
			}
		})
	}
}

func TestSubscriptionProtectedLaneAuthorizationAndReceiptFencing(t *testing.T) {
	f := newSubscriptionFixture(t)
	op := f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	r, _ := f.record()
	request := &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: op.OperationId, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}

	response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, request)); domain.SafeError(rpc.ClientError(err)).Code != domain.RecoveryRequired {
		t.Fatal("receipt replay regained native delivery authority")
	}
	if _, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.RequestSubscriptionRequest{Mutation: request.Mutation, MachineId: request.MachineId, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN})); err == nil {
		t.Fatal("Worker invoked an owner operation")
	}
	foreign := subscriptionTestBundle("foreign-account", "first", time.Now().Add(-time.Minute))
	if _, err := f.finish(response.Msg, foreign, true, false, false); err != nil {
		t.Fatal(err)
	}
	_, a := f.record()
	if a.Connection != nil || !a.Subscription.RecoveryRequired {
		t.Fatal("unconfirmed cleanup published authentication")
	}
}

func TestSubscriptionExecutionRegistrationCannotUseAPIRelay(t *testing.T) {
	f := newSubscriptionFixture(t)
	raw := f.login()
	defer clear(raw)
	_, account := f.record()
	f.input.ConnectionID = account.Connection.ID
	f.input.Configuration.Subscription = true
	var err error
	f.input.ConfigurationDigest, err = f.input.Configuration.Digest()
	if err != nil {
		t.Fatal(err)
	}
	var jobRevision uint64
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.subscription-execution", nil, func(tx *store.Tx) (any, error) {
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
		jobRevision = updated.Revision
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	take := &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: jobRevision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: string(f.job), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE}
	response, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, take))
	if err != nil {
		t.Fatal(err)
	}
	clear(response.Msg.Bundle)
	f.register.Mutation.ExpectedRevision = jobRevision
	registration, err := f.authorityFixture.client.RegisterExecution(context.Background(), subscriptionRequest(f.workerToken, f.register))
	if err != nil {
		t.Fatal(err)
	}
	if registration.Msg.ProxyPath != "" {
		t.Fatal("native subscription advertised an API proxy")
	}
	if _, err := f.service.executionAuthority.Acquire(context.Background(), f.token); err == nil {
		t.Fatal("subscription publication token granted upstream relay authority")
	}
	// A current authenticated operation proceeds while earlier native cleanup
	// is unknown. An older completion cannot overwrite its newer revision.
	op := f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	lease, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH)
	if err != nil {
		t.Fatal("native ownership blocked refresh admission", err)
	}
	clear(lease.Bundle)
	if _, err := f.finish(response.Msg, raw, true, false, true); domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict {
		t.Fatal("superseded completion ignored the newer revision", err)
	}
	rotated := subscriptionTestBundle("fixture-account", "second", time.Now().UTC())
	defer clear(rotated)
	if _, err := f.finish(lease, rotated, true, true, true); err != nil {
		t.Fatal(err)
	}
	waiting := &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: take.Mutation.Id, ExpectedRevision: jobRevision}, MachineId: take.MachineId, InstanceId: take.InstanceId, OperationId: take.OperationId, Action: take.Action}
	resumed, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, waiting))
	if err != nil || resumed.Msg.GenerationId == response.Msg.GenerationId || !bytes.Equal(resumed.Msg.Bundle, rotated) {
		t.Fatal("selected execution did not receive current credentials", err)
	}
	clear(resumed.Msg.Bundle)

}

func TestSubscriptionTakeDoesNotReplaceActiveLeaseForSameOperation(t *testing.T) {
	f := newSubscriptionFixture(t)
	original := f.login()
	defer clear(original)
	lease := takeSubscriptionExecutionFixture(t, f)
	defer clear(lease.Bundle)

	var jobRevision uint64
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		job, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return err
		}
		jobRevision = job.Revision
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	request := &pb.TakeSubscriptionRequest{
		Mutation:    &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: jobRevision},
		MachineId:   string(f.input.MachineID),
		InstanceId:  string(f.instance),
		OperationId: string(f.job),
		Action:      pb.SubscriptionAction_SUBSCRIPTION_ACTION_EXECUTE,
	}
	if _, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, request)); domain.SafeError(rpc.ClientError(err)).Code != domain.ResourceExhausted {
		t.Fatal("a fresh lease replaced the active lease for the same operation", err)
	}
	_, account := f.record()
	if account.Subscription.Lease == nil || account.Subscription.Lease.ID != domain.ID(lease.LeaseId) {
		t.Fatal("the original lease lost its finish authority")
	}
	if _, err := f.finish(lease, original, true, false, true); err != nil {
		t.Fatal("the original lease could not finish", err)
	}
}

func TestSubscriptionOtherAccountsRemainIndependentAndIdentityCannotBeDuplicated(t *testing.T) {
	f := newSubscriptionFixture(t)
	op := f.start(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	first, err := f.take(op, pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	if err != nil {
		t.Fatal(err)
	}
	var other store.Record
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.independent-subscription", nil, func(tx *store.Tx) (any, error) {
		var err error
		other, err = tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", domain.Account{Alias: "Independent", SubscriptionService: domain.SubscriptionChatGPT, Type: domain.SubscriptionAccount, Enabled: true, Health: domain.AccountDisconnected})
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(other.ID), ExpectedRevision: other.Revision}, MachineId: string(f.input.MachineID), Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.client.TakeSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.TakeSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(other.ID), ExpectedRevision: started.Msg.Account.Revision}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OperationId: started.Msg.OperationId, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN}))
	if err != nil {
		t.Fatal("another account was blocked by the first lease", err)
	}
	raw := subscriptionTestBundle("same-provider-account", "first", time.Now().Add(-time.Minute))
	defer clear(raw)
	if _, err := f.finish(first, raw, true, false, true); err != nil {
		t.Fatal(err)
	}
	_, err = f.client.FinishSubscription(context.Background(), subscriptionRequest(f.workerToken, &pb.FinishSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(other.ID), ExpectedRevision: second.Msg.LeaseRevision}, LeaseId: second.Msg.LeaseId, GenerationId: second.Msg.GenerationId, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Bundle: bytes.Clone(raw), Succeeded: true, CleanupConfirmed: true}))
	if err != nil {
		t.Fatal("provider identity metadata blocked another selected account", err)
	}
}

func TestSubscriptionCapabilityNegotiatesWithExistingWorkerCapabilities(t *testing.T) {
	f := newSubscriptionFixture(t)
	client := delidevv1connect.NewWorkerServiceClient(http.DefaultClient, f.http.URL)
	capabilities := []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_AUTOMATIC_TITLES_CODEX_V1, pb.WorkerCapability_WORKER_CAPABILITY_SESSION_FORWARDING_V1, pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_CODEX_SUBSCRIPTIONS_V1}
	request := &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Version: rpc.Version, Capabilities: capabilities}
	response, err := client.AttachWorker(context.Background(), subscriptionRequest(f.workerToken, request))
	if err != nil {
		t.Fatal(err)
	}
	var machine domain.Machine
	if domain.Decode(response.Msg.Machine.DocumentJson, &machine) != nil || machine.Validate() != nil || len(machine.WorkerCapabilities) != len(capabilities) {
		t.Fatal("independent capabilities were not retained")
	}
	request.RequestId = string(domain.NewID())
	request.Capabilities = append(capabilities, pb.WorkerCapability_WORKER_CAPABILITY_MANAGED_CODEX_SUBSCRIPTIONS_V1)
	if _, err := client.AttachWorker(context.Background(), subscriptionRequest(f.workerToken, request)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("duplicate capability accepted: %v", err)
	}
}

func TestSubscriptionQueuedInitiatorRevocationSettlesOperations(t *testing.T) {
	for _, phase := range []domain.SubscriptionPhase{domain.SubscriptionQueued, domain.SubscriptionClaimed} {
		for _, action := range []pb.SubscriptionAction{
			pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN,
			pb.SubscriptionAction_SUBSCRIPTION_ACTION_REFRESH,
			pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGOUT,
		} {
			t.Run(string(phase)+"/"+action.String(), func(t *testing.T) {
				f := newSubscriptionFixture(t)
				if action != pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN {
					raw := f.login()
					defer clear(raw)
				}
				devices := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, f.http.URL)
				ctx := context.Background()
				code, token := randomCode(), randomCode()
				codeHash, tokenHash := sha256.Sum256([]byte(code)), sha256.Sum256([]byte(token))
				pairing, err := devices.CreatePairing(ctx, subscriptionRequest(f.service.Identity.Token, &pb.CreatePairingRequest{RequestId: string(domain.NewID()), Name: "subscription client", Type: pb.DeviceType_DEVICE_TYPE_CLIENT, CodeDigest: codeHash[:]}))
				if err != nil {
					t.Fatal(err)
				}
				paired, err := devices.PairDevice(ctx, connect.NewRequest(&pb.PairDeviceRequest{RequestId: string(domain.NewID()), PairingId: pairing.Msg.Pairing.Id, Code: code, DeviceId: string(domain.NewID()), CredentialDigest: tokenHash[:]}))
				if err != nil {
					t.Fatal(err)
				}
				r, _ := f.record()
				requested, err := f.client.RequestSubscription(ctx, subscriptionRequest(token, &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, MachineId: string(f.input.MachineID), Action: action}))
				if err != nil {
					t.Fatal(err)
				}
				var active *pb.TakeSubscriptionResponse
				if phase == domain.SubscriptionClaimed {
					active, err = f.take(requested.Msg, action)
					if err != nil {
						t.Fatal(err)
					}
					clear(active.Bundle)
				}
				_, before := f.record()
				revoke := &pb.RevokeDeviceRequest{Mutation: acctMutation(paired.Msg.Device, domain.NewID())}
				if _, err := devices.RevokeDevice(ctx, subscriptionRequest(f.service.Identity.Token, revoke)); err != nil {
					t.Fatal(err)
				}
				_, after := f.record()
				if phase == domain.SubscriptionClaimed {
					if after.Subscription.Pending == nil || after.Subscription.Pending.ID != before.Subscription.Pending.ID || after.Subscription.Lease == nil || string(after.Subscription.Lease.ID) != active.LeaseId || after.Subscription.Generation != before.Subscription.Generation || after.Health != before.Health {
						t.Fatal("initiator revocation released original claimed ownership")
					}
					if _, err := f.take(requested.Msg, action); err == nil {
						t.Fatal("claimed operation granted another lease after revocation")
					}
					return
				}
				if after.Subscription.Pending != nil || after.Subscription.Lease != nil || after.Subscription.RecoveryRequired {
					t.Fatal("revocation retained an unclaimed operation or fabricated native ownership")
				}
				if after.Subscription.Generation != before.Subscription.Generation || after.Health != before.Health || after.Subscription.IdentityCommitment != before.Subscription.IdentityCommitment {
					t.Fatal("queued cancellation changed credential ownership or accepted logout revocation")
				}
				if _, err := f.take(requested.Msg, action); err == nil {
					t.Fatal("revoked operation granted native authority")
				}
				replacement := f.start(action)
				if _, err := devices.RevokeDevice(ctx, subscriptionRequest(f.service.Identity.Token, revoke)); err != nil {
					t.Fatal(err)
				}
				_, current := f.record()
				if current.Subscription.Pending == nil || string(current.Subscription.Pending.ID) != replacement.OperationId {
					t.Fatal("revocation receipt replay canceled another initiator's replacement operation")
				}
				lease, err := f.take(replacement, action)
				if err != nil {
					t.Fatal("replacement authorized request could not acquire the account", err)
				}
				clear(lease.Bundle)
			})
		}
	}
}
