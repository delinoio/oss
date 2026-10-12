// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func nativeConfigurationTarget(tx *store.Tx, target domain.NativeConfigurationTarget) (store.Record, validatable, error) {
	record, err := tx.Get(target.EntityKind(), target.ID)
	if err != nil {
		return record, nil, err
	}
	if record.Revision != target.Revision {
		return record, nil, domain.NativeConfigurationChanged()
	}
	value, err := configurationValue(record.Kind, record.Data, true)
	if err != nil {
		return record, nil, err
	}
	if agent, ok := value.(*domain.Agent); ok {
		if agent.Harness != target.Scope.Harness || agent.HarnessSettings == nil || target.RouteIndex == nil || int(*target.RouteIndex) >= len(agent.Routes) {
			return record, nil, domain.NativeConfigurationInvalid()
		}
		model := agent.Routes[*target.RouteIndex].Model
		if model == nil || target.Scope.Source == domain.HarnessAnySource || model.ProviderID != "" && (target.Scope.Source != domain.HarnessAPISource || target.Scope.ProviderID != model.ProviderID) || model.SubscriptionService != "" && (target.Scope.Source != domain.HarnessSubscriptionSource || target.Scope.SubscriptionService != model.SubscriptionService) {
			return record, nil, domain.NativeConfigurationInvalid()
		}
	}
	if err := validateHarnessDefaultRelationships(tx, &domain.HarnessDefaults{Version: 1, Entries: []domain.HarnessDefaultEntry{{Scope: target.Scope, Settings: domain.HarnessSettings{Version: 1}}}}); err != nil {
		return record, nil, err
	}
	return record, value, nil
}
func nativeConfigurationAuthority(tx *store.Tx, input domain.NativeConfigurationJobInput, serverID domain.ID) error {
	if input.Validate() != nil || input.ServerID != serverID {
		return domain.NativeConfigurationInvalid()
	}
	if err := authorizeConfigurationImport(tx, input.Actor); err != nil {
		return err
	}
	record, machine, err := activeMachine(tx, input.Preview.MachineID)
	if err != nil {
		return err
	}
	if record.Revision != input.Preview.MachineRevision || !slices.Contains(machine.WorkerCapabilities, domain.NativeConfigurationImportV1) {
		return domain.NativeConfigurationChanged()
	}
	device, err := tx.InstallationWorkerDevice(record.ID)
	if err != nil {
		return err
	}
	if device != input.WorkerDeviceID {
		return domain.NativeConfigurationChanged()
	}
	if err := currentInstance(tx, record.ID, input.InstanceID); err != nil {
		return err
	}
	_, _, err = nativeConfigurationTarget(tx, input.Preview.Target)
	return err
}
func nativeConfigurationJob(tx *store.Tx, id domain.ID) (store.Record, domain.Job, domain.NativeConfigurationJobInput, error) {
	record, err := tx.Get(domain.JobKind, id)
	if err != nil {
		return record, domain.Job{}, domain.NativeConfigurationJobInput{}, err
	}
	job, err := store.Decode[domain.Job](record)
	var input domain.NativeConfigurationJobInput
	if err != nil || !slices.Contains([]domain.JobType{domain.NativeConfigurationPreviewJob, domain.NativeConfigurationApplyJob}, job.Type) || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.Preview.MachineID != job.MachineID || (job.Type == domain.NativeConfigurationApplyJob) != (input.Selection != nil) {
		return record, job, input, domain.NativeConfigurationInvalid()
	}
	return record, job, input, nil
}
func (s *Service) PreviewNativeConfigurationImport(ctx context.Context, req *connect.Request[pb.PreviewNativeConfigurationImportRequest]) (*connect.Response[pb.PreviewNativeConfigurationImportResponse], error) {
	actor, err := configurationActor(ctx)
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var selection domain.NativeConfigurationPreviewSelection
	if domain.DecodeWithLimit(req.Msg.SelectionJson, &selection, domain.MaxNativeConfigurationSelectionBytes) != nil || selection.Validate() != nil {
		return nil, rpc.Error(domain.NativeConfigurationInvalid(), correlation)
	}
	request := struct {
		Actor     domain.Principal
		Selection domain.NativeConfigurationPreviewSelection
	}{actor, selection}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "native-configuration.preview", request, func(tx *store.Tx) (any, error) {
		instance, seen, err := tx.WorkerInstance(selection.MachineID)
		if err != nil {
			return nil, err
		}
		if instance == "" || time.Since(seen) > domain.WorkerConnectionTimeout {
			return nil, domain.Fail(domain.Unavailable, "The selected Worker is unavailable.", "Reconnect the selected Worker before inspecting its configuration.")
		}
		device, err := tx.InstallationWorkerDevice(selection.MachineID)
		if err != nil {
			return nil, err
		}
		input := domain.NativeConfigurationJobInput{Version: 1, ServerID: s.Identity.ServerID, Actor: actor, WorkerDeviceID: device, InstanceID: instance, Preview: selection}
		if err := nativeConfigurationAuthority(tx, input, s.Identity.ServerID); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(input)
		job, err := tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.NativeConfigurationPreviewJob, State: domain.JobQueued, MachineID: selection.MachineID, Input: raw, AcceptedAt: time.Now().UTC()})
		if err != nil {
			return nil, err
		}
		if err = tx.PutNativeConfigurationOriginal(domain.ID(req.Msg.RequestId), store.NativeConfigurationOriginal{Actor: actor, JobID: job.ID}); err != nil {
			return nil, err
		}
		return struct {
			ID domain.ID `json:"id"`
		}{job.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	job, err := s.nativeConfigurationReceipt(ctx, result)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "native configuration preview accepted", "request_id", req.Msg.RequestId, "job_id", job.Id, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.PreviewNativeConfigurationImportResponse{RequestId: req.Msg.RequestId, Replayed: result.Replayed, Job: job})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) nativeConfigurationReceipt(ctx context.Context, result store.Result) (*pb.Resource, error) {
	var receipt struct {
		ID domain.ID `json:"id"`
	}
	if domain.Decode(result.Data, &receipt) != nil {
		return nil, domain.NativeConfigurationInvalid()
	}
	record, err := s.Store.Get(ctx, domain.JobKind, receipt.ID)
	if err != nil {
		return nil, err
	}
	return rpc.Resource(record), nil
}
func (s *Service) ApplyNativeConfigurationImport(ctx context.Context, req *connect.Request[pb.ApplyNativeConfigurationImportRequest]) (*connect.Response[pb.ApplyNativeConfigurationImportResponse], error) {
	actor, err := configurationActor(ctx)
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var plan domain.NativeConfigurationPlan
	if domain.DecodeWithLimit(req.Msg.PlanJson, &plan, domain.MaxNativeConfigurationSelectionBytes) != nil || plan.Version != 1 || plan.PreviewJobID.Validate() != nil || plan.Selection.Validate() != nil {
		return nil, rpc.Error(domain.NativeConfigurationInvalid(), correlation)
	}
	selection := plan.Selection
	request := struct {
		Actor     domain.Principal
		PreviewID domain.ID
		Selection domain.NativeConfigurationApplySelection
		Token     string
	}{actor, plan.PreviewJobID, selection, plan.Token}
	// Mutate checks an exact durable receipt before any freshness check. A lost
	// response retains the original accepted job even after its preview expires.
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "native-configuration.apply", request, func(tx *store.Tx) (any, error) {
		if _, e := s.Identity.DecodeCursor(plan.Token, nativeConfigurationPlanScope(actor, plan)); e != nil {
			return nil, domain.NativeConfigurationChanged()
		}
		original, found, e := tx.NativeConfigurationOriginal(domain.ID(req.Msg.RequestId))
		if e != nil {
			return nil, e
		}
		if found && (original.Actor != actor || original.JobID == "") {
			return nil, domain.NativeConfigurationChanged()
		}
		_, preview, input, err := nativeConfigurationJob(tx, request.PreviewID)
		if err != nil {
			return nil, err
		}
		if preview.Type != domain.NativeConfigurationPreviewJob || preview.State != domain.JobSucceeded || input.Actor != actor || input.ServerID != s.Identity.ServerID || time.Since(preview.AcceptedAt) > 24*time.Hour {
			return nil, domain.NativeConfigurationChanged()
		}
		if input.InstanceID != preview.InstanceID || input.WorkerDeviceID != preview.AssignedDeviceID || domain.HarnessEvidenceDigest(preview.Output) != selection.PreviewDigest {
			return nil, domain.NativeConfigurationChanged()
		}
		var observation domain.NativeConfigurationObservation
		if domain.DecodeWithLimit(preview.Output, &observation, domain.MaxNativeConfigurationObservationBytes) != nil || observation.Validate(input.Preview) != nil {
			return nil, domain.NativeConfigurationInvalid()
		}
		if err := validateNativeConfigurationSelection(input.Preview, observation, selection); err != nil {
			return nil, err
		}
		input.PreviewJobID = request.PreviewID
		input.Selection = &selection
		if err := nativeConfigurationAuthority(tx, input, s.Identity.ServerID); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(input)
		job, err := tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.NativeConfigurationApplyJob, State: domain.JobQueued, MachineID: input.Preview.MachineID, ParentID: request.PreviewID, Input: raw, AcceptedAt: time.Now().UTC()})
		if err != nil {
			return nil, err
		}
		if err = tx.PutNativeConfigurationOriginal(domain.ID(req.Msg.RequestId), store.NativeConfigurationOriginal{Actor: actor, JobID: job.ID}); err != nil {
			return nil, err
		}
		return struct {
			ID domain.ID `json:"id"`
		}{job.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	job, err := s.nativeConfigurationReceipt(ctx, result)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "native configuration import accepted", "request_id", req.Msg.RequestId, "job_id", job.Id, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ApplyNativeConfigurationImportResponse{RequestId: req.Msg.RequestId, Replayed: result.Replayed, Job: job})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func validateNativeConfigurationSelection(preview domain.NativeConfigurationPreviewSelection, observation domain.NativeConfigurationObservation, selection domain.NativeConfigurationApplySelection) error {
	if selection.Validate() != nil || selection.AttachInstructions && preview.Target.Kind != domain.NativeConfigurationAgentWorker {
		return domain.NativeConfigurationInvalid()
	}
	found := 0
	selectedSettings := map[string]bool{}
	for _, entry := range observation.Entries {
		if !slices.Contains(selection.Entries, entry.ID) {
			continue
		}
		found++
		if entry.Kind == domain.NativeConfigurationSetting {
			if selectedSettings[entry.Name] {
				return domain.NativeConfigurationInvalid()
			}
			selectedSettings[entry.Name] = true
		}
		if !slices.Contains([]domain.NativeConfigurationEntryKind{domain.NativeConfigurationSetting, domain.NativeConfigurationInstruction, domain.NativeConfigurationInertExtension}, entry.Kind) {
			return domain.NativeConfigurationInvalid()
		}
	}
	if found != len(selection.Entries) {
		return domain.NativeConfigurationInvalid()
	}
	return nil
}

