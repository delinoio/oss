// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"google.golang.org/protobuf/proto"
	"strings"
	"testing"
)

func TestHarnessDefaultsFenceOldClientAndRetainStaleDraft(t *testing.T) {
	s, _ := newDoctorFixture(t)
	id := domain.NewID()
	settings := domain.DefaultSettings()
	settings.HarnessDefaults = []domain.HarnessDefault{{Harness: domain.Codex, Values: domain.InheritedHarnessValues()}}
	doctorPut(t, s, domain.SettingsKind, id, 0, settings)
	old := domain.DefaultSettings()
	raw, _ := json.Marshal(old)
	_, err := SaveConfiguration(context.Background(), s.Store, ConfigurationMutation{RequestID: domain.NewID(), ID: id, ExpectedRevision: 1, Kind: domain.SettingsKind, Document: raw})
	if err == nil || domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("old client removed defaults", err)
	}
	settings.HarnessDefaults[0].Values.Effort = domain.InheritedValue[string]{State: domain.HarnessOverride, Value: new(string)}
	raw, _ = json.Marshal(settings)
	if rpc.ResourceSchemaVersion(domain.SettingsKind, raw) != 4 {
		t.Fatal("harness settings lost schema marker")
	}
	_, err = SaveConfiguration(context.Background(), s.Store, ConfigurationMutation{RequestID: domain.NewID(), ID: id, ExpectedRevision: 99, Kind: domain.SettingsKind, Document: raw})
	if err == nil || domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("stale draft overwrote original", err)
	}
	err = s.Store.Read(context.Background(), func(tx *store.Tx) error {
		r, err := tx.Get(domain.SettingsKind, id)
		if err == nil && r.Revision != 1 {
			t.Fatal("rejected write changed original")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestHarnessDefaultsPortableMappingsAndNullRejection(t *testing.T) {
	oldProvider, oldModel, newProvider, newModel := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	values := domain.InheritedHarnessValues()
	values.Model = domain.InheritedValue[domain.ID]{State: domain.HarnessOverride, Value: &oldModel}
	entries := []domain.HarnessDefault{{Harness: domain.Codex, ProviderID: oldProvider, Values: values}}
	err := rewriteHarnessDefaults(entries, func(id *domain.ID, kind domain.Kind) error {
		if kind == domain.ProviderKind {
			*id = newProvider
		} else {
			*id = newModel
		}
		return nil
	})
	if err != nil || entries[0].ProviderID != newProvider || *entries[0].Values.Model.Value != newModel {
		t.Fatal("portable defaults kept foreign IDs", err)
	}
	settings := domain.DefaultSettings()
	raw, _ := json.Marshal(settings)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	fields["harness_defaults"] = json.RawMessage("null")
	raw, _ = json.Marshal(fields)
	if _, err := configurationValue(domain.SettingsKind, raw, false); err == nil {
		t.Fatal("null defaults accepted")
	}
	if domain.ConfigurationBundleVersion != 7 {
		t.Fatal("inheritance must have a versioned portable shape")
	}
}

func TestHarnessNativeDefaultsRequireOriginalReadyAndNegotiation(t *testing.T) {
	f := directStartupFixture(t)
	f.registerGrant(t)
	model := "native-fixture"
	o := domain.ExecutionStartupObservation{State: domain.StartupReady, Phase: domain.StartupSettings, Harness: domain.Codex, NativeVersion: domain.CodexProtocolVersion, ExecutableSHA256: strings.Repeat("a", 64), Protocol: domain.CodexAppServer, CorrelationID: f.job, InputDelivery: domain.StartupNotSent, NativeDefaults: &domain.NativeHarnessDefaults{Version: 1, Model: &model}}
	if _, err := f.client.ReportExecutionStartup(context.Background(), startupRequest(f, o)); err == nil {
		t.Fatal("unnegotiated defaults accepted")
	}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.native-default-capability", nil, func(tx *store.Tx) (any, error) {
		r, m, e := activeMachine(tx, f.input.MachineID)
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.NativeHarnessDefaultsV1)
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	req := startupRequest(f, o)
	first, err := f.client.ReportExecutionStartup(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.client.ReportExecutionStartup(context.Background(), req)
	if err != nil || !replay.Msg.Replayed || !proto.Equal(first.Msg.Observation, replay.Msg.Observation) {
		t.Fatalf("default observation receipt changed: %v", err)
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		proof, e := tx.NativeHarnessDefaults(f.input.MachineID, f.input.AccountID, "")
		if e != nil {
			return e
		}
		if proof.JobID != f.job || proof.ExecutionID != f.input.ExecutionID || proof.ConnectionID != f.input.ConnectionID || proof.Defaults.Model == nil || *proof.Defaults.Model != model {
			t.Fatal("original default ownership changed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
