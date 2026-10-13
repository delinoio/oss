// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/tailscale"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// tailnetClock is a shared fake clock for the fixture services.
type tailnetClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *tailnetClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *tailnetClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// tailnetPeerFixture is one DeliDev server with a Ready (fixture) Tailscale
// manager. Peers reach each other through in-process transports.
type tailnetPeerFixture struct {
	s      *Service
	root   string
	origin string
	owner  context.Context
}

func newTailnetPeer(t *testing.T, name string, clock *tailnetClock) *tailnetPeerFixture {
	t.Helper()
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	root := filepath.Join(t.TempDir(), "state")
	db, err := store.Open(owner, root)
	if err != nil {
		t.Fatal(err)
	}
	origin := "https://" + name + ".tailnet.ts.net:8443"
	identity := security.Identity{ServerID: domain.NewID(), Token: "temporary-tailnet-test-owner"}
	s := &Service{Store: db, Identity: identity, logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), tailscale: tailscale.NewReadyManagerForTest(identity.ServerID, origin), tailnetNow: clock.Now}
	t.Cleanup(func() { db.Close() })
	return &tailnetPeerFixture{s: s, root: root, origin: origin, owner: owner}
}

// restart models a process restart: a new Service over the same durable state.
func (p *tailnetPeerFixture) restart(clock *tailnetClock) {
	old := p.s
	p.s = &Service{Store: old.Store, Identity: old.Identity, logger: old.logger, tailscale: old.tailscale, tailnetNow: clock.Now, tailnetStartWorker: old.tailnetStartWorker, tailnetPair: old.tailnetPair, tailnetHTTP: old.tailnetHTTP}
}

// route serves the target's real ingress handler for any outbound request. The
// optional fail hook may turn a call into an uncertain transport failure after
// the target already processed it.
type tailnetRoute struct {
	target   *tailnetPeerFixture
	remote   string
	failNext atomic.Int32
	calls    atomic.Int32
}

func (r *tailnetRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls.Add(1)
	handler := r.target.s.tailnetIngress(r.target.origin)
	clone := req.Clone(req.Context())
	clone.RemoteAddr = r.remote
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, clone)
	if req.URL.Path == "/delidev.v1.TailscaleService/DeliverTailscaleWorker" && r.failNext.Load() > 0 {
		r.failNext.Add(-1)
		return nil, errors.New("uncertain transport failure after target processed the request")
	}
	return recorder.Result(), nil
}

func (p *tailnetPeerFixture) useRoute(r *tailnetRoute) {
	p.s.tailnetHTTP = func() (*http.Client, *http.Transport) {
		return &http.Client{Transport: r}, &http.Transport{}
	}
}

func tailnetCode(t *testing.T, target *tailnetPeerFixture, id domain.ID) string {
	t.Helper()
	v, err := target.s.readTailnet(id)
	if err != nil {
		t.Fatal(err)
	}
	return v.Approval.Code
}

// newOutgoingWorker writes the requester's original outgoing Worker journal,
// bypassing native peer discovery that needs an installed Tailscale.
func newOutgoingWorker(t *testing.T, requester, target *tailnetPeerFixture) tailnetOutgoing {
	t.Helper()
	if err := security.PrivateDir(filepath.Join(requester.s.tailnetRoot(), "outgoing")); err != nil {
		t.Fatal(err)
	}
	private, public, err := tailscale.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := domain.PrincipalFrom(requester.owner)
	v := tailnetOutgoing{Version: 1, ID: domain.NewID(), PeerID: "peer", Name: "worker one", Role: pb.DeviceType_DEVICE_TYPE_WORKER, Actor: actor, Origin: target.origin, ServerID: target.s.Identity.ServerID, PrivateKey: private, PublicKey: public, ProfileID: domain.NewID(), GrantRequest: domain.NewID()}
	if err := requester.s.writeOutgoing(v); err != nil {
		t.Fatal(err)
	}
	return v
}

type tailnetWorkerPair struct {
	clock     *tailnetClock
	requester *tailnetPeerFixture
	target    *tailnetPeerFixture
	route     *tailnetRoute
	outgoing  tailnetOutgoing
	pairCalls atomic.Int32
	started   atomic.Int32
	pairFail  atomic.Int32
}

