package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type accountTestSecrets struct {
	mu                    sync.Mutex
	values                map[credentials.Ref][]byte
	removed               map[credentials.Ref]bool
	putError, deleteError error
	afterPut              func()
	puts, deletes         int
}

func (v *accountTestSecrets) Put(ctx context.Context, ref credentials.Ref, key []byte) (string, error) {
	v.mu.Lock()
	v.puts++
	if v.removed[ref] {
		v.mu.Unlock()
		return "", domain.Fail(domain.Conflict, "Removed reference.", "")
	}
	if old, exists := v.values[ref]; exists && !bytes.Equal(old, key) {
		v.mu.Unlock()
		return "", domain.Fail(domain.Conflict, "Different retry secret.", "")
	}
	v.values[ref] = bytes.Clone(key)
	hook, err := v.afterPut, v.putError
	v.mu.Unlock()
	if hook != nil {
		hook()
	}
	return "opaque-binding", err
}
func (v *accountTestSecrets) Delete(ctx context.Context, ref credentials.Ref) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.deletes++
	if v.deleteError != nil {
		return v.deleteError
	}
	delete(v.values, ref)
	v.removed[ref] = true
	return nil
}
func (v *accountTestSecrets) Get(ctx context.Context, ref credentials.Ref) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if key, ok := v.values[ref]; ok {
		return bytes.Clone(key), nil
	}
	return nil, domain.Fail(domain.NotFound, "Test credential missing.", "")
}
func (v *accountTestSecrets) UnremovedReferences(ctx context.Context, owner domain.ID) ([]credentials.Ref, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	refs := []credentials.Ref{}
	for ref := range v.values {
		if ref.Owner == owner {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}
func (v *accountTestSecrets) counts() (int, int, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.puts, v.deletes, len(v.values)
}

type accountFixture struct {
	t         *testing.T
	root      string
	secrets   *accountTestSecrets
	endpoint  Endpoint
	identity  security.Identity
	accounts  delidevv1connect.AccountServiceClient
	config    delidevv1connect.ConfigurationServiceClient
	resources delidevv1connect.ResourceServiceClient
	stop      context.CancelFunc
	done      chan error
}

func newAccountFixture(t *testing.T) *accountFixture {
	f := &accountFixture{t: t, root: filepath.Join(t.TempDir(), "state"), secrets: &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}}
	f.start()
	t.Cleanup(f.shutdown)
	return f
}
func (f *accountFixture) start() {
	f.t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	f.stop = stop
	f.done = make(chan error, 1)
	ready := make(chan Endpoint, 1)
	go func() {
		f.done <- Serve(ctx, Config{DataDir: f.root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), accountSecrets: f.secrets}, func(e Endpoint) { ready <- e })
	}()
	select {
	case f.endpoint = <-ready:
	case err := <-f.done:
		f.t.Fatal(err)
	case <-time.After(10 * time.Second):
		f.t.Fatal("account server startup timed out")
	}
	var err error
	f.identity, err = security.LoadIdentity(f.root)
	if err != nil {
		f.t.Fatal(err)
	}
	f.accounts = delidevv1connect.NewAccountServiceClient(http.DefaultClient, f.endpoint.URL)
	f.config = delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, f.endpoint.URL)
	f.resources = delidevv1connect.NewResourceServiceClient(http.DefaultClient, f.endpoint.URL)
}
func (f *accountFixture) shutdown() {
	f.t.Helper()
	if f.stop != nil {
		f.stop()
		if err := <-f.done; err != nil {
			f.t.Error(err)
		}
		f.stop = nil
	}
}
func (f *accountFixture) save(kind pb.EntityKind, value any) *pb.Resource {
	f.t.Helper()
	raw, _ := json.Marshal(value)
	response, err := f.config.SaveConfiguration(context.Background(), ownerRequest(f.identity, &pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, Kind: kind, SchemaVersion: 1, DocumentJson: raw}))
	if err != nil {
		f.t.Fatal(err)
	}
	return response.Msg.Resource
}
func (f *accountFixture) newAccount(auth domain.Authentication) *pb.Resource {
	protocol, endpoint, kind := domain.OpenAIChat, "https://api.example.test/v1", domain.APIAccount
	if auth == domain.KeylessAuth {
		endpoint = "http://127.0.0.1:11434/v1"
	}
	if auth == domain.SubscriptionAuth {
		protocol = domain.NativeSubscription
		endpoint = ""
		kind = domain.SubscriptionAccount
	}
	provider := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "provider", Protocol: protocol, Endpoint: endpoint, Authentication: auth})
	return f.save(pb.EntityKind_ENTITY_KIND_ACCOUNT, domain.Account{Alias: "account", ProviderID: domain.ID(provider.Id), Type: kind, Enabled: true, Health: domain.AccountDisconnected})
}
func accountBody(t *testing.T, record *pb.Resource) domain.Account {
	t.Helper()
	var account domain.Account
	if err := domain.Decode(record.DocumentJson, &account); err != nil {
		t.Fatal(err)
	}
	return account
}
func acctMutation(record *pb.Resource, request domain.ID) *pb.Mutation {
	return &pb.Mutation{Id: record.Id, ExpectedRevision: record.Revision, RequestId: string(request)}
}
func connectAccount(f *accountFixture, record *pb.Resource, request domain.ID, key string, keyless bool) (*connect.Response[pb.ConnectAccountResponse], error) {
	return f.accounts.ConnectAccount(context.Background(), ownerRequest(f.identity, &pb.ConnectAccountRequest{Mutation: acctMutation(record, request), ApiKey: []byte(key), Keyless: keyless}))
}
func wantAccountCode(t *testing.T, err error, code domain.Code) {
	t.Helper()
	if err == nil || rpc.ClientError(err).Code != code {
		t.Fatalf("got %v; want %s", err, code)
	}
}