type nativeConfigurationOverlay struct {
	*store.Tx
	staged map[domain.ID]store.Record
}

func (v nativeConfigurationOverlay) Get(kind domain.Kind, id domain.ID) (store.Record, error) {
	if record, ok := v.staged[id]; ok {
		if record.Kind != kind {
			return store.Record{}, domain.NativeConfigurationInvalid()
		}
		return record, nil
	}
	return v.Tx.Get(kind, id)
}

type nativeConfigurationResource struct {
	EntryID  string      `json:"entry_id,omitempty"`
	ID       domain.ID   `json:"id"`
	Kind     domain.Kind `json:"kind"`
	Revision uint64      `json:"revision,string"`
}

func stageNativeConfiguration(tx *store.Tx, input domain.NativeConfigurationJobInput, observation domain.NativeConfigurationObservation) ([]nativeConfigurationResource, func() error, error) {
	target := input.Preview.Target
	record, value, err := nativeConfigurationTarget(tx, target)
	if err != nil {
		return nil, nil, err
	}
	settings := domain.HarnessSettings{Version: 1}
	packages := []struct {
		entry domain.NativeConfigurationEntry
		id    domain.ID
		value domain.Template
	}{}
	for order, entry := range observation.Entries {
		if !slices.Contains(input.Selection.Entries, entry.ID) {
			continue
		}
		if entry.Kind == domain.NativeConfigurationSetting {
			selected, err := domain.NativeConfigurationSettingValue(entry.Name, entry.Value)
			if err != nil {
				return nil, nil, err
			}
			domain.MergeNativeConfigurationSettings(&settings, selected)
		} else {
			packageValue := domain.Template{Name: fmt.Sprintf("Imported %s %s %d", input.Preview.Harness, entry.Name, order+1), Contents: entry.Contents, NativeImport: &domain.NativeImportedPackage{Version: 1, Harness: input.Preview.Harness, Kind: entry.Kind, SourceDigest: entry.SourceDigest, Category: domain.NativeImportedCategory(entry.Name), SourceScope: input.Preview.Scopes[entry.ScopeIndex].Kind, SourceRoot: observation.Roots[entry.ScopeIndex], SourceFile: entry.File, Profile: entry.Profile, Order: uint32(order), Files: entry.Files}}
			if err := packageValue.Validate(); err != nil {
				return nil, nil, err
			}
			packages = append(packages, struct {
				entry domain.NativeConfigurationEntry
				id    domain.ID
				value domain.Template
			}{entry, domain.NewID(), packageValue})
		}
	}
	changed := settings.Model != nil || settings.Effort != nil || settings.Permission != nil || settings.ApprovalPolicy != nil || settings.ServiceTier != nil
	switch v := value.(type) {
	case *domain.Agent:
		if settings.Model != nil {
			route := &v.Routes[*target.RouteIndex]
			route.ModelSelection = settings.Model
			route.Model = &domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: route.Model.ProviderID, SubscriptionService: route.Model.SubscriptionService, NativeID: *settings.Model.Value}, MetadataSource: domain.UserDeclared}
			settings.Model = nil
		}
		domain.MergeNativeConfigurationSettings(v.HarnessSettings, settings)
		if input.Selection.AttachInstructions {
			for _, pkg := range packages {
				if pkg.entry.Kind == domain.NativeConfigurationInstruction {
					v.Templates = append(v.Templates, pkg.id)
					changed = true
				}
			}
		}
	case *domain.Project:
		if changed {
			mergeNativeConfigurationDefaults(&v.HarnessDefaults, target.Scope, settings)
		}
	case *domain.Settings:
		if changed {
			mergeNativeConfigurationDefaults(&v.HarnessDefaults, target.Scope, settings)
		}
	default:
		return nil, nil, domain.NativeConfigurationInvalid()
	}
	if err := value.Validate(); err != nil {
		return nil, nil, err
	}
	staged := map[domain.ID]store.Record{}
	for _, pkg := range packages {
		raw, _ := json.Marshal(pkg.value)
		staged[pkg.id] = store.Record{ID: pkg.id, Kind: domain.TemplateKind, Data: raw}
	}
	overlay := nativeConfigurationOverlay{Tx: tx, staged: staged}
	if err := validateRelationships(overlay, record.Kind, record.ID, record.Revision, value); err != nil {
		return nil, nil, err
	}
	if err := validateNewProviderSelections(tx, ConfigurationMutation{Kind: record.Kind, ID: record.ID, ExpectedRevision: record.Revision}, record.ID, value); err != nil {
		return nil, nil, err
	}
	resources := []nativeConfigurationResource{}
	for _, pkg := range packages {
		resources = append(resources, nativeConfigurationResource{EntryID: pkg.entry.ID, ID: pkg.id, Kind: domain.TemplateKind, Revision: 1})
	}
	if changed {
		resources = append(resources, nativeConfigurationResource{ID: record.ID, Kind: record.Kind, Revision: record.Revision + 1})
	}
	publish := func() error {
		for _, pkg := range packages {
			if _, err := tx.Put(domain.TemplateKind, pkg.id, 0, "", "", pkg.value); err != nil {
				return err
			}
		}
		if changed {
			_, err := tx.Put(record.Kind, record.ID, record.Revision, record.SessionID, record.ProjectID, value)
			return err
		}
		return nil
	}
	return resources, publish, nil
}
func mergeNativeConfigurationDefaults(defaults **domain.HarnessDefaults, scope domain.HarnessDefaultScope, settings domain.HarnessSettings) {
	if *defaults == nil {
		*defaults = &domain.HarnessDefaults{Version: 1, Entries: []domain.HarnessDefaultEntry{}}
	}
	for i := range (*defaults).Entries {
		entry := &(*defaults).Entries[i]
		if entry.Scope == scope {
			domain.MergeNativeConfigurationSettings(&entry.Settings, settings)
			return
		}
	}
	(*defaults).Entries = append((*defaults).Entries, domain.HarnessDefaultEntry{Scope: scope, Settings: settings})
}
func (s *Service) finishNativeConfiguration(tx *store.Tx, record store.Record, job domain.Job, expected uint64, raw []byte, problem *domain.Error) (any, error) {
	var input domain.NativeConfigurationJobInput
	if record.Revision != expected || domain.Decode(job.Input, &input) != nil || input.Validate() != nil {
		return nil, domain.NativeConfigurationInvalid()
	}
	if problem == nil {
		if err := nativeConfigurationAuthority(tx, input, s.Identity.ServerID); err != nil {
			problem = domain.SafeError(err)
		}
		canceled, err := tx.JobCancellationRequested(record.ID)
		if err != nil {
			return nil, err
		}
		if canceled {
			problem = domain.Fail(domain.Canceled, "The native configuration import was canceled.", "Create a new explicit preview if needed.")
		}
		var observation domain.NativeConfigurationObservation
		if problem == nil && (domain.DecodeWithLimit(raw, &observation, domain.MaxNativeConfigurationObservationBytes) != nil || observation.Validate(input.Preview) != nil) {
			problem = domain.NativeConfigurationInvalid()
		}
		if problem == nil {
			canonical, _ := json.Marshal(observation)
			if input.Selection == nil {
				job.Output = canonical
				job.NativeConfigurationDigest = domain.HarnessEvidenceDigest(canonical)
			} else {
				_, preview, original, err := nativeConfigurationJob(tx, input.PreviewJobID)
				if err != nil || preview.State != domain.JobSucceeded || preview.Type != domain.NativeConfigurationPreviewJob || original.Actor != input.Actor || original.InstanceID != input.InstanceID || original.WorkerDeviceID != input.WorkerDeviceID || domain.HarnessEvidenceDigest(preview.Output) != input.Selection.PreviewDigest || domain.HarnessEvidenceDigest(canonical) != input.Selection.PreviewDigest {
					problem = domain.NativeConfigurationChanged()
				} else if err := validateNativeConfigurationSelection(input.Preview, observation, *input.Selection); err != nil {
					problem = domain.SafeError(err)
				} else {
					resources, publish, err := stageNativeConfiguration(tx, input, observation)
					if err != nil {
						problem = domain.SafeError(err)
					} else {
						// Validation precedes every write. A storage failure rolls back the report,
						// packages, destination and receipt together, retaining original retry.
						if err := publish(); err != nil {
							return nil, err
						}
						for _, entry := range observation.Entries {
							if !slices.Contains(input.Selection.Entries, entry.ID) {
								continue
							}
							if entry.Kind == domain.NativeConfigurationSetting {
								job.NativeConfigurationSettingsCount++
							} else {
								job.NativeConfigurationPackageCount++
							}
						}
						job.Output, _ = json.Marshal(resources)
					}
				}
			}
		}
	}
	now := time.Now().UTC()
	job.State = domain.JobSucceeded
	job.FinishedAt = &now
	if problem != nil {
		job.State = domain.JobFailed
		job.Problem = domain.SafeError(problem)
		job.Output = nil
		if problem.Code == domain.Canceled {
			job.State = domain.JobCanceled
		}
	}
	return tx.PutJob(record.ID, record.Revision, record.SessionID, record.ProjectID, job)
}

