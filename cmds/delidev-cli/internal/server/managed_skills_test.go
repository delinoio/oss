// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedSkillReferencesAndOldClientPresenceFence(t *testing.T) {
	f := newFirstDispatchFixture(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	id := domain.NewID()
	pkg := domain.ManagedSkillPackage{PackageID: id, WorkerDeviceID: f.workerDevice, MachineID: domain.ID(f.machine.Id), Revision: 1, ContentRevision: strings.Repeat("a", 64), LegacyRevision: strings.Repeat("b", 64), Name: "fixture", Description: "fixture", Enabled: true}
	_, e := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.managed-skill", nil, func(tx *store.Tx) (any, error) {
		if e := tx.PutManagedSkill(pkg); e != nil {
			return nil, e
		}
		row, e := tx.Get(domain.AgentKind, domain.ID(f.agent.Id))
		if e != nil {
			return nil, e
		}
		agent, e := store.Decode[domain.Agent](row)
		if e != nil {
			return nil, e
		}
		agent.ManagedSkills = &domain.ManagedSkillSelectionList{Selections: []domain.ManagedSkillSelection{{PackageID: id, WorkerDeviceID: f.workerDevice, ContentRevision: pkg.ContentRevision}}}
		return tx.Put(domain.AgentKind, row.ID, row.Revision, "", "", agent)
	})
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.managed-fences", nil, func(tx *store.Tx) (any, error) {
		row, e := tx.Get(domain.AgentKind, domain.ID(f.agent.Id))
		if e != nil {
			return nil, e
		}
		agent, e := store.Decode[domain.Agent](row)
		if e != nil {
			return nil, e
		}
		refs, e := tx.ManagedSkillReferences(id)
		if e != nil || len(refs) != 1 || refs[0] != row.ID {
			t.Fatal(refs, e)
		}
		agent.ManagedSkills = nil
		if e = preserveManagedAgent(tx, ConfigurationMutation{ExpectedRevision: row.Revision}, row.ID, &agent); e != nil || agent.ManagedSkills == nil {
			t.Fatal("old client erased selections", e)
		}
		agent.ManagedSkills = &domain.ManagedSkillSelectionList{Selections: []domain.ManagedSkillSelection{}}
		if e = preserveManagedAgent(tx, ConfigurationMutation{ExpectedRevision: row.Revision}, row.ID, &agent); e != nil || len(agent.ManagedSkills.Selections) != 0 {
			t.Fatal("explicit clear rejected", e)
		}
		pkg.Deleting = true
		if e = tx.PutManagedSkill(pkg); e != nil {
			return nil, e
		}
		if e = tx.ValidateManagedSkillSelections(&domain.ManagedSkillSelectionList{Selections: []domain.ManagedSkillSelection{{PackageID: id, WorkerDeviceID: f.workerDevice, ContentRevision: pkg.ContentRevision}}}); e == nil {
			t.Fatal("concurrent Agent selected deletion reservation")
		}
		return nil, nil
	})
	if e != nil {
		t.Fatal(e)
	}
}

