// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"crypto/sha256"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

type browserFixture struct {
	s                                *Service
	root                             string
	first, second                    context.Context
	account, other, session, another domain.ID
}

func newBrowserFixture(t *testing.T) *browserFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	db, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: db, Identity: security.Identity{ServerID: domain.NewID(), Token: "temporary-browser-fixture"}, logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	f := &browserFixture{s: s, root: root, account: domain.NewID(), other: domain.NewID(), session: domain.NewID(), another: domain.NewID()}
	t.Cleanup(func() { f.s.Store.Close() })
	for _, target := range []*context.Context{&f.first, &f.second} {
		id := domain.NewID()
		doctorPut(t, s, domain.DeviceKind, id, 0, domain.Device{Type: domain.ClientDevice})
		*target = domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: id})
	}
	for _, account := range []domain.ID{f.account, f.other} {
		doctorPut(t, s, domain.AccountKind, account, 0, domain.Account{Alias: "browser fixture", Health: domain.AccountDisconnected})
	}
	for _, id := range []domain.ID{f.session, f.another} {
		doctorPut(t, s, domain.SessionKind, id, 0, domain.Session{CurrentExecution: &domain.ExecutionSelection{ID: domain.NewID(), AccountID: f.account}})
	}
	return f
}
func (f *browserFixture) register(t *testing.T, ctx context.Context, session domain.ID, request domain.ID) *pb.RegisterBrowserProfileResponse {
	t.Helper()
	r, err := f.s.RegisterBrowserProfile(ctx, connect.NewRequest(&pb.RegisterBrowserProfileRequest{Session: &pb.Mutation{Id: string(session), ExpectedRevision: 1, RequestId: string(request)}, AccountId: string(f.account)}))
	if err != nil {
		t.Fatal(err)
	}
	return r.Msg
}
func profileBody(t *testing.T, s *Service, ctx context.Context, id string) domain.BrowserProfile {
	t.Helper()
	r, err := s.browserProfile(ctx, domain.ID(id))
	if err != nil {
		t.Fatal(err)
	}
	return r.Data
}

