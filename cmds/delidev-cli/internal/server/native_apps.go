// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func nativeAppsActor(ctx context.Context) (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice {
		return actor, domain.Fail(domain.PermissionDenied, "Apps require an owner or paired client.", "Use an authorized product client.")
	}
	return actor, nil
}

func nativeAppsScope(tx *store.Tx, id domain.ID) (store.Record, domain.Session, domain.ExecutionJobInput, error) {
	var input domain.ExecutionJobInput
	row, session, err := sessionRecord(tx, id)
	if err != nil {
		return row, session, input, err
	}
	if tx.Authorize() != nil || !session.WorkspaceAvailable() || session.InitialExecution == nil || session.Fork != nil || session.Recovery != domain.NoRecovery || session.Archive != domain.NotArchived || session.CompactionJobID != "" || session.PendingSteerID != "" {
		return row, session, input, domain.NativeAppsUnavailable()
	}
	if err := tx.RequireNoSessionFork(id); err != nil {
		return row, session, input, err
	}
	selected := session.ExecutionSelection()
	jobRow, err := tx.SessionExecutionJob(id, selected.ID)
	if err != nil {
		return row, session, input, domain.NativeAppsUnavailable()
	}
	job, err := store.Decode[domain.Job](jobRow)
	if err != nil || job.Type != domain.ExecuteSessionJob || job.State == domain.JobUncertain || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.Configuration.Harness != domain.Codex || input.Configuration.SidechatPolicy != "" || input.Fork != nil || !session.OwnsExecution(input) {
		return row, session, input, domain.NativeAppsUnavailable()
	}
	canceled, cancelErr := tx.JobCancellationRequested(jobRow.ID)
	instance, seen, instanceErr := tx.WorkerInstance(session.MachineID)
	if cancelErr != nil || canceled || instanceErr != nil || instance != job.InstanceID || time.Since(seen) > domain.WorkerConnectionTimeout {
		return row, session, input, domain.NativeAppsUnavailable()
	}
	_, machine, err := activeMachine(tx, session.MachineID)
	if err != nil || !slices.Contains(machine.WorkerCapabilities, domain.SessionNativeAppsV1) {
		return row, session, input, domain.NativeAppsUnavailable()
	}
	if err := checkedExecutionSource(tx, row, session, machine, input); err != nil {
		return row, session, input, err
	}
	if err := tx.WorkerUpdateAdmission(session.MachineID); err != nil {
		return row, session, input, err
	}
	return row, session, input, nil
}