func TestAccountConnectRestartDisconnectAndOldReceipt(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	initial := f.newAccount(domain.BearerAuth)
	request := domain.NewID()
	key := "private-api-connection-test-secret-unique"
	connected, err := connectAccount(f, initial, request, key, false)
	if err != nil {
		t.Fatal(err)
	}
	a := accountBody(t, connected.Msg.Account)
	if a.Health != domain.AccountUnverified || a.Connection == nil || a.Connection.ID != request || connected.Msg.Account.Revision != 2 {
		t.Fatal("connection not recorded as unverified")
	}
	replay, err := connectAccount(f, initial, request, key, false)
	if err != nil || !replay.Msg.Replayed {
		t.Fatalf("retry: %v", err)
	}
	_, err = connectAccount(f, initial, request, key+"changed", false)
	wantAccountCode(t, err, domain.Conflict)
	puts, _, active := f.secrets.counts()
	if puts != 1 || active != 1 {
		t.Fatal("receipt replay wrote native credentials")
	}
	f.shutdown()
	f.start()
	replay, err = connectAccount(f, initial, request, key, false)
	if err != nil || !replay.Msg.Replayed {
		t.Fatalf("restart replay: %v", err)
	}
	disconnectID := domain.NewID()
	disconnect := &pb.DisconnectAccountRequest{Mutation: acctMutation(connected.Msg.Account, disconnectID)}
	removed, err := f.accounts.DisconnectAccount(ctx, ownerRequest(f.identity, disconnect))
	if err != nil || len(removed.Msg.CleanupProblemJson) != 0 {
		t.Fatalf("disconnect: %v", err)
	}
	a = accountBody(t, removed.Msg.Account)
	if a.Health != domain.AccountDisconnected || a.Connection != nil || a.Removal != nil || removed.Msg.Account.Revision != 4 {
		t.Fatal("disconnect did not finish both durable transitions")
	}
	replay, err = connectAccount(f, initial, request, key, false)
	if err != nil || !replay.Msg.Replayed || accountBody(t, replay.Msg.Account).Connection != nil {
		t.Fatalf("old connect resurrected secret: %v", err)
	}
	puts, _, active = f.secrets.counts()
	if puts != 1 || active != 0 {
		t.Fatal("old receipt reached native storage")
	}
	fresh, err := connectAccount(f, removed.Msg.Account, domain.NewID(), key+"new", false)
	if err != nil {
		t.Fatal(err)
	}
	oldDisconnect, err := f.accounts.DisconnectAccount(ctx, ownerRequest(f.identity, disconnect))
	if err != nil || !oldDisconnect.Msg.Replayed || accountBody(t, oldDisconnect.Msg.Account).Connection.ID != accountBody(t, fresh.Msg.Account).Connection.ID {
		t.Fatalf("old disconnect changed new connection: %v", err)
	}
	_, _, active = f.secrets.counts()
	if active != 1 {
		t.Fatal("old disconnect deleted new credential")
	}
	f.shutdown()
	err = filepath.Walk(f.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if bytes.Contains(raw, []byte(key)) || bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString([]byte(key)))) {
			t.Errorf("credential persisted outside protected store: %s", filepath.Base(path))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAccountCleanupFailureRemainsDisconnectedAcrossRestart(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	initial := f.newAccount(domain.BearerAuth)
	connected, err := connectAccount(f, initial, domain.NewID(), "private-cleanup-secret", false)
	if err != nil {
		t.Fatal(err)
	}
	f.secrets.mu.Lock()
	f.secrets.deleteError = domain.Fail(domain.ConfirmationRequired, "The native store is locked.", "Unlock it and retry.")
	f.secrets.mu.Unlock()
	requestID := domain.NewID()
	input := &pb.DisconnectAccountRequest{Mutation: acctMutation(connected.Msg.Account, requestID)}
	accepted, err := f.accounts.DisconnectAccount(ctx, ownerRequest(f.identity, input))
	if err != nil {
		t.Fatal(err)
	}
	account := accountBody(t, accepted.Msg.Account)
	if account.Health != domain.AccountDisconnected || account.Connection != nil || account.Removal == nil || account.Removal.RequestID != requestID || account.Removal.ExpectedRevision != connected.Msg.Account.Revision {
		t.Fatal("cleanup failure lost authoritative disconnect")
	}
	if bytes.Contains(accepted.Msg.Account.DocumentJson, []byte("completion_id")) {
		t.Fatal("private cleanup receipt identity escaped")
	}
	var problem domain.Error
	if err = domain.Decode(accepted.Msg.CleanupProblemJson, &problem); err != nil || problem.Code != domain.ConfirmationRequired {
		t.Fatalf("cleanup classification: %v", err)
	}
	_, err = connectAccount(f, accepted.Msg.Account, domain.NewID(), "new-key", false)
	wantAccountCode(t, err, domain.Conflict)
	_, err = f.config.DeleteConfiguration(ctx, ownerRequest(f.identity, &pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: acctMutation(accepted.Msg.Account, domain.NewID())}))
	wantAccountCode(t, err, domain.Conflict)
	// Metadata edits while cleanup is blocked must survive completion of the
	// original request, whose expected revision predates both writes.
	account.Alias = "edited while cleanup waits"
	editedJSON, _ := json.Marshal(account)
	_, err = f.config.SaveConfiguration(ctx, ownerRequest(f.identity, &pb.SaveConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, SchemaVersion: 1, DocumentJson: editedJSON, Mutation: acctMutation(accepted.Msg.Account, domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	f.shutdown()
	f.secrets.mu.Lock()
	f.secrets.deleteError = nil
	f.secrets.mu.Unlock()
	f.start()
	completed, err := f.accounts.DisconnectAccount(ctx, ownerRequest(f.identity, input))
	if err != nil || !completed.Msg.Replayed || len(completed.Msg.CleanupProblemJson) > 0 || accountBody(t, completed.Msg.Account).Removal != nil {
		t.Fatalf("cleanup retry: %v", err)
	}
	if accountBody(t, completed.Msg.Account).Alias != "edited while cleanup waits" {
		t.Fatal("cleanup overwrote a newer metadata edit")
	}
	deletion := &pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: acctMutation(completed.Msg.Account, domain.NewID())}
	if _, err = f.config.DeleteConfiguration(ctx, ownerRequest(f.identity, deletion)); err != nil {
		t.Fatal(err)
	}
	again, err := f.config.DeleteConfiguration(ctx, ownerRequest(f.identity, deletion))
	if err != nil || !again.Msg.Replayed {
		t.Fatalf("deletion replay: %v", err)
	}
	_, err = f.accounts.DisconnectAccount(ctx, ownerRequest(f.identity, input))
	wantAccountCode(t, err, domain.NotFound)
}

func TestInterruptedConnectIntentRequiresExplicitCleanup(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	account := f.newAccount(domain.BearerAuth)
	request := domain.NewID()
	f.secrets.mu.Lock()
	f.secrets.putError = domain.Fail(domain.Unavailable, "Native write acknowledgment was lost.", "Retry.")
	f.secrets.mu.Unlock()
	_, err := connectAccount(f, account, request, "staged-secret", false)
	wantAccountCode(t, err, domain.Unavailable)
	status, err := f.accounts.GetAccountStatus(ctx, ownerRequest(f.identity, &pb.GetAccountStatusRequest{Id: account.Id}))
	if err != nil || status.Msg.Account.Revision != 1 || accountBody(t, status.Msg.Account).Connection != nil {
		t.Fatalf("failed connect changed metadata: %v", err)
	}
	_, err = f.config.DeleteConfiguration(ctx, ownerRequest(f.identity, &pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: acctMutation(account, domain.NewID())}))
	wantAccountCode(t, err, domain.Conflict)
	f.secrets.mu.Lock()
	f.secrets.putError = nil
	f.secrets.mu.Unlock()
	removed, err := f.accounts.DisconnectAccount(ctx, ownerRequest(f.identity, &pb.DisconnectAccountRequest{Mutation: acctMutation(account, domain.NewID())}))
	if err != nil || len(removed.Msg.CleanupProblemJson) != 0 {
		t.Fatalf("orphan cleanup: %v", err)
	}
	_, _, active := f.secrets.counts()
	if active != 0 {
		t.Fatal("staged secret orphaned")
	}
	_, err = connectAccount(f, account, request, "staged-secret", false)
	wantAccountCode(t, err, domain.Conflict)
}

func TestAccountInputConfigurationAndWorkerGuards(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	keyless := f.newAccount(domain.KeylessAuth)
	bearer := f.newAccount(domain.BearerAuth)
	subscription := f.newAccount(domain.SubscriptionAuth)
	_, err := connectAccount(f, keyless, domain.NewID(), "wrong-input", false)
	wantAccountCode(t, err, domain.InvalidArgument)
	_, err = connectAccount(f, bearer, domain.NewID(), "", true)
	wantAccountCode(t, err, domain.InvalidArgument)
	_, err = connectAccount(f, bearer, domain.NewID(), "key\nheader", false)
	wantAccountCode(t, err, domain.InvalidArgument)
	_, err = connectAccount(f, subscription, domain.NewID(), "subscription-secret", false)
	wantAccountCode(t, err, domain.Unsupported)
	connected, err := connectAccount(f, keyless, domain.NewID(), "", true)
	if err != nil {
		t.Fatal(err)
	}
	puts, _, _ := f.secrets.counts()
	if puts != 0 {
		t.Fatal("keyless or rejected request touched native secrets")
	}
	forged := accountBody(t, connected.Msg.Account)
	forged.Health = domain.AccountReady
	raw, _ := json.Marshal(forged)
	_, err = f.config.SaveConfiguration(ctx, ownerRequest(f.identity, &pb.SaveConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, SchemaVersion: 1, DocumentJson: raw, Mutation: acctMutation(connected.Msg.Account, domain.NewID())}))
	wantAccountCode(t, err, domain.InvalidArgument)
	worker, _ := pairedWorker(t, ctx, f.endpoint, f.identity)
	_, err = f.accounts.ConnectAccount(ctx, ownerRequest(worker, &pb.ConnectAccountRequest{Mutation: acctMutation(bearer, domain.NewID()), ApiKey: []byte("worker-secret")}))
	wantAccountCode(t, err, domain.PermissionDenied)
	_, err = f.accounts.GetAccountStatus(ctx, ownerRequest(worker, &pb.GetAccountStatusRequest{Id: bearer.Id}))
	wantAccountCode(t, err, domain.PermissionDenied)
}

