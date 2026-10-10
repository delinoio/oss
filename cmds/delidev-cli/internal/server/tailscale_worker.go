// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/tailscale"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func outgoingApproval(v *pb.TailscaleConnection, original *pb.RequestTailscaleConnectionRequest) tailscale.Approval {
	role := domain.ClientDevice
	if v.Role == pb.DeviceType_DEVICE_TYPE_WORKER {
		role = domain.WorkerDevice
	}
	return tailscale.Approval{Input: tailscale.ApprovalInput{ObservedTargetKey: original.ObservedTargetKey, ID: domain.ID(v.RequestId), RequesterName: v.RequesterName, RequesterKey: v.RequesterKey, ServerID: domain.ID(v.ServerId), Origin: v.Origin, Role: role, WorkerServerID: domain.ID(v.WorkerServerId), WorkerServerOrigin: v.WorkerServerOrigin}, TargetKey: v.TargetKey, Code: v.ConfirmationCode, State: tailscale.ApprovalApproved, EncryptedGrant: v.EncryptedGrant}
}
func (s *Service) completeOutgoingWorker(ctx context.Context, id string, value *tailscaleOutgoing, v *pb.TailscaleConnection, original *pb.RequestTailscaleConnectionRequest) (*pb.CompleteTailscalePeerConnectionResponse, error) {
	if value.Completed {
		return &pb.CompleteTailscalePeerConnectionResponse{WorkerDeviceId: string(value.WorkerDevice), Completed: true}, nil
	}
	approved := outgoingApproval(v, original)
	if approved.Input.WorkerServerID != s.Identity.ServerID || s.tailscale.access.Origin() != approved.Input.WorkerServerOrigin {
		return nil, installationFailure(domain.Conflict)
	}
	if len(value.WorkerDelivery) == 0 {
		raw, err := tailscale.OpenGrant(value.PrivateKey, approved)
		if err != nil {
			return nil, err
		}
		defer clear(raw)
		var ticket struct {
			RequestID domain.ID `json:"request_id"`
			Token     string    `json:"token"`
		}
		if domain.Decode(raw, &ticket) != nil || ticket.RequestID != domain.ID(id) {
			return nil, installationFailure(domain.Conflict)
		}
		if value.PairingRequest == "" {
			code, err := worker.RandomToken()
			if err != nil {
				return nil, err
			}
			value.PairingCode = code
			value.PairingRequest = domain.NewID()
			if err = s.saveOutgoing(id, *value); err != nil {
				return nil, err
			}
		}
		digest := sha256.Sum256([]byte(value.PairingCode))
		response, err := s.CreatePairing(ctx, connect.NewRequest(&pb.CreatePairingRequest{RequestId: string(value.PairingRequest), Name: "Approved Tailscale Worker", Type: pb.DeviceType_DEVICE_TYPE_WORKER, CodeDigest: digest[:]}))
		if err != nil {
			return nil, err
		}
		grant := worker.PairingCode{Version: 1, PairingID: domain.ID(response.Msg.Pairing.Id), ServerID: s.Identity.ServerID, Endpoint: approved.Input.WorkerServerOrigin, Code: value.PairingCode}
		grantBytes, err := json.Marshal(grant)
		if err != nil {
			return nil, err
		}
		delivery, err := json.Marshal(tailscale.WorkerDelivery{Token: ticket.Token, Grant: grantBytes})
		clear(grantBytes)
		if err != nil {
			return nil, err
		}
		defer clear(delivery)
		value.WorkerDelivery, err = tailscale.SealWorkerDelivery(value.PrivateKey, approved, delivery)
		if err != nil {
			return nil, err
		}
		if err = s.saveOutgoing(id, *value); err != nil {
			return nil, err
		}
	}
	response, err := peerClient(value.Peer.Origin).DeliverTailscaleWorkerGrant(ctx, connect.NewRequest(&pb.DeliverTailscaleWorkerGrantRequest{RequestId: id, RequesterKey: original.RequesterKey, EncryptedGrant: value.WorkerDelivery}))
	if err != nil {
		return nil, err
	}
	if !response.Msg.Completed || domain.ID(response.Msg.WorkerDeviceId).Validate() != nil {
		return nil, installationFailure(domain.RecoveryRequired)
	}
	value.WorkerDevice = domain.ID(response.Msg.WorkerDeviceId)
	value.Completed = true
	if err = s.saveOutgoing(id, *value); err != nil {
		return nil, err
	}
	return &pb.CompleteTailscalePeerConnectionResponse{WorkerDeviceId: string(value.WorkerDevice), Completed: true}, nil
}
func (s *Service) DeliverTailscaleWorkerGrant(ctx context.Context, req *connect.Request[pb.DeliverTailscaleWorkerGrantRequest]) (*connect.Response[pb.DeliverTailscaleWorkerGrantResponse], error) {
	a, err := s.tailscaleApprovals(ctx)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	device, err := a.DeliverWorker(ctx, domain.ID(req.Msg.RequestId), req.Msg.RequesterKey, req.Msg.EncryptedGrant, func(ctx context.Context, input tailscale.ApprovalInput, raw json.RawMessage, fresh bool) (domain.ID, error) {
		var grant worker.PairingCode
		if domain.Decode(raw, &grant) != nil || grant.Validate() != nil || grant.ServerID != input.WorkerServerID || grant.Endpoint != input.WorkerServerOrigin {
			return "", installationFailure(domain.Conflict)
		}
		scope := filepath.Join(s.Store.Root(), "tailscale-workers", string(input.ID), "worker")
		if err := security.PrivateDir(filepath.Dir(scope)); err != nil {
			return "", err
		}
		var credential worker.Credential
		var err error
		if fresh {
			credential, err = worker.Pair(ctx, scope, grant, domain.WorkerDevice, "Tailscale Worker")
		} else {
			credential, err = worker.RetryPair(ctx, scope, grant, domain.WorkerDevice, "Tailscale Worker")
		}
		if err != nil {
			return "", err
		}
		// A retained start fence makes lost replies reconcile the original generation;
		// the same approval cannot start a replacement after exit or lost evidence.
		fence := filepath.Join(filepath.Dir(scope), "start-request.json")
		_, existing := security.ReadPrivate(fence, 8<<10)
		if existing != nil && !os.IsNotExist(existing) {
			return "", existing
		}
		if os.IsNotExist(existing) {
			if !fresh {
				return "", installationFailure(domain.RecoveryRequired)
			}
			marker, _ := json.Marshal(struct {
				Device domain.ID `json:"device"`
			}{credential.DeviceID})
			if err = security.WriteAtomic(fence, marker); err != nil {
				return "", err
			}
			executable, err := os.Executable()
			if err != nil {
				return "", err
			}
			bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			command := exec.CommandContext(bounded, executable, "--json", "--data-dir", s.Store.Root(), "worker", "start", "--worker-dir", scope, "--detach")
			command.Stdout, command.Stderr = io.Discard, io.Discard
			// This installed controller revalidates signed update authority and retains
			// its exact startup generation. The detached Worker owns its own lifetime.
			_ = command.Run()
		}
		status, err := worker.Status(scope)
		if err != nil {
			return "", err
		}
		if status.State != worker.StateRunning {
			return "", installationFailure(domain.RecoveryRequired)
		}
		return credential.DeviceID, nil
	})
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	s.logger.InfoContext(ctx, "tailscale_worker_delivery_completed", "operation_id", req.Msg.RequestId)
	return connect.NewResponse(&pb.DeliverTailscaleWorkerGrantResponse{WorkerDeviceId: string(device), Completed: true}), nil
}
