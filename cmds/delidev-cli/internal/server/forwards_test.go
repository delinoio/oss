package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/forwarding"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

type forwardFixture struct {
	service           *Service
	endpoint          string
	identity, worker  security.Identity
	client            delidevv1connect.ForwardServiceClient
	workers           delidevv1connect.WorkerServiceClient
	primary           *connect.ServerStreamForClient[pb.WatchWorkResponse]
	lane              *connect.ServerStreamForClient[pb.WatchForwardRequestsResponse]
	machine, instance domain.ID
	session           store.Record
	ctx               context.Context
	cancel            context.CancelFunc
}

func newForwardFixture(t *testing.T) *forwardFixture {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: db, Identity: security.Identity{ServerID: domain.NewID(), Token: "private-forward-owner"}, logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	httpServer := httptest.NewServer(s.Handler(nil, true))
	ctx, cancel := context.WithCancel(context.Background())
	f := &forwardFixture{service: s, endpoint: httpServer.URL, identity: s.Identity, ctx: ctx, cancel: cancel}
	t.Cleanup(func() {
		cancel()
		if f.lane != nil {
			_ = f.lane.Close()
		}
		if f.primary != nil {
			_ = f.primary.Close()
		}
		s.executionAuthority.cancel()
		httpServer.Close()
		s.executionAuthority.close()
		_ = db.Close()
	})
	base := &accountFixture{t: t, endpoint: Endpoint{URL: httpServer.URL, ServerID: s.Identity.ServerID}, identity: s.Identity}
	worker, paired := pairedWorker(t, ctx, base.endpoint, base.identity)
	f.worker, f.machine, f.instance = worker, domain.ID(paired.Machine.Id), domain.NewID()
	f.client = delidevv1connect.NewForwardServiceClient(http.DefaultClient, httpServer.URL)
	f.workers = delidevv1connect.NewWorkerServiceClient(http.DefaultClient, httpServer.URL)
	_, err = f.workers.AttachWorker(ctx, ownerRequest(worker, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: string(f.machine), InstanceId: string(f.instance), Version: rpc.Version, Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_SESSION_FORWARDING_V1}}))
	if err != nil {
		t.Fatal(err)
	}
	f.primary, err = f.workers.WatchWork(ctx, ownerRequest(worker, &pb.WatchWorkRequest{MachineId: string(f.machine), InstanceId: string(f.instance)}))
	if err != nil || !f.primary.Receive() || !f.primary.Msg().Heartbeat {
		t.Fatal("primary readiness", err)
	}
	f.lane, err = f.workers.WatchForwardRequests(ctx, ownerRequest(worker, &pb.WatchForwardRequestsRequest{MachineId: string(f.machine), InstanceId: string(f.instance)}))
	if err != nil || !f.lane.Receive() || !f.lane.Msg().Heartbeat {
		t.Fatal("forward readiness", err)
	}
	id := domain.NewID()
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.session", id, func(tx *store.Tx) (any, error) {
		var err error
		f.session, err = tx.Put(domain.SessionKind, id, 0, id, "", domain.Session{Name: "Forward fixture", MachineID: f.machine, Workspace: domain.GeneralChat, Outcome: domain.ExecutionNotStarted, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Dispatch: domain.DispatchPaused})
		return id, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *forwardFixture) start(t *testing.T, port uint32, local uint32, identity security.Identity) (*pb.StartForwardRequest, *pb.ForwardChange) {
	t.Helper()
	m := &pb.StartForwardRequest{RequestId: string(domain.NewID()), SessionId: string(f.session.ID), ExpectedSessionRevision: f.session.Revision, MachineId: string(f.machine), WorkerPort: port, LocalPort: local}
	response, err := f.client.StartForward(f.ctx, ownerRequest(identity, m))
	if err != nil {
		t.Fatal(err)
	}
	return m, &pb.ForwardChange{Forward: response.Msg.Forward, Replayed: response.Msg.Replayed}
}
func forwardValue(t *testing.T, r *pb.Resource) domain.Forward {
	t.Helper()
	var v domain.Forward
	if r == nil || domain.Decode(r.DocumentJson, &v) != nil {
		t.Fatal("invalid forward")
	}
	return v
}
func (f *forwardFixture) config(t *testing.T, r *pb.Resource, worker bool, identity security.Identity) forwarding.Config {
	t.Helper()
	v := forwardValue(t, r)
	peer := &pb.ForwardPeer{ForwardId: r.Id, SessionId: r.SessionId, RuntimeId: string(v.ClientRuntimeID)}
	if worker {
		peer.RuntimeId, peer.MachineId, peer.InstanceId = string(v.WorkerRuntimeID), string(f.machine), string(f.instance)
	}
	return forwarding.Config{Client: f.client, Token: identity.Token, Peer: peer, Forward: v, Endpoint: f.endpoint, Root: filepath.Join(t.TempDir(), "runtime"), Logger: f.service.logger}
}
func (f *forwardFixture) observe(t *testing.T, id string, identity security.Identity) *pb.Resource {
	t.Helper()
	response, err := f.client.GetForward(f.ctx, ownerRequest(identity, &pb.GetForwardRequest{ForwardId: id, SessionId: string(f.session.ID)}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.Forward
}
func (f *forwardFixture) runPair(t *testing.T, record *pb.Resource, identity security.Identity) (string, <-chan error, <-chan error) {
	t.Helper()
	ready := make(chan string, 1)
	client := f.config(t, record, false, identity)
	client.Ready = func(endpoint string) error { ready <- endpoint; return nil }
	clientDone := make(chan error, 1)
	go func() { clientDone <- forwarding.Run(f.ctx, client) }()
	if !f.lane.Receive() || f.lane.Msg().Forward == nil {
		t.Fatal("missing original Worker forward", f.lane.Err())
	}
	worker := f.config(t, f.lane.Msg().Forward, true, f.worker)
	workerReady := make(chan string, 1)
	worker.Ready = func(endpoint string) error { workerReady <- endpoint; return nil }
	workerDone := make(chan error, 1)
	go func() { workerDone <- forwarding.Run(f.ctx, worker) }()
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	endpoint, err := awaitForwardPairReady(ctx, ready, workerReady, clientDone, workerDone)
	if err != nil {
		t.Fatal(err)
	}
	return endpoint, clientDone, workerDone
}

// Each original stream publishes Ready independently. A client endpoint alone
// cannot establish that the Worker reached the deletion fixture's ready boundary.
func awaitForwardPairReady(ctx context.Context, clientReady, workerReady <-chan string, clientDone, workerDone <-chan error) (string, error) {
	var endpoint string
	for clientReady != nil || workerReady != nil {
		select {
		case endpoint = <-clientReady:
			clientReady = nil
		case <-workerReady:
			workerReady = nil
		case err := <-clientDone:
			return "", fmt.Errorf("client ended before pair readiness: %v", err)
		case err := <-workerDone:
			return "", fmt.Errorf("Worker ended before pair readiness: %v", err)
		case <-ctx.Done():
			return "", fmt.Errorf("forward readiness timeout (client missing=%t, Worker missing=%t): %w", clientReady != nil, workerReady != nil, ctx.Err())
		}
	}
	return endpoint, nil
}

func TestForwardPairReadyObservation(t *testing.T) {
	t.Run("delayed Worker", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		client, worker := make(chan string), make(chan string)
		done := make(chan error, 1)
		result := make(chan string, 1)
		go func() {
			endpoint, err := awaitForwardPairReady(ctx, client, worker, nil, nil)
			result <- endpoint
			done <- err
		}()
		// The unbuffered send acknowledges receipt of client Ready while the
		// independent Worker barrier still has not been released.
		select {
		case client <- "127.0.0.1:12345":
		case <-ctx.Done():
			t.Fatal("client Ready was not observed", ctx.Err())
		}
		select {
		case endpoint := <-result:
			t.Fatalf("returned before Worker Ready: %s", endpoint)
		default:
		}
		select {
		case worker <- "":
		case <-ctx.Done():
			t.Fatal("Worker Ready was not observed", ctx.Err())
		}
		if endpoint := <-result; endpoint != "127.0.0.1:12345" {
			t.Fatal("changed original endpoint", endpoint)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
	for _, peer := range []string{"client", "Worker"} {
		t.Run(peer+" premature exit", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			clientDone, workerDone := make(chan error, 1), make(chan error, 1)
			if peer == "client" {
				clientDone <- fmt.Errorf("fixture ended")
			} else {
				workerDone <- fmt.Errorf("fixture ended")
			}
			_, err := awaitForwardPairReady(ctx, make(chan string), make(chan string), clientDone, workerDone)
			if err == nil || !strings.Contains(err.Error(), peer+" ended") {
				t.Fatal("missing peer diagnostic", err)
			}
		})
	}
	t.Run("missing Worker", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		client := make(chan string)
		done := make(chan error, 1)
		go func() { _, err := awaitForwardPairReady(ctx, client, make(chan string), nil, nil); done <- err }()
		select {
		case client <- "127.0.0.1:12345":
		case <-ctx.Done():
			t.Fatal("client Ready was not observed", ctx.Err())
		}
		cancel()
		if err := <-done; err == nil || !strings.Contains(err.Error(), "client missing=false, Worker missing=true") {
			t.Fatal("missing bounded readiness diagnostic", err)
		}
	})
}
func awaitForward(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("original native cleanup did not join")
	}
}
func TestForwardExactBytesReceiptReplayAndStopVersusArchive(t *testing.T) {
	f := newForwardFixture(t)
	fixture, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	payload := bytes.Repeat([]byte{0, 1, 0xff, '\n', '\r', 13, 42}, 48000)
	upstreamDone := make(chan error, 1)
	go func() {
		conn, err := fixture.Accept()
		if err != nil {
			upstreamDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
		observed, err := io.ReadAll(conn)
		if err == nil && !bytes.Equal(observed, payload) {
			err = io.ErrUnexpectedEOF
		}
		if err == nil {
			_, err = conn.Write(observed)
		}
		upstreamDone <- err
	}()
	port := uint32(fixture.Addr().(*net.TCPAddr).Port)
	start, accepted := f.start(t, port, 0, f.identity)
	endpoint, clientDone, workerDone := f.runPair(t, accepted.Forward, f.identity)
	replay, err := f.client.StartForward(f.ctx, ownerRequest(f.identity, start))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Forward.Id != accepted.Forward.Id {
		t.Fatal("creation was replayed", err)
	}
	value := forwardValue(t, replay.Msg.Forward)
	if value.State != domain.ForwardActive || value.LocalEndpoint != endpoint {
		t.Fatal("incorrect active endpoint", value.State, value.LocalEndpoint)
	}
	duplicate := f.config(t, replay.Msg.Forward, false, f.identity)
	if err := forwarding.Run(f.ctx, duplicate); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("a second listener lifetime was granted", err)
	}
	conn, err := net.Dial("tcp4", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	tcp := conn.(*net.TCPConn)
	if _, err := tcp.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tcp.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(tcp)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatal("bidirectional bytes or half-close changed", len(got), err)
	}
	if err := <-upstreamDone; err != nil {
		t.Fatal(err)
	}
	sessions := delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.endpoint)
	stopped, err := sessions.ControlSession(f.ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.session.ID), ExpectedRevision: f.session.Revision}, Action: pb.SessionAction_SESSION_ACTION_STOP}))
	if err != nil {
		t.Fatal(err)
	}
	if v := forwardValue(t, f.observe(t, accepted.Forward.Id, f.identity)); v.State != domain.ForwardActive {
		t.Fatal("agent Stop closed forward")
	}
	archived, err := sessions.ControlSession(f.ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.session.ID), ExpectedRevision: stopped.Msg.Change.Session.Revision}, Action: pb.SessionAction_SESSION_ACTION_ARCHIVE}))
	if err != nil {
		t.Fatal(err)
	}
	if v := sessionBody(t, archived.Msg.Change.Session); v.Archive != domain.ArchivePending {
		t.Fatal("Archive bypassed independent native cleanup", v.Archive)
	}
	awaitForward(t, clientDone)
	awaitForward(t, workerDone)
	current := f.observe(t, accepted.Forward.Id, f.identity)
	if v := forwardValue(t, current); !v.Closed() {
		t.Fatal("original handles not independently cleaned", v)
	}
	session, err := f.service.Store.Get(f.ctx, domain.SessionKind, f.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	v, err := store.Decode[domain.Session](session)
	if err != nil || v.Archive != domain.Archived {
		t.Fatal("Archive did not finish after both cleanup reports", err)
	}
	if conn, err := net.DialTimeout("tcp4", endpoint, time.Second); err == nil {
		conn.Close()
		t.Fatal("archived listener is still open")
	}
	replay, err = f.client.StartForward(f.ctx, ownerRequest(f.identity, start))
	if err != nil || !replay.Msg.Replayed || !forwardValue(t, replay.Msg.Forward).Closed() {
		t.Fatal("retry reopened archived forward", err)
	}
}
func TestForwardOccupiedExplicitPortNeverRemapsAndClaimNeverReplays(t *testing.T) {
	f := newForwardFixture(t)
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	start, change := f.start(t, 43210, uint32(occupied.Addr().(*net.TCPAddr).Port), f.identity)
	config := f.config(t, change.Forward, false, f.identity)
	if err := forwarding.Run(f.ctx, config); domain.SafeError(err).Code != domain.Unavailable {
		t.Fatal("occupied port did not fail", err)
	}
	current := f.observe(t, change.Forward.Id, f.identity)
	if v := forwardValue(t, current); !v.Closed() || v.LocalEndpoint != "" || v.WorkerClaimed {
		t.Fatal("occupied port remapped or dialed Worker", v)
	}
	replay, err := f.client.StartForward(f.ctx, ownerRequest(f.identity, start))
	if err != nil || !replay.Msg.Replayed || !forwardValue(t, replay.Msg.Forward).Closed() {
		t.Fatal(err)
	}
	// The exact original claim receipt no longer grants native authority.
	if _, err := f.client.ClaimForward(f.ctx, ownerRequest(f.identity, &pb.ClaimForwardRequest{RequestId: string(domain.NewID()), Peer: config.Peer})); err == nil {
		t.Fatal("stopped claim was reopened")
	}
}

