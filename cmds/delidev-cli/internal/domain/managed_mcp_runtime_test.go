// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestManagedMCPSupportComesOnlyFromThePublishedWorkerCapability(t *testing.T) {
	if ManagedMCPSupport([]WorkerCapability{ManagedMCPV1}) != nil {
		t.Fatal("management authority advertised native support")
	}
	if got := ManagedMCPSupport([]WorkerCapability{ManagedMCPV1, CodexManagedMCPV1}); len(got) != 1 || got[0] != Codex {
		t.Fatalf("published Codex adapter not derived: %v", got)
	}
}

func TestManagedMCPNativeServerNameIsStableValidAndCollisionResistant(t *testing.T) {
	valid := regexp.MustCompile(`^[a-z0-9_]{1,128}$`)
	id := NewID()
	d := ManagedMCPDefinition{ID: id, Revision: 1, Name: "My Server! 한글"}
	name := d.NativeServerName()
	if !valid.MatchString(name) || !strings.HasPrefix(name, "my_server_") {
		t.Fatalf("native name %q", name)
	}
	d.Revision, d.Name = 9, "My Server! 한글"
	if d.NativeServerName() != name {
		t.Fatal("a later generation renamed its native tools")
	}
	other := ManagedMCPDefinition{ID: NewID(), Revision: 1, Name: "My Server! 한글"}
	if other.NativeServerName() == name {
		t.Fatal("two definitions with one display name collided")
	}
	for _, label := range []string{"", "   ", "한글", strings.Repeat("a", 300), "../../etc"} {
		generated := (ManagedMCPDefinition{ID: NewID(), Name: label}).NativeServerName()
		if !valid.MatchString(generated) {
			t.Fatalf("%q produced %q", label, generated)
		}
	}
}

func mcpSnapshot(status ToolStatus) ToolSnapshot {
	return ToolSnapshot{Kind: MCPTool, Status: status, MCP: &MCPToolObservation{MCPServerIdentity: MCPServerIdentity{DefinitionID: NewID(), Revision: 2, Server: "fx_abcdef0123"}, Tool: "write"}}
}

func TestMCPToolSnapshotValidationKeepsIdentityAndTypedOutcome(t *testing.T) {
	if mcpSnapshot(ToolRunning).Validate() != nil {
		t.Fatal("running call rejected")
	}
	done := mcpSnapshot(ToolCompleted)
	text := "ok"
	done.MCP.ResultText = &text
	if err := done.Validate(); err != nil {
		t.Fatal(err)
	}
	failed := mcpSnapshot(ToolFailed)
	message := "denied"
	failed.MCP.Error = &message
	if err := failed.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ToolSnapshot){
		"missing-observation": func(s *ToolSnapshot) { s.MCP = nil },
		"declined":            func(s *ToolSnapshot) { s.Status = ToolDeclined },
		"bad-definition":      func(s *ToolSnapshot) { s.MCP.DefinitionID = "invalid" },
		"zero-revision":       func(s *ToolSnapshot) { s.MCP.Revision = 0 },
		"empty-server":        func(s *ToolSnapshot) { s.MCP.Server = "" },
		"empty-tool":          func(s *ToolSnapshot) { s.MCP.Tool = " " },
		"running-with-result": func(s *ToolSnapshot) { s.Status = ToolRunning; s.MCP.ResultText = &text },
		"completed-error":     func(s *ToolSnapshot) { s.Status = ToolCompleted; s.MCP.Error = &message },
		"command-mixed":       func(s *ToolSnapshot) { s.Command = &CommandObservation{} },
		"changes-mixed":       func(s *ToolSnapshot) { s.Changes = []FileChangeObservation{} },
		"negative-duration":   func(s *ToolSnapshot) { d := int64(-1); s.MCP.DurationMS = &d },
	} {
		t.Run(name, func(t *testing.T) {
			s := mcpSnapshot(ToolRunning)
			mutate(&s)
			if s.Validate() == nil {
				t.Fatal("invalid MCP snapshot accepted")
			}
		})
	}
	other := ToolSnapshot{Kind: CommandTool, Status: ToolRunning, Command: &CommandObservation{Command: "ls", Cwd: "/", Source: AgentCommand, Actions: []CommandAction{}}, MCP: mcpSnapshot(ToolRunning).MCP}
	if other.Validate() == nil {
		t.Fatal("an MCP observation attached to another tool kind")
	}
}

