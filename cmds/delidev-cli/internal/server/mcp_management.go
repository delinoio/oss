// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/mcpmanagement"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
	"slices"
	"sync"
	"time"
)

type mcpReader struct {
	owner    workspaceReader
	gate     sync.Mutex
	requests chan *pb.McpManagementCommand
	pending  *pb.McpManagementCommand
	reply    chan *pb.ReportMcpManagementRequest
}

func mcpWorkerProof(tx *store.Tx, reader *workspaceReader) error {
	if e := currentWorkspaceReader(tx, reader); e != nil {
		return e
	}
	_, machine, e := activeMachine(tx, reader.machine)
	if e != nil {
		return e
	}
	if !slices.Contains(machine.WorkerCapabilities, domain.MCPManagementV1) {
		return domain.Fail(domain.Unsupported, "This Runner does not support MCP management.", "Update and reconnect the original Runner.")
	}
	return nil
}
func mcpUnavailable() error {
	return domain.Fail(domain.Unavailable, "The selected Runner's MCP owner is unavailable.", "Reconnect the original Runner and retry the exact management request.")
}
func (s *Service) WatchMcpManagement(ctx context.Context, req *connect.Request[pb.WatchMcpManagementRequest], stream *connect.ServerStream[pb.WatchMcpManagementResponse]) error {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if e := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); e != nil {
		return rpc.Error(e, correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	machine, instance := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)
	primary, e := s.primaryWorkspaceStream(machine, instance)
	if e != nil {
		return rpc.Error(e, correlation)
	}
	reader := &mcpReader{owner: workspaceReader{machine: machine, instance: instance, device: actor.DeviceID, primary: primary.ID, done: ctx.Done()}, requests: make(chan *pb.McpManagementCommand, 1), reply: make(chan *pb.ReportMcpManagementRequest, 1)}
	if e = s.Store.Read(ctx, func(tx *store.Tx) error { return mcpWorkerProof(tx, &reader.owner) }); e != nil {
		return rpc.Error(e, correlation)
	}
	s.mcpMu.Lock()
	if s.mcpReaders == nil {
		s.mcpReaders = map[domain.ID]*mcpReader{}
	}
	if s.mcpReaders[machine] != nil || len(s.mcpReaders) >= 1024 {
		s.mcpMu.Unlock()
		return rpc.Error(mcpUnavailable(), correlation)
	}
	s.mcpReaders[machine] = reader
	s.mcpMu.Unlock()
	defer func() {
		s.mcpMu.Lock()
		if s.mcpReaders[machine] == reader {
			delete(s.mcpReaders, machine)
		}
		s.mcpMu.Unlock()
	}()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	if e = stream.Send(&pb.WatchMcpManagementResponse{Heartbeat: true}); e != nil {
		return e
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-primary.Done:
			return nil
		case cmd := <-reader.requests:
			if e = s.Store.Read(ctx, func(tx *store.Tx) error { return mcpWorkerProof(tx, &reader.owner) }); e != nil {
				return rpc.Error(e, correlation)
			}
			if e = stream.Send(&pb.WatchMcpManagementResponse{Command: cmd}); e != nil {
				return e
			}
		case <-ticker.C:
			if e = s.Store.Read(ctx, func(tx *store.Tx) error { return mcpWorkerProof(tx, &reader.owner) }); e != nil {
				return rpc.Error(e, correlation)
			}
			if e = stream.Send(&pb.WatchMcpManagementResponse{Heartbeat: true}); e != nil {
				return e
			}
		}
	}
}
func (s *Service) ReportMcpManagement(ctx context.Context, req *connect.Request[pb.ReportMcpManagementRequest]) (*connect.Response[pb.ReportMcpManagementResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if e := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); e != nil {
		return nil, rpc.Error(e, correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	reader := s.mcpReaders[domain.ID(req.Msg.MachineId)]
	if (req.Msg.ProblemCode == "" && (req.Msg.Result == nil || req.Msg.RejectedWithoutEffect)) || (req.Msg.ProblemCode != "" && req.Msg.Result != nil) {
		return nil, rpc.Error(mcpUnavailable(), correlation)
	}
	if reader == nil || reader.owner.instance != domain.ID(req.Msg.InstanceId) || reader.owner.device != actor.DeviceID || reader.pending == nil || reader.pending.Id != req.Msg.CommandId || proto.Size(req.Msg) > 256<<10 {
		return nil, rpc.Error(mcpUnavailable(), correlation)
	}
	primary, e := s.primaryWorkspaceStream(reader.owner.machine, reader.owner.instance)
	if e != nil || primary.ID != reader.owner.primary {
		return nil, rpc.Error(mcpUnavailable(), correlation)
	}
	if e := s.Store.Read(ctx, func(tx *store.Tx) error { return mcpWorkerProof(tx, &reader.owner) }); e != nil {
		return nil, rpc.Error(e, correlation)
	}
	// A secret-bearing request is never included in the result or retained by this
	// relay. Only the exact original pending command receives its one bounded reply.
	select {
	case reader.reply <- proto.Clone(req.Msg).(*pb.ReportMcpManagementRequest):
		reader.pending = nil
		return connect.NewResponse(&pb.ReportMcpManagementResponse{}), nil
	default:
		return nil, rpc.Error(mcpUnavailable(), correlation)
	}
}
func (s *Service) mcpCommand(ctx context.Context, machine string, cmd *pb.McpManagementCommand) (*pb.ReportMcpManagementRequest, *mcpReader, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.DeviceID.Validate() != nil || domain.ID(machine).Validate() != nil || actor.Type == domain.WorkerDevice {
		return nil, nil, domain.Fail(domain.PermissionDenied, "MCP management requires owner or paired-client authority.", "Use the authenticated selected Runner connection.")
	}
	s.mcpMu.Lock()
	reader := s.mcpReaders[domain.ID(machine)]
	s.mcpMu.Unlock()
	if reader == nil || !reader.gate.TryLock() {
		return nil, nil, mcpUnavailable()
	}
	defer reader.gate.Unlock()
	primary, e := s.primaryWorkspaceStream(reader.owner.machine, reader.owner.instance)
	if e != nil || primary.ID != reader.owner.primary {
		return nil, nil, mcpUnavailable()
	}
	if e := s.Store.Read(ctx, func(tx *store.Tx) error { return mcpWorkerProof(tx, &reader.owner) }); e != nil {
		return nil, nil, e
	}
	cmd.Id = string(domain.NewID())
	cmd.ActorId = string(actor.DeviceID)
	cmd.MachineId = machine
	cmd.WorkerDeviceId = string(reader.owner.device)
	cmd.WorkerInstanceId = string(reader.owner.instance)
	if proto.Size(cmd) > 128<<10 {
		return nil, nil, domain.Fail(domain.ResourceExhausted, "MCP management input exceeds its bound.", "Reduce metadata or protected values before submitting a new request.")
	}
	if mutation := cmd.GetMutation(); mutation != nil {
		if mutation.Mutation == nil || domain.ID(mutation.Mutation.Id).Validate() != nil || domain.ID(mutation.Mutation.RequestId).Validate() != nil {
			return nil, nil, domain.Fail(domain.InvalidArgument, "Invalid MCP mutation.", "Use the original definition and request IDs.")
		}
		safe := proto.Clone(mutation).(*pb.MutateMcpServerRequest)
		safe.Secrets = nil
		raw, _ := proto.MarshalOptions{Deterministic: true}.Marshal(safe)
		hash := sha256.Sum256(raw)
		digest := hex.EncodeToString(hash[:])
		e := s.Store.UpdateMCPMetadata(ctx, func(tx *store.Tx) error {
			if e := mcpWorkerProof(tx, &reader.owner); e != nil {
				return e
			}
			meta, exists, e := tx.MCPMetadata(reader.owner.machine, domain.ID(mutation.Mutation.Id))
			if e != nil {
				return e
			}
			if exists && meta.DeviceID != reader.owner.device {
				return domain.Fail(domain.Conflict, "MCP Worker ownership changed.", "Recover the original definition on its original Worker.")
			}
			if meta.PendingID != "" && (meta.PendingID != domain.ID(mutation.Mutation.RequestId) || meta.PendingActor != actor.DeviceID || meta.PendingDigest != digest) {
				return domain.Fail(domain.RecoveryRequired, "An original MCP mutation is unsettled.", "Retry its exact original request before editing this definition.")
			}
			if mutation.Operation == pb.McpMutation_MCP_MUTATION_DELETE {
				refs, e := tx.MCPReferences(reader.owner.machine, domain.ID(mutation.Mutation.Id))
				if e != nil {
					return e
				}
				if len(refs) > 0 {
					return domain.Fail(domain.Conflict, "Agent Workers still reference this MCP definition.", "Remove those selections explicitly before confirming deletion.")
				}
			}
			meta.MachineID = reader.owner.machine
			meta.DeviceID = reader.owner.device
			meta.DefinitionID = domain.ID(mutation.Mutation.Id)
			meta.PendingID = domain.ID(mutation.Mutation.RequestId)
			meta.PendingActor = actor.DeviceID
			meta.PendingDigest = digest
			return tx.PutMCPMetadata(meta)
		})
		if e != nil {
			return nil, nil, e
		}
	}
	s.mcpMu.Lock()
	// A canceled caller may leave a late bounded reply. It belongs only to its
	// original command; the next command must never consume it as acceptance.
	select {
	case <-reader.reply:
	default:
	}
	reader.pending = cmd
	s.mcpMu.Unlock()
	defer func() {
		s.mcpMu.Lock()
		if reader.pending == cmd {
			reader.pending = nil
		}
		s.mcpMu.Unlock()
	}()
	select {
	case reader.requests <- cmd:
	case <-ctx.Done():
		return nil, nil, mcpUnavailable()
	case <-reader.owner.done:
		return nil, nil, mcpUnavailable()
	}
	select {
	case reply := <-reader.reply:
		if reply.CommandId != cmd.Id {
			return nil, nil, mcpUnavailable()
		}
		if e := s.Store.Read(ctx, func(tx *store.Tx) error { return mcpWorkerProof(tx, &reader.owner) }); e != nil {
			return nil, nil, e
		}
		if reply.ProblemCode != "" {
			if reply.RejectedWithoutEffect && cmd.GetMutation() != nil {
				mutation := cmd.GetMutation()
				if e := s.Store.UpdateMCPMetadata(ctx, func(tx *store.Tx) error {
					meta, ok, e := tx.MCPMetadata(reader.owner.machine, domain.ID(mutation.Mutation.Id))
					if e != nil {
						return e
					}
					if ok && meta.PendingID == domain.ID(mutation.Mutation.RequestId) && meta.PendingActor == actor.DeviceID {
						meta.PendingID = ""
						meta.PendingActor = ""
						meta.PendingDigest = ""
						return tx.PutMCPMetadata(meta)
					}
					return nil
				}); e != nil {
					return nil, nil, e
				}
			}
			code := domain.Code(reply.ProblemCode)
			switch code {
			case domain.InvalidArgument, domain.Conflict, domain.PermissionDenied, domain.Unavailable, domain.RecoveryRequired, domain.Unsupported, domain.ResourceExhausted, domain.NotFound, domain.ConfirmationRequired:
			default:
				return nil, nil, mcpUnavailable()
			}
			return nil, nil, domain.Fail(code, "The original Runner could not complete MCP management.", "Retain and retry the original request, or refresh after a confirmed rejection.")
		}
		return reply, reader, nil
	case <-ctx.Done():
		return nil, nil, mcpUnavailable()
	case <-reader.owner.done:
		return nil, nil, mcpUnavailable()
	}
}
func (s *Service) ListMcpServers(ctx context.Context, req *connect.Request[pb.ListMcpServersRequest]) (*connect.Response[pb.ListMcpServersResponse], error) {
	reply, reader, e := s.mcpCommand(ctx, req.Msg.MachineId, &pb.McpManagementCommand{Operation: &pb.McpManagementCommand_List{List: req.Msg}})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	result := reply.GetList()
	if result == nil || len(result.Servers) > 128 {
		return nil, rpc.Error(mcpUnavailable(), "")
	}
	e = s.Store.UpdateMCPMetadata(ctx, func(tx *store.Tx) error {
		seen := map[string]bool{}
		for _, server := range result.Servers {
			if server == nil || server.Definition == nil || seen[server.Definition.Id] {
				return mcpUnavailable()
			}
			seen[server.Definition.Id] = true
			if e := s.mcpServerProof(server, reader); e != nil {
				return e
			}
			meta, exists, e := tx.MCPMetadata(reader.owner.machine, domain.ID(server.Definition.Id))
			if e != nil {
				return e
			}
			if !exists || meta.PendingID == "" && server.Definition.Revision >= meta.Revision {
				meta = store.MCPMetadata{MachineID: reader.owner.machine, DeviceID: reader.owner.device, DefinitionID: domain.ID(server.Definition.Id), Revision: server.Definition.Revision, Enabled: server.Definition.Enabled, AuthenticationReady: server.AuthenticationState == pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_READY || server.AuthenticationState == pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_NOT_REQUIRED}
				if e = tx.PutMCPMetadata(meta); e != nil {
					return e
				}
			}
			refs, e := tx.MCPReferences(reader.owner.machine, domain.ID(server.Definition.Id))
			if e != nil {
				return e
			}
			server.AgentIds = nil
			for _, id := range refs {
				server.AgentIds = append(server.AgentIds, string(id))
			}
		}
		return nil
	})
	if e != nil {
		return nil, rpc.Error(e, "")
	}
	return connect.NewResponse(result), nil
}
func (s *Service) mcpServerProof(server *pb.McpServer, reader *mcpReader) error {
	if server != nil && (server.AuthenticationState < pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_NOT_REQUIRED || server.AuthenticationState > pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_UNCERTAIN) {
		return mcpUnavailable()
	}
	if server == nil || server.Definition == nil || server.MachineId != string(reader.owner.machine) || server.WorkerDeviceId != string(reader.owner.device) || server.Definition.Revision == 0 || len(server.SupportedHarnesses) != 0 {
		return mcpUnavailable()
	}
	_, e := mcpmanagement.DefinitionFromWire(server.Definition)
	if e != nil {
		return mcpUnavailable()
	}
	return nil
}
func (s *Service) MutateMcpServer(ctx context.Context, req *connect.Request[pb.MutateMcpServerRequest]) (*connect.Response[pb.MutateMcpServerResponse], error) {
	reply, reader, e := s.mcpCommand(ctx, req.Msg.MachineId, &pb.McpManagementCommand{Operation: &pb.McpManagementCommand_Mutation{Mutation: req.Msg}})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	result := reply.GetMutation()
	if result == nil || req.Msg.Mutation == nil || result.RequestId != req.Msg.Mutation.RequestId || (result.Deleted && (req.Msg.Operation != pb.McpMutation_MCP_MUTATION_DELETE || result.Server != nil)) || (!result.Deleted && (s.mcpServerProof(result.Server, reader) != nil || result.Server.Definition.Id != req.Msg.Mutation.Id)) {
		return nil, rpc.Error(mcpUnavailable(), "")
	}
	e = s.Store.UpdateMCPMetadata(ctx, func(tx *store.Tx) error {
		meta, ok, e := tx.MCPMetadata(reader.owner.machine, domain.ID(req.Msg.Mutation.Id))
		if e != nil || !ok {
			return mcpUnavailable()
		}
		if meta.PendingID != domain.ID(result.RequestId) {
			return mcpUnavailable()
		}
		meta.PendingID = ""
		meta.PendingActor = ""
		meta.PendingDigest = ""
		if result.Deleted {
			meta.Deleted = true
		} else if result.Server.Definition.Revision >= meta.Revision {
			meta.Revision = result.Server.Definition.Revision
			meta.Enabled = result.Server.Definition.Enabled
			meta.AuthenticationReady = result.Server.AuthenticationState == pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_READY || result.Server.AuthenticationState == pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_NOT_REQUIRED
			meta.Deleted = false
		}
		return tx.PutMCPMetadata(meta)
	})
	if e != nil {
		return nil, rpc.Error(e, "")
	}
	return connect.NewResponse(result), nil
}
func (s *Service) AuthenticateMcpServer(ctx context.Context, req *connect.Request[pb.AuthenticateMcpServerRequest]) (*connect.Response[pb.AuthenticateMcpServerResponse], error) {
	reply, _, e := s.mcpCommand(ctx, req.Msg.MachineId, &pb.McpManagementCommand{Operation: &pb.McpManagementCommand_Authentication{Authentication: req.Msg}})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	result := reply.GetAuthentication()
	if result == nil || req.Msg.Mutation == nil || result.RequestId != req.Msg.Mutation.RequestId {
		return nil, rpc.Error(mcpUnavailable(), "")
	}
	return connect.NewResponse(result), nil
}