func TestAccountConcurrentConnectionsUseOneProtectedReference(t *testing.T) {
	f := newAccountFixture(t)
	initial := f.newAccount(domain.BearerAuth)
	var wg sync.WaitGroup
	out := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := connectAccount(f, initial, domain.NewID(), "concurrent-key", false)
			out <- err
		}()
	}
	wg.Wait()
	close(out)
	success := 0
	for err := range out {
		if err == nil {
			success++
		} else {
			wantAccountCode(t, err, domain.Conflict)
		}
	}
	puts, _, active := f.secrets.counts()
	if success != 1 || puts != 1 || active != 1 {
		t.Fatalf("success=%d native_puts=%d active=%d", success, puts, active)
	}
}

func TestClientRevocationDuringNativeWriteCannotConnect(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	account := f.newAccount(domain.BearerAuth)
	devices := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, f.endpoint.URL)
	code, token := randomCode(), randomCode()
	codeHash, tokenHash := sha256.Sum256([]byte(code)), sha256.Sum256([]byte(token))
	pairing, err := devices.CreatePairing(ctx, ownerRequest(f.identity, &pb.CreatePairingRequest{RequestId: string(domain.NewID()), Name: "client", Type: pb.DeviceType_DEVICE_TYPE_CLIENT, CodeDigest: codeHash[:]}))
	if err != nil {
		t.Fatal(err)
	}
	paired, err := devices.PairDevice(ctx, connect.NewRequest(&pb.PairDeviceRequest{RequestId: string(domain.NewID()), PairingId: pairing.Msg.Pairing.Id, Code: code, DeviceId: string(domain.NewID()), CredentialDigest: tokenHash[:]}))
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	releaseNative := sync.OnceFunc(func() { close(release) })
	defer releaseNative()
	f.secrets.mu.Lock()
	f.secrets.afterPut = func() { close(entered); <-release }
	f.secrets.mu.Unlock()
	finished := make(chan error, 1)
	requestID := domain.NewID()
	go func() {
		_, err := f.accounts.ConnectAccount(ctx, ownerRequest(security.Identity{Token: token}, &pb.ConnectAccountRequest{Mutation: acctMutation(account, requestID), ApiKey: []byte("revoked-in-flight-secret")}))
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("native staging did not start")
	}
	_, err = devices.RevokeDevice(ctx, ownerRequest(f.identity, &pb.RevokeDeviceRequest{Mutation: acctMutation(paired.Msg.Device, domain.NewID())}))
	if err != nil {
		releaseNative()
		t.Fatal(err)
	}
	releaseNative()
	if err = <-finished; err == nil {
		t.Fatal("revoked principal completed connection")
	}
	status, err := f.accounts.GetAccountStatus(ctx, ownerRequest(f.identity, &pb.GetAccountStatusRequest{Id: account.Id}))
	if err != nil || status.Msg.Account.Revision != 1 || accountBody(t, status.Msg.Account).Connection != nil {
		t.Fatalf("revoked write committed: %v", err)
	}
	f.secrets.mu.Lock()
	f.secrets.afterPut = nil
	f.secrets.mu.Unlock()
	removed, err := f.accounts.DisconnectAccount(ctx, ownerRequest(f.identity, &pb.DisconnectAccountRequest{Mutation: acctMutation(account, domain.NewID())}))
	if err != nil || len(removed.Msg.CleanupProblemJson) > 0 {
		t.Fatalf("revoked staging cleanup: %v", err)
	}
	_, _, active := f.secrets.counts()
	if active != 0 {
		t.Fatal("revoked staging leaked an active credential")
	}
}
