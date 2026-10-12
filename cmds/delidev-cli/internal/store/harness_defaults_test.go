// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func legacyHarnessAgent(provider, account domain.ID) domain.Agent {
	return domain.Agent{Name: "Legacy", Harness: domain.Codex, Routes: []domain.AgentSourceRoute{{Model: &domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: provider, NativeID: "owner-chosen"}, MetadataSource: domain.UserDeclared}, Accounts: []domain.WeightedAccount{{ID: account, Weight: 3}}}}, Effort: "high", Options: domain.AgentOptions{Permission: domain.PermissionFullAccess, ServiceTier: "fast"}}
}
func TestHarnessUpgradeIsAtomicIdempotentAndLeavesHistoricalBytes(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	first, second := domain.NewID(), domain.NewID()
	if second < first {
		first, second = second, first
	}
	historyID := domain.NewID()
	history := `{"initial_execution":{"configuration":{"native_model":"owner-chosen","effort":"high"},"configuration_digest":"retained-original-digest"}}`
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.legacy-harness", nil, func(tx *Tx) (any, error) {
		for _, id := range []domain.ID{first, second} {
			a := legacyHarnessAgent(domain.NewID(), domain.NewID())
			a.Templates = []domain.ID{domain.NewID()}
			if _, err := tx.Put(domain.AgentKind, id, 0, "", "", a); err != nil {
				return nil, err
			}
		}
		// Historical bytes are independent recovery evidence; the live document
		// upgrade must neither decode nor rewrite this retained session fixture.
		_, err := tx.tx.ExecContext(ctx, "INSERT INTO entities(id,kind,revision,session_id,project_id,body,created_at,updated_at) VALUES(?,'session',1,'','',?,1,1)", historyID, history)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get(ctx, domain.AgentKind, first)
	// A failure after the first Agent exercises the same SQLite rollback that
	// protects a restart during upgrade. No partial Agent/event commit survives.
	_, err = s.db.ExecContext(ctx, "CREATE TRIGGER fixture_upgrade_failure BEFORE UPDATE ON entities WHEN OLD.id='"+string(second)+"' BEGIN SELECT RAISE(ABORT,'fixture upgrade failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	if err := upgradeHarnessDefaults(ctx, s.db); err == nil {
		t.Fatal("interrupted upgrade succeeded")
	}
	unchanged, _ := s.Get(ctx, domain.AgentKind, first)
	if string(before.Data) != string(unchanged.Data) || unchanged.Revision != before.Revision {
		t.Fatal("partial upgrade committed")
	}
	if _, err = s.db.ExecContext(ctx, "DROP TRIGGER fixture_upgrade_failure"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	var upgraded Record
	for _, id := range []domain.ID{first, second} {
		row, err := s.Get(ctx, domain.AgentKind, id)
		if err != nil {
			t.Fatal(err)
		}
		a, err := Decode[domain.Agent](row)
		if err != nil || a.Validate() != nil {
			t.Fatal(err, a)
		}
		if row.Revision != 2 || a.HarnessSettings == nil || a.Routes[0].Model.NativeID != "" || a.Routes[0].ModelSelection.Mode != domain.HarnessInherit || a.Effort != "" || a.Options != (domain.AgentOptions{Permission: domain.PermissionDefault}) || a.Routes[0].Accounts[0].Weight != 3 || len(a.Templates) != 1 {
			t.Fatal("wrong conversion", a)
		}
		upgraded = row
	}
	historical, err := s.Get(ctx, domain.SessionKind, historyID)
	if err != nil || string(historical.Data) != history || historical.Revision != 1 {
		t.Fatal("historical bytes changed", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	row, _ := s.Get(ctx, domain.AgentKind, upgraded.ID)
	if row.Revision != upgraded.Revision || string(row.Data) != string(upgraded.Data) {
		t.Fatal("restart repeated conversion")
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal("configuration upgrade migrated SQLite", version, err)
	}
}

func TestNativeHarnessDefaultEvidenceRemainsExactAndReadOnly(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for _, effort := range []domain.NativeReasoningEffort{domain.NativeReasoningLow, domain.NativeReasoningHigh} {
		provider, account, machine, connection := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
		var jobID domain.ID
		_, err := s.Mutate(ctx, domain.NewID(), "fixture.native-defaults", nil, func(tx *Tx) (any, error) {
			m := domain.Machine{Name: "Fixture", OS: "linux", Architecture: "amd64", DiscoveryRevision: 1, WorkerCapabilities: []domain.WorkerCapability{domain.NativeModelsV1}, Installations: []domain.Installation{{Harness: domain.Codex, State: domain.InstallationDetected, Version: domain.CodexProtocolVersion, ResolvedPath: "/fixture/codex", ExecutableSHA256: strings.Repeat("a", 64)}}}
			if _, err := tx.Put(domain.MachineKind, machine, 0, "", "", m); err != nil {
				return nil, err
			}
			a := domain.Account{Alias: "Fixture", Type: domain.APIAccount, ProviderID: provider, Enabled: true, Health: domain.AccountReady, Connection: &domain.AccountConnection{ID: connection, Authentication: domain.KeylessAuth, ConnectedAt: tx.now}}
			if _, err := tx.Put(domain.AccountKind, account, 0, "", "", a); err != nil {
				return nil, err
			}
			scope := domain.NativeModelScope{Version: 1, MachineID: machine, MachineRevision: 1, InstallationGeneration: 1, NativeVersion: domain.CodexProtocolVersion, Executable: "/fixture/codex", ExecutableSHA256: strings.Repeat("a", 64), AccountID: account, AccountRevision: 1, ConnectionID: connection, ProviderID: provider, Actor: domain.Principal{Type: domain.OwnerDevice}, IncludeHidden: effort == domain.NativeReasoningHigh}
			input, _ := json.Marshal(scope)
			output, _ := json.Marshal(domain.NativeModelObservation{Version: 1, ObservedAt: tx.now, CleanupVerified: true, Models: []domain.NativeModel{{ID: "model", Model: "model", DisplayName: "Fixture", Reasoning: []domain.NativeReasoningEffort{effort}, DefaultReasoning: effort}}})
			jobID = domain.NewID()
			_, err := tx.PutJob(jobID, 0, "", "", domain.Job{Type: domain.NativeModelsJob, State: domain.JobSucceeded, MachineID: machine, ParentID: account, Input: input, Output: output, AcceptedAt: tx.now, FinishedAt: &tx.now})
			return nil, err
		})
		if err != nil {
			t.Fatal(err)
		}
		before, _ := s.Get(ctx, domain.JobKind, jobID)
		err = s.Read(ctx, func(tx *Tx) error {
			_, a, err := decodeEntity[domain.Account](tx, domain.AccountKind, account)
			if err != nil {
				return err
			}
			layer, err := tx.nativeHarnessDefaultLayer(machine, account, a, "model", domain.Codex)
			if err != nil {
				return err
			}
			if layer == nil || *layer.Settings.Effort.Value != string(effort) || layer.Origin.ID != jobID || layer.Origin.EvidenceDigest == "" || layer.Settings.Model != nil {
				t.Fatal("wrong native source default", layer)
			}
			a.Connection.ID = domain.NewID()
			layer, err = tx.nativeHarnessDefaultLayer(machine, account, a, "model", domain.Codex)
			if err != nil {
				return err
			}
			if layer != nil {
				t.Fatal("foreign connection supplied defaults")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		after, _ := s.Get(ctx, domain.JobKind, jobID)
		if string(before.Data) != string(after.Data) || before.Revision != after.Revision {
			t.Fatal("default resolution launched or rewrote native evidence")
		}
	}
}

func TestCurrentAgentCreateDoesNotChangeOnStoreRestart(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	a := legacyHarnessAgent(domain.NewID(), domain.NewID())
	raw, _ := json.Marshal(a)
	if err := domain.NormalizeNewAgentHarnessSettings(&a, raw); err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.current-create", nil, func(tx *Tx) (any, error) { return tx.Put(domain.AgentKind, id, 0, "", "", a) })
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get(ctx, domain.AgentKind, id)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, _ := s.Get(ctx, domain.AgentKind, id)
	if string(before.Data) != string(after.Data) || before.Revision != after.Revision || !after.CreatedAt.Equal(before.CreatedAt) || after.UpdatedAt.Before(time.Unix(0, 0)) {
		t.Fatal("current create changed on restart")
	}
}