func TestBrowserProfilesShareOnlyOriginalDeviceAccountAndServerAcrossRestart(t *testing.T) {
	f := newBrowserFixture(t)
	request := domain.NewID()
	first := f.register(t, f.first, f.session, request)
	shared := f.register(t, f.first, f.another, domain.NewID())
	otherDevice := f.register(t, f.second, f.session, domain.NewID())
	if first.Profile.Id != shared.Profile.Id || first.Profile.Id == otherDevice.Profile.Id {
		t.Fatal("profile ownership was not isolated")
	}
	p := profileBody(t, f.s, f.first, first.Profile.Id)
	if p.ServerID != f.s.Identity.ServerID || p.AccountID != f.account {
		t.Fatal(p)
	}
	if _, err := f.s.GetBrowserProfile(f.second, connect.NewRequest(&pb.GetBrowserProfileRequest{Id: first.Profile.Id})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("foreign profile read", err)
	}

	if _, err := f.s.RegisterBrowserProfile(f.first, connect.NewRequest(&pb.RegisterBrowserProfileRequest{Session: &pb.Mutation{Id: string(f.session), ExpectedRevision: 1, RequestId: string(domain.NewID())}, AccountId: string(f.other)})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("foreign account", err)
	}
	session := domain.NewID()
	doctorPut(t, f.s, domain.SessionKind, session, 0, domain.Session{CurrentExecution: &domain.ExecutionSelection{AccountID: f.other}})
	different, err := f.s.RegisterBrowserProfile(f.first, connect.NewRequest(&pb.RegisterBrowserProfileRequest{Session: &pb.Mutation{Id: string(session), ExpectedRevision: 1, RequestId: string(domain.NewID())}, AccountId: string(f.other)}))
	if err != nil || different.Msg.Profile.Id == first.Profile.Id {
		t.Fatal(different, err)
	}
	page, err := f.s.ListBrowserProfiles(f.first, connect.NewRequest(&pb.ListBrowserProfilesRequest{PageSize: 1}))
	if err != nil || len(page.Msg.Profiles) != 1 || page.Msg.NextPageToken == "" {
		t.Fatal(page, err)
	}
	if _, err = f.s.ListBrowserProfiles(f.second, connect.NewRequest(&pb.ListBrowserProfilesRequest{PageToken: page.Msg.NextPageToken})); connect.CodeOf(err) != connect.CodeOutOfRange {
		t.Fatal("foreign cursor", err)
	}
	if err = f.s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	f.s.Store, err = store.Open(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	replay := f.register(t, f.first, f.session, request)
	if !replay.Replayed || replay.Profile.Id != first.Profile.Id {
		t.Fatal(replay)
	}
	otherServer := newBrowserFixture(t)
	otherServer.account = f.account
	doctorPut(t, otherServer.s, domain.AccountKind, f.account, 0, domain.Account{Alias: "same UUID, different server"})
	doctorPut(t, otherServer.s, domain.SessionKind, otherServer.session, 1, domain.Session{CurrentExecution: &domain.ExecutionSelection{AccountID: f.account}})
	differentServer, err := otherServer.s.RegisterBrowserProfile(otherServer.first, connect.NewRequest(&pb.RegisterBrowserProfileRequest{Session: &pb.Mutation{Id: string(otherServer.session), ExpectedRevision: 2, RequestId: string(domain.NewID())}, AccountId: string(f.account)}))
	if err != nil || differentServer.Msg.Profile.Id == first.Profile.Id {
		t.Fatal(differentServer, err)
	}
}

func TestBrowserProfileFollowsPendingAccountSwitchBeforeResume(t *testing.T) {
	f, nextAccount, _ := accountSwitchFixture(t, domain.FullNativeHistory, true)
	device := domain.NewID()
	doctorPut(t, f.service, domain.DeviceKind, device, 0, domain.Device{Type: domain.ClientDevice})
	client := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: device})
	register := func(account string, revision uint64) (*connect.Response[pb.RegisterBrowserProfileResponse], error) {
		return f.service.RegisterBrowserProfile(client, connect.NewRequest(&pb.RegisterBrowserProfileRequest{
			Session: &pb.Mutation{Id: string(f.refresh(t).ID), ExpectedRevision: revision, RequestId: string(domain.NewID())}, AccountId: account,
		}))
	}
	before := f.refresh(t)
	original, _ := store.Decode[domain.Session](before)
	previous, err := register(f.account.Id, before.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sessionClient(f.accountFixture).SwitchSessionAccount(context.Background(), ownerRequest(f.identity, switchRequest(f, nextAccount, t))); err != nil {
		t.Fatal(err)
	}
	selected := f.refresh(t)
	paused, _ := store.Decode[domain.Session](selected)
	if paused.Dispatch != domain.DispatchPaused || paused.ExecutionSelection() != original.ExecutionSelection() {
		t.Fatal("fixture advanced execution before Resume")
	}
	if _, err = register(f.account.Id, selected.Revision); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("previous execution account authorized a new browser registration", err)
	}
	if _, err = register(nextAccount.Id, before.Revision); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale session revision authorized the pending account", err)
	}
	next, err := register(nextAccount.Id, selected.Revision)
	if err != nil || next.Msg.Profile.AccountId != nextAccount.Id || next.Msg.Profile.Id == previous.Msg.Profile.Id {
		t.Fatal("pending selection did not receive its isolated profile", next, err)
	}
	if _, err = sessionClient(f.accountFixture).SwitchSessionAccount(context.Background(), ownerRequest(f.identity, switchRequest(f, f.account, t))); err != nil {
		t.Fatal(err)
	}
	back, err := register(f.account.Id, f.refresh(t).Revision)
	if err != nil || back.Msg.Profile.Id != previous.Msg.Profile.Id {
		t.Fatal("latest switch did not restore the original account profile", back, err)
	}
}
func TestBrowserProfileSessionClosureRetainsDataAndAccountCleanupWaitsForEveryDevice(t *testing.T) {
	f := newBrowserFixture(t)
	request := domain.NewID()
	first := f.register(t, f.first, f.session, request)
	second := f.register(t, f.second, f.session, domain.NewID())
	r, err := f.s.Store.Get(context.Background(), domain.SessionKind, f.session)
	if err != nil {
		t.Fatal(err)
	}
	session, _ := store.Decode[domain.Session](r)
	session.Archive = domain.Archived
	doctorPut(t, f.s, domain.SessionKind, f.session, 1, session)
	_, err = f.s.Store.Mutate(f.first, domain.NewID(), "browser.fixture.session-delete", nil, func(tx *store.Tx) (any, error) { return nil, tx.Delete(domain.SessionKind, f.session, 2) })
	if err != nil {
		t.Fatal(err)
	}
	if p := profileBody(t, f.s, f.first, first.Profile.Id); p.State != domain.BrowserProfileActive {
		t.Fatal("session deletion removed profile", p)
	}
	deletion := domain.NewID()
	_, err = f.s.Store.Mutate(f.first, deletion, "browser.fixture.account-delete", nil, func(tx *store.Tx) (any, error) { return nil, tx.Delete(domain.AccountKind, f.account, 1) })
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err := f.s.GetAccountBrowserCleanup(f.first, connect.NewRequest(&pb.GetAccountBrowserCleanupRequest{AccountId: string(f.account)}))
	if err != nil || cleanup.Msg.Pending != 2 {
		t.Fatal(cleanup, err)
	}
	pending, err := f.s.GetBrowserProfile(f.first, connect.NewRequest(&pb.GetBrowserProfileRequest{Id: first.Profile.Id}))
	if err != nil {
		t.Fatal(err)
	}
	if p := profileBody(t, f.s, f.first, first.Profile.Id); p.State != domain.BrowserProfileRemovalPending || p.DeletionRequestID != deletion {
		t.Fatal(p)
	}
	confirm := &pb.ConfirmBrowserProfileRemovalRequest{Mutation: &pb.Mutation{Id: first.Profile.Id, ExpectedRevision: pending.Msg.Profile.Revision, RequestId: string(domain.NewID())}, DeletionRequestId: string(deletion)}
	if _, err = f.s.ConfirmBrowserProfileRemoval(f.second, connect.NewRequest(confirm)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("another device acknowledged cleanup", err)
	}
	confirm.Mutation.ExpectedRevision--
	if _, err = f.s.ConfirmBrowserProfileRemoval(f.first, connect.NewRequest(confirm)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale cleanup accepted", err)
	}
	confirm.Mutation.ExpectedRevision++
	ack, err := f.s.ConfirmBrowserProfileRemoval(f.first, connect.NewRequest(confirm))
	if err != nil || ack.Msg.Profile.Revision != 3 {
		t.Fatal(ack, err)
	}
	replay, err := f.s.ConfirmBrowserProfileRemoval(f.first, connect.NewRequest(confirm))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Profile.Revision != 3 {
		t.Fatal(replay, err)
	}
	f.s.Store.Close()
	f.s.Store, err = store.Open(context.Background(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err = f.s.GetAccountBrowserCleanup(f.first, connect.NewRequest(&pb.GetAccountBrowserCleanupRequest{AccountId: string(f.account)}))
	if err != nil || cleanup.Msg.Pending != 1 || cleanup.Msg.Removed != 1 {
		t.Fatal("offline cleanup lost at restart", cleanup, err)
	}
	offline, err := f.s.GetBrowserProfile(f.second, connect.NewRequest(&pb.GetBrowserProfileRequest{Id: second.Profile.Id}))
	if err != nil {
		t.Fatal(err)
	}
	confirm = &pb.ConfirmBrowserProfileRemovalRequest{Mutation: &pb.Mutation{Id: second.Profile.Id, ExpectedRevision: offline.Msg.Profile.Revision, RequestId: string(domain.NewID())}, DeletionRequestId: string(deletion)}
	if _, err = f.s.ConfirmBrowserProfileRemoval(f.second, connect.NewRequest(confirm)); err != nil {
		t.Fatal(err)
	}
	cleanup, err = f.s.GetAccountBrowserCleanup(f.first, connect.NewRequest(&pb.GetAccountBrowserCleanupRequest{AccountId: string(f.account)}))
	if err != nil || cleanup.Msg.Pending != 0 || cleanup.Msg.Removed != 2 {
		t.Fatal(cleanup, err)
	}
	// An original registration receipt returns current removed state, never active state.
	replayRegistration := f.register(t, f.first, f.session, request)
	if !replayRegistration.Replayed || profileBody(t, f.s, f.first, replayRegistration.Profile.Id).State != domain.BrowserProfileRemoved {
		t.Fatal("receipt resurrected deleted profile")
	}
}
func TestBrowserProfileRejectsWorkerOwnerRevokedDeviceAndStaleRevision(t *testing.T) {
	f := newBrowserFixture(t)
	if _, err := f.s.GetBrowserProfile(f.first, connect.NewRequest(&pb.GetBrowserProfileRequest{Id: "malformed"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("malformed profile identity", err)
	}
	for _, kind := range []domain.DeviceType{domain.OwnerDevice, domain.WorkerDevice} {
		ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: kind, DeviceID: domain.NewID()})
		_, err := f.s.RegisterBrowserProfile(ctx, connect.NewRequest(&pb.RegisterBrowserProfileRequest{}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal(kind, err)
		}
	}
	_, err := f.s.RegisterBrowserProfile(f.first, connect.NewRequest(&pb.RegisterBrowserProfileRequest{Session: &pb.Mutation{Id: string(f.session), ExpectedRevision: 2, RequestId: string(domain.NewID())}, AccountId: string(f.account)}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale session", err)
	}
	actor, _ := domain.PrincipalFrom(f.first)
	doctorPut(t, f.s, domain.DeviceKind, actor.DeviceID, 1, domain.Device{Type: domain.ClientDevice, Revoked: true})
	_, err = f.s.ListBrowserProfiles(f.first, connect.NewRequest(&pb.ListBrowserProfilesRequest{}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("revoked client", err)
	}
}

func TestBrowserConnectRejectsUnauthenticatedAndHostileOrigins(t *testing.T) {
	f := newBrowserFixture(t)
	actor, _ := domain.PrincipalFrom(f.first)
	token := string(domain.NewID())
	digest := sha256.Sum256([]byte(token))
	_, err := f.s.Store.Mutate(context.Background(), domain.NewID(), "browser.fixture.credential", nil, func(tx *store.Tx) (any, error) { return nil, tx.PutCredential(actor.DeviceID, digest[:]) })
	if err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(f.s.Handler([]string{"http://tauri.localhost"}, true))
	defer host.Close()
	client := delidevv1connect.NewBrowserServiceClient(host.Client(), host.URL)
	if _, err = client.GetBrowserCapabilities(context.Background(), connect.NewRequest(&pb.GetBrowserCapabilitiesRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("anonymous capability", err)
	}
	req := connect.NewRequest(&pb.GetBrowserCapabilitiesRequest{})
	req.Header().Set("Authorization", "Bearer "+token)
	req.Header().Set("Origin", "https://hostile.test")
	if _, err = client.GetBrowserCapabilities(context.Background(), req); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("external origin used product authorization", err)
	}
	req.Header().Set("Origin", "http://tauri.localhost")
	capabilities, err := client.GetBrowserCapabilities(context.Background(), req)
	if err != nil || len(capabilities.Msg.Capabilities) != 1 {
		t.Fatal(capabilities, err)
	}
	registration := connect.NewRequest(&pb.RegisterBrowserProfileRequest{Session: &pb.Mutation{Id: string(f.session), ExpectedRevision: 1, RequestId: string(domain.NewID())}, AccountId: string(f.account)})
	registration.Header().Set("Authorization", "Bearer "+token)
	response, err := client.RegisterBrowserProfile(context.Background(), registration)
	if err != nil || response.Msg.Profile.DeviceId != string(actor.DeviceID) || response.Msg.Profile.State != pb.BrowserProfileState_BROWSER_PROFILE_STATE_ACTIVE {
		t.Fatal(response, err)
	}
}
func TestBrowserConcurrentRegistrationPublishesOneDeviceProfile(t *testing.T) {
	f := newBrowserFixture(t)
	var wait sync.WaitGroup
	ids := make(chan string, 8)
	failures := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, err := f.s.RegisterBrowserProfile(f.first, connect.NewRequest(&pb.RegisterBrowserProfileRequest{Session: &pb.Mutation{Id: string(f.session), ExpectedRevision: 1, RequestId: string(domain.NewID())}, AccountId: string(f.account)}))
			if err != nil {
				failures <- err
				return
			}
			ids <- response.Msg.Profile.Id
		}()
	}
	wait.Wait()
	close(ids)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	unique := map[string]bool{}
	for id := range ids {
		unique[id] = true
	}
	if len(unique) != 1 {
		t.Fatal("concurrent registrations duplicated profile identities", unique)
	}
	actor, _ := domain.PrincipalFrom(f.first)
	r, err := f.s.Store.Get(context.Background(), domain.DeviceKind, actor.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	device, err := store.Decode[domain.Device](r)
	if err != nil || len(device.BrowserProfiles) != 1 || r.Revision != 2 {
		t.Fatal("duplicate profile publication", r.Revision, device, err)
	}
}
