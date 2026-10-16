package server

import (
	"context"
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestMcpServerConfigurationRoundTrip(t *testing.T) {
	s, _ := newDoctorFixture(t)
	ctx := context.Background()
	id := domain.NewID()
	save := func(revision uint64, document any) error {
		raw, _ := json.Marshal(document)
		_, err := s.SaveConfiguration(ctx, connect.NewRequest(&pb.SaveConfigurationRequest{
			Mutation:      &pb.Mutation{Id: string(id), ExpectedRevision: revision, RequestId: string(domain.NewID())},
			Kind:          pb.EntityKind_ENTITY_KIND_MCP_SERVER,
			SchemaVersion: 1,
			DocumentJson:  raw,
		}))
		return err
	}
	server := domain.McpServer{Name: "Files", Transport: domain.McpTransportStdio, Enabled: true, Command: "mcp-files", Args: []string{"--root", "/work"}}
	if err := save(0, server); err != nil {
		t.Fatalf("create: %v", err)
	}
	server.Enabled = false
	if err := save(0, server); err == nil {
		t.Fatal("create replay with a stale revision must conflict")
	}
	if err := save(1, server); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if err := save(2, domain.McpServer{Name: "Bad", Transport: domain.McpTransportStreamableHTTP, URL: "https://u:p@example.com"}); err == nil {
		t.Fatal("credentialed endpoint must be rejected")
	}
	if err := save(2, map[string]any{"name": "x", "transport": "stdio", "command": "c", "secret": "nope"}); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
}
