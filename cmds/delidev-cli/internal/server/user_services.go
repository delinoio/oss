package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/userservice"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func serviceKind(v pb.UserServiceKind) (userservice.Kind, error) {
	switch v {
	case pb.UserServiceKind_USER_SERVICE_KIND_SERVER:
		return userservice.Server, nil
	case pb.UserServiceKind_USER_SERVICE_KIND_WORKER:
		return userservice.Worker, nil
	}
	return "", domain.Fail(domain.InvalidArgument, "An explicit user service kind is required.", "Select server or the server computer's local Worker.")
}
func serviceAction(v pb.UserServiceAction) (userservice.Action, error) {
	switch v {
	case pb.UserServiceAction_USER_SERVICE_ACTION_INSTALL:
		return userservice.Install, nil
	case pb.UserServiceAction_USER_SERVICE_ACTION_START:
		return userservice.Start, nil
	case pb.UserServiceAction_USER_SERVICE_ACTION_STOP:
		return userservice.Stop, nil
	case pb.UserServiceAction_USER_SERVICE_ACTION_REMOVE:
		return userservice.Remove, nil
	}
	return "", domain.Fail(domain.InvalidArgument, "An explicit user service action is required.", "Select install, start, stop or remove.")
}
func serviceWire(s userservice.Status) *pb.UserService {
	k := pb.UserServiceKind_USER_SERVICE_KIND_SERVER
	if s.Kind == userservice.Worker {
		k = pb.UserServiceKind_USER_SERVICE_KIND_WORKER
	}
	states := map[userservice.State]pb.UserServiceState{userservice.Absent: pb.UserServiceState_USER_SERVICE_STATE_ABSENT, userservice.Stopped: pb.UserServiceState_USER_SERVICE_STATE_STOPPED, userservice.Starting: pb.UserServiceState_USER_SERVICE_STATE_STARTING, userservice.Running: pb.UserServiceState_USER_SERVICE_STATE_RUNNING, userservice.Stopping: pb.UserServiceState_USER_SERVICE_STATE_STOPPING, userservice.Uncertain: pb.UserServiceState_USER_SERVICE_STATE_UNCERTAIN}
	return &pb.UserService{Kind: k, Id: string(s.ID), Revision: s.Revision, State: states[s.State], DesiredState: states[s.Desired], LoginEnabled: s.LoginEnabled, CleanupConfirmed: s.CleanupConfirmed}
}
func (s *Service) userService(ctx context.Context, k userservice.Kind) (*userservice.Manager, string, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok {
		return nil, "", domain.Fail(domain.PermissionDenied, "Only the owner or a paired client can control user services.", "Use an authorized product client on the selected server.")
	}
	authorize := func(work context.Context) error {
		return s.Store.Read(work, func(tx *store.Tx) error { return tx.Authorize() })
	}
	if err := authorize(ctx); err != nil {
		return nil, "", err
	}
	root, err := filepath.EvalSymlinks(s.Store.Root())
	if err != nil {
		return nil, "", domain.SafeError(err)
	}
	if k == userservice.Worker {
		root = filepath.Join(root, "worker")
		credential, err := worker.LoadCredential(root)
		if err != nil {
			return nil, "", err
		}
		if domain.OwnershipBlocks(domain.OwnershipInstance, "", credential.ServerID != s.Identity.ServerID) ||
			domain.OwnershipBlocks(domain.OwnershipActor, "", credential.Type != domain.WorkerDevice) {
			return nil, "", domain.Fail(domain.PermissionDenied, "The local Worker does not belong to this server.", "Pair the server computer's separate Worker scope explicitly.")
		}
	}
	manager := userservice.New(root, k, s.logger)
	manager.Authorize = authorize
	if s.userServiceBackend != nil {
		manager.Backend = s.userServiceBackend
	}
	identity, _ := json.Marshal(struct {
		Server domain.ID
		Actor  domain.Principal
	}{s.Identity.ServerID, actor})
	return manager, string(identity), nil
}
func (s *Service) GetUserService(ctx context.Context, req *connect.Request[pb.GetUserServiceRequest]) (*connect.Response[pb.GetUserServiceResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	k, err := serviceKind(req.Msg.Kind)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	m, _, err := s.userService(ctx, k)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	status, err := m.Status(ctx)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.GetUserServiceResponse{Service: serviceWire(status)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) ControlUserService(ctx context.Context, req *connect.Request[pb.ControlUserServiceRequest]) (*connect.Response[pb.ControlUserServiceResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	correlation := req.Header().Get(rpc.CorrelationHeader)
	k, err := serviceKind(req.Msg.Kind)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	a, err := serviceAction(req.Msg.Action)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	m, actor, err := s.userService(ctx, k)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	// Native registration intent lives outside SQLite. Keep its original
	// authorization and side effects within the same lifecycle barrier as restore,
	// so replacement cannot pass a service control that is still in flight.
	lifecycle, err := LockLifecycle(s.Store.Root())
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	defer lifecycle.Close()
	if a == userservice.Install && k == userservice.Server {
		m.Options = s.userServiceOptions
	}
	result, err := m.Control(ctx, a, domain.ID(req.Msg.RequestId), req.Msg.ExpectedRevision, actor)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "user_service_control_accepted", "correlation_id", correlation, "kind", k, "action", a, "request_id", result.RequestID, "replayed", result.Replayed, "state", result.Status.State)
	response := connect.NewResponse(&pb.ControlUserServiceResponse{Service: serviceWire(result.Status), RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
