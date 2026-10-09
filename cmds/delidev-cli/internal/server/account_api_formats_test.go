// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func saveAPIFormatConfiguration(f *oauthFixture, kind domain.Kind, value any, original *pb.Resource) (*pb.Resource, error) {
	raw, _ := json.Marshal(value)
	mutation := &pb.Mutation{RequestId: string(domain.NewID())}
	if original != nil {
		mutation.Id, mutation.ExpectedRevision = original.Id, original.Revision
	}
	r, err := f.s.SaveConfiguration(f.ctx, connect.NewRequest(&pb.SaveConfigurationRequest{Mutation: mutation, Kind: rpc.WireKind(kind), SchemaVersion: rpc.ResourceSchemaVersion(kind, raw), DocumentJson: raw}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	return r.Msg.Resource, nil
}

func TestAccountReferencedProfilesAndCredentialClassAreImmutable(t *testing.T) {
	f := newOAuthFixture(t)
	profiles := []domain.ProviderAPIFormat{
		{Protocol: domain.OpenAIResponses, Endpoint: "https://api.example.test/responses/v1", Authentication: domain.BearerAuth},
		{Protocol: domain.OpenAIChat, Endpoint: "http://127.0.0.1:1234/v1", Authentication: domain.KeylessAuth},
	}
	p := domain.Provider{Name: "Custom", Protocol: profiles[0].Protocol, Endpoint: profiles[0].Endpoint, Authentication: profiles[0].Authentication, APIFormats: profiles}
	provider, err := saveAPIFormatConfiguration(f, domain.ProviderKind, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []domain.APIProtocol{"", domain.OpenAIResponses} {
		a, err := saveAPIFormatConfiguration(f, domain.AccountKind, domain.Account{Alias: "Fixture", Type: domain.APIAccount, ProviderID: domain.ID(provider.Id), APIProtocol: protocol, Health: domain.AccountDisconnected}, nil)
		if err != nil {
			t.Fatal(err)
		}
		changed := accountBody(t, a)
		changed.APIProtocol = domain.OpenAIChat
		if _, err := saveAPIFormatConfiguration(f, domain.AccountKind, changed, a); domain.SafeError(err).Code != domain.Conflict {
			t.Fatal("an existing account changed its credential class", err)
		}
	}
	for _, remove := range []bool{false, true} {
		changed := p
		changed.APIFormats = append([]domain.ProviderAPIFormat(nil), profiles...)
		if remove {
			changed.APIFormats = changed.APIFormats[1:]
		} else {
			changed.APIFormats[0].Endpoint = "https://changed.example.test/v1"
		}
		if _, err := saveAPIFormatConfiguration(f, domain.ProviderKind, changed, provider); domain.SafeError(err).Code != domain.Conflict {
			t.Fatal("disconnected account lost its referenced profile", err)
		}
	}
	p.APIFormats = append(p.APIFormats, domain.ProviderAPIFormat{Protocol: domain.AnthropicMessages, Endpoint: "https://api.example.test/messages/v1", Authentication: domain.APIKeyAuth})
	if _, err := saveAPIFormatConfiguration(f, domain.ProviderKind, p, provider); err != nil {
		t.Fatal("an unreferenced profile could not be added", err)
	}
}

// These HTTP/Worker fixtures exercise admission and relay behavior, not a real
// OpenRouter account or an installed native harness.
func TestSelectedAPIFormatControlsExecutionRelay(t *testing.T) {
	for _, protocol := range []domain.APIProtocol{domain.OpenAIResponses, domain.OpenAIChat, domain.AnthropicMessages} {
		t.Run(string(protocol), func(t *testing.T) {
			harness, path, authentication := domain.Codex, "/responses", domain.BearerAuth
			if protocol == domain.OpenAIChat {
				harness, path = domain.OpenCode, "/chat/completions"
			}
			if protocol == domain.AnthropicMessages {
				harness, path, authentication = domain.ClaudeCode, "/messages", domain.APIKeyAuth
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/selected/v1"+path {
					t.Error("relay used the default profile URL", r.URL.Path)
				}
				if authentication == domain.APIKeyAuth {
					if r.Header.Get("x-api-key") != "temporary-upstream-fixture-key" || r.Header.Get("Authorization") != "" {
						t.Error("wrong Messages authentication")
					}
				} else if r.Header.Get("Authorization") != "Bearer temporary-upstream-fixture-key" || r.Header.Get("x-api-key") != "" {
					t.Error("wrong OpenAI authentication")
				}
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), `"tools"`) {
					t.Error("tool declaration was lost")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				stream := "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"tool-call\",\"call_id\":\"fixture-call\",\"name\":\"fixture\",\"arguments\":\"{}\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"fixture-response\",\"error\":null}}\n\n"
				if protocol == domain.OpenAIChat {
					stream = "data: {\"id\":\"fixture-response\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"tool-call\",\"type\":\"function\",\"function\":{\"name\":\"fixture\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
				}
				if protocol == domain.AnthropicMessages {
					stream = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture-response\"}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-call\",\"name\":\"fixture\",\"input\":{}}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				}
				io.WriteString(w, stream)
				w.(http.Flusher).Flush()
			}))
			defer upstream.Close()
			f := newProfileAuthorityFixture(t, upstream.URL+"/default/v1", harness, protocol, nil, false)
			profile := domain.ProviderAPIFormat{Protocol: protocol, Endpoint: upstream.URL + "/selected/v1", Authentication: authentication}
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.api-format", nil, func(tx *store.Tx) (any, error) {
				ar, err := tx.Get(domain.AccountKind, f.input.AccountID)
				if err != nil {
					return nil, err
				}
				a, err := store.Decode[domain.Account](ar)
				if err != nil {
					return nil, err
				}
				pr, err := tx.Get(domain.ProviderKind, a.ProviderID)
				if err != nil {
					return nil, err
				}
				p, err := store.Decode[domain.Provider](pr)
				if err != nil {
					return nil, err
				}
				p.Protocol = domain.OpenAIChat
				p.APIFormats = []domain.ProviderAPIFormat{profile}
				if _, err := tx.Put(pr.Kind, pr.ID, pr.Revision, "", "", p); err != nil {
					return nil, err
				}
				a.APIProtocol, a.Connection.Authentication, a.Connection.APIFormat = protocol, authentication, &profile
				return tx.Put(ar.Kind, ar.ID, ar.Revision, "", "", a)
			})
			if err != nil {
				t.Fatal(err)
			}
			f.registerGrant(t)
			body := `{"model":"fixture-model","stream":true,"tools":[{"type":"function","name":"fixture","parameters":{"type":"object"}}]}`
			if protocol == domain.OpenAIChat {
				body = `{"model":"fixture-model","messages":[{"role":"user","content":"fixture"}],"stream":true,"tools":[{"type":"function","function":{"name":"fixture","parameters":{"type":"object"}}}]}`
			}
			if protocol == domain.AnthropicMessages {
				body = `{"model":"fixture-model","max_tokens":32,"messages":[{"role":"user","content":"fixture"}],"stream":true,"tools":[{"name":"fixture","input_schema":{"type":"object"}}]}`
			}
			response := f.requestPath(t, f.token, path, body)
			result, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(result), "tool-call") {
				t.Fatal("selected relay failed", response.StatusCode, err)
			}
			toolField := map[domain.APIProtocol]string{domain.OpenAIResponses: "function_call", domain.OpenAIChat: "tool_calls", domain.AnthropicMessages: "tool_use"}[protocol]
			if !strings.Contains(string(result), toolField) {
				t.Fatal("relay lost the selected format's streaming tool call")
			}
			other := "/responses"
			if other == path {
				other = "/chat/completions"
			}
			if response := f.requestPath(t, f.token, other, body); response.StatusCode == http.StatusOK {
				t.Fatal("relay converted an unsupported operation")
			}
		})
	}
}

