// SPDX-License-Identifier: Apache-2.0
package mcpmanagement

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func DefinitionFromWire(v *pb.McpDefinition) (domain.MCPDefinition, error) {
	if v == nil {
		return domain.MCPDefinition{}, invalid()
	}
	d := domain.MCPDefinition{ID: domain.ID(v.Id), Name: v.Name, Executable: v.Executable, Arguments: v.Arguments, Directory: v.Directory, Endpoint: v.Endpoint, EnvironmentNames: v.EnvironmentNames, HeaderNames: v.HeaderNames, Enabled: v.Enabled, Revision: v.Revision}
	switch v.Transport {
	case pb.McpTransport_MCP_TRANSPORT_STDIO:
		d.Transport = domain.MCPStdio
	case pb.McpTransport_MCP_TRANSPORT_STREAMABLE_HTTP:
		d.Transport = domain.MCPHTTP
	default:
		return d, invalid()
	}
	switch v.Authentication {
	case pb.McpAuthentication_MCP_AUTHENTICATION_NONE:
		d.Authentication = domain.MCPAnonymous
	case pb.McpAuthentication_MCP_AUTHENTICATION_MANUAL:
		d.Authentication = domain.MCPManual
	case pb.McpAuthentication_MCP_AUTHENTICATION_OAUTH:
		d.Authentication = domain.MCPOAuth
	default:
		return d, invalid()
	}
	return d, d.Validate()
}
func EntryToWire(v Entry, machine, device domain.ID) *pb.McpServer {
	d := v.Definition
	transport := pb.McpTransport_MCP_TRANSPORT_STDIO
	if d.Transport == domain.MCPHTTP {
		transport = pb.McpTransport_MCP_TRANSPORT_STREAMABLE_HTTP
	}
	auth := pb.McpAuthentication_MCP_AUTHENTICATION_NONE
	if d.Authentication == domain.MCPManual {
		auth = pb.McpAuthentication_MCP_AUTHENTICATION_MANUAL
	}
	if d.Authentication == domain.MCPOAuth {
		auth = pb.McpAuthentication_MCP_AUTHENTICATION_OAUTH
	}
	return &pb.McpServer{Definition: &pb.McpDefinition{Id: string(d.ID), Name: d.Name, Transport: transport, Executable: d.Executable, Arguments: d.Arguments, Directory: d.Directory, Endpoint: d.Endpoint, EnvironmentNames: d.EnvironmentNames, HeaderNames: d.HeaderNames, Authentication: auth, Enabled: d.Enabled, Revision: d.Revision}, MachineId: string(machine), WorkerDeviceId: string(device), AuthenticationState: authToWire(v.Authentication)}
}
func authToWire(v AuthState) pb.McpAuthenticationState {
	switch v {
	case NotRequired:
		return pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_NOT_REQUIRED
	case Ready:
		return pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_READY
	case Pending:
		return pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_PENDING
	case Canceled:
		return pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_CANCELED
	case Uncertain:
		return pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_UNCERTAIN
	default:
		return pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_REQUIRED
	}
}
func (m *Manager) Execute(ctx context.Context, cmd *pb.McpManagementCommand) (*pb.ReportMcpManagementRequest, error) {
	reply := &pb.ReportMcpManagementRequest{MachineId: cmd.MachineId, InstanceId: cmd.WorkerInstanceId, CommandId: cmd.Id}
	if cmd.GetList() != nil {
		rows, e := m.List()
		if e != nil {
			return nil, e
		}
		result := &pb.ListMcpServersResponse{}
		for _, row := range rows {
			item := EntryToWire(row, domain.ID(cmd.MachineId), domain.ID(cmd.WorkerDeviceId))
			state, attempt, revision := m.AuthenticationMetadata(domain.ID(cmd.ActorId), row.Definition.ID)
			if state != "" {
				item.AuthenticationState = authToWire(state)
				item.AuthenticationAttemptId = string(attempt)
				item.AuthenticationAttemptRevision = revision
			}
			result.Servers = append(result.Servers, item)
		}
		reply.Result = &pb.ReportMcpManagementRequest_List{List: result}
		return reply, nil
	}
	if wire := cmd.GetMutation(); wire != nil {
		if wire.Mutation == nil {
			return nil, invalid()
		}
		r := Request{ID: domain.ID(wire.Mutation.RequestId), Actor: domain.ID(cmd.ActorId), ServerID: domain.ID(wire.Mutation.Id), Revision: wire.Mutation.ExpectedRevision, Enabled: wire.Enabled, Confirmed: wire.Confirmed}
		switch wire.Operation {
		case pb.McpMutation_MCP_MUTATION_SAVE:
			r.Operation = Save
			d, e := DefinitionFromWire(wire.Definition)
			if e != nil {
				return nil, e
			}
			r.Definition = &d
		case pb.McpMutation_MCP_MUTATION_ENABLE:
			r.Operation = Enable
		case pb.McpMutation_MCP_MUTATION_DELETE:
			r.Operation = Delete
		default:
			return nil, invalid()
		}
		if wire.Secrets != nil {
			r.Secrets = &Secrets{Environment: map[string]string{}, Headers: map[string]string{}}
			for _, v := range wire.Secrets.Environment {
				if v == nil {
					return nil, invalid()
				}
				if _, ok := r.Secrets.Environment[v.Name]; ok {
					return nil, invalid()
				}
				r.Secrets.Environment[v.Name] = v.Value
			}
			for _, v := range wire.Secrets.Headers {
				if v == nil {
					return nil, invalid()
				}
				if _, ok := r.Secrets.Headers[v.Name]; ok {
					return nil, invalid()
				}
				r.Secrets.Headers[v.Name] = v.Value
			}
		}
		result, e := m.Mutate(ctx, r)
		if e != nil {
			return nil, e
		}
		response := &pb.MutateMcpServerResponse{RequestId: wire.Mutation.RequestId, Deleted: result.Deleted, Replayed: result.Replayed}
		if result.Entry != nil {
			response.Server = EntryToWire(*result.Entry, domain.ID(cmd.MachineId), domain.ID(cmd.WorkerDeviceId))
		}
		reply.Result = &pb.ReportMcpManagementRequest_Mutation{Mutation: response}
		return reply, nil
	}
	if auth := cmd.GetAuthentication(); auth != nil {
		result, e := m.Authenticate(ctx, domain.ID(cmd.ActorId), auth)
		if e != nil {
			return nil, e
		}
		reply.Result = &pb.ReportMcpManagementRequest_Authentication{Authentication: result}
		return reply, nil
	}
	return nil, invalid()
}
