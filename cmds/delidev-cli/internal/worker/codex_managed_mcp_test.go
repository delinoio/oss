// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func mcpTool(id string, status codex.ToolStatus, identity domain.MCPServerIdentity, tool string) *codex.Tool {
	return &codex.Tool{ID: id, Kind: codex.MCPToolKind, Status: status, MCP: &codex.MCPCall{Identity: identity, Tool: tool}}
}

func TestCodexManagedMCPCallPublishesOriginalIdentityAndOutcome(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	identity := domain.MCPServerIdentity{DefinitionID: domain.NewID(), Revision: 4, Server: "fx_abcdef0123"}
	native := mcpTool("call-1", codex.ToolRunning, identity, "write")
	event := codex.Event{Kind: codex.ToolStartedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: native.ID, Correlated: true, Tool: native}
	before := len(rpc.events)
	if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil {
		t.Fatal(err)
	}
	denied := "user cancelled MCP tool call"
	done := mcpTool("call-1", codex.ToolFailed, identity, "write")
	done.MCP.Error = &denied
	event.Kind, event.Tool = codex.ToolCompletedEvent, done
	if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil || c.finished || len(rpc.events) != before+2 {
		t.Fatal("a failed call became a turn result", err)
	}
	var started, completed domain.ExecutionEvent
	if domain.Decode(rpc.events[before], &started) != nil || domain.Decode(rpc.events[before+1], &completed) != nil {
		t.Fatal("published events do not decode")
	}
	if started.Tool.Snapshot.MCP.MCPServerIdentity != identity || completed.Tool.Snapshot.MCP.MCPServerIdentity != identity || completed.Tool.Snapshot.Status != domain.ToolFailed || *completed.Tool.Snapshot.MCP.Error != denied || started.Tool.ID != completed.Tool.ID {
		t.Fatalf("call identity or outcome changed: %+v", completed.Tool.Snapshot.MCP)
	}
	if strings.Contains(string(rpc.events[before]), "arguments") {
		t.Fatal("arguments are not part of the published observation")
	}
}

func TestCodexManagedMCPCallRejectsChangedIdentityAndFlow(t *testing.T) {
	for _, change := range []string{"server", "revision", "definition", "tool", "turn", "duplicate", "unstarted"} {
		t.Run(change, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			identity := domain.MCPServerIdentity{DefinitionID: domain.NewID(), Revision: 4, Server: "fx_abcdef0123"}
			native := mcpTool("call-1", codex.ToolRunning, identity, "write")
			event := codex.Event{Kind: codex.ToolStartedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: native.ID, Correlated: true, Tool: native}
			if change != "unstarted" {
				if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil {
					t.Fatal(err)
				}
			}
			done := mcpTool("call-1", codex.ToolCompleted, identity, "write")
			event.Kind, event.Tool = codex.ToolCompletedEvent, done
			switch change {
			case "server":
				done.MCP.Identity.Server = "other_0000000000"
			case "revision":
				done.MCP.Identity.Revision = 5
			case "definition":
				done.MCP.Identity.DefinitionID = domain.NewID()
			case "tool":
				done.MCP.Tool = "delete"
			case "turn":
				event.TurnID = domain.NewID()
			case "duplicate":
				if _, err := c.PublishCore(context.Background(), event); err != nil {
					t.Fatal(err)
				}
			}
			before := len(rpc.events)
			if _, err := c.PublishCore(context.Background(), event); err == nil || len(rpc.events) != before {
				t.Fatal("MCP call lifecycle relaxed its original identity")
			}
		})
	}
}