// The existing bounded workspace observation channel transports metadata from
// the original live native owner. It neither prepares a new process nor starts
// an inference or login. Publication rechecks scope and paired ownership.
func (s *Service) observeNativeApps(ctx context.Context, scope domain.NativeAppScope, force bool) (domain.NativeAppInventory, domain.ID, domain.ID, error) {
	var empty domain.NativeAppInventory
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	profile := &workspace.NativeAppsReadRequest{Scope: scope, ForceRefresh: force}
	observation := &workspaceObservation{request: workspace.ReadRequest{ID: domain.NewID(), Deadline: deadline.UTC(), NativeApps: profile}, result: make(chan workspaceReadReply, 1)}
	s.workspaceReadsMu.Lock()
	reader := s.workspaceReaders[scope.MachineID]
	if reader == nil || reader.pending != nil {
		s.workspaceReadsMu.Unlock()
		return empty, "", "", domain.NativeAppsUnavailable()
	}
	active := 0
	for _, r := range s.workspaceReaders {
		if r.pending != nil {
			active++
		}
	}
	if active >= 64 {
		s.workspaceReadsMu.Unlock()
		return empty, "", "", domain.NativeAppsUnavailable()
	}
	profile.WorkerDeviceID, profile.WorkerInstanceID = reader.device, reader.instance
	reader.pending = observation
	s.workspaceReadsMu.Unlock()
	defer func() {
		s.workspaceReadsMu.Lock()
		defer s.workspaceReadsMu.Unlock()
		if reader.pending == observation {
			reader.pending = nil
		}
	}()
	primary, err := s.primaryWorkspaceStream(reader.machine, reader.instance)
	if err != nil || primary.ID != reader.primary {
		return empty, "", "", domain.NativeAppsUnavailable()
	}
	check := func(tx *store.Tx) error {
		if err := currentWorkspaceReader(tx, reader); err != nil {
			return err
		}
		_, _, input, err := nativeAppsScope(tx, scope.SessionID)
		if err != nil {
			return err
		}
		if domain.NativeAppsAssignmentScope(input) != scope {
			return domain.NativeAppsUnavailable()
		}
		return nil
	}
	raw, _ := json.Marshal(observation.request)
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := check(tx); err != nil {
			return err
		}
		select {
		case reader.requests <- raw:
			return nil
		default:
			return domain.NativeAppsUnavailable()
		}
	}); err != nil {
		return empty, "", "", err
	}
	select {
	case <-ctx.Done():
		return empty, "", "", domain.NativeAppsUnavailable()
	case <-reader.done:
		return empty, "", "", domain.NativeAppsUnavailable()
	case <-primary.Done:
		return empty, "", "", domain.NativeAppsUnavailable()
	case reply := <-observation.result:
		if err := s.Store.Read(ctx, check); err != nil {
			return empty, "", "", err
		}
		current, err := s.primaryWorkspaceStream(reader.machine, reader.instance)
		if err != nil || current.ID != reader.primary {
			return empty, "", "", domain.NativeAppsUnavailable()
		}
		if reply.problem != nil {
			return empty, "", "", reply.problem
		}
		var inventory domain.NativeAppInventory
		if domain.Decode(reply.document, &inventory) != nil || inventory.Validate() != nil || inventory.Scope != scope {
			return empty, "", "", domain.NativeAppsUnavailable()
		}
		return inventory, reader.device, reader.instance, nil
	}
}

func wireNativeAppScope(v domain.NativeAppScope) *pb.SessionNativeAppScope {
	return &pb.SessionNativeAppScope{SessionId: string(v.SessionID), MachineId: string(v.MachineID), OriginalAccountId: string(v.AccountID), OriginalConnectionId: string(v.ConnectionID), ConfigurationDigest: v.ConfigurationDigest}
}
func wireNativeAppSelection(v *domain.SessionNativeAppSelection) *pb.SessionNativeAppSelection {
	if v == nil {
		return nil
	}
	return &pb.SessionNativeAppSelection{Scope: wireNativeAppScope(v.Scope), InventoryId: string(v.InventoryID), Revision: v.Revision, AppIds: slices.Clone(v.AppIDs)}
}

