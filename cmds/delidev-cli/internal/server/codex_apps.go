// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func codexAppsUnavailable() error {
	return domain.Fail(domain.RecoveryRequired, "The original Codex app controller is unavailable.", "Preserve the original session, account, selection and native obligation; do not replace or resend it.")
}
func codexAppsResource(id domain.ID, revision uint64, session domain.ID, value any) (*pb.Resource, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &pb.Resource{Id: string(id), Kind: pb.EntityKind_ENTITY_KIND_UNSPECIFIED, Revision: revision, SchemaVersion: 1, SessionId: string(session), DocumentJson: raw}, nil
}
func codexAppsResponse(value *store.CodexAppsSnapshot, replayed bool) (*pb.CodexAppsResponse, error) {
	result := &pb.CodexAppsResponse{Replayed: replayed}
	if value == nil {
		return result, nil
	}
	var err error
	if value.Configuration != nil {
		result.Configuration, err = codexAppsResource(value.Configuration.Generation, value.Revision, value.SessionID, value.Configuration)
		if err != nil {
			return nil, err
		}
	}
	if value.Operation != nil {
		result.Operation, err = codexAppsResource(value.Operation.ID, value.Operation.Revision, value.SessionID, value.Operation)
		if err != nil {
			return nil, err
		}
	}
	if value.Inventory != nil {
		result.Inventory, err = codexAppsResource(value.Inventory.OperationID, value.Revision, value.SessionID, value.Inventory)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
func (s *Service) readCodexApps(ctx context.Context, id domain.ID, replayed bool) (*pb.CodexAppsResponse, error) {
	var value *store.CodexAppsSnapshot
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		if _, _, err := sessionRecord(tx, id); err != nil {
			return err
		}
		var err error
		value, err = tx.CodexAppsSnapshot(id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return codexAppsResponse(value, replayed)
}
func (s *Service) GetCodexApps(ctx context.Context, req *connect.Request[pb.GetCodexAppsRequest]) (*connect.Response[pb.CodexAppsResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	if err := compactionActor(ctx); err != nil {
		return nil, rpc.Error(err, corr)
	}
	if err := domain.ID(req.Msg.SessionId).Validate(); err != nil {
		return nil, rpc.Error(err, corr)
	}
	value, err := s.readCodexApps(ctx, domain.ID(req.Msg.SessionId), false)
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	response := connect.NewResponse(value)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

// Positive controls preserve the actual selected account; routing candidates,
// inventory labels, installed plugins and API accounts cannot supply authority.
func codexAppsProfile(tx *store.Tx, sr store.Record, session domain.Session, accountID domain.ID, accountRevision uint64) (domain.ExecutionJobInput, domain.Machine, error) {
	var empty domain.ExecutionJobInput
	if session.InitialExecution == nil || session.IsSidechat() || session.Fork != nil || session.Archive != domain.NotArchived || session.InitialExecution.Configuration.Harness != domain.Codex || !session.InitialExecution.Configuration.Subscription || session.InitialExecution.Configuration.SubscriptionService != domain.SubscriptionChatGPT || session.ExecutionSelection().AccountID != accountID {
		return empty, domain.Machine{}, codexAppsUnavailable()
	}
	if err := tx.RequireNoSessionFork(sr.ID); err != nil {
		return empty, domain.Machine{}, err
	}
	ar, err := tx.Get(domain.AccountKind, accountID)
	if err != nil {
		return empty, domain.Machine{}, err
	}
	account, err := store.Decode[domain.Account](ar)
	if err != nil {
		return empty, domain.Machine{}, err
	}
	if accountRevision == 0 || ar.Revision != accountRevision || account.Type != domain.SubscriptionAccount || account.SubscriptionService != domain.SubscriptionChatGPT {
		return empty, domain.Machine{}, codexAppsUnavailable()
	}
	_, machine, err := activeMachine(tx, session.MachineID)
	if err != nil {
		return empty, machine, err
	}
	if !slices.Contains(machine.WorkerCapabilities, domain.CodexAppsV1) {
		return empty, machine, domain.Fail(domain.Unsupported, "The original Worker does not support Codex apps.", "Update that Worker without replacing this account or controller.")
	}
	jobRecord, err := tx.SessionExecutionJob(sr.ID, session.ExecutionSelection().ID)
	if err != nil {
		return empty, machine, err
	}
	job, err := store.Decode[domain.Job](jobRecord)
	if err != nil {
		return empty, machine, err
	}
	if domain.Decode(job.Input, &empty) != nil || empty.Validate() != nil || !session.OwnsExecution(empty) {
		return empty, machine, codexAppsUnavailable()
	}
	if err := checkedExecutionSource(tx, sr, session, machine, empty); err != nil {
		return empty, machine, err
	}
	return empty, machine, nil
}
func codexAppsIdle(session domain.Session) bool {
	return session.ActiveExecutionID == "" && session.PendingSteerID == "" && session.PendingInputs == 0 && session.PendingInputBytes == 0 && session.CompactionJobID == "" && session.ExecutionRecoveryJobID == "" && session.Recovery == domain.NoRecovery && session.Preparation != nil && session.Preparation.State == domain.PreparationReady && session.StartPreparation == nil && (session.Execution == nil || session.Execution.CleanupVerified)
}
func codexAppsAvailableSelection(value *store.CodexAppsSnapshot, ids []string) bool {
	if len(ids) == 0 {
		return true
	}
	if value == nil || value.Configuration == nil || value.Inventory == nil || value.Inventory.Validate() != nil || value.Inventory.ConfigurationGeneration != value.Configuration.Generation || value.Inventory.AccountID != value.Configuration.AccountID {
		return false
	}
	for _, id := range ids {
		found := false
		for _, app := range value.Inventory.Apps {
			if app.ID == id && app.Discovered && app.Accessible && app.Installed {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

type codexAppsMutationIdentity struct {
	Actor                            domain.Principal
	Session, Account, Generation     domain.ID
	SessionRevision, AccountRevision uint64
	AppIDs                           []string
	Action                           domain.CodexAppsAction
}

func codexAppsMutation(ctx context.Context, m *pb.Mutation, account, generation string, accountRevision uint64, ids []string, action domain.CodexAppsAction) (codexAppsMutationIdentity, error) {
	value := codexAppsMutationIdentity{Session: domain.ID(m.GetId()), Account: domain.ID(account), Generation: domain.ID(generation), SessionRevision: m.GetExpectedRevision(), AccountRevision: accountRevision, AppIDs: slices.Clone(ids), Action: action}
	if value.AppIDs == nil {
		value.AppIDs = []string{}
	}
	if err := compactionActor(ctx); err != nil {
		return value, err
	}
	if err := validateSessionMutation(m); err != nil {
		return value, err
	}
	if err := value.Account.Validate(); err != nil {
		return value, err
	}
	if value.Generation != "" {
		if err := value.Generation.Validate(); err != nil {
			return value, err
		}
	}
	value.Actor, _ = domain.PrincipalFrom(ctx)
	return value, nil
}
func codexAppsCurrent(tx *store.Tx, identity codexAppsMutationIdentity) (store.Record, domain.Session, *store.CodexAppsSnapshot, error) {
	sr, session, err := sessionRecord(tx, identity.Session)
	if err != nil {
		return sr, session, nil, err
	}
	if sr.Revision != identity.SessionRevision {
		return sr, session, nil, codexAppsUnavailable()
	}
	value, err := tx.CodexAppsSnapshot(sr.ID)
	if err != nil {
		return sr, session, value, err
	}
	if value != nil && value.Operation != nil && slices.Contains([]domain.CodexAppsState{domain.CodexAppsQueued, domain.CodexAppsClaimed, domain.CodexAppsUncertain}, value.Operation.State) {
		return sr, session, value, codexAppsUnavailable()
	}
	if value == nil || value.Configuration == nil {
		if identity.Generation != "" {
			return sr, session, value, codexAppsUnavailable()
		}
	} else if value.Configuration.Generation != identity.Generation || value.Configuration.AccountID != identity.Account {
		return sr, session, value, codexAppsUnavailable()
	}
	return sr, session, value, nil
}
func (s *Service) SelectCodexApps(ctx context.Context, req *connect.Request[pb.SelectCodexAppsRequest]) (*connect.Response[pb.CodexAppsResponse], error) {
	corr := req.Header().Get(rpc.CorrelationHeader)
	m := req.Msg.Mutation
	identity, err := codexAppsMutation(ctx, m, req.Msg.AccountId, req.Msg.ConfigurationGeneration, req.Msg.AccountRevision, req.Msg.AppIds, "")
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	selection := domain.CodexAppConfiguration{Version: 1, SessionID: identity.Session, AccountID: identity.Account, Generation: domain.ID(m.RequestId), AppIDs: identity.AppIDs}
	if err := selection.Validate(); err != nil {
		return nil, rpc.Error(err, corr)
	}
	result, err := s.Store.Mutate(ctx, selection.Generation, "codex-apps.select", identity, func(tx *store.Tx) (any, error) {
		sr, session, value, err := codexAppsCurrent(tx, identity)
		if err != nil {
			return nil, err
		}
		if !codexAppsIdle(session) {
			return nil, codexAppsUnavailable()
		}
		if _, _, err := codexAppsProfile(tx, sr, session, identity.Account, identity.AccountRevision); err != nil {
			return nil, err
		}
		if !codexAppsAvailableSelection(value, selection.AppIDs) {
			return nil, codexAppsUnavailable()
		}
		revision := uint64(0)
		if value != nil {
			revision = value.Revision
		} else {
			value = &store.CodexAppsSnapshot{}
		}
		accountRecord, err := tx.Get(domain.AccountKind, identity.Account)
		if err != nil {
			return nil, err
		}
		account, err := store.Decode[domain.Account](accountRecord)
		if err != nil || account.Subscription == nil || account.Connection == nil {
			return nil, codexAppsUnavailable()
		}
		if value.Configuration != nil && (value.AccountGeneration != account.Subscription.Generation || value.ConnectionID != account.Connection.ID) {
			return nil, codexAppsUnavailable()
		}
		value.AccountGeneration, value.ConnectionID = account.Subscription.Generation, account.Connection.ID
		value.Configuration = &selection
		value.Inventory = nil
		if err := tx.PutCodexAppsSnapshot(sr.ID, revision, *value); err != nil {
			return nil, err
		}
		if _, err := tx.Put(sr.Kind, sr.ID, sr.Revision, sr.SessionID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return struct{ Session domain.ID }{sr.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	value, err := s.readCodexApps(ctx, identity.Session, result.Replayed)
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	s.logger.InfoContext(ctx, "codex_apps_selection_accepted", "session_id", identity.Session, "request_id", m.RequestId, "replayed", result.Replayed)
	response := connect.NewResponse(value)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) InspectCodexApps(ctx context.Context, req *connect.Request[pb.InspectCodexAppsRequest]) (*connect.Response[pb.CodexAppsResponse], error) {
	identity, err := codexAppsMutation(ctx, req.Msg.Mutation, req.Msg.AccountId, req.Msg.ConfigurationGeneration, req.Msg.AccountRevision, nil, domain.CodexAppsInspect)
	return s.queueCodexApps(ctx, req.Header(), req.Msg.Mutation, identity, err)
}
func (s *Service) RevokeCodexApps(ctx context.Context, req *connect.Request[pb.RevokeCodexAppsRequest]) (*connect.Response[pb.CodexAppsResponse], error) {
	identity, err := codexAppsMutation(ctx, req.Msg.Mutation, req.Msg.AccountId, req.Msg.ConfigurationGeneration, 0, req.Msg.AppIds, domain.CodexAppsRevoke)
	return s.queueCodexApps(ctx, req.Header(), req.Msg.Mutation, identity, err)
}

func (s *Service) codexAppsLiveSource(tx *store.Tx, sr store.Record, session domain.Session, configuration domain.CodexAppConfiguration, positive bool) (store.Record, domain.Job, domain.ExecutionJobInput, error) {
	var input domain.ExecutionJobInput
	if session.InitialExecution == nil || session.IsSidechat() || session.Fork != nil || session.Execution == nil || session.ActiveExecutionID == "" || session.Execution.ExecutionID != session.ActiveExecutionID || session.ExecutionSelection().AccountID != configuration.AccountID || configuration.SessionID != sr.ID {
		return store.Record{}, domain.Job{}, input, codexAppsUnavailable()
	}
	if err := tx.RequireNoSessionFork(sr.ID); err != nil {
		return store.Record{}, domain.Job{}, input, err
	}
	record, err := tx.SessionExecutionJob(sr.ID, session.ActiveExecutionID)
	if err != nil {
		return record, domain.Job{}, input, err
	}
	job, err := store.Decode[domain.Job](record)
	if err != nil {
		return record, job, input, err
	}
	if record.SessionID != sr.ID || job.Type != domain.ExecuteSessionJob || job.State != domain.JobClaimed || job.MachineID != session.MachineID || job.AssignedDeviceID.Validate() != nil || job.InstanceID.Validate() != nil || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || !session.OwnsExecution(input) || input.CodexApps == nil || input.Configuration.Harness != domain.Codex || !input.Configuration.Subscription || input.Configuration.SubscriptionService != domain.SubscriptionChatGPT || input.Configuration.SidechatPolicy != "" || input.AccountID != configuration.AccountID {
		return record, job, input, codexAppsUnavailable()
	}
	metadata, err := tx.CodexAppsSnapshot(sr.ID)
	if err != nil || metadata == nil || metadata.Configuration == nil {
		return record, job, input, codexAppsUnavailable()
	}
	current := metadata.Configuration
	if current.Generation != configuration.Generation || current.AccountID != input.CodexApps.AccountID || !slices.Equal(current.AppIDs, configuration.AppIDs) || !(input.CodexApps.Generation == current.Generation && slices.Equal(input.CodexApps.AppIDs, current.AppIDs) || input.CodexApps.RemovalOnly(*current)) {
		return record, job, input, codexAppsUnavailable()
	}
	if err := currentInstance(tx, job.MachineID, job.InstanceID); err != nil {
		return record, job, input, err
	}
	_, seen, err := tx.WorkerInstance(job.MachineID)
	if err != nil || time.Since(seen) > domain.WorkerConnectionTimeout || seen.After(time.Now().UTC().Add(time.Second)) {
		return record, job, input, codexAppsUnavailable()
	}
	if _, err := s.primaryWorkspaceStream(job.MachineID, job.InstanceID); err != nil {
		return record, job, input, err
	}
	deviceRecord, err := tx.Get(domain.DeviceKind, job.AssignedDeviceID)
	if err != nil {
		return record, job, input, err
	}
	device, err := store.Decode[domain.Device](deviceRecord)
	if err != nil || device.Revoked || device.Type != domain.WorkerDevice || device.MachineID != job.MachineID {
		return record, job, input, codexAppsUnavailable()
	}
	accountRecord, err := tx.Get(domain.AccountKind, input.AccountID)
	if err != nil {
		return record, job, input, err
	}
	account, err := store.Decode[domain.Account](accountRecord)
	if err != nil || account.Type != domain.SubscriptionAccount || account.SubscriptionService != domain.SubscriptionChatGPT || account.Subscription == nil || account.Subscription.Lease == nil {
		return record, job, input, codexAppsUnavailable()
	}
	state, lease := account.Subscription, account.Subscription.Lease
	if metadata.AccountGeneration != state.Generation || metadata.ConnectionID != input.ConnectionID || lease.Generation != metadata.AccountGeneration {
		return record, job, input, codexAppsUnavailable()
	}
	if lease.Action != domain.SubscriptionExecute || lease.OperationID != record.ID || lease.MachineID != job.MachineID || lease.InstanceID != job.InstanceID || lease.DeviceID != job.AssignedDeviceID || lease.Epoch != s.subscriptionServerEpoch() || lease.Generation != state.Generation {
		return record, job, input, codexAppsUnavailable()
	}
	if positive {
		if !account.Enabled || !quotaAccountReady(account) || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery {
			return record, job, input, codexAppsUnavailable()
		}
		_, machine, err := activeMachine(tx, job.MachineID)
		if err != nil {
			return record, job, input, err
		}
		if !slices.Contains(machine.WorkerCapabilities, domain.CodexAppsV1) {
			return record, job, input, codexAppsUnavailable()
		}
		if err := checkedExecutionSource(tx, sr, session, machine, input); err != nil {
			return record, job, input, err
		}
	}
	if domain.NativeIdentity(session.Execution.NativeThreadID).Validate(domain.Codex, domain.NativeThreadIdentity) != nil {
		return record, job, input, codexAppsUnavailable()
	}
	return record, job, input, nil
}
func (s *Service) queueCodexApps(ctx context.Context, header http.Header, m *pb.Mutation, identity codexAppsMutationIdentity, prior error) (*connect.Response[pb.CodexAppsResponse], error) {
	corr := header.Get(rpc.CorrelationHeader)
	if prior != nil {
		return nil, rpc.Error(prior, corr)
	}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "codex-apps."+string(identity.Action), identity, func(tx *store.Tx) (any, error) {
		sr, session, value, err := codexAppsCurrent(tx, identity)
		if err != nil {
			return nil, err
		}
		if value == nil || value.Configuration == nil {
			return nil, codexAppsUnavailable()
		}
		original := value.Configuration.Clone()
		if identity.Action == domain.CodexAppsInspect {
			if _, _, err := codexAppsProfile(tx, sr, session, identity.Account, identity.AccountRevision); err != nil {
				return nil, err
			}
		}
		jobRecord, job, _, err := s.codexAppsLiveSource(tx, sr, session, original, identity.Action == domain.CodexAppsInspect)
		if err != nil {
			return nil, err
		}
		operation := domain.CodexAppsOperation{Version: 1, ID: domain.ID(m.RequestId), Revision: 1, RequestID: domain.ID(m.RequestId), ActorID: identity.Actor.DeviceID, Action: identity.Action, State: domain.CodexAppsQueued, Original: original, ExecutionID: session.ActiveExecutionID, ExecutionJobID: jobRecord.ID, MachineID: job.MachineID, InstanceID: job.InstanceID, NativeThreadID: domain.NativeIdentity(session.Execution.NativeThreadID)}
		if identity.Action == domain.CodexAppsRevoke {
			remove := domain.CodexAppConfiguration{Version: 1, SessionID: sr.ID, AccountID: identity.Account, Generation: domain.ID(m.RequestId), AppIDs: identity.AppIDs}
			if err := remove.Validate(); err != nil {
				return nil, err
			}
			if len(remove.AppIDs) == 0 {
				return nil, codexAppsUnavailable()
			}
			for _, id := range remove.AppIDs {
				if !slices.Contains(original.AppIDs, id) {
					return nil, codexAppsUnavailable()
				}
			}
			next := original.Clone()
			next.Generation = domain.ID(m.RequestId)
			next.AppIDs = []string{}
			for _, id := range original.AppIDs {
				if !slices.Contains(remove.AppIDs, id) {
					next.AppIDs = append(next.AppIDs, id)
				}
			}
			operation.Next = &next
		}
		if err := operation.Validate(); err != nil {
			return nil, err
		}
		value.Operation = &operation
		if err := tx.PutCodexAppsSnapshot(sr.ID, value.Revision, *value); err != nil {
			return nil, err
		}
		if _, err := tx.Put(sr.Kind, sr.ID, sr.Revision, sr.SessionID, sr.ProjectID, session); err != nil {
			return nil, err
		}
		return struct{ Session domain.ID }{sr.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	value, err := s.readCodexApps(ctx, identity.Session, result.Replayed)
	if err != nil {
		return nil, rpc.Error(err, corr)
	}
	s.logger.InfoContext(ctx, "codex_apps_control_accepted", "session_id", identity.Session, "request_id", m.RequestId, "action", identity.Action, "replayed", result.Replayed)
	response := connect.NewResponse(value)
	rpc.CopyCorrelation(response, header)
	return response, nil
}
