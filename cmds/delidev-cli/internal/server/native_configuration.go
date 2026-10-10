// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"reflect"
	"slices"
	"time"
)

type nativeConfigurationPending struct {
	Plan    domain.NativeConfigurationImportPlan `json:"plan"`
	ChildID domain.ID                            `json:"child_id"`
}

func nativeConfigurationConflict() *domain.Error {
	return domain.Fail(domain.Conflict, "The selected source, Worker or destination changed.", "Obtain a fresh native configuration preview and confirm its exact selection.")
}
func nativeConfigurationAuthority(tx *store.Tx, input domain.NativeConfigurationJobInput) error {
	if input.Validate() != nil {
		return domain.NativeConfigurationInvalid()
	}
	if err := authorizeConfigurationImport(tx, input.Actor); err != nil {
		return err
	}
	_, machine, err := activeMachine(tx, input.MachineID)
	if err != nil {
		return err
	}
	if !slices.Contains(machine.WorkerCapabilities, domain.CodexConfigurationImportV1) {
		return domain.Fail(domain.Unsupported, "This Worker cannot inspect selected Codex configuration.", "Update and reconnect the selected Worker.")
	}
	instance, seen, err := tx.WorkerInstance(input.MachineID)
	if err != nil {
		return err
	}
	device, err := tx.InstallationWorkerDevice(input.MachineID)
	if err != nil {
		return err
	}
	if instance != input.InstanceID || device != input.DeviceID || time.Since(seen) > workerLease {
		return nativeConfigurationConflict()
	}
	return nil
}
func validateNativeConfigurationReport(tx *store.Tx, job domain.Job, raw []byte) error {
	var input domain.NativeConfigurationJobInput
	var output domain.NativeConfigurationSnapshot
	if domain.Decode(job.Input, &input) != nil || domain.Decode(raw, &output) != nil || output.Validate() != nil || !reflect.DeepEqual(input.Read.Scopes, output.Scopes) || job.MachineID != input.MachineID || job.InstanceID != input.InstanceID || job.AssignedDeviceID != input.DeviceID {
		return domain.NativeConfigurationInvalid()
	}
	if input.Read.ExpectedDigest != "" && input.Read.ExpectedDigest != output.Digest {
		return nativeConfigurationConflict()
	}
	return nativeConfigurationAuthority(tx, input)
}
func (s *Service) nativeConfigurationResource(ctx context.Context, id domain.ID) (*pb.Resource, error) {
	var resource *pb.Resource
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		record, err := tx.Get(domain.JobKind, id)
		if err != nil {
			return err
		}
		resource = rpc.Resource(record)
		return nil
	})
	return resource, err
}
func (s *Service) RequestCodexConfigurationPreview(ctx context.Context, req *connect.Request[pb.RequestCodexConfigurationPreviewRequest]) (*connect.Response[pb.RequestCodexConfigurationPreviewResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := configurationActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var read domain.NativeConfigurationRead
	if err = domain.Decode(req.Msg.SelectionJson, &read); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if read.Validate() != nil || read.ExpectedDigest != "" {
		return nil, rpc.Error(domain.NativeConfigurationInvalid(), correlation)
	}
	input := struct {
		Actor   domain.Principal
		Machine domain.ID
		Read    domain.NativeConfigurationRead
	}{actor, domain.ID(req.Msg.MachineId), read}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "configuration.native-preview", input, func(tx *store.Tx) (any, error) {
		if err := tx.Authorize(); err != nil {
			return nil, err
		}
		instance, _, err := tx.WorkerInstance(input.Machine)
		if err != nil {
			return nil, err
		}
		device, err := tx.InstallationWorkerDevice(input.Machine)
		if err != nil {
			return nil, err
		}
		jobInput := domain.NativeConfigurationJobInput{Actor: actor, Read: read, MachineID: input.Machine, DeviceID: device, InstanceID: instance}
		if err = nativeConfigurationAuthority(tx, jobInput); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(jobInput)
		if err != nil {
			return nil, err
		}
		record, err := tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.InspectCodexConfigurationJob, State: domain.JobQueued, MachineID: input.Machine, Input: raw, AcceptedAt: time.Now().UTC()})
		if err != nil {
			return nil, err
		}
		return struct {
			JobID domain.ID `json:"job_id"`
		}{record.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var reference struct {
		JobID domain.ID `json:"job_id"`
	}
	if err = domain.Decode(result.Data, &reference); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	job, err := s.nativeConfigurationResource(ctx, reference.JobID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "native configuration preview accepted", "request_id", req.Msg.RequestId, "job_id", reference.JobID, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.RequestCodexConfigurationPreviewResponse{RequestId: req.Msg.RequestId, Replayed: result.Replayed, Job: job})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func nativeConfigurationPlanScope(actor domain.Principal, plan domain.NativeConfigurationImportPlan) string {
	raw, _ := json.Marshal(struct {
		Actor domain.Principal
		Plan  domain.NativeConfigurationImportPlan
	}{actor, plan})
	digest := sha256.Sum256(raw)
	return "native-configuration:" + hex.EncodeToString(digest[:])
}
func nativeConfigurationSource(tx *store.Tx, actor domain.Principal, id domain.ID, revision uint64) (domain.Job, domain.NativeConfigurationJobInput, domain.NativeConfigurationSnapshot, error) {
	var input domain.NativeConfigurationJobInput
	var snapshot domain.NativeConfigurationSnapshot
	record, err := tx.Get(domain.JobKind, id)
	if err != nil {
		return domain.Job{}, input, snapshot, err
	}
	job, err := store.Decode[domain.Job](record)
	if err != nil {
		return job, input, snapshot, err
	}
	if record.Revision != revision || job.Type != domain.InspectCodexConfigurationJob || job.State != domain.JobSucceeded || job.ParentID != "" || domain.Decode(job.Input, &input) != nil || input.Actor != actor {
		return job, input, snapshot, nativeConfigurationConflict()
	}
	if err = validateNativeConfigurationReport(tx, job, job.Output); err != nil {
		return job, input, snapshot, err
	}
	err = domain.Decode(job.Output, &snapshot)
	return job, input, snapshot, err
}
func (s *Service) PreviewCodexConfigurationImport(ctx context.Context, req *connect.Request[pb.PreviewCodexConfigurationImportRequest]) (*connect.Response[pb.PreviewCodexConfigurationImportResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := configurationActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var selection domain.NativeConfigurationImportSelection
	if err = domain.Decode(req.Msg.SelectionJson, &selection); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	plan := domain.NativeConfigurationImportPlan{}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		_, input, snapshot, err := nativeConfigurationSource(tx, actor, selection.PreviewJobID, selection.ExpectedPreviewRevision)
		if err != nil {
			return err
		}
		record, err := tx.Get(domain.AgentKind, selection.AgentID)
		if err != nil {
			return err
		}
		agent, err := store.Decode[domain.Agent](record)
		if err != nil {
			return err
		}
		if record.Revision != selection.ExpectedAgentRevision || agent.Harness != domain.Codex || len(selection.Entries) == 0 || len(selection.Entries) > 256 {
			return nativeConfigurationConflict()
		}
		selected := map[string]bool{}
		for _, key := range selection.Entries {
			if selected[key] {
				return domain.NativeConfigurationInvalid()
			}
			selected[key] = true
		}
		plan = domain.NativeConfigurationImportPlan{SourceJobID: selection.PreviewJobID, SourceRevision: selection.ExpectedPreviewRevision, MachineID: input.MachineID, DeviceID: input.DeviceID, InstanceID: input.InstanceID, Read: input.Read, AgentID: selection.AgentID, ExpectedAgentRevision: selection.ExpectedAgentRevision, Agent: agent, Packages: []domain.NativeInstructionPackage{}, Selected: []string{}}
		plan.Read.ExpectedDigest = snapshot.Digest
		// Source order is authoritative; project selections override selected home
		// settings and instruction packages retain their outer-to-inner order.
		for _, entry := range snapshot.Entries {
			if !selected[entry.Key] {
				continue
			}
			delete(selected, entry.Key)
			plan.Selected = append(plan.Selected, entry.Key)
			switch entry.Kind {
			case domain.NativeConfigurationSetting:
				switch entry.Name {
				case "model_reasoning_effort":
					plan.Agent.Effort = entry.Value
				case "approval_policy":
					plan.Agent.Options.ApprovalPolicy = entry.Value
				case "service_tier":
					plan.Agent.Options.ServiceTier = entry.Value
				case "sandbox_mode":
					permission := domain.PermissionMode(entry.Value)
					if entry.Value == "danger-full-access" {
						permission = domain.PermissionFullAccess
					}
					plan.Agent.Options.Permission = permission
				}
			case domain.NativeConfigurationInstruction:
				id := domain.NewID()
				packageValue := domain.Template{Name: fmt.Sprintf("Imported Codex instructions %d", len(plan.Packages)+1), Contents: entry.Value, NativeSource: &domain.NativeInstructionSource{Digest: snapshot.Digest, Scope: snapshot.Scopes[entry.Scope].Kind, Source: entry.Source}}
				if err = packageValue.Validate(); err != nil {
					return err
				}
				plan.Packages = append(plan.Packages, domain.NativeInstructionPackage{ID: id, Template: packageValue})
				plan.Agent.Templates = append(plan.Agent.Templates, id)
			default:
				return domain.NativeConfigurationInvalid()
			}
		}
		if len(selected) != 0 {
			return domain.NativeConfigurationInvalid()
		}
		return plan.Agent.Validate()
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	token, err := s.Identity.EncodeCursor(security.Cursor{Scope: nativeConfigurationPlanScope(actor, plan)})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	raw, err := json.Marshal(domain.NativeConfigurationImportPreview{Plan: plan, Token: token})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.PreviewCodexConfigurationImportResponse{PreviewJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func validateNativeConfigurationPlan(tx *store.Tx, actor domain.Principal, plan domain.NativeConfigurationImportPlan) error {
	_, _, snapshot, err := nativeConfigurationSource(tx, actor, plan.SourceJobID, plan.SourceRevision)
	if err != nil {
		return err
	}
	if snapshot.Digest != plan.Read.ExpectedDigest {
		return nativeConfigurationConflict()
	}
	if err = nativeConfigurationAuthority(tx, domain.NativeConfigurationJobInput{Actor: actor, Read: plan.Read, MachineID: plan.MachineID, DeviceID: plan.DeviceID, InstanceID: plan.InstanceID}); err != nil {
		return err
	}
	record, err := tx.Get(domain.AgentKind, plan.AgentID)
	if err != nil {
		return err
	}
	if record.Revision != plan.ExpectedAgentRevision {
		return nativeConfigurationConflict()
	}
	if plan.Agent.Validate() != nil || plan.Agent.Harness != domain.Codex {
		return domain.NativeConfigurationInvalid()
	}
	packageIDs := map[domain.ID]bool{}
	for _, item := range plan.Packages {
		packageIDs[item.ID] = true
	}
	existing := plan.Agent
	existing.Templates = nil
	for _, id := range plan.Agent.Templates {
		if !packageIDs[id] {
			existing.Templates = append(existing.Templates, id)
		}
	}
	if err = validateRelationships(tx, domain.AgentKind, plan.AgentID, plan.ExpectedAgentRevision, &existing); err != nil {
		return err
	}
	for _, item := range plan.Packages {
		if item.Template.Validate() != nil || item.Template.NativeSource == nil {
			return domain.NativeConfigurationInvalid()
		}
		if err = tx.RequireUnusedID(item.ID); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) ApplyCodexConfigurationImport(ctx context.Context, req *connect.Request[pb.ApplyCodexConfigurationImportRequest]) (*connect.Response[pb.ApplyCodexConfigurationImportResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := configurationActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var preview domain.NativeConfigurationImportPreview
	if err = domain.Decode(req.Msg.PreviewJson, &preview); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	input := struct {
		Actor   domain.Principal
		Preview domain.NativeConfigurationImportPreview
	}{actor, preview}
	id := domain.ID(req.Msg.RequestId)
	result, found, err := s.Store.Replay(ctx, id, "configuration.native-import", input)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if !found {
		if _, err = s.Identity.DecodeCursor(preview.Token, nativeConfigurationPlanScope(actor, preview.Plan)); err != nil {
			return nil, rpc.Error(err, correlation)
		}
		result, err = s.Store.Mutate(ctx, id, "configuration.native-import", input, func(tx *store.Tx) (any, error) {
			if err := tx.Authorize(); err != nil {
				return nil, err
			}
			if err := validateNativeConfigurationPlan(tx, actor, preview.Plan); err != nil {
				return nil, err
			}
			parentID, childID := domain.NewID(), domain.NewID()
			now := time.Now().UTC()
			pending := configurationImportJob{Actor: actor, Native: &nativeConfigurationPending{Plan: preview.Plan, ChildID: childID}}
			raw, err := json.Marshal(pending)
			if err != nil {
				return nil, err
			}
			if _, err = tx.PutJob(parentID, 0, "", "", domain.Job{Type: domain.ImportConfigurationJob, State: domain.JobQueued, Input: raw, AcceptedAt: now}); err != nil {
				return nil, err
			}
			plan := preview.Plan
			childInput := domain.NativeConfigurationJobInput{Actor: actor, Read: plan.Read, MachineID: plan.MachineID, DeviceID: plan.DeviceID, InstanceID: plan.InstanceID}
			raw, err = json.Marshal(childInput)
			if err != nil {
				return nil, err
			}
			if _, err = tx.PutJob(childID, 0, "", "", domain.Job{Type: domain.InspectCodexConfigurationJob, State: domain.JobQueued, MachineID: plan.MachineID, ParentID: parentID, Input: raw, AcceptedAt: now}); err != nil {
				return nil, err
			}
			return struct {
				JobID domain.ID `json:"job_id"`
			}{parentID}, nil
		})
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	var reference struct {
		JobID domain.ID `json:"job_id"`
	}
	if err = domain.Decode(result.Data, &reference); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	job, err := s.nativeConfigurationResource(ctx, reference.JobID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "native configuration import observed", "request_id", id, "job_id", reference.JobID, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ApplyCodexConfigurationImportResponse{RequestId: string(id), Replayed: result.Replayed, Job: job})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func finishNativeConfigurationImport(tx *store.Tx, record store.Record, parent domain.Job, actor domain.Principal, pending nativeConfigurationPending) error {
	row, err := tx.Get(domain.JobKind, pending.ChildID)
	if err != nil {
		return err
	}
	child, err := store.Decode[domain.Job](row)
	if err != nil {
		return err
	}
	if child.Type != domain.InspectCodexConfigurationJob || child.ParentID != record.ID {
		return domain.NativeConfigurationInvalid()
	}
	if child.State == domain.JobQueued || child.State == domain.JobClaimed {
		return nil
	}
	var problem *domain.Error
	if child.State != domain.JobSucceeded {
		problem = nativeConfigurationConflict()
	} else if err = validateNativeConfigurationReport(tx, child, child.Output); err != nil {
		problem = domain.SafeError(err)
	}
	if problem == nil {
		if err = validateNativeConfigurationPlan(tx, actor, pending.Plan); err != nil {
			problem = domain.SafeError(err)
		}
	}
	if problem == nil {
		resources := []domain.ConfigurationImportedResource{}
		for _, item := range pending.Plan.Packages {
			row, err := tx.Put(domain.TemplateKind, item.ID, 0, "", "", item.Template)
			if err != nil {
				return err
			}
			resources = append(resources, domain.ConfigurationImportedResource{ID: row.ID, Kind: row.Kind, Action: domain.ConfigurationCreate, Revision: row.Revision})
		}
		if err = validateRelationships(tx, domain.AgentKind, pending.Plan.AgentID, pending.Plan.ExpectedAgentRevision, &pending.Plan.Agent); err != nil {
			return err
		}
		row, err := tx.Put(domain.AgentKind, pending.Plan.AgentID, pending.Plan.ExpectedAgentRevision, "", "", pending.Plan.Agent)
		if err != nil {
			return err
		}
		resources = append(resources, domain.ConfigurationImportedResource{ID: row.ID, Kind: row.Kind, Action: domain.ConfigurationReplace, Revision: row.Revision})
		parent.Output, err = json.Marshal(resources)
		if err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	parent.Input = []byte(`{"version":1}`)
	parent.FinishedAt = &now
	if problem == nil {
		parent.State = domain.JobSucceeded
	} else {
		parent.State = domain.JobFailed
		parent.Problem = problem
	}
	_, err = tx.PutJob(record.ID, record.Revision, "", "", parent)
	return err
}
