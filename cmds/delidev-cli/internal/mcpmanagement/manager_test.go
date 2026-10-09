// SPDX-License-Identifier: Apache-2.0
package mcpmanagement

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
)

type fixtureSecrets struct {
	values        map[credentials.Ref][]byte
	puts, deletes int
	failDelete    int
}

func (f *fixtureSecrets) Put(_ context.Context, r credentials.Ref, b []byte) (string, error) {
	if previous, ok := f.values[r]; ok {
		if string(previous) != string(b) {
			return "", conflict()
		}
		return "fixture", nil
	}
	f.puts++
	f.values[r] = append([]byte{}, b...)
	return "fixture", nil
}
func (f *fixtureSecrets) Get(_ context.Context, r credentials.Ref) ([]byte, error) {
	return append([]byte{}, f.values[r]...), nil
}
func (f *fixtureSecrets) Delete(_ context.Context, r credentials.Ref) error {
	if f.failDelete > 0 {
		f.failDelete--
		return unavailable()
	}
	f.deletes++
	delete(f.values, r)
	return nil
}
func TestMCPIndependentProtectedCatalogAndExactReplay(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	serverID, deviceID, actor := domain.NewID(), domain.NewID(), domain.NewID()
	vault := &fixtureSecrets{values: map[credentials.Ref][]byte{}}
	m, e := open(root, serverID, deviceID, vault)
	if e != nil {
		t.Fatal(e)
	}
	id := domain.NewID()
	def := domain.MCPDefinition{ID: id, Name: "selected", Transport: domain.MCPHTTP, Endpoint: "https://mcp.example.test/api", Authentication: domain.MCPManual, HeaderNames: []string{"Authorization"}, Enabled: true}
	request := Request{ID: domain.NewID(), Actor: actor, ServerID: id, Operation: Save, Definition: &def, Secrets: &Secrets{Headers: map[string]string{"Authorization": "private-fixture-secret"}}}
	result, e := m.Mutate(ctx, request)
	if e != nil || result.Entry.Definition.Revision != 1 || result.Entry.Authentication != Ready {
		t.Fatal(result, e)
	}
	rows, e := m.List()
	raw, _ := json.Marshal(rows)
	if e != nil || string(raw) == "" || strings.Contains(string(raw), "private-fixture-secret") {
		t.Fatal(rows, e)
	}
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		t.Fatal("metadata invalid")
	}
	if len(vault.values) != 1 {
		t.Fatal("protected value missing")
	}
	m, e = open(root, serverID, deviceID, vault)
	if e != nil {
		t.Fatal(e)
	}
	result, e = m.Mutate(ctx, request)
	if e != nil || !result.Replayed || vault.puts != 1 {
		t.Fatal(result, e, vault.puts)
	}
	request.Actor = domain.NewID()
	if _, e = m.Mutate(ctx, request); e == nil {
		t.Fatal("foreign actor replay accepted")
	}
	request.Actor = actor
	request.Secrets.Headers["Authorization"] = "changed"
	if _, e = m.Mutate(ctx, request); e == nil {
		t.Fatal("changed request replay accepted")
	}
}
func TestMCPRevisionConflictsAndConfirmedDeletion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	vault := &fixtureSecrets{values: map[credentials.Ref][]byte{}}
	m, e := open(root, domain.NewID(), domain.NewID(), vault)
	if e != nil {
		t.Fatal(e)
	}
	id, actor := domain.NewID(), domain.NewID()
	def := domain.MCPDefinition{ID: id, Name: "fixture", Transport: domain.MCPHTTP, Endpoint: "https://mcp.example.test", Authentication: domain.MCPAnonymous, Enabled: true}
	create := Request{ID: domain.NewID(), Actor: actor, ServerID: id, Operation: Save, Definition: &def}
	if _, e = m.Mutate(ctx, create); e != nil {
		t.Fatal(e)
	}
	enable := false
	change := Request{ID: domain.NewID(), Actor: actor, ServerID: id, Revision: 1, Operation: Enable, Enabled: &enable}
	if _, e = m.Mutate(ctx, change); e != nil {
		t.Fatal(e)
	}
	stale := change
	stale.ID = domain.NewID()
	if _, e = m.Mutate(ctx, stale); e == nil {
		t.Fatal("stale mutation accepted")
	}
	deletion := Request{ID: domain.NewID(), Actor: actor, ServerID: id, Revision: 2, Operation: Delete}
	if _, e = m.Mutate(ctx, deletion); e == nil {
		t.Fatal("unconfirmed delete accepted")
	}
	deletion.Confirmed = true

	if result, e := m.Mutate(ctx, deletion); e != nil || !result.Deleted {
		t.Fatal(result, e)
	}
	if result, e := m.Mutate(ctx, deletion); e != nil || !result.Replayed || !result.Deleted {
		t.Fatal(result, e)
	}
}

