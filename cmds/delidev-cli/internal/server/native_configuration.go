// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func nativeConfigurationScope(tx *store.Tx, selection domain.NativeConfigurationSelection) (domain.NativeConfigurationReadScope, error) {
	scope := domain.NativeConfigurationReadScope{MachineID: selection.MachineID, ProjectID: selection.ProjectID, IncludeUser: selection.IncludeUser}
	if err := tx.Authorize(); err != nil {
		return scope, err
	}
	_, machine, err := activeMachine(tx, selection.MachineID)
	if err != nil {
		return scope, err
	}
	if !slices.Contains(machine.WorkerCapabilities, domain.ClaudeConfigurationImportV1) {
		return scope, domain.Fail(domain.Unsupported, "Runner cannot preview Claude configuration.", "Update the original Runner.")
	}
	if selection.ProjectID != "" {
		row, e := tx.Get(domain.ProjectKind, selection.ProjectID)
		if e != nil {
			return scope, e
		}
		project, e := store.Decode[domain.Project](row)
		if e != nil {
			return scope, e
		}
		scope.ProjectRevision = row.Revision
		scope.RepositoryID = project.PrimaryRepository
		repositoryRow, e := tx.Get(domain.RepositoryKind, scope.RepositoryID)
		if e != nil {
			return scope, e
		}
		repository, e := store.Decode[domain.Repository](repositoryRow)
		if e != nil {
			return scope, e
		}
		scope.RepositoryRevision = repositoryRow.Revision
		for _, checkout := range repository.Checkouts {
			if checkout.MachineID == selection.MachineID {
				scope.ProjectRoot = checkout.Path
				break
			}
		}
		if scope.ProjectRoot == "" {
			return scope, domain.Fail(domain.MissingInput, "Project has no checkout on the selected Runner.", "Save its original checkout before previewing configuration.")
		}
	}
	return scope, nil
}
func (s *Service) observeClaudeConfiguration(ctx context.Context, selection domain.NativeConfigurationSelection) (domain.NativeConfigurationReadScope, domain.NativeConfigurationSnapshot, error) {
	var scope domain.NativeConfigurationReadScope
	var snapshot domain.NativeConfigurationSnapshot
	err := s.Store.Read(ctx, func(tx *store.Tx) error { var e error; scope, e = nativeConfigurationScope(tx, selection); return e })
	if err != nil {
		return scope, snapshot, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	s.workspaceReadsMu.Lock()
	reader := s.workspaceReaders[scope.MachineID]
	active := 0
	for _, current := range s.workspaceReaders {
		if current.pending != nil {
			active++
		}
	}
	if reader == nil || reader.pending != nil || active >= 64 {
		s.workspaceReadsMu.Unlock()
		return scope, snapshot, workspaceReadUnavailable()
	}
	scope.WorkerDeviceID = reader.device
	scope.WorkerInstanceID = reader.instance
	observation := &workspaceObservation{request: workspace.ReadRequest{ID: domain.NewID(), Deadline: deadline.UTC(), Preparation: workspace.PrepareRequest{MachineID: scope.MachineID}, ClaudeConfiguration: &scope}, result: make(chan workspaceReadReply, 1)}
	reader.pending = observation
	s.workspaceReadsMu.Unlock()
	defer func() {
		s.workspaceReadsMu.Lock()
		if reader.pending == observation {
			reader.pending = nil
		}
		s.workspaceReadsMu.Unlock()
	}()
	primary, e := s.primaryWorkspaceStream(reader.machine, reader.instance)
	if e != nil || primary.ID != reader.primary {
		return scope, snapshot, workspaceReadUnavailable()
	}
	check := func(tx *store.Tx) error {
		if e := currentWorkspaceReader(tx, reader); e != nil {
			return e
		}
		fresh, e := nativeConfigurationScope(tx, selection)
		if e != nil {
			return e
		}
		fresh.WorkerDeviceID = scope.WorkerDeviceID
		fresh.WorkerInstanceID = scope.WorkerInstanceID
		if !reflect.DeepEqual(fresh, scope) {
			return transferConflict()
		}
		return nil
	}
	raw, _ := json.Marshal(observation.request)
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if e := check(tx); e != nil {
			return e
		}
		select {
		case reader.requests <- raw:
			return nil
		default:
			return workspaceReadUnavailable()
		}
	})
	if err != nil {
		return scope, snapshot, err
	}
	select {
	case <-ctx.Done():
		return scope, snapshot, workspaceReadUnavailable()
	case <-reader.done:
		return scope, snapshot, workspaceReadUnavailable()
	case <-primary.Done:
		return scope, snapshot, workspaceReadUnavailable()
	case reply := <-observation.result:
		if reply.problem != nil {
			return scope, snapshot, reply.problem
		}
		if err = s.Store.Read(ctx, check); err != nil {
			return scope, snapshot, err
		}
		current, e := s.primaryWorkspaceStream(reader.machine, reader.instance)
		if e != nil || current.ID != reader.primary {
			return scope, snapshot, workspaceReadUnavailable()
		}
		if domain.Decode(reply.document, &snapshot) != nil || snapshot.Validate() != nil {
			return scope, snapshot, workspaceReadUnavailable()
		}
		return scope, snapshot, nil
	}
}
func nativeConfigurationTokenScope(actor domain.Principal, preview domain.NativeConfigurationPreview) string {
	preview.Token = ""
	return "claude-configuration:" + domain.NativeConfigurationDigest([]any{actor, preview})
}
func nativeConfigurationDestination(tx *store.Tx, selection domain.NativeConfigurationSelection) (domain.ImportedNativeConfiguration, error) {
	if selection.ExpectedRevision == 0 {
		return domain.ImportedNativeConfiguration{}, tx.RequireUnusedID(selection.TargetID)
	}
	row, e := tx.Get(domain.ImportedNativeConfigurationKind, selection.TargetID)
	if e != nil {
		return domain.ImportedNativeConfiguration{}, e
	}
	if row.Revision != selection.ExpectedRevision {
		return domain.ImportedNativeConfiguration{}, transferConflict()
	}
	value, e := store.Decode[domain.ImportedNativeConfiguration](row)
	if e != nil {
		return value, e
	}
	if value.MachineID != selection.MachineID || value.ProjectID != selection.ProjectID {
		return value, transferConflict()
	}
	return value, nil
}
func nativeConfigurationMerge(existing domain.ImportedNativeConfiguration, preview domain.NativeConfigurationPreview) (domain.ImportedNativeConfiguration, error) {
	selected := map[string]bool{}
	for _, id := range preview.Selection.Entries {
		selected[id] = true
	}
	if len(selected) == 0 {
		return existing, transferInvalid()
	}
	merged := map[string]domain.NativeConfigurationEntry{}
	for _, entry := range existing.Entries {
		merged[entry.ID] = entry
	}
	for _, entry := range preview.Snapshot.Entries {
		if selected[entry.ID] {
			if !entry.Supported {
				return existing, transferInvalid()
			}
			merged[entry.ID] = entry
			delete(selected, entry.ID)
		}
	}
	if len(selected) != 0 {
		return existing, transferInvalid()
	}
	result := domain.ImportedNativeConfiguration{Version: 1, Name: preview.Selection.Name, MachineID: preview.Selection.MachineID, ProjectID: preview.Selection.ProjectID, SourceDigest: preview.Snapshot.Digest, Entries: []domain.NativeConfigurationEntry{}}
	for _, entry := range merged {
		result.Entries = append(result.Entries, entry)
	}
	slices.SortStableFunc(result.Entries, func(a, b domain.NativeConfigurationEntry) int {
		if a.Precedence < b.Precedence {
			return -1
		}
		if a.Precedence > b.Precedence {
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return result, result.Validate()
}
func (s *Service) PreviewClaudeConfigurationImport(ctx context.Context, req *connect.Request[pb.PreviewClaudeConfigurationImportRequest]) (*connect.Response[pb.PreviewClaudeConfigurationImportResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := configurationActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var selection domain.NativeConfigurationSelection
	if len(req.Msg.SelectionJson) > 32<<10 || domain.Decode(req.Msg.SelectionJson, &selection) != nil || selection.Validate() != nil {
		return nil, rpc.Error(transferInvalid(), correlation)
	}
	if selection.TargetID == "" {
		selection.TargetID = domain.NewID()
	}
	var existing domain.ImportedNativeConfiguration
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if e := tx.Authorize(); e != nil {
			return e
		}
		var e error
		existing, e = nativeConfigurationDestination(tx, selection)
		return e
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	scope, snapshot, err := s.observeClaudeConfiguration(ctx, selection)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	preview := domain.NativeConfigurationPreview{Version: 1, Selection: selection, Scope: scope, Snapshot: snapshot}
	// Empty selection is an inventory preview; apply always requires explicit IDs.
	if len(selection.Entries) > 0 {
		if _, err = nativeConfigurationMerge(existing, preview); err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	preview.Token, err = s.Identity.EncodeCursor(security.Cursor{Scope: nativeConfigurationTokenScope(actor, preview)})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	raw, _ := json.Marshal(preview)
	response := connect.NewResponse(&pb.PreviewClaudeConfigurationImportResponse{PreviewJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) ApplyClaudeConfigurationImport(ctx context.Context, req *connect.Request[pb.ApplyClaudeConfigurationImportRequest]) (*connect.Response[pb.ApplyClaudeConfigurationImportResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := configurationActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var preview domain.NativeConfigurationPreview
	if len(req.Msg.PreviewJson) > domain.MaxNativeConfigurationBytes+32<<10 || domain.Decode(req.Msg.PreviewJson, &preview) != nil || preview.Version != 1 || preview.Selection.Validate() != nil || preview.Selection.TargetID.Validate() != nil || preview.Snapshot.Validate() != nil {
		return nil, rpc.Error(transferInvalid(), correlation)
	}
	input := struct {
		Actor   domain.Principal
		Preview domain.NativeConfigurationPreview
	}{actor, preview}
	id := domain.ID(req.Msg.RequestId)
	result, found, err := s.Store.Replay(ctx, id, "claude-configuration.import", input)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if !found {
		if _, err = s.Identity.DecodeCursor(preview.Token, nativeConfigurationTokenScope(actor, preview)); err != nil {
			return nil, rpc.Error(err, correlation)
		}
		scope, snapshot, e := s.observeClaudeConfiguration(ctx, preview.Selection)
		if e != nil {
			return nil, rpc.Error(e, correlation)
		}
		if !reflect.DeepEqual(scope, preview.Scope) || !reflect.DeepEqual(snapshot, preview.Snapshot) {
			return nil, rpc.Error(transferConflict(), correlation)
		}
		result, err = s.Store.Mutate(ctx, id, "claude-configuration.import", input, func(tx *store.Tx) (any, error) {
			if e := authorizeConfigurationImport(tx, actor); e != nil {
				return nil, e
			}
			fresh, e := nativeConfigurationScope(tx, preview.Selection)
			if e != nil {
				return nil, e
			}
			fresh.WorkerDeviceID = scope.WorkerDeviceID
			fresh.WorkerInstanceID = scope.WorkerInstanceID
			if !reflect.DeepEqual(fresh, scope) {
				return nil, transferConflict()
			}
			// The original live Worker must still own the auxiliary read lease at commit.
			s.workspaceReadsMu.Lock()
			reader := s.workspaceReaders[scope.MachineID]
			s.workspaceReadsMu.Unlock()
			if reader == nil || reader.device != scope.WorkerDeviceID || reader.instance != scope.WorkerInstanceID {
				return nil, workspaceReadUnavailable()
			}
			primary, e := s.primaryWorkspaceStream(reader.machine, reader.instance)
			if e != nil || primary.ID != reader.primary {
				return nil, workspaceReadUnavailable()
			}
			if e = currentWorkspaceReader(tx, reader); e != nil {
				return nil, e
			}
			existing, e := nativeConfigurationDestination(tx, preview.Selection)
			if e != nil {
				return nil, e
			}
			value, e := nativeConfigurationMerge(existing, preview)
			if e != nil {
				return nil, e
			}
			row, e := tx.Put(domain.ImportedNativeConfigurationKind, preview.Selection.TargetID, preview.Selection.ExpectedRevision, "", preview.Selection.ProjectID, value)
			if e != nil {
				return nil, e
			}
			return rpc.Resource(row), nil
		})
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	var imported pb.Resource
	if err = json.Unmarshal(result.Data, &imported); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.ApplyClaudeConfigurationImportResponse{Imported: &imported, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