func TestCodexManagedMCPPassiveObservationsPublishNothing(t *testing.T) {
	c, rpc := codexTerminalStatusFixture(t)
	before := len(rpc.events)
	outcome := &codex.MCPServerOutcome{Identity: domain.MCPServerIdentity{DefinitionID: domain.NewID(), Revision: 1, Server: "fx_abcdef0123"}, State: domain.MCPStartupReady}
	for _, kind := range []codex.MetadataKind{codex.MetadataMCPStartup, codex.MetadataMCPElicitationRefused} {
		event := codex.Event{Kind: codex.MetadataEvent, Metadata: kind, Correlated: true, ThreadID: c.thread, TurnID: c.turn, MCPStartup: outcome}
		if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	if len(rpc.events) != before {
		t.Fatal("passive MCP startup metadata became product state")
	}
}

func TestCodexElicitationAcceptanceUsesItsOwnNativeEvidence(t *testing.T) {
	for _, elicitation := range []bool{false, true} {
		f := newQuestionControllerFixture(t, "uncertain")
		f.inspection = func(status codex.InteractionStatus) (codex.InteractionStatus, error) {
			status.Elicitation = elicitation
			done := make(chan error, 1)
			go func() {
				_, err := f.mapper.PublishCore(context.Background(), codex.Event{Kind: codex.QuestionAcceptedEvent, ThreadID: f.mapper.thread, TurnID: f.mapper.turn, ItemID: status.ItemID, Correlated: true, InteractionState: &status})
				done <- err
			}()
			return status, <-done
		}
		if err := f.mapper.deliverQuestionResponse(context.Background(), context.Background(), f.control, f); err != nil {
			t.Fatal(err)
		}
		want := domain.NativeQuestionOutput
		if elicitation {
			want = domain.NativeMCPElicitationResolved
		}
		if len(f.acceptanceEvidence) != 1 || f.acceptanceEvidence[0] != want {
			t.Fatalf("elicitation=%v published %v", elicitation, f.acceptanceEvidence)
		}
	}
}

func TestManagedMCPProfileRefusesOtherHarnessesAndSidechat(t *testing.T) {
	selected := []domain.ManagedMCPDefinition{{}}
	for name, configuration := range map[string]domain.ExecutionConfiguration{
		"claude":   {Harness: domain.ClaudeCode, ManagedMCP: selected},
		"opencode": {Harness: domain.OpenCode, ManagedMCP: selected},
		"grok":     {Harness: domain.GrokBuild, ManagedMCP: selected},
		"sidechat": {Harness: domain.Codex, SidechatPolicy: domain.CodexReadOnlySidechatV1, ManagedMCP: selected},
	} {
		t.Run(name, func(t *testing.T) {
			if err := checkManagedMCPProfile(configuration); err == nil || domain.SafeError(err).Code != domain.Unsupported {
				t.Fatalf("unsupported profile admitted managed servers: %v", err)
			}
		})
	}
	for name, configuration := range map[string]domain.ExecutionConfiguration{
		"codex":    {Harness: domain.Codex, ManagedMCP: selected},
		"no-mcp":   {Harness: domain.ClaudeCode},
		"sidechat": {Harness: domain.Codex, SidechatPolicy: domain.CodexReadOnlySidechatV1},
	} {
		if err := checkManagedMCPProfile(configuration); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

type scriptedMCPVerifier struct {
	verify     []scriptedMCPResult
	reconnect  []scriptedMCPResult
	verified   int
	reconnects []domain.ID
}

type scriptedMCPResult struct {
	outcomes []codex.MCPServerOutcome
	err      error
}

func (v *scriptedMCPVerifier) VerifyManagedMCP(context.Context) ([]codex.MCPServerOutcome, error) {
	r := v.verify[v.verified]
	v.verified++
	return r.outcomes, r.err
}

func (v *scriptedMCPVerifier) ReconnectManagedMCP(_ context.Context, id domain.ID) ([]codex.MCPServerOutcome, error) {
	r := v.reconnect[len(v.reconnects)]
	v.reconnects = append(v.reconnects, id)
	return r.outcomes, r.err
}

func mcpOutcomes(states ...domain.MCPStartupOutcome) []codex.MCPServerOutcome {
	var result []codex.MCPServerOutcome
	for i, state := range states {
		result = append(result, codex.MCPServerOutcome{Identity: domain.MCPServerIdentity{DefinitionID: domain.NewID(), Revision: uint64(i + 1), Server: "fx_abcdef0123"}, State: state})
	}
	return result
}

func TestManagedMCPStartupReconnectsOnlyAFailedOriginalServerOnce(t *testing.T) {
	unavailable := domain.Fail(domain.Unavailable, "A selected MCP server did not start.", "")
	failing := mcpOutcomes(domain.MCPStartupReady, domain.MCPStartupFailed)
	v := &scriptedMCPVerifier{verify: []scriptedMCPResult{{failing, unavailable}}, reconnect: []scriptedMCPResult{{mcpOutcomes(domain.MCPStartupReady, domain.MCPStartupReady), nil}}}
	if err := verifyManagedMCPBeforeInput(context.Background(), v, nil); err != nil {
		t.Fatal(err)
	}
	if len(v.reconnects) != 1 || v.reconnects[0] != failing[1].Identity.DefinitionID || v.verified != 1 {
		t.Fatalf("reconnect did not target the failed original definition once: %v", v.reconnects)
	}
	v = &scriptedMCPVerifier{verify: []scriptedMCPResult{{failing, unavailable}}, reconnect: []scriptedMCPResult{{failing, unavailable}}}
	if err := verifyManagedMCPBeforeInput(context.Background(), v, nil); err == nil || len(v.reconnects) != 1 {
		t.Fatalf("a persistent failure must remain a startup failure after one bounded attempt: %v", err)
	}
}

func TestManagedMCPStartupNeverReconnectsAuthenticationOrDriftFailures(t *testing.T) {
	unavailable := domain.Fail(domain.Unavailable, "A selected MCP server did not start.", "")
	for name, run := range map[string]scriptedMCPResult{
		"authentication": {mcpOutcomes(domain.MCPStartupFailed, domain.MCPStartupAuthenticationRequired), unavailable},
		"drift":          {mcpOutcomes(domain.MCPStartupFailed), domain.Fail(domain.Unsupported, "inventory", "")},
		"pending":        {mcpOutcomes(domain.MCPStartupPending), unavailable},
	} {
		t.Run(name, func(t *testing.T) {
			v := &scriptedMCPVerifier{verify: []scriptedMCPResult{run}}
			if err := verifyManagedMCPBeforeInput(context.Background(), v, nil); err == nil || len(v.reconnects) != 0 {
				t.Fatalf("reconnect hid an owner action or native drift: %v %v", err, v.reconnects)
			}
		})
	}
	ok := &scriptedMCPVerifier{verify: []scriptedMCPResult{{mcpOutcomes(domain.MCPStartupReady), nil}}}
	if err := verifyManagedMCPBeforeInput(context.Background(), ok, nil); err != nil || len(ok.reconnects) != 0 {
		t.Fatal("a ready inventory reconnected")
	}
}
