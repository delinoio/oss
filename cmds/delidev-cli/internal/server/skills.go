// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"slices"
	"time"
)

func skillUnavailable() error {
	return domain.Fail(domain.Unavailable, "Skills are unavailable for this Runner Device and Harness.", "Connect a supported Codex Runner Device and retry.")
}
func validateSkillResult(result domain.SkillReadResult) error {
	if len(result.Entries) > 256 {
		return skillUnavailable()
	}
	seen := map[domain.ID]bool{}
	for _, v := range result.Entries {
		if domain.ValidateSkills([]domain.SkillBinding{{WorkerDeviceID: v.WorkerDeviceID, InventoryID: v.InventoryID, SkillID: v.SkillID, ContentRevision: v.ContentRevision, SnapshotID: v.InventoryID}}) != nil || seen[v.SkillID] || domain.Text(v.Name, "skill name", 128, true) != nil || domain.Text(v.Description, "skill description", 4096, true) != nil || (v.Provenance != "user" && v.Provenance != "project") {
			return skillUnavailable()
		}
		seen[v.SkillID] = true
	}
	return nil
}
func skillScope(tx *store.Tx, request domain.SkillReadRequest) (workspace.PrepareRequest, workspace.Manifest, error) {
	var input workspace.PrepareRequest
	var manifest workspace.Manifest
	if err := tx.Authorize(); err != nil {
		return input, manifest, err
	}
	if request.ProjectID != "" {
		if e := validateSessionSelection(tx, domain.CreateSession{AgentID: request.AgentID, MachineID: request.MachineID, ProjectID: request.ProjectID}); e != nil {
			return input, manifest, e
		}
	}

	row, err := tx.Get(domain.AgentKind, request.AgentID)
	if err != nil {
		return input, manifest, err
	}
	agent, err := store.Decode[domain.Agent](row)
	if err != nil {
		return input, manifest, err
	}
	_, machine, err := activeMachine(tx, request.MachineID)
	if err != nil {
		return input, manifest, err
	}
	if !slices.Contains(machine.WorkerCapabilities, domain.NativeSkillsV1) {
		return input, manifest, skillUnavailable()
	}
	if err = tx.WorkerUpdateAdmission(request.MachineID); err != nil {
		return input, manifest, err
	}
	if request.SessionID != "" {
		_, session, e := sessionRecord(tx, request.SessionID)
		if e != nil {
			return input, manifest, e
		}
		if session.AgentID != request.AgentID || session.MachineID != request.MachineID {
			return input, manifest, skillUnavailable()
		}
		if session.InitialExecution != nil {
			if session.InitialExecution.Configuration.Harness != domain.Codex {
				return input, manifest, skillUnavailable()
			}
		} else if agent.Harness != domain.Codex {
			return input, manifest, skillUnavailable()
		}
		if session.WorkspaceAvailable() {
			return workspaceReadScope(tx, request.SessionID)
		}
	} else if agent.Harness != domain.Codex {
		return input, manifest, skillUnavailable()
	}
	input.MachineID = request.MachineID
	return input, manifest, nil
}
func (s *Service) observeSkills(ctx context.Context, scope domain.SkillReadRequest) (domain.SkillReadResult, error) {
	empty := domain.SkillReadResult{Entries: []domain.SkillEntry{}}
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return empty, domain.Fail(domain.PermissionDenied, "Skills require an authorized product client.", "Use the paired owner or client.")
	}
	scope.ActorID = actor.DeviceID
	if scope.ActorID == "" {
		scope.ActorID = s.Identity.ServerID
	}
	if scope.MachineID.Validate() != nil || scope.AgentID.Validate() != nil || (scope.SessionID != "" && scope.SessionID.Validate() != nil) || domain.ValidateSkills(scope.Selections) != nil {
		return empty, skillUnavailable()
	}
	var input workspace.PrepareRequest
	var manifest workspace.Manifest
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		var e error
		row, readErr := tx.Get(domain.AgentKind, scope.AgentID)
		if readErr != nil {
			return readErr
		}
		scope.AgentRevision = row.Revision
		if scope.SessionID != "" {
			_, session, readErr := sessionRecord(tx, scope.SessionID)
			if readErr != nil {
				return readErr
			}
			if session.InitialExecution != nil {
				scope.AgentRevision = session.InitialExecution.Configuration.AgentRevision
			}
		}
		input, manifest, e = skillScope(tx, scope)
		return e
	})
	if err != nil {
		return empty, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	observation := &workspaceObservation{request: workspace.ReadRequest{ID: domain.NewID(), Deadline: deadline.UTC(), Preparation: input, Manifest: manifest, Skills: &scope}, result: make(chan workspaceReadReply, 1)}
	s.workspaceReadsMu.Lock()
	reader := s.workspaceReaders[scope.MachineID]
	if reader == nil || reader.pending != nil {
		s.workspaceReadsMu.Unlock()
		return empty, skillUnavailable()
	}
	scope.WorkerDeviceID = reader.device
	scope.WorkerInstanceID = reader.instance
	for _, binding := range scope.Selections {
		if binding.WorkerDeviceID != reader.device {
			s.workspaceReadsMu.Unlock()
			return empty, skillUnavailable()
		}
	}
	active := 0
	for _, current := range s.workspaceReaders {
		if current.pending != nil {
			active++
		}
	}
	if active >= 64 {
		s.workspaceReadsMu.Unlock()
		return empty, domain.Fail(domain.ResourceExhausted, "The skill observation limit is reached.", "Wait for current reads and retry.")
	}
	reader.pending = observation
	s.workspaceReadsMu.Unlock()
	defer func() {
		s.workspaceReadsMu.Lock()
		if reader.pending == observation {
			reader.pending = nil
		}
		s.workspaceReadsMu.Unlock()
	}()
	primary, err := s.primaryWorkspaceStream(reader.machine, reader.instance)
	if err != nil || primary.ID != reader.primary {
		return empty, skillUnavailable()
	}
	check := func(tx *store.Tx) error {
		if e := currentWorkspaceReader(tx, reader); e != nil {
			return e
		}
		row, e := tx.Get(domain.AgentKind, scope.AgentID)
		if e != nil {
			return e
		}
		revision := row.Revision
		if scope.SessionID != "" {
			_, session, err := sessionRecord(tx, scope.SessionID)
			if err != nil {
				return err
			}
			if session.InitialExecution != nil {
				revision = session.InitialExecution.Configuration.AgentRevision
			}
		}
		if revision != scope.AgentRevision {
			return skillUnavailable()
		}
		freshInput, freshManifest, e := skillScope(tx, scope)
		if e != nil {
			return e
		}
		a, _ := json.Marshal([]any{input, manifest})
		b, _ := json.Marshal([]any{freshInput, freshManifest})
		if !bytes.Equal(a, b) {
			return skillUnavailable()
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
			return skillUnavailable()
		}
	})
	if err != nil {
		return empty, err
	}
	select {
	case <-ctx.Done():
		return empty, skillUnavailable()
	case <-reader.done:
		return empty, skillUnavailable()
	case <-primary.Done:
		return empty, skillUnavailable()
	case reply := <-observation.result:
		if err = s.Store.Read(ctx, check); err != nil {
			return empty, err
		}
		current, e := s.primaryWorkspaceStream(reader.machine, reader.instance)
		if e != nil || current.ID != reader.primary {
			return empty, skillUnavailable()
		}
		if reply.problem != nil {
			return empty, reply.problem
		}
		var result domain.SkillReadResult
		if domain.Decode(reply.document, &result) != nil || validateSkillResult(result) != nil {
			return empty, skillUnavailable()
		}
		for _, entry := range result.Entries {
			if entry.WorkerDeviceID != reader.device {
				return empty, skillUnavailable()
			}
		}
		result.Scope = &scope
		return result, nil
	}
}
func (s *Service) ListSkills(ctx context.Context, req *connect.Request[pb.ListSkillsRequest]) (*connect.Response[pb.ListSkillsResponse], error) {
	result, err := s.observeSkills(ctx, domain.SkillReadRequest{MachineID: domain.ID(req.Msg.MachineId), AgentID: domain.ID(req.Msg.AgentId), ProjectID: domain.ID(req.Msg.ProjectId), SessionID: domain.ID(req.Msg.SessionId)})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := &pb.ListSkillsResponse{}
	for _, entry := range result.Entries {
		p := pb.SkillProvenance_SKILL_PROVENANCE_USER
		if entry.Provenance == "project" {
			p = pb.SkillProvenance_SKILL_PROVENANCE_PROJECT
		}
		response.Skills = append(response.Skills, &pb.SkillEntry{Selection: &pb.SkillSelection{WorkerDeviceId: string(entry.WorkerDeviceID), InventoryId: string(entry.InventoryID), SkillId: string(entry.SkillID), ContentRevision: entry.ContentRevision}, Name: entry.Name, Description: entry.Description, Provenance: p})
	}
	return connect.NewResponse(response), nil
}
func skillBindings(values []*pb.SkillSelection, request string) ([]domain.SkillBinding, error) {
	result := []domain.SkillBinding{}
	for _, v := range values {
		if v == nil {
			return nil, skillUnavailable()
		}
		result = append(result, domain.SkillBinding{WorkerDeviceID: domain.ID(v.WorkerDeviceId), InventoryID: domain.ID(v.InventoryId), SkillID: domain.ID(v.SkillId), ContentRevision: v.ContentRevision, SnapshotID: domain.ID(request)})
	}
	return result, domain.ValidateSkills(result)
}

// The acceptance transaction fences the prepared original context once more.
func acceptSkillScope(tx *store.Tx, scope *domain.SkillReadRequest) error {
	if scope == nil {
		return nil
	}
	if _, _, err := skillScope(tx, *scope); err != nil {
		return err
	}
	row, err := tx.Get(domain.AgentKind, scope.AgentID)
	if err != nil {
		return err
	}
	revision := row.Revision
	if scope.SessionID != "" {
		_, session, err := sessionRecord(tx, scope.SessionID)
		if err != nil {
			return err
		}
		if session.InitialExecution != nil {
			revision = session.InitialExecution.Configuration.AgentRevision
		}
	}
	instance, _, err := tx.WorkerInstance(scope.MachineID)
	if err != nil || instance != scope.WorkerInstanceID || revision != scope.AgentRevision {
		return skillUnavailable()
	}
	device, err := tx.Get(domain.DeviceKind, scope.WorkerDeviceID)
	if err != nil {
		return err
	}
	worker, err := store.Decode[domain.Device](device)
	if err != nil || worker.Revoked || worker.MachineID != scope.MachineID || worker.Type != domain.WorkerDevice {
		return skillUnavailable()
	}
	return nil
}
