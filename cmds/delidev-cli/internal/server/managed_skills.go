// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"slices"
	"time"
)

func (s *Service) observeManagedSkills(ctx context.Context, machine, request domain.ID, mutation domain.ManagedSkillMutation) (domain.ManagedSkillResult, error) {
	var empty domain.ManagedSkillResult
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice {
		return empty, domain.Fail(domain.PermissionDenied, "Skill management requires a paired product client.", "Use the authorized owner or client.")
	}
	if machine.Validate() != nil || request.Validate() != nil || mutation.Validate() != nil {
		return empty, domain.ManagedSkillUnavailable()
	}
	actorID := actor.DeviceID
	if actorID == "" {
		actorID = s.Identity.ServerID
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	s.workspaceReadsMu.Lock()
	reader := s.workspaceReaders[machine]
	if reader == nil || reader.pending != nil {
		s.workspaceReadsMu.Unlock()
		return empty, domain.ManagedSkillUnavailable()
	}
	q := domain.ManagedSkillRequest{ServerID: s.Identity.ServerID, ActorID: actorID, MachineID: machine, WorkerDeviceID: reader.device, WorkerInstanceID: reader.instance, RequestID: request, Mutation: mutation}
	observation := &workspaceObservation{request: workspace.ReadRequest{ID: domain.NewID(), Deadline: deadline.UTC(), Preparation: workspace.PrepareRequest{MachineID: machine}, ManagedSkills: &q}, result: make(chan workspaceReadReply, 1)}
	active := 0
	for _, r := range s.workspaceReaders {
		if r.pending != nil {
			active++
		}
	}
	if active >= 64 {
		s.workspaceReadsMu.Unlock()
		return empty, domain.ManagedSkillUnavailable()
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
	check := func(tx *store.Tx) error {
		if err := currentWorkspaceReader(tx, reader); err != nil {
			return err
		}
		_, value, err := activeMachine(tx, machine)
		if err != nil {
			return err
		}
		if !slices.Contains(value.WorkerCapabilities, domain.ManagedSkillManagementV1) {
			return domain.Fail(domain.Unsupported, "This Worker does not support skill management.", "Update the selected Runner Device; native runtime support remains separate.")
		}
		return tx.WorkerUpdateAdmission(machine)
	}
	if mutation.Action != domain.ManagedSkillList && mutation.Action != domain.ManagedSkillInspect && mutation.Action != domain.ManagedSkillView {
		raw, _ := json.Marshal(mutation)
		hash := sha256.Sum256(raw)
		intent := store.ManagedSkillIntent{RequestID: request, WorkerDeviceID: reader.device, ActorID: actorID, PackageID: mutation.PackageID, Action: mutation.Action, Digest: hex.EncodeToString(hash[:]), State: domain.MCPPending}
		_, err := s.Store.Mutate(ctx, request, "managed-skills.accept", intent, func(tx *store.Tx) (any, error) {
			if e := check(tx); e != nil {
				return nil, e
			}
			if e := tx.ManagedSkillPrepareIntent(intent); e != nil {
				return nil, e
			}
			return intent, nil
		})
		if err != nil {
			return empty, err
		}
	} else if mutation.Action == domain.ManagedSkillInspect {
		// Reserve an undelivered original identity before observing the Worker.
		// A delayed submission cannot turn a confirmed absence into a new write.
		verify := func(tx *store.Tx) error {
			if e := check(tx); e != nil {
				return e
			}
			intent, e := tx.ManagedSkillIntent(request)
			if e != nil {
				return e
			}
			if intent.ActorID != actorID || intent.WorkerDeviceID != reader.device {
				return domain.ManagedSkillUnavailable()
			}
			return nil
		}
		err := s.Store.Read(ctx, verify)
		if err != nil && domain.SafeError(err).Code == domain.NotFound {
			fence := store.ManagedSkillIntent{RequestID: request, WorkerDeviceID: reader.device, ActorID: actorID, Action: domain.ManagedSkillInspect, Digest: "undelivered-original", State: domain.MCPFailed}
			_, err = s.Store.Mutate(ctx, request, "managed-skills.accept", fence, func(tx *store.Tx) (any, error) {
				if e := check(tx); e != nil {
					return nil, e
				}
				if e := tx.ManagedSkillPrepareIntent(fence); e != nil {
					return nil, e
				}
				return fence, nil
			})
			if err != nil && domain.SafeError(err).Code == domain.Conflict {
				err = s.Store.Read(ctx, verify)
			}
		}
		if err != nil {
			return empty, err
		}
	}
	raw, _ := json.Marshal(observation.request)
	if len(raw) > domain.MaxMCPDocumentBytes+4096 {
		return empty, domain.ManagedSkillUnavailable()
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if e := check(tx); e != nil {
			return e
		}
		select {
		case reader.requests <- raw:
			return nil
		default:
			return domain.ManagedSkillUnavailable()
		}
	}); err != nil {
		return empty, err
	}
	select {
	case <-ctx.Done():
		return empty, domain.Fail(domain.RecoveryRequired, "The skill request outcome is unconfirmed.", "Inspect the original request instead of submitting a new mutation.")
	case <-reader.done:
		return empty, domain.ManagedSkillUnavailable()
	case reply := <-observation.result:
		if reply.problem != nil {
			return empty, reply.problem
		}
		var result domain.ManagedSkillResult
		if domain.DecodeBounded(reply.document, &result, 8<<20) != nil || result.Validate() != nil || result.RequestID != request || result.WorkerDeviceID != reader.device {
			return empty, domain.ManagedSkillUnavailable()
		}
		safeResult := result
		safeResult.SkillMarkdown = ""
		safeResult.Manifest = nil
		_, err := s.Store.Mutate(ctx, domain.NewID(), "managed-skills.observe", struct {
			Request domain.ID
			Worker  domain.ID
			Result  domain.ManagedSkillResult
		}{request, reader.device, safeResult}, func(tx *store.Tx) (any, error) {
			if e := check(tx); e != nil {
				return nil, e
			}
			if e := tx.ManagedSkillAcceptResult(result); e != nil {
				return nil, e
			}
			return nil, nil
		})
		if err != nil {
			return empty, err
		}
		if err := s.Store.Read(ctx, func(tx *store.Tx) error {
			if e := check(tx); e != nil {
				return e
			}
			for i := range result.Packages {
				refs, e := tx.ManagedSkillReferences(reader.device, result.Packages[i].ID)
				if e != nil {
					return e
				}
				result.Packages[i].AgentReferences = refs
			}
			return nil
		}); err != nil {
			return empty, err
		}
		return result, nil
	}
}

func skillMutationState(state domain.MCPMutationState) pb.SkillMutationState {
	switch state {
	case domain.MCPPending:
		return pb.SkillMutationState_SKILL_MUTATION_STATE_PENDING
	case domain.MCPSucceeded:
		return pb.SkillMutationState_SKILL_MUTATION_STATE_SUCCEEDED
	case domain.MCPFailed:
		return pb.SkillMutationState_SKILL_MUTATION_STATE_FAILED
	default:
		return pb.SkillMutationState_SKILL_MUTATION_STATE_RECOVERY_REQUIRED
	}
}
func (s *Service) ListManagedSkills(ctx context.Context, req *connect.Request[pb.ListManagedSkillsRequest]) (*connect.Response[pb.ListManagedSkillsResponse], error) {
	result, err := s.observeManagedSkills(ctx, domain.ID(req.Msg.MachineId), domain.NewID(), domain.ManagedSkillMutation{Version: 1, Action: domain.ManagedSkillList})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	raw, _ := json.Marshal(result.Packages)
	return connect.NewResponse(&pb.ListManagedSkillsResponse{WorkerDeviceId: string(result.WorkerDeviceID), PackagesJson: raw}), nil
}
func (s *Service) MutateManagedSkill(ctx context.Context, req *connect.Request[pb.MutateManagedSkillRequest]) (*connect.Response[pb.MutateManagedSkillResponse], error) {
	var mutation domain.ManagedSkillMutation
	if domain.DecodeWithLimit(req.Msg.DocumentJson, &mutation, domain.MaxMCPDocumentBytes) != nil || mutation.Validate() != nil || !slices.Contains([]domain.ManagedSkillAction{domain.ManagedSkillImport, domain.ManagedSkillEnable, domain.ManagedSkillDelete}, mutation.Action) {
		return nil, rpc.Error(domain.ManagedSkillUnavailable(), req.Header().Get(rpc.CorrelationHeader))
	}
	result, err := s.observeManagedSkills(ctx, domain.ID(req.Msg.MachineId), domain.ID(req.Msg.RequestId), mutation)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	raw, _ := json.Marshal(result)
	return connect.NewResponse(&pb.MutateManagedSkillResponse{RequestId: req.Msg.RequestId, State: skillMutationState(result.State), ResultJson: raw}), nil
}
func (s *Service) GetManagedSkillMutation(ctx context.Context, req *connect.Request[pb.GetManagedSkillMutationRequest]) (*connect.Response[pb.GetManagedSkillMutationResponse], error) {
	result, err := s.observeManagedSkills(ctx, domain.ID(req.Msg.MachineId), domain.ID(req.Msg.RequestId), domain.ManagedSkillMutation{Version: 1, Action: domain.ManagedSkillInspect})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	raw, _ := json.Marshal(result)
	return connect.NewResponse(&pb.GetManagedSkillMutationResponse{RequestId: req.Msg.RequestId, State: skillMutationState(result.State), ResultJson: raw}), nil
}
func (s *Service) ViewManagedSkill(ctx context.Context, req *connect.Request[pb.ViewManagedSkillRequest]) (*connect.Response[pb.ViewManagedSkillResponse], error) {
	result, err := s.observeManagedSkills(ctx, domain.ID(req.Msg.MachineId), domain.NewID(), domain.ManagedSkillMutation{Version: 1, Action: domain.ManagedSkillView, PackageID: domain.ID(req.Msg.PackageId), ExpectedRevision: req.Msg.ExpectedRevision})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	raw, _ := json.Marshal(result.Package)
	manifest, _ := json.Marshal(result.Manifest)
	return connect.NewResponse(&pb.ViewManagedSkillResponse{WorkerDeviceId: string(result.WorkerDeviceID), PackageJson: raw, SkillMarkdown: []byte(result.SkillMarkdown), ManifestJson: manifest}), nil
}
func preserveManagedSkillSelections(tx *store.Tx, id domain.ID, expected uint64, agent *domain.Agent) error {
	if expected > 0 && agent.ManagedSkills == nil {
		row, err := tx.Get(domain.AgentKind, id)
		if err != nil {
			return err
		}
		prior, err := store.Decode[domain.Agent](row)
		if err != nil {
			return err
		}
		agent.ManagedSkills = prior.ManagedSkills
	}
	return tx.ValidateManagedSkillAgent(agent.ManagedSkills)
}
