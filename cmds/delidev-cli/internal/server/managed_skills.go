// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func managedSkillFailure() error {
	return domain.Fail(domain.Unavailable, "Skill management is unavailable on this Runner Device.", "Connect the original supported Runner and retry the original request.")
}
func managedSkillActor(ctx context.Context, server domain.ID) (domain.ID, error) {
	a, ok := domain.PrincipalFrom(ctx)
	if !ok || a.Type != domain.OwnerDevice && a.Type != domain.ClientDevice {
		return "", domain.Fail(domain.PermissionDenied, "Skill management requires an authorized product client.", "Use the paired owner or client.")
	}
	if a.DeviceID == "" {
		return server, nil
	}
	return a.DeviceID, nil
}
func (s *Service) observeManagedSkills(ctx context.Context, scope domain.ManagedSkillRequest) (domain.ManagedSkillResult, error) {
	empty := domain.ManagedSkillResult{Packages: []domain.ManagedSkillPackage{}}
	actor, e := managedSkillActor(ctx, s.Identity.ServerID)
	if e != nil {
		return empty, e
	}
	scope.ActorID = actor
	scope.ServerID = s.Identity.ServerID
	if scope.MachineID.Validate() != nil || scope.RequestID.Validate() != nil {
		return empty, managedSkillFailure()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	s.workspaceReadsMu.Lock()
	reader := s.workspaceReaders[scope.MachineID]
	if reader == nil || reader.pending != nil {
		s.workspaceReadsMu.Unlock()
		return empty, managedSkillFailure()
	}
	if scope.WorkerDeviceID != "" && scope.WorkerDeviceID != reader.device {
		s.workspaceReadsMu.Unlock()
		return empty, managedSkillFailure()
	}
	scope.WorkerDeviceID, scope.WorkerInstanceID = reader.device, reader.instance
	observation := &workspaceObservation{request: workspace.ReadRequest{ID: domain.NewID(), Deadline: deadline.UTC(), Preparation: workspace.PrepareRequest{MachineID: scope.MachineID}, ManagedSkills: &scope}, result: make(chan workspaceReadReply, 1)}
	reader.pending = observation
	s.workspaceReadsMu.Unlock()
	defer func() {
		s.workspaceReadsMu.Lock()
		if reader.pending == observation {
			reader.pending = nil
		}
		s.workspaceReadsMu.Unlock()
	}()
	check := func(tx *store.Tx) error {
		if e := currentWorkspaceReader(tx, reader); e != nil {
			return e
		}
		_, machine, e := activeMachine(tx, scope.MachineID)
		if e != nil {
			return e
		}
		if !slices.Contains(machine.WorkerCapabilities, domain.ManagedSkillsV1) {
			return domain.Fail(domain.Unsupported, "This Runner does not support managed skills.", "Update the original Runner.")
		}
		return tx.WorkerUpdateAdmission(scope.MachineID)
	}
	if e = s.Store.Read(ctx, check); e != nil {
		return empty, e
	}
	primary, e := s.primaryWorkspaceStream(reader.machine, reader.instance)
	if e != nil || primary.ID != reader.primary {
		return empty, managedSkillFailure()
	}
	raw, _ := json.Marshal(observation.request)
	if e = s.Store.Read(ctx, func(tx *store.Tx) error {
		if e := check(tx); e != nil {
			return e
		}
		select {
		case reader.requests <- raw:
			return nil
		default:
			return managedSkillFailure()
		}
	}); e != nil {
		return empty, e
	}
	select {
	case <-ctx.Done():
		return empty, managedSkillFailure()
	case <-reader.done:
		return empty, managedSkillFailure()
	case <-primary.Done:
		return empty, managedSkillFailure()
	case reply := <-observation.result:
		if e = s.Store.Read(ctx, check); e != nil {
			return empty, e
		}
		if reply.problem != nil {
			return empty, reply.problem
		}
		var result domain.ManagedSkillResult
		if domain.Decode(reply.document, &result) != nil || len(result.Packages) > 256 {
			return empty, managedSkillFailure()
		}
		for _, p := range result.Packages {
			var total int64
			seen := map[string]bool{}
			for _, resource := range p.Resources {
				digest, err := hex.DecodeString(resource.Digest)
				if !fs.ValidPath(resource.Path) || strings.Contains(resource.Path, "\\") || seen[resource.Path] || resource.Size < 0 || resource.Size > 16<<20 || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != resource.Digest || resource.Mode != 0600 && resource.Mode != 0700 {
					return empty, managedSkillFailure()
				}
				seen[resource.Path] = true
				total += resource.Size
				if total > 16<<20 {
					return empty, managedSkillFailure()
				}
			}
			legacy, e := hex.DecodeString(p.LegacyRevision)
			if e != nil || len(legacy) != 32 || hex.EncodeToString(legacy) != p.LegacyRevision {
				return empty, managedSkillFailure()
			}
			if len(p.Resources) > 0 {
				raw, _ := json.Marshal(p.Resources)
				full := sha256.Sum256(raw)
				if hex.EncodeToString(full[:]) != p.ContentRevision || !seen["SKILL.md"] {
					return empty, managedSkillFailure()
				}
			} else if scope.Action != domain.ManagedSkillList {
				return empty, managedSkillFailure()
			}
			if p.PackageID.Validate() != nil || p.WorkerDeviceID != reader.device || p.MachineID != reader.machine || p.Revision == 0 || len(p.Resources) > 1024 || domain.Text(p.Name, "skill name", 128, true) != nil || domain.Text(p.Description, "skill description", 4096, true) != nil || (domain.ManagedSkillSelectionList{Selections: []domain.ManagedSkillSelection{{WorkerDeviceID: p.WorkerDeviceID, PackageID: p.PackageID, ContentRevision: p.ContentRevision}}}).Validate() != nil {
				return empty, managedSkillFailure()
			}
		}
		return result, nil
	}
}
func (s *Service) managedSkillWire(ctx context.Context, p domain.ManagedSkillPackage) (*pb.ManagedSkillPackage, error) {
	out := &pb.ManagedSkillPackage{Selection: &pb.ManagedSkillSelection{WorkerDeviceId: string(p.WorkerDeviceID), PackageId: string(p.PackageID), ContentRevision: p.ContentRevision}, Revision: p.Revision, Name: p.Name, Description: p.Description, Enabled: p.Enabled, SupportedHarnessProfiles: []string{}}
	for _, r := range p.Resources {
		out.Resources = append(out.Resources, &pb.ManagedSkillResource{Path: r.Path, Size: uint64(r.Size), Mode: r.Mode, Digest: r.Digest})
	}
	e := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, machine, e := activeMachine(tx, p.MachineID)
		if e != nil {
			return e
		}
		for _, installation := range machine.Installations {
			if installation.Harness == domain.Codex && installation.ProtocolVerified && installation.Protocol != nil && installation.Protocol.State == domain.ProtocolVerified {
				out.SupportedHarnessProfiles = []string{"codex"}
				break
			}
		}
		refs, e := tx.ManagedSkillReferences(p.PackageID)
		for _, id := range refs {
			out.ReferencingAgentIds = append(out.ReferencingAgentIds, string(id))
		}
		return e
	})
	return out, e
}
func (s *Service) ListManagedSkills(ctx context.Context, req *connect.Request[pb.ListManagedSkillsRequest]) (*connect.Response[pb.ListManagedSkillsResponse], error) {
	result, e := s.observeManagedSkills(ctx, domain.ManagedSkillRequest{Action: domain.ManagedSkillList, RequestID: domain.NewID(), MachineID: domain.ID(req.Msg.MachineId)})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	out := &pb.ListManagedSkillsResponse{}
	for _, p := range result.Packages {

		v, e := s.managedSkillWire(ctx, p)
		if e != nil {
			return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
		}
		out.Packages = append(out.Packages, v)
	}
	return connect.NewResponse(out), nil
}
func (s *Service) GetManagedSkill(ctx context.Context, req *connect.Request[pb.GetManagedSkillRequest]) (*connect.Response[pb.GetManagedSkillResponse], error) {
	result, e := s.observeManagedSkills(ctx, domain.ManagedSkillRequest{Action: domain.ManagedSkillView, RequestID: domain.NewID(), MachineID: domain.ID(req.Msg.MachineId), PackageID: domain.ID(req.Msg.PackageId)})
	if e != nil || len(result.Packages) != 1 {
		if e == nil {
			e = managedSkillFailure()
		}
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	v, e := s.managedSkillWire(ctx, result.Packages[0])
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.GetManagedSkillResponse{Package: v, SkillMarkdown: result.SkillMarkdown}), nil
}
func (s *Service) mutateManagedSkill(ctx context.Context, request domain.ManagedSkillRequest) (domain.ManagedSkillPackage, bool, error) {
	var pkg domain.ManagedSkillPackage
	actor, e := managedSkillActor(ctx, s.Identity.ServerID)
	if e != nil {
		return pkg, false, e
	}
	request.ActorID = actor
	request.ServerID = s.Identity.ServerID
	if request.RequestID.Validate() != nil || request.PackageID.Validate() != nil || request.MachineID.Validate() != nil {
		return pkg, false, managedSkillFailure()
	}
	hash := sha256.Sum256([]byte(request.SourceDirectory))
	sourceDigest := hex.EncodeToString(hash[:])
	source := request.SourceDirectory
	request.SourceDirectory = ""
	var retained store.ManagedSkillOperation
	var retainedFound bool
	if e = s.Store.Read(ctx, func(tx *store.Tx) error {
		var err error
		retained, retainedFound, err = tx.ManagedSkillOperation(request.RequestID)
		return err
	}); e != nil {
		return pkg, false, e
	}
	if retainedFound && retained.Finished {
		a, b := retained.Request, request
		b.WorkerDeviceID = a.WorkerDeviceID
		a.WorkerInstanceID = ""
		b.WorkerInstanceID = ""
		one, _ := json.Marshal(a)
		two, _ := json.Marshal(b)
		if string(one) != string(two) || retained.SourceDigest != sourceDigest {
			return pkg, false, domain.Fail(domain.Conflict, "The original skill request changed.", "Retry its exact original identity.")
		}
		return retained.Result, true, nil
	}
	s.workspaceReadsMu.Lock()
	reader := s.workspaceReaders[request.MachineID]
	if reader != nil {
		request.WorkerDeviceID, request.WorkerInstanceID = reader.device, reader.instance
	}
	s.workspaceReadsMu.Unlock()
	if reader == nil {
		return pkg, false, managedSkillFailure()
	}
	var operation store.ManagedSkillOperation
	_, e = s.Store.Mutate(ctx, domain.NewID(), "managed-skill.claim", []any{request, sourceDigest}, func(tx *store.Tx) (any, error) {
		if e := currentWorkspaceReader(tx, reader); e != nil {
			return nil, e
		}
		_, machine, e := activeMachine(tx, request.MachineID)
		if e != nil {
			return nil, e
		}
		if !slices.Contains(machine.WorkerCapabilities, domain.ManagedSkillsV1) {
			return nil, domain.Fail(domain.Unsupported, "This Runner does not support managed skills.", "Update the original Runner.")
		}
		if e = tx.WorkerUpdateAdmission(request.MachineID); e != nil {
			return nil, e
		}
		old, found, e := tx.ManagedSkillOperation(request.RequestID)
		if e != nil {
			return nil, e
		}
		if found {
			a, b := old.Request, request
			a.WorkerInstanceID = ""
			b.WorkerInstanceID = ""
			one, _ := json.Marshal(a)
			two, _ := json.Marshal(b)
			if string(one) != string(two) || old.SourceDigest != sourceDigest {
				return nil, domain.Fail(domain.Conflict, "The skill request identity changed.", "Retry its exact original action and selection.")
			}
			if old.CancelRequestID != "" {
				return nil, domain.Fail(domain.RecoveryRequired, "Original import cancellation is pending.", "Retry the original confirmed cleanup request.")
			}
			operation = old
			return nil, nil
		}
		if request.Action != domain.ManagedSkillImport {
			current, found, e := tx.ManagedSkill(request.PackageID)
			if e != nil {
				return nil, e
			}
			if !found || current.Deleted || current.Deleting || current.Revision != request.ExpectedRevision || current.MachineID != request.MachineID || current.WorkerDeviceID != request.WorkerDeviceID {
				return nil, domain.Fail(domain.Conflict, "The managed package revision changed.", "Refresh the original package.")
			}
			if request.Action == domain.ManagedSkillDelete {
				refs, e := tx.ManagedSkillReferences(request.PackageID)
				if e != nil {
					return nil, e
				}
				if len(refs) > 0 {
					return nil, domain.Fail(domain.Conflict, "Agent Workers still reference this skill.", "Explicitly unselect it before deletion.")
				}
				current.Deleting = true
				if e = tx.PutManagedSkill(current); e != nil {
					return nil, e
				}
			}
		}
		operation = store.ManagedSkillOperation{Request: request, SourceDigest: sourceDigest}
		return nil, tx.PutManagedSkillOperation(operation)
	})
	if e != nil {
		return pkg, false, e
	}
	if operation.Finished {
		return operation.Result, true, nil
	}
	delivery := operation.Request
	delivery.SourceDirectory = source
	observed, e := s.observeManagedSkills(ctx, delivery)
	if e != nil {
		return pkg, false, e
	}
	if request.Action == domain.ManagedSkillDelete {
		if len(observed.Packages) != 0 {
			return pkg, false, managedSkillFailure()
		}
	} else if len(observed.Packages) != 1 || observed.Packages[0].PackageID != request.PackageID {
		return pkg, false, managedSkillFailure()
	} else {
		pkg = observed.Packages[0]
	}
	result, e := s.Store.Mutate(ctx, request.RequestID, "managed-skill.finish", operation, func(tx *store.Tx) (any, error) {
		old, found, e := tx.ManagedSkillOperation(request.RequestID)
		if e != nil || !found || old.Finished {
			return nil, managedSkillFailure()
		}
		if request.Action == domain.ManagedSkillDelete {
			pkg, found, e = tx.ManagedSkill(request.PackageID)
			if e != nil || !found || !pkg.Deleting {
				return nil, managedSkillFailure()
			}
			refs, e := tx.ManagedSkillReferences(request.PackageID)
			if e != nil || len(refs) > 0 {
				return nil, managedSkillFailure()
			}
			pkg.Deleting = false
			pkg.Deleted = true
			pkg.Enabled = false
			pkg.Revision++
		} else if request.Action == domain.ManagedSkillEnable && pkg.Revision != request.ExpectedRevision+1 {
			return nil, managedSkillFailure()
		}
		if e = tx.PutManagedSkill(pkg); e != nil {
			return nil, e
		}
		old.Finished = true
		old.Result = pkg
		if e = tx.PutManagedSkillOperation(old); e != nil {
			return nil, e
		}
		return request.PackageID, nil
	})
	if e == nil {
		s.logger.InfoContext(ctx, "managed_skill_operation_finished", "package_id", request.PackageID, "machine_id", request.MachineID, "action", request.Action, "revision", pkg.Revision)
	}
	return pkg, result.Replayed, e
}
func (s *Service) ImportManagedSkill(ctx context.Context, req *connect.Request[pb.ImportManagedSkillRequest]) (*connect.Response[pb.ImportManagedSkillResponse], error) {
	id := domain.ID(req.Msg.RequestId)
	p, replayed, e := s.mutateManagedSkill(ctx, domain.ManagedSkillRequest{Action: domain.ManagedSkillImport, MachineID: domain.ID(req.Msg.MachineId), PackageID: id, RequestID: id, SourceDirectory: req.Msg.SourceDirectory})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	if p.Deleted {
		return connect.NewResponse(&pb.ImportManagedSkillResponse{RequestId: string(id), Replayed: replayed, Canceled: true}), nil
	}
	v, e := s.managedSkillWire(ctx, p)
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.ImportManagedSkillResponse{Package: v, RequestId: string(id), Replayed: replayed}), nil
}
func (s *Service) SetManagedSkillEnabled(ctx context.Context, req *connect.Request[pb.SetManagedSkillEnabledRequest]) (*connect.Response[pb.SetManagedSkillEnabledResponse], error) {
	if req.Msg.Mutation == nil {
		return nil, rpc.Error(managedSkillFailure(), req.Header().Get(rpc.CorrelationHeader))
	}
	m := req.Msg.Mutation
	p, replayed, e := s.mutateManagedSkill(ctx, domain.ManagedSkillRequest{Action: domain.ManagedSkillEnable, MachineID: domain.ID(req.Msg.MachineId), PackageID: domain.ID(m.Id), RequestID: domain.ID(m.RequestId), ExpectedRevision: m.ExpectedRevision, Enabled: req.Msg.Enabled})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	v, e := s.managedSkillWire(ctx, p)
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.SetManagedSkillEnabledResponse{Package: v, RequestId: m.RequestId, Replayed: replayed}), nil
}
func (s *Service) DeleteManagedSkill(ctx context.Context, req *connect.Request[pb.DeleteManagedSkillRequest]) (*connect.Response[pb.DeleteManagedSkillResponse], error) {
	if req.Msg.Mutation == nil {
		return nil, rpc.Error(managedSkillFailure(), req.Header().Get(rpc.CorrelationHeader))
	}
	m := req.Msg.Mutation
	_, replayed, e := s.mutateManagedSkill(ctx, domain.ManagedSkillRequest{Action: domain.ManagedSkillDelete, MachineID: domain.ID(req.Msg.MachineId), PackageID: domain.ID(m.Id), RequestID: domain.ID(m.RequestId), ExpectedRevision: m.ExpectedRevision})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.DeleteManagedSkillResponse{RequestId: m.RequestId, Replayed: replayed}), nil
}