func TestPortableConfigurationFormatsVersionFour(t *testing.T) {
	for _, version := range []uint32{1, 2, 3, 4} {
		selection := transferSelection()
		selection.Bundle.Version = version
		// Preserve the historical provider tuple for old bundles while using a
		// format that was already compatible with this fixture's Codex Worker.
		for i := range selection.Bundle.Entries {
			e := &selection.Bundle.Entries[i]
			if e.Kind == domain.ProviderKind {
				var p domain.Provider
				json.Unmarshal(e.Document, &p)
				p.Protocol = domain.OpenAIResponses
				if version == 4 {
					p.APIFormats = []domain.ProviderAPIFormat{p.LegacyAPIFormat()}
				}
				e.Document, _ = json.Marshal(p)
			}
			if e.Kind == domain.AccountKind && version == 4 {
				var a domain.Account
				json.Unmarshal(e.Document, &a)
				a.APIProtocol = domain.OpenAIResponses
				e.Document, _ = json.Marshal(a)
			}
		}
		s, _ := newDoctorFixture(t)
		var plan domain.ConfigurationImportPlan
		err := s.Store.Read(context.Background(), func(tx *store.Tx) error { var err error; plan, err = buildConfigurationPlan(tx, selection); return err })
		if err != nil || plan.Version != domain.ConfigurationBundleVersion {
			t.Fatal("portable bundle compatibility failed", version, err)
		}
		transferApply(t, s, transferPreview(t, s, selection), domain.NewID())
		exported, err := s.ExportConfiguration(transferOwner(), connect.NewRequest(&pb.ExportConfigurationRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		var bundle domain.ConfigurationBundle
		if err := domain.Decode(exported.Msg.DocumentJson, &bundle); err != nil || bundle.Version != domain.ConfigurationBundleVersion {
			t.Fatal("round-trip export did not use version four", err)
		}
		target, _ := newDoctorFixture(t)
		reimport := domain.ConfigurationImportSelection{Bundle: bundle}
		presets, err := target.Store.List(context.Background(), store.Filter{Kind: domain.ProviderKind, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range bundle.Entries {
			if entry.Kind != domain.ProviderKind {
				continue
			}
			var incoming domain.Provider
			if err := domain.Decode(entry.Document, &incoming); err != nil {
				t.Fatal(err)
			}
			if incoming.PresetID == nil {
				continue
			}
			for _, preset := range presets {
				current, decodeErr := store.Decode[domain.Provider](preset)
				if decodeErr != nil {
					t.Fatal(decodeErr)
				}
				if current.PresetID != nil && *current.PresetID == *incoming.PresetID {
					reimport.Bindings = append(reimport.Bindings, domain.ConfigurationBinding{SourceID: entry.ID, TargetID: preset.ID, ExpectedRevision: preset.Revision, Action: domain.ConfigurationReuse})
				}
			}
		}
		result := transferApply(t, target, transferPreview(t, target, reimport), domain.NewID())
		for _, imported := range result.Resources {
			if imported.Kind != domain.AccountKind {
				continue
			}
			row, getErr := target.Store.Get(context.Background(), domain.AccountKind, imported.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			account, decodeErr := store.Decode[domain.Account](row)
			if decodeErr != nil || account.Health != domain.AccountDisconnected || account.Connection != nil || (version == 4) != (account.APIProtocol == domain.OpenAIResponses) {
				t.Fatal("round-trip account lost its original format or gained connection authority", decodeErr)
			}
		}
		if version == 4 {
			selection.Bundle.Version = 3
			if err := s.Store.Read(context.Background(), func(tx *store.Tx) error { _, err := buildConfigurationPlan(tx, selection); return err }); err == nil {
				t.Fatal("old portable version carried new profiles")
			}
		}
	}
}

func TestOpenRouterAccountFormatsAndCodexConfiguration(t *testing.T) {
	f := newOAuthFixture(t)
	var accounts []*pb.Resource
	for _, protocol := range []domain.APIProtocol{domain.OpenAIResponses, domain.OpenAIChat} {
		a, err := saveAPIFormatConfiguration(f, domain.AccountKind, domain.Account{Alias: string(protocol), Type: domain.APIAccount, ProviderID: domain.ID(f.provider.Id), APIProtocol: protocol, Enabled: true, Health: domain.AccountDisconnected}, nil)
		if err != nil || a.SchemaVersion != 3 {
			t.Fatal("explicit account format was not retained", err)
		}
		r, err := f.s.ConnectAccount(f.ctx, connect.NewRequest(&pb.ConnectAccountRequest{Mutation: acctMutation(a, domain.NewID()), ApiKey: []byte("fixture-router-key")}))
		if err != nil {
			t.Fatal(err)
		}
		body := accountBody(t, r.Msg.Account)
		if body.APIProtocol != protocol || body.Connection.APIFormat == nil || body.Connection.APIFormat.Protocol != protocol || body.Connection.APIFormat.Endpoint != "https://openrouter.ai/api/v1" {
			t.Fatal("connection format was not pinned")
		}
		if err := f.s.Store.Read(f.ctx, func(tx *store.Tx) error {
			_, provider, err := inspectionPreflight(tx, disconnectAccountInput{ID: domain.ID(a.Id), Revision: r.Msg.Account.Revision}, validationInspection)
			if err == nil && provider.Protocol != protocol {
				t.Error("validation used the provider default")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, r.Msg.Account)
	}
	for i, a := range accounts {
		agent := domain.Agent{Name: "Codex", Harness: domain.Codex, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly}, Routes: []domain.AgentSourceRoute{{Model: &domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: domain.ID(f.provider.Id), NativeID: "provider/model"}, MetadataSource: domain.UserDeclared}, Accounts: []domain.WeightedAccount{{ID: domain.ID(a.Id), Weight: 1}}}}}
		_, err := saveAPIFormatConfiguration(f, domain.AgentKind, agent, nil)
		if i == 0 && err != nil || i == 1 && domain.SafeError(err).Code != domain.Unsupported {
			t.Fatal("Codex ignored the selected account format", err)
		}
		request := wizardRequest([]*pb.Resource{a}, "provider/model")
		_, err = f.s.SaveAgentWorker(f.ctx, connect.NewRequest(request))
		if i == 0 && err != nil || i == 1 && domain.SafeError(rpc.ClientError(err)).Code != domain.Unsupported {
			t.Fatal("atomic Worker save ignored the selected account format", err)
		}
	}
	listed, err := f.s.ListResources(f.ctx, connect.NewRequest(&pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 1}, ProviderId: f.provider.Id, ApiProtocol: pb.ApiProtocol_API_PROTOCOL_OPENAI_RESPONSES}))
	if err != nil || len(listed.Msg.Resources) != 1 || listed.Msg.Resources[0].Id != accounts[0].Id || listed.Msg.NextPageToken != "" {
		t.Fatal("format filter did not apply before pagination", err)
	}
}

func TestAccountFormatChangeRequiresOriginalCleanup(t *testing.T) {
	f := newOAuthFixture(t)
	a, err := saveAPIFormatConfiguration(f, domain.AccountKind, domain.Account{Alias: "Fixture", Type: domain.APIAccount, ProviderID: domain.ID(f.provider.Id), APIProtocol: domain.OpenAIChat, Enabled: true, Health: domain.AccountDisconnected}, nil)
	if err != nil {
		t.Fatal(err)
	}
	connected, err := f.s.ConnectAccount(f.ctx, connect.NewRequest(&pb.ConnectAccountRequest{Mutation: acctMutation(a, domain.NewID()), ApiKey: []byte("fixture-key")}))
	if err != nil {
		t.Fatal(err)
	}
	a = connected.Msg.Account
	change := accountBody(t, a)
	change.APIProtocol = domain.OpenAIResponses
	if _, err = saveAPIFormatConfiguration(f, domain.AccountKind, change, a); err == nil {
		t.Fatal("connected format changed")
	}
	f.vault.deleteError = domain.Fail(domain.Unavailable, "Fixture cleanup failure.", "")
	input := &pb.DisconnectAccountRequest{Mutation: acctMutation(a, domain.NewID())}
	disconnected, err := f.s.DisconnectAccount(f.ctx, connect.NewRequest(input))
	if err != nil {
		t.Fatal(err)
	}
	a = disconnected.Msg.Account
	change = accountBody(t, a)
	change.APIProtocol = domain.OpenAIResponses
	if _, err = saveAPIFormatConfiguration(f, domain.AccountKind, change, a); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("pending cleanup allowed format change", err)
	}
	f.vault.deleteError = nil
	cleaned, err := f.s.DisconnectAccount(f.ctx, connect.NewRequest(input))
	if err != nil || accountBody(t, cleaned.Msg.Account).Removal != nil {
		t.Fatal("original cleanup did not complete", err)
	}
	a = cleaned.Msg.Account
	change = accountBody(t, a)
	change.APIProtocol = domain.OpenAIResponses
	changed, err := saveAPIFormatConfiguration(f, domain.AccountKind, change, a)
	if err != nil || accountBody(t, changed).APIProtocol != domain.OpenAIResponses {
		t.Fatal("disconnected format change failed", err)
	}
	if _, err = saveAPIFormatConfiguration(f, domain.AccountKind, change, a); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("stale revision accepted", err)
	}
	legacy := change
	legacy.APIProtocol = ""
	if _, err = saveAPIFormatConfiguration(f, domain.AccountKind, legacy, changed); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("old client cleared explicit format", err)
	}
}

