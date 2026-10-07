package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
)

const maxLiveForwards = 64
const maxForwardConnections = 16
const maxForwardFrames = 16
const maxForwardConnectionHistory = 4096

type forwardConnection struct {
	sequence [2]uint64
	eof      [2]bool
	closed   bool
}
type forwardRelay struct {
	ctx         context.Context
	cancel      context.CancelFunc
	joined      [2]bool // Protected by Service.forwardsMu.
	queues      [2]chan *pb.WatchForwardResponse
	sendMu      [2]sync.Mutex
	mu          sync.Mutex
	connections map[string]*forwardConnection
}
type forwardLane struct {
	reader      *workspaceReader
	primaryDone <-chan struct{}
	requests    chan *pb.Resource
}

func liveForwardLane(lane *forwardLane, primary domain.ID) error {
	if lane == nil || lane.reader.primary != primary {
		return forwardUnavailable()
	}
	select {
	case <-lane.reader.done:
		return forwardUnavailable()
	case <-lane.primaryDone:
		return forwardUnavailable()
	default:
		return nil
	}
}

func forwardUnavailable() error {
	return domain.Fail(domain.Unavailable, "The forward lifetime is unavailable.", "Inspect its status and cleanup; reconnect never reopens a stopped or claimed lifetime.")
}
func forwardDenied() error {
	return domain.Fail(domain.PermissionDenied, "This device does not own the session forward.", "Use its original authenticated client or owning Worker.")
}
func forwardClient(ctx context.Context) (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok {
		return actor, forwardDenied()
	}
	return actor, nil
}
func (s *Service) forwardInit() {
	s.forwardsOnce.Do(func() {
		s.forwardEpoch = domain.NewID()
		s.forwardRelays = map[domain.ID]*forwardRelay{}
		s.forwardLanes = map[domain.ID]*forwardLane{}
	})
}
func forwardRecord(tx *store.Tx, id domain.ID) (store.Record, domain.Forward, error) {
	r, err := tx.Get(domain.ForwardKind, id)
	if err != nil {
		return r, domain.Forward{}, err
	}
	v, err := store.Decode[domain.Forward](r)
	return r, v, err
}
func ownedForward(tx *store.Tx, actor domain.Principal, id, session domain.ID) (store.Record, domain.Forward, error) {
	if err := tx.Authorize(); err != nil {
		return store.Record{}, domain.Forward{}, err
	}
	r, v, err := forwardRecord(tx, id)
	if err != nil {
		return r, v, err
	}
	if r.SessionID != session || actor.Type != v.ClientType || actor.DeviceID != v.ClientDeviceID {
		domain.ObserveOwnership(domain.OwnershipResource, r.ID)
	}
	return r, v, nil
}
func (s *Service) forwardResult(ctx context.Context, result store.Result, session domain.ID, actor domain.Principal) (*pb.ForwardChange, error) {
	var id domain.ID
	if err := json.Unmarshal(result.Data, &id); err != nil {
		return nil, err
	}
	var row store.Record
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		var err error
		row, _, err = ownedForward(tx, actor, id, session)
		return err
	})
	return &pb.ForwardChange{Forward: rpc.Resource(row), Replayed: result.Replayed}, err
}
func (s *Service) StartForward(ctx context.Context, req *connect.Request[pb.StartForwardRequest]) (*connect.Response[pb.StartForwardResponse], error) {
	s.forwardInit()
	actor, err := forwardClient(ctx)
	fail := func(err error) (*connect.Response[pb.StartForwardResponse], error) {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	if err != nil {
		return fail(err)
	}
	m := req.Msg
	if domain.ID(m.SessionId).Validate() != nil || domain.ID(m.MachineId).Validate() != nil || m.ExpectedSessionRevision == 0 || m.WorkerPort == 0 || m.WorkerPort > 65535 || m.LocalPort > 65535 {
		return fail(domain.Fail(domain.InvalidArgument, "Select an exact session, Worker and development port.", "Worker ports are 1–65535; local port zero requests an OS-assigned loopback port."))
	}
	// Actor identity is part of every receipt; one device cannot replay another's
	// start or native claim, even when it knows the public request UUID.
	input := struct {
		Actor   domain.Principal
		Request *pb.StartForwardRequest
	}{actor, m}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "forward.start", input, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, domain.ID(m.SessionId))
		if err != nil {
			return nil, err
		}
		if sr.Revision != m.ExpectedSessionRevision {
			return nil, domain.Fail(domain.Conflict, "The session revision changed.", "Reload the session before starting a forward.")
		}
		if session.IsSidechat() {
			return nil, domain.SidechatUnavailable()
		}
		if !session.WorkspaceAvailable() {
			return nil, domain.Fail(domain.Conflict, "Workspace storage retains this session.", "Settle the original storage operation and restore the workspace before starting a forward.")
		}
		if session.MachineID != domain.ID(m.MachineId) || session.Archive != domain.NotArchived {
			return nil, forwardDenied()
		}
		_, machine, err := activeMachine(tx, session.MachineID)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(machine.WorkerCapabilities, domain.SessionForwardingV1) {
			return nil, domain.Fail(domain.Unsupported, "This Worker has no session forwarding capability.", "Connect a matching forwarding-capable Worker.")
		}
		s.forwardsMu.Lock()
		lane := s.forwardLanes[session.MachineID]
		s.forwardsMu.Unlock()
		if lane == nil {
			return nil, forwardUnavailable()
		}
		if err := currentWorkspaceReader(tx, lane.reader); err != nil {
			return nil, err
		}
		if err := liveForwardLane(lane, lane.reader.primary); err != nil {
			return nil, err
		}
		total, clientCount, machineCount := 0, 0, 0
		if err := tx.VisitForwards("", func(row store.Record, v domain.Forward) error {
			if !v.Closed() && v.Epoch != s.forwardEpoch {
				v.Stop()
				if _, err := tx.Put(domain.ForwardKind, row.ID, row.Revision, row.SessionID, row.ProjectID, v); err != nil {
					return err
				}
			}
			if !v.Closed() {
				total++
				if v.ClientType == actor.Type && v.ClientDeviceID == actor.DeviceID {
					clientCount++
				}
				if v.MachineID == session.MachineID {
					machineCount++
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
		if total >= maxLiveForwards || clientCount >= 8 || machineCount >= 16 {
			return nil, domain.Fail(domain.ResourceExhausted, "The live forward limit is reached.", "Stop and confirm cleanup of existing forwards before creating another.")
		}
		id := domain.ID(m.RequestId)
		value := domain.Forward{MachineID: session.MachineID, WorkerDeviceID: lane.reader.device, WorkerInstanceID: lane.reader.instance, PrimaryStreamID: lane.reader.primary, ClientDeviceID: actor.DeviceID, ClientType: actor.Type, ClientRuntimeID: domain.NewID(), WorkerRuntimeID: domain.NewID(), Epoch: s.forwardEpoch, WorkerPort: m.WorkerPort, LocalPort: m.LocalPort, State: domain.ForwardPending}
		if _, err := tx.Put(domain.ForwardKind, id, 0, sr.ID, sr.ProjectID, value); err != nil {
			return nil, err
		}
		return id, nil
	})
	if err != nil {
		return fail(err)
	}
	change, err := s.forwardResult(ctx, result, domain.ID(m.SessionId), actor)
	if err != nil {
		return fail(err)
	}
	s.logger.InfoContext(ctx, "forward_start_accepted", "forward_id", m.RequestId, "session_id", m.SessionId, "machine_id", m.MachineId, "replayed", result.Replayed)
	return connect.NewResponse(&pb.StartForwardResponse{Forward: change.Forward, Replayed: change.Replayed}), nil
}
func (s *Service) GetForward(ctx context.Context, req *connect.Request[pb.GetForwardRequest]) (*connect.Response[pb.GetForwardResponse], error) {
	s.forwardInit()
	actor, err := forwardClient(ctx)
	var row store.Record
	if err == nil && (domain.ID(req.Msg.ForwardId).Validate() != nil || domain.ID(req.Msg.SessionId).Validate() != nil) {
		err = domain.ID(req.Msg.ForwardId).Validate()
		if err == nil {
			err = domain.ID(req.Msg.SessionId).Validate()
		}
	}
	if err == nil {
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			var e error
			row, _, e = ownedForward(tx, actor, domain.ID(req.Msg.ForwardId), domain.ID(req.Msg.SessionId))
			return e
		})
	}
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	// Server replacement cannot restore traffic. Reconcile the metadata without
	// claiming either peer's independent native cleanup.
	value, err := store.Decode[domain.Forward](row)
	if err == nil && value.Epoch != s.forwardEpoch && !value.Closed() {
		s.endForward(row.ID)
		row, err = s.Store.Get(ctx, domain.ForwardKind, row.ID)
	}
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.GetForwardResponse{Forward: rpc.Resource(row)}), nil
}
func (s *Service) StopForward(ctx context.Context, req *connect.Request[pb.StopForwardRequest]) (*connect.Response[pb.StopForwardResponse], error) {
	actor, err := forwardClient(ctx)
	fail := func(e error) (*connect.Response[pb.StopForwardResponse], error) {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	if err != nil {
		return fail(err)
	}
	if err := validateSessionMutation(req.Msg.Mutation); err != nil {
		return fail(err)
	}
	m := req.Msg.Mutation
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "forward.stop", struct {
		Actor   domain.Principal
		Request *pb.StopForwardRequest
	}{actor, req.Msg}, func(tx *store.Tx) (any, error) {
		r, v, err := ownedForward(tx, actor, domain.ID(m.Id), domain.ID(req.Msg.SessionId))
		if err != nil {
			return nil, err
		}
		if r.Revision != m.ExpectedRevision {
			return nil, domain.Fail(domain.Conflict, "The forward revision changed.", "Reload its current status before stopping it.")
		}
		v.Stop()
		if _, err := tx.Put(domain.ForwardKind, r.ID, r.Revision, r.SessionID, r.ProjectID, v); err != nil {
			return nil, err
		}
		return r.ID, nil
	})
	if err != nil {
		return fail(err)
	}
	s.endForward(domain.ID(m.Id))
	change, err := s.forwardResult(ctx, result, domain.ID(req.Msg.SessionId), actor)
	if err != nil {
		return fail(err)
	}
	return connect.NewResponse(&pb.StopForwardResponse{Forward: change.Forward, Replayed: change.Replayed}), nil
}

