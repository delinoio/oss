package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type testPATs struct {
	mu                  sync.Mutex
	values              map[credentials.PATRef][]byte
	puts, deletes       int
	failPut, failDelete bool
}

func (v *testPATs) Put(_ context.Context, ref credentials.PATRef, token []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.puts++
	if old := v.values[ref]; old != nil && !bytes.Equal(old, token) {
		return domain.Fail(domain.Conflict, "Different generation.", "")
	}
	v.values[ref] = bytes.Clone(token)
	if v.failPut {
		return domain.Fail(domain.Unavailable, "Lost native acknowledgment.", "Retry the original request.")
	}
	return nil
}
func (v *testPATs) Get(_ context.Context, ref credentials.PATRef) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.values[ref] == nil {
		return nil, domain.Fail(domain.NotFound, "Missing native token.", "")
	}
	return bytes.Clone(v.values[ref]), nil
}
func (v *testPATs) Delete(_ context.Context, ref credentials.PATRef) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.deletes++
	if v.failDelete {
		return domain.Fail(domain.Unavailable, "Native store locked.", "Retry deletion.")
	}
	clear(v.values[ref])
	delete(v.values, ref)
	return nil
}
func (v *testPATs) UnremovedReferences(_ context.Context, id domain.ID) ([]credentials.PATRef, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	refs := []credentials.PATRef{}
	for ref := range v.values {
		if ref.ProfileID == id {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}
func (v *testPATs) counts() (int, int, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.puts, v.deletes, len(v.values)
}

type identityFunc func(context.Context, []byte) (gh.IdentityObservation, error)

func (f identityFunc) Identity(ctx context.Context, token []byte) (gh.IdentityObservation, error) {
	return f(ctx, token)
}

type integrationFixture struct {
	httpServer *httptest.Server
	url        string
	t          *testing.T
	service    *Service
	native     *testPATs
	client     delidevv1connect.IntegrationServiceClient
	calls      atomic.Int32
	root       string
}

func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	db, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := security.CreateIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	native := &testPATs{values: map[credentials.PATRef][]byte{}}
	f := &integrationFixture{t: t, root: root, native: native}
	f.service = &Service{Store: db, Identity: identity, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), integrationSecrets: native}
	f.service.github = identityFunc(func(_ context.Context, token []byte) (gh.IdentityObservation, error) {
		f.calls.Add(1)
		return gh.IdentityObservation{State: domain.IntegrationIdentityVerified, Identity: &domain.GitHubIdentity{ID: "17", NodeID: "U_17", Login: "fixture-user"}}, nil
	})
	f.startHTTP()
	t.Cleanup(func() {
		f.httpServer.Close()
		f.service.executionAuthority.close()
		_ = f.service.closeIntegrationSecrets()
		_ = f.service.Store.Close()
	})
	return f
}
func (f *integrationFixture) startHTTP() {
	f.httpServer = httptest.NewServer(f.service.Handler(nil, true))
	f.url = f.httpServer.URL
	f.client = delidevv1connect.NewIntegrationServiceClient(http.DefaultClient, f.url)
}
func (f *integrationFixture) restart() {
	f.t.Helper()
	f.httpServer.Close()
	f.service.executionAuthority.close()
	if err := f.service.closeIntegrationSecrets(); err != nil {
		f.t.Fatal(err)
	}
	if err := f.service.Store.Close(); err != nil {
		f.t.Fatal(err)
	}
	db, err := store.Open(context.Background(), f.root)
	if err != nil {
		f.t.Fatal(err)
	}
	identity, err := security.LoadIdentity(f.root)
	if err != nil {
		f.t.Fatal(err)
	}
	f.service = &Service{Store: db, Identity: identity, logger: f.service.logger, integrationSecrets: f.native, github: f.service.github}
	f.startHTTP()
}
func (f *integrationFixture) save(name string) *pb.Resource {
	f.t.Helper()
	raw, _ := json.Marshal(domain.IntegrationDefinition{Name: name, Provider: domain.GitHubCom, TokenKind: domain.FineGrainedPAT, ResourceOwner: "fixture-owner"})
	reply, err := f.client.SaveIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.SaveIntegrationProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, SchemaVersion: 1, DocumentJson: raw}))
	if err != nil {
		f.t.Fatal(err)
	}
	return reply.Msg.Profile
}
func profileMutation(r *pb.Resource) *pb.Mutation {
	return &pb.Mutation{Id: r.Id, ExpectedRevision: r.Revision, RequestId: string(domain.NewID())}
}
func (f *integrationFixture) replace(m *pb.Mutation, token string) *pb.ReplaceIntegrationTokenResponse {
	f.t.Helper()
	reply, err := f.client.ReplaceIntegrationToken(context.Background(), ownerRequest(f.service.Identity, &pb.ReplaceIntegrationTokenRequest{Mutation: m, Token: []byte(token)}))
	if err != nil {
		f.t.Fatal(err)
	}
	return reply.Msg
}
func integrationValue(t *testing.T, r *pb.Resource) domain.Integration {
	t.Helper()
	var value domain.Integration
	if err := domain.Decode(r.DocumentJson, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestIntegrationProfilesReplayReplacementAndDelete(t *testing.T) {
	f := newIntegrationFixture(t)
	first, independent := f.save("Personal"), f.save("Organization")
	m := profileMutation(first)
	token := "fixture-PAT-do-not-persist-A"
	connected := f.replace(m, token)
	value := integrationValue(t, connected.Profile)
	if connected.Replayed || len(connected.ProblemJson) != 0 || value.Connection == nil || value.Connection.Validation.Identity.ID != "17" {
		t.Fatalf("unexpected connection: %+v", connected)
	}
	other := f.replace(profileMutation(independent), "fixture-PAT-do-not-persist-B")
	secondMutation := profileMutation(connected.Profile)
	replaced := f.replace(secondMutation, "fixture-PAT-do-not-persist-C")
	beforePut, beforeDelete, beforeCount := f.native.counts()
	replay := f.replace(m, token)
	afterPut, afterDelete, afterCount := f.native.counts()
	if !replay.Replayed || replay.Profile.Revision != replaced.Profile.Revision || beforePut != afterPut || beforeDelete != afterDelete || beforeCount != 2 || afterCount != 2 || f.calls.Load() != 3 {
		t.Fatal("old replacement repeated a side effect")
	}
	changed := ownerRequest(f.service.Identity, &pb.ReplaceIntegrationTokenRequest{Mutation: m, Token: []byte("different")})
	if _, err := f.client.ReplaceIntegrationToken(context.Background(), changed); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("changed replay accepted: %v", err)
	}
	deletion := profileMutation(replaced.Profile)
	deleted, err := f.client.DeleteIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteIntegrationProfileRequest{Mutation: deletion}))
	if err != nil || !deleted.Msg.Deleted {
		t.Fatalf("delete: %v %+v", err, deleted)
	}
	deleted, err = f.client.DeleteIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteIntegrationProfileRequest{Mutation: deletion}))
	if err != nil || !deleted.Msg.Deleted || !deleted.Msg.Replayed {
		t.Fatalf("delete retry: %v", err)
	}
	if _, err := f.client.ReplaceIntegrationToken(context.Background(), ownerRequest(f.service.Identity, &pb.ReplaceIntegrationTokenRequest{Mutation: m, Token: []byte(token)})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("deleted token resurrected: %v", err)
	}
	_, _, count := f.native.counts()
	if count != 1 {
		t.Fatal("unrelated profile removed")
	}
	if _, _, err := f.service.integrationRecord(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), domain.ID(other.Profile.Id)); err != nil {
		t.Fatal(err)
	}
	// Scan complete SQLite/WAL and metadata bytes, not just JSON responses.
	err = filepath.WalkDir(f.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, marker := range []string{token, "fixture-PAT-do-not-persist-B", "fixture-PAT-do-not-persist-C"} {
				if bytes.Contains(raw, []byte(marker)) {
					t.Fatal("token persisted outside native store")
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestIntegrationPendingLostNativeAcknowledgmentAndCleanup(t *testing.T) {
	f := newIntegrationFixture(t)
	r := f.save("Retry")
	f.native.failPut = true
	m := profileMutation(r)
	pending := f.replace(m, "retry-token")
	if v := integrationValue(t, pending.Profile); v.Connection != nil || v.Pending == nil || len(pending.ProblemJson) == 0 {
		t.Fatal("uncertain write was published")
	}
	f.restart()
	f.native.failPut = false
	ready := f.replace(m, "retry-token")
	if !ready.Replayed || integrationValue(t, ready.Profile).Pending != nil || f.calls.Load() != 1 {
		t.Fatal("original retry did not reconcile")
	}
	f.native.failDelete = true
	dm := profileMutation(ready.Profile)
	deleted, err := f.client.DeleteIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteIntegrationProfileRequest{Mutation: dm}))
	if err != nil || deleted.Msg.Deleted || len(deleted.Msg.ProblemJson) == 0 || integrationValue(t, deleted.Msg.Profile).Connection != nil {
		t.Fatalf("cleanup pending: %v", err)
	}
	_, err = f.client.ValidateIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.ValidateIntegrationProfileRequest{Mutation: profileMutation(deleted.Msg.Profile)}))
	if connect.CodeOf(err) != connect.CodeAborted || f.calls.Load() != 1 {
		t.Fatalf("denied token used: %v", err)
	}
	f.restart()
	f.native.failDelete = false
	deleted, err = f.client.DeleteIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteIntegrationProfileRequest{Mutation: dm}))
	if err != nil || !deleted.Msg.Deleted || !deleted.Msg.Replayed {
		t.Fatal("deletion did not reconcile", err)
	}
}
func TestIntegrationDeleteCancelsValidationWithoutBlockingOtherProfiles(t *testing.T) {
	f := newIntegrationFixture(t)
	connected := f.replace(profileMutation(f.save("Cancel")), "cancel-token")
	started := make(chan struct{})
	finished := make(chan struct{})
	f.service.github = identityFunc(func(ctx context.Context, _ []byte) (gh.IdentityObservation, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return gh.IdentityObservation{}, ctx.Err()
	})
	done := make(chan error, 1)
	go func() {
		_, err := f.client.ValidateIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.ValidateIntegrationProfileRequest{Mutation: profileMutation(connected.Profile)}))
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("validation did not start")
	}
	f.save("Independent metadata")
	deleted, err := f.client.DeleteIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteIntegrationProfileRequest{Mutation: profileMutation(connected.Profile)}))
	if err != nil || !deleted.Msg.Deleted {
		t.Fatal("blocked delete", err)
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("lookup survived deletion")
	}
	if err := <-done; err == nil {
		t.Fatal("canceled observation published")
	}
}

