// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type nativeConfigurationFixture struct {
	f                       *nativeModelsFixture
	agent, device, instance domain.ID
	worker                  context.Context
	read                    domain.NativeConfigurationRead
	originals               map[string][]byte
}

func newNativeConfigurationFixture(t *testing.T) *nativeConfigurationFixture {
	t.Helper()
	f := newNativeModelsFixture(t)
	n := &nativeConfigurationFixture{f: f, agent: domain.NewID(), device: domain.NewID(), instance: domain.NewID(), originals: map[string][]byte{}}
	n.worker = domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: n.device, MachineID: f.machine})
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	project := filepath.Join(base, "project")
	for path, value := range map[string]string{filepath.Join(home, "config.toml"): "model_reasoning_effort = 'low'\napi_key = 'SECRET-CONFIG'\n", filepath.Join(home, "auth.json"): "SECRET-AUTH", filepath.Join(home, "AGENTS.md"): "Home instructions\n", filepath.Join(project, ".codex", "config.toml"): "model_reasoning_effort = 'high'\n[hooks]\ncommand = 'SECRET-HOOK'\n", filepath.Join(project, "AGENTS.override.md"): "Selected project instructions\n", filepath.Join(project, "AGENTS.md"): "Overridden instructions\n"} {
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		n.originals[path] = []byte(value)
	}
	n.read = domain.NativeConfigurationRead{Scopes: []domain.NativeConfigurationScope{{Kind: domain.NativeConfigurationHome, Path: home}, {Kind: domain.NativeConfigurationProject, Path: project}}}
	_, err = f.service.Store.Mutate(f.ctx, domain.NewID(), "native.fixture", nil, func(tx *store.Tx) (any, error) {
		record, err := tx.Get(domain.MachineKind, f.machine)
		if err != nil {
			return nil, err
		}
		machine, err := store.Decode[domain.Machine](record)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.CodexConfigurationImportV1)
		if _, err = tx.Put(domain.MachineKind, f.machine, record.Revision, "", "", machine); err != nil {
			return nil, err
		}
		if _, err = tx.Put(domain.DeviceKind, n.device, 0, "", "", domain.Device{Name: "Original Worker", Type: domain.WorkerDevice, MachineID: f.machine, PairedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		if err = tx.SetWorkerInstance(f.machine, n.instance, time.Now().UTC()); err != nil {
			return nil, err
		}
		return tx.Put(domain.AgentKind, n.agent, 0, "", "", domain.Agent{Name: "Selected Agent", Harness: domain.Codex, Effort: "medium", Templates: []domain.ID{}, Routes: []domain.AgentSourceRoute{{Model: &domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: f.provider, NativeID: "fixture"}, Name: "Fixture", MetadataSource: domain.UserDeclared}, Accounts: []domain.WeightedAccount{{ID: f.account, Weight: 1}}}}, Options: domain.AgentOptions{Permission: domain.PermissionDefault}})
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func (n *nativeConfigurationFixture) job(t *testing.T, id domain.ID) (store.Record, domain.Job) {
	t.Helper()
	var row store.Record
	var job domain.Job
	err := n.f.service.Store.Read(n.f.ctx, func(tx *store.Tx) error {
		var err error
		row, err = tx.Get(domain.JobKind, id)
		if err != nil {
			return err
		}
		job, err = store.Decode[domain.Job](row)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return row, job
}
func (n *nativeConfigurationFixture) claimAndReport(t *testing.T, id domain.ID, output []byte, problem *domain.Error) {
	t.Helper()
	_, err := n.f.service.Store.Mutate(n.f.ctx, domain.NewID(), "native.claim", id, func(tx *store.Tx) (any, error) {
		row, err := tx.Get(domain.JobKind, id)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](row)
		if err != nil {
			return nil, err
		}
		job.State = domain.JobClaimed
		job.InstanceID = n.instance
		job.AssignedDeviceID = n.device
		return tx.PutJob(id, row.Revision, "", "", job)
	})
	if err != nil {
		t.Fatal(err)
	}
	row, _ := n.job(t, id)
	request := &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(id), ExpectedRevision: row.Revision}, MachineId: string(n.f.machine), InstanceId: string(n.instance), OutputJson: output}
	if problem != nil {
		request.Problem = &pb.ErrorDetail{Code: string(problem.Code), Cause: problem.Message, Guidance: problem.Guidance}
	}
	if _, err = n.f.service.ReportWork(n.worker, connect.NewRequest(request)); err != nil {
		t.Fatal(err)
	}
}
func (n *nativeConfigurationFixture) preview(t *testing.T) domain.NativeConfigurationImportPreview {
	t.Helper()
	raw, _ := json.Marshal(n.read)
	accepted, err := n.f.service.RequestCodexConfigurationPreview(n.f.ctx, connect.NewRequest(&pb.RequestCodexConfigurationPreviewRequest{RequestId: string(domain.NewID()), MachineId: string(n.f.machine), SelectionJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := codex.ReadConfiguration(context.Background(), n.read)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(snapshot)
	if bytes.Contains(raw, []byte("SECRET-")) {
		t.Fatal("secret leaked from fixture")
	}
	n.claimAndReport(t, domain.ID(accepted.Msg.Job.Id), raw, nil)
	row, _ := n.job(t, domain.ID(accepted.Msg.Job.Id))
	entries := []string{}
	for _, entry := range snapshot.Entries {
		if entry.Scope == 1 && (entry.Kind == domain.NativeConfigurationSetting || entry.Kind == domain.NativeConfigurationInstruction) {
			entries = append(entries, entry.Key)
		}
	}
	raw, _ = json.Marshal(domain.NativeConfigurationImportSelection{PreviewJobID: row.ID, ExpectedPreviewRevision: row.Revision, AgentID: n.agent, ExpectedAgentRevision: 1, Entries: entries})
	review, err := n.f.service.PreviewCodexConfigurationImport(n.f.ctx, connect.NewRequest(&pb.PreviewCodexConfigurationImportRequest{SelectionJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	var preview domain.NativeConfigurationImportPreview
	if err = domain.Decode(review.Msg.PreviewJson, &preview); err != nil {
		t.Fatal(err)
	}
	return preview
}
func (n *nativeConfigurationFixture) destination(t *testing.T) (store.Record, domain.Agent, []store.Record) {
	t.Helper()
	var row store.Record
	var agent domain.Agent
	var packages []store.Record
	err := n.f.service.Store.Read(n.f.ctx, func(tx *store.Tx) error {
		var err error
		row, err = tx.Get(domain.AgentKind, n.agent)
		if err != nil {
			return err
		}
		agent, err = store.Decode[domain.Agent](row)
		if err != nil {
			return err
		}
		for _, id := range agent.Templates {
			item, err := tx.Get(domain.TemplateKind, id)
			if err != nil {
				return err
			}
			packages = append(packages, item)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return row, agent, packages
}
func TestNativeConfigurationAtomicExactSelectionAndReceipt(t *testing.T) {
	n := newNativeConfigurationFixture(t)
	preview := n.preview(t)
	if len(preview.Plan.Packages) != 1 || preview.Plan.Agent.Effort != "high" || preview.Plan.Agent.Options.Permission != domain.PermissionDefault {
		t.Fatalf("incorrect plan: %+v", preview.Plan)
	}
	raw, _ := json.Marshal(preview)
	request := connect.NewRequest(&pb.ApplyCodexConfigurationImportRequest{RequestId: string(domain.NewID()), PreviewJson: raw})
	accepted, err := n.f.service.ApplyCodexConfigurationImport(n.f.ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	_, before, packages := n.destination(t)
	if before.Effort != "medium" || len(packages) != 0 {
		t.Fatal("writes occurred before original Worker recheck")
	}
	_, parent := n.job(t, domain.ID(accepted.Msg.Job.Id))
	var pending configurationImportJob
	if err = domain.Decode(parent.Input, &pending); err != nil {
		t.Fatal(err)
	}
	snapshot, err := codex.ReadConfiguration(context.Background(), pending.Native.Plan.Read)
	if err != nil {
		t.Fatal(err)
	}
	output, _ := json.Marshal(snapshot)
	n.claimAndReport(t, pending.Native.ChildID, output, nil)
	row, agent, packages := n.destination(t)
	if row.Revision != 2 || agent.Effort != "high" || len(packages) != 1 {
		t.Fatal("exact setting and package not imported")
	}
	template, err := store.Decode[domain.Template](packages[0])
	if err != nil {
		t.Fatal(err)
	}
	if template.Contents != "Selected project instructions\n" || template.NativeSource == nil {
		t.Fatal("wrong package or missing provenance")
	}
	replay, err := n.f.service.ApplyCodexConfigurationImport(n.f.ctx, request)
	if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != accepted.Msg.Job.Id {
		t.Fatalf("receipt lost: %v", err)
	}
	row, _, packages = n.destination(t)
	if row.Revision != 2 || len(packages) != 1 {
		t.Fatal("replay duplicated import")
	}
	for path, original := range n.originals {
		current, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(original, current) {
			t.Fatal("original host file changed")
		}
	}
	edited, _ := json.Marshal(domain.Template{Name: template.Name, Contents: "Changed"})
	if _, err = SaveConfiguration(n.f.ctx, n.f.service.Store, ConfigurationMutation{RequestID: domain.NewID(), Kind: domain.TemplateKind, ID: packages[0].ID, ExpectedRevision: packages[0].Revision, Document: edited}); err == nil {
		t.Fatal("immutable package edited")
	}
	_, parent = n.job(t, domain.ID(accepted.Msg.Job.Id))
	if parent.State != domain.JobSucceeded || bytes.Contains(parent.Input, []byte("instructions")) {
		t.Fatal("terminal import retained staged instructions")
	}
}
func TestNativeConfigurationChangedSourcesAndAuthorityFailClosed(t *testing.T) {
	for _, scenario := range []string{"source", "destination", "worker", "token"} {
		t.Run(scenario, func(t *testing.T) {
			n := newNativeConfigurationFixture(t)
			preview := n.preview(t)
			if scenario == "destination" {
				row, agent, _ := n.destination(t)
				agent.Name = "Changed destination"
				doctorPut(t, n.f.service, domain.AgentKind, n.agent, row.Revision, agent)
			}
			if scenario == "worker" {
				_, err := n.f.service.Store.Mutate(n.f.ctx, domain.NewID(), "native.replace-worker", nil, func(tx *store.Tx) (any, error) {
					return nil, tx.SetWorkerInstance(n.f.machine, domain.NewID(), time.Now().UTC())
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "token" {
				preview.Plan.Agent.Effort = "low"
			}
			raw, _ := json.Marshal(preview)
			response, err := n.f.service.ApplyCodexConfigurationImport(n.f.ctx, connect.NewRequest(&pb.ApplyCodexConfigurationImportRequest{RequestId: string(domain.NewID()), PreviewJson: raw}))
			if scenario != "source" {
				if err == nil {
					t.Fatal("changed authority accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(n.read.Scopes[1].Path, "AGENTS.override.md")
			if err = os.WriteFile(path, []byte("Changed source\n"), 0600); err != nil {
				t.Fatal(err)
			}
			_, parent := n.job(t, domain.ID(response.Msg.Job.Id))
			var pending configurationImportJob
			domain.Decode(parent.Input, &pending)
			_, sourceErr := codex.ReadConfiguration(context.Background(), pending.Native.Plan.Read)
			if sourceErr == nil {
				t.Fatal("changed digest accepted")
			}
			n.claimAndReport(t, pending.Native.ChildID, nil, domain.SafeError(sourceErr))
			row, agent, packages := n.destination(t)
			if row.Revision != 1 || agent.Effort != "medium" || len(packages) != 0 {
				t.Fatal("failed source recheck wrote destination")
			}
			_, parent = n.job(t, domain.ID(response.Msg.Job.Id))
			if parent.State != domain.JobFailed {
				t.Fatal("source conflict not terminal")
			}
		})
	}
}
