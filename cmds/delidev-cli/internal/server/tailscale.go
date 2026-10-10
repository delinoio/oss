// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"os/exec"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/desktopruntime"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/tailscale"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type tailscaleIngressKey struct{}
type tailscaleController struct {
	runner      tailscale.Runner
	access      *tailscale.AccessManager
	offer       []byte
	approvals   *tailscale.Approvals
	startWorker func(context.Context, string) error
}

func (s *Service) initializeTailscale(ctx context.Context, config Config) error {
	if config.Desktop == nil {
		return nil
	}
	path, _ := exec.LookPath("tailscale")
	runner := tailscale.CLI{Executable: path}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	controller := &tailscaleController{runner: runner, offer: key.PublicKey().Bytes(), approvals: &tailscale.Approvals{Root: s.Store.Root(), ServerID: s.Identity.ServerID}}
	businessHandler := s.Handler(config.AllowedOrigins, false)
	ingress := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == desktopruntime.ProofPath || len(r.URL.Path) < len("/delidev.v1.") || r.URL.Path[:len("/delidev.v1.")] != "/delidev.v1." {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), tailscaleIngressKey{}, true))
		businessHandler.ServeHTTP(w, r)
	})
	controller.access = &tailscale.AccessManager{Root: s.Store.Root(), ServerID: s.Identity.ServerID, Parent: ctx, Runner: runner, Launcher: tailscale.ProcessLauncher{Executable: path, Root: s.Store.Root(), Logger: s.logger}, Handler: ingress, Logger: s.logger}
	s.tailscale = controller
	// A retained opt-in is re-admitted under this original server and current
	// Tailscale identity. Failure leaves safe guidance; it never fails Local.
	if err := controller.access.Restore(ctx); err != nil {
		s.logger.WarnContext(ctx, "tailscale_access_readmission_failed", "failed", err != nil)
	}
	return nil
}
func (s *Service) tailnet(ctx context.Context) bool {
	value, _ := ctx.Value(tailscaleIngressKey{}).(bool)
	return value && s.tailscale != nil && s.tailscale.access.Origin() != "" && !s.stopping.Load()
}
func (s *Service) tailscaleLocal(ctx context.Context) error {
	actor, ok := domain.PrincipalFrom(ctx)
	target := desktopruntime.FromContext(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) || target == nil || target.ServerID != s.Identity.ServerID || s.tailscale == nil || s.stopping.Load() {
		return domain.Fail(domain.PermissionDenied, "This operation requires the original main Local connection.", "Use Connections in the main Local desktop.")
	}
	return nil
}
func (s *Service) tailscaleApprovals(ctx context.Context) (*tailscale.Approvals, error) {
	if !s.tailnet(ctx) {
		return nil, domain.Fail(domain.Unavailable, "Tailscale access is not accepting requests.", "Enable the original server's explicit access first.")
	}
	a := s.tailscale.approvals
	origin := s.tailscale.access.Origin()
	a.SetOrigin(origin)
	return a, nil
}
func (s *Service) GetTailscaleStatus(ctx context.Context, req *connect.Request[pb.GetTailscaleStatusRequest]) (*connect.Response[pb.GetTailscaleStatusResponse], error) {
	if !s.tailnet(ctx) {
		return nil, rpc.Error(domain.Fail(domain.Unsupported, "This endpoint does not expose Tailscale discovery.", "Check the peer's explicit HTTPS access."), req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.GetTailscaleStatusResponse{ServerId: string(s.Identity.ServerID), Origin: s.tailscale.access.Origin(), PublicKey: s.tailscale.offer, Accepting: true}), nil
}
func peerMessage(p tailscale.Peer) *pb.TailscalePeer {
	ownership := map[tailscale.Ownership]pb.TailscaleOwnership{tailscale.Own: pb.TailscaleOwnership_TAILSCALE_OWNERSHIP_OWN, tailscale.Shared: pb.TailscaleOwnership_TAILSCALE_OWNERSHIP_SHARED, tailscale.Tagged: pb.TailscaleOwnership_TAILSCALE_OWNERSHIP_TAGGED, tailscale.Unknown: pb.TailscaleOwnership_TAILSCALE_OWNERSHIP_UNKNOWN}[p.Ownership]
	return &pb.TailscalePeer{Id: p.ID, Name: p.Name, Origin: p.Origin, Online: p.Online, Ownership: ownership}
}
func (s *Service) ReadTailscaleDevices(ctx context.Context, req *connect.Request[pb.ReadTailscaleDevicesRequest]) (*connect.Response[pb.ReadTailscaleDevicesResponse], error) {
	if err := s.tailscaleLocal(ctx); err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	d, err := tailscale.Discover(ctx, s.tailscale.runner)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	state := map[tailscale.State]pb.TailscaleDiscoveryState{tailscale.Ready: pb.TailscaleDiscoveryState_TAILSCALE_DISCOVERY_STATE_READY, tailscale.Missing: pb.TailscaleDiscoveryState_TAILSCALE_DISCOVERY_STATE_MISSING, tailscale.Stopped: pb.TailscaleDiscoveryState_TAILSCALE_DISCOVERY_STATE_STOPPED, tailscale.LoggedOut: pb.TailscaleDiscoveryState_TAILSCALE_DISCOVERY_STATE_LOGGED_OUT, tailscale.PermissionDenied: pb.TailscaleDiscoveryState_TAILSCALE_DISCOVERY_STATE_PERMISSION_DENIED, tailscale.Malformed: pb.TailscaleDiscoveryState_TAILSCALE_DISCOVERY_STATE_MALFORMED, tailscale.Incomplete: pb.TailscaleDiscoveryState_TAILSCALE_DISCOVERY_STATE_INCOMPLETE}[d.State]
	v := &pb.ReadTailscaleDevicesResponse{State: state, HttpsReady: d.HTTPSReady}
	if d.Self != nil {
		v.Self = peerMessage(*d.Self)
	}
	for _, p := range d.Peers {
		v.Peers = append(v.Peers, peerMessage(p))
	}
	return connect.NewResponse(v), nil
}
func (s *Service) selectedTailscalePeer(ctx context.Context, id string) (tailscale.Peer, error) {
	if err := s.tailscaleLocal(ctx); err != nil {
		return tailscale.Peer{}, err
	}
	d, err := tailscale.Discover(ctx, s.tailscale.runner)
	if err != nil {
		return tailscale.Peer{}, err
	}
	if d.State != tailscale.Ready {
		return tailscale.Peer{}, domain.Fail(domain.Unavailable, "Tailscale devices are unavailable.", "Refresh the original discovery.")
	}
	for _, p := range d.Peers {
		if p.ID == id {
			return p, nil
		}
	}
	return tailscale.Peer{}, domain.Fail(domain.NotFound, "The selected peer is no longer visible.", "Refresh the original device list.")
}
func (s *Service) CheckTailscaleDevice(ctx context.Context, req *connect.Request[pb.CheckTailscaleDeviceRequest]) (*connect.Response[pb.CheckTailscaleDeviceResponse], error) {
	p, err := s.selectedTailscalePeer(ctx, req.Msg.PeerId)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	v, err := tailscale.Check(ctx, p, nil)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	state := map[tailscale.PeerState]pb.TailscalePeerState{tailscale.NotChecked: pb.TailscalePeerState_TAILSCALE_PEER_STATE_NOT_CHECKED, tailscale.Accepting: pb.TailscalePeerState_TAILSCALE_PEER_STATE_ACCEPTING, tailscale.Offline: pb.TailscalePeerState_TAILSCALE_PEER_STATE_OFFLINE, tailscale.TLSFailure: pb.TailscalePeerState_TAILSCALE_PEER_STATE_TLS_FAILURE, tailscale.Blocked: pb.TailscalePeerState_TAILSCALE_PEER_STATE_BLOCKED, tailscale.Unsupported: pb.TailscalePeerState_TAILSCALE_PEER_STATE_UNSUPPORTED}[v.State]
	return connect.NewResponse(&pb.CheckTailscaleDeviceResponse{State: state, ServerId: string(v.ServerID)}), nil
}
func accessMessage(a tailscale.Access) *pb.GetTailscaleAccessResponse {
	state := map[tailscale.AccessState]pb.TailscaleAccessState{tailscale.AccessOff: pb.TailscaleAccessState_TAILSCALE_ACCESS_STATE_OFF, tailscale.AccessStarting: pb.TailscaleAccessState_TAILSCALE_ACCESS_STATE_STARTING, tailscale.AccessReady: pb.TailscaleAccessState_TAILSCALE_ACCESS_STATE_READY, tailscale.AccessUnavailable: pb.TailscaleAccessState_TAILSCALE_ACCESS_STATE_UNAVAILABLE, tailscale.AccessCleanupRequired: pb.TailscaleAccessState_TAILSCALE_ACCESS_STATE_CLEANUP_REQUIRED}[a.State]
	return &pb.GetTailscaleAccessResponse{State: state, Origin: a.Origin, Enabled: a.Enabled}
}
func (s *Service) GetTailscaleAccess(ctx context.Context, req *connect.Request[pb.GetTailscaleAccessRequest]) (*connect.Response[pb.GetTailscaleAccessResponse], error) {
	if err := s.tailscaleLocal(ctx); err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	v, err := s.tailscale.access.Status()
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(accessMessage(v)), nil
}
func (s *Service) SetTailscaleAccess(ctx context.Context, req *connect.Request[pb.SetTailscaleAccessRequest]) (*connect.Response[pb.SetTailscaleAccessResponse], error) {
	if err := s.tailscaleLocal(ctx); err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	v, err := s.tailscale.access.Set(ctx, domain.ID(req.Msg.RequestId), req.Msg.Enable, domain.ID(req.Msg.ServerId), req.Msg.Origin)
	s.logger.InfoContext(ctx, "tailscale_access_operation", "operation_id", req.Msg.RequestId, "enable", req.Msg.Enable, "failed", err != nil)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.SetTailscaleAccessResponse{State: accessMessage(v).State, Origin: v.Origin, Enabled: v.Enabled}), nil
}
func approvalMessage(a tailscale.Approval) *pb.TailscaleConnection {
	role := pb.DeviceType_DEVICE_TYPE_CLIENT
	if a.Input.Role == domain.WorkerDevice {
		role = pb.DeviceType_DEVICE_TYPE_WORKER
	}
	state := map[tailscale.ApprovalState]pb.TailscaleApprovalState{tailscale.ApprovalPending: pb.TailscaleApprovalState_TAILSCALE_APPROVAL_STATE_PENDING, tailscale.ApprovalApproved: pb.TailscaleApprovalState_TAILSCALE_APPROVAL_STATE_APPROVED, tailscale.ApprovalDenied: pb.TailscaleApprovalState_TAILSCALE_APPROVAL_STATE_DENIED, tailscale.ApprovalExpired: pb.TailscaleApprovalState_TAILSCALE_APPROVAL_STATE_EXPIRED, tailscale.ApprovalCanceled: pb.TailscaleApprovalState_TAILSCALE_APPROVAL_STATE_CANCELED}[a.State]
	return &pb.TailscaleConnection{RequestId: string(a.Input.ID), RequesterName: a.Input.RequesterName, RequesterKey: a.Input.RequesterKey, TargetKey: a.TargetKey, ServerId: string(a.Input.ServerID), Origin: a.Input.Origin, Role: role, ConfirmationCode: a.Code, ExpiresAt: a.ExpiresAt.Format(time.RFC3339Nano), State: state, EncryptedGrant: a.EncryptedGrant, DecisionId: string(a.DecisionID), DecisionAllow: a.DecisionAllow, WorkerServerId: string(a.Input.WorkerServerID), WorkerServerOrigin: a.Input.WorkerServerOrigin}
}
func (s *Service) RequestTailscaleConnection(ctx context.Context, req *connect.Request[pb.RequestTailscaleConnectionRequest]) (*connect.Response[pb.RequestTailscaleConnectionResponse], error) {
	a, err := s.tailscaleApprovals(ctx)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	role, err := deviceType(req.Msg.Role)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	input := tailscale.ApprovalInput{ObservedTargetKey: req.Msg.ObservedTargetKey, ID: domain.ID(req.Msg.RequestId), RequesterName: req.Msg.RequesterName, RequesterKey: req.Msg.RequesterKey, ServerID: domain.ID(req.Msg.ServerId), Origin: req.Msg.Origin, Role: role, WorkerServerID: domain.ID(req.Msg.WorkerServerId), WorkerServerOrigin: req.Msg.WorkerServerOrigin}
	if previous, e := a.Get(input.ID, input.RequesterKey); e == nil {
		v, e := a.Request(input)
		if e != nil {
			return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
		}
		_ = previous
		return connect.NewResponse(&pb.RequestTailscaleConnectionResponse{Connection: approvalMessage(v)}), nil
	}
	if !ecdhPublicEqual(req.Msg.ObservedTargetKey, s.tailscale.offer) {
		return nil, rpc.Error(domain.Fail(domain.Conflict, "The original target capability changed.", "Check the target before starting a new explicit request."), req.Header().Get(rpc.CorrelationHeader))
	}
	v, err := a.Request(input)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	s.logger.InfoContext(ctx, "tailscale_approval_requested", "operation_id", input.ID, "role", role)
	return connect.NewResponse(&pb.RequestTailscaleConnectionResponse{Connection: approvalMessage(v)}), nil
}
func ecdhPublicEqual(a, b []byte) bool {
	if len(a) != 32 || len(b) != 32 {
		return false
	}
	var diff byte
	for n := range a {
		diff |= a[n] ^ b[n]
	}
	return diff == 0
}
func (s *Service) GetTailscaleConnection(ctx context.Context, req *connect.Request[pb.GetTailscaleConnectionRequest]) (*connect.Response[pb.GetTailscaleConnectionResponse], error) {
	a, err := s.tailscaleApprovals(ctx)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	v, err := a.Get(domain.ID(req.Msg.RequestId), req.Msg.RequesterKey)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.GetTailscaleConnectionResponse{Connection: approvalMessage(v)}), nil
}
func (s *Service) CancelTailscaleConnection(ctx context.Context, req *connect.Request[pb.CancelTailscaleConnectionRequest]) (*connect.Response[pb.CancelTailscaleConnectionResponse], error) {
	a, err := s.tailscaleApprovals(ctx)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	v, err := a.Cancel(domain.ID(req.Msg.RequestId), req.Msg.RequesterKey)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.CancelTailscaleConnectionResponse{Connection: approvalMessage(v)}), nil
}
func (s *Service) ListTailscaleConnections(ctx context.Context, req *connect.Request[pb.ListTailscaleConnectionsRequest]) (*connect.Response[pb.ListTailscaleConnectionsResponse], error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) || s.tailscale == nil {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Original client authority is required.", "Use the authenticated target connection."), req.Header().Get(rpc.CorrelationHeader))
	}
	access, err := s.tailscale.access.Status()
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	s.tailscale.approvals.SetOrigin(access.Origin)
	values, err := s.tailscale.approvals.List()
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := &pb.ListTailscaleConnectionsResponse{}
	for _, v := range values {
		if access.State != tailscale.AccessReady && !(v.State == tailscale.ApprovalApproved && v.Input.Role == domain.WorkerDevice) {
			continue
		}
		response.Connections = append(response.Connections, approvalMessage(v))
	}
	return connect.NewResponse(response), nil
}
func (s *Service) DecideTailscaleConnection(ctx context.Context, req *connect.Request[pb.DecideTailscaleConnectionRequest]) (*connect.Response[pb.DecideTailscaleConnectionResponse], error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) || s.tailscale == nil || s.tailscale.access.Origin() == "" {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Original target client approval is required.", "Approve from the authenticated target connection."), req.Header().Get(rpc.CorrelationHeader))
	}
	a := s.tailscale.approvals
	origin := s.tailscale.access.Origin()
	a.SetOrigin(origin)
	v, err := a.Decide(ctx, domain.ID(req.Msg.RequestId), domain.ID(req.Msg.DecisionId), req.Msg.ConfirmationCode, req.Msg.Allow, string(actor.Type)+":"+string(actor.DeviceID), func(ctx context.Context, id domain.ID, code string, role domain.DeviceType, digest []byte) ([]byte, error) {
		if role == domain.WorkerDevice {
			return json.Marshal(struct {
				RequestID domain.ID `json:"request_id"`
				Token     string    `json:"token"`
			}{domain.ID(req.Msg.RequestId), code})
		}
		response, err := s.CreatePairing(ctx, connect.NewRequest(&pb.CreatePairingRequest{RequestId: string(id), Name: "Approved Tailscale client", Type: pb.DeviceType_DEVICE_TYPE_CLIENT, CodeDigest: digest}))
		if err != nil {
			return nil, err
		}
		return json.Marshal(worker.PairingCode{Version: 1, PairingID: domain.ID(response.Msg.Pairing.Id), ServerID: s.Identity.ServerID, Endpoint: origin, Code: code})
	})
	s.logger.InfoContext(ctx, "tailscale_approval_decided", "operation_id", req.Msg.RequestId, "allowed", req.Msg.Allow, "failed", err != nil)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.DecideTailscaleConnectionResponse{Connection: approvalMessage(v)}), nil
}

var _ delidevv1connect.TailscaleServiceHandler = (*Service)(nil)
