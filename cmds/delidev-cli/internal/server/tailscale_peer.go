// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/connections"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/tailscale"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/encoding/protojson"
)

type tailscaleOutgoing struct {
	CancellationRequested bool             `json:"cancellation_requested,omitempty"`
	PairingRequest        domain.ID        `json:"pairing_request,omitempty"`
	PairingCode           string           `json:"pairing_code,omitempty"`
	WorkerDelivery        []byte           `json:"worker_delivery,omitempty"`
	WorkerDevice          domain.ID        `json:"worker_device,omitempty"`
	Server                domain.ID        `json:"server"`
	Actor                 domain.Principal `json:"actor"`
	Peer                  tailscale.Peer   `json:"peer"`
	Request               []byte           `json:"request"`
	PrivateKey            []byte           `json:"private_key"`
	Response              []byte           `json:"response,omitempty"`
	Profile               domain.ID        `json:"profile"`
	Completed             bool             `json:"completed"`
}

func (s *Service) outgoingPath(id string) string {
	return filepath.Join(s.Store.Root(), "tailscale-outgoing", id+".json")
}
func (s *Service) outgoingLock(ctx context.Context) (*security.Lock, error) {
	if err := s.tailscaleLocal(ctx); err != nil {
		return nil, err
	}
	return security.TryLock(filepath.Join(s.Store.Root(), "tailscale-outgoing.lock"))
}
func (s *Service) readOutgoing(ctx context.Context, id string) (tailscaleOutgoing, error) {
	var value tailscaleOutgoing
	if domain.ID(id).Validate() != nil {
		return value, installationFailure(domain.InvalidArgument)
	}
	raw, err := security.ReadPrivate(s.outgoingPath(id), 64<<10)
	if err != nil {
		return value, err
	}
	actor, _ := domain.PrincipalFrom(ctx)
	if domain.Decode(raw, &value) != nil || value.Server != s.Identity.ServerID || value.Actor != actor || value.Profile.Validate() != nil || len(value.PrivateKey) != 32 || tailscale.ValidateOrigin(value.Peer.Origin) != nil {
		return value, installationFailure(domain.RecoveryRequired)
	}
	var request pb.RequestTailscaleConnectionRequest
	if protojson.Unmarshal(value.Request, &request) != nil || request.RequestId != id || request.Origin != value.Peer.Origin || value.Completed && request.Role == pb.DeviceType_DEVICE_TYPE_WORKER && value.WorkerDevice.Validate() != nil {
		return value, installationFailure(domain.RecoveryRequired)
	}
	key, err := ecdh.X25519().NewPrivateKey(value.PrivateKey)
	if err != nil || !ecdhPublicEqual(key.PublicKey().Bytes(), request.RequesterKey) {
		return value, installationFailure(domain.RecoveryRequired)
	}
	return value, nil
}
func (s *Service) saveOutgoing(id string, value tailscaleOutgoing) error {
	if err := security.PrivateDir(filepath.Dir(s.outgoingPath(id))); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	defer clear(raw)
	return security.WriteAtomic(s.outgoingPath(id), raw)
}
func peerClient(origin string) delidevv1connect.TailscaleServiceClient {
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 16 << 10}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return delidevv1connect.NewTailscaleServiceClient(client, origin, connect.WithReadMaxBytes(32<<10))
}
func (s *Service) updateOutgoing(ctx context.Context, id string, value *tailscaleOutgoing, start bool) (*pb.TailscaleConnectionResponse, error) {
	var original pb.RequestTailscaleConnectionRequest
	if protojson.Unmarshal(value.Request, &original) != nil {
		return nil, installationFailure(domain.RecoveryRequired)
	}
	var v *pb.TailscaleConnection
	var err error
	if value.CancellationRequested {
		response, problem := peerClient(value.Peer.Origin).CancelTailscaleConnection(ctx, connect.NewRequest(&pb.CancelTailscaleConnectionRequest{RequestId: id, RequesterKey: original.RequesterKey}))
		err = problem
		if problem == nil {
			v = response.Msg.Connection
		}
	} else if start || len(value.Response) == 0 {
		response, problem := peerClient(value.Peer.Origin).RequestTailscaleConnection(ctx, connect.NewRequest(&original))
		err = problem
		if problem == nil {
			v = response.Msg.Connection
		}
	} else {
		response, problem := peerClient(value.Peer.Origin).GetTailscaleConnection(ctx, connect.NewRequest(&pb.GetTailscaleConnectionRequest{RequestId: id, RequesterKey: original.RequesterKey}))
		err = problem
		if problem == nil {
			v = response.Msg.Connection
		}
	}
	if err != nil {
		return nil, err
	}
	response := &pb.TailscaleConnectionResponse{Connection: v}
	if v == nil || v.RequestId != id || v.ServerId != original.ServerId || v.Origin != original.Origin || v.Role != original.Role || v.RequesterName != original.RequesterName || !ecdhPublicEqual(v.RequesterKey, original.RequesterKey) || len(v.TargetKey) != 32 || v.WorkerServerId != original.WorkerServerId || v.WorkerServerOrigin != original.WorkerServerOrigin {
		return nil, installationFailure(domain.Conflict)
	}
	if len(value.Response) > 0 {
		var previous pb.TailscaleConnectionResponse
		if protojson.Unmarshal(value.Response, &previous) != nil || previous.Connection == nil || !ecdhPublicEqual(previous.Connection.TargetKey, v.TargetKey) || previous.Connection.ConfirmationCode != v.ConfirmationCode {
			return nil, installationFailure(domain.Conflict)
		}
	}
	raw, err := protojson.Marshal(response)
	if err != nil {
		return nil, err
	}
	value.Response = raw
	if err = s.saveOutgoing(id, *value); err != nil {
		return nil, err
	}
	// The renderer receives confirmation and status. Protected grants stay in Go.
	v.EncryptedGrant = nil
	return response, nil
}
func (s *Service) StartTailscalePeerConnection(ctx context.Context, req *connect.Request[pb.StartTailscalePeerConnectionRequest]) (*connect.Response[pb.StartTailscalePeerConnectionResponse], error) {
	lock, err := s.outgoingLock(ctx)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	defer lock.Close()
	id := req.Msg.RequestId
	if domain.ID(id).Validate() != nil || domain.ID(req.Msg.TargetServerId).Validate() != nil || tailscale.ValidateOrigin(req.Msg.TargetOrigin) != nil {
		return nil, rpc.Error(installationFailure(domain.InvalidArgument), "")
	}
	value, err := s.readOutgoing(ctx, id)
	if errors.Is(err, os.ErrNotExist) {
		entries, problem := os.ReadDir(filepath.Dir(s.outgoingPath(id)))
		if problem != nil && !os.IsNotExist(problem) {
			return nil, rpc.Error(problem, "")
		}
		if len(entries) >= 256 {
			return nil, rpc.Error(installationFailure(domain.ResourceExhausted), "")
		}
		peer, e := s.selectedTailscalePeer(ctx, req.Msg.PeerId)
		if e != nil {
			return nil, rpc.Error(e, "")
		}
		if peer.Origin != req.Msg.TargetOrigin {
			return nil, rpc.Error(installationFailure(domain.Conflict), "")
		}
		check, e := tailscale.Check(ctx, peer, nil)
		if e != nil || check.State != tailscale.Accepting || string(check.ServerID) != req.Msg.TargetServerId {
			return nil, rpc.Error(installationFailure(domain.Conflict), "")
		}
		role, e := deviceType(req.Msg.Role)
		if e != nil || (role != domain.ClientDevice && role != domain.WorkerDevice) {
			return nil, rpc.Error(installationFailure(domain.InvalidArgument), "")
		}
		key, e := ecdh.X25519().GenerateKey(rand.Reader)
		if e != nil {
			return nil, rpc.Error(e, "")
		}
		actor, _ := domain.PrincipalFrom(ctx)
		original := &pb.RequestTailscaleConnectionRequest{RequestId: id, RequesterName: "DeliDev desktop", RequesterKey: key.PublicKey().Bytes(), ServerId: string(check.ServerID), Origin: peer.Origin, Role: req.Msg.Role, ObservedTargetKey: check.Key}
		if role == domain.WorkerDevice {
			origin := s.tailscale.access.Origin()
			if origin == "" {
				return nil, rpc.Error(domain.Fail(domain.Unavailable, "Enable the current server's Tailscale HTTPS access first.", "The selected target must join this original server."), "")
			}
			original.WorkerServerId = string(s.Identity.ServerID)
			original.WorkerServerOrigin = origin
		}
		wire, e := protojson.Marshal(original)
		if e != nil {
			return nil, rpc.Error(e, "")
		}
		value = tailscaleOutgoing{Server: s.Identity.ServerID, Actor: actor, Peer: peer, Request: wire, PrivateKey: key.Bytes(), Profile: domain.NewID()}
		if e = s.saveOutgoing(id, value); e != nil {
			return nil, rpc.Error(e, "")
		}
	} else if err != nil {
		return nil, rpc.Error(err, "")
	}
	var original pb.RequestTailscaleConnectionRequest
	if protojson.Unmarshal(value.Request, &original) != nil || value.Peer.ID != req.Msg.PeerId || original.Role != req.Msg.Role || original.Origin != req.Msg.TargetOrigin || original.ServerId != req.Msg.TargetServerId {
		return nil, rpc.Error(installationFailure(domain.Conflict), "")
	}
	response, err := s.updateOutgoing(ctx, id, &value, true)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	return connect.NewResponse(&pb.StartTailscalePeerConnectionResponse{Connection: response.Connection}), nil
}
func (s *Service) PollTailscalePeerConnection(ctx context.Context, req *connect.Request[pb.PollTailscalePeerConnectionRequest]) (*connect.Response[pb.PollTailscalePeerConnectionResponse], error) {
	lock, err := s.outgoingLock(ctx)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	defer lock.Close()
	value, err := s.readOutgoing(ctx, req.Msg.RequestId)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	response, err := s.updateOutgoing(ctx, req.Msg.RequestId, &value, false)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	return connect.NewResponse(&pb.PollTailscalePeerConnectionResponse{Connection: response.Connection}), nil
}
func (s *Service) CompleteTailscalePeerConnection(ctx context.Context, req *connect.Request[pb.CompleteTailscalePeerConnectionRequest]) (*connect.Response[pb.CompleteTailscalePeerConnectionResponse], error) {
	lock, err := s.outgoingLock(ctx)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	defer lock.Close()
	value, err := s.readOutgoing(ctx, req.Msg.RequestId)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	if !value.Completed {
		if _, err = s.updateOutgoing(ctx, req.Msg.RequestId, &value, false); err != nil {
			return nil, rpc.Error(err, "")
		}
	}
	var response pb.TailscaleConnectionResponse
	if protojson.Unmarshal(value.Response, &response) != nil || response.Connection == nil {
		return nil, rpc.Error(installationFailure(domain.RecoveryRequired), "")
	}
	v := response.Connection
	if v.ConfirmationCode != req.Msg.ConfirmationCode || v.State != pb.TailscaleApprovalState_TAILSCALE_APPROVAL_STATE_APPROVED {
		return nil, rpc.Error(installationFailure(domain.Conflict), "")
	}
	if v.Role == pb.DeviceType_DEVICE_TYPE_WORKER {
		var original pb.RequestTailscaleConnectionRequest
		if protojson.Unmarshal(value.Request, &original) != nil {
			return nil, rpc.Error(installationFailure(domain.RecoveryRequired), "")
		}
		result, err := s.completeOutgoingWorker(ctx, req.Msg.RequestId, &value, v, &original)
		if err != nil {
			return nil, rpc.Error(err, "")
		}
		return connect.NewResponse(result), nil
	}
	if v.Role != pb.DeviceType_DEVICE_TYPE_CLIENT {
		return nil, rpc.Error(installationFailure(domain.InvalidArgument), "")
	}
	if !value.Completed {
		var original pb.RequestTailscaleConnectionRequest
		if protojson.Unmarshal(value.Request, &original) != nil {
			return nil, rpc.Error(installationFailure(domain.RecoveryRequired), "")
		}
		approved := tailscale.Approval{Input: tailscale.ApprovalInput{ObservedTargetKey: original.ObservedTargetKey, ID: domain.ID(v.RequestId), RequesterName: v.RequesterName, RequesterKey: v.RequesterKey, ServerID: domain.ID(v.ServerId), Origin: v.Origin, Role: domain.ClientDevice}, TargetKey: v.TargetKey, Code: v.ConfirmationCode, State: tailscale.ApprovalApproved, EncryptedGrant: v.EncryptedGrant}
		raw, e := tailscale.OpenGrant(value.PrivateKey, approved)
		if e != nil {
			return nil, rpc.Error(e, "")
		}
		defer clear(raw)
		var grant worker.PairingCode
		if domain.Decode(raw, &grant) != nil || grant.ServerID != approved.Input.ServerID || grant.Endpoint != approved.Input.Origin {
			return nil, rpc.Error(installationFailure(domain.Conflict), "")
		}
		if _, e = connections.Pair(ctx, s.Store.Root(), value.Profile, value.Peer.Name, grant); e != nil {
			return nil, rpc.Error(e, "")
		}
		value.Completed = true
		if e = s.saveOutgoing(req.Msg.RequestId, value); e != nil {
			return nil, rpc.Error(e, "")
		}
	}
	return connect.NewResponse(&pb.CompleteTailscalePeerConnectionResponse{ProfileId: string(value.Profile), Completed: true}), nil
}

