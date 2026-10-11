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

func (s *Service) observeMCP(ctx context.Context, machine, request domain.ID, mutation domain.MCPMutation) (domain.MCPResult, error) {
	var empty domain.MCPResult
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice {
		return empty, domain.Fail(domain.PermissionDenied, "MCP management requires a paired product client.", "Use the authorized owner or client.")
	}
	if machine.Validate() != nil || request.Validate() != nil || mutation.Validate() != nil {
		return empty, domain.MCPUnavailable()
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
		return empty, domain.MCPUnavailable()
	}
	q := domain.MCPRequest{ServerID: s.Identity.ServerID, ActorID: actorID, MachineID: machine, WorkerDeviceID: reader.device, WorkerInstanceID: reader.instance, RequestID: request, Mutation: mutation}
	observation := &workspaceObservation{request: workspace.ReadRequest{ID: domain.NewID(), Deadline: deadline.UTC(), Preparation: workspace.PrepareRequest{MachineID: machine}, MCP: &q}, result: make(chan workspaceReadReply, 1)}
	active := 0
	for _, r := range s.workspaceReaders {
		if r.pending != nil {
			active++
		}
	}
	if active >= 64 {
		s.workspaceReadsMu.Unlock()
		return empty, domain.MCPUnavailable()
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
		if !slices.Contains(value.WorkerCapabilities, domain.MCPManagementV1) {
			return domain.Fail(domain.Unsupported, "This Worker does not support MCP management.", "Update the selected Runner Device; native runtime support remains separate.")
		}
		return tx.WorkerUpdateAdmission(machine)
	}
	if mutation.Action != domain.MCPList && mutation.Action != domain.MCPInspectMutation {
		raw, _ := json.Marshal(mutation)
		hash := sha256.Sum256(raw)
		intent := store.MCPIntent{RequestID: request, WorkerDeviceID: reader.device, ActorID: actorID, DefinitionID: mutation.DefinitionID, Action: mutation.Action, Digest: hex.EncodeToString(hash[:]), State: domain.MCPPending}
		_, err := s.Store.Mutate(ctx, request, "mcp.accept", intent, func(tx *store.Tx) (any, error) {
			if e := check(tx); e != nil {
				return nil, e
			}
			if e := tx.MCPPrepareIntent(intent); e != nil {
				return nil, e
			}
			return intent, nil
		})
		if err != nil {
			return empty, err
		}
	} else if mutation.Action == domain.MCPInspectMutation {
		// Reserve an undelivered original identity before observing the Worker.
		// A delayed submission cannot turn a confirmed absence into a new write.
		verify := func(tx *store.Tx) error {
			if e := check(tx); e != nil {
				return e
			}
			intent, e := tx.MCPIntent(request)
			if e != nil {
				return e
			}
			if intent.ActorID != actorID || intent.WorkerDeviceID != reader.device {
				return domain.MCPUnavailable()
			}
			return nil
		}
		err := s.Store.Read(ctx, verify)
		if err != nil && domain.SafeError(err).Code == domain.NotFound {
			fence := store.MCPIntent{RequestID: request, WorkerDeviceID: reader.device, ActorID: actorID, Action: domain.MCPInspectMutation, Digest: "undelivered-original", State: domain.MCPFailed}
			_, err = s.Store.Mutate(ctx, request, "mcp.accept", fence, func(tx *store.Tx) (any, error) {
				if e := check(tx); e != nil {
					return nil, e
				}
				if e := tx.MCPPrepareIntent(fence); e != nil {
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
		return empty, domain.MCPUnavailable()
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if e := check(tx); e != nil {
			return e
		}
		select {
		case reader.requests <- raw:
			return nil
		default:
			return domain.MCPUnavailable()
		}
	}); err != nil {
		return empty, err
	}
	select {
	case <-ctx.Done():
		return empty, domain.Fail(domain.RecoveryRequired, "The MCP request outcome is unconfirmed.", "Inspect the original request instead of submitting a new mutation.")
	case <-reader.done:
		return empty, domain.MCPUnavailable()
	case reply := <-observation.result:
		if reply.problem != nil {
			return empty, reply.problem
		}
		var result domain.MCPResult
		if domain.Decode(reply.document, &result) != nil || result.Validate() != nil || result.RequestID != request || result.WorkerDeviceID != reader.device {
			return empty, domain.MCPUnavailable()
		}
		_, err := s.Store.Mutate(ctx, domain.NewID(), "mcp.observe", struct {
			Request domain.ID
			Worker  domain.ID
			Result  domain.MCPResult
		}{request, reader.device, result}, func(tx *store.Tx) (any, error) {
			if e := check(tx); e != nil {
				return nil, e
			}
			if e := tx.MCPAcceptResult(result); e != nil {
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
			for i := range result.Definitions {
				refs, e := tx.MCPReferences(reader.device, result.Definitions[i].ID)
				if e != nil {
					return e
				}
				result.Definitions[i].AgentReferences = refs
			}
			return nil
		}); err != nil {
			return empty, err
		}
		return result, nil
	}
}
func mcpState(state domain.MCPMutationState) pb.MCPMutationState {
	switch state {
	case domain.MCPPending:
		return pb.MCPMutationState_MCP_MUTATION_STATE_PENDING
	case domain.MCPSucceeded:
		return pb.MCPMutationState_MCP_MUTATION_STATE_SUCCEEDED
	case domain.MCPCanceled:
		return pb.MCPMutationState_MCP_MUTATION_STATE_CANCELED
	case domain.MCPFailed:
		return pb.MCPMutationState_MCP_MUTATION_STATE_FAILED
	default:
		return pb.MCPMutationState_MCP_MUTATION_STATE_RECOVERY_REQUIRED
	}
}
func decodeMCPMutation(raw []byte) (domain.MCPMutation, error) {
	var mutation domain.MCPMutation
	if domain.DecodeWithLimit(raw, &mutation, domain.MaxMCPDocumentBytes) != nil || mutation.Validate() != nil {
		return mutation, domain.MCPUnavailable()
	}
	return mutation, nil
}
func (s *Service) ListMCPDefinitions(ctx context.Context, req *connect.Request[pb.ListMCPDefinitionsRequest]) (*connect.Response[pb.ListMCPDefinitionsResponse], error) {
	result, err := s.observeMCP(ctx, domain.ID(req.Msg.MachineId), domain.NewID(), domain.MCPMutation{Version: 1, Action: domain.MCPList})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	raw, _ := json.Marshal(result.Definitions)
	return connect.NewResponse(&pb.ListMCPDefinitionsResponse{WorkerDeviceId: string(result.WorkerDeviceID), DefinitionsJson: raw}), nil
}
func (s *Service) MutateMCPDefinition(ctx context.Context, req *connect.Request[pb.MutateMCPDefinitionRequest]) (*connect.Response[pb.MutateMCPDefinitionResponse], error) {
	mutation, err := decodeMCPMutation(req.Msg.DocumentJson)
	if err == nil && mutation.Action != domain.MCPSave && mutation.Action != domain.MCPDelete {
		err = domain.MCPUnavailable()
	}
	var result domain.MCPResult
	if err == nil {
		result, err = s.observeMCP(ctx, domain.ID(req.Msg.MachineId), domain.ID(req.Msg.RequestId), mutation)
	}
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	raw, _ := json.Marshal(result)
	return connect.NewResponse(&pb.MutateMCPDefinitionResponse{RequestId: req.Msg.RequestId, State: mcpState(result.State), ResultJson: raw}), nil
}
func (s *Service) AuthenticateMCP(ctx context.Context, req *connect.Request[pb.AuthenticateMCPRequest]) (*connect.Response[pb.AuthenticateMCPResponse], error) {
	mutation, err := decodeMCPMutation(req.Msg.DocumentJson)
	if err == nil && !slices.Contains([]domain.MCPAction{domain.MCPManualAuthentication, domain.MCPOAuthStart, domain.MCPOAuthCallback, domain.MCPOAuthCancel}, mutation.Action) {
		err = domain.MCPUnavailable()
	}
	var result domain.MCPResult
	if err == nil {
		result, err = s.observeMCP(ctx, domain.ID(req.Msg.MachineId), domain.ID(req.Msg.RequestId), mutation)
	}
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	raw, _ := json.Marshal(result)
	return connect.NewResponse(&pb.AuthenticateMCPResponse{RequestId: req.Msg.RequestId, State: mcpState(result.State), ResultJson: raw}), nil
}
func (s *Service) GetMCPMutation(ctx context.Context, req *connect.Request[pb.GetMCPMutationRequest]) (*connect.Response[pb.GetMCPMutationResponse], error) {
	result, err := s.observeMCP(ctx, domain.ID(req.Msg.MachineId), domain.ID(req.Msg.RequestId), domain.MCPMutation{Version: 1, Action: domain.MCPInspectMutation})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	raw, _ := json.Marshal(result)
	return connect.NewResponse(&pb.GetMCPMutationResponse{RequestId: req.Msg.RequestId, State: mcpState(result.State), ResultJson: raw}), nil
}

func preserveMCPSelections(tx *store.Tx, id domain.ID, expected uint64, agent *domain.Agent) error {
	if expected > 0 && agent.MCP == nil {
		row, err := tx.Get(domain.AgentKind, id)
		if err != nil {
			return err
		}
		prior, err := store.Decode[domain.Agent](row)
		if err != nil {
			return err
		}
		agent.MCP = prior.MCP
	}
	return tx.ValidateMCPAgent(agent.MCP)
}
