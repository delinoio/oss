// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (s *Service) GetStatus(_ context.Context, req *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
	response := connect.NewResponse(&pb.GetStatusResponse{Version: rpc.Version, ProtocolVersion: rpc.ProtocolVersion, SchemaVersion: store.SchemaVersion, ServerId: string(s.Identity.ServerID), Listener: s.Endpoint.URL, StartedAt: s.Endpoint.StartedAt.Format(time.RFC3339Nano), Stopping: s.stopping.Load(), Capabilities: []pb.SystemCapability{pb.SystemCapability_SYSTEM_CAPABILITY_AUTOMATIC_TITLES_V1, pb.SystemCapability_SYSTEM_CAPABILITY_PERMANENT_SESSION_DELETION_V1, pb.SystemCapability_SYSTEM_CAPABILITY_SESSION_FORWARDING_V1, pb.SystemCapability_SYSTEM_CAPABILITY_USER_SERVICES_V1, pb.SystemCapability_SYSTEM_CAPABILITY_MANAGED_BACKUP_RESTORE_V1, pb.SystemCapability_SYSTEM_CAPABILITY_NATIVE_ACCOUNTING_V1}})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) StopServer(ctx context.Context, req *connect.Request[pb.StopServerRequest]) (*connect.Response[pb.StopServerResponse], error) {
	id := domain.ID(req.Msg.RequestId)
	lock, err := LockLifecycle(s.Store.Root())
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	defer lock.Close()
	_, err = s.Store.Mutate(ctx, id, "server.stop", struct {
		StartedAt time.Time `json:"started_at"`
	}{s.Endpoint.StartedAt}, func(*store.Tx) (any, error) {
		if err := writeStopped(s.Store.Root(), id, configurationDigest(Config{})); err != nil {
			return nil, err
		}
		return struct {
			Accepted bool `json:"accepted"`
		}{true}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	s.stopping.Store(true)
	// Let the accepted response flush before canceling connection contexts. The
	// durable receipt already prevents an ambiguous client retry from duplicating.
	time.AfterFunc(100*time.Millisecond, s.stop)
	response := connect.NewResponse(&pb.StopServerResponse{RequestId: string(id)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