func (s *Service) ReadSessionApps(ctx context.Context, req *connect.Request[pb.ReadSessionAppsRequest]) (*connect.Response[pb.ReadSessionAppsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.ReadSessionAppsResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	actor, err := nativeAppsActor(ctx)
	if err != nil {
		return fail(err)
	}
	id, requestID := domain.ID(req.Msg.SessionId), domain.ID(req.Msg.RequestId)
	if id.Validate() != nil || requestID.Validate() != nil {
		return fail(domain.NativeAppsUnavailable())
	}
	var scope domain.NativeAppScope
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, _, input, err := nativeAppsScope(tx, id)
		scope = domain.NativeAppsAssignmentScope(input)
		return err
	}); err != nil {
		return fail(err)
	}
	// Reserve the original read identity before contacting the native owner.
	// An uncertain replay observes retained evidence only; it never repeats a
	// hosted runtime refresh. A fresh explicit identity may make a fresh read.
	begin, err := s.Store.Mutate(ctx, requestID, "session.apps.inventory.request", struct {
		Actor   domain.Principal
		Scope   domain.NativeAppScope
		Refresh bool
	}{actor, scope, req.Msg.ForceRefresh}, func(tx *store.Tx) (any, error) {
		row, session, input, err := nativeAppsScope(tx, id)
		if err != nil {
			return nil, err
		}
		if domain.NativeAppsAssignmentScope(input) != scope {
			return nil, domain.NativeAppsUnavailable()
		}
		state := domain.NativeAppsReadState{RequestID: requestID, CompletionID: domain.NewID(), Actor: actor, Scope: scope, ForceRefresh: req.Msg.ForceRefresh}
		session.NativeAppsRead = &state
		if _, err := tx.Put(row.Kind, row.ID, row.Revision, row.SessionID, row.ProjectID, session); err != nil {
			return nil, err
		}
		return state, nil
	})
	if err != nil {
		return fail(err)
	}
	var state domain.NativeAppsReadState
	if domain.Decode(begin.Data, &state) != nil || state.RequestID != requestID || state.Scope != scope || state.Actor != actor {
		return fail(domain.NativeAppsUnavailable())
	}
	var inventory domain.NativeAppInventory
	var observed time.Time
	var selection *domain.SessionNativeAppSelection
	if !begin.Replayed {
		var device, instance domain.ID
		inventory, device, instance, err = s.observeNativeApps(ctx, scope, req.Msg.ForceRefresh)
		if err != nil {
			return fail(err)
		}
		observed = time.Now().UTC()
		_, err = s.Store.Mutate(ctx, state.CompletionID, "session.apps.inventory.complete", struct {
			Request          domain.ID
			Inventory        domain.NativeAppInventory
			Device, Instance domain.ID
		}{requestID, inventory, device, instance}, func(tx *store.Tx) (any, error) {
			row, session, input, err := nativeAppsScope(tx, id)
			if err != nil {
				return nil, err
			}
			current, seen, err := tx.WorkerInstance(scope.MachineID)
			if err != nil || current != instance || time.Since(seen) > domain.WorkerConnectionTimeout || domain.NativeAppsAssignmentScope(input) != scope || session.NativeAppsRead == nil || *session.NativeAppsRead != state {
				return nil, domain.NativeAppsUnavailable()
			}
			session.NativeAppsObservation = &domain.NativeAppsObservation{RequestID: requestID, Inventory: inventory, Actor: actor, WorkerDeviceID: device, WorkerInstanceID: instance, ObservedAt: observed}
			_, err = tx.Put(row.Kind, row.ID, row.Revision, row.SessionID, row.ProjectID, session)
			return inventory.InventoryID, err
		})
		if err != nil {
			return fail(err)
		}
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		_, session, input, err := nativeAppsScope(tx, id)
		if err != nil {
			return err
		}
		retained := session.NativeAppsObservation
		if domain.NativeAppsAssignmentScope(input) != scope || retained == nil || retained.RequestID != requestID || retained.Actor != actor || retained.Inventory.Scope != scope || retained.Inventory.Validate() != nil {
			return domain.NativeAppsUnavailable()
		}
		inventory, observed = retained.Inventory, retained.ObservedAt
		selection = session.NativeApps
		if selection != nil && selection.Scope != scope {
			selection = nil
		}
		return nil
	}); err != nil {
		return fail(err)
	}
	out := &pb.ReadSessionAppsResponse{RequestId: string(requestID), SessionId: string(id), OriginalAccountId: string(scope.AccountID), OriginalConnectionId: string(scope.ConnectionID), ConfigurationDigest: scope.ConfigurationDigest, InventoryId: string(inventory.InventoryID), Complete: true, ObservedAt: observed.Format(time.RFC3339Nano), Selection: wireNativeAppSelection(selection), Discovered: []*pb.NativeAppDiscovery{}, Installed: []*pb.NativeAppInstalled{}}
	for _, v := range inventory.Discovered {
		out.Discovered = append(out.Discovered, &pb.NativeAppDiscovery{Id: v.ID, Name: v.Name, Accessible: v.Accessible, Enabled: v.Enabled})
	}
	for _, v := range inventory.Installed {
		out.Installed = append(out.Installed, &pb.NativeAppInstalled{Id: v.ID, Enabled: v.Enabled, Callable: v.Callable})
	}
	s.logger.InfoContext(ctx, "native_apps_inventory_observed", "session_id", id, "discovered_count", len(inventory.Discovered), "installed_count", len(inventory.Installed), "force_refresh", req.Msg.ForceRefresh)
	response := connect.NewResponse(out)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