func nativeConfigurationPlanScope(actor domain.Principal, plan domain.NativeConfigurationPlan) string {
	plan.Token = ""
	raw, _ := json.Marshal(struct {
		Actor domain.Principal
		Plan  domain.NativeConfigurationPlan
	}{actor, plan})
	return "native-configuration-import:" + domain.HarnessEvidenceDigest(raw)
}
func (s *Service) PrepareNativeConfigurationImport(ctx context.Context, req *connect.Request[pb.PrepareNativeConfigurationImportRequest]) (*connect.Response[pb.PrepareNativeConfigurationImportResponse], error) {
	actor, err := configurationActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	var selection domain.NativeConfigurationApplySelection
	if domain.DecodeWithLimit(req.Msg.SelectionJson, &selection, domain.MaxNativeConfigurationSelectionBytes) != nil || selection.Validate() != nil {
		return nil, rpc.Error(domain.NativeConfigurationInvalid(), "")
	}
	plan := domain.NativeConfigurationPlan{Version: 1, PreviewJobID: domain.ID(req.Msg.PreviewJobId), Selection: selection}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if e := tx.Authorize(); e != nil {
			return e
		}
		_, job, input, e := nativeConfigurationJob(tx, plan.PreviewJobID)
		if e != nil {
			return e
		}
		if job.Type != domain.NativeConfigurationPreviewJob || job.State != domain.JobSucceeded || input.Actor != actor || time.Since(job.AcceptedAt) > 24*time.Hour || domain.HarnessEvidenceDigest(job.Output) != selection.PreviewDigest {
			return domain.NativeConfigurationChanged()
		}
		if e = nativeConfigurationAuthority(tx, input, s.Identity.ServerID); e != nil {
			return e
		}
		var observation domain.NativeConfigurationObservation
		if domain.Decode(job.Output, &observation) != nil || observation.Validate(input.Preview) != nil {
			return domain.NativeConfigurationInvalid()
		}
		return validateNativeConfigurationSelection(input.Preview, observation, selection)
	})
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	plan.Token, err = s.Identity.EncodeCursor(security.Cursor{Scope: nativeConfigurationPlanScope(actor, plan)})
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	raw, _ := json.Marshal(plan)
	return connect.NewResponse(&pb.PrepareNativeConfigurationImportResponse{PlanJson: raw}), nil
}
func (s *Service) GetNativeConfigurationImport(ctx context.Context, req *connect.Request[pb.GetNativeConfigurationImportRequest]) (*connect.Response[pb.GetNativeConfigurationImportResponse], error) {
	actor, err := configurationActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	id := domain.ID(req.Msg.RequestId)
	if id.Validate() != nil {
		return nil, rpc.Error(domain.NativeConfigurationInvalid(), "")
	}
	var original store.NativeConfigurationOriginal
	read := func(tx *store.Tx) error {
		if e := tx.Authorize(); e != nil {
			return e
		}
		value, found, e := tx.NativeConfigurationOriginal(id)
		if e != nil {
			return e
		}
		if !found {
			return domain.Fail(domain.NotFound, "The original import was not delivered.", "Create a new preview before a new explicit import.")
		}
		if value.Actor != actor {
			return domain.NativeConfigurationInvalid()
		}
		original = value
		return nil
	}
	err = s.Store.Read(ctx, read)
	if err != nil && domain.SafeError(err).Code == domain.NotFound {
		_, err = s.Store.Mutate(ctx, domain.NewID(), "native-configuration.fence", struct {
			ID    domain.ID
			Actor domain.Principal
		}{id, actor}, func(tx *store.Tx) (any, error) {
			value, found, e := tx.NativeConfigurationOriginal(id)
			if e != nil {
				return nil, e
			}
			if found {
				if value.Actor != actor {
					return nil, domain.NativeConfigurationInvalid()
				}
				original = value
				return nil, nil
			}
			original = store.NativeConfigurationOriginal{Actor: actor}
			return nil, tx.PutNativeConfigurationOriginal(id, original)
		})
	}
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	response := &pb.GetNativeConfigurationImportResponse{RequestId: req.Msg.RequestId, State: pb.NativeConfigurationImportState_NATIVE_CONFIGURATION_IMPORT_STATE_UNDELIVERED}
	if original.JobID != "" {
		record, e := s.Store.Get(ctx, domain.JobKind, original.JobID)
		if e != nil {
			return nil, rpc.Error(e, "")
		}
		job, e := store.Decode[domain.Job](record)
		if e != nil {
			return nil, rpc.Error(e, "")
		}
		response.Job = rpc.Resource(record)
		response.State = pb.NativeConfigurationImportState_NATIVE_CONFIGURATION_IMPORT_STATE_PENDING
		if job.State == domain.JobSucceeded {
			response.State = pb.NativeConfigurationImportState_NATIVE_CONFIGURATION_IMPORT_STATE_SUCCEEDED
		}
		if job.State == domain.JobFailed || job.State == domain.JobCanceled {
			response.State = pb.NativeConfigurationImportState_NATIVE_CONFIGURATION_IMPORT_STATE_FAILED
		}
	}
	return connect.NewResponse(response), nil
}
