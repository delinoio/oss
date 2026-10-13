// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"path/filepath"
	"testing"
)

func TestManagedMCPReplyOriginalIdentityFence(t *testing.T) {
	machine, worker, definition, request := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	d := domain.ManagedMCPDefinition{ID: definition, Revision: 1, MachineID: machine, WorkerDeviceID: worker, Name: "Fixture", Transport: domain.MCPStreamableHTTP, Endpoint: "https://fixture.test/mcp", Enabled: true, Authentication: domain.MCPNoAuthentication}
	q := domain.ManagedMCPRequest{MachineID: machine, WorkerDeviceID: worker, Action: domain.MCPOperationRead, AttemptID: request, DefinitionID: definition}
	r := domain.ManagedMCPResult{Definitions: []domain.ManagedMCPDefinition{d}, Operation: &domain.ManagedMCPOperation{ID: request, DefinitionID: definition, State: domain.MCPOperationCompleted}}
	if err := mcpDefinitions(q, r); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"request", "definition", "worker", "machine", "state"} {
		t.Run(mode, func(t *testing.T) {
			v := r
			v.Definitions = append([]domain.ManagedMCPDefinition(nil), r.Definitions...)
			op := *r.Operation
			v.Operation = &op
			switch mode {
			case "request":
				op.ID = domain.NewID()
			case "definition":
				op.DefinitionID = domain.NewID()
			case "worker":
				v.Definitions[0].WorkerDeviceID = domain.NewID()
			case "machine":
				v.Definitions[0].MachineID = domain.NewID()
			case "state":
				op.State = "unknown"
			}
			if mcpDefinitions(q, v) == nil {
				t.Fatal("foreign or unknown reply published")
			}
		})
	}
}

func TestManagedMCPAwaitingAuthorizationKeepsAdmissionFence(t *testing.T) {
	actor, other, begin := domain.NewID(), domain.NewID(), domain.NewID()
	record := domain.ManagedMCPRecord{PendingRequestID: begin, PendingActorID: actor, PendingAction: domain.MCPOAuthBegin}
	for state, settles := range map[domain.MCPOperationState]bool{
		domain.MCPOperationAwaiting:  false,
		domain.MCPOperationStarted:   false,
		domain.MCPOperationRecovery:  false,
		domain.MCPOperationCompleted: true,
		domain.MCPOperationCanceled:  true,
		domain.MCPOperationRejected:  true,
	} {
		if got := managedMCPSettlesPending(record, &domain.ManagedMCPOperation{ID: begin, State: state}, actor); got != settles {
			t.Fatalf("state %s settles=%v, want %v", state, got, settles)
		}
	}
	if managedMCPSettlesPending(record, &domain.ManagedMCPOperation{ID: domain.NewID(), State: domain.MCPOperationCompleted}, actor) || managedMCPSettlesPending(record, &domain.ManagedMCPOperation{ID: begin, State: domain.MCPOperationCompleted}, other) {
		t.Fatal("foreign operation settled the original admission")
	}
	// The Agent selection guard reads PendingRequestID, so an awaiting attempt blocks selection.
	if record.PendingRequestID == "" {
		t.Fatal("awaiting attempt released the selection fence")
	}
	cases := map[string]struct {
		q    domain.ManagedMCPRequest
		deny bool
	}{
		"complete original attempt": {domain.ManagedMCPRequest{RequestID: domain.NewID(), ActorID: actor, Action: domain.MCPOAuthComplete, AttemptID: begin}, false},
		"cancel original attempt":   {domain.ManagedMCPRequest{RequestID: domain.NewID(), ActorID: actor, Action: domain.MCPOAuthCancel, AttemptID: begin}, false},
		"replay of the begin":       {domain.ManagedMCPRequest{RequestID: begin, ActorID: actor, Action: domain.MCPOAuthBegin}, false},
		"complete foreign actor":    {domain.ManagedMCPRequest{RequestID: domain.NewID(), ActorID: other, Action: domain.MCPOAuthComplete, AttemptID: begin}, true},
		"complete another attempt":  {domain.ManagedMCPRequest{RequestID: domain.NewID(), ActorID: actor, Action: domain.MCPOAuthComplete, AttemptID: domain.NewID()}, true},
		"competing save":            {domain.ManagedMCPRequest{RequestID: domain.NewID(), ActorID: actor, Action: domain.MCPSave}, true},
		"competing delete":          {domain.ManagedMCPRequest{RequestID: domain.NewID(), ActorID: actor, Action: domain.MCPDelete, AttemptID: begin}, true},
		"second begin":              {domain.ManagedMCPRequest{RequestID: domain.NewID(), ActorID: actor, Action: domain.MCPOAuthBegin}, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := managedMCPAdmissionFence(record, c.q)
			if c.deny && domain.SafeError(e).Code != domain.RecoveryRequired || !c.deny && e != nil {
				t.Fatalf("deny=%v err=%v", c.deny, e)
			}
		})
	}
	if managedMCPAdmissionFence(domain.ManagedMCPRecord{}, domain.ManagedMCPRequest{RequestID: domain.NewID(), ActorID: actor, Action: domain.MCPSave}) != nil {
		t.Fatal("unfenced definition rejected a mutation")
	}
}

