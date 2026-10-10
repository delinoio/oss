// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestHarnessInheritanceUpgradeFreezesHistoryAndFutureSourceDefaults(t *testing.T) {
	s, root := openTest(t)
	f := newExecutionFixture(t, s)
	session, input := f.session(t, domain.DispatchReady)
	if _, err := f.claim(domain.NewID(), session, input); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(readExecutionSession(t, s, session))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var restartErr error
	s, restartErr = Open(context.Background(), root)
	if restartErr != nil {
		t.Fatal(restartErr)
	}
	defer s.Close()
	f.store = s
	var first Record
	if err := s.Read(context.Background(), func(tx *Tx) error { var err error; first, err = tx.Get(domain.AgentKind, f.agent); return err }); err != nil {
		t.Fatal(err)
	}
	agent, err := Decode[domain.Agent](first)
	if err != nil || agent.HarnessSelection == nil || agent.Routes[0].ModelSelection.State != domain.InheritValue || agent.Effort != "" || agent.Routes[0].Accounts[0].ID != f.accounts[0] || agent.Templates[0] != f.template {
		t.Fatalf("conversion lost scope: %+v %v", agent, err)
	}
	if err := s.upgradeHarnessInheritance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Read(context.Background(), func(tx *Tx) error {
		again, err := tx.Get(domain.AgentKind, f.agent)
		if err == nil && (again.Revision != first.Revision || !bytes.Equal(again.Data, first.Data)) {
			t.Fatal("conversion was not idempotent")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(readExecutionSession(t, s, session))
	if !bytes.Equal(before, after) {
		t.Fatal("configuration conversion changed historical execution")
	}
	settingsID := domain.NewID()
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.harness-default", settingsID, func(tx *Tx) (any, error) {
		settings := domain.DefaultSettings()
		settings.HarnessDefaults = []domain.HarnessDefault{{Harness: domain.Codex, ProviderID: f.provider, Model: &domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: f.provider, NativeID: "inherited-fixture"}, MetadataSource: domain.UserDeclared}}}
		return tx.Put(domain.SettingsKind, settingsID, 0, "", "", settings)
	})
	if err != nil {
		t.Fatal(err)
	}
	next, nextInput := f.session(t, domain.DispatchReady)
	if _, err = f.claim(domain.NewID(), next, nextInput); err != nil {
		t.Fatal(err)
	}
	if readExecutionSession(t, s, next).InitialExecution.Configuration.NativeModel != "inherited-fixture" {
		t.Fatal("new execution did not use its source default")
	}
}
func TestHarnessInheritanceUpgradeRollsBackEntireConfiguration(t *testing.T) {
	s, _ := openTest(t)
	f := newExecutionFixture(t, s)
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.invalid-agent", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.AgentKind, domain.NewID(), 0, "", "", map[string]any{"foreign": true})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.upgradeHarnessInheritance(context.Background()); err == nil {
		t.Fatal("malformed configuration accepted")
	}
	if err = s.Read(context.Background(), func(tx *Tx) error {
		r, e := tx.Get(domain.AgentKind, f.agent)
		if e != nil {
			return e
		}
		agent, e := Decode[domain.Agent](r)
		if e == nil && (agent.HarnessSelection != nil || r.Revision != 1) {
			t.Fatal("partial conversion survived rollback")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}
