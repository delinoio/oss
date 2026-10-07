// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"slices"
	"strconv"
	"time"
)

func nativeModelClient(ctx context.Context) error {
	_, ok := domain.PrincipalFrom(ctx)
	if !ok {
		return domain.Fail(domain.PermissionDenied, "Server authentication is required.", "Use the server token or a registered device credential.")
	}
	return nil
}

func nativeModelAuthority(tx *store.Tx, scope domain.NativeModelScope) error {
	if scope.Validate() != nil {
		return domain.NativeModelFailure()
	}
	if err := tx.Authorize(); err != nil {
		return err
	}

	record, machine, err := activeMachine(tx, scope.MachineID)
	if err != nil {
		return err
	}
	if record.Revision != scope.MachineRevision || machine.DiscoveryRevision != scope.InstallationGeneration || !slices.Contains(machine.WorkerCapabilities, domain.NativeModelsV1) {
		return domain.Fail(domain.Conflict, "The selected installation or Runner Device changed.", "Inspect the original observation and select a current installation.")
	}
	found := false
	for _, installation := range machine.Installations {
		if installation.Harness == domain.Codex && installation.State == domain.InstallationDetected && installation.Version == scope.NativeVersion && installation.ResolvedPath == scope.Executable && installation.ExecutableSHA256 == scope.ExecutableSHA256 {
			found = true
		}
	}
	if !found {
		return domain.NativeModelFailure()
	}
	accountRecord, err := tx.Get(domain.AccountKind, scope.AccountID)
	if err != nil {
		return err
	}
	account, err := store.Decode[domain.Account](accountRecord)
	if err != nil {
		return err
	}
	if account.Type == domain.SubscriptionAccount {
		return domain.Fail(domain.Unsupported, "Managed subscription model observation is unavailable.", "Wait for the isolated managed-account lifecycle; host login cannot be used.")
	}
	if accountRecord.Revision != scope.AccountRevision || !account.Enabled || account.Connection == nil || account.Connection.ID != scope.ConnectionID || account.Removal != nil || account.ProviderID != scope.ProviderID {
		return domain.Fail(domain.Conflict, "The selected account or connection changed.", "Inspect the original observation and select the current connected account.")
	}
	return nil
}

func nativeModelJob(tx *store.Tx, id domain.ID) (store.Record, domain.Job, domain.NativeModelScope, error) {
	record, err := tx.Get(domain.JobKind, id)
	if err != nil {
		return record, domain.Job{}, domain.NativeModelScope{}, err
	}
	job, err := store.Decode[domain.Job](record)
	var scope domain.NativeModelScope
	if err != nil || job.Type != domain.NativeModelsJob || domain.Decode(job.Input, &scope) != nil || scope.Validate() != nil {
		return record, job, scope, domain.NativeModelFailure()
	}
	return record, job, scope, nil
}