func newTailnetWorkerPair(t *testing.T) *tailnetWorkerPair {
	t.Helper()
	f := &tailnetWorkerPair{clock: &tailnetClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}}
	f.requester = newTailnetPeer(t, "requester", f.clock)
	f.target = newTailnetPeer(t, "target", f.clock)
	f.route = &tailnetRoute{target: f.target, remote: "100.64.0.9:41000"}
	f.requester.useRoute(f.route)
	f.target.s.tailnetPair = func(ctx context.Context, root string, grant worker.PairingCode, kind domain.DeviceType, name string) (worker.Credential, error) {
		f.pairCalls.Add(1)
		if f.pairFail.Load() > 0 {
			f.pairFail.Add(-1)
			return worker.Credential{}, errors.New("uncertain pairing response")
		}
		return worker.Credential{MachineID: domain.NewID()}, nil
	}
	f.target.s.tailnetStartWorker = func(context.Context, string) error { f.started.Add(1); return nil }
	f.outgoing = newOutgoingWorker(t, f.requester, f.target)
	return f
}

func (f *tailnetWorkerPair) resume() (tailnetOutgoing, error) {
	_, err := f.requester.s.ResumeTailscaleConnection(f.requester.owner, connect.NewRequest(&pb.ResumeTailscaleConnectionRequest{RequestId: string(f.outgoing.ID)}))
	if err != nil {
		return tailnetOutgoing{}, err
	}
	return f.requester.s.readOutgoing(f.outgoing.ID)
}

func (f *tailnetWorkerPair) decide(allow bool) error {
	_, err := f.target.s.DecideTailscaleApproval(f.target.owner, connect.NewRequest(&pb.DecideTailscaleApprovalRequest{RequestId: string(f.outgoing.ID), ConfirmationCode: tailnetCodeOf(f), Allow: allow}))
	return err
}
func tailnetCodeOf(f *tailnetWorkerPair) string {
	v, err := f.target.s.readTailnet(f.outgoing.ID)
	if err != nil {
		return ""
	}
	return v.Approval.Code
}

func assertCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %v, got success", want)
	}
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("expected %v, got %v: %v", want, got, err)
	}
}

func TestTailnetWorkerApprovalIsRecoverableAfterRequestExpiry(t *testing.T) {
	f := newTailnetWorkerPair(t)
	pending, err := f.resume()
	if err != nil {
		t.Fatal(err)
	}
	if pending.Approval == nil || pending.Approval.State != tailscale.Pending || pending.Grant != nil {
		t.Fatalf("expected a Pending approval without a grant: %#v", pending.Approval)
	}
	if err := f.decide(true); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(tailnetRequestLifetime + time.Hour)
	done, err := f.resume()
	if err != nil {
		t.Fatalf("approved Worker setup must recover after request expiry: %v", err)
	}
	if !done.Complete || done.Grant == nil || done.GrantRequest != f.outgoing.GrantRequest {
		t.Fatalf("original request did not complete with its original grant request: %#v", done)
	}
	record, err := f.target.s.readTailnet(f.outgoing.ID)
	if err != nil || record.Approval.State != tailscale.Completed || len(record.IncomingGrant) == 0 {
		t.Fatalf("target did not accept first delivery after expiry: %v %#v", err, record.Approval)
	}
	if f.pairCalls.Load() != 1 || f.started.Load() != 1 {
		t.Fatalf("expected exactly one worker pairing and start, got %d and %d", f.pairCalls.Load(), f.started.Load())
	}
	// A repeated resume neither creates a new grant nor registers another Worker.
	again, err := f.resume()
	if err != nil || !again.Complete || again.Grant.Code != done.Grant.Code {
		t.Fatalf("repeated resume changed completed state: %v", err)
	}
	if f.pairCalls.Load() != 1 || f.started.Load() != 1 {
		t.Fatal("repeated resume registered another Worker")
	}
}