func TestForwardSessionDeletionClosesOriginalHandles(t *testing.T) {
	f := newForwardFixture(t)
	_, change := f.start(t, 43210, 0, f.identity)
	endpoint, clientDone, workerDone := f.runPair(t, change.Forward, f.identity)
	_, err := f.service.Store.Mutate(f.ctx, domain.NewID(), "fixture.session.delete", f.session.ID, func(tx *store.Tx) (any, error) {
		return f.session.ID, tx.Delete(domain.SessionKind, f.session.ID, f.session.Revision)
	})
	if err != nil {
		t.Fatal(err)
	}
	awaitForward(t, clientDone)
	awaitForward(t, workerDone)
	if value := forwardValue(t, f.observe(t, change.Forward.Id, f.identity)); !value.Closed() {
		t.Fatal("deleted session bypassed independently joined socket cleanup", value)
	}
	if conn, err := net.DialTimeout("tcp4", endpoint, time.Second); err == nil {
		conn.Close()
		t.Fatal("deleted session listener remained open")
	}
	peer := f.config(t, change.Forward, false, f.identity).Peer
	if _, err := f.client.ClaimForward(f.ctx, ownerRequest(f.identity, &pb.ClaimForwardRequest{RequestId: string(domain.NewID()), Peer: peer})); err == nil {
		t.Fatal("deleted session reopened a native lifetime")
	}
}

