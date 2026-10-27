// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	rpc "github.com/delinoio/oss/protos/aiprouter/v1/aiprouterv1connect"
)

type acceptanceFixture struct {
	t   *testing.T
	ctx context.Context
	s   *store.Store
	url string
}

func newAcceptance(t *testing.T) *acceptanceFixture {
	t.Helper()
	s := serverStore(t)
	config := Config{PublicURL: "https://router.test", Operators: []OperatorIdentity{{Issuer: "https://identity.test", Subject: "operator"}}}
	srv := &server{config: config, store: s, auth: &Auth{config: config, store: persistentAuth{s: s}}, telemetry: newTelemetry(slog.New(slog.NewJSONHandler(io.Discard, nil)))}
	h := httptest.NewServer(srv.handler())
	t.Cleanup(h.Close)
	return &acceptanceFixture{t: t, ctx: context.Background(), s: s, url: h.URL}
}
func (f *acceptanceFixture) user(name string) (store.User, *http.Client) {
	f.t.Helper()
	u, session := testIdentity(f.t, f.s, name)
	return u, &http.Client{Transport: sessionTransport{origin: "https://router.test", cookie: session}}
}
func (f *acceptanceFixture) org(c *http.Client, name string) string {
	f.t.Helper()
	r, e := rpc.NewOrganizationServiceClient(c, f.url).CreateOrganization(f.ctx, connect.NewRequest(&v1.CreateOrganizationRequest{Name: name}))
	acceptanceOK(f.t, e)
	return r.Msg.Organization.Id
}
func acceptanceOK(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func acceptanceCode(t *testing.T, e error, want connect.Code) {
	t.Helper()
	if e == nil || connect.CodeOf(e) != want {
		t.Fatalf("RPC error = %v; want %s", e, want)
	}
}

// Capture the delivery boundary without SMTP timing or exposing tokens in test output.
func (f *acceptanceFixture) delivery() string {
	f.t.Helper()
	mail, e := f.s.ClaimMail(f.ctx)
	acceptanceOK(f.t, e)
	var token string
	e = f.s.WithMailAttempt(f.ctx, mail, func(_ context.Context, m store.Mail) store.MailOutcome { token = m.Token; return store.MailSuccess })
	acceptanceOK(f.t, e)
	if token == "" {
		f.t.Fatal("invitation delivery omitted token")
	}
	return token
}
func (f *acceptanceFixture) join(owner, member *http.Client, org, email string) {
	f.t.Helper()
	_, e := rpc.NewInvitationServiceClient(owner, f.url).CreateInvitation(f.ctx, connect.NewRequest(&v1.CreateInvitationRequest{OrganizationId: org, Email: email}))
	acceptanceOK(f.t, e)
	_, e = rpc.NewInvitationServiceClient(member, f.url).AcceptInvitation(f.ctx, connect.NewRequest(&v1.AcceptInvitationRequest{Token: f.delivery()}))
	acceptanceOK(f.t, e)
}
func TestAcceptanceLiveDemotion(t *testing.T) {
	f := newAcceptance(t)
	_, owner := f.user("owner")
	adminUser, admin := f.user("admin")
	peerUser, peer := f.user("peer")
	org := f.org(owner, "roles")
	f.join(owner, admin, org, adminUser.Email)
	f.join(owner, peer, org, peerUser.Email)
	members := rpc.NewMembershipServiceClient(owner, f.url)
	for _, u := range []store.User{adminUser, peerUser} {
		_, e := members.SetMemberRole(f.ctx, connect.NewRequest(&v1.SetMemberRoleRequest{OrganizationId: org, UserId: u.ID, Role: v1.Role_ROLE_ADMINISTRATOR}))
		acceptanceOK(t, e)
	}
	adminMembers := rpc.NewMembershipServiceClient(admin, f.url)
	_, e := adminMembers.RemoveMember(f.ctx, connect.NewRequest(&v1.RemoveMemberRequest{OrganizationId: org, UserId: peerUser.ID}))
	acceptanceCode(t, e, connect.CodePermissionDenied)
	_, e = adminMembers.SetMemberRole(f.ctx, connect.NewRequest(&v1.SetMemberRoleRequest{OrganizationId: org, UserId: peerUser.ID, Role: v1.Role_ROLE_MEMBER}))
	acceptanceCode(t, e, connect.CodePermissionDenied)
	orgs := rpc.NewOrganizationServiceClient(admin, f.url)
	_, e = orgs.UpdateOrganization(f.ctx, connect.NewRequest(&v1.UpdateOrganizationRequest{OrganizationId: org, Name: "before demotion"}))
	acceptanceOK(t, e)
	_, e = members.SetMemberRole(f.ctx, connect.NewRequest(&v1.SetMemberRoleRequest{OrganizationId: org, UserId: adminUser.ID, Role: v1.Role_ROLE_MEMBER}))
	acceptanceOK(t, e)
	_, e = orgs.UpdateOrganization(f.ctx, connect.NewRequest(&v1.UpdateOrganizationRequest{OrganizationId: org, Name: "forbidden rename"}))
	acceptanceCode(t, e, connect.CodePermissionDenied)
	_, e = adminMembers.ListMembers(f.ctx, connect.NewRequest(&v1.ListMembersRequest{OrganizationId: org}))
	acceptanceCode(t, e, connect.CodePermissionDenied)
	_, e = rpc.NewInvitationServiceClient(admin, f.url).CreateInvitation(f.ctx, connect.NewRequest(&v1.CreateInvitationRequest{OrganizationId: org, Email: "new@example.test"}))
	acceptanceCode(t, e, connect.CodePermissionDenied)
	session, e := rpc.NewAccountServiceClient(admin, f.url).GetSession(f.ctx, connect.NewRequest(&v1.GetSessionRequest{}))
	acceptanceOK(t, e)
	if session.Msg.UserId != adminUser.ID {
		t.Fatal("demotion changed session identity")
	}
	got, e := orgs.GetOrganization(f.ctx, connect.NewRequest(&v1.GetOrganizationRequest{OrganizationId: org}))
	acceptanceOK(t, e)
	if got.Msg.Organization.Name != "before demotion" || got.Msg.Organization.Role != v1.Role_ROLE_MEMBER {
		t.Fatal("demotion or denied write did not persist correctly")
	}
}
func TestAcceptanceOwnershipReplacementCancellationAndRemoval(t *testing.T) {
	f := newAcceptance(t)
	_, owner := f.user("owner")
	a, alice := f.user("alice")
	b, bob := f.user("bob")
	org := f.org(owner, "ownership")
	f.join(owner, alice, org, a.Email)
	f.join(owner, bob, org, b.Email)
	owners := rpc.NewOwnershipServiceClient(owner, f.url)
	nominate := func(user string) string {
		t.Helper()
		r, e := owners.NominateOwner(f.ctx, connect.NewRequest(&v1.NominateOwnerRequest{OrganizationId: org, UserId: user}))
		acceptanceOK(t, e)
		return r.Msg.Transfer.Id
	}
	accept := func(c *http.Client, id string) error {
		_, e := rpc.NewOwnershipServiceClient(c, f.url).AcceptOwnership(f.ctx, connect.NewRequest(&v1.AcceptOwnershipRequest{OrganizationId: org, TransferId: id}))
		return e
	}
	old := nominate(a.ID)
	current := nominate(b.ID)
	if e := accept(alice, old); e == nil {
		t.Fatal("superseded transfer accepted")
	}
	_, e := owners.CancelOwnershipTransfer(f.ctx, connect.NewRequest(&v1.CancelOwnershipTransferRequest{OrganizationId: org, TransferId: current}))
	acceptanceOK(t, e)
	if e := accept(bob, current); e == nil {
		t.Fatal("canceled transfer accepted")
	}
	removed := nominate(a.ID)
	_, e = rpc.NewMembershipServiceClient(owner, f.url).RemoveMember(f.ctx, connect.NewRequest(&v1.RemoveMemberRequest{OrganizationId: org, UserId: a.ID}))
	acceptanceOK(t, e)
	acceptanceCode(t, accept(alice, removed), connect.CodeNotFound)
	_, e = rpc.NewOrganizationServiceClient(owner, f.url).LeaveOrganization(f.ctx, connect.NewRequest(&v1.LeaveOrganizationRequest{OrganizationId: org}))
	if e == nil {
		t.Fatal("last owner left organization")
	}
	final := nominate(b.ID)
	acceptanceOK(t, accept(bob, final))
	if e := accept(bob, final); e == nil {
		t.Fatal("ownership acceptance replay succeeded")
	}
	for _, tc := range []struct {
		c    *http.Client
		role v1.Role
	}{{owner, v1.Role_ROLE_ADMINISTRATOR}, {bob, v1.Role_ROLE_OWNER}} {
		r, e := rpc.NewOrganizationServiceClient(tc.c, f.url).GetOrganization(f.ctx, connect.NewRequest(&v1.GetOrganizationRequest{OrganizationId: org}))
		acceptanceOK(t, e)
		if r.Msg.Organization.Role != tc.role {
			t.Fatalf("role = %s; want %s", r.Msg.Organization.Role, tc.role)
		}
	}
	_, e = rpc.NewOrganizationServiceClient(owner, f.url).DeleteOrganization(f.ctx, connect.NewRequest(&v1.DeleteOrganizationRequest{OrganizationId: org}))
	acceptanceCode(t, e, connect.CodePermissionDenied)
}
func TestAcceptanceInvitationEmailReplayAndCancel(t *testing.T) {
	f := newAcceptance(t)
	_, owner := f.user("owner")
	a, alice := f.user("alice")
	b, bob := f.user("bob")
	org := f.org(owner, "invitations")
	invites := rpc.NewInvitationServiceClient(owner, f.url)
	created, e := invites.CreateInvitation(f.ctx, connect.NewRequest(&v1.CreateInvitationRequest{OrganizationId: org, Email: a.Email}))
	acceptanceOK(t, e)
	_, e = invites.ResendInvitation(f.ctx, connect.NewRequest(&v1.ResendInvitationRequest{OrganizationId: org, InvitationId: created.Msg.Invitation.Id}))
	acceptanceCode(t, e, connect.CodeResourceExhausted)
	token := f.delivery()
	_, e = rpc.NewInvitationServiceClient(bob, f.url).AcceptInvitation(f.ctx, connect.NewRequest(&v1.AcceptInvitationRequest{Token: token}))
	acceptanceCode(t, e, connect.CodePermissionDenied)
	_, e = rpc.NewInvitationServiceClient(alice, f.url).AcceptInvitation(f.ctx, connect.NewRequest(&v1.AcceptInvitationRequest{Token: token}))
	acceptanceOK(t, e)
	_, e = rpc.NewInvitationServiceClient(alice, f.url).AcceptInvitation(f.ctx, connect.NewRequest(&v1.AcceptInvitationRequest{Token: token}))
	acceptanceCode(t, e, connect.CodeNotFound)
	canceled, e := invites.CreateInvitation(f.ctx, connect.NewRequest(&v1.CreateInvitationRequest{OrganizationId: org, Email: b.Email}))
	acceptanceOK(t, e)
	token = f.delivery()
	_, e = invites.CancelInvitation(f.ctx, connect.NewRequest(&v1.CancelInvitationRequest{OrganizationId: org, InvitationId: canceled.Msg.Invitation.Id}))
	acceptanceOK(t, e)
	_, e = rpc.NewInvitationServiceClient(bob, f.url).AcceptInvitation(f.ctx, connect.NewRequest(&v1.AcceptInvitationRequest{Token: token}))
	acceptanceCode(t, e, connect.CodeNotFound)
	list, e := rpc.NewOrganizationServiceClient(bob, f.url).ListOrganizations(f.ctx, connect.NewRequest(&v1.ListOrganizationsRequest{}))
	acceptanceOK(t, e)
	if len(list.Msg.Organizations) != 0 {
		t.Fatal("rejected invitation created membership")
	}
}
func TestAcceptanceDeviceHandoffReplayAndSelfRevocation(t *testing.T) {
	f := newAcceptance(t)
	_, owner := f.user("owner")
	org := f.org(owner, "device")
	device := rpc.NewDeviceAuthorizationServiceClient(http.DefaultClient, f.url)
	approval := rpc.NewDeviceApprovalServiceClient(owner, f.url)
	started, e := device.BeginDeviceAuthorization(f.ctx, connect.NewRequest(&v1.BeginDeviceAuthorizationRequest{DeviceName: "SSH laptop"}))
	acceptanceOK(t, e)
	d := started.Msg
	if d.PollIntervalSeconds != 5 || time.Until(d.ExpiresAt.AsTime()) > 10*time.Minute || time.Until(d.ExpiresAt.AsTime()) < 9*time.Minute {
		t.Fatal("incorrect device lifetime or poll interval")
	}
	_, e = device.PollDeviceAuthorization(f.ctx, connect.NewRequest(&v1.PollDeviceAuthorizationRequest{AuthorizationId: d.AuthorizationId, DeviceSecret: "incorrect"}))
	acceptanceCode(t, e, connect.CodeUnauthenticated)
	_, e = approval.ApproveDeviceAuthorization(f.ctx, connect.NewRequest(&v1.ApproveDeviceAuthorizationRequest{UserCode: d.UserCode, OrganizationId: org}))
	acceptanceOK(t, e)
	_, e = approval.ApproveDeviceAuthorization(f.ctx, connect.NewRequest(&v1.ApproveDeviceAuthorizationRequest{UserCode: d.UserCode, OrganizationId: org}))
	if e == nil {
		t.Fatal("approval replay issued another key")
	}
	poll := func() *v1.PollDeviceAuthorizationResponse {
		t.Helper()
		r, e := device.PollDeviceAuthorization(f.ctx, connect.NewRequest(&v1.PollDeviceAuthorizationRequest{AuthorizationId: d.AuthorizationId, DeviceSecret: d.DeviceSecret}))
		acceptanceOK(t, e)
		return r.Msg
	}
	first := poll()
	if first.Result == nil || first.Result.RawKey == "" || first.Result.OrganizationId != org {
		t.Fatal("missing or incorrectly scoped handoff")
	}
	_, e = device.PollDeviceAuthorization(f.ctx, connect.NewRequest(&v1.PollDeviceAuthorizationRequest{AuthorizationId: d.AuthorizationId, DeviceSecret: d.DeviceSecret}))
	acceptanceCode(t, e, connect.CodeResourceExhausted)
	time.Sleep(5100 * time.Millisecond)
	second := poll()
	if second.Result == nil || second.Result.KeyId != first.Result.KeyId || second.Result.RawKey != first.Result.RawKey {
		t.Fatal("interrupted handoff did not return same key")
	}
	keys, e := rpc.NewKeyServiceClient(owner, f.url).ListKeys(f.ctx, connect.NewRequest(&v1.ListKeysRequest{OrganizationId: org}))
	acceptanceOK(t, e)
	if len(keys.Msg.Keys) != 1 {
		t.Fatal("approval replay created multiple keys")
	}
	_, e = device.ConfirmDeviceStorage(f.ctx, connect.NewRequest(&v1.ConfirmDeviceStorageRequest{AuthorizationId: d.AuthorizationId, DeviceSecret: "incorrect"}))
	acceptanceCode(t, e, connect.CodeUnauthenticated)
	_, e = device.ConfirmDeviceStorage(f.ctx, connect.NewRequest(&v1.ConfirmDeviceStorageRequest{AuthorizationId: d.AuthorizationId, DeviceSecret: d.DeviceSecret}))
	acceptanceOK(t, e)
	delivered := poll()
	if delivered.State != v1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_DELIVERED || delivered.Result != nil {
		t.Fatal("confirmed handoff exposes raw key")
	}
	other, e := rpc.NewKeyServiceClient(owner, f.url).CreateKey(f.ctx, connect.NewRequest(&v1.CreateKeyRequest{OrganizationId: org, Name: "unrelated"}))
	acceptanceOK(t, e)
	client := rpc.NewClientServiceClient(&http.Client{Transport: sessionTransport{bearer: first.Result.RawKey}}, f.url)
	_, e = client.RevokeCLIKey(f.ctx, connect.NewRequest(&v1.RevokeCLIKeyRequest{KeyId: other.Msg.Key.Id}))
	acceptanceCode(t, e, connect.CodeUnauthenticated)
	_, e = client.RevokeCLIKey(f.ctx, connect.NewRequest(&v1.RevokeCLIKeyRequest{KeyId: first.Result.KeyId}))
	acceptanceOK(t, e)
	_, e = client.ListClientModels(f.ctx, connect.NewRequest(&v1.ListClientModelsRequest{}))
	acceptanceCode(t, e, connect.CodeUnauthenticated)
	_, e = f.s.AuthenticateKey(f.ctx, other.Msg.RawKey)
	acceptanceOK(t, e)
}
func TestAcceptanceDeviceAbortAndDenial(t *testing.T) {
	for _, approve := range []bool{false, true} {
		t.Run(map[bool]string{false: "denied", true: "storage-failure"}[approve], func(t *testing.T) {
			f := newAcceptance(t)
			_, owner := f.user("owner")
			org := f.org(owner, "abort")
			device := rpc.NewDeviceAuthorizationServiceClient(http.DefaultClient, f.url)
			approval := rpc.NewDeviceApprovalServiceClient(owner, f.url)
			started, e := device.BeginDeviceAuthorization(f.ctx, connect.NewRequest(&v1.BeginDeviceAuthorizationRequest{DeviceName: "laptop"}))
			acceptanceOK(t, e)
			d := started.Msg
			var raw string
			if approve {
				_, e = approval.ApproveDeviceAuthorization(f.ctx, connect.NewRequest(&v1.ApproveDeviceAuthorizationRequest{UserCode: d.UserCode, OrganizationId: org}))
				acceptanceOK(t, e)
				p, e := device.PollDeviceAuthorization(f.ctx, connect.NewRequest(&v1.PollDeviceAuthorizationRequest{AuthorizationId: d.AuthorizationId, DeviceSecret: d.DeviceSecret}))
				acceptanceOK(t, e)
				raw = p.Msg.Result.RawKey
				_, e = device.RevokeDeviceKey(f.ctx, connect.NewRequest(&v1.RevokeDeviceKeyRequest{AuthorizationId: d.AuthorizationId, DeviceSecret: "wrong"}))
				acceptanceCode(t, e, connect.CodeUnauthenticated)
				_, e = f.s.AuthenticateKey(f.ctx, raw)
				acceptanceOK(t, e)
				_, e = device.RevokeDeviceKey(f.ctx, connect.NewRequest(&v1.RevokeDeviceKeyRequest{AuthorizationId: d.AuthorizationId, DeviceSecret: d.DeviceSecret}))
				acceptanceOK(t, e)
				if _, e = f.s.AuthenticateKey(f.ctx, raw); e == nil {
					t.Fatal("aborted device key remains valid")
				}
			} else {
				_, e = approval.DenyDeviceAuthorization(f.ctx, connect.NewRequest(&v1.DenyDeviceAuthorizationRequest{UserCode: d.UserCode}))
				acceptanceOK(t, e)
			}
			_, e = approval.ApproveDeviceAuthorization(f.ctx, connect.NewRequest(&v1.ApproveDeviceAuthorizationRequest{UserCode: d.UserCode, OrganizationId: org}))
			if e == nil {
				t.Fatal("denied device code reused")
			}
			_, e = device.ConfirmDeviceStorage(f.ctx, connect.NewRequest(&v1.ConfirmDeviceStorageRequest{AuthorizationId: d.AuthorizationId, DeviceSecret: d.DeviceSecret}))
			if e == nil {
				t.Fatal("denied device storage confirmed")
			}
		})
	}
}

func TestAcceptancePersonalResourcesAcrossOrganizations(t *testing.T) {
	f := newAcceptance(t)
	_, alice := f.user("alice")
	bobUser, bob := f.user("bob")
	a := f.org(alice, "A")
	b := f.org(alice, "B")
	f.join(alice, bob, a, bobUser.Email)
	providers := rpc.NewProviderServiceClient(alice, f.url)
	secret := "fixture-provider-secret-never-returned"
	created, e := providers.CreateProviderConnection(f.ctx, connect.NewRequest(&v1.CreateProviderConnectionRequest{OrganizationId: a, ProviderId: "custom", Name: "private", BaseUrl: "https://api.openai.com", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, ApiKey: &secret}))
	acceptanceOK(t, e)
	if strings.Contains(created.Msg.String(), secret) {
		t.Fatal("create response exposes provider secret")
	}
	for _, tc := range []struct {
		name, org string
		c         *http.Client
	}{{"same user other organization", b, alice}, {"other user same organization", a, bob}} {
		t.Run(tc.name, func(t *testing.T) {
			p := rpc.NewProviderServiceClient(tc.c, f.url)
			list, e := p.ListProviderConnections(f.ctx, connect.NewRequest(&v1.ListProviderConnectionsRequest{OrganizationId: tc.org}))
			acceptanceOK(t, e)
			if len(list.Msg.Connections) != 0 {
				t.Fatal("private connection crossed ownership boundary")
			}
			_, e = p.UpdateProviderConnection(f.ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{OrganizationId: tc.org, ConnectionId: created.Msg.Connection.Id, ApiKey: ptr("attacker-replacement")}))
			acceptanceCode(t, e, connect.CodeNotFound)
			_, e = rpc.NewPoolServiceClient(tc.c, f.url).CreatePool(f.ctx, connect.NewRequest(&v1.CreatePoolRequest{OrganizationId: tc.org, Name: "stolen", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: []*v1.PoolMember{{ConnectionId: created.Msg.Connection.Id}}}))
			acceptanceCode(t, e, connect.CodeNotFound)
		})
	}
	list, e := providers.ListProviderConnections(f.ctx, connect.NewRequest(&v1.ListProviderConnectionsRequest{OrganizationId: a}))
	acceptanceOK(t, e)
	if len(list.Msg.Connections) != 1 || strings.Contains(list.Msg.String(), secret) {
		t.Fatal("private connection missing or secret exposed")
	}
	keys := rpc.NewKeyServiceClient(alice, f.url)
	key, e := keys.CreateKey(f.ctx, connect.NewRequest(&v1.CreateKeyRequest{OrganizationId: a, Name: "A only"}))
	acceptanceOK(t, e)
	_, e = keys.RevokeKey(f.ctx, connect.NewRequest(&v1.RevokeKeyRequest{OrganizationId: b, KeyId: key.Msg.Key.Id}))
	if e != nil {
		acceptanceCode(t, e, connect.CodeNotFound)
	}
	_, e = f.s.AuthenticateKey(f.ctx, key.Msg.RawKey)
	acceptanceOK(t, e)
	listed, e := keys.ListKeys(f.ctx, connect.NewRequest(&v1.ListKeysRequest{OrganizationId: a}))
	acceptanceOK(t, e)
	if strings.Contains(listed.Msg.String(), key.Msg.RawKey) {
		t.Fatal("key list exposes raw key")
	}
	_, e = keys.RevokeKey(f.ctx, connect.NewRequest(&v1.RevokeKeyRequest{OrganizationId: a, KeyId: key.Msg.Key.Id}))
	acceptanceOK(t, e)
	_, e = rpc.NewClientServiceClient(&http.Client{Transport: sessionTransport{bearer: key.Msg.RawKey}}, f.url).ListClientModels(f.ctx, connect.NewRequest(&v1.ListClientModelsRequest{}))
	acceptanceCode(t, e, connect.CodeUnauthenticated)
}
func TestAcceptanceOperatorCannotInspectOrganization(t *testing.T) {
	f := newAcceptance(t)
	_, owner := f.user("owner")
	_, operator := f.user("operator")
	org := f.org(owner, "private organization")
	session, e := rpc.NewAccountServiceClient(operator, f.url).GetSession(f.ctx, connect.NewRequest(&v1.GetSessionRequest{}))
	acceptanceOK(t, e)
	if !session.Msg.IsInstanceOperator {
		t.Fatal("operator fixture is not an operator")
	}
	_, e = rpc.NewOperatorServiceClient(operator, f.url).GetInstanceSettings(f.ctx, connect.NewRequest(&v1.GetInstanceSettingsRequest{}))
	acceptanceOK(t, e)
	_, e = rpc.NewOperatorServiceClient(owner, f.url).GetInstanceSettings(f.ctx, connect.NewRequest(&v1.GetInstanceSettingsRequest{}))
	acceptanceCode(t, e, connect.CodePermissionDenied)
	_, e = rpc.NewUsageServiceClient(operator, f.url).ListUsage(f.ctx, connect.NewRequest(&v1.ListUsageRequest{OrganizationId: org, UserId: ptr("")}))
	acceptanceCode(t, e, connect.CodeNotFound)
	_, e = rpc.NewAuditServiceClient(operator, f.url).ListOrganizationAudit(f.ctx, connect.NewRequest(&v1.ListOrganizationAuditRequest{OrganizationId: org}))
	acceptanceCode(t, e, connect.CodeNotFound)
	_, e = rpc.NewProviderServiceClient(operator, f.url).ListProviderConnections(f.ctx, connect.NewRequest(&v1.ListProviderConnectionsRequest{OrganizationId: org}))
	acceptanceCode(t, e, connect.CodeNotFound)
	_, e = rpc.NewMembershipServiceClient(operator, f.url).ListMembers(f.ctx, connect.NewRequest(&v1.ListMembersRequest{OrganizationId: org}))
	acceptanceCode(t, e, connect.CodeNotFound)
}
func TestAcceptanceAccountDeletionAnonymizesRetainedMetadata(t *testing.T) {
	f := newAcceptance(t)
	_, owner := f.user("owner")
	member, user := f.user("member")
	org := f.org(owner, "surviving org")
	f.join(owner, user, org, member.Email)
	_, e := rpc.NewAccountServiceClient(owner, f.url).DeleteAccount(f.ctx, connect.NewRequest(&v1.DeleteAccountRequest{}))
	if e == nil {
		t.Fatal("account owner deleted without transfer")
	}
	key, e := rpc.NewKeyServiceClient(user, f.url).CreateKey(f.ctx, connect.NewRequest(&v1.CreateKeyRequest{OrganizationId: org, Name: "member key"}))
	acceptanceOK(t, e)
	// Seed a completed inference at the storage boundary; all metadata reads and deletion use HTTP.
	lease, e := f.s.Admit(f.ctx, key.Msg.RawKey)
	acceptanceOK(t, e)
	acceptanceOK(t, f.s.RecordUsage(f.ctx, lease, store.Usage{Model: "fixture-model", Provider: "openai", InputTokens: ptr(int64(12)), OutputTokens: ptr(int64(4)), Cost: ptr("0.125")}))
	acceptanceOK(t, f.s.Release(f.ctx, lease))
	usage := rpc.NewUsageServiceClient(owner, f.url)
	before, e := usage.ListUsage(f.ctx, connect.NewRequest(&v1.ListUsageRequest{OrganizationId: org, UserId: ptr("")}))
	acceptanceOK(t, e)
	if len(before.Msg.Records) != 1 || before.Msg.Records[0].GetUserId() != member.ID {
		t.Fatal("missing usage fixture")
	}
	_, e = rpc.NewAccountServiceClient(user, f.url).DeleteAccount(f.ctx, connect.NewRequest(&v1.DeleteAccountRequest{}))
	acceptanceOK(t, e)
	_, e = rpc.NewAccountServiceClient(user, f.url).GetSession(f.ctx, connect.NewRequest(&v1.GetSessionRequest{}))
	acceptanceCode(t, e, connect.CodeUnauthenticated)
	_, e = rpc.NewClientServiceClient(&http.Client{Transport: sessionTransport{bearer: key.Msg.RawKey}}, f.url).ListClientModels(f.ctx, connect.NewRequest(&v1.ListClientModelsRequest{}))
	acceptanceCode(t, e, connect.CodeUnauthenticated)
	after, e := usage.ListUsage(f.ctx, connect.NewRequest(&v1.ListUsageRequest{OrganizationId: org, UserId: ptr("")}))
	acceptanceOK(t, e)
	if len(after.Msg.Records) != 1 || after.Msg.Records[0].UserId != nil || after.Msg.Records[0].EstimatedCost.GetDecimalAmount() != "0.125" {
		t.Fatalf("usage was lost, changed, or not anonymized: %v", after.Msg)
	}
	audits, e := rpc.NewAuditServiceClient(owner, f.url).ListOrganizationAudit(f.ctx, connect.NewRequest(&v1.ListOrganizationAuditRequest{OrganizationId: org}))
	acceptanceOK(t, e)
	for _, a := range audits.Msg.Records {
		if a.GetActorUserId() == member.ID {
			t.Fatal("deleted actor remains identifiable")
		}
	}
	members, e := rpc.NewMembershipServiceClient(owner, f.url).ListMembers(f.ctx, connect.NewRequest(&v1.ListMembersRequest{OrganizationId: org}))
	acceptanceOK(t, e)
	if len(members.Msg.Members) != 1 {
		t.Fatal("account deletion retained membership")
	}
}
func TestAcceptanceDatabaseFailureRejectsNewRequests(t *testing.T) {
	f := newAcceptance(t)
	_, owner := f.user("owner")
	org := f.org(owner, "outage")
	key, e := rpc.NewKeyServiceClient(owner, f.url).CreateKey(f.ctx, connect.NewRequest(&v1.CreateKeyRequest{OrganizationId: org, Name: "outage key"}))
	acceptanceOK(t, e)
	_, e = rpc.NewAccountServiceClient(owner, f.url).GetSession(f.ctx, connect.NewRequest(&v1.GetSessionRequest{}))
	acceptanceOK(t, e)
	// Close this fixture's pool only. Other integration tests retain their own database connections.
	f.s.Close()
	_, e = rpc.NewAccountServiceClient(owner, f.url).GetSession(f.ctx, connect.NewRequest(&v1.GetSessionRequest{}))
	acceptanceCode(t, e, connect.CodeUnavailable)
	_, e = rpc.NewDeviceAuthorizationServiceClient(http.DefaultClient, f.url).BeginDeviceAuthorization(f.ctx, connect.NewRequest(&v1.BeginDeviceAuthorizationRequest{DeviceName: "outage"}))
	acceptanceCode(t, e, connect.CodeUnavailable)
	for _, path := range []string{"/readyz", "/v1/models"} {
		req, e := http.NewRequestWithContext(f.ctx, http.MethodGet, f.url+path, nil)
		acceptanceOK(t, e)
		req.Header.Set("Authorization", "Bearer "+key.Msg.RawKey)
		res, e := http.DefaultClient.Do(req)
		acceptanceOK(t, e)
		body, e := io.ReadAll(res.Body)
		res.Body.Close()
		acceptanceOK(t, e)
		if res.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d", path, res.StatusCode)
		}
		if strings.Contains(string(body), key.Msg.RawKey) || strings.Contains(string(body), "sql:") {
			t.Fatal("outage response exposes key or database details")
		}
	}
}