func TestTailnetWorkerDeliveryRetryAfterUncertainTransportIsExactlyOnce(t *testing.T) {
	f := newTailnetWorkerPair(t)
	if _, err := f.resume(); err != nil {
		t.Fatal(err)
	}
	if err := f.decide(true); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(tailnetRequestLifetime + time.Minute)
	f.route.failNext.Store(1)
	if _, err := f.resume(); err == nil {
		t.Fatal("expected the dropped delivery response to be reported as uncertain")
	}
	first, err := f.requester.s.readOutgoing(f.outgoing.ID)
	if err != nil || first.Grant == nil || len(first.EncryptedGrant) == 0 || first.Complete {
		t.Fatalf("the original grant must be retained for an exact retry: %v", err)
	}
	// The target accepted and completed the first delivery; a retry replays the
	// same bytes without a second pairing or Worker start.
	retried, err := f.resume()
	if err != nil || !retried.Complete {
		t.Fatalf("retry did not complete: %v", err)
	}
	if retried.Grant.Code != first.Grant.Code || !bytes.Equal(retried.EncryptedGrant, first.EncryptedGrant) {
		t.Fatal("retry replaced the original grant")
	}
	if f.pairCalls.Load() != 1 || f.started.Load() != 1 {
		t.Fatalf("expected one pairing and one start, got %d and %d", f.pairCalls.Load(), f.started.Load())
	}
}

func TestTailnetWorkerDeliveryResumesPreparedJournalAfterPairingFailure(t *testing.T) {
	f := newTailnetWorkerPair(t)
	if _, err := f.resume(); err != nil {
		t.Fatal(err)
	}
	if err := f.decide(true); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(tailnetRequestLifetime + time.Minute)
	f.target.s.tailnetPair = func(ctx context.Context, root string, grant worker.PairingCode, kind domain.DeviceType, name string) (worker.Credential, error) {
		n := f.pairCalls.Add(1)
		if n == 1 {
			return worker.Credential{}, errors.New("uncertain pairing response")
		}
		return worker.Credential{MachineID: domain.NewID()}, nil
	}
	if _, err := f.resume(); err == nil {
		t.Fatal("expected the first delivery to report the uncertain pairing")
	}
	record, err := f.target.s.readTailnet(f.outgoing.ID)
	if err != nil || len(record.IncomingGrant) == 0 || record.Approval.State != tailscale.Approved {
		t.Fatalf("the first post-expiry delivery must be journaled before pairing: %v", err)
	}
	// Even a delayed retry past the recovery window replays the journaled grant.
	f.clock.Advance(2 * tailnetRecoveryWindow)
	if _, err := f.target.s.DeliverTailscaleWorker(context.Background(), connect.NewRequest(&pb.DeliverTailscaleWorkerRequest{RequestId: string(f.outgoing.ID), RequesterKey: f.outgoing.PublicKey, EncryptedGrant: []byte("different")})); err == nil {
		t.Fatal("a different delivery body must be rejected after the original was journaled")
	} else {
		assertCode(t, err, connect.CodeAborted)
	}
}

func TestTailnetWorkerApprovalAfterExpiryStaysImpossible(t *testing.T) {
	f := newTailnetWorkerPair(t)
	if _, err := f.resume(); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(tailnetRequestLifetime + time.Second)
	if err := f.decide(true); err != nil {
		t.Fatal(err)
	}
	record, err := f.target.s.readTailnet(f.outgoing.ID)
	if err != nil || record.Approval.State == tailscale.Approved || record.Allow != nil {
		t.Fatalf("approval first made after expiry must not be recorded: %v %#v", err, record.Approval.State)
	}
	got, err := f.resume()
	if err != nil || got.Grant != nil || got.Approval.State != tailscale.Expired {
		t.Fatalf("expired request must stay expired without a grant: %v %#v", err, got.Approval)
	}
}

