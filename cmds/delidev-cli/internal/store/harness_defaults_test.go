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

func TestHarnessDefaultsSelectedAccountProfileAndFrozenSnapshot(t *testing.T) {
	for _, scenario := range []string{"selected-profile", "missing-profile-default", "ambiguous-profile"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := openTest(t)
			f := newExecutionFixture(t, s)
			settingsID := domain.NewID()
			matchingModel := &domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: f.provider, NativeID: "selected-responses-default"}, MetadataSource: domain.UserDeclared}
			chat := domain.ProviderAPIFormat{Protocol: domain.OpenAIChat, Endpoint: "http://127.0.0.1:1/v1", Authentication: domain.BearerAuth}
			responses := domain.ProviderAPIFormat{Protocol: domain.OpenAIResponses, Endpoint: "http://127.0.0.1:2/v1", Authentication: domain.KeylessAuth}
			high, wrong := "high", "wrong-profile"
			var settingsRevision uint64
			_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.profile-defaults", settingsID, func(tx *Tx) (any, error) {
				providerRecord, provider, err := decodeEntity[domain.Provider](tx, domain.ProviderKind, f.provider)
				if err != nil {
					return nil, err
				}
				provider.Protocol, provider.Endpoint, provider.Authentication = chat.Protocol, chat.Endpoint, chat.Authentication
				provider.APIFormats = []domain.ProviderAPIFormat{chat, responses}
				if scenario == "ambiguous-profile" {
					provider.APIFormats = append(provider.APIFormats, responses)
				}
				if _, err = tx.Put(domain.ProviderKind, f.provider, providerRecord.Revision, "", "", provider); err != nil {
					return nil, err
				}
				for index, id := range f.accounts {
					record, account, err := decodeEntity[domain.Account](tx, domain.AccountKind, id)
					if err != nil {
						return nil, err
					}
					profile := responses
					if index == 0 {
						profile = chat
						account.Validation.Authentication = domain.CredentialAccepted
					}
					account.APIProtocol, account.Connection.Authentication, account.Connection.APIFormat = profile.Protocol, profile.Authentication, &profile
					if _, err = tx.Put(domain.AccountKind, id, record.Revision, "", "", account); err != nil {
						return nil, err
					}
				}
				record, agent, err := decodeEntity[domain.Agent](tx, domain.AgentKind, f.agent)
				if err != nil {
					return nil, err
				}
				policy := domain.Priority
				agent.Routes[0].Routing = &policy
				agent.HarnessSelection = &domain.HarnessSelection{}
				agent.Routes[0].ModelSelection = &domain.InheritedValue[domain.InlineModel]{State: domain.InheritValue}
				if _, err = tx.Put(domain.AgentKind, f.agent, record.Revision, "", "", agent); err != nil {
					return nil, err
				}
				settings := domain.DefaultSettings()
				settings.HarnessDefaults = []domain.HarnessDefault{
					{Harness: domain.Codex, ProviderID: f.provider, APIProtocol: domain.OpenAIChat, Model: &domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: f.provider, NativeID: "must-not-use-sibling"}, MetadataSource: domain.UserDeclared}, Selection: domain.HarnessSelection{Effort: &domain.InheritedValue[string]{State: domain.OverrideValue, Value: &wrong}}},
					{Harness: domain.Codex, ProviderID: f.provider, APIProtocol: domain.OpenAIResponses, Model: matchingModel, Selection: domain.HarnessSelection{Effort: &domain.InheritedValue[string]{State: domain.OverrideValue, Value: &high}}},
				}
				if scenario == "missing-profile-default" {
					settings.HarnessDefaults[1].Model = nil
				}
				saved, err := tx.Put(domain.SettingsKind, settingsID, 0, "", "", settings)
				settingsRevision = saved.Revision
				return nil, err
			})
			if err != nil {
				t.Fatal(err)
			}
			session, input := f.session(t, domain.DispatchReady)
			before, _ := json.Marshal(readExecutionSession(t, s, session))
			_, err = f.claim(domain.NewID(), session, input)
			if scenario != "selected-profile" {
				if err == nil {
					t.Fatal("missing or ambiguous selected-profile evidence admitted input")
				}
				if scenario == "missing-profile-default" && domain.SafeError(err).Code != domain.MissingInput {
					t.Fatalf("incorrect missing default error: %v", err)
				}
				after, _ := json.Marshal(readExecutionSession(t, s, session))
				if !bytes.Equal(before, after) {
					t.Fatal("failed default resolution changed the session")
				}
				queued, e := s.Get(context.Background(), domain.QueueKind, input)
				if e != nil {
					t.Fatal(e)
				}
				value, e := Decode[domain.QueuedInput](queued)
				if e != nil || value.Delivery != domain.InputQueued {
					t.Fatal("failed profile resolution consumed input")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			frozen := readExecutionSession(t, s, session).InitialExecution
			configuration := frozen.Configuration
			if frozen.InitialAccountID != f.accounts[1] || configuration.NativeModel != matchingModel.NativeID || configuration.Effort != high || configuration.HarnessDefaults.SettingsID != settingsID || configuration.HarnessDefaults.SettingsRevision != settingsRevision {
				t.Fatalf("selected account profile did not own frozen defaults: %+v", frozen)
			}
			source := frozen.Route.Sources[0]
			if source.NativeModel != matchingModel.NativeID || source.ModelID != matchingModel.ModelIdentity.Key() || source.Route.Candidates[0].ID != f.accounts[0] || source.Route.Candidates[0].Eligibility != domain.IncompatibleAccount || source.Route.Candidates[1].ID != f.accounts[1] {
				t.Fatalf("routing identity/order/profile attribution changed: %+v", source)
			}
			retained, _ := json.Marshal(frozen)
			_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.profile-default-edit", settingsID, func(tx *Tx) (any, error) {
				record, settings, err := decodeEntity[domain.Settings](tx, domain.SettingsKind, settingsID)
				if err != nil {
					return nil, err
				}
				settings.HarnessDefaults[1].Model.NativeID = "later-responses-default"
				return tx.Put(domain.SettingsKind, settingsID, record.Revision, "", "", settings)
			})
			if err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(readExecutionSession(t, s, session).InitialExecution)
			if !bytes.Equal(retained, after) {
				t.Fatal("default edit changed retained execution/configuration digest")
			}
			next, nextInput := f.session(t, domain.DispatchReady)
			if _, err = f.claim(domain.NewID(), next, nextInput); err != nil {
				t.Fatal(err)
			}
			later := readExecutionSession(t, s, next).InitialExecution
			if later.Configuration.NativeModel != "later-responses-default" || later.ConfigurationDigest == frozen.ConfigurationDigest || later.Configuration.HarnessDefaults.SettingsRevision <= settingsRevision {
				t.Fatal("later execution did not freeze its own exact defaults revision")
			}
		})
	}
}