// peerForward is shared by every traffic, claim and cleanup operation. Cleanup
// may outlive the server/Worker epoch but can never gain traffic authority.
func (s *Service) peerForward(tx *store.Tx, actor domain.Principal, peer *pb.ForwardPeer, live bool) (store.Record, domain.Forward, int, error) {
	var r store.Record
	var v domain.Forward
	if peer == nil || domain.ID(peer.ForwardId).Validate() != nil || domain.ID(peer.SessionId).Validate() != nil || domain.ID(peer.RuntimeId).Validate() != nil {
		return r, v, 0, forwardDenied()
	}
	if err := tx.Authorize(); err != nil {
		return r, v, 0, err
	}
	r, v, err := forwardRecord(tx, domain.ID(peer.ForwardId))
	if err != nil {
		return r, v, 0, err
	}
	side := 0
	if peer.MachineId != "" {
		side = 1
		if peer.MachineId != string(v.MachineID) || peer.InstanceId != string(v.WorkerInstanceID) || peer.RuntimeId != string(v.WorkerRuntimeID) {
			return r, v, side, forwardDenied()
		}
	} else if peer.RuntimeId != string(v.ClientRuntimeID) || peer.MachineId != "" || peer.InstanceId != "" {
		return r, v, side, forwardDenied()
	}
	if r.SessionID != domain.ID(peer.SessionId) {
		return r, v, side, forwardDenied()
	}
	if !live {
		return r, v, side, nil
	}
	if v.Epoch != s.forwardEpoch || (v.State != domain.ForwardPending && v.State != domain.ForwardActive) {
		return r, v, side, forwardUnavailable()
	}
	_, session, err := sessionRecord(tx, r.SessionID)
	if err != nil || session.Archive != domain.NotArchived || !session.WorkspaceAvailable() || session.MachineID != v.MachineID {
		return r, v, side, forwardUnavailable()
	}
	reader := &workspaceReader{machine: v.MachineID, instance: v.WorkerInstanceID, device: v.WorkerDeviceID}
	if err := currentWorkspaceReader(tx, reader); err != nil {
		return r, v, side, err
	}
	// Recheck the client independently on Worker writes and streaming sends.
	if err := forwardClientAuthorization(tx, v); err != nil {
		return r, v, side, err
	}
	// Authentication registers requests while holding connectionsMu across a
	// database read. Never acquire that mutex inside this transaction: use the
	// immutable captured primary lifetime, whose cancellation invalidates this
	// exact generation even when the same Worker instance reconnects.
	s.forwardsMu.Lock()
	lane := s.forwardLanes[v.MachineID]
	s.forwardsMu.Unlock()
	if err := liveForwardLane(lane, v.PrimaryStreamID); err != nil {
		return r, v, side, err
	}
	return r, v, side, nil
}
func forwardClientAuthorization(tx *store.Tx, v domain.Forward) error {
	if v.ClientType == domain.OwnerDevice && v.ClientDeviceID == "" {
		return nil
	}
	row, err := tx.Get(domain.DeviceKind, v.ClientDeviceID)
	if err != nil {
		return forwardDenied()
	}
	device, err := store.Decode[domain.Device](row)
	if err != nil || device.Revoked || device.Type != domain.ClientDevice {
		return forwardDenied()
	}
	return nil
}
func (s *Service) ClaimForward(ctx context.Context, req *connect.Request[pb.ClaimForwardRequest]) (*connect.Response[pb.ClaimForwardResponse], error) {
	s.forwardInit()
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok {
		return nil, rpc.Error(forwardDenied(), req.Header().Get(rpc.CorrelationHeader))
	}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "forward.claim", struct {
		Actor   domain.Principal
		Request *pb.ClaimForwardRequest
	}{actor, req.Msg}, func(tx *store.Tx) (any, error) {
		r, v, side, err := s.peerForward(tx, actor, req.Msg.Peer, true)
		if err != nil {
			return nil, err
		}
		if (side == 0 && v.ClientClaimed) || (side == 1 && v.WorkerClaimed) {
			return nil, domain.Fail(domain.Conflict, "This native forward lifetime was already claimed.", "Inspect the original runtime; a new request cannot reopen it.")
		}
		if err := tx.WorkerUpdateAdmission(v.MachineID); err != nil {
			return nil, err
		}
		if side == 0 {
			v.ClientClaimed = true
		} else {
			v.WorkerClaimed = true
		}
		if _, err := tx.Put(domain.ForwardKind, r.ID, r.Revision, r.SessionID, r.ProjectID, v); err != nil {
			return nil, err
		}
		return r.ID, nil
	})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	var row store.Record
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		var e error
		row, _, _, e = s.peerForward(tx, actor, req.Msg.Peer, true)
		return e
	})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.ClaimForwardResponse{Forward: rpc.Resource(row), Granted: !result.Replayed}), nil
}
func (s *Service) endForward(id domain.ID) {
	s.forwardInit()
	s.forwardsMu.Lock()
	relay := s.forwardRelays[id]
	if relay != nil {
		relay.cancel()
	}
	s.forwardsMu.Unlock()
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), 5*time.Second)
	defer cancel()
	_, err := s.Store.Mutate(ctx, domain.NewID(), "forward.end", id, func(tx *store.Tx) (any, error) {
		r, v, err := forwardRecord(tx, id)
		if err != nil {
			return nil, err
		}
		before := v
		v.Stop()
		if before != v {
			if _, err := tx.Put(domain.ForwardKind, r.ID, r.Revision, r.SessionID, r.ProjectID, v); err != nil {
				return nil, err
			}
		}
		return id, nil
	})
	if err != nil && s.logger != nil {
		s.logger.Warn("forward_cleanup_pending", "forward_id", id, "code", domain.SafeError(err).Code)
	}
}
func (s *Service) WatchForward(ctx context.Context, req *connect.Request[pb.WatchForwardRequest], stream *connect.ServerStream[pb.WatchForwardResponse]) error {
	s.forwardInit()
	actor, ok := domain.PrincipalFrom(ctx)
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if !ok {
		return rpc.Error(forwardDenied(), correlation)
	}
	var row store.Record
	var value domain.Forward
	var side int
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		var e error
		row, value, side, e = s.peerForward(tx, actor, req.Msg.Peer, true)
		return e
	})
	if err != nil {
		return rpc.Error(err, correlation)
	}
	if (side == 0 && (!value.ClientClaimed || value.ValidateEndpoint(req.Msg.LocalEndpoint) != nil)) || (side == 1 && (!value.WorkerClaimed || req.Msg.LocalEndpoint != "")) {
		return rpc.Error(forwardDenied(), correlation)
	}
	s.forwardsMu.Lock()
	relay := s.forwardRelays[row.ID]
	if relay == nil {
		// Only the client can create this relay, once. A lost lane cancels it and
		// leaves a retained canceled relay until both peer streams have joined exit.
		if side != 0 || len(s.forwardRelays) >= maxLiveForwards {
			s.forwardsMu.Unlock()
			return rpc.Error(forwardUnavailable(), correlation)
		}
		child, cancel := context.WithCancel(context.Background())
		relay = &forwardRelay{ctx: child, cancel: cancel, connections: map[string]*forwardConnection{}}
		for i := range 2 {
			relay.queues[i] = make(chan *pb.WatchForwardResponse, maxForwardFrames)
		}
		s.forwardRelays[row.ID] = relay
	}
	if relay.joined[side] || relay.ctx.Err() != nil {
		s.forwardsMu.Unlock()
		return rpc.Error(forwardUnavailable(), correlation)
	}
	relay.joined[side] = true
	s.forwardsMu.Unlock()
	defer func() {
		s.endForward(row.ID)
		s.forwardsMu.Lock()
		relay.joined[side] = false
		if !relay.joined[0] && !relay.joined[1] {
			delete(s.forwardRelays, row.ID)
		}
		s.forwardsMu.Unlock()
	}()
	// Persist the exact observed endpoint, never a client-selected network target.
	_, err = s.Store.Mutate(ctx, domain.NewID(), "forward.connected", struct {
		ID       domain.ID
		Side     int
		Endpoint string
	}{row.ID, side, req.Msg.LocalEndpoint}, func(tx *store.Tx) (any, error) {
		r, v, _, err := s.peerForward(tx, actor, req.Msg.Peer, true)
		if err != nil {
			return nil, err
		}
		if side == 0 {
			if v.LocalEndpoint != "" {
				return nil, forwardUnavailable()
			}
			v.LocalEndpoint = req.Msg.LocalEndpoint
		}
		if side == 1 {
			v.State = domain.ForwardActive
		}
		_, err = tx.Put(domain.ForwardKind, r.ID, r.Revision, r.SessionID, r.ProjectID, v)
		return r.ID, err
	})
	if err != nil {
		return rpc.Error(err, correlation)
	}
	controller, ok := ctx.Value(writeControllerKey{}).(*http.ResponseController)
	if !ok {
		return rpc.Error(forwardUnavailable(), correlation)
	}
	send := func(m *pb.WatchForwardResponse) error {
		if err := controller.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return err
		}
		return stream.Send(m)
	}
	stream.ResponseHeader().Set(rpc.CorrelationHeader, correlation)
	if err := send(&pb.WatchForwardResponse{Heartbeat: true}); err != nil {
		return err
	}
	if side == 0 {
		s.forwardsMu.Lock()
		lane := s.forwardLanes[value.MachineID]
		s.forwardsMu.Unlock()
		if lane == nil {
			return rpc.Error(forwardUnavailable(), correlation)
		}
		current, err := s.Store.Get(ctx, domain.ForwardKind, row.ID)
		if err != nil {
			return rpc.Error(err, correlation)
		}
		select {
		case lane.requests <- rpc.Resource(current):
		case <-lane.reader.done:
			return rpc.Error(forwardUnavailable(), correlation)
		case <-ctx.Done():
			return nil
		default:
			return rpc.Error(forwardUnavailable(), correlation)
		}
	} else {
		for i := range 2 {
			select {
			case relay.queues[i] <- &pb.WatchForwardResponse{Ready: true}:
			case <-relay.ctx.Done():
				return nil
			default:
				return rpc.Error(forwardUnavailable(), correlation)
			}
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	ticks := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-relay.ctx.Done():
			return nil
		case m := <-relay.queues[side]:
			if err := s.Store.Read(ctx, func(tx *store.Tx) error { _, _, _, e := s.peerForward(tx, actor, req.Msg.Peer, true); return e }); err != nil {
				return rpc.Error(err, correlation)
			}
			if err := send(m); err != nil {
				return err
			}
		case <-ticker.C:
			if err := s.Store.Read(ctx, func(tx *store.Tx) error { _, _, _, e := s.peerForward(tx, actor, req.Msg.Peer, true); return e }); err != nil {
				return rpc.Error(err, correlation)
			}
			ticks++
			if ticks%10 == 0 {
				if err := send(&pb.WatchForwardResponse{Heartbeat: true}); err != nil {
					return err
				}
			}
		}
	}
}
func (s *Service) SendForward(ctx context.Context, req *connect.Request[pb.SendForwardRequest]) (*connect.Response[pb.SendForwardResponse], error) {
	s.forwardInit()
	actor, ok := domain.PrincipalFrom(ctx)
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(e error) (*connect.Response[pb.SendForwardResponse], error) {
		return nil, rpc.Error(e, correlation)
	}
	if !ok {
		return fail(forwardDenied())
	}
	var row store.Record
	var side int
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		var e error
		row, _, side, e = s.peerForward(tx, actor, req.Msg.Peer, true)
		return e
	}); err != nil {
		return fail(err)
	}
	f := req.Msg.Frame
	if f == nil || domain.ID(f.ConnectionId).Validate() != nil || f.Sequence == 0 || f.Sequence >= 1<<63 || len(f.Data) > 32<<10 || (f.Kind != pb.ForwardFrameKind_FORWARD_FRAME_KIND_DATA && len(f.Data) != 0) || (f.Kind == pb.ForwardFrameKind_FORWARD_FRAME_KIND_DATA && len(f.Data) == 0) || f.Kind < pb.ForwardFrameKind_FORWARD_FRAME_KIND_OPEN || f.Kind > pb.ForwardFrameKind_FORWARD_FRAME_KIND_CLOSE {
		return fail(domain.Fail(domain.InvalidArgument, "Invalid forward traffic frame.", "Use bounded contiguous frames from the original socket lifetime."))
	}
	s.forwardsMu.Lock()
	relay := s.forwardRelays[row.ID]
	joined := relay != nil && relay.joined[0] && relay.joined[1]
	s.forwardsMu.Unlock()
	if !joined || relay.ctx.Err() != nil {
		return fail(forwardUnavailable())
	}
	relay.sendMu[side].Lock()
	defer relay.sendMu[side].Unlock()
	relay.mu.Lock()
	connection := relay.connections[f.ConnectionId]
	if f.Kind == pb.ForwardFrameKind_FORWARD_FRAME_KIND_OPEN {
		active := 0
		for _, c := range relay.connections {
			if !c.closed {
				active++
			}
		}
		if side != 0 || connection != nil || f.Sequence != 1 || active >= maxForwardConnections || len(relay.connections) >= maxForwardConnectionHistory {
			relay.mu.Unlock()
			return fail(domain.Fail(domain.ResourceExhausted, "The forward connection bound or original identity is invalid.", "Use at most sixteen live sockets and never reuse a connection identity."))
		}
		connection = &forwardConnection{}
		relay.connections[f.ConnectionId] = connection
	}
	if connection == nil || f.Sequence != connection.sequence[side]+1 || (connection.eof[side] && f.Kind != pb.ForwardFrameKind_FORWARD_FRAME_KIND_CLOSE) {
		relay.mu.Unlock()
		return fail(domain.Fail(domain.Conflict, "The forward traffic order changed.", "Close the original socket; frames are never blindly retried."))
	}
	connection.sequence[side] = f.Sequence
	discard := connection.closed
	if f.Kind == pb.ForwardFrameKind_FORWARD_FRAME_KIND_EOF {
		connection.eof[side] = true
	}
	if f.Kind == pb.ForwardFrameKind_FORWARD_FRAME_KIND_CLOSE || (connection.eof[0] && connection.eof[1]) {
		connection.closed = true
	}
	relay.mu.Unlock()
	if !discard {
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		select {
		case relay.queues[1-side] <- &pb.WatchForwardResponse{Frame: proto.Clone(f).(*pb.ForwardFrame)}:
		case <-relay.ctx.Done():
			return fail(forwardUnavailable())
		case <-bounded.Done():
			s.endForward(row.ID)
			return fail(domain.Fail(domain.ResourceExhausted, "Forward traffic exceeded its bounded backpressure window.", "Inspect cleanup before explicitly creating a new forward."))
		}
	}
	return connect.NewResponse(&pb.SendForwardResponse{}), nil
}
func (s *Service) ReportForwardCleanup(ctx context.Context, req *connect.Request[pb.ReportForwardCleanupRequest]) (*connect.Response[pb.ReportForwardCleanupResponse], error) {
	s.forwardInit()
	actor, ok := domain.PrincipalFrom(ctx)
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if !ok {
		return nil, rpc.Error(forwardDenied(), correlation)
	}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "forward.cleanup", struct {
		Actor   domain.Principal
		Request *pb.ReportForwardCleanupRequest
	}{actor, req.Msg}, func(tx *store.Tx) (any, error) {
		r, v, side, err := s.peerForward(tx, actor, req.Msg.Peer, false)
		if err != nil {
			return nil, err
		}
		if (side == 0 && !v.ClientClaimed) || (side == 1 && !v.WorkerClaimed) {
			return nil, forwardDenied()
		}
		v.Stop()
		if side == 0 {
			v.ClientClean = true
		} else {
			v.WorkerClean = true
		}
		if v.ClientClean && v.WorkerClean {
			v.State = domain.ForwardStopped
		}
		if _, err := tx.Put(domain.ForwardKind, r.ID, r.Revision, r.SessionID, r.ProjectID, v); err != nil {
			return nil, err
		}
		if err := finishForwardArchive(tx, r.SessionID); err != nil {
			return nil, err
		}
		return r.ID, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.endForward(domain.ID(req.Msg.Peer.ForwardId))
	var row store.Record
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		var e error
		row, _, _, e = s.peerForward(tx, actor, req.Msg.Peer, false)
		return e
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "forward_cleanup_confirmed", "forward_id", row.ID, "device_type", actor.Type, "replayed", result.Replayed)
	return connect.NewResponse(&pb.ReportForwardCleanupResponse{Forward: rpc.Resource(row), Replayed: result.Replayed}), nil
}
func finishForwardArchive(tx *store.Tx, id domain.ID) error {
	// Cleanup reports remain valid during permanent deletion. Its controller
	// owns final resource removal; ordinary session controls are already closed.
	deleting, err := tx.SessionDeleting(id)
	if err != nil || deleting {
		return err
	}
	r, session, err := sessionRecord(tx, id)
	if err != nil {
		if domain.SafeError(err).Code == domain.NotFound {
			return nil
		}
		return err
	}
	if session.Archive != domain.ArchivePending || session.ActiveExecutionID != "" || session.CompactionJobID != "" || session.Recovery != domain.NoRecovery {
		return nil
	}
	pending, err := tx.SessionForwardsPending(id)
	if err != nil || pending {
		return err
	}
	if session.TitleJobID != "" {
		row, err := tx.Get(domain.JobKind, session.TitleJobID)
		if err != nil {
			return err
		}
		job, err := store.Decode[domain.Job](row)
		if err != nil {
			return err
		}
		if !job.State.Terminal() || job.State == domain.JobUncertain {
			return nil
		}
	}
	stopped, err := stopSessionWorkspace(tx, &session)
	if err != nil || !stopped || session.Recovery != domain.NoRecovery {
		return err
	}
	session.Archive = domain.Archived
	_, err = tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, session)
	return err
}
func (s *Service) WatchForwardRequests(ctx context.Context, req *connect.Request[pb.WatchForwardRequestsRequest], stream *connect.ServerStream[pb.WatchForwardRequestsResponse]) error {
	s.forwardInit()
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return rpc.Error(err, correlation)
	}
	primary, err := s.primaryWorkspaceStream(domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId))
	if err != nil {
		return rpc.Error(err, correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	reader := &workspaceReader{machine: domain.ID(req.Msg.MachineId), instance: domain.ID(req.Msg.InstanceId), device: actor.DeviceID, primary: primary.ID, done: ctx.Done()}
	authorize := func() error {
		return s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := currentWorkspaceReader(tx, reader); err != nil {
				return err
			}
			_, machine, err := activeMachine(tx, reader.machine)
			if err != nil {
				return err
			}
			if !slices.Contains(machine.WorkerCapabilities, domain.SessionForwardingV1) {
				return domain.Fail(domain.Unsupported, "Worker forwarding capability was not negotiated.", "Attach with the verified forwarding protocol capability.")
			}
			return nil
		})
	}
	if err := authorize(); err != nil {
		return rpc.Error(err, correlation)
	}
	lane := &forwardLane{reader: reader, primaryDone: primary.Done, requests: make(chan *pb.Resource, 16)}
	s.forwardsMu.Lock()
	if s.forwardLanes[reader.machine] != nil || len(s.forwardLanes) >= 1024 {
		s.forwardsMu.Unlock()
		return rpc.Error(forwardUnavailable(), correlation)
	}
	s.forwardLanes[reader.machine] = lane
	s.forwardsMu.Unlock()
	defer func() {
		s.forwardsMu.Lock()
		if s.forwardLanes[reader.machine] == lane {
			delete(s.forwardLanes, reader.machine)
		}
		s.forwardsMu.Unlock()
	}()
	controller, ok := ctx.Value(writeControllerKey{}).(*http.ResponseController)
	if !ok {
		return rpc.Error(forwardUnavailable(), correlation)
	}
	send := func(m *pb.WatchForwardRequestsResponse) error {
		if err := controller.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return err
		}
		return stream.Send(m)
	}
	stream.ResponseHeader().Set(rpc.CorrelationHeader, correlation)
	if err := send(&pb.WatchForwardRequestsResponse{Heartbeat: true}); err != nil {
		return err
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-primary.Done:
			return nil
		case resource := <-lane.requests:
			if err := authorize(); err != nil {
				return rpc.Error(err, correlation)
			}
			if err := send(&pb.WatchForwardRequestsResponse{Forward: resource}); err != nil {
				return err
			}
		case <-ticker.C:
			if err := authorize(); err != nil {
				return rpc.Error(err, correlation)
			}
			if err := send(&pb.WatchForwardRequestsResponse{Heartbeat: true}); err != nil {
				return err
			}
		}
	}
}