func TestTailnetWorkerRecoveryIsBoundedByWindow(t *testing.T) {
	f := newTailnetWorkerPair(t)
	if _, err := f.resume(); err != nil {
		t.Fatal(err)
	}
	if err := f.decide(true); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(tailnetRequestLifetime + tailnetRecoveryWindow + time.Minute)
	_, err := f.resume()
	assertCode(t, err, connect.CodeAborted)
	left, readErr := f.requester.s.readOutgoing(f.outgoing.ID)
	if readErr != nil || left.Grant != nil || len(left.EncryptedGrant) != 0 {
		t.Fatalf("no grant may be created beyond the window: %v", readErr)
	}
	// The target also refuses a first delivery beyond the window.
	_, err = f.target.s.DeliverTailscaleWorker(context.Background(), connect.NewRequest(&pb.DeliverTailscaleWorkerRequest{RequestId: string(f.outgoing.ID), RequesterKey: f.outgoing.PublicKey, EncryptedGrant: []byte("late")}))
	assertCode(t, err, connect.CodeAborted)
	if f.pairCalls.Load() != 0 {
		t.Fatal("no Worker may be paired beyond the recovery window")
	}
}

func TestTailnetWorkerDeliveryRejectsMismatchedIdentity(t *testing.T) {
	f := newTailnetWorkerPair(t)
	if _, err := f.resume(); err != nil {
		t.Fatal(err)
	}
	if err := f.decide(true); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(tailnetRequestLifetime + time.Minute)
	_, other, _ := tailscale.NewKey()
	_, err := f.target.s.DeliverTailscaleWorker(context.Background(), connect.NewRequest(&pb.DeliverTailscaleWorkerRequest{RequestId: string(f.outgoing.ID), RequesterKey: other, EncryptedGrant: []byte("x")}))
	assertCode(t, err, connect.CodePermissionDenied)
	_, err = f.target.s.DeliverTailscaleWorker(context.Background(), connect.NewRequest(&pb.DeliverTailscaleWorkerRequest{RequestId: string(domain.NewID()), RequesterKey: f.outgoing.PublicKey, EncryptedGrant: []byte("x")}))
	assertCode(t, err, connect.CodePermissionDenied)
	// A mismatched request key is also refused when asking for the original request.
	_, err = f.target.s.RequestTailscaleApproval(context.Background(), connect.NewRequest(&pb.RequestTailscaleApprovalRequest{RequestId: string(f.outgoing.ID), ServerId: string(f.target.s.Identity.ServerID), Origin: f.target.origin, Role: pb.DeviceType_DEVICE_TYPE_WORKER, RequesterKey: other, Name: f.outgoing.Name, SelectedServerId: string(f.requester.s.Identity.ServerID), SelectedOrigin: f.requester.origin}))
	assertCode(t, err, connect.CodeAborted)
	if f.pairCalls.Load() != 0 {
		t.Fatal("rejected deliveries must not pair a Worker")
	}
}

func tailnetRequest(t *testing.T, target *tailnetPeerFixture, id domain.ID, host string) error {
	t.Helper()
	_, public, err := tailscale.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), tailnetHostKey{}, tailnetHostDigest(host+":1000"))
	_, err = target.s.RequestTailscaleApproval(ctx, connect.NewRequest(&pb.RequestTailscaleApprovalRequest{RequestId: string(id), ServerId: string(target.s.Identity.ServerID), Origin: target.origin, Role: pb.DeviceType_DEVICE_TYPE_CLIENT, RequesterKey: public, Name: "peer"}))
	return err
}

