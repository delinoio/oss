// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"testing"
)

func nativeTestEntry(name, value string) domain.NativeConfigurationEntry {
	e := domain.NativeConfigurationEntry{Source: domain.NativeConfigurationProject, Path: ".claude/settings.json", Name: name, Kind: domain.NativeConfigurationSetting, Precedence: 2, Supported: true, Value: json.RawMessage(value), Activation: domain.NativeConfigurationDisabled}
	e.ID = domain.NativeConfigurationEntryID(e)
	e.Digest = domain.NativeConfigurationEntryDigest(e)
	return e
}
func nativeTestReport(t *testing.T, f *firstDispatchFixture, ctx context.Context, stream *connect.ServerStreamForClient[pb.WatchWorkspaceReadsResponse], snapshot domain.NativeConfigurationSnapshot) {
	t.Helper()
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	var read workspace.ReadRequest
	if e := domain.Decode(stream.Msg().RequestJson, &read); e != nil || read.ClaudeConfiguration == nil || read.ClaudeConfiguration.WorkerDeviceID != f.workerDevice || read.ClaudeConfiguration.WorkerInstanceID != domain.ID(f.workerInstance) || read.ClaudeConfiguration.ProjectRoot != "" {
		t.Fatal("wrong native original scope", e, read)
	}
	raw, _ := json.Marshal(snapshot)
	if _, e := f.workerClient.ReportWorkspaceRead(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkspaceReadRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance, ReadId: string(read.ID), DocumentJson: raw})); e != nil {
		t.Fatal(e)
	}
}
func TestClaudeConfigurationAtomicApplyOriginalWorkerAndDrift(t *testing.T) {
	f, ctx, stream := skillReaderFixture(t)
	_, e := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.native-capability", nil, func(tx *store.Tx) (any, error) {
		row, m, e := activeMachine(tx, domain.ID(f.machine.Id))
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.ClaudeConfigurationImportV1)
		return tx.Put(domain.MachineKind, row.ID, row.Revision, "", "", m)
	})
	if e != nil {
		t.Fatal(e)
	}
	setting := nativeTestEntry("model", `"fixture"`)
	setting.Source = domain.NativeConfigurationUser
	setting.Precedence = 1
	setting.ID = domain.NativeConfigurationEntryID(setting)
	setting.Digest = domain.NativeConfigurationEntryDigest(setting)
	rule := domain.NativeConfigurationEntry{Source: domain.NativeConfigurationProject, Path: ".claude/rules/rule.md", Name: "rule.md", Kind: domain.NativeConfigurationInstruction, Precedence: 2, Supported: true, Files: []domain.NativeConfigurationFile{{Path: "rule.md", Contents: "Exact rule.\n"}}, Activation: domain.NativeConfigurationDisabled}
	rule.Source = domain.NativeConfigurationUser
	rule.Precedence = 1
	rule.ID = domain.NativeConfigurationEntryID(rule)
	rule.Digest = domain.NativeConfigurationEntryDigest(rule)
	snapshot := domain.NativeConfigurationSnapshot{Digest: domain.NativeConfigurationDigest("original-source"), Entries: []domain.NativeConfigurationEntry{setting, rule}}
	selection := domain.NativeConfigurationSelection{MachineID: domain.ID(f.machine.Id), IncludeUser: true, Name: "Reviewed configuration", Entries: []string{setting.ID, rule.ID}}
	previewCall := func(selection domain.NativeConfigurationSelection) domain.NativeConfigurationPreview {
		raw, _ := json.Marshal(selection)
		done := make(chan *connect.Response[pb.PreviewClaudeConfigurationImportResponse], 1)
		errs := make(chan error, 1)
		go func() {
			r, e := f.service.PreviewClaudeConfigurationImport(transferOwner(), connect.NewRequest(&pb.PreviewClaudeConfigurationImportRequest{SelectionJson: raw}))
			done <- r
			errs <- e
		}()
		nativeTestReport(t, f, ctx, stream, snapshot)
		r := <-done
		if e := <-errs; e != nil {
			t.Fatal(e)
		}
		var preview domain.NativeConfigurationPreview
		if e := domain.Decode(r.Msg.PreviewJson, &preview); e != nil {
			t.Fatal(e)
		}
		return preview
	}
	preview := previewCall(selection)
	raw, _ := json.Marshal(preview)
	request := &pb.ApplyClaudeConfigurationImportRequest{RequestId: string(domain.NewID()), PreviewJson: raw}
	done := make(chan error, 1)
	go func() {
		_, e := f.service.ApplyClaudeConfigurationImport(transferOwner(), connect.NewRequest(request))
		done <- e
	}()
	nativeTestReport(t, f, ctx, stream, snapshot)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	replay, e := f.service.ApplyClaudeConfigurationImport(transferOwner(), connect.NewRequest(request))
	if e != nil || !replay.Msg.Replayed {
		t.Fatal("exact retry reread or reapplied", e)
	}
	var original store.Record
	f.service.Store.Read(ctx, func(tx *store.Tx) error {
		var e error
		original, e = tx.Get(domain.ImportedNativeConfigurationKind, preview.Selection.TargetID)
		return e
	})
	selection.TargetID = original.ID
	selection.ExpectedRevision = original.Revision
	selection.Entries = []string{setting.ID}
	preview = previewCall(selection)
	raw, _ = json.Marshal(preview)
	request = &pb.ApplyClaudeConfigurationImportRequest{RequestId: string(domain.NewID()), PreviewJson: raw}
	changed := snapshot
	changed.Digest = domain.NativeConfigurationDigest("edited-source")
	go func() {
		_, e := f.service.ApplyClaudeConfigurationImport(transferOwner(), connect.NewRequest(request))
		done <- e
	}()
	nativeTestReport(t, f, ctx, stream, changed)
	if e := <-done; connect.CodeOf(e) != connect.CodeAborted {
		t.Fatal("source drift accepted", e)
	}
	// Race a destination revision while the exact source observation is pending.
	go func() {
		_, e := f.service.ApplyClaudeConfigurationImport(transferOwner(), connect.NewRequest(request))
		done <- e
	}()
	_, e = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.native-edit", nil, func(tx *store.Tx) (any, error) {
		value, e := store.Decode[domain.ImportedNativeConfiguration](original)
		if e != nil {
			return nil, e
		}
		value.Name = "Concurrent edit"
		return tx.Put(original.Kind, original.ID, original.Revision, "", "", value)
	})
	if e != nil {
		t.Fatal(e)
	}
	nativeTestReport(t, f, ctx, stream, snapshot)
	if e := <-done; connect.CodeOf(e) != connect.CodeAborted {
		t.Fatal("destination race accepted", e)
	}
	if e = f.service.Store.Read(ctx, func(tx *store.Tx) error {
		row, e := tx.Get(original.Kind, original.ID)
		if e != nil {
			return e
		}
		value, e := store.Decode[domain.ImportedNativeConfiguration](row)
		if e != nil {
			return e
		}
		if value.Name != "Concurrent edit" || len(value.Entries) != 2 {
			return domain.Fail(domain.Internal, "partial import", "")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestClaudeConfigurationMergePreservesUnselectedAndDisablesPackages(t *testing.T) {
	setting := nativeTestEntry("model", `"new"`)
	old := nativeTestEntry("model", `"old"`)
	preserved := nativeTestEntry("effortLevel", `"high"`)
	pkg := domain.NativeConfigurationEntry{Source: domain.NativeConfigurationProject, Path: ".claude/plugins/example", Name: "example", Kind: domain.NativeConfigurationPlugin, Precedence: 2, Supported: true, Files: []domain.NativeConfigurationFile{{Path: "plugin.json", Contents: `{"name":"example"}`}}, Activation: domain.NativeConfigurationDisabled}
	pkg.ID = domain.NativeConfigurationEntryID(pkg)
	pkg.Digest = domain.NativeConfigurationEntryDigest(pkg)
	preview := domain.NativeConfigurationPreview{Selection: domain.NativeConfigurationSelection{MachineID: domain.NewID(), Name: "Import", Entries: []string{setting.ID, pkg.ID}}, Snapshot: domain.NativeConfigurationSnapshot{Digest: domain.NativeConfigurationDigest("source"), Entries: []domain.NativeConfigurationEntry{setting, pkg}}}
	result, e := nativeConfigurationMerge(domain.ImportedNativeConfiguration{Entries: []domain.NativeConfigurationEntry{old, preserved}}, preview)
	if e != nil {
		t.Fatal(e)
	}
	if len(result.Entries) != 3 {
		t.Fatal("lost unselected entries")
	}
	for _, entry := range result.Entries {
		if entry.Activation != domain.NativeConfigurationDisabled {
			t.Fatal("implicitly activated native extension")
		}
		if entry.ID == preserved.ID && entry.Digest != preserved.Digest {
			t.Fatal("modified unselected entry")
		}
		if entry.ID == setting.ID && string(entry.Value) != `"new"` {
			t.Fatal("lost selected setting")
		}
	}
	preview.Selection.Entries = append(preview.Selection.Entries, domain.NativeConfigurationDigest("unknown"))
	if _, e = nativeConfigurationMerge(result, preview); e == nil {
		t.Fatal("accepted unobserved entry")
	}
}
func TestClaudeConfigurationRejectsUnnegotiatedAndWorkerActors(t *testing.T) {
	f := newFirstDispatchFixture(t)
	selection := domain.NativeConfigurationSelection{MachineID: domain.ID(f.machine.Id), IncludeUser: true, Name: "Import"}
	raw, _ := json.Marshal(selection)
	req := connect.NewRequest(&pb.PreviewClaudeConfigurationImportRequest{SelectionJson: raw})
	if _, e := f.service.PreviewClaudeConfigurationImport(transferOwner(), req); connect.CodeOf(e) != connect.CodeUnimplemented {
		t.Fatal("unnegotiated read", e)
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice})
	if _, e := f.service.PreviewClaudeConfigurationImport(ctx, req); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("Worker acquired import authority", e)
	}
}