func (s *Service) DiscoverNativeModels(ctx context.Context, req *connect.Request[pb.DiscoverNativeModelsRequest]) (*connect.Response[pb.DiscoverNativeModelsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := nativeModelClient(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	if meta == nil || meta.ExpectedRevision == 0 || req.Msg.AccountRevision == 0 {
		return nil, rpc.Error(domain.Fail(domain.MissingInput, "Select a Runner Device and connected account with their current revisions.", "Read both selections before observing native models."), correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	input := struct {
		Machine         domain.ID
		Revision        uint64
		Account         domain.ID
		AccountRevision uint64
		Hidden          bool
		Actor           domain.Principal
	}{domain.ID(meta.Id), meta.ExpectedRevision, domain.ID(req.Msg.AccountId), req.Msg.AccountRevision, req.Msg.IncludeHidden, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "native-models.discover", input, func(tx *store.Tx) (any, error) {
		record, machine, err := activeMachine(tx, input.Machine)
		if err != nil {
			return nil, err
		}
		accountRecord, err := tx.Get(domain.AccountKind, input.Account)
		if err != nil {
			return nil, err
		}
		account, err := store.Decode[domain.Account](accountRecord)
		if err != nil {
			return nil, err
		}
		if account.Type == domain.SubscriptionAccount {
			return nil, domain.Fail(domain.Unsupported, "Managed subscription model observation is unavailable.", "Wait for the isolated managed-account lifecycle; host login cannot be used.")
		}
		if account.Connection == nil {
			return nil, domain.Fail(domain.Conflict, "The selected account is disconnected.", "Select a connected account before observation.")
		}
		scope := domain.NativeModelScope{Version: 1, MachineID: record.ID, MachineRevision: input.Revision, InstallationGeneration: machine.DiscoveryRevision, AccountID: input.Account, AccountRevision: input.AccountRevision, ConnectionID: account.Connection.ID, ProviderID: account.ProviderID, Actor: actor, IncludeHidden: input.Hidden}
		for _, installation := range machine.Installations {
			if installation.Harness == domain.Codex {
				scope.Executable = installation.ResolvedPath
				scope.ExecutableSHA256 = installation.ExecutableSHA256
				scope.NativeVersion = installation.Version
			}
		}
		if err := nativeModelAuthority(tx, scope); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(scope)
		job, err := tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.NativeModelsJob, State: domain.JobQueued, MachineID: record.ID, ParentID: input.Account, Input: raw, AcceptedAt: time.Now().UTC()})
		return struct {
			ID domain.ID `json:"id"`
		}{job.ID}, err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt struct {
		ID domain.ID `json:"id"`
	}
	if domain.Decode(result.Data, &receipt) != nil {
		return nil, rpc.Error(domain.NativeModelFailure(), correlation)
	}
	s.logger.InfoContext(ctx, "native model observation accepted", "request_id", meta.RequestId, "job_id", receipt.ID, "machine_id", input.Machine, "account_id", input.Account, "phase", "accept", "replayed", result.Replayed)
	change, err := s.nativeModelChange(ctx, receipt.ID, result.Replayed, correlation)
	if err != nil {
		return nil, err
	}
	response := connect.NewResponse(&pb.DiscoverNativeModelsResponse{Job: change.Msg.Job, LastSuccess: change.Msg.LastSuccess, Replayed: change.Msg.Replayed})
	response.Header().Set(rpc.CorrelationHeader, correlation)
	return response, nil
}

func (s *Service) nativeModelChange(ctx context.Context, id domain.ID, replayed bool, correlation string) (*connect.Response[pb.NativeModelChange], error) {
	message := &pb.NativeModelChange{Replayed: replayed}
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		record, _, scope, err := nativeModelJob(tx, id)
		if err != nil {
			return err
		}
		message.Job = rpc.Resource(record)
		last, err := tx.LastNativeModelSuccess(scope)
		if err != nil {
			return err
		}
		if last.ID != "" {
			message.LastSuccess = rpc.Resource(last)
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(message)
	response.Header().Set(rpc.CorrelationHeader, correlation)
	return response, nil
}

func (s *Service) GetNativeModelObservation(ctx context.Context, req *connect.Request[pb.GetNativeModelObservationRequest]) (*connect.Response[pb.GetNativeModelObservationResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := nativeModelClient(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	change, err := s.nativeModelChange(ctx, domain.ID(req.Msg.JobId), false, correlation)
	if err != nil {
		return nil, err
	}
	response := connect.NewResponse(&pb.GetNativeModelObservationResponse{Job: change.Msg.Job, LastSuccess: change.Msg.LastSuccess, Replayed: change.Msg.Replayed})
	response.Header().Set(rpc.CorrelationHeader, correlation)
	return response, nil
}

func (s *Service) CancelNativeModelDiscovery(ctx context.Context, req *connect.Request[pb.CancelNativeModelDiscoveryRequest]) (*connect.Response[pb.CancelNativeModelDiscoveryResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := nativeModelClient(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	if meta == nil || meta.ExpectedRevision == 0 {
		return nil, rpc.Error(domain.NativeModelFailure(), correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	input := struct {
		ID       domain.ID
		Revision uint64
		Actor    domain.Principal
	}{domain.ID(meta.Id), meta.ExpectedRevision, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "native-models.cancel", input, func(tx *store.Tx) (any, error) {
		record, job, _, err := nativeModelJob(tx, input.ID)
		if err != nil {
			return nil, err
		}
		if record.Revision != input.Revision {
			return nil, domain.Fail(domain.Conflict, "The observation revision changed.", "Read its current status before cancellation.")
		}
		if job.State == domain.JobQueued {
			now := time.Now().UTC()
			job.State = domain.JobCanceled
			job.FinishedAt = &now
			job.Problem = domain.Fail(domain.Canceled, "The native model observation was canceled.", "Create a new explicit observation if needed.")
			if _, err := tx.PutJob(record.ID, record.Revision, "", "", job); err != nil {
				return nil, err
			}
		} else if job.State == domain.JobClaimed {
			if err := tx.RequestJobCancellation(record.ID); err != nil {
				return nil, err
			}
		}
		return struct {
			ID domain.ID `json:"id"`
		}{record.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	change, err := s.nativeModelChange(ctx, input.ID, result.Replayed, correlation)
	if err != nil {
		return nil, err
	}
	response := connect.NewResponse(&pb.CancelNativeModelDiscoveryResponse{Job: change.Msg.Job, LastSuccess: change.Msg.LastSuccess, Replayed: change.Msg.Replayed})
	response.Header().Set(rpc.CorrelationHeader, correlation)
	return response, nil
}

func (s *Service) ListNativeModels(ctx context.Context, req *connect.Request[pb.ListNativeModelsRequest]) (*connect.Response[pb.ListNativeModelsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := nativeModelClient(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	limit := int(req.Msg.PageSize)
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return nil, rpc.Error(domain.NativeModelFailure(), correlation)
	}
	id := domain.ID(req.Msg.JobId)
	scope := "native-model-observation-v1:" + string(id)
	offset := 0
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		offset, err = strconv.Atoi(string(cursor.After))
		if err != nil || offset < 0 || offset > domain.MaxNativeModels {
			return nil, rpc.Error(domain.NativeModelFailure(), correlation)
		}
	}
	message := &pb.ListNativeModelsResponse{}
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		record, job, selected, err := nativeModelJob(tx, id)
		if err != nil {
			return err
		}
		var observation domain.NativeModelObservation
		if job.State != domain.JobSucceeded || domain.Decode(job.Output, &observation) != nil || observation.Validate(selected.IncludeHidden) != nil {
			return domain.Fail(domain.Conflict, "The operation has no complete successful observation.", "Inspect its status or select the retained last successful observation.")
		}
		if offset > len(observation.Models) {
			return domain.NativeModelFailure()
		}
		end := min(offset+limit, len(observation.Models))
		message.Job = rpc.Resource(record)
		message.ModelsJson, _ = json.Marshal(observation.Models[offset:end])
		if end < len(observation.Models) {
			message.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: domain.ID(strconv.Itoa(end))})
		}
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(message)
	response.Header().Set(rpc.CorrelationHeader, correlation)
	return response, nil
}

func finishNativeModels(tx *store.Tx, record store.Record, job domain.Job, raw []byte, problem *domain.Error) (any, error) {
	var scope domain.NativeModelScope
	if domain.Decode(job.Input, &scope) != nil {
		return nil, domain.NativeModelFailure()
	}
	if problem == nil {
		if err := nativeModelAuthority(tx, scope); err != nil {
			problem = domain.SafeError(err)
		}
		canceled, err := tx.JobCancellationRequested(record.ID)
		if err != nil {
			return nil, err
		}
		if canceled {
			problem = domain.Fail(domain.Canceled, "The native model observation was canceled.", "Create a new explicit observation if needed.")
		}
		var observation domain.NativeModelObservation
		if problem == nil && (domain.Decode(raw, &observation) != nil || observation.Validate(scope.IncludeHidden) != nil || observation.ObservedAt.Before(job.AcceptedAt) || observation.ObservedAt.After(time.Now().UTC().Add(time.Minute))) {
			problem = domain.NativeModelFailure()
		}
		if problem == nil {
			job.Output, _ = json.Marshal(observation)
		}
	}
	now := time.Now().UTC()
	job.FinishedAt = &now
	job.State = domain.JobSucceeded
	if problem != nil {
		job.Output = nil
		job.Problem = problem
		job.State = domain.JobFailed
		if problem.Code == domain.Canceled {
			job.State = domain.JobCanceled
		}
		if problem.Code == domain.RecoveryRequired {
			job.State = domain.JobUncertain
		}
	}
	return tx.PutJob(record.ID, record.Revision, "", "", job)
}