func TestMCPToolUpdateRequiresStartedRunningAndCompletedTerminal(t *testing.T) {
	update := ExecutionToolUpdate{ID: NewID(), NativeID: "call-1", Snapshot: ptr(mcpSnapshot(ToolRunning))}
	if err := update.Validate(ExecutionToolStarted); err != nil {
		t.Fatal(err)
	}
	if update.Validate(ExecutionToolCompleted) == nil {
		t.Fatal("a running snapshot completed a call")
	}
	done := mcpSnapshot(ToolFailed)
	message := "denied"
	done.MCP.Error = &message
	update.Snapshot = &done
	if err := update.Validate(ExecutionToolCompleted); err != nil {
		t.Fatal(err)
	}
	if update.Validate(ExecutionToolStarted) == nil {
		t.Fatal("a failed snapshot started a call")
	}
}

func ptr[T any](v T) *T { return &v }

func TestExecutionConfigurationBindsManagedMCPGenerationsAndRejectsSidechat(t *testing.T) {
	d := ManagedMCPDefinition{ID: NewID(), Revision: 3, Name: "Fixture", MachineID: NewID(), WorkerDeviceID: NewID()}
	c := ExecutionConfiguration{ManagedMCP: []ManagedMCPDefinition{d}}
	identity := MCPServerIdentity{DefinitionID: d.ID, Revision: 3, Server: d.NativeServerName()}
	if !c.MatchesManagedMCP(identity) {
		t.Fatal("original generation not matched")
	}
	for name, mutate := range map[string]func(*MCPServerIdentity){
		"later-revision": func(i *MCPServerIdentity) { i.Revision = 4 },
		"foreign-id":     func(i *MCPServerIdentity) { i.DefinitionID = NewID() },
		"renamed":        func(i *MCPServerIdentity) { i.Server = "other_0000000000" },
	} {
		forged := identity
		mutate(&forged)
		if c.MatchesManagedMCP(forged) {
			t.Fatalf("%s identity matched", name)
		}
	}
	sidechat := ExecutionConfiguration{SidechatPolicy: CodexReadOnlySidechatV1, Harness: Codex, ManagedMCP: []ManagedMCPDefinition{d}, Options: AgentOptions{Permission: PermissionReadOnly, ApprovalPolicy: "never"}}
	if sidechat.ValidateSidechat() == nil {
		t.Fatal("read-only Sidechat accepted external managed servers")
	}
}

func TestSidechatSnapshotDropsManagedMCPAuthority(t *testing.T) {
	id := NewID()
	c, err := resolveInlineFixture(NewID(), 3, Agent{Name: "Parent", Harness: Codex, ModelID: NewID(), Effort: "high", Options: AgentOptions{Permission: PermissionWorkspaceWrite, ApprovalPolicy: "on-request"}, Accounts: []WeightedAccount{{ID: NewID(), Weight: 1}}, Templates: []ID{id}}, 7, Model{Name: "Parent", NativeID: "native-parent", ProviderID: NewID(), Harnesses: []Harness{Codex}, MetadataSource: UserDeclared}, Priority, []AppliedTemplate{{ID: id, Revision: 5, Contents: "Parent\n"}})
	if err != nil {
		t.Fatal(err)
	}
	c.ManagedMCP = []ManagedMCPDefinition{{ID: NewID(), Revision: 1, MachineID: NewID(), WorkerDeviceID: NewID(), Name: "Parent MCP", Transport: MCPStreamableHTTP, Endpoint: "https://fixture.test/mcp", Enabled: true, Authentication: MCPNoAuthentication, SupportedHarnesses: []Harness{Codex}}}
	digest, _ := c.Digest()
	parent := InitialExecution{ID: NewID(), InputID: NewID(), Configuration: c, ConfigurationDigest: digest, InitialAccountID: NewID(), ConnectionID: NewID(), AcceptedAt: time.Now().UTC()}
	child, err := SidechatSnapshot(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(child.Configuration.ManagedMCP) != 0 || len(parent.Configuration.ManagedMCP) != 1 {
		t.Fatal("Sidechat kept external servers or the parent lost its selection")
	}
}

func TestQuestionAcceptanceEvidenceAllowsExactElicitationResolution(t *testing.T) {
	update := ExecutionQuestionAcceptanceUpdate{InteractionID: NewID(), ResponseID: NewID(), ClaimID: NewID(), NativeItemID: "mcp-elicitation/n:0", Evidence: NativeMCPElicitationResolved}
	if err := update.Validate(); err != nil {
		t.Fatal(err)
	}
	update.Evidence = "transport-delivery"
	if update.Validate() == nil {
		t.Fatal("transport delivery substituted for acceptance")
	}
}
