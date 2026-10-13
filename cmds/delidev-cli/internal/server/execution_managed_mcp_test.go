// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func mcpPublicationFixture(t *testing.T) (*publicationFixture, domain.ManagedMCPDefinition) {
	t.Helper()
	definition := domain.ManagedMCPDefinition{ID: domain.NewID(), Revision: 3, MachineID: domain.NewID(), WorkerDeviceID: domain.NewID(), Name: "Fixture", Transport: domain.MCPStreamableHTTP, Endpoint: "https://fixture.test/mcp", Enabled: true, Authentication: domain.MCPNoAuthentication, SupportedHarnesses: []domain.Harness{domain.Codex}}
	authority := newConfiguredAuthorityFixture(t, "http://127.0.0.1:1", func(input *domain.ExecutionJobInput) {
		input.Configuration.ManagedMCP = []domain.ManagedMCPDefinition{definition}
		digest, err := input.Configuration.Digest()
		if err != nil {
			t.Fatal(err)
		}
		input.ConfigurationDigest = digest
	}, false)
	return publicationFixtureFromAuthority(t, authority), definition
}

func mcpToolSnapshot(definition domain.ManagedMCPDefinition, revision uint64, tool string, status domain.ToolStatus) *domain.ToolSnapshot {
	snapshot := &domain.ToolSnapshot{Kind: domain.MCPTool, Status: status, MCP: &domain.MCPToolObservation{MCPServerIdentity: domain.MCPServerIdentity{DefinitionID: definition.ID, Revision: revision, Server: definition.NativeServerName()}, Tool: tool}}
	if status == domain.ToolFailed {
		message := "denied"
		snapshot.MCP.Error = &message
	}
	return snapshot
}

func TestExecutionMCPCallIsBoundToTheFrozenGenerationAndKeepsOneLifecycle(t *testing.T) {
	f, definition := mcpPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	foreign := f.toolEvent(domain.ExecutionToolStarted, 3, domain.NewID(), "foreign-call")
	foreign.Tool.Snapshot = mcpToolSnapshot(definition, 4, "write", domain.ToolRunning)
	if _, err := f.call(f.requestEvent(t, foreign)); err == nil {
		t.Fatal("a later generation's call was attributed to this execution")
	}
	foreign.Tool.Snapshot = mcpToolSnapshot(domain.ManagedMCPDefinition{ID: domain.NewID(), Name: "Other"}, 3, "write", domain.ToolRunning)
	if _, err := f.call(f.requestEvent(t, foreign)); err == nil {
		t.Fatal("an unselected definition's call was published")
	}
	id := domain.NewID()
	started := f.toolEvent(domain.ExecutionToolStarted, 3, id, "call-1")
	started.Tool.Snapshot = mcpToolSnapshot(definition, 3, "write", domain.ToolRunning)
	f.publish(t, started)
	wrong := f.toolEvent(domain.ExecutionToolCompleted, 4, id, "call-1")
	wrong.Tool.Snapshot = mcpToolSnapshot(definition, 3, "delete", domain.ToolFailed)
	if _, err := f.call(f.requestEvent(t, wrong)); err == nil {
		t.Fatal("completion substituted another tool")
	}
	done := f.toolEvent(domain.ExecutionToolCompleted, 4, id, "call-1")
	done.Tool.Snapshot = mcpToolSnapshot(definition, 3, "write", domain.ToolFailed)
	request := f.publish(t, done)
	if reply, err := f.call(request); err != nil || !reply.Msg.Replayed {
		t.Fatal("receipt replay failed", err)
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal("MCP call duplicated the transcript", err)
	}
	m, err := store.Decode[domain.ExecutionMessage](rows[0])
	if err != nil || m.Tool == nil || m.Tool.Completed == nil || m.Tool.Started.MCP.Server != definition.NativeServerName() || m.Tool.Completed.Status != domain.ToolFailed || *m.Tool.Completed.MCP.Error != "denied" {
		t.Fatal("MCP call lost its original identity or outcome")
	}
}

func TestExecutionMCPCallRequiresCodexAndNoParent(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	definition := domain.ManagedMCPDefinition{ID: domain.NewID(), Name: "Unselected"}
	e := f.toolEvent(domain.ExecutionToolStarted, 3, domain.NewID(), "call-1")
	e.Tool.Snapshot = mcpToolSnapshot(definition, 1, "write", domain.ToolRunning)
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("an execution without selections accepted an MCP call")
	}
	f, definition = mcpPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	child := f.toolEvent(domain.ExecutionToolStarted, 3, domain.NewID(), "child-call")
	child.Tool.NativeParentID = "native-parent"
	child.Tool.Snapshot = mcpToolSnapshot(definition, 3, "write", domain.ToolRunning)
	if _, err := f.call(f.requestEvent(t, child)); err == nil {
		t.Fatal("a child-thread call inherited the root selection")
	}
}