func TestIntegrationActorBoundReceiptsWorkerDenialAndRevocation(t *testing.T) {
	f := newIntegrationFixture(t)
	ctx := context.Background()
	devices := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, f.url)
	pair := func(kind pb.DeviceType) (security.Identity, *pb.Resource) {
		code, token := randomCode(), randomCode()
		codeHash, tokenHash := sha256.Sum256([]byte(code)), sha256.Sum256([]byte(token))
		grant, err := devices.CreatePairing(ctx, ownerRequest(f.service.Identity, &pb.CreatePairingRequest{RequestId: string(domain.NewID()), Name: "Integration test device", Type: kind, CodeDigest: codeHash[:]}))
		if err != nil {
			t.Fatal(err)
		}
		pairRequest := &pb.PairDeviceRequest{RequestId: string(domain.NewID()), PairingId: grant.Msg.Pairing.Id, Code: code, DeviceId: string(domain.NewID()), CredentialDigest: tokenHash[:]}
		if kind == pb.DeviceType_DEVICE_TYPE_WORKER {
			pairRequest.MachineId = string(domain.NewID())
			pairRequest.MachineJson, _ = json.Marshal(domain.Machine{Name: "Fixture Worker", OS: "linux", Architecture: "amd64", Version: rpc.Version})
		}
		paired, err := devices.PairDevice(ctx, connect.NewRequest(pairRequest))
		if err != nil {
			t.Fatal(err)
		}
		return security.Identity{Token: token}, paired.Msg.Device
	}
	worker, _ := pair(pb.DeviceType_DEVICE_TYPE_WORKER)
	actor, device := pair(pb.DeviceType_DEVICE_TYPE_CLIENT)
	raw := []byte(`{"name":"Actor profile","provider":"github.com","token_kind":"classic"}`)
	save := &pb.SaveIntegrationProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, SchemaVersion: 1, DocumentJson: raw}
	if _, err := f.client.SaveIntegrationProfile(ctx, connect.NewRequest(save)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("anonymous accepted", err)
	}
	if _, err := f.client.SaveIntegrationProfile(ctx, ownerRequest(worker, save)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker accepted", err)
	}
	saved, err := f.client.SaveIntegrationProfile(ctx, ownerRequest(actor, save))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.SaveIntegrationProfile(ctx, ownerRequest(f.service.Identity, save)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("receipt crossed actors", err)
	}
	connected := f.replace(profileMutation(saved.Msg.Profile), "revocation-pat")
	started, finished := make(chan struct{}), make(chan struct{})
	f.service.github = identityFunc(func(ctx context.Context, _ []byte) (gh.IdentityObservation, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return gh.IdentityObservation{}, ctx.Err()
	})
	done := make(chan error, 1)
	go func() {
		_, err := f.client.ValidateIntegrationProfile(ctx, ownerRequest(actor, &pb.ValidateIntegrationProfileRequest{Mutation: profileMutation(connected.Profile)}))
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("validation not started")
	}
	if _, err := devices.RevokeDevice(ctx, ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: profileMutation(device)})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("revoked validation survived")
	}
	if err := <-done; err == nil {
		t.Fatal("revoked observation published")
	}
	record, value, err := f.service.integrationRecord(domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice}), domain.ID(connected.Profile.Id))
	if err != nil || record.Revision != connected.Profile.Revision || value.Connection.Validation.Identity.ID != "17" {
		t.Fatal("revocation overwrote prior observation", err)
	}
}
func TestIntegrationAbandonedReplacementDeletesStagedGeneration(t *testing.T) {
	f := newIntegrationFixture(t)
	r := f.save("Abandon")
	f.native.failPut = true
	pending := f.replace(profileMutation(r), "lost-token")
	reply, err := f.client.DeleteIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.DeleteIntegrationProfileRequest{Mutation: profileMutation(pending.Profile)}))
	if err != nil || !reply.Msg.Deleted {
		t.Fatal("abandon failed", err)
	}
	_, _, count := f.native.counts()
	if count != 0 || f.calls.Load() != 0 {
		t.Fatal("staged credential survived or was used")
	}
}
func TestIntegrationDefinitionsCannotForgeConnectionOrIdentity(t *testing.T) {
	f := newIntegrationFixture(t)
	for _, extra := range []string{`"token":"secret"`, `"connection":null`, `"pending":null`, `"identity":{"id":"17"}`} {
		raw := []byte(`{"name":"Forged","provider":"github.com","token_kind":"classic",` + extra + `}`)
		_, err := f.client.SaveIntegrationProfile(context.Background(), ownerRequest(f.service.Identity, &pb.SaveIntegrationProfileRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, SchemaVersion: 1, DocumentJson: raw}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("forged fields accepted: %v", err)
		}
	}
}