func TestAcceptanceRemovalPreservesOtherOrganizationAndRejoinIsEmpty(t *testing.T) {
	f := newAcceptance(t)
	ownerUser, owner := f.user("owner")
	member, user := f.user("member")
	org := f.org(owner, "shared")
	other := f.org(user, "personal")
	f.join(owner, user, org, member.Email)
	providers := rpc.NewProviderServiceClient(user, f.url)
	keys := rpc.NewKeyServiceClient(user, f.url)
	for _, id := range []string{org, other} {
		_, e := providers.CreateProviderConnection(f.ctx, connect.NewRequest(&v1.CreateProviderConnectionRequest{OrganizationId: id, ProviderId: "custom", Name: "private", BaseUrl: "https://provider.example.test", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, ApiKey: ptr("fixture-secret")}))
		acceptanceOK(t, e)
	}
	key, e := keys.CreateKey(f.ctx, connect.NewRequest(&v1.CreateKeyRequest{OrganizationId: org, Name: "shared key"}))
	acceptanceOK(t, e)
	otherKey, e := keys.CreateKey(f.ctx, connect.NewRequest(&v1.CreateKeyRequest{OrganizationId: other, Name: "other key"}))
	acceptanceOK(t, e)
	// An ordinary member cannot use explicit user filters to inspect another member's metadata.
	_, e = rpc.NewUsageServiceClient(user, f.url).ListUsage(f.ctx, connect.NewRequest(&v1.ListUsageRequest{OrganizationId: org, UserId: &ownerUser.ID}))
	acceptanceCode(t, e, connect.CodePermissionDenied)
	_, e = rpc.NewUsageServiceClient(user, f.url).ListUsage(f.ctx, connect.NewRequest(&v1.ListUsageRequest{OrganizationId: org, UserId: ptr("")}))
	acceptanceCode(t, e, connect.CodePermissionDenied)
	_, e = rpc.NewAuditServiceClient(user, f.url).ListOrganizationAudit(f.ctx, connect.NewRequest(&v1.ListOrganizationAuditRequest{OrganizationId: org}))
	acceptanceCode(t, e, connect.CodePermissionDenied)
	_, e = rpc.NewMembershipServiceClient(owner, f.url).RemoveMember(f.ctx, connect.NewRequest(&v1.RemoveMemberRequest{OrganizationId: org, UserId: member.ID}))
	acceptanceOK(t, e)
	_, e = keys.CreateKey(f.ctx, connect.NewRequest(&v1.CreateKeyRequest{OrganizationId: org, Name: "stale page"}))
	acceptanceCode(t, e, connect.CodeNotFound)
	_, e = rpc.NewClientServiceClient(&http.Client{Transport: sessionTransport{bearer: key.Msg.RawKey}}, f.url).ListClientModels(f.ctx, connect.NewRequest(&v1.ListClientModelsRequest{}))
	acceptanceCode(t, e, connect.CodeUnauthenticated)
	_, e = f.s.AuthenticateKey(f.ctx, otherKey.Msg.RawKey)
	acceptanceOK(t, e)
	remaining, e := providers.ListProviderConnections(f.ctx, connect.NewRequest(&v1.ListProviderConnectionsRequest{OrganizationId: other}))
	acceptanceOK(t, e)
	if len(remaining.Msg.Connections) != 1 {
		t.Fatal("removal affected other organization's provider")
	}
	// The same OIDC identity can rejoin after its verified email changes.
	// Change only the verified identity fixture to avoid waiting for the one-minute resend limit.
	_, e = f.s.RegisterIdentity(f.ctx, member.Issuer, member.Subject, "rejoin@example.test", true)
	acceptanceOK(t, e)
	f.join(owner, user, org, "rejoin@example.test")
	remaining, e = providers.ListProviderConnections(f.ctx, connect.NewRequest(&v1.ListProviderConnectionsRequest{OrganizationId: org}))
	acceptanceOK(t, e)
	if len(remaining.Msg.Connections) != 0 {
		t.Fatal("rejoin restored deleted private configuration")
	}
	_, e = rpc.NewClientServiceClient(&http.Client{Transport: sessionTransport{bearer: key.Msg.RawKey}}, f.url).ListClientModels(f.ctx, connect.NewRequest(&v1.ListClientModelsRequest{}))
	acceptanceCode(t, e, connect.CodeUnauthenticated)
}

