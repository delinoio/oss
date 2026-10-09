// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/mcpmanagement"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
	"time"
)

// MCP management is independently joined with the original primary Worker.
// Its reconnection never resubmits a mutation or changes an execution claim.
func watchMcpManagement(ctx context.Context, config Config, client delidevv1connect.McpWorkerServiceClient, credential Credential, instance domain.ID) {
	if client == nil {
		return
	}
	manager, e := mcpmanagement.Open(config.Root, credential.ServerID, credential.DeviceID, credential.MachineID, config.Logger)
	if e != nil {
		if config.Logger != nil {
			config.Logger.WarnContext(ctx, "mcp_catalog_unavailable", "machine_id", credential.MachineID, "code", domain.SafeError(e).Code)
		}
		return
	}
	defer manager.Close()
	for ctx.Err() == nil {
		e := receiveMcpManagement(ctx, config, client, credential, instance, manager)
		if ctx.Err() != nil {
			return
		}
		if config.Logger != nil {
			config.Logger.WarnContext(ctx, "mcp_management_disconnected", "machine_id", credential.MachineID, "code", domain.SafeError(e).Code)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func receiveMcpManagement(ctx context.Context, config Config, client delidevv1connect.McpWorkerServiceClient, credential Credential, instance domain.ID, manager *mcpmanagement.Manager) error {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	deadline := time.AfterFunc(domain.WorkerConnectionTimeout, cancel)
	defer deadline.Stop()
	stream, e := client.WatchMcpManagement(child, authenticated(credential, &pb.WatchMcpManagementRequest{MachineId: string(credential.MachineID), InstanceId: string(instance)}))
	if e != nil {
		return e
	}
	defer stream.Close()
	for stream.Receive() {
		deadline.Reset(domain.WorkerConnectionTimeout)
		message := stream.Msg()
		if message.Heartbeat {
			if message.Command != nil {
				return domain.Fail(domain.RecoveryRequired, "Invalid MCP management heartbeat.", "Keep original Worker authority.")
			}
			continue
		}
		cmd := message.Command
		if cmd == nil || proto.Size(cmd) > 128<<10 || domain.ID(cmd.Id).Validate() != nil || domain.ID(cmd.ActorId).Validate() != nil || cmd.MachineId != string(credential.MachineID) || cmd.WorkerDeviceId != string(credential.DeviceID) || cmd.WorkerInstanceId != string(instance) {
			return domain.Fail(domain.PermissionDenied, "Foreign MCP management command.", "Use the original authenticated Worker scope.")
		}
		if cmd.GetList() != nil && cmd.GetList().MachineId != cmd.MachineId || cmd.GetMutation() != nil && cmd.GetMutation().MachineId != cmd.MachineId || cmd.GetAuthentication() != nil && cmd.GetAuthentication().MachineId != cmd.MachineId {
			return domain.Fail(domain.PermissionDenied, "Foreign MCP operation target.", "Keep the original authenticated Worker scope.")
		}
		deadline.Stop()
		bounded, stop := context.WithTimeout(child, 45*time.Second)
		reply, problem := manager.Execute(bounded, cmd)
		stop()
		if problem != nil {
			reply = &pb.ReportMcpManagementRequest{MachineId: string(credential.MachineID), InstanceId: string(instance), CommandId: cmd.Id, ProblemCode: string(domain.SafeError(problem).Code)}
			if mutation := cmd.GetMutation(); mutation != nil && mutation.Mutation != nil {
				reply.RejectedWithoutEffect = manager.RejectedWithoutEffect(domain.ID(mutation.Mutation.RequestId))
			}
		}
		if reply == nil || proto.Size(reply) > 256<<10 {
			return domain.Fail(domain.ResourceExhausted, "MCP management response exceeds its bound.", "Reduce catalog metadata without changing original receipts.")
		}
		attempt, stop := context.WithTimeout(child, 5*time.Second)
		_, e = client.ReportMcpManagement(attempt, authenticated(credential, reply))
		stop()
		if e != nil {
			return e
		}
		deadline.Reset(domain.WorkerConnectionTimeout)
	}
	return stream.Err()
}
