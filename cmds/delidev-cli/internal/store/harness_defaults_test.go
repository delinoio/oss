// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestHarnessDefaultUpgradeAtomicRestartAndHistoricalBytes(t *testing.T) {
	s, root := openTest(t)
	ids := []domain.ID{domain.NewID(), domain.NewID()}
	providers := []domain.ID{domain.NewID(), domain.NewID()}
	original := make([]Record, 2)
	for i, id := range ids {
		a := domain.Agent{Name: "Legacy Worker", Harness: domain.Codex, Effort: "high", Options: domain.AgentOptions{Permission: domain.PermissionDefault}, Templates: []domain.ID{}, Routes: []domain.AgentSourceRoute{{Model: &domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: providers[i], NativeID: "legacy-model"}, MetadataSource: domain.Unknown}, Accounts: []domain.WeightedAccount{{ID: domain.NewID(), Weight: 1}}}}}
		result, err := s.Mutate(context.Background(), domain.NewID(), "fixture.legacy-worker", a, func(tx *Tx) (any, error) { return tx.Put(domain.AgentKind, id, 0, "", "", a) })
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(result.Data, &original[i]); err != nil {
			t.Fatal(err)
		}
	}
	// A retained historical job document stands in for byte-owned execution
	// history; admission must never decode or rewrite it during config upgrade.
	historyID := domain.NewID()
	history := []byte(`{"configuration_digest":"original-history","native_model":"legacy-model","effort":"high"}`)
	if _, err := s.db.Exec("INSERT INTO entities(id,kind,revision,session_id,project_id,body,created_at,updated_at) VALUES(?,'job',1,'','',?,1,1)", historyID, history); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("CREATE TRIGGER fail_harness_upgrade BEFORE UPDATE ON entities WHEN NEW.kind='agent' AND NEW.id='" + string(ids[1]) + "' BEGIN SELECT RAISE(ABORT,'injected upgrade fault'); END"); err != nil {
		t.Fatal(err)
	}
	if err := upgradeHarnessDefaults(context.Background(), s.db); err == nil {
		t.Fatal("faulted upgrade committed")
	}
	for i, id := range ids {
		r, err := s.Get(context.Background(), domain.AgentKind, id)
		if err != nil || r.Revision != original[i].Revision || !bytes.Equal(r.Data, original[i].Data) {
			t.Fatalf("partial upgrade: %v %#v", err, r)
		}
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_harness_upgrade"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for i, id := range ids {
		r, err := reopened.Get(context.Background(), domain.AgentKind, id)
		if err != nil {
			t.Fatal(err)
		}
		a, err := Decode[domain.Agent](r)
		if err != nil {
			t.Fatal(err)
		}
		if r.Revision != original[i].Revision+1 || a.HarnessSettings == nil || a.HarnessSettings.Effort.Mode != domain.SettingInherit || a.HarnessSettings.Options.Mode != domain.SettingInherit || a.Routes[0].ModelMode != domain.SettingInherit || a.Routes[0].Model.ProviderID != providers[i] || a.Routes[0].Model.NativeID != "legacy-model" {
			t.Fatalf("incorrect automatic conversion: %#v", a)
		}
	}
	r, err := reopened.Get(context.Background(), domain.JobKind, historyID)
	if err != nil || !bytes.Equal(r.Data, history) {
		t.Fatalf("history changed: %v %s", err, r.Data)
	}
	if err := upgradeHarnessDefaults(context.Background(), reopened.db); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		r, err := reopened.Get(context.Background(), domain.AgentKind, id)
		if err != nil || r.Revision != original[i].Revision+1 {
			t.Fatal("repeated upgrade changed original revision")
		}
	}
}