func (s *Service) CancelTailscalePeerConnection(ctx context.Context, req *connect.Request[pb.CancelTailscalePeerConnectionRequest]) (*connect.Response[pb.CancelTailscalePeerConnectionResponse], error) {
	lock, err := s.outgoingLock(ctx)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	defer lock.Close()
	value, err := s.readOutgoing(ctx, req.Msg.RequestId)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	if value.Completed || len(value.WorkerDelivery) > 0 {
		return nil, rpc.Error(installationFailure(domain.Conflict), "")
	}
	value.CancellationRequested = true
	if err = s.saveOutgoing(req.Msg.RequestId, value); err != nil {
		return nil, rpc.Error(err, "")
	}
	response, err := s.updateOutgoing(ctx, req.Msg.RequestId, &value, false)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	return connect.NewResponse(&pb.CancelTailscalePeerConnectionResponse{Connection: response.Connection}), nil
}
func (s *Service) ListTailscalePeerConnections(ctx context.Context, req *connect.Request[pb.ListTailscalePeerConnectionsRequest]) (*connect.Response[pb.ListTailscalePeerConnectionsResponse], error) {
	lock, err := s.outgoingLock(ctx)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	defer lock.Close()
	entries, err := os.ReadDir(filepath.Join(s.Store.Root(), "tailscale-outgoing"))
	if os.IsNotExist(err) {
		return connect.NewResponse(&pb.ListTailscalePeerConnectionsResponse{}), nil
	}
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	if len(entries) > 256 {
		return nil, rpc.Error(installationFailure(domain.RecoveryRequired), "")
	}
	response := &pb.ListTailscalePeerConnectionsResponse{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return nil, rpc.Error(installationFailure(domain.RecoveryRequired), "")
		}
		id := entry.Name()[:len(entry.Name())-len(".json")]
		value, err := s.readOutgoing(ctx, id)
		if err != nil {
			return nil, rpc.Error(err, "")
		}
		if value.Completed {
			continue
		}
		var status pb.TailscaleConnectionResponse
		if len(value.Response) > 0 {
			if protojson.Unmarshal(value.Response, &status) != nil || status.Connection == nil {
				return nil, rpc.Error(installationFailure(domain.RecoveryRequired), "")
			}
		} else {
			var original pb.RequestTailscaleConnectionRequest
			if protojson.Unmarshal(value.Request, &original) != nil {
				return nil, rpc.Error(installationFailure(domain.RecoveryRequired), "")
			}
			status.Connection = &pb.TailscaleConnection{RequestId: id, RequesterName: original.RequesterName, ServerId: original.ServerId, Origin: original.Origin, Role: original.Role, RequesterKey: original.RequesterKey, State: pb.TailscaleApprovalState_TAILSCALE_APPROVAL_STATE_PENDING, WorkerServerId: original.WorkerServerId, WorkerServerOrigin: original.WorkerServerOrigin}
		}
		status.Connection.EncryptedGrant = nil
		response.Connections = append(response.Connections, status.Connection)
	}
	return connect.NewResponse(response), nil
}