func TestAccountFormatChangeRejectsUnsettledNativeCredentials(t *testing.T) {
	f := newOAuthFixture(t)
	a, err := saveAPIFormatConfiguration(f, domain.AccountKind, domain.Account{Alias: "Fixture", Type: domain.APIAccount, ProviderID: domain.ID(f.provider.Id), APIProtocol: domain.OpenAIChat, Health: domain.AccountDisconnected}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Model a native Put that committed before the original Connect failed to
	// publish SQL state. This disconnected account still owns its original intent.
	f.vault.values[credentials.Ref{Owner: domain.ID(a.Id), ID: domain.NewID(), Purpose: credentials.AccountAPI}] = []byte("fixture-unsettled-key")
	change := accountBody(t, a)
	change.APIProtocol = domain.OpenAIResponses
	if _, err := saveAPIFormatConfiguration(f, domain.AccountKind, change, a); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("unsettled native key allowed format change", err)
	}
	f.vault.referenceError = domain.Fail(domain.Unavailable, "Fixture vault unavailable.", "")
	if _, err := saveAPIFormatConfiguration(f, domain.AccountKind, change, a); domain.SafeError(err).Code != domain.Unavailable {
		t.Fatal("failed native enumeration became cleanup proof", err)
	}
	f.vault.referenceError = nil
	r, err := f.s.DisconnectAccount(f.ctx, connect.NewRequest(&pb.DisconnectAccountRequest{Mutation: acctMutation(a, domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	a = r.Msg.Account
	change = accountBody(t, a)
	change.APIProtocol = domain.OpenAIResponses
	changed, err := saveAPIFormatConfiguration(f, domain.AccountKind, change, a)
	if err != nil {
		t.Fatal("confirmed cleanup did not allow format change", err)
	}
	if strings.Contains(string(changed.DocumentJson), "fixture-unsettled-key") || strings.Contains(f.logs.String(), "fixture-unsettled-key") {
		t.Fatal("secret escaped protected storage")
	}
}

func TestKeylessFormatChangePreservesCleanupProof(t *testing.T) {
	f := newOAuthFixture(t)
	profiles := []domain.ProviderAPIFormat{{Protocol: domain.OpenAIChat, Endpoint: "http://127.0.0.1:1234/v1", Authentication: domain.KeylessAuth}, {Protocol: domain.OpenAIResponses, Endpoint: "http://127.0.0.1:1234/v1", Authentication: domain.KeylessAuth}}
	p, err := saveAPIFormatConfiguration(f, domain.ProviderKind, domain.Provider{Name: "Keyless", Protocol: profiles[0].Protocol, Endpoint: profiles[0].Endpoint, Authentication: domain.KeylessAuth, APIFormats: profiles}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := saveAPIFormatConfiguration(f, domain.AccountKind, domain.Account{Alias: "Keyless", Type: domain.APIAccount, ProviderID: domain.ID(p.Id), APIProtocol: domain.OpenAIChat, Health: domain.AccountDisconnected}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.vault.referenceError = domain.Fail(domain.Unavailable, "The vault must not be opened.", "")
	change := accountBody(t, a)
	change.APIProtocol = domain.OpenAIResponses
	if _, err := saveAPIFormatConfiguration(f, domain.AccountKind, change, a); err != nil {
		t.Fatal("keyless proof required vault access", err)
	}
	if f.vault.enumerations != 0 {
		t.Fatal("keyless change enumerated native credentials")
	}
}
