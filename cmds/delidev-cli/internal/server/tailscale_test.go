// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/desktopruntime"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/tailscale"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"net/http"
	"path/filepath"
	"testing"
)

type tailnetFixture struct {
	target string
	done   chan struct{}
}

func (f *tailnetFixture) Read(_ context.Context, args ...string) ([]byte, error) {
	if args[0] == "status" {
		return []byte(`{"BackendState":"Running","Self":{"ID":"fixture-self","HostName":"fixture-self","DNSName":"target.fixture.ts.net.","UserID":1,"Online":true},"CertDomains":["target.fixture.ts.net"],"Peer":{}}`), nil
	}
	if f.target == "" {
		return []byte(`{}`), nil
	}
	return json.Marshal(map[string]any{"Foreground": map[string]any{"fixture-session": map[string]any{"TCP": map[string]any{"8443": map[string]any{"HTTPS": true}}, "Web": map[string]any{"target.fixture.ts.net:8443": map[string]any{"Handlers": map[string]any{"/": map[string]any{"Proxy": f.target}}}}}}})
}
func (f *tailnetFixture) Start(_ context.Context, _ domain.ID, target string) (tailscale.Child, error) {
	f.target = target
	f.done = make(chan struct{})
	return f, nil
}
func (f *tailnetFixture) Done() <-chan struct{} { return f.done }
func (f *tailnetFixture) Close() error {
	if f.done != nil {
		select {
		case <-f.done:
		default:
			close(f.done)
		}
	}
	return nil
}
func tailscaleServerFixture(t *testing.T) (*oauthFixture, context.Context) {
	t.Helper()
	f := newOAuthFixture(t)
	native := &tailnetFixture{}
	access := &tailscale.AccessManager{Root: f.s.Store.Root(), ServerID: f.s.Identity.ServerID, Runner: native, Launcher: native, Handler: http.NotFoundHandler()}
	origin := "https://target.fixture.ts.net:8443"
	if _, err := access.Set(context.Background(), domain.NewID(), true, f.s.Identity.ServerID, origin); err != nil {
		t.Fatal(err)
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.s.tailscale = &tailscaleController{access: access, runner: native, offer: key.PublicKey().Bytes(), approvals: &tailscale.Approvals{Root: f.s.Store.Root(), ServerID: f.s.Identity.ServerID, Origin: origin}}
	t.Cleanup(func() { _ = access.Close() })
	return f, context.WithValue(context.Background(), tailscaleIngressKey{}, true)
}
func TestTailscaleLocalAuthorityAndPrivateProofFence(t *testing.T) {
	f, tailnet := tailscaleServerFixture(t)
	for _, ctx := range []context.Context{f.ctx, tailnet, desktopruntime.WithTarget(f.ctx, &desktopruntime.Target{ServerID: domain.NewID()}), desktopruntime.WithTarget(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: domain.NewID()}), &desktopruntime.Target{ServerID: f.s.Identity.ServerID})} {
		if _, err := f.s.ReadTailscaleDevices(ctx, connect.NewRequest(&pb.ReadTailscaleDevicesRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("foreign/main-unproven discovery admitted", err)
		}
	}
	local := desktopruntime.WithTarget(f.ctx, &desktopruntime.Target{ServerID: f.s.Identity.ServerID})
	if _, err := f.s.ReadTailscaleDevices(local, connect.NewRequest(&pb.ReadTailscaleDevicesRequest{})); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.GetTailscaleStatus(f.ctx, connect.NewRequest(&pb.GetTailscaleStatusRequest{})); err == nil {
		t.Fatal("anonymous capability leaked to unrelated ingress")
	}
	status, err := f.s.GetTailscaleStatus(tailnet, connect.NewRequest(&pb.GetTailscaleStatusRequest{}))
	if err != nil || !status.Msg.Accepting || status.Msg.ServerId != string(f.s.Identity.ServerID) {
		t.Fatal("original ingress unavailable", err)
	}
}
func TestTailscaleApprovalEncryptsOriginalClientGrantAndReplays(t *testing.T) {
	f, tailnet := tailscaleServerFixture(t)
	requester, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	request := &pb.RequestTailscaleConnectionRequest{RequestId: string(domain.NewID()), RequesterName: "fixture requester", RequesterKey: requester.PublicKey().Bytes(), ServerId: string(f.s.Identity.ServerID), Origin: "https://target.fixture.ts.net:8443", Role: pb.DeviceType_DEVICE_TYPE_CLIENT, ObservedTargetKey: f.s.tailscale.offer}
	pending, err := f.s.RequestTailscaleConnection(tailnet, connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	decision := &pb.DecideTailscaleConnectionRequest{RequestId: request.RequestId, DecisionId: string(domain.NewID()), ConfirmationCode: pending.Msg.Connection.ConfirmationCode, Allow: true}
	if _, err = f.s.DecideTailscaleConnection(tailnet, connect.NewRequest(decision)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("anonymous requester approved itself", err)
	}
	approved, err := f.s.DecideTailscaleConnection(f.ctx, connect.NewRequest(decision))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.s.DecideTailscaleConnection(f.ctx, connect.NewRequest(decision))
	if err != nil || string(approved.Msg.Connection.EncryptedGrant) != string(replay.Msg.Connection.EncryptedGrant) {
		t.Fatal("original grant was replaced", err)
	}
	raw, err := tailscale.OpenGrant(requester.Bytes(), outgoingApproval(approved.Msg.Connection, request))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	var grant worker.PairingCode
	if domain.Decode(raw, &grant) != nil || grant.Validate() != nil || grant.ServerID != f.s.Identity.ServerID || grant.Endpoint != request.Origin {
		t.Fatal("grant did not name the original server")
	}
	token, err := worker.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(token))
	consume := &pb.PairDeviceRequest{RequestId: string(domain.NewID()), PairingId: string(grant.PairingID), Code: grant.Code, DeviceId: string(domain.NewID()), CredentialDigest: digest[:]}
	paired, err := f.s.PairDevice(context.Background(), connect.NewRequest(consume))
	if err != nil {
		t.Fatal("encrypted grant could not authorize its original client", err)
	}
	same, err := f.s.PairDevice(context.Background(), connect.NewRequest(consume))
	if err != nil || same.Msg.Device.Id != paired.Msg.Device.Id {
		t.Fatal("original client receipt did not replay", err)
	}
	replacement := *consume
	replacement.RequestId = string(domain.NewID())
	replacement.DeviceId = string(domain.NewID())
	if _, err = f.s.PairDevice(context.Background(), connect.NewRequest(&replacement)); err == nil {
		t.Fatal("single-use grant admitted a replacement client")
	}
	changed := *request
	changed.ObservedTargetKey = make([]byte, 32)
	if _, err = f.s.RequestTailscaleConnection(tailnet, connect.NewRequest(&changed)); err == nil {
		t.Fatal("changed original target offer accepted")
	}
}
func TestTailscaleSSHOriginMismatchDoesNotStageCredentials(t *testing.T) {
	f, _ := tailscaleServerFixture(t)
	record := sshObserved(t, f)
	var operation sshOperation
	_ = domain.Decode(record.Data, &operation)
	f.s.releaseFactory = func() (releaseClient, error) { return fixtureRelease{}, nil }
	credential, _ := json.Marshal(map[string]any{"method": "password", "secret": []byte("fixture-password")})
	request := &pb.StartSSHSetupRequest{Mutation: &pb.Mutation{Id: string(record.ID), ExpectedRevision: record.Revision, RequestId: string(domain.NewID())}, Credential: credential, Name: "fixture worker", ConfirmedFingerprint: operation.Identity.Fingerprint, ServerOrigin: "https://replacement.fixture.ts.net:8443"}
	if _, err := f.s.StartSSHSetup(f.ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("changed SSH server origin accepted", err)
	}
	if len(f.vault.values) != 0 {
		t.Fatal("credential staged before original ingress validation")
	}
}

