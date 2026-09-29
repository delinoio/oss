package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func transferEntry(kind domain.Kind, value any) domain.ConfigurationEntry {
	raw, _ := json.Marshal(value)
	return domain.ConfigurationEntry{ID: domain.NewID(), Kind: kind, Document: raw}
}
func transferSelection() domain.ConfigurationImportSelection {
	provider := transferEntry(domain.ProviderKind, domain.Provider{Name: "API", Endpoint: "https://api.example.test/v1", Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth})
	model := transferEntry(domain.ModelKind, domain.Model{ProviderID: provider.ID, NativeID: "fixture", Name: "Model", Harnesses: []domain.Harness{domain.Codex}, Manual: true, MetadataSource: domain.UserDeclared})
	account := transferEntry(domain.AccountKind, domain.Account{Alias: "Fresh account", ProviderID: provider.ID, Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected, Quota: []domain.QuotaWindow{}})
	template := transferEntry(domain.TemplateKind, domain.Template{Name: "Exact instructions", Contents: "Keep every line.\n한국어 <script>inert</script>\n"})
	agent := transferEntry(domain.AgentKind, domain.Agent{Name: "Agent", Harness: domain.Codex, ModelID: model.ID, Accounts: []domain.WeightedAccount{{ID: account.ID, Weight: 3}}, Templates: []domain.ID{template.ID}, Options: domain.AgentOptions{Permission: domain.PermissionDefault}})
	return domain.ConfigurationImportSelection{Bundle: domain.ConfigurationBundle{Version: 1, Entries: []domain.ConfigurationEntry{agent, template, account, model, provider}, Machines: []domain.ConfigurationMachine{}}, Bindings: []domain.ConfigurationBinding{}, Machines: []domain.ConfigurationMachineBinding{}, Checkouts: []domain.ConfigurationCheckoutBinding{}}
}
func transferOwner() context.Context {
	return domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
}
func transferPreview(t *testing.T, s *Service, selection domain.ConfigurationImportSelection) []byte {
	t.Helper()
	raw, _ := json.Marshal(selection)
	response, err := s.PreviewConfigurationImport(transferOwner(), connect.NewRequest(&pb.PreviewConfigurationImportRequest{SelectionJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.PreviewJson
}
func transferApply(t *testing.T, s *Service, preview []byte, id domain.ID) domain.ConfigurationImportResult {
	t.Helper()
	response, err := s.ApplyConfigurationImport(transferOwner(), connect.NewRequest(&pb.ApplyConfigurationImportRequest{RequestId: string(id), PreviewJson: preview}))
	if err != nil {
		t.Fatal(err)
	}
	var report domain.ConfigurationImportResult
	if err = domain.Decode(response.Msg.ResultJson, &report); err != nil {
		t.Fatal(err)
	}
	return report
}
func TestConfigurationTransferExportExcludesRuntimeAndKeepsExactInstructions(t *testing.T) {
	s, _ := newDoctorFixture(t)
	selection := transferSelection()
	var accountID domain.ID
	for _, entry := range selection.Bundle.Entries {
		value, err := configurationValue(entry.Kind, entry.Document)
		if err != nil {
			t.Fatal(err)
		}
		if account, ok := value.(*domain.Account); ok {
			accountID = entry.ID
			account.Health = domain.AccountReady
			account.Connection = &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.BearerAuth, ConnectedAt: time.Now().UTC()}
			account.Quota = []domain.QuotaWindow{{ID: "private-observation", State: domain.Observed}}
		}
		if model, ok := value.(*domain.Model); ok {
			model.MetadataSource = domain.Known
			model.Manual = false
			model.New = true
			model.Discovery = &domain.ModelDiscovery{FirstSeenAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		}
		doctorPut(t, s, entry.Kind, entry.ID, 0, value)
	}
	doctorPut(t, s, domain.TemplateKind, domain.NewID(), 0, domain.Template{Name: "Another", Contents: "Exact final newline\n"})
	response, err := s.ExportConfiguration(transferOwner(), connect.NewRequest(&pb.ExportConfigurationRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(response.Msg.DocumentJson, []byte("private-observation")) || bytes.Contains(response.Msg.DocumentJson, []byte("connected_at")) || bytes.Contains(response.Msg.DocumentJson, []byte("first_seen_at")) {
		t.Fatal("export leaked runtime evidence")
	}
	var bundle domain.ConfigurationBundle
	if err = domain.Decode(response.Msg.DocumentJson, &bundle); err != nil {
		t.Fatal(err)
	}
	for _, entry := range bundle.Entries {
		if _, err = portableValue(entry.Kind, entry.Document, true); err != nil {
			t.Fatal(err)
		}
	}
	original, err := s.Store.Get(context.Background(), domain.AccountKind, accountID)
	if err != nil {
		t.Fatal(err)
	}
	account, _ := store.Decode[domain.Account](original)
	if account.Health != domain.AccountReady || account.Connection == nil {
		t.Fatal("export changed source authentication")
	}
	if !bytes.Contains(response.Msg.DocumentJson, []byte(`Exact final newline\n`)) {
		t.Fatal("export omitted instructions")
	}
}
func TestConfigurationImportPreviewReadOnlyAtomicRemappingAndReplay(t *testing.T) {
	s, _ := newDoctorFixture(t)
	selection := transferSelection()
	_, before, err := s.Store.Snapshot(context.Background(), store.Filter{Kind: domain.TemplateKind, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	preview := transferPreview(t, s, selection)
	rows, after, err := s.Store.Snapshot(context.Background(), store.Filter{Kind: domain.TemplateKind, Limit: 10})
	if err != nil || len(rows) != 0 || before != after {
		t.Fatal("preview changed state/events", err)
	}
	id := domain.NewID()
	report := transferApply(t, s, preview, id)
	if report.State != domain.JobSucceeded || len(report.Resources) != 5 {
		t.Fatal(report)
	}
	ids := map[domain.ID]domain.ID{}
	for _, entry := range report.Resources {
		if entry.ID == entry.SourceID {
			t.Fatal("source identity silently reused")
		}
		ids[entry.SourceID] = entry.ID
	}
	for _, entry := range selection.Bundle.Entries {
		row, err := s.Store.Get(context.Background(), entry.Kind, ids[entry.ID])
		if err != nil {
			t.Fatal(err)
		}
		switch entry.Kind {
		case domain.AgentKind:
			agent, _ := store.Decode[domain.Agent](row)
			var original domain.Agent
			domain.Decode(entry.Document, &original)
			if agent.ModelID != ids[original.ModelID] || agent.Accounts[0].ID != ids[original.Accounts[0].ID] || agent.Templates[0] != ids[original.Templates[0]] {
				t.Fatal("graph was not remapped")
			}
		case domain.TemplateKind:
			if !bytes.Equal(row.Data, entry.Document) {
				t.Fatal("instructions changed")
			}
		case domain.AccountKind:
			account, _ := store.Decode[domain.Account](row)
			if account.Health != domain.AccountDisconnected || account.Connection != nil {
				t.Fatal("account connected implicitly")
			}
		}
	}
	replay := transferApply(t, s, preview, id)
	if replay.JobID != report.JobID {
		t.Fatal("replay duplicated import")
	}
	var changed domain.ConfigurationImportPreview
	domain.Decode(preview, &changed)
	changed.Plan.Changes[0].After = []byte(`{}`)
	tampered, _ := json.Marshal(changed)
	if _, err = s.ApplyConfigurationImport(transferOwner(), connect.NewRequest(&pb.ApplyConfigurationImportRequest{RequestId: string(domain.NewID()), PreviewJson: tampered})); err == nil {
		t.Fatal("changed preview accepted")
	}
	if _, err = s.PreviewConfigurationImport(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID()}), connect.NewRequest(&pb.PreviewConfigurationImportRequest{SelectionJson: []byte(`{}`)})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker allowed", err)
	}
}
func TestConfigurationImportSettingsConflictAndStalePreviewPreserveEverything(t *testing.T) {
	s, _ := newDoctorFixture(t)
	settingsID := domain.NewID()
	current := domain.DefaultSettings()
	doctorPut(t, s, domain.SettingsKind, settingsID, 0, current)
	selection := transferSelection()
	modified := current
	modified.AutomaticFetch = false
	entry := transferEntry(domain.SettingsKind, modified)
	selection.Bundle.Entries = append(selection.Bundle.Entries, entry)
	raw, _ := json.Marshal(selection)
	if _, err := s.PreviewConfigurationImport(transferOwner(), connect.NewRequest(&pb.PreviewConfigurationImportRequest{SelectionJson: raw})); err == nil {
		t.Fatal("implicit settings replacement allowed")
	}
	selection.Bindings = []domain.ConfigurationBinding{{SourceID: entry.ID, Action: domain.ConfigurationReplace, TargetID: settingsID, ExpectedRevision: 1}}
	preview := transferPreview(t, s, selection)
	current.Notifications = false
	doctorPut(t, s, domain.SettingsKind, settingsID, 1, current)
	if _, err := s.ApplyConfigurationImport(transferOwner(), connect.NewRequest(&pb.ApplyConfigurationImportRequest{RequestId: string(domain.NewID()), PreviewJson: preview})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale settings accepted", err)
	}
	rows, err := s.Store.List(context.Background(), store.Filter{Kind: domain.TemplateKind, Limit: 10})
	if err != nil || len(rows) != 0 {
		t.Fatal("failed apply left partial configuration", err)
	}
}
func TestConfigurationImportRejectsCredentialsMissingLinksAndConflictingModels(t *testing.T) {
	for _, name := range []string{"observation", "missing-template", "alias", "extra-machine", "unknown-field", "wrong-kind", "oversize"} {
		t.Run(name, func(t *testing.T) {
			s, _ := newDoctorFixture(t)
			selection := transferSelection()
			switch name {
			case "observation":
				var account domain.Account
				domain.Decode(selection.Bundle.Entries[2].Document, &account)
				account.ConfirmedExhausted = true
				selection.Bundle.Entries[2].Document, _ = json.Marshal(account)
			case "missing-template":
				selection.Bundle.Entries = append(selection.Bundle.Entries[:1], selection.Bundle.Entries[2:]...)
			case "alias":
				duplicate := selection.Bundle.Entries[3]
				duplicate.ID = domain.NewID()
				selection.Bundle.Entries = append(selection.Bundle.Entries, duplicate)
			case "extra-machine":
				selection.Bundle.Machines = append(selection.Bundle.Machines, domain.ConfigurationMachine{ID: domain.NewID(), Name: "foreign", OS: "linux", Architecture: "amd64"})
			case "unknown-field":
				selection.Bundle.Entries[1].Document = []byte(`{"name":"n","contents":"text","api_key":"never-import"}`)
			case "wrong-kind":
				selection.Bundle.Entries[1].Kind = domain.SessionKind
			case "oversize":
				selection.Bundle.Entries[1].Document, _ = json.Marshal(domain.Template{Name: "n", Contents: strings.Repeat("x", 128<<10+1)})
			}
			raw, _ := json.Marshal(selection)
			if _, err := s.PreviewConfigurationImport(transferOwner(), connect.NewRequest(&pb.PreviewConfigurationImportRequest{SelectionJson: raw})); err == nil {
				t.Fatal("invalid import preview accepted")
			}
		})
	}
}

func TestPortableProviderActivationFieldsRejectExplicitMalformedValues(t *testing.T) {
	for _, raw := range []string{
		`{"name":"Portable","endpoint":"https://api.example.test/v1","protocol":"openai-chat","authentication":"bearer","discovery":true,"enabled":null}`,
		`{"name":"Portable","endpoint":"https://api.example.test/v1","protocol":"openai-chat","authentication":"bearer","discovery":true,"enabled":"yes"}`,
		`{"name":"Portable","endpoint":"https://api.example.test/v1","protocol":"openai-chat","authentication":"bearer","discovery":true,"preset_id":null}`,
		`{"name":"Portable","endpoint":"https://api.example.test/v1","protocol":"openai-chat","authentication":"bearer","discovery":true,"preset_id":"unknown"}`,
	} {
		if _, err := portableValue(domain.ProviderKind, []byte(raw), true); err == nil {
			t.Fatalf("accepted malformed provider fields: %s", raw)
		}
	}
	legacy := []byte(`{"name":"Legacy","endpoint":"http://127.0.0.1:11434/v1","protocol":"openai-chat","authentication":"keyless","discovery":true}`)
	value, err := portableValue(domain.ProviderKind, legacy, true)
	if err != nil || !value.(*domain.Provider).EnabledValue() || value.(*domain.Provider).PresetID != nil {
		t.Fatal("legacy provider semantics changed", err)
	}
}

func TestConfigurationImportRejectsManagedPresetCollisionsAtPreviewAndApply(t *testing.T) {
	managed := providers.Presets()[6].Provider // Ollama remains virtual until explicitly saved.
	single := func() domain.ConfigurationImportSelection {
		provider := transferEntry(domain.ProviderKind, managed)
		return domain.ConfigurationImportSelection{Bundle: domain.ConfigurationBundle{Version: 1, Entries: []domain.ConfigurationEntry{provider}, Machines: []domain.ConfigurationMachine{}}, Bindings: []domain.ConfigurationBinding{}, Machines: []domain.ConfigurationMachineBinding{}, Checkouts: []domain.ConfigurationCheckoutBinding{}}
	}
	t.Run("duplicate bundle", func(t *testing.T) {
		s, _ := newDoctorFixture(t)
		selection := single()
		duplicate := selection.Bundle.Entries[0]
		duplicate.ID = domain.NewID()
		selection.Bundle.Entries = append(selection.Bundle.Entries, duplicate)
		raw, _ := json.Marshal(selection)
		if _, err := s.PreviewConfigurationImport(transferOwner(), connect.NewRequest(&pb.PreviewConfigurationImportRequest{SelectionJson: raw})); err == nil {
			t.Fatal("duplicate managed presets in one bundle were accepted")
		}
	})
	t.Run("target and deferred apply recheck", func(t *testing.T) {
		s, _ := newDoctorFixture(t)
		selection := single()
		preview := transferPreview(t, s, selection)
		doctorPut(t, s, domain.ProviderKind, domain.NewID(), 0, managed)
		_, err := s.ApplyConfigurationImport(transferOwner(), connect.NewRequest(&pb.ApplyConfigurationImportRequest{RequestId: string(domain.NewID()), PreviewJson: preview}))
		if err == nil {
			t.Fatal("deferred apply did not recheck the managed preset collision")
		}
		rows, listErr := s.Store.List(context.Background(), store.Filter{Kind: domain.ProviderKind, Limit: 10})
		if listErr != nil || len(rows) != 7 {
			t.Fatal("failed deferred apply changed provider state", listErr, len(rows))
		}
	})
}

func TestConfigurationImportRepositoryValidationCommitsAllOrNothing(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "canonical-path", "stale-settings", "revoked-client", "retired-identity"} {
		t.Run(outcome, func(t *testing.T) {
			s, _ := newDoctorFixture(t)
			selection := transferSelection()
			sources := []domain.ID{domain.NewID(), domain.NewID()}
			targets := []domain.ID{domain.NewID(), domain.NewID()}
			repository := domain.Repository{Name: "Both checkouts", AutoFetch: true}
			for i, source := range sources {
				doctorPut(t, s, domain.MachineKind, targets[i], 0, domain.Machine{Name: "target", OS: "linux", Architecture: "amd64"})
				selection.Bundle.Machines = append(selection.Bundle.Machines, domain.ConfigurationMachine{ID: source, Name: "source", OS: "linux", Architecture: "amd64"})
				selection.Machines = append(selection.Machines, domain.ConfigurationMachineBinding{SourceID: source, TargetID: targets[i]})
				repository.Checkouts = append(repository.Checkouts, domain.Checkout{MachineID: source, Path: "/untrusted/old"})
			}
			repo := transferEntry(domain.RepositoryKind, repository)
			selection.Bundle.Entries = append(selection.Bundle.Entries, repo)
			for _, source := range sources {
				selection.Checkouts = append(selection.Checkouts, domain.ConfigurationCheckoutBinding{RepositoryID: repo.ID, MachineID: source, Path: "/target/checkout"})
			}
			settingsID := domain.NewID()
			settings := domain.DefaultSettings()
			doctorPut(t, s, domain.SettingsKind, settingsID, 0, settings)
			settingEntry := transferEntry(domain.SettingsKind, settings)
			selection.Bundle.Entries = append(selection.Bundle.Entries, settingEntry)
			selection.Bindings = append(selection.Bindings, domain.ConfigurationBinding{SourceID: settingEntry.ID, Action: domain.ConfigurationReplace, TargetID: settingsID, ExpectedRevision: 1})
			preview := transferPreview(t, s, selection)
			if outcome == "revoked-client" {
				clientID := domain.NewID()
				actor := domain.Principal{Type: domain.ClientDevice, DeviceID: clientID}
				doctorPut(t, s, domain.DeviceKind, clientID, 0, domain.Device{Name: "client", Type: domain.ClientDevice})
				var document domain.ConfigurationImportPreview
				domain.Decode(preview, &document)
				token, err := s.Identity.EncodeCursor(security.Cursor{Scope: configurationPlanScope(actor, document.Plan)})
				if err != nil {
					t.Fatal(err)
				}
				document.Token = token
				preview, _ = json.Marshal(document)
				response, err := s.ApplyConfigurationImport(domain.WithPrincipal(context.Background(), actor), connect.NewRequest(&pb.ApplyConfigurationImportRequest{RequestId: string(domain.NewID()), PreviewJson: preview}))
				if err != nil {
					t.Fatal(err)
				}
				var report domain.ConfigurationImportResult
				domain.Decode(response.Msg.ResultJson, &report)
				doctorPut(t, s, domain.DeviceKind, clientID, 1, domain.Device{Name: "client", Type: domain.ClientDevice, Revoked: true})
				finishTransferTest(t, s, report.JobID, outcome, settingsID, settings)
			} else {
				report := transferApply(t, s, preview, domain.NewID())
				if report.State != domain.JobQueued {
					t.Fatal(report)
				}
				finishTransferTest(t, s, report.JobID, outcome, settingsID, settings)
			}
		})
	}
}
func finishTransferTest(t *testing.T, s *Service, parentID domain.ID, outcome string, settingsID domain.ID, settings domain.Settings) {
	t.Helper()
	var children []store.Record
	if err := s.Store.Read(context.Background(), func(tx *store.Tx) error { var err error; children, err = tx.Jobs("", parentID, "", "", 10); return err }); err != nil || len(children) != 2 {
		t.Fatal("children", err)
	}
	if outcome == "retired-identity" {
		row, err := s.Store.Get(context.Background(), domain.JobKind, parentID)
		if err != nil {
			t.Fatal(err)
		}
		job, _ := store.Decode[domain.Job](row)
		var pending configurationImportJob
		if err = domain.Decode(job.Input, &pending); err != nil {
			t.Fatal(err)
		}
		for _, change := range pending.Plan.Changes {
			if change.Kind == domain.TemplateKind {
				doctorPut(t, s, domain.TemplateKind, change.ID, 0, domain.Template{Name: "Conflicting future target", Contents: "retired"})
				_, err = s.Store.Mutate(context.Background(), domain.NewID(), "fixture.retire", nil, func(tx *store.Tx) (any, error) { return nil, tx.Delete(domain.TemplateKind, change.ID, 1) })
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if outcome == "stale-settings" {
		settings.AutomaticFetch = false
		doctorPut(t, s, domain.SettingsKind, settingsID, 1, settings)
	}
	for index, row := range children {
		_, err := s.Store.Mutate(context.Background(), domain.NewID(), "import.fixture.finish", nil, func(tx *store.Tx) (any, error) {
			job, err := store.Decode[domain.Job](row)
			if err != nil {
				return nil, err
			}
			job.State = domain.JobSucceeded
			root := "/target/checkout"
			if outcome == "canonical-path" {
				root = "/different/canonical"
			}
			job.Output, _ = json.Marshal(workspace.Inspection{Root: root, Name: "repository"})
			if outcome == "failure" && index == 1 {
				job.State = domain.JobFailed
				job.Problem = domain.Fail(domain.InvalidArgument, "fixture rejection", "Retry explicitly.")
			}
			if _, err := tx.PutJob(row.ID, row.Revision, "", "", job); err != nil {
				return nil, err
			}
			return nil, finishRepositorySave(tx, parentID)
		})
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			rows, _ := s.Store.List(context.Background(), store.Filter{Kind: domain.TemplateKind, Limit: 10})
			if len(rows) != 0 {
				t.Fatal("partial import before all inspections")
			}
		}
	}
	row, err := s.Store.Get(context.Background(), domain.JobKind, parentID)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := store.Decode[domain.Job](row)
	rows, err := s.Store.List(context.Background(), store.Filter{Kind: domain.TemplateKind, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if outcome == "success" {
		if job.State != domain.JobSucceeded || len(rows) != 1 {
			t.Fatal("not applied", job.State)
		}
	} else if job.State != domain.JobFailed || len(rows) != 0 {
		t.Fatal("partial or unresolved failure", outcome, job.State)
	}
}
func TestConfigurationTransferRealConnectRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	endpoint, identity, stop, done := runTestServer(t, root)
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	client := delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, endpoint.URL)
	selection := transferSelection()
	raw, _ := json.Marshal(selection)
	preview, err := client.PreviewConfigurationImport(context.Background(), ownerRequest(identity, &pb.PreviewConfigurationImportRequest{SelectionJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	request := &pb.ApplyConfigurationImportRequest{RequestId: string(domain.NewID()), PreviewJson: preview.Msg.PreviewJson}
	first, err := client.ApplyConfigurationImport(context.Background(), ownerRequest(identity, request))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := client.ApplyConfigurationImport(context.Background(), ownerRequest(identity, request))
	if err != nil || !replay.Msg.Replayed || !bytes.Equal(first.Msg.ResultJson, replay.Msg.ResultJson) {
		t.Fatal("wire replay", err)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	endpoint, identity, stop, done = runTestServer(t, root)
	client = delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, endpoint.URL)
	replay, err = client.ApplyConfigurationImport(context.Background(), ownerRequest(identity, request))
	if err != nil || !replay.Msg.Replayed || !bytes.Equal(first.Msg.ResultJson, replay.Msg.ResultJson) {
		t.Fatal("restart changed original import", err)
	}
	exported, err := client.ExportConfiguration(context.Background(), ownerRequest(identity, &pb.ExportConfigurationRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var bundle domain.ConfigurationBundle
	if err = domain.Decode(exported.Msg.DocumentJson, &bundle); err != nil || len(bundle.Entries) != 11 {
		t.Fatal("wire export", err)
	}
}

func TestConfigurationImportExplicitReuseAndDeletedReceiptCannotRecreate(t *testing.T) {
	s, _ := newDoctorFixture(t)
	selection := transferSelection()
	first := transferApply(t, s, transferPreview(t, s, selection), domain.NewID())
	mapping := map[domain.ID]domain.ConfigurationImportedResource{}
	for _, entry := range first.Resources {
		mapping[entry.SourceID] = entry
	}
	for _, entry := range selection.Bundle.Entries {
		if entry.Kind == domain.ProviderKind || entry.Kind == domain.ModelKind || entry.Kind == domain.TemplateKind {
			existing := mapping[entry.ID]
			selection.Bindings = append(selection.Bindings, domain.ConfigurationBinding{SourceID: entry.ID, Action: domain.ConfigurationReuse, TargetID: existing.ID, ExpectedRevision: existing.Revision})
		}
	}
	second := transferApply(t, s, transferPreview(t, s, selection), domain.NewID())
	for _, entry := range second.Resources {
		if entry.Kind == domain.ProviderKind || entry.Kind == domain.ModelKind || entry.Kind == domain.TemplateKind {
			if entry.ID != mapping[entry.SourceID].ID || entry.Revision != 1 || entry.Action != domain.ConfigurationReuse {
				t.Fatal("reused entry changed")
			}
		} else if entry.ID == mapping[entry.SourceID].ID {
			t.Fatal("account/Agent was implicitly reused")
		}
	}
	// A single newly created instruction can be removed without dependency links.
	single := domain.ConfigurationImportSelection{Bundle: domain.ConfigurationBundle{Version: 1, Entries: []domain.ConfigurationEntry{transferEntry(domain.TemplateKind, domain.Template{Name: "Disposable", Contents: "no retained copy"})}}}
	preview := transferPreview(t, s, single)
	request := domain.NewID()
	created := transferApply(t, s, preview, request)
	_, err := s.Store.Mutate(context.Background(), domain.NewID(), "fixture.delete", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.Delete(domain.TemplateKind, created.Resources[0].ID, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ApplyConfigurationImport(transferOwner(), connect.NewRequest(&pb.ApplyConfigurationImportRequest{RequestId: string(request), PreviewJson: preview}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("deleted import receipt was not preserved", err)
	}
	_, err = s.ApplyConfigurationImport(transferOwner(), connect.NewRequest(&pb.ApplyConfigurationImportRequest{RequestId: string(domain.NewID()), PreviewJson: preview}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("deleted identity reached publication again", err)
	}
	row, err := s.Store.Get(context.Background(), domain.JobKind, created.JobID)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := store.Decode[domain.Job](row)
	if bytes.Contains(job.Input, []byte("no retained copy")) {
		t.Fatal("terminal job retained instructions")
	}
}