func TestMCPCurrentGenerationAndIndependentJoinedCleanup(t *testing.T) {
	ctx := context.Background()
	vault := &fixtureSecrets{values: map[credentials.Ref][]byte{}}
	serverID, deviceID, actor := domain.NewID(), domain.NewID(), domain.NewID()
	m, e := open(t.TempDir(), serverID, deviceID, vault)
	if e != nil {
		t.Fatal(e)
	}
	id := domain.NewID()
	definition := domain.MCPDefinition{ID: id, Name: "original", Transport: domain.MCPHTTP, Endpoint: "https://mcp.example.test/api", Authentication: domain.MCPManual, HeaderNames: []string{"Authorization"}, Enabled: true}
	_, e = m.Mutate(ctx, Request{ID: domain.NewID(), Actor: actor, ServerID: id, Operation: Save, Definition: &definition, Secrets: &Secrets{Headers: map[string]string{"Authorization": "original-secret"}}})
	if e != nil {
		t.Fatal(e)
	}
	selection := domain.MCPSelection{MachineID: domain.NewID(), DeviceID: deviceID, ServerID: id, Revision: 1}
	m.state.MachineID = selection.MachineID
	first, second := domain.NewID(), domain.NewID()
	old, secret, e := m.PinCurrent(ctx, first, selection)
	if e != nil || old.Definition.Revision != 1 || secret.Headers["Authorization"] != "original-secret" {
		t.Fatal(old, e)
	}
	definition.Revision = 1
	definition.Name = "updated"
	_, e = m.Mutate(ctx, Request{ID: domain.NewID(), Actor: actor, ServerID: id, Revision: 1, Operation: Save, Definition: &definition, Secrets: &Secrets{Headers: map[string]string{"Authorization": "updated-secret"}}})
	if e != nil {
		t.Fatal(e)
	}
	current, _, e := m.PinCurrent(ctx, second, selection)
	if e != nil || current.Definition.Revision != 2 {
		t.Fatal("new generation did not resolve current Worker ownership", e)
	}
	replay, secret, e := m.PinCurrent(ctx, first, selection)
	if e != nil || replay.Definition.Revision != 1 || secret.Headers["Authorization"] != "original-secret" {
		t.Fatal("active generation changed", e)
	}
	wrong := selection
	wrong.MachineID = domain.NewID()
	if _, _, e = m.PinCurrent(ctx, domain.NewID(), wrong); e == nil {
		t.Fatal("another machine adopted protected values")
	}
	wrong = selection
	wrong.DeviceID = domain.NewID()
	if _, _, e = m.PinCurrent(ctx, domain.NewID(), wrong); e == nil {
		t.Fatal("another Worker adopted protected values")
	}
	request := Request{ID: domain.NewID(), Actor: actor, ServerID: id, Revision: 2, Operation: Delete, Confirmed: true}
	result, e := m.Mutate(ctx, request)
	if e != nil || !result.Deleted || len(vault.values) != 2 {
		t.Fatal("catalog deletion removed an active protected snapshot", e)
	}
	if _, _, e = m.PinCurrent(ctx, domain.NewID(), selection); e == nil {
		t.Fatal("retired catalog admitted a new execution")
	}
	if _, _, e = m.Pin(ctx, first, selection); e != nil {
		t.Fatal("original active pin could not recover", e)
	}
	if e = m.Release(ctx, first); e != nil || len(vault.values) != 2 {
		t.Fatal("cleanup removed another owner's credentials", e)
	}
	vault.failDelete = 1
	if e = m.Release(ctx, second); e == nil {
		t.Fatal("failed cleanup fabricated confirmation")
	}
	if len(m.state.Pins) != 1 {
		t.Fatal("failed cleanup lost its durable original claim")
	}
	if e = m.Release(ctx, second); e != nil || len(vault.values) != 0 || len(m.state.Pins) != 0 {
		t.Fatal("original cleanup could not resume", e)
	}
	if e = m.Release(ctx, second); e != nil {
		t.Fatal("confirmed cleanup was not idempotent", e)
	}
}
func TestMCPProtectedValueRejectsHeaderInjection(t *testing.T) {
	definition := domain.MCPDefinition{Authentication: domain.MCPManual, HeaderNames: []string{"Authorization"}}
	if validateSecrets(definition, Secrets{Headers: map[string]string{"Authorization": "value\r\nInjected: value"}}) == nil {
		t.Fatal("protected header injection accepted")
	}
}
