// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
	"strings"
	"testing"
)

func TestMCPAgentSelectionFencesAndPortableRebinding(t *testing.T) {
	f := newAuthorityFixture(t, "http://127.0.0.1:46311")
	ctx := context.Background()
	id := domain.NewID()
	metadata := store.MCPMetadata{MachineID: f.input.MachineID, DeviceID: f.device, DefinitionID: id, Revision: 7, Enabled: true, AuthenticationReady: true}
	if e := f.service.Store.UpdateMCPMetadata(ctx, func(tx *store.Tx) error { return tx.PutMCPMetadata(metadata) }); e != nil {
		t.Fatal(e)
	}
	if e := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		row, e := tx.Get(domain.AgentKind, f.input.Configuration.AgentID)
		if e != nil {
			return e
		}
		agent, e := store.Decode[domain.Agent](row)
		if e != nil {
			return e
		}
		agent.MCPSelections = &domain.MCPSelectionList{Selections: []domain.MCPSelection{{MachineID: metadata.MachineID, DeviceID: metadata.DeviceID, ServerID: id, Revision: 7}}}
		if e = validateRelationships(tx, row.Kind, row.ID, row.Revision, &agent); e != nil {
			return e
		}
		raw, _ := json.Marshal(agent)
		portable, e := portableValue(domain.AgentKind, raw, true)
		if e != nil {
			return e
		}
		imported := portable.(*domain.Agent)
		if !imported.MCPSelections.RebindingRequired || len(imported.MCPSelections.Selections) != 1 {
			t.Fatal("portable import erased or adopted Worker ownership")
		}
		agent.MCPSelections.Selections[0].Revision = 6
		if e = validateRelationships(tx, row.Kind, row.ID, row.Revision, &agent); domain.SafeError(e).Code != domain.Conflict {
			t.Fatal("stale revision accepted", e)
		}
		agent.MCPSelections = &domain.MCPSelectionList{Selections: []domain.MCPSelection{}}
		return validateRelationships(tx, row.Kind, row.ID, row.Revision, &agent)
	}); e != nil {
		t.Fatal(e)
	}
	metadata.PendingID = domain.NewID()
	metadata.PendingActor = domain.NewID()
	metadata.PendingDigest = "original-safe-request-digest"
	if e := f.service.Store.UpdateMCPMetadata(ctx, func(tx *store.Tx) error { return tx.PutMCPMetadata(metadata) }); e != nil {
		t.Fatal(e)
	}
	if e := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		row, e := tx.Get(domain.AgentKind, f.input.Configuration.AgentID)
		if e != nil {
			return e
		}
		agent, _ := store.Decode[domain.Agent](row)
		agent.MCPSelections = &domain.MCPSelectionList{Selections: []domain.MCPSelection{{MachineID: metadata.MachineID, DeviceID: metadata.DeviceID, ServerID: id, Revision: 7}}}
		if e = validateRelationships(tx, row.Kind, row.ID, row.Revision, &agent); domain.SafeError(e).Code != domain.Conflict {
			t.Fatal("unsettled deletion admitted an Agent reference", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestMCPRelayBindsOriginalPrimaryAndKeepsSecretsOutOfMetadata(t *testing.T) {
	f := newAuthorityFixture(t, "http://127.0.0.1:46311")
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice, DeviceID: f.service.Identity.ServerID})
	id := domain.NewID()
	primary := domain.NewID()
	done := make(chan struct{})
	if _, e := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.mcp-capability", nil, func(tx *store.Tx) (any, error) {
		row, machine, e := activeMachine(tx, f.input.MachineID)
		if e != nil {
			return nil, e
		}
		machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.MCPManagementV1)
		return tx.Put(row.Kind, row.ID, row.Revision, "", "", machine)
	}); e != nil {
		t.Fatal(e)
	}
	f.service.workerStreams = map[domain.ID]workerStream{f.input.MachineID: {ID: primary, Instance: f.instance, Done: done}}
	reader := &mcpReader{owner: workspaceReader{machine: f.input.MachineID, device: f.device, instance: f.instance, primary: primary, done: done}, requests: make(chan *pb.McpManagementCommand, 1), reply: make(chan *pb.ReportMcpManagementRequest, 1)}
	f.service.mcpReaders = map[domain.ID]*mcpReader{f.input.MachineID: reader}
	request := &pb.MutateMcpServerRequest{MachineId: string(f.input.MachineID), Mutation: &pb.Mutation{Id: string(id), RequestId: string(domain.NewID())}, Operation: pb.McpMutation_MCP_MUTATION_SAVE, Definition: &pb.McpDefinition{Id: string(id), Name: "Fixture", Transport: pb.McpTransport_MCP_TRANSPORT_STREAMABLE_HTTP, Endpoint: "https://mcp.example.test/api", Authentication: pb.McpAuthentication_MCP_AUTHENTICATION_MANUAL, HeaderNames: []string{"Authorization"}, Enabled: true}, Secrets: &pb.McpSecretValues{Headers: []*pb.McpSecret{{Name: "Authorization", Value: "private-fixture-relay-secret"}}}}
	go func() {
		cmd := <-reader.requests
		reply := &pb.ReportMcpManagementRequest{MachineId: cmd.MachineId, InstanceId: cmd.WorkerInstanceId, CommandId: cmd.Id, Result: &pb.ReportMcpManagementRequest_Mutation{Mutation: &pb.MutateMcpServerResponse{RequestId: cmd.GetMutation().Mutation.RequestId, Server: &pb.McpServer{MachineId: cmd.MachineId, WorkerDeviceId: cmd.WorkerDeviceId, Definition: proto.Clone(request.Definition).(*pb.McpDefinition), AuthenticationState: pb.McpAuthenticationState_MCP_AUTHENTICATION_STATE_READY}}}}
		reply.GetMutation().Server.Definition.Revision = 1
		reader.reply <- reply
	}()
	result, e := f.service.MutateMcpServer(ctx, connect.NewRequest(request))
	if e != nil || result.Msg.Server.Definition.Revision != 1 {
		t.Fatal(result, e)
	}
	if e = f.service.Store.Read(ctx, func(tx *store.Tx) error {
		metadata, ok, e := tx.MCPMetadata(f.input.MachineID, id)
		raw, _ := json.Marshal(metadata)
		if e != nil || !ok || metadata.PendingID != "" || strings.Contains(string(raw), "private-fixture-relay-secret") {
			t.Fatal("relay persisted a secret or lost original receipt fence", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	f.service.workerStreams[f.input.MachineID] = workerStream{ID: domain.NewID(), Instance: f.instance, Done: done}
	if _, e = f.service.ListMcpServers(ctx, connect.NewRequest(&pb.ListMcpServersRequest{MachineId: string(f.input.MachineID)})); e == nil {
		t.Fatal("replacement primary stream adopted original management owner")
	}
}
