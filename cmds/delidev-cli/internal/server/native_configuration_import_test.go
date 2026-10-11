// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type nativeImportServerFixture struct {
	t                                 *testing.T
	s                                 *Service
	machine, device, instance, active domain.ID
	selection                         domain.NativeConfigurationPreviewSelection
	observation                       domain.NativeConfigurationObservation
	before                            store.Record
}

func newNativeImportServerFixture(t *testing.T, kind domain.NativeConfigurationTargetKind) *nativeImportServerFixture {
	t.Helper()
	s, _ := newDoctorFixture(t)
	f := &nativeImportServerFixture{t: t, s: s, machine: domain.NewID(), device: domain.NewID(), instance: domain.NewID(), active: domain.NewID()}
	doctorPut(t, s, domain.MachineKind, f.machine, 0, domain.Machine{Name: "Selected", OS: "linux", Architecture: "amd64", WorkerCapabilities: []domain.WorkerCapability{domain.NativeConfigurationImportV1}})
	doctorPut(t, s, domain.DeviceKind, f.device, 0, domain.Device{Name: "Original Worker", Type: domain.WorkerDevice, MachineID: f.machine})
	_, err := s.Store.Mutate(transferOwner(), domain.NewID(), "fixture.native-worker", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.SetWorkerInstance(f.machine, f.instance, time.Now().UTC())
	})
	if err != nil {
		t.Fatal(err)
	}
	target := domain.NativeConfigurationTarget{Kind: kind, ID: domain.NewID(), Revision: 1, Scope: domain.HarnessDefaultScope{Harness: domain.Codex, Source: domain.HarnessAnySource}}
	switch kind {
	case domain.NativeConfigurationServerDefaults:
		doctorPut(t, s, domain.SettingsKind, target.ID, 0, domain.DefaultSettings())
	case domain.NativeConfigurationProjectDefaults:
		repo := domain.NewID()
		doctorPut(t, s, domain.RepositoryKind, repo, 0, domain.Repository{Name: "Original", RemoteURL: "https://github.com/fixture/repo.git"})
		doctorPut(t, s, domain.ProjectKind, target.ID, 0, domain.Project{Name: "Original", Repositories: []domain.ID{repo}, PrimaryRepository: repo})
	case domain.NativeConfigurationAgentWorker:
		provider, account := domain.NewID(), domain.NewID()
		index := uint32(0)
		target.RouteIndex = &index
		target.Scope = domain.HarnessDefaultScope{Harness: domain.Codex, Source: domain.HarnessAPISource, ProviderID: provider}
		doctorPut(t, s, domain.ProviderKind, provider, 0, domain.Provider{Name: "Original", Endpoint: "http://127.0.0.1:1234/v1", Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth})
		doctorPut(t, s, domain.AccountKind, account, 0, domain.Account{Alias: "Original", ProviderID: provider, APIProtocol: domain.OpenAIResponses, Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected, Quota: []domain.QuotaWindow{}})
		doctorPut(t, s, domain.AgentKind, target.ID, 0, domain.Agent{Name: "Original", Harness: domain.Codex, HarnessSettings: &domain.HarnessSettings{Version: 1}, Options: domain.AgentOptions{Permission: domain.PermissionDefault}, Routes: []domain.AgentSourceRoute{{Model: &domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: provider}, MetadataSource: domain.Unknown}, ModelSelection: &domain.HarnessSetting[string]{Mode: domain.HarnessInherit}, Accounts: []domain.WeightedAccount{{ID: account, Weight: 3}}}}})
	}
	f.selection = domain.NativeConfigurationPreviewSelection{Version: 1, Harness: domain.Codex, MachineID: f.machine, MachineRevision: 1, Scopes: []domain.NativeConfigurationScope{{Kind: domain.NativeConfigurationHome, Path: "/explicit/selected/home"}}, Target: target}
	text := "Original ordered instructions.\n"
	configDigest := domain.HarnessEvidenceDigest([]byte("fixture native config"))
	instructionDigest := domain.HarnessEvidenceDigest([]byte(text))
	f.observation = domain.NativeConfigurationObservation{Version: 1, Sources: []domain.NativeConfigurationSource{{ScopeIndex: 0, File: "config.toml", Present: true, Digest: configDigest}, {ScopeIndex: 0, File: "AGENTS.override.md", Present: false, Digest: domain.HarnessEvidenceDigest(nil)}, {ScopeIndex: 0, File: "AGENTS.md", Present: true, Digest: instructionDigest}}, Entries: []domain.NativeConfigurationEntry{{ID: domain.HarnessEvidenceDigest([]byte("model")), File: "config.toml", Kind: domain.NativeConfigurationSetting, Name: "model", Value: json.RawMessage(`"selected-model"`), SourceDigest: configDigest}, {ID: domain.HarnessEvidenceDigest([]byte("effort")), File: "config.toml", Kind: domain.NativeConfigurationSetting, Name: "effort", Value: json.RawMessage(`"high"`), SourceDigest: configDigest}, {ID: domain.HarnessEvidenceDigest([]byte("instructions")), File: "AGENTS.md", Kind: domain.NativeConfigurationInstruction, Name: "instructions", Contents: text, SourceDigest: instructionDigest}, {ID: domain.HarnessEvidenceDigest([]byte("mcp")), File: "config.toml", Kind: domain.NativeConfigurationInertExtension, Name: "mcp_servers", Contents: `{"execution_authority":false,"credential_authority":false,"trust":false}`, SourceDigest: configDigest}}}
	doctorPut(t, s, domain.JobKind, f.active, 0, domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobSucceeded, MachineID: f.machine, Input: json.RawMessage(`{"original_generation":"unchanged","model":"active-model"}`)})
	f.before, err = s.Store.Get(context.Background(), target.EntityKind(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *nativeImportServerFixture) claim(resource *pb.Resource) *pb.Resource {
	f.t.Helper()
	var row store.Record
	_, err := f.s.Store.Mutate(transferOwner(), domain.NewID(), "fixture.native-claim", resource.Id, func(tx *store.Tx) (any, error) {
		record, err := tx.Get(domain.JobKind, domain.ID(resource.Id))
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](record)
		if err != nil {
			return nil, err
		}
		job.State, job.InstanceID, job.AssignedDeviceID = domain.JobClaimed, f.instance, f.device
		row, err = tx.PutJob(record.ID, record.Revision, "", "", job)
		return row, err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return rpc.Resource(row)
}
func (f *nativeImportServerFixture) report(resource *pb.Resource, observation domain.NativeConfigurationObservation) *pb.Resource {
	f.t.Helper()
	raw, _ := json.Marshal(observation)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: f.device, MachineID: f.machine})
	response, err := f.s.ReportWork(ctx, connect.NewRequest(&pb.ReportWorkRequest{Mutation: &pb.Mutation{Id: resource.Id, ExpectedRevision: resource.Revision, RequestId: string(domain.NewID())}, MachineId: string(f.machine), InstanceId: string(f.instance), OutputJson: raw}))
	if err != nil {
		f.t.Fatal(err)
	}
	return response.Msg.Job
}
func (f *nativeImportServerFixture) preview() *pb.Resource {
	f.t.Helper()
	raw, _ := json.Marshal(f.selection)
	response, err := f.s.PreviewNativeConfigurationImport(transferOwner(), connect.NewRequest(&pb.PreviewNativeConfigurationImportRequest{RequestId: string(domain.NewID()), SelectionJson: raw}))
	if err != nil {
		f.t.Fatal(err)
	}
	result := f.report(f.claim(response.Msg.Job), f.observation)
	var job domain.Job
	if domain.Decode(result.DocumentJson, &job) != nil || job.State != domain.JobSucceeded {
		f.t.Fatal("preview failed", string(result.DocumentJson))
	}
	return result
}
func (f *nativeImportServerFixture) applyRequest(preview *pb.Resource, entries []string, attach bool) *pb.ApplyNativeConfigurationImportRequest {
	f.t.Helper()
	var job domain.Job
	if domain.Decode(preview.DocumentJson, &job) != nil {
		f.t.Fatal("malformed fixture")
	}
	selection := domain.NativeConfigurationApplySelection{Version: 1, PreviewDigest: domain.HarnessEvidenceDigest(job.Output), Entries: entries, AttachInstructions: attach}
	raw, _ := json.Marshal(selection)
	return &pb.ApplyNativeConfigurationImportRequest{RequestId: string(domain.NewID()), PreviewJobId: preview.Id, SelectionJson: raw}
}
func TestNativeImportAllDestinationsSelectExactEntriesAndKeepActiveGeneration(t *testing.T) {
	for _, kind := range []domain.NativeConfigurationTargetKind{domain.NativeConfigurationServerDefaults, domain.NativeConfigurationProjectDefaults, domain.NativeConfigurationAgentWorker} {
		t.Run(string(kind), func(t *testing.T) {
			f := newNativeImportServerFixture(t, kind)
			activeBefore, _ := f.s.Store.Get(context.Background(), domain.JobKind, f.active)
			preview := f.preview()
			request := f.applyRequest(preview, []string{f.observation.Entries[0].ID, f.observation.Entries[2].ID}, kind == domain.NativeConfigurationAgentWorker)
			accepted, err := f.s.ApplyNativeConfigurationImport(transferOwner(), connect.NewRequest(request))
			if err != nil {
				t.Fatal(err)
			}
			result := f.report(f.claim(accepted.Msg.Job), f.observation)
			var job domain.Job
			if domain.Decode(result.DocumentJson, &job) != nil || job.State != domain.JobSucceeded {
				t.Fatal("apply failed", string(result.DocumentJson))
			}
			var resources []nativeConfigurationResource
			if domain.Decode(job.Output, &resources) != nil || len(resources) != 2 {
				t.Fatal("partial import", string(job.Output))
			}
			target, err := f.s.Store.Get(context.Background(), f.selection.Target.EntityKind(), f.selection.Target.ID)
			if err != nil || target.Revision != 2 {
				t.Fatal(err, target)
			}
			switch kind {
			case domain.NativeConfigurationServerDefaults:
				settings, _ := store.Decode[domain.Settings](target)
				if settings.HarnessDefaults.Entries[0].Settings.Model == nil || *settings.HarnessDefaults.Entries[0].Settings.Model.Value != "selected-model" || settings.HarnessDefaults.Entries[0].Settings.Effort != nil {
					t.Fatal("unselected setting imported", settings)
				}
			case domain.NativeConfigurationProjectDefaults:
				project, _ := store.Decode[domain.Project](target)
				if *project.HarnessDefaults.Entries[0].Settings.Model.Value != "selected-model" || project.HarnessDefaults.Entries[0].Settings.Effort != nil {
					t.Fatal(project)
				}
			case domain.NativeConfigurationAgentWorker:
				agent, _ := store.Decode[domain.Agent](target)
				original, _ := store.Decode[domain.Agent](f.before)
				if agent.Routes[0].Model.NativeID != "selected-model" || agent.Routes[0].Accounts[0] != original.Routes[0].Accounts[0] || agent.HarnessSettings.Effort != nil || len(agent.Templates) != 1 {
					t.Fatal("Agent source or exact selection changed", agent)
				}
			}
			pkgRecord, err := f.s.Store.Get(context.Background(), domain.TemplateKind, resources[0].ID)
			pkg, _ := store.Decode[domain.Template](pkgRecord)
			if err != nil || pkg.NativeImport == nil || pkg.Contents != f.observation.Entries[2].Contents || rpc.ResourceSchemaVersion(domain.TemplateKind, pkgRecord.Data) != 2 {
				t.Fatal("immutable instruction proof lost", err, pkg)
			}
			activeAfter, _ := f.s.Store.Get(context.Background(), domain.JobKind, f.active)
			if !bytes.Equal(activeBefore.Data, activeAfter.Data) || activeBefore.Revision != activeAfter.Revision {
				t.Fatal("active generation changed")
			}
			replay, err := f.s.ApplyNativeConfigurationImport(transferOwner(), connect.NewRequest(request))
			if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != accepted.Msg.Job.Id {
				t.Fatal("did not retain original successful receipt", err)
			}
			// A different selection cannot reuse the original mutation identity.
			changed := f.applyRequest(preview, []string{f.observation.Entries[1].ID}, false)
			changed.RequestId = request.RequestId
			if _, err := f.s.ApplyNativeConfigurationImport(transferOwner(), connect.NewRequest(changed)); err == nil {
				t.Fatal("changed selection reused original receipt")
			}
		})
	}
}
func TestNativeImportDriftRevocationAndTargetConflictPublishNothing(t *testing.T) {
	for _, scenario := range []string{"source", "destination", "worker-instance", "worker-device", "client-revoked", "unsupported-selection", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			f := newNativeImportServerFixture(t, domain.NativeConfigurationServerDefaults)
			client := domain.NewID()
			ctx := transferOwner()
			if scenario == "client-revoked" {
				doctorPut(t, f.s, domain.DeviceKind, client, 0, domain.Device{Name: "Original client", Type: domain.ClientDevice})
				ctx = domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: client})
			}
			raw, _ := json.Marshal(f.selection)
			created, err := f.s.PreviewNativeConfigurationImport(ctx, connect.NewRequest(&pb.PreviewNativeConfigurationImportRequest{RequestId: string(domain.NewID()), SelectionJson: raw}))
			if err != nil {
				t.Fatal(err)
			}
			preview := f.report(f.claim(created.Msg.Job), f.observation)
			request := f.applyRequest(preview, []string{f.observation.Entries[0].ID, f.observation.Entries[2].ID}, false)
			if scenario == "unsupported-selection" {
				selection := domain.NativeConfigurationApplySelection{Version: 1, PreviewDigest: domain.HarnessEvidenceDigest([]byte("wrong")), Entries: []string{domain.HarnessEvidenceDigest([]byte("unknown"))}}
				request.SelectionJson, _ = json.Marshal(selection)
				if _, err := f.s.ApplyNativeConfigurationImport(ctx, connect.NewRequest(request)); err == nil {
					t.Fatal("unreviewed selection accepted")
				}
				return
			}
			accepted, err := f.s.ApplyNativeConfigurationImport(ctx, connect.NewRequest(request))
			if err != nil {
				t.Fatal(err)
			}
			claimed := f.claim(accepted.Msg.Job)
			observation := f.observation
			observation.Entries = slices.Clone(f.observation.Entries)
			switch scenario {
			case "source":
				observation.Entries[0].Value = json.RawMessage(`"changed-model"`)
			case "destination":
				doctorPut(t, f.s, domain.SettingsKind, f.selection.Target.ID, 1, domain.DefaultSettings())
			case "worker-instance":
				_, err = f.s.Store.Mutate(transferOwner(), domain.NewID(), "fixture.native-replacement", nil, func(tx *store.Tx) (any, error) {
					return nil, tx.SetWorkerInstance(f.machine, domain.NewID(), time.Now().UTC())
				})
			case "worker-device":
				doctorPut(t, f.s, domain.DeviceKind, f.device, 1, domain.Device{Name: "Revoked Worker", Type: domain.WorkerDevice, MachineID: f.machine, Revoked: true})
			case "client-revoked":
				doctorPut(t, f.s, domain.DeviceKind, client, 1, domain.Device{Name: "Revoked client", Type: domain.ClientDevice, Revoked: true})
			case "canceled":
				_, err = f.s.Store.Mutate(transferOwner(), domain.NewID(), "fixture.native-cancel", nil, func(tx *store.Tx) (any, error) { return nil, tx.RequestJobCancellation(domain.ID(claimed.Id)) })
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "worker-instance" || scenario == "worker-device" {
				var input domain.NativeConfigurationJobInput
				var job domain.Job
				domain.Decode(claimed.DocumentJson, &job)
				domain.Decode(job.Input, &input)
				err = f.s.Store.Read(transferOwner(), func(tx *store.Tx) error { return nativeConfigurationAuthority(tx, input, f.s.Identity.ServerID) })
				if err == nil {
					t.Fatal("replaced/revoked Worker retained authority")
				}
			} else {
				result := f.report(claimed, observation)
				var job domain.Job
				domain.Decode(result.DocumentJson, &job)
				if job.State != domain.JobFailed && job.State != domain.JobCanceled {
					t.Fatal("conflicting report published", job.State)
				}
			}
			records, err := f.s.Store.List(context.Background(), store.Filter{Kind: domain.TemplateKind, Limit: store.MaxPage})
			if err != nil || len(records) != 0 {
				t.Fatal("partial package publication", err, records)
			}
			target, _ := f.s.Store.Get(context.Background(), domain.SettingsKind, f.selection.Target.ID)
			if scenario != "destination" && !bytes.Equal(target.Data, f.before.Data) {
				t.Fatal("failure changed destination")
			}
		})
	}
}
func TestNativeImportPackagesRemainImmutableAndInertThroughPortableTransfer(t *testing.T) {
	f := newNativeImportServerFixture(t, domain.NativeConfigurationAgentWorker)
	preview := f.preview()
	request := f.applyRequest(preview, []string{f.observation.Entries[2].ID, f.observation.Entries[3].ID}, true)
	accepted, err := f.s.ApplyNativeConfigurationImport(transferOwner(), connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	result := f.report(f.claim(accepted.Msg.Job), f.observation)
	var job domain.Job
	domain.Decode(result.DocumentJson, &job)
	if job.State != domain.JobSucceeded {
		t.Fatal(job.Problem)
	}
	var resources []nativeConfigurationResource
	domain.Decode(job.Output, &resources)
	for _, item := range resources[:2] {
		record, err := f.s.Store.Get(context.Background(), domain.TemplateKind, item.ID)
		if err != nil {
			t.Fatal(err)
		}
		pkg, _ := store.Decode[domain.Template](record)
		portable, err := portableDocument(domain.TemplateKind, record.Data)
		if err != nil {
			t.Fatal(err)
		}
		retained, err := portableValue(domain.TemplateKind, portable, true)
		if err != nil || retained.(*domain.Template).NativeImport == nil || retained.(*domain.Template).Contents != pkg.Contents {
			t.Fatal("portable transfer stripped imported package proof", err)
		}
		pkg.NativeImport = nil
		raw, _ := json.Marshal(pkg)
		if _, err := SaveConfiguration(transferOwner(), f.s.Store, ConfigurationMutation{RequestID: domain.NewID(), ID: item.ID, ExpectedRevision: 1, Kind: domain.TemplateKind, Document: raw}); err == nil {
			t.Fatal("old-client save made imported package mutable")
		}
	}
	agentRecord, _ := f.s.Store.Get(context.Background(), domain.AgentKind, f.selection.Target.ID)
	agent, _ := store.Decode[domain.Agent](agentRecord)
	if len(agent.Templates) != 1 || agent.Templates[0] != resources[0].ID {
		t.Fatal("inert extension attached as instructions")
	}
	agent.Templates = append(agent.Templates, resources[1].ID)
	raw, _ := json.Marshal(agent)
	if _, err := SaveConfiguration(transferOwner(), f.s.Store, ConfigurationMutation{RequestID: domain.NewID(), ID: agentRecord.ID, ExpectedRevision: agentRecord.Revision, Kind: domain.AgentKind, Document: raw}); err == nil {
		t.Fatal("direct save granted inert package execution")
	}
}

func TestNativeImportStorageFailureRollsBackAllPackagesAndRetainsExactReport(t *testing.T) {
	f := newNativeImportServerFixture(t, domain.NativeConfigurationServerDefaults)
	preview := f.preview()
	request := f.applyRequest(preview, []string{f.observation.Entries[0].ID, f.observation.Entries[2].ID}, false)
	accepted, err := f.s.ApplyNativeConfigurationImport(transferOwner(), connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	claimed := f.claim(accepted.Msg.Job)
	db, err := sql.Open("sqlite", filepath.Join(f.s.Store.Root(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER native_import_fixture_failure BEFORE UPDATE ON entities WHEN OLD.kind='settings' BEGIN SELECT RAISE(ABORT,'fixture atomic publication failure'); END`); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(f.observation)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: f.device, MachineID: f.machine})
	report := &pb.ReportWorkRequest{Mutation: &pb.Mutation{Id: claimed.Id, ExpectedRevision: claimed.Revision, RequestId: string(domain.NewID())}, MachineId: string(f.machine), InstanceId: string(f.instance), OutputJson: raw}
	if _, err := f.s.ReportWork(ctx, connect.NewRequest(report)); err == nil {
		t.Fatal("storage failure was treated as application")
	}
	packages, err := f.s.Store.List(context.Background(), store.Filter{Kind: domain.TemplateKind, Limit: store.MaxPage})
	if err != nil || len(packages) != 0 {
		t.Fatal("package write survived rolled-back publication", err)
	}
	target, _ := f.s.Store.Get(context.Background(), domain.SettingsKind, f.selection.Target.ID)
	if !bytes.Equal(target.Data, f.before.Data) {
		t.Fatal("storage failure changed destination")
	}
	retained, _ := f.s.Store.Get(context.Background(), domain.JobKind, domain.ID(claimed.Id))
	if retained.Revision != claimed.Revision || !bytes.Equal(retained.Data, claimed.DocumentJson) {
		t.Fatal("rollback lost original pending assignment")
	}
	if _, err := db.Exec(`DROP TRIGGER native_import_fixture_failure`); err != nil {
		t.Fatal(err)
	}
	result, err := f.s.ReportWork(ctx, connect.NewRequest(report))
	if err != nil {
		t.Fatal(err)
	}
	var job domain.Job
	domain.Decode(result.Msg.Job.DocumentJson, &job)
	if job.State != domain.JobSucceeded {
		t.Fatal("original report could not complete after rollback", job.Problem)
	}
	replay, err := f.s.ReportWork(ctx, connect.NewRequest(report))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != claimed.Id {
		t.Fatal("report replay repeated publication", err)
	}
	packages, _ = f.s.Store.List(context.Background(), store.Filter{Kind: domain.TemplateKind, Limit: store.MaxPage})
	if len(packages) != 1 {
		t.Fatal("report replay created another package")
	}
}
func TestNativeImportReplayAfterExpiryRestartAndPackageDeletionDoesNotRecreate(t *testing.T) {
	f := newNativeImportServerFixture(t, domain.NativeConfigurationServerDefaults)
	preview := f.preview()
	request := f.applyRequest(preview, []string{f.observation.Entries[2].ID}, false)
	accepted, err := f.s.ApplyNativeConfigurationImport(transferOwner(), connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	result := f.report(f.claim(accepted.Msg.Job), f.observation)
	var job domain.Job
	domain.Decode(result.DocumentJson, &job)
	var resources []nativeConfigurationResource
	domain.Decode(job.Output, &resources)
	if job.State != domain.JobSucceeded || len(resources) != 1 {
		t.Fatal("package import failed", job.Problem)
	}
	_, err = f.s.Store.Mutate(transferOwner(), domain.NewID(), "fixture.native-expiry-delete", nil, func(tx *store.Tx) (any, error) {
		record, err := tx.Get(domain.JobKind, domain.ID(preview.Id))
		if err != nil {
			return nil, err
		}
		old, err := store.Decode[domain.Job](record)
		if err != nil {
			return nil, err
		}
		old.AcceptedAt = old.AcceptedAt.Add(-48 * time.Hour)
		if _, err := tx.PutJob(record.ID, record.Revision, "", "", old); err != nil {
			return nil, err
		}
		return nil, tx.Delete(domain.TemplateKind, resources[0].ID, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	root := f.s.Store.Root()
	if err := f.s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	f.s.Store = reopened
	replay, err := f.s.ApplyNativeConfigurationImport(transferOwner(), connect.NewRequest(request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != accepted.Msg.Job.Id {
		t.Fatal("expired/restarted receipt lost original outcome", err)
	}
	if _, err := f.s.Store.Get(context.Background(), domain.TemplateKind, resources[0].ID); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("original replay recreated deleted package", err)
	}
	request.RequestId = string(domain.NewID())
	if _, err := f.s.ApplyNativeConfigurationImport(transferOwner(), connect.NewRequest(request)); err == nil {
		t.Fatal("new request reused expired source proof")
	}
}