// Both Agent save surfaces preserve an older client's omitted extension. Only
// explicit presence (including an empty list) can replace these references.
func preserveManagedAgent(tx *store.Tx, input ConfigurationMutation, id domain.ID, agent *domain.Agent) error {
	if agent.ManagedSkills == nil && input.ExpectedRevision > 0 {
		record, e := tx.Get(domain.AgentKind, id)
		if e != nil {
			return e
		}
		old, e := store.Decode[domain.Agent](record)
		if e != nil {
			return e
		}
		agent.ManagedSkills = old.ManagedSkills
	}
	return tx.ValidateManagedSkillSelections(agent.ManagedSkills)
}

// Capture enabled selections for one future generation; accepted copies never
// consult this mutable catalog again.
func (s *Service) managedGenerationSelections(ctx context.Context, agentID, machineID, requestID domain.ID) ([]domain.SkillBinding, uint64, error) {
	result := []domain.SkillBinding{}
	var revision uint64
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		ar, e := tx.Get(domain.AgentKind, agentID)
		if e != nil {
			return e
		}
		var agent domain.Agent
		if e = domain.Decode(ar.Data, &agent); e != nil {
			return e
		}
		revision = ar.Revision
		if agent.ManagedSkills == nil || len(agent.ManagedSkills.Selections) == 0 {
			return nil
		}
		if e = tx.ValidateManagedSkillSelections(agent.ManagedSkills); e != nil {
			return e
		}
		for _, selection := range agent.ManagedSkills.Selections {
			pkg, found, e := tx.ManagedSkill(selection.PackageID)
			if e != nil {
				return e
			}
			if !found || pkg.MachineID != machineID {
				return managedSkillFailure()
			}
			if !pkg.Enabled {
				continue
			}
			result = append(result, domain.SkillBinding{ManagedRevision: pkg.ContentRevision, WorkerDeviceID: pkg.WorkerDeviceID, InventoryID: pkg.PackageID, SkillID: pkg.PackageID, ContentRevision: pkg.LegacyRevision, SnapshotID: requestID})
		}
		if len(result) > 0 && agent.Harness != domain.Codex {
			return domain.Fail(domain.Unsupported, "Managed skills are not supported by this native adapter.", "Use a verified Codex adapter or explicitly unselect enabled packages.")
		}
		return domain.ValidateSkills(result)
	})
	return result, revision, err
}