func TestAcceptanceConcurrentDeviceApprovalIssuesOneKey(t *testing.T) {
	f := newAcceptance(t)
	_, owner := f.user("owner")
	org := f.org(owner, "concurrent approval")
	device := rpc.NewDeviceAuthorizationServiceClient(http.DefaultClient, f.url)
	started, e := device.BeginDeviceAuthorization(f.ctx, connect.NewRequest(&v1.BeginDeviceAuthorizationRequest{DeviceName: "shared approval page"}))
	acceptanceOK(t, e)
	approval := rpc.NewDeviceApprovalServiceClient(owner, f.url)
	results := make(chan error, 8)
	start := make(chan struct{})
	for range 8 {
		go func() {
			<-start
			_, e := approval.ApproveDeviceAuthorization(f.ctx, connect.NewRequest(&v1.ApproveDeviceAuthorizationRequest{OrganizationId: org, UserCode: started.Msg.UserCode}))
			results <- e
		}()
	}
	close(start)
	successes := 0
	for range 8 {
		if e := <-results; e == nil {
			successes++
		} else if connect.CodeOf(e) != connect.CodeNotFound && connect.CodeOf(e) != connect.CodeFailedPrecondition {
			t.Errorf("unexpected concurrent approval error: %v", e)
		}
	}
	if successes != 1 {
		t.Fatalf("successful approvals = %d; want 1", successes)
	}
	keys, e := rpc.NewKeyServiceClient(owner, f.url).ListKeys(f.ctx, connect.NewRequest(&v1.ListKeysRequest{OrganizationId: org}))
	acceptanceOK(t, e)
	if len(keys.Msg.Keys) != 1 {
		t.Fatal("concurrent approvals issued multiple keys")
	}
	poll, e := device.PollDeviceAuthorization(f.ctx, connect.NewRequest(&v1.PollDeviceAuthorizationRequest{AuthorizationId: started.Msg.AuthorizationId, DeviceSecret: started.Msg.DeviceSecret}))
	acceptanceOK(t, e)
	if poll.Msg.Result == nil || poll.Msg.Result.KeyId != keys.Msg.Keys[0].Id {
		t.Fatal("handoff does not match the only issued key")
	}
}
