// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestHarnessInheritanceUpgradeAtomicAndHistoryUnchanged(t *testing.T) {
	s, _ := openTest(t)
	f := newExecutionFixture(t, s)
	secondSource := newExecutionFixture(t, s)
	ctx := context.Background()
	first, _ := f.session(t, domain.DispatchReady)
	var history domain.InitialExecution
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.freeze", first, func(tx *Tx) (any, error) {
		r, e := tx.Get(domain.SessionKind, first)
		if e != nil {
			return nil, e
		}
		var session domain.Session
		json.Unmarshal(r.Data, &session)
		ir, e := tx.OldestQueuedInput(first)
		if e != nil {
			return nil, e
		}
		history, e = tx.ClaimInitialExecution(first, r.Revision, ir.ID, ir.Revision)
		return history, e
	})
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.Get(ctx, domain.SessionKind, first)
	if err != nil {
		t.Fatal(err)
	}
	agentBefore, _ := s.Get(ctx, domain.AgentKind, f.agent)
	if err := upgradeHarnessAgents(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Get(ctx, domain.AgentKind, f.agent)
	agent, err := Decode[domain.Agent](after)
	if err != nil || agent.HarnessSettings == nil || after.Revision != agentBefore.Revision+1 {
		t.Fatalf("upgrade: %+v %v", after, err)
	}
	historical, _ := s.Get(ctx, domain.SessionKind, first)
	if string(historical.Data) != string(original.Data) {
		t.Fatal("historical execution changed")
	}
	if err := upgradeHarnessAgents(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	again, _ := s.Get(ctx, domain.AgentKind, f.agent)
	if again.Revision != after.Revision {
		t.Fatal("restart repeats migration")
	}
	secondRecord, err := s.Get(ctx, domain.AgentKind, secondSource.agent)
	if err != nil {
		t.Fatal(err)
	}
	secondAgent, err := Decode[domain.Agent](secondRecord)
	if err != nil || secondAgent.HarnessSettings == nil {
		t.Fatal("second source not converted", err)
	}
	if history.ConfigurationDigest == "" {
		t.Fatal("fixture did not freeze original history")
	}
	second, _ := f.session(t, domain.DispatchReady)
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.no-evidence", second, func(tx *Tx) (any, error) {
		r, e := tx.Get(domain.SessionKind, second)
		if e != nil {
			return nil, e
		}
		ir, e := tx.OldestQueuedInput(second)
		if e != nil {
			return nil, e
		}
		return tx.ClaimInitialExecution(second, r.Revision, ir.ID, ir.Revision)
	})
	if err == nil || domain.SafeError(err).Message != "Harness model default is unavailable." {
		t.Fatalf("unknown defaults accepted: %v", err)
	}
	r, _ := s.Get(ctx, domain.SessionKind, second)
	value, _ := Decode[domain.Session](r)
	if value.InitialExecution != nil {
		t.Fatal("missing defaults claimed execution")
	}
	defaults := domain.DefaultSettings()
	for i, fixture := range []executionFixture{f, secondSource} {
		native := []string{"native-default-one", "native-default-two"}[i]
		model := domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: fixture.provider, NativeID: native}, MetadataSource: domain.UserDeclared}
		defaults.HarnessDefaults = append(defaults.HarnessDefaults, domain.HarnessDefault{Harness: domain.Codex, ProviderID: fixture.provider, Model: domain.HarnessSetting[domain.InlineModel]{Mode: domain.OverrideSetting, Value: &model}, AgentHarnessSettings: *domain.InheritedHarnessSettings()})
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.source-defaults", nil, func(tx *Tx) (any, error) { return tx.Put(domain.SettingsKind, domain.NewID(), 0, "", "", defaults) })
	if err != nil {
		t.Fatal(err)
	}
	for i, fixture := range []executionFixture{f, secondSource} {
		id, _ := fixture.session(t, domain.DispatchReady)
		_, err = s.Mutate(ctx, domain.NewID(), "fixture.inherited-freeze", id, func(tx *Tx) (any, error) {
			r, e := tx.Get(domain.SessionKind, id)
			if e != nil {
				return nil, e
			}
			ir, e := tx.OldestQueuedInput(id)
			if e != nil {
				return nil, e
			}
			claim, e := tx.ClaimInitialExecution(id, r.Revision, ir.ID, ir.Revision)
			if e == nil && claim.Configuration.NativeModel != []string{"native-default-one", "native-default-two"}[i] {
				t.Fatal("source defaults conflated")
			}
			return claim, e
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	historical, _ = s.Get(ctx, domain.SessionKind, first)
	if string(historical.Data) != string(original.Data) {
		t.Fatal("new defaults rewrote original history")
	}

}
func TestHarnessInheritanceUpgradeRollsBackMalformedAgent(t *testing.T) {
	s, _ := openTest(t)
	f := newExecutionFixture(t, s)
	ctx := context.Background()
	before, _ := s.Get(ctx, domain.AgentKind, f.agent)
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.malformed-agent", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.AgentKind, domain.NewID(), 0, "", "", map[string]any{"unknown_field": true})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := upgradeHarnessAgents(ctx, s.db); err == nil {
		t.Fatal("partial upgrade accepted")
	}
	after, _ := s.Get(ctx, domain.AgentKind, f.agent)
	if before.Revision != after.Revision || string(before.Data) != string(after.Data) {
		t.Fatal("failed upgrade changed other Agent")
	}
}
