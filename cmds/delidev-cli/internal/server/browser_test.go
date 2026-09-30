// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"io"
	"log/slog"
	"path/filepath"
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