func (s *Service) CancelManagedSkillImport(ctx context.Context, req *connect.Request[pb.CancelManagedSkillImportRequest]) (*connect.Response[pb.CancelManagedSkillImportResponse], error) {
	actor, e := managedSkillActor(ctx, s.Identity.ServerID)
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	request, original, machine := domain.ID(req.Msg.RequestId), domain.ID(req.Msg.OriginalRequestId), domain.ID(req.Msg.MachineId)
	if request.Validate() != nil || original.Validate() != nil || machine.Validate() != nil || request == original {
		return nil, rpc.Error(managedSkillFailure(), req.Header().Get(rpc.CorrelationHeader))
	}
	identity := struct{ Actor, Original, Machine domain.ID }{actor, original, machine}
	if previous, found, e := s.Store.Replay(ctx, request, "managed-skill.cancel", identity); e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	} else if found {
		var complete bool
		if domain.Decode(previous.Data, &complete) != nil {
			return nil, rpc.Error(managedSkillFailure(), req.Header().Get(rpc.CorrelationHeader))
		}
		return connect.NewResponse(&pb.CancelManagedSkillImportResponse{RequestId: string(request), OriginalRequestId: string(original), Replayed: true, ImportComplete: complete}), nil
	}
	var retained store.ManagedSkillOperation
	_, e = s.Store.Mutate(ctx, domain.NewID(), "managed-skill.cancel.claim", identity, func(tx *store.Tx) (any, error) {
		var found bool
		var e error
		retained, found, e = tx.ManagedSkillOperation(original)
		if e != nil {
			return nil, e
		}
		if !found || retained.Request.Action != domain.ManagedSkillImport || retained.Request.ActorID != actor || retained.Request.ServerID != s.Identity.ServerID || retained.Request.MachineID != machine || retained.Finished && !retained.Canceled {
			return nil, domain.Fail(domain.Conflict, "The original import cannot be canceled.", "Recover its original result; delete a complete catalog package through confirmed deletion.")
		}
		if retained.CancelRequestID != "" && retained.CancelRequestID != request {
			return nil, domain.Fail(domain.Conflict, "Another original cancellation is retained.", "Retry its original request.")
		}
		retained.CancelRequestID = request
		return nil, tx.PutManagedSkillOperation(retained)
	})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	completedPackage := domain.ManagedSkillPackage{}
	if !retained.Canceled {
		cleanup := retained.Request
		cleanup.Action = domain.ManagedSkillCancelImport
		cleanup.RequestID = request
		cleanup.SourceDirectory = ""
		observed, err := s.observeManagedSkills(ctx, cleanup)
		e = err
		if e != nil {
			return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
		}
		if len(observed.Packages) > 1 || len(observed.Packages) == 1 && observed.Packages[0].PackageID != original {
			return nil, rpc.Error(managedSkillFailure(), req.Header().Get(rpc.CorrelationHeader))
		}
		if len(observed.Packages) == 1 {
			completedPackage = observed.Packages[0]
		}
	}
	_, e = s.Store.Mutate(ctx, request, "managed-skill.cancel", identity, func(tx *store.Tx) (any, error) {
		old, found, e := tx.ManagedSkillOperation(original)
		if e != nil {
			return nil, e
		}
		if !found || old.CancelRequestID != request {
			return nil, managedSkillFailure()
		}
		old.Finished = true
		old.Canceled = completedPackage.PackageID == ""
		old.Result = domain.ManagedSkillPackage{PackageID: original, MachineID: machine, WorkerDeviceID: old.Request.WorkerDeviceID, Deleted: true}
		if !old.Canceled {
			old.Result = completedPackage
			if e = tx.PutManagedSkill(completedPackage); e != nil {
				return nil, e
			}
		}
		if e = tx.PutManagedSkillOperation(old); e != nil {
			return nil, e
		}
		return !old.Canceled, nil
	})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	s.logger.InfoContext(ctx, "managed_skill_original_import_cleanup_finished", "original_request_id", original, "request_id", request, "machine_id", machine, "import_complete", completedPackage.PackageID != "")
	return connect.NewResponse(&pb.CancelManagedSkillImportResponse{RequestId: string(request), OriginalRequestId: string(original), ImportComplete: completedPackage.PackageID != ""}), nil
}