func approvalFiles(t *testing.T, target *tailnetPeerFixture) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(target.s.tailnetRoot(), "approvals"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestTailnetHostCannotConsumeEveryApprovalSlot(t *testing.T) {
	clock := &tailnetClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	target := newTailnetPeer(t, "target", clock)
	for i := 0; i < tailnetHostPendingLimit; i++ {
		if err := tailnetRequest(t, target, domain.NewID(), "100.64.0.1"); err != nil {
			t.Fatal(err)
		}
	}
	assertCode(t, tailnetRequest(t, target, domain.NewID(), "100.64.0.1"), connect.CodeResourceExhausted)
	if err := tailnetRequest(t, target, domain.NewID(), "100.64.0.2"); err != nil {
		t.Fatalf("another host must still be admitted: %v", err)
	}
	// Once the first host's requests expire they no longer count as live.
	clock.Advance(tailnetRequestLifetime + time.Second)
	if err := tailnetRequest(t, target, domain.NewID(), "100.64.0.1"); err != nil {
		t.Fatalf("expired requests must stop counting against the host: %v", err)
	}
}

func TestTailnetReplayOfRetainedRequestBypassesAdmission(t *testing.T) {
	clock := &tailnetClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	target := newTailnetPeer(t, "target", clock)
	var first domain.ID
	_, public, _ := tailscale.NewKey()
	for i := 0; i < tailnetHostPendingLimit; i++ {
		id := domain.NewID()
		if first == "" {
			first = id
		}
		ctx := context.WithValue(context.Background(), tailnetHostKey{}, tailnetHostDigest("100.64.0.1:1"))
		if _, err := target.s.RequestTailscaleApproval(ctx, connect.NewRequest(&pb.RequestTailscaleApprovalRequest{RequestId: string(id), ServerId: string(target.s.Identity.ServerID), Origin: target.origin, Role: pb.DeviceType_DEVICE_TYPE_CLIENT, RequesterKey: public, Name: "peer"})); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.WithValue(context.Background(), tailnetHostKey{}, tailnetHostDigest("100.64.0.1:1"))
	if _, err := target.s.RequestTailscaleApproval(ctx, connect.NewRequest(&pb.RequestTailscaleApprovalRequest{RequestId: string(first), ServerId: string(target.s.Identity.ServerID), Origin: target.origin, Role: pb.DeviceType_DEVICE_TYPE_CLIENT, RequesterKey: public, Name: "peer"})); err != nil {
		t.Fatalf("exact replay of a retained request must not be refused: %v", err)
	}
}

func TestTailnetIngressAttributesRequestsToRemoteHost(t *testing.T) {
	f := newTailnetWorkerPair(t)
	if _, err := f.resume(); err != nil {
		t.Fatal(err)
	}
	record, err := f.target.s.readTailnet(f.outgoing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.RequesterHost == "" || record.RequesterHost != tailnetHostDigest(f.route.remote) {
		t.Fatalf("ingress must persist the requester host digest, got %q", record.RequesterHost)
	}
}

func TestTailnetFullDirectoryReclaimsAfterRecoveryWindowAndRestart(t *testing.T) {
	clock := &tailnetClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	target := newTailnetPeer(t, "target", clock)
	// 256 unauthenticated requests from distinct hosts that are never approved.
	for i := 0; i < tailnetApprovalLimit; i++ {
		if err := tailnetRequest(t, target, domain.NewID(), fmt.Sprintf("100.64.%d.%d", i/200, i%200+1)); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	assertCode(t, tailnetRequest(t, target, domain.NewID(), "100.100.0.1"), connect.CodeResourceExhausted)
	// Expired but inside the recovery window: still retained.
	clock.Advance(tailnetRequestLifetime + time.Hour)
	assertCode(t, tailnetRequest(t, target, domain.NewID(), "100.100.0.1"), connect.CodeResourceExhausted)
	if n := len(approvalFiles(t, target)); n != tailnetApprovalLimit {
		t.Fatalf("records inside the recovery window must be retained, got %d", n)
	}
	clock.Advance(tailnetRecoveryWindow)
	target.restart(clock)
	if err := tailnetRequest(t, target, domain.NewID(), "100.100.0.1"); err != nil {
		t.Fatalf("a legitimate request must be admitted once capacity is reclaimable after restart: %v", err)
	}
	if n := len(approvalFiles(t, target)); n != 1 {
		t.Fatalf("expected only the new record to remain, got %d", n)
	}
}

func writeTailnetFixtureRecord(t *testing.T, target *tailnetPeerFixture, mutate func(*tailnetRecord)) tailnetRecord {
	t.Helper()
	private, public, err := tailscale.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	_, requester, _ := tailscale.NewKey()
	v := tailnetRecord{PrivateKey: private, Approval: tailscale.Approval{ID: domain.NewID(), ServerID: target.s.Identity.ServerID, Origin: target.origin, Role: tailscale.ClientRole, Name: "peer", RequesterKey: requester, TargetKey: public, ExpiresAt: time.Date(2026, 1, 2, 3, 9, 5, 0, time.UTC), State: tailscale.Pending}}
	mutate(&v)
	v.Approval.Code = v.Approval.Confirmation()
	if err := security.PrivateDir(filepath.Join(target.s.tailnetRoot(), "approvals")); err != nil {
		t.Fatal(err)
	}
	if err := target.s.writeTailnet(v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTailnetReclamationKeepsActiveRecordsAndForeignFiles(t *testing.T) {
	clock := &tailnetClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	target := newTailnetPeer(t, "target", clock)
	yes := true
	grant := &worker.PairingCode{Version: 1}
	type fixture struct {
		name   string
		mutate func(*tailnetRecord)
		keep   bool
	}
	cases := []fixture{
		{"pending-unapproved", func(v *tailnetRecord) {}, false},
		{"denied", func(v *tailnetRecord) { v.Approval.State = tailscale.Denied }, false},
		{"expired", func(v *tailnetRecord) { v.Approval.State = tailscale.Expired }, false},
		{"approved-worker-undelivered", func(v *tailnetRecord) {
			v.Approval.State = tailscale.Approved
			v.Approval.Role = tailscale.WorkerRole
			v.Approval.SelectedServer = domain.NewID()
			v.Approval.SelectedOrigin = "https://requester.tailnet.ts.net:8443"
			v.Allow = &yes
		}, false},
		{"approved-worker-delivered", func(v *tailnetRecord) {
			v.Approval.State = tailscale.Approved
			v.Approval.Role = tailscale.WorkerRole
			v.Approval.SelectedServer = domain.NewID()
			v.Approval.SelectedOrigin = "https://requester.tailnet.ts.net:8443"
			v.Allow = &yes
			v.IncomingGrant = []byte("original")
		}, true},
		{"completed-worker", func(v *tailnetRecord) {
			v.Approval.State = tailscale.Completed
			v.Approval.Role = tailscale.WorkerRole
			v.Approval.SelectedServer = domain.NewID()
			v.Approval.SelectedOrigin = "https://requester.tailnet.ts.net:8443"
			v.Allow = &yes
			v.IncomingGrant = []byte("original")
		}, true},
		{"approved-client-receipt", func(v *tailnetRecord) {
			v.Approval.State = tailscale.Approved
			v.Approval.EncryptedGrant = []byte("receipt")
			v.Allow = &yes
			v.Grant = grant
		}, true},
		{"pending-client-awaiting-grant", func(v *tailnetRecord) { v.Allow = &yes; v.Grant = grant }, true},
	}
	ids := map[string]domain.ID{}
	for _, c := range cases {
		ids[c.name] = writeTailnetFixtureRecord(t, target, c.mutate).Approval.ID
	}
	directory := filepath.Join(target.s.tailnetRoot(), "approvals")
	foreign := filepath.Join(directory, "notes.txt")
	malformed := filepath.Join(directory, string(domain.NewID())+".json")
	for _, p := range []string{foreign, malformed} {
		if err := os.WriteFile(p, []byte("not an approval"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outgoing := newOutgoingWorker(t, target, target)
	// Inside the window nothing is touched.
	clock.Advance(tailnetRequestLifetime + time.Hour)
	if got, err := target.s.reclaimTailnet(context.Background(), directory, clock.Now()); err != nil || got.Reclaimed != 0 {
		t.Fatalf("nothing may be reclaimed inside the recovery window: %v %d", err, got.Reclaimed)
	}
	clock.Advance(tailnetRecoveryWindow)
	got, err := target.s.reclaimTailnet(context.Background(), directory, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Reclaimed != 4 {
		t.Fatalf("expected 4 reclaimed records, got %d", got.Reclaimed)
	}
	for _, c := range cases {
		_, err := os.Stat(target.s.tailnetPath(ids[c.name]))
		if c.keep && err != nil {
			t.Errorf("%s must be retained: %v", c.name, err)
		}
		if !c.keep && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s must be reclaimed: %v", c.name, err)
		}
	}
	for _, p := range []string{foreign, malformed, target.s.outgoingPath(outgoing.ID)} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("unrelated state %s must never be deleted: %v", p, err)
		}
	}
}

func TestTailnetReclaimablePredicate(t *testing.T) {
	expiry := time.Date(2026, 1, 2, 3, 9, 5, 0, time.UTC)
	record := tailnetRecord{Approval: tailscale.Approval{ExpiresAt: expiry, State: tailscale.Denied}}
	if tailnetReclaimable(record, expiry.Add(tailnetRecoveryWindow)) {
		t.Fatal("the recovery window boundary itself is still recoverable")
	}
	if !tailnetReclaimable(record, expiry.Add(tailnetRecoveryWindow+time.Nanosecond)) {
		t.Fatal("a Denied record past the window must be reclaimable")
	}
	record.Approval.State = tailscale.Completed
	if tailnetReclaimable(record, expiry.Add(100*tailnetRecoveryWindow)) {
		t.Fatal("Completed receipts must never be reclaimable")
	}
}

func TestTailnetPeerAddressUsesServeForwardedPeer(t *testing.T) {
	cases := []struct {
		name      string
		remote    string
		forwarded []string
		want      string
	}{
		{"serve proxy with peer", "127.0.0.1:41000", []string{"100.64.0.7"}, "100.64.0.7:0"},
		{"serve proxy with ipv6 peer", "127.0.0.1:41000", []string{"fd7a:115c:a1e0::1"}, "[fd7a:115c:a1e0::1]:0"},
		{"mapped ipv4 peer is normalized", "127.0.0.1:41000", []string{"::ffff:100.64.0.7"}, "100.64.0.7:0"},
		{"padded value", "127.0.0.1:41000", []string{" 100.64.0.7 "}, "100.64.0.7:0"},
		{"ipv6 loopback proxy", "[::1]:41000", []string{"100.64.0.7"}, "100.64.0.7:0"},
		{"no header", "127.0.0.1:41000", nil, "127.0.0.1:41000"},
		{"chained header", "127.0.0.1:41000", []string{"100.64.0.7, 100.64.0.8"}, "127.0.0.1:41000"},
		{"repeated header", "127.0.0.1:41000", []string{"100.64.0.7", "100.64.0.8"}, "127.0.0.1:41000"},
		{"garbage header", "127.0.0.1:41000", []string{"not-an-ip"}, "127.0.0.1:41000"},
		{"zoned header", "127.0.0.1:41000", []string{"fe80::1%eth0"}, "127.0.0.1:41000"},
		{"unspecified header", "127.0.0.1:41000", []string{"0.0.0.0"}, "127.0.0.1:41000"},
		{"non-loopback connection ignores header", "100.64.0.9:41000", []string{"100.64.0.7"}, "100.64.0.9:41000"},
		{"unparseable remote", "bogus", []string{"100.64.0.7"}, "bogus"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.RemoteAddr = c.remote
		for _, v := range c.forwarded {
			r.Header.Add("X-Forwarded-For", v)
		}
		if got := tailnetPeerAddress(r); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// serveRoute models `tailscale serve`: every request reaches the ingress from the
// loopback proxy and carries the originating peer in X-Forwarded-For.
type serveRoute struct {
	target *tailnetPeerFixture
	peer   string
}

func (r *serveRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.RemoteAddr = "127.0.0.1:50000"
	clone.Header.Set("X-Forwarded-For", r.peer)
	recorder := httptest.NewRecorder()
	r.target.s.tailnetIngress(r.target.origin).ServeHTTP(recorder, clone)
	return recorder.Result(), nil
}

func serveRequest(t *testing.T, target *tailnetPeerFixture, peer string, id domain.ID) error {
	t.Helper()
	_, public, err := tailscale.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	client := delidevv1connect.NewTailscaleServiceClient(&http.Client{Transport: &serveRoute{target: target, peer: peer}}, target.origin)
	_, err = client.RequestTailscaleApproval(context.Background(), connect.NewRequest(&pb.RequestTailscaleApprovalRequest{RequestId: string(id), ServerId: string(target.s.Identity.ServerID), Origin: target.origin, Role: pb.DeviceType_DEVICE_TYPE_CLIENT, RequesterKey: public, Name: "peer"}))
	return err
}

func TestTailnetIngressBoundsApprovalsPerOriginatingPeerBehindServe(t *testing.T) {
	clock := &tailnetClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	target := newTailnetPeer(t, "target", clock)
	for i := 0; i < tailnetHostPendingLimit; i++ {
		if err := serveRequest(t, target, "100.64.0.1", domain.NewID()); err != nil {
			t.Fatal(err)
		}
	}
	assertCode(t, serveRequest(t, target, "100.64.0.1", domain.NewID()), connect.CodeResourceExhausted)
	if err := serveRequest(t, target, "100.64.0.2", domain.NewID()); err != nil {
		t.Fatalf("a different originating peer behind the same Serve proxy must still be admitted: %v", err)
	}
	var digests = map[string]int{}
	for _, name := range approvalFiles(t, target) {
		v, err := target.s.readTailnet(domain.ID(strings.TrimSuffix(name, ".json")))
		if err != nil {
			t.Fatal(err)
		}
		digests[v.RequesterHost]++
	}
	if len(digests) != 2 || digests[tailnetHostDigest("100.64.0.1:0")] != tailnetHostPendingLimit || digests[tailnetHostDigest("100.64.0.2:0")] != 1 {
		t.Fatalf("requests must be attributed to the forwarded peer, got %v", digests)
	}
}

func TestTailnetWorkerRetryAfterWindowCreatesNoPairingFromRetainedGrant(t *testing.T) {
	f := newTailnetWorkerPair(t)
	if _, err := f.resume(); err != nil {
		t.Fatal(err)
	}
	if err := f.decide(true); err != nil {
		t.Fatal(err)
	}
	// Model a stop after the original grant was persisted inside the window but
	// before CreatePairing or any journaled delivery.
	f.clock.Advance(tailnetRequestLifetime + time.Hour)
	journal, err := f.requester.s.readOutgoing(f.outgoing.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, err := worker.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	journal.Grant = &worker.PairingCode{Version: 1, ServerID: f.requester.s.Identity.ServerID, Endpoint: journal.Approval.SelectedOrigin, Code: code}
	if err := f.requester.s.writeOutgoing(journal); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(tailnetRecoveryWindow)
	_, err = f.resume()
	assertCode(t, err, connect.CodeAborted)
	left, err := f.requester.s.readOutgoing(f.outgoing.ID)
	if err != nil || len(left.EncryptedGrant) != 0 || left.Complete {
		t.Fatalf("no delivery may be journaled beyond the window: %v", err)
	}
	// The original CreatePairing request was never executed, so replaying its
	// exact input now creates the pairing for the first time.
	digest := sha256.Sum256([]byte(code))
	created, err := f.requester.s.CreatePairing(f.requester.owner, connect.NewRequest(&pb.CreatePairingRequest{RequestId: string(f.outgoing.GrantRequest), Name: f.outgoing.Name, Type: pb.DeviceType_DEVICE_TYPE_WORKER, CodeDigest: digest[:]}))
	if err != nil || created.Msg.Replayed {
		t.Fatalf("a pairing was created after the recovery window: %v", err)
	}
	if f.pairCalls.Load() != 0 || f.started.Load() != 0 {
		t.Fatal("no Worker may be paired beyond the recovery window")
	}
}

func TestTailnetWorkerRetainedGrantStillResumesInsideWindow(t *testing.T) {
	f := newTailnetWorkerPair(t)
	if _, err := f.resume(); err != nil {
		t.Fatal(err)
	}
	if err := f.decide(true); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(tailnetRequestLifetime + time.Hour)
	journal, err := f.requester.s.readOutgoing(f.outgoing.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, err := worker.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	journal.Grant = &worker.PairingCode{Version: 1, ServerID: f.requester.s.Identity.ServerID, Endpoint: journal.Approval.SelectedOrigin, Code: code}
	if err := f.requester.s.writeOutgoing(journal); err != nil {
		t.Fatal(err)
	}
	done, err := f.resume()
	if err != nil || !done.Complete || done.Grant.Code != code {
		t.Fatalf("a retained grant inside the window must complete with its original verifier: %v", err)
	}
}
