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
	if approved.Input.WorkerServerID != s.Identity.ServerID || len(value.WorkerDelivery) == 0 && s.tailscale.access.Origin() != approved.Input.WorkerServerOrigin {
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
		markerRaw, existing := security.ReadPrivate(fence, 8<<10)
		if existing == nil {
			var marker struct {
				Device domain.ID `json:"device"`
			}
			if domain.Decode(markerRaw, &marker) != nil || marker.Device != credential.DeviceID {
				return "", installationFailure(domain.RecoveryRequired)
			}
		}
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

// Worker controls address only the credential captured by an original approved
// delivery. Explicit Start may reserve a new controller generation, but it never
// pairs a replacement device or expands the captured server authority.
func (s *Service) ownedTailscaleWorker(input tailscale.ApprovalInput) (string, worker.RuntimeStatus, error) {
	scope := filepath.Join(s.Store.Root(), "tailscale-workers", string(input.ID), "worker")
	credential, err := worker.LoadCredential(scope)
	if err != nil {
		return "", worker.RuntimeStatus{}, err
	}
	if credential.Type != domain.WorkerDevice || credential.ServerID != input.WorkerServerID || credential.Endpoint != input.WorkerServerOrigin {
		return "", worker.RuntimeStatus{}, installationFailure(domain.RecoveryRequired)
	}
	markerRaw, err := security.ReadPrivate(filepath.Join(filepath.Dir(scope), "start-request.json"), 8<<10)
	var marker struct {
		Device domain.ID `json:"device"`
	}
	if err != nil || domain.Decode(markerRaw, &marker) != nil || marker.Device != credential.DeviceID {
		return "", worker.RuntimeStatus{}, installationFailure(domain.RecoveryRequired)
	}
	status, err := worker.Status(scope)
	return scope, status, err
}
func tailscaleWorkerState(v worker.RuntimeStatus) pb.TailscaleWorkerState {
	switch v.State {
	case worker.StateIdle:
		return pb.TailscaleWorkerState_TAILSCALE_WORKER_STATE_NOT_STARTED
	case worker.StateStarting:
		return pb.TailscaleWorkerState_TAILSCALE_WORKER_STATE_STARTING
	case worker.StateRunning:
		return pb.TailscaleWorkerState_TAILSCALE_WORKER_STATE_RUNNING
	case worker.StateStopping:
		return pb.TailscaleWorkerState_TAILSCALE_WORKER_STATE_STOPPING
	case worker.StateExited:
		if v.Lifecycle.Desired == worker.WorkerStopped {
			return pb.TailscaleWorkerState_TAILSCALE_WORKER_STATE_STOPPED
		}
		return pb.TailscaleWorkerState_TAILSCALE_WORKER_STATE_EXITED
	default:
		return pb.TailscaleWorkerState_TAILSCALE_WORKER_STATE_UNCERTAIN
	}
}
func (s *Service) GetTailscaleWorker(ctx context.Context, req *connect.Request[pb.GetTailscaleWorkerRequest]) (*connect.Response[pb.GetTailscaleWorkerResponse], error) {
	if err := s.tailscaleLocal(ctx); err != nil {
		return nil, rpc.Error(err, "")
	}
	access, err := s.tailscale.access.Status()
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	s.tailscale.approvals.SetOrigin(access.Origin)
	input, err := s.tailscale.approvals.OwnedWorker(domain.ID(req.Msg.ApprovalRequestId))
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	_, status, err := s.ownedTailscaleWorker(input)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	return connect.NewResponse(&pb.GetTailscaleWorkerResponse{State: tailscaleWorkerState(status), Generation: string(status.Lifecycle.Generation)}), nil
}
func (s *Service) controlTailscaleWorker(ctx context.Context, approval, request, generation string, action tailscale.WorkerAction) (worker.RuntimeStatus, error) {
	if err := s.tailscaleLocal(ctx); err != nil {
		return worker.RuntimeStatus{}, err
	}
	access, err := s.tailscale.access.Status()
	if err != nil {
		return worker.RuntimeStatus{}, err
	}
	s.tailscale.approvals.SetOrigin(access.Origin)
	actor, _ := domain.PrincipalFrom(ctx)
	var status worker.RuntimeStatus
	err = s.tailscale.approvals.ControlWorker(ctx, domain.ID(approval), tailscale.WorkerControl{ID: domain.ID(request), Action: action, Generation: domain.ID(generation), Actor: string(actor.Type) + ":" + string(actor.DeviceID)}, func(ctx context.Context, input tailscale.ApprovalInput, phase tailscale.WorkerControlPhase) error {
		scope, current, err := s.ownedTailscaleWorker(input)
		if err != nil {
			return err
		}
		status = current
		if phase == tailscale.WorkerObserve {
			return nil
		}
		if current.Lifecycle.Generation != domain.ID(generation) {
			return installationFailure(domain.Conflict)
		}
		if action == tailscale.WorkerStart && current.State != worker.StateIdle && current.State != worker.StateExited {
			return installationFailure(domain.Conflict)
		}
		if action == tailscale.WorkerStop && generation == "" {
			return installationFailure(domain.Conflict)
		}
		if phase == tailscale.WorkerValidate {
			return nil
		}
		if action == tailscale.WorkerStop {
			err = worker.RequestStop(scope, domain.ID(generation))
		} else {
			if s.tailscale.startWorker != nil {
				err = s.tailscale.startWorker(ctx, scope)
			} else {
				err = s.launchTailscaleWorker(ctx, scope)
			}
		}
		if err != nil {
			return err
		}
		_, status, err = s.ownedTailscaleWorker(input)
		return err
	})
	s.logger.InfoContext(ctx, "tailscale_worker_control", "operation_id", request, "action", action, "failed", err != nil)
	return status, err
}
func (s *Service) launchTailscaleWorker(ctx context.Context, scope string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	command := exec.CommandContext(bounded, executable, "--json", "--data-dir", s.Store.Root(), "worker", "start", "--worker-dir", scope, "--detach")
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if command.Run() != nil {
		return installationFailure(domain.RecoveryRequired)
	}
	return nil
}
func (s *Service) StartTailscaleWorker(ctx context.Context, req *connect.Request[pb.StartTailscaleWorkerRequest]) (*connect.Response[pb.StartTailscaleWorkerResponse], error) {
	status, err := s.controlTailscaleWorker(ctx, req.Msg.ApprovalRequestId, req.Msg.RequestId, req.Msg.ExpectedGeneration, tailscale.WorkerStart)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	return connect.NewResponse(&pb.StartTailscaleWorkerResponse{State: tailscaleWorkerState(status), Generation: string(status.Lifecycle.Generation), RequestId: req.Msg.RequestId}), nil
}
func (s *Service) StopTailscaleWorker(ctx context.Context, req *connect.Request[pb.StopTailscaleWorkerRequest]) (*connect.Response[pb.StopTailscaleWorkerResponse], error) {
	status, err := s.controlTailscaleWorker(ctx, req.Msg.ApprovalRequestId, req.Msg.RequestId, req.Msg.ExpectedGeneration, tailscale.WorkerStop)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	return connect.NewResponse(&pb.StopTailscaleWorkerResponse{State: tailscaleWorkerState(status), Generation: string(status.Lifecycle.Generation), RequestId: req.Msg.RequestId}), nil
}