func TestManagedMCPFirstSaveNeverRetainsAPlaceholderTheWorkerRejects(t *testing.T) {
	s, _ := newDoctorFixture(t)
	machine, worker := domain.NewID(), domain.NewID()
	command := filepath.Join(t.TempDir(), "mcp")
	build := func(mutate func(*domain.ManagedMCPDefinition)) domain.ManagedMCPRequest {
		d := domain.ManagedMCPDefinition{ID: domain.NewID(), Revision: 1, MachineID: machine, WorkerDeviceID: worker, Name: "Fixture", Transport: domain.MCPStdio, Command: command, Cwd: t.TempDir(), Enabled: true, Authentication: domain.MCPNoAuthentication}
		mutate(&d)
		return domain.ManagedMCPRequest{MachineID: machine, WorkerDeviceID: worker, ActorID: domain.NewID(), RequestID: domain.NewID(), Action: domain.MCPSave, Definition: &d}
	}
	admit := func(q domain.ManagedMCPRequest) error {
		_, err := s.Store.Mutate(context.Background(), domain.NewID(), "managed-mcp.admit", q, func(tx *store.Tx) (any, error) {
			return admitManagedMCPMutation(tx, q)
		})
		return err
	}
	retained := func() int {
		count := 0
		if err := s.Store.Read(context.Background(), func(tx *store.Tx) error {
			rows, e := all(tx, domain.ManagedMCPKind)
			count = len(rows)
			return e
		}); err != nil {
			t.Fatal(err)
		}
		return count
	}
	for name, mutate := range map[string]func(*domain.ManagedMCPDefinition){
		"relative executable": func(d *domain.ManagedMCPDefinition) { d.Command = "relative/mcp" },
		"too many arguments": func(d *domain.ManagedMCPDefinition) {
			d.Arguments = make([]string, 129)
		},
		"native support claim": func(d *domain.ManagedMCPDefinition) { d.SupportedHarnesses = []domain.Harness{domain.Codex} },
		"credential reference": func(d *domain.ManagedMCPDefinition) { d.CredentialID = domain.NewID() },
	} {
		t.Run(name, func(t *testing.T) {
			if err := admit(build(mutate)); domain.SafeError(err).Code != domain.InvalidArgument {
				t.Fatalf("invalid first save admitted: %v", err)
			}
			if retained() != 0 {
				t.Fatal("rejected first save retained a hidden placeholder")
			}
		})
	}
	if err := admit(build(func(*domain.ManagedMCPDefinition) {})); err != nil {
		t.Fatal(err)
	}
	if retained() != 1 {
		t.Fatal("valid first save was not retained")
	}
	for retained() < managedMCPCatalogCapacity {
		q := build(func(*domain.ManagedMCPDefinition) {})
		if err := admit(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := admit(build(func(*domain.ManagedMCPDefinition) {})); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatalf("full catalog admitted another placeholder: %v", err)
	}
}