func (f *forwardFixture) pairedClient(t *testing.T, label string) (security.Identity, domain.ID) {
	t.Helper()
	id := domain.NewID()
	token := "temporary-forward-" + label
	digest := sha256.Sum256([]byte(token))
	_, err := f.service.Store.Mutate(f.ctx, domain.NewID(), "fixture.client", id, func(tx *store.Tx) (any, error) {
		if _, err := tx.Put(domain.DeviceKind, id, 0, "", "", domain.Device{Name: label, Type: domain.ClientDevice}); err != nil {
			return nil, err
		}
		return id, tx.PutCredential(id, digest[:])
	})
	if err != nil {
		t.Fatal(err)
	}
	return security.Identity{Token: token, ServerID: f.identity.ServerID}, id
}
func TestForwardRejectsForeignSessionsDevicesAndRevokedClients(t *testing.T) {
	f := newForwardFixture(t)
	client, device := f.pairedClient(t, "original")
	other, _ := f.pairedClient(t, "other")
	_, change := f.start(t, 43210, 0, client)
	c := f.config(t, change.Forward, false, client)
	if _, err := f.client.GetForward(f.ctx, ownerRequest(other, &pb.GetForwardRequest{ForwardId: change.Forward.Id, SessionId: string(f.session.ID)})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("foreign device observed authority", err)
	}
	if _, err := f.client.GetForward(f.ctx, ownerRequest(client, &pb.GetForwardRequest{ForwardId: change.Forward.Id, SessionId: string(domain.NewID())})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("foreign session accepted", err)
	}
	foreign := proto.Clone(c.Peer).(*pb.ForwardPeer)
	foreign.SessionId = string(domain.NewID())
	if _, err := f.client.ClaimForward(f.ctx, ownerRequest(client, &pb.ClaimForwardRequest{RequestId: string(domain.NewID()), Peer: foreign})); err == nil {
		t.Fatal("foreign-session native claim")
	}
	endpoint, clientDone, workerDone := f.runPair(t, change.Forward, client)
	devices := delidevv1connect.NewDeviceServiceClient(http.DefaultClient, f.endpoint)
	row, err := f.service.Store.Get(f.ctx, domain.DeviceKind, device)
	if err != nil {
		t.Fatal(err)
	}
	_, err = devices.RevokeDevice(f.ctx, ownerRequest(f.identity, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(device), ExpectedRevision: row.Revision}}))
	if err != nil {
		t.Fatal(err)
	}
	// The revoked peer's positive cleanup remains private because revoked
	// credentials cannot report it. Worker cleanup remains independently usable.
	select {
	case err := <-clientDone:
		if domain.SafeError(err).Code != domain.Unauthenticated && domain.SafeError(err).Code != domain.Unavailable && domain.SafeError(err).Code != domain.Canceled {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("revoked listener remained")
	}
	awaitForward(t, workerDone)
	if conn, err := net.DialTimeout("tcp4", endpoint, time.Second); err == nil {
		conn.Close()
		t.Fatal("revoked client still listens")
	}
	if _, err := f.client.SendForward(f.ctx, ownerRequest(client, &pb.SendForwardRequest{Peer: c.Peer, Frame: &pb.ForwardFrame{ConnectionId: string(domain.NewID()), Sequence: 1, Kind: pb.ForwardFrameKind_FORWARD_FRAME_KIND_OPEN}})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("revoked traffic accepted", err)
	}
	current, err := f.service.Store.Get(f.ctx, domain.ForwardKind, domain.ID(change.Forward.Id))
	if err != nil {
		t.Fatal(err)
	}
	v, err := store.Decode[domain.Forward](current)
	if err != nil || v.State != domain.ForwardStopping || v.ClientClean || !v.WorkerClean {
		t.Fatal("revocation fabricated cleanup", err, v)
	}
}
func TestForwardOrderedFramesBoundsAndStoppedReconnect(t *testing.T) {
	f := newForwardFixture(t)
	_, change := f.start(t, 43210, 0, f.identity)
	c := f.config(t, change.Forward, false, f.identity)
	claimed, err := f.client.ClaimForward(f.ctx, ownerRequest(f.identity, &pb.ClaimForwardRequest{RequestId: string(domain.NewID()), Peer: c.Peer}))
	if err != nil || !claimed.Msg.Granted {
		t.Fatal(err)
	}
	claim := &pb.ClaimForwardRequest{RequestId: string(domain.NewID()), Peer: &pb.ForwardPeer{ForwardId: c.Peer.ForwardId, SessionId: c.Peer.SessionId, RuntimeId: string(c.Forward.WorkerRuntimeID), MachineId: string(f.machine), InstanceId: string(f.instance)}}
	if _, err := f.client.ClaimForward(f.ctx, ownerRequest(f.worker, claim)); err != nil {
		t.Fatal(err)
	}
	replay, err := f.client.ClaimForward(f.ctx, ownerRequest(f.worker, claim))
	if err != nil || replay.Msg.Granted {
		t.Fatal("claim receipt regranted native work", err)
	}
	child, cancel := context.WithCancel(context.Background())
	defer cancel()
	relay := &forwardRelay{ctx: child, cancel: cancel, joined: [2]bool{true, true}, connections: map[string]*forwardConnection{}}
	for i := range 2 {
		relay.queues[i] = make(chan *pb.WatchForwardResponse, maxForwardFrames)
	}
	f.service.forwardsMu.Lock()
	f.service.forwardRelays[domain.ID(change.Forward.Id)] = relay
	f.service.forwardsMu.Unlock()
	send := func(ctx context.Context, frame *pb.ForwardFrame) error {
		_, err := f.client.SendForward(ctx, ownerRequest(f.identity, &pb.SendForwardRequest{Peer: c.Peer, Frame: frame}))
		return err
	}
	id := string(domain.NewID())
	if err := send(f.ctx, &pb.ForwardFrame{ConnectionId: id, Sequence: 1, Kind: pb.ForwardFrameKind_FORWARD_FRAME_KIND_OPEN}); err != nil {
		t.Fatal(err)
	}
	if err := send(f.ctx, &pb.ForwardFrame{ConnectionId: id, Sequence: 3, Kind: pb.ForwardFrameKind_FORWARD_FRAME_KIND_DATA, Data: []byte("gap")}); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("out-of-order bytes accepted", err)
	}
	if err := send(f.ctx, &pb.ForwardFrame{ConnectionId: id, Sequence: 2, Kind: pb.ForwardFrameKind_FORWARD_FRAME_KIND_DATA, Data: make([]byte, (32<<10)+1)}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("oversized traffic accepted", err)
	}
	for seq := uint64(2); seq <= maxForwardFrames; seq++ {
		if err := send(f.ctx, &pb.ForwardFrame{ConnectionId: id, Sequence: seq, Kind: pb.ForwardFrameKind_FORWARD_FRAME_KIND_DATA, Data: []byte{byte(seq)}}); err != nil {
			t.Fatal(err)
		}
	}
	bounded, stop := context.WithTimeout(f.ctx, 100*time.Millisecond)
	defer stop()
	if err := send(bounded, &pb.ForwardFrame{ConnectionId: id, Sequence: maxForwardFrames + 1, Kind: pb.ForwardFrameKind_FORWARD_FRAME_KIND_DATA, Data: []byte("bounded")}); err == nil {
		t.Fatal("unbounded backpressure")
	}
	// The canceled sender must settle the forward rather than replaying a frame.
	deadline := time.Now().Add(3 * time.Second)
	var current *pb.Resource
	for time.Now().Before(deadline) {
		current = f.observe(t, change.Forward.Id, f.identity)
		if forwardValue(t, current).State == domain.ForwardStopping {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if forwardValue(t, current).State != domain.ForwardStopping {
		t.Fatal("backpressure did not end original lifetime")
	}
	reconnected, err := f.client.WatchForward(f.ctx, ownerRequest(f.identity, &pb.WatchForwardRequest{Peer: c.Peer, LocalEndpoint: "127.0.0.1:43211"}))
	if err == nil {
		defer reconnected.Close()
		if reconnected.Receive() || reconnected.Err() == nil {
			t.Fatal("reconnect reopened stopped forward")
		}
	}
}
func TestForwardEndpointValidation(t *testing.T) {
	v := domain.Forward{LocalPort: 1234}
	for _, endpoint := range []string{"0.0.0.0:1234", "localhost:1234", "127.0.0.2:1234", "127.0.0.1:0", "127.0.0.1:01234", "127.0.0.1:1235", "https://127.0.0.1:1234"} {
		if v.ValidateEndpoint(endpoint) == nil {
			t.Fatal("invalid local endpoint", endpoint)
		}
	}
	if v.ValidateEndpoint(net.JoinHostPort("::1", strconv.Itoa(1234))) != nil {
		t.Fatal("explicit IPv6 loopback rejected")
	}
}

type loseForwardCleanupAck struct {
	delidevv1connect.ForwardServiceClient
	dropped bool
	before  bool
}

func (c *loseForwardCleanupAck) ReportForwardCleanup(ctx context.Context, req *connect.Request[pb.ReportForwardCleanupRequest]) (*connect.Response[pb.ReportForwardCleanupResponse], error) {
	if c.before && !c.dropped {
		c.dropped = true
		return nil, connect.NewError(connect.CodeUnavailable, io.ErrUnexpectedEOF)
	}
	response, err := c.ForwardServiceClient.ReportForwardCleanup(ctx, req)
	if err == nil && !c.dropped {
		c.dropped = true
		return nil, connect.NewError(connect.CodeUnavailable, io.ErrUnexpectedEOF)
	}
	return response, err
}
func TestForwardOfflineCleanupReceiptCannotReopenNativeLifetime(t *testing.T) {
	for _, before := range []bool{true, false} {
		name := "lost-acknowledgment"
		if before {
			name = "offline-before-report"
		}
		t.Run(name, func(t *testing.T) {
			f := newForwardFixture(t)
			_, accepted := f.start(t, 43210, 0, f.identity)
			config := f.config(t, accepted.Forward, false, f.identity)
			// Lose only the original cleanup acknowledgment after the server commits it.
			// The retained positive local proof must replay that receipt without Listen.
			lost := &loseForwardCleanupAck{ForwardServiceClient: f.client, before: before}
			config.Client = lost
			ready := make(chan string, 1)
			config.Ready = func(endpoint string) error { ready <- endpoint; return nil }
			clientDone := make(chan error, 1)
			go func() { clientDone <- forwarding.Run(f.ctx, config) }()
			if !f.lane.Receive() || f.lane.Msg().Forward == nil {
				t.Fatal("missing Worker lifetime")
			}
			w := f.config(t, f.lane.Msg().Forward, true, f.worker)
			workerDone := make(chan error, 1)
			go func() { workerDone <- forwarding.Run(f.ctx, w) }()
			var endpoint string
			select {
			case endpoint = <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("readiness")
			}
			current := f.observe(t, accepted.Forward.Id, f.identity)
			_, err := f.client.StopForward(f.ctx, ownerRequest(f.identity, &pb.StopForwardRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: current.Id, ExpectedRevision: current.Revision}, SessionId: current.SessionId}))
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-clientDone:
				if domain.SafeError(err).Code != domain.ServerUnavailable {
					t.Fatal("cleanup response loss not retained", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("cleanup did not join")
			}
			awaitForward(t, workerDone)
			replacement, err := net.Listen("tcp4", endpoint)
			if err != nil {
				t.Fatal("original listener was not closed", err)
			}
			defer replacement.Close()
			// Retrying cleanup while another listener owns that exact port cannot open,
			// stop or otherwise affect the unrelated replacement listener.
			config.Client = f.client
			if err := forwarding.Reconcile(f.ctx, config); err != nil {
				t.Fatal(err)
			}
			if !forwardValue(t, f.observe(t, current.Id, f.identity)).Closed() {
				t.Fatal("original receipt did not retain cleanup")
			}
			if err := forwarding.Run(f.ctx, config); err == nil {
				t.Fatal("cleanup replay regained Listen authority")
			}
			if err := forwarding.Reconcile(f.ctx, config); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("missing local proof fabricated cleanup", err)
			}
		})
	}
}