func TestTailscaleWorkerApprovalDoesNotPairTargetToItself(t *testing.T) {
	f, tailnet := tailscaleServerFixture(t)
	requester, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	selected := domain.NewID()
	request := &pb.RequestTailscaleConnectionRequest{RequestId: string(domain.NewID()), RequesterName: "fixture requester", RequesterKey: requester.PublicKey().Bytes(), ServerId: string(f.s.Identity.ServerID), Origin: "https://target.fixture.ts.net:8443", Role: pb.DeviceType_DEVICE_TYPE_WORKER, ObservedTargetKey: f.s.tailscale.offer, WorkerServerId: string(selected), WorkerServerOrigin: "https://selected.fixture.ts.net:8443"}
	pending, err := f.s.RequestTailscaleConnection(tailnet, connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	approved, err := f.s.DecideTailscaleConnection(f.ctx, connect.NewRequest(&pb.DecideTailscaleConnectionRequest{RequestId: request.RequestId, DecisionId: string(domain.NewID()), ConfirmationCode: pending.Msg.Connection.ConfirmationCode, Allow: true}))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := tailscale.OpenGrant(requester.Bytes(), outgoingApproval(approved.Msg.Connection, request))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	var ticket struct {
		RequestID domain.ID `json:"request_id"`
		Token     string    `json:"token"`
	}
	if domain.Decode(raw, &ticket) != nil || ticket.RequestID != domain.ID(request.RequestId) || ticket.Token == "" {
		t.Fatal("missing selected-target approval ticket")
	}
	grants, err := f.s.Store.List(f.ctx, store.Filter{Kind: domain.PairingKind, Limit: 100})
	if err != nil || len(grants) != 0 {
		t.Fatal("target approval created a grant for its own server", err)
	}
}

func TestTailscaleWorkerScopeCredentialAndStartupFence(t *testing.T) {
	f, _ := tailscaleServerFixture(t)
	input := tailscale.ApprovalInput{ID: domain.NewID(), WorkerServerID: domain.NewID(), WorkerServerOrigin: "https://selected.fixture.ts.net:8443"}
	scope := filepath.Join(f.s.Store.Root(), "tailscale-workers", string(input.ID), "worker")
	if err := security.PrivateDir(scope); err != nil {
		t.Fatal(err)
	}
	token, err := worker.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	credential := worker.Credential{Version: 1, Type: domain.WorkerDevice, ServerID: input.WorkerServerID, Endpoint: input.WorkerServerOrigin, DeviceID: domain.NewID(), MachineID: domain.NewID(), PairingID: domain.NewID(), Token: token}
	raw, _ := json.Marshal(credential)
	if err = security.WriteAtomic(filepath.Join(scope, "device.json"), raw); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.s.ownedTailscaleWorker(input); err == nil {
		t.Fatal("lost startup fence accepted")
	}
	marker, _ := json.Marshal(struct {
		Device domain.ID `json:"device"`
	}{credential.DeviceID})
	if err = security.WriteAtomic(filepath.Join(filepath.Dir(scope), "start-request.json"), marker); err != nil {
		t.Fatal(err)
	}
	got, status, err := f.s.ownedTailscaleWorker(input)
	if err != nil || got != scope || status.State != worker.StateIdle {
		t.Fatal("original credential unavailable", err)
	}
	input.WorkerServerID = domain.NewID()
	if _, _, err = f.s.ownedTailscaleWorker(input); err == nil {
		t.Fatal("foreign server credential accepted")
	}
	local := desktopruntime.WithTarget(f.ctx, &desktopruntime.Target{ServerID: f.s.Identity.ServerID})
	if _, err = f.s.GetTailscaleWorker(local, connect.NewRequest(&pb.GetTailscaleWorkerRequest{ApprovalRequestId: string(input.ID)})); err == nil {
		t.Fatal("unapproved scope admitted")
	}
	if _, err = f.s.GetTailscaleWorker(f.ctx, connect.NewRequest(&pb.GetTailscaleWorkerRequest{ApprovalRequestId: string(input.ID)})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("main proof bypass", err)
	}
}
