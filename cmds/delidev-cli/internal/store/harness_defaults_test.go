// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"path/filepath"
	"testing"
)

func TestHarnessUpgradeAtomicIdempotentAndHistoricalSnapshots(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "state")
	s, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	f := newExecutionFixture(t, s)
	session, input := f.session(t, domain.DispatchReady)
	if _, err := f.claim(domain.NewID(), session, input); err != nil {
		t.Fatal(err)
	}
	before, err := s.Get(ctx, domain.SessionKind, session)
	if err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), before.Data...)
	if err := upgradeHarnessAgents(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	var upgraded Record
	err = s.Read(ctx, func(tx *Tx) error {
		var err error
		upgraded, err = tx.Get(domain.AgentKind, f.agent)
		if err != nil {
			return err
		}
		a, err := Decode[domain.Agent](upgraded)
		if err != nil {
			return err
		}
		if a.HarnessSettings == nil || a.HarnessSettings.Models[0].State != domain.HarnessInherit || a.ModelID != f.model || a.Effort != "high" || a.Templates[0] != f.template {
			t.Fatal("upgrade changed source/template anchors or omitted inheritance")
		}
		history, err := tx.Get(domain.SessionKind, session)
		if err == nil && string(history.Data) != string(original) {
			t.Fatal("historical bytes changed")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := upgradeHarnessAgents(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	err = s.Read(ctx, func(tx *Tx) error {
		r, err := tx.Get(domain.AgentKind, f.agent)
		if err == nil && r.Revision != upgraded.Revision {
			t.Fatal("upgrade was not idempotent")
		}
		a, err := Decode[domain.Agent](r)
		if err != nil {
			return err
		}
		_, err = tx.resolveHarnessSource(a, a.SourceRoutes()[0], 0, nil)
		if err == nil || domain.SafeError(err).Code != domain.MissingInput {
			t.Fatal("unknown source defaults did not block")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
}

func TestHarnessUpgradeRollbackAndSourceDefaults(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	f := newExecutionFixture(t, s)
	g := newExecutionFixture(t, s)
	bad := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.invalid-harness", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.AgentKind, bad, 0, "", "", domain.Agent{Name: "invalid", Harness: "unknown"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := upgradeHarnessAgents(ctx, s.db); err == nil {
		t.Fatal("invalid batch committed")
	}
	r, err := s.Get(ctx, domain.AgentKind, f.agent)
	if err != nil || r.Revision != 1 {
		t.Fatal("failed upgrade changed earlier Agent", err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.remove-invalid", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.AgentKind, bad, 1) })
	if err != nil {
		t.Fatal(err)
	}
	if err := upgradeHarnessAgents(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	entries := []domain.HarnessDefault{}
	for i, fixture := range []executionFixture{f, g} {
		values := domain.InheritedHarnessValues()
		id := fixture.model
		effort := []string{"low", "high"}[i]
		values.Model = domain.InheritedValue[domain.ID]{State: domain.HarnessOverride, Value: &id}
		values.Effort = domain.InheritedValue[string]{State: domain.HarnessOverride, Value: &effort}
		entries = append(entries, domain.HarnessDefault{Harness: domain.Codex, ProviderID: fixture.provider, Values: values})
	}
	settings := domain.DefaultSettings()
	settings.HarnessDefaults = entries
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.harness-defaults", nil, func(tx *Tx) (any, error) { return tx.Put(domain.SettingsKind, domain.NewID(), 0, "", "", settings) })
	if err != nil {
		t.Fatal(err)
	}
	err = s.Read(ctx, func(tx *Tx) error {
		for i, fixture := range []executionFixture{f, g} {
			r, err := tx.Get(domain.AgentKind, fixture.agent)
			if err != nil {
				return err
			}
			a, err := Decode[domain.Agent](r)
			if err != nil {
				return err
			}
			effective, err := tx.resolveHarnessSource(a, a.SourceRoutes()[0], 0, nil)
			if err != nil {
				return err
			}
			if effective.ModelID != fixture.model || effective.Effort != []string{"low", "high"}[i] {
				t.Fatal("source defaults crossed providers")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