type nativeAppsSelectionReceipt struct {
	SessionID domain.ID                        `json:"session_id"`
	Selection domain.SessionNativeAppSelection `json:"selection"`
}

func (s *Service) UpdateSessionApps(ctx context.Context, req *connect.Request[pb.UpdateSessionAppsRequest]) (*connect.Response[pb.UpdateSessionAppsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.UpdateSessionAppsResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	actor, err := nativeAppsActor(ctx)
	if err != nil {
		return fail(err)
	}
	meta := req.Msg.Mutation
	id := domain.ID(req.Msg.SessionId)
	if meta == nil || meta.Id != string(id) || meta.ExpectedRevision == 0 || id.Validate() != nil || domain.ID(meta.RequestId).Validate() != nil || len(req.Msg.SelectedAppIds) > domain.MaxSessionNativeApps {
		return fail(domain.NativeAppsUnavailable())
	}
	identity := struct {
		Actor                                           domain.Principal
		SessionID, AccountID, ConnectionID, InventoryID domain.ID
		Digest                                          string
		Revision                                        uint64
		Apps                                            []string
	}{actor, id, domain.ID(req.Msg.OriginalAccountId), domain.ID(req.Msg.OriginalConnectionId), domain.ID(req.Msg.InventoryId), req.Msg.ConfigurationDigest, meta.ExpectedRevision, slices.Clone(req.Msg.SelectedAppIds)}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "session.apps.selection", identity, func(tx *store.Tx) (any, error) {
		row, session, input, err := nativeAppsScope(tx, id)
		if err != nil {
			return nil, err
		}
		if row.Revision != identity.Revision {
			return nil, domain.Fail(domain.Conflict, "The session changed before Apps selection.", "Refresh the session and retain the original uncertain request.")
		}
		scope := domain.NativeAppsAssignmentScope(input)
		observation := session.NativeAppsObservation
		if scope.AccountID != identity.AccountID || scope.ConnectionID != identity.ConnectionID || scope.ConfigurationDigest != identity.Digest || observation == nil || observation.Actor != actor || observation.Inventory.Scope != scope || observation.Inventory.InventoryID != identity.InventoryID || observation.Inventory.Validate() != nil {
			return nil, domain.NativeAppsUnavailable()
		}
		instance, seen, err := tx.WorkerInstance(scope.MachineID)
		if err != nil || instance != observation.WorkerInstanceID || time.Since(seen) > domain.WorkerConnectionTimeout {
			return nil, domain.NativeAppsUnavailable()
		}
		revision := uint64(1)
		if session.NativeApps != nil {
			revision = session.NativeApps.Revision + 1
			if revision == 0 {
				return nil, domain.NativeAppsUnavailable()
			}
		}
		apps := slices.Clone(identity.Apps)
		if apps == nil {
			apps = []string{}
		}
		selection := domain.SessionNativeAppSelection{Scope: scope, InventoryID: identity.InventoryID, Revision: revision, AppIDs: apps}
		if selection.Validate() != nil {
			return nil, domain.NativeAppsUnavailable()
		}
		for _, id := range selection.AppIDs {
			if !slices.ContainsFunc(observation.Inventory.Discovered, func(v domain.NativeAppDiscovery) bool { return v.ID == id && v.Accessible }) {
				return nil, domain.NativeAppsUnavailable()
			}
		}
		session.NativeApps = &selection
		if _, err := tx.Put(row.Kind, row.ID, row.Revision, row.SessionID, row.ProjectID, session); err != nil {
			return nil, err
		}
		return nativeAppsSelectionReceipt{SessionID: id, Selection: selection}, nil
	})
	if err != nil {
		return fail(err)
	}
	var retained nativeAppsSelectionReceipt
	if domain.Decode(result.Data, &retained) != nil || retained.SessionID != id || retained.Selection.Validate() != nil {
		return fail(domain.NativeAppsUnavailable())
	}
	var row store.Record
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		row, _, err = sessionRecord(tx, id)
		return err
	}); err != nil {
		return fail(err)
	}
	s.logger.InfoContext(ctx, "native_apps_selection_saved", "session_id", id, "selection_revision", retained.Selection.Revision, "selected_count", len(retained.Selection.AppIDs), "replayed", result.Replayed)
	response := connect.NewResponse(&pb.UpdateSessionAppsResponse{Receipt: rpc.Resource(row), Selection: wireNativeAppSelection(&retained.Selection), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

// Called only inside the first durable question.claim transaction. Revocation
// uses the same storage transaction boundary; a retained claim is never replayed.
func (s *Service) validateNativeAppsEffect(tx *store.Tx, value domain.ExecutionInteraction) error {
	proof := value.NativeApps
	if proof == nil {
		return nil
	}
	if proof.Validate() != nil || proof.NativeItemID != value.NativeItemID {
		return domain.NativeAppsUnavailable()
	}
	_, session, input, err := nativeAppsScope(tx, proof.Scope.SessionID)
	if err != nil {
		return err
	}
	if input.ExecutionID != value.ExecutionID || session.ActiveExecutionID != value.ExecutionID || session.Execution == nil || session.Execution.NativeThreadID != value.NativeThreadID || session.Execution.NativeTurnID != value.NativeTurnID || domain.NativeAppsAssignmentScope(input) != proof.Scope || input.NativeApps == nil || session.NativeApps == nil {
		return domain.NativeAppsUnavailable()
	}
	original, _ := json.Marshal(input.NativeApps)
	claimed, _ := json.Marshal(proof.Selection)
	if string(original) != string(claimed) {
		return domain.NativeAppsUnavailable()
	}
	return domain.AdmitNativeAppCall(proof.Selection, *session.NativeApps, proof.Inventory, proof.AppID)
}

// Refresh only the pending original Apps call before its first release claim.
// This installed-state read is not a connector call, config mutation or native
// approval. Claimed/uncertain delivery retains its original owner and receipt.
func (s *Service) refreshNativeAppsEffect(ctx context.Context, identity questionClaimIdentity) (*domain.NativeAppInventory, error) {
	var scope domain.NativeAppScope
	needed := false
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		row, value, err := s.currentClaimQuestion(tx, identity)
		if err != nil {
			return err
		}
		if value.NativeApps == nil {
			return nil
		}
		if value.Response.State == domain.QuestionResponseClaimed && value.Response.Claim != nil {
			return nil
		}
		if row.Revision != identity.Revision || value.Response.State != domain.QuestionResponseQueued || value.Response.Claim != nil || value.NativeApps.Validate() != nil || value.NativeApps.NativeItemID != value.NativeItemID {
			return domain.NativeAppsUnavailable()
		}
		_, session, input, err := nativeAppsScope(tx, value.NativeApps.Scope.SessionID)
		if err != nil {
			return err
		}
		if input.ExecutionID != value.ExecutionID || input.NativeApps == nil || session.ActiveExecutionID != value.ExecutionID || domain.NativeAppsAssignmentScope(input) != value.NativeApps.Scope {
			return domain.NativeAppsUnavailable()
		}
		frozen, _ := json.Marshal(input.NativeApps)
		proof, _ := json.Marshal(value.NativeApps.Selection)
		if string(frozen) != string(proof) {
			return domain.NativeAppsUnavailable()
		}
		scope = value.NativeApps.Scope
		needed = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !needed {
		return nil, nil
	}
	inventory, _, _, err := s.observeNativeApps(ctx, scope, false)
	if err != nil {
		return nil, err
	}
	return &inventory, nil
}