func TestManagedSkillOfflineOriginalReceiptsDoNotRelaunchWork(t *testing.T) {
	f := newFirstDispatchFixture(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	request := domain.ManagedSkillRequest{Action: domain.ManagedSkillImport, ServerID: f.service.Identity.ServerID, ActorID: f.service.Identity.ServerID, MachineID: domain.ID(f.machine.Id), WorkerDeviceID: f.workerDevice, WorkerInstanceID: domain.ID(f.workerInstance), RequestID: domain.NewID(), SourceDirectory: "/original-worker/package"}
	request.PackageID = request.RequestID
	source := request.SourceDirectory
	request.SourceDirectory = ""
	hash := sha256.Sum256([]byte(source))
	result := domain.ManagedSkillPackage{PackageID: request.PackageID, MachineID: request.MachineID, WorkerDeviceID: request.WorkerDeviceID, Revision: 1, ContentRevision: strings.Repeat("a", 64), LegacyRevision: strings.Repeat("b", 64), Name: "fixture", Description: "fixture", Enabled: true}
	_, e := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.managed-receipt", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.PutManagedSkillOperation(store.ManagedSkillOperation{Request: request, SourceDigest: hex.EncodeToString(hash[:]), Result: result, Finished: true})
	})
	if e != nil {
		t.Fatal(e)
	}
	retry := request
	retry.ServerID = ""
	retry.ActorID = ""
	retry.WorkerDeviceID = ""
	retry.WorkerInstanceID = ""
	retry.SourceDirectory = source
	observed, replayed, e := f.service.mutateManagedSkill(ctx, retry)
	if e != nil || !replayed || observed.PackageID != result.PackageID || observed.Revision != result.Revision || observed.ContentRevision != result.ContentRevision {
		t.Fatal("offline original receipt was not stable", observed, replayed, e)
	}
	retry.SourceDirectory = "/replacement"
	if _, _, e = f.service.mutateManagedSkill(ctx, retry); e == nil {
		t.Fatal("offline changed source replay accepted")
	}
	foreign := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: domain.NewID()})
	retry.SourceDirectory = source
	if _, _, e = f.service.mutateManagedSkill(foreign, retry); e == nil {
		t.Fatal("foreign actor recovered another original receipt")
	}
}

func TestManagedSkillJoinedImportPinsOriginalWorkerAndReceipt(t *testing.T) {
	f, ctx, stream := skillReaderFixture(t)
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.managed-capability", nil, func(tx *store.Tx) (any, error) {
		row, machine, e := activeMachine(tx, domain.ID(f.machine.Id))
		if e != nil {
			return nil, e
		}
		machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.ManagedSkillsV1)
		return tx.Put(domain.MachineKind, row.ID, row.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if err = os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: fixture\ndescription: fixture\n---\nOriginal marker\n"), 0600); err != nil {
		t.Fatal(err)
	}
	client := delidevv1connect.NewSkillServiceClient(http.DefaultClient, f.endpoint.URL)
	request := &pb.ImportManagedSkillRequest{RequestId: string(domain.NewID()), MachineId: f.machine.Id, SourceDirectory: source}
	done := make(chan error, 1)
	go func() {
		response, e := client.ImportManagedSkill(ctx, ownerRequest(f.identity, request))
		if e == nil && (response.Msg.Package == nil || response.Msg.Package.Selection.PackageId != request.RequestId) {
			e = domain.Fail(domain.Internal, "invalid fixture receipt", "")
		}
		done <- e
	}()
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	var delivery workspace.ReadRequest
	if err = domain.Decode(stream.Msg().RequestJson, &delivery); err != nil || delivery.ManagedSkills == nil {
		t.Fatal(delivery, err)
	}
	original := delivery.ManagedSkills
	if original.ActorID != f.service.Identity.ServerID || original.ServerID != f.service.Identity.ServerID || original.MachineID != domain.ID(f.machine.Id) || original.WorkerDeviceID != f.workerDevice || original.WorkerInstanceID != domain.ID(f.workerInstance) || original.SourceDirectory != source {
		t.Fatal("joined import changed original authority", original)
	}
	manager := skills.Manager{Root: t.TempDir()}
	result, e := manager.Managed(ctx, *original)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(result)
	if _, e = f.workerClient.ReportWorkspaceRead(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkspaceReadRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance, ReadId: string(delivery.ID), DocumentJson: raw})); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	stream.Close()
	os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("source changed"), 0600)
	replay, e := client.ImportManagedSkill(ctx, ownerRequest(f.identity, request))
	if e != nil || !replay.Msg.Replayed || replay.Msg.Package.Selection.ContentRevision != result.Packages[0].ContentRevision {
		t.Fatal("original receipt required new source or Worker observation", replay, e)
	}
}
