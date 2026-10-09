// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestHarnessVerifiedNativeDefaultsFreezeAndScope(t *testing.T) {
	s, _ := openTest(t)
	f := newExecutionFixture(t, s)
	device := domain.NewID()
	ctx := context.Background()
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.native-harness-defaults", nil, func(tx *Tx) (any, error) {
		mr, m, e := decodeEntity[domain.Machine](tx, domain.MachineKind, f.machine)
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = []domain.WorkerCapability{domain.NativeHarnessDefaultsV1}
		if _, e = tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", m); e != nil {
			return nil, e
		}
		if _, e = tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Original", Type: domain.WorkerDevice, MachineID: f.machine, PairedAt: time.Now().UTC()}); e != nil {
			return nil, e
		}
		ar, a, e := decodeEntity[domain.Agent](tx, domain.AgentKind, f.agent)
		if e != nil {
			return nil, e
		}
		a.HarnessSettings = domain.NewInheritedAgentSettings(1)
		if _, e = tx.Put(domain.AgentKind, ar.ID, ar.Revision, "", "", a); e != nil {
			return nil, e
		}
		model, effort := "native-fixture", "medium"
		for _, id := range f.accounts {
			_, account, e := decodeEntity[domain.Account](tx, domain.AccountKind, id)
			if e != nil {
				return nil, e
			}
			input := domain.ExecutionJobInput{Version: 4, MachineID: f.machine, AccountID: id, ConnectionID: account.Connection.ID, ExecutionID: domain.NewID()}
			job := domain.NewID()
			o := domain.ExecutionStartupObservation{State: domain.StartupReady, Phase: domain.StartupSettings, Harness: domain.Codex, NativeVersion: domain.CodexProtocolVersion, ExecutableSHA256: strings.Repeat("a", 64), Protocol: domain.CodexAppServer, CorrelationID: job, InputDelivery: domain.StartupNotSent, NativeDefaults: &domain.NativeHarnessDefaults{Version: 1, Model: &model, Effort: &effort}}
			if e = tx.RecordNativeHarnessDefaults(input, job, device, "", o); e != nil {
				return nil, e
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	session, _ := f.session(t, domain.DispatchReady)
	var frozen domain.ExecutionConfiguration
	err = s.Read(ctx, func(tx *Tx) error {
		_, v, e := decodeEntity[domain.Session](tx, domain.SessionKind, session)
		if e != nil {
			return e
		}
		preview, e := tx.PreviewInitialExecution(v)
		if e != nil {
			return e
		}
		frozen = preview.Configuration
		if frozen.ModelID != f.model || frozen.Effort != "medium" || frozen.NativeDefaults == nil || frozen.NativeDefaults.AccountID != preview.AccountID {
			t.Fatal("valid native default proof was not consumed and frozen")
		}
		if _, e = tx.NativeHarnessDefaults(domain.NewID(), preview.AccountID, ""); e == nil {
			t.Fatal("foreign machine used original proof")
		}
		if _, e = tx.NativeHarnessDefaults(f.machine, preview.AccountID, domain.NewID()); e == nil {
			t.Fatal("foreign project used original proof")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := frozen.Digest()
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.rotate-native-default-scope", nil, func(tx *Tx) (any, error) {
		ar, a, e := decodeEntity[domain.Account](tx, domain.AccountKind, frozen.NativeDefaults.AccountID)
		if e != nil {
			return nil, e
		}
		a.Connection.ID = domain.NewID()
		return tx.Put(domain.AccountKind, ar.ID, ar.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Read(ctx, func(tx *Tx) error {
		if _, e := tx.NativeHarnessDefaults(f.machine, frozen.NativeDefaults.AccountID, ""); e == nil {
			t.Fatal("rotated account accepted previous proof")
		}
		_, v, e := decodeEntity[domain.Session](tx, domain.SessionKind, session)
		if e != nil {
			return e
		}
		if _, e = tx.PreviewInitialExecution(v); e == nil {
			t.Fatal("missing current source proof silently used legacy model")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := frozen.Digest()
	if digest != after {
		t.Fatal("new source state changed immutable execution snapshot")
	}
}

func TestHarnessTwoProviderNativeDefaultsPreserveLegacyHistory(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	fixtures := []executionFixture{newExecutionFixture(t, s), newExecutionFixture(t, s)}
	sessions := make([]domain.ID, 2)
	history := make([]string, 2)
	for i, f := range fixtures {
		session, input := f.session(t, domain.DispatchReady)
		sessions[i] = session
		if _, err := f.claim(domain.NewID(), session, input); err != nil {
			t.Fatal(err)
		}
		record, err := s.Get(ctx, domain.SessionKind, session)
		if err != nil {
			t.Fatal(err)
		}
		history[i] = string(record.Data)
	}
	if err := upgradeHarnessAgents(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	for i, f := range fixtures {
		native, effort := "native-fixture", "medium"
		if i == 1 {
			native, effort = "second-default", "low"
		}
		_, err := s.Mutate(ctx, domain.NewID(), "fixture.source-native-default", f.provider, func(tx *Tx) (any, error) {
			mr, m, e := decodeEntity[domain.Machine](tx, domain.MachineKind, f.machine)
			if e != nil {
				return nil, e
			}
			m.WorkerCapabilities = []domain.WorkerCapability{domain.NativeHarnessDefaultsV1}
			if _, e = tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", m); e != nil {
				return nil, e
			}
			modelRecord, model, e := decodeEntity[domain.Model](tx, domain.ModelKind, f.model)
			if e != nil {
				return nil, e
			}
			model.NativeID = native
			if _, e = tx.Put(domain.ModelKind, modelRecord.ID, modelRecord.Revision, "", "", model); e != nil {
				return nil, e
			}
			device := domain.NewID()
			if _, e = tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Original", Type: domain.WorkerDevice, MachineID: f.machine, PairedAt: time.Now().UTC()}); e != nil {
				return nil, e
			}
			for _, id := range f.accounts {
				_, account, e := decodeEntity[domain.Account](tx, domain.AccountKind, id)
				if e != nil {
					return nil, e
				}
				job := domain.NewID()
				input := domain.ExecutionJobInput{Version: 4, MachineID: f.machine, AccountID: id, ConnectionID: account.Connection.ID, ExecutionID: domain.NewID()}
				o := domain.ExecutionStartupObservation{State: domain.StartupReady, Phase: domain.StartupSettings, Harness: domain.Codex, NativeVersion: domain.CodexProtocolVersion, ExecutableSHA256: strings.Repeat("a", 64), Protocol: domain.CodexAppServer, CorrelationID: job, InputDelivery: domain.StartupNotSent, NativeDefaults: &domain.NativeHarnessDefaults{Version: 1, Model: &native, Effort: &effort}}
				if e = tx.RecordNativeHarnessDefaults(input, job, device, "", o); e != nil {
					return nil, e
				}
			}
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		next, _ := f.session(t, domain.DispatchReady)
		err = s.Read(ctx, func(tx *Tx) error {
			_, session, e := decodeEntity[domain.Session](tx, domain.SessionKind, next)
			if e != nil {
				return e
			}
			preview, e := tx.PreviewInitialExecution(session)
			if e != nil {
				return e
			}
			if preview.Configuration.ProviderID != f.provider || preview.Configuration.NativeModel != native || preview.Configuration.Effort != effort || preview.Configuration.NativeDefaults == nil {
				t.Fatal("different native defaults lost their original source")
			}
			old, e := tx.Get(domain.SessionKind, sessions[i])
			if e != nil {
				return e
			}
			if string(old.Data) != history[i] {
				t.Fatal("legacy execution history changed")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
