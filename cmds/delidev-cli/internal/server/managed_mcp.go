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

func mcpUnavailable() error {
	return domain.Fail(domain.Unavailable, "MCP management is unavailable for this Runner Device.", "Connect the selected Runner with managed MCP support and retry the read.")
}
func mcpDefinitions(q domain.ManagedMCPRequest, r domain.ManagedMCPResult) error {
	if len(r.Definitions) > 256 {
		return mcpUnavailable()
	}
	seen := map[domain.ID]bool{}
	for _, d := range r.Definitions {
		if d.Validate() != nil || d.MachineID != q.MachineID || d.WorkerDeviceID != q.WorkerDeviceID || seen[d.ID] {
			return mcpUnavailable()
		}
		seen[d.ID] = true
	}
	if r.Operation != nil {
		if r.Operation.ID.Validate() != nil || r.Operation.DefinitionID.Validate() != nil || q.Action != domain.MCPOperationRead && r.Operation.ID != q.RequestID {
			return mcpUnavailable()
		}
		switch r.Operation.State {
		case domain.MCPOperationStarted, domain.MCPOperationCompleted, domain.MCPOperationAwaiting, domain.MCPOperationCanceled, domain.MCPOperationRecovery, domain.MCPOperationRejected:
		default:
			return mcpUnavailable()
		}
	}
	if q.Action != domain.MCPList && r.Operation == nil {
		return mcpUnavailable()
	}
	return nil
}
func managedMCPReferences(tx *store.Tx, machine, worker, id domain.ID) ([]*pb.ManagedMcpAgentReference, error) {
	result := []*pb.ManagedMcpAgentReference{}
	after := domain.ID("")
	for {
		rows, e := tx.List(store.Filter{Kind: domain.AgentKind, After: after, Limit: 200})
		if e != nil {
			return nil, e
		}
		for _, row := range rows {
			a, e := store.Decode[domain.Agent](row)
			if e != nil {
				return nil, e
			}
			if a.ManagedMCP != nil && slices.ContainsFunc(a.ManagedMCP.Selections, func(v domain.ManagedMCPSelection) bool {
				return v.MachineID == machine && v.WorkerDeviceID == worker && v.DefinitionID == id
			}) {
				result = append(result, &pb.ManagedMcpAgentReference{Id: string(row.ID), Name: a.Name})
			}
		}
		if len(rows) < 200 {
			return result, nil
		}
		after = rows[len(rows)-1].ID
	}
}
func (s *Service) forwardManagedMCP(ctx context.Context, q domain.ManagedMCPRequest) (domain.ManagedMCPResult, error) {
	empty := domain.ManagedMCPResult{Definitions: []domain.ManagedMCPDefinition{}}
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice {
		return empty, domain.Fail(domain.PermissionDenied, "MCP management requires a product client.", "Use the paired owner or client.")
	}
	q.ActorID = actor.DeviceID
	if q.ActorID == "" {
		q.ActorID = s.Identity.ServerID
	}
	q.ServerID = s.Identity.ServerID
	if q.MachineID.Validate() != nil || q.RequestID.Validate() != nil {
		return empty, domain.Fail(domain.InvalidArgument, "MCP request identity is invalid.", "Use the selected Runner and original request.")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	s.workspaceReadsMu.Lock()
	reader := s.workspaceReaders[q.MachineID]
	if reader == nil || reader.pending != nil {
		s.workspaceReadsMu.Unlock()
		return empty, mcpUnavailable()
	}
	q.WorkerDeviceID = reader.device
	q.WorkerInstanceID = reader.instance
	if q.Definition != nil {
		if q.Definition.WorkerDeviceID != "" && q.Definition.WorkerDeviceID != q.WorkerDeviceID {
			s.workspaceReadsMu.Unlock()
			return empty, domain.Fail(domain.PermissionDenied, "The MCP draft belongs to another Worker.", "Keep its original Runner Device.")
		}
		q.Definition.MachineID = q.MachineID
		q.Definition.WorkerDeviceID = q.WorkerDeviceID
	}
	observation := &workspaceObservation{request: workspace.ReadRequest{ID: domain.NewID(), Deadline: deadline.UTC(), Preparation: workspace.PrepareRequest{MachineID: q.MachineID}, ManagedMCP: &q}, result: make(chan workspaceReadReply, 1)}
	reader.pending = observation
	s.workspaceReadsMu.Unlock()
	defer func() {
		s.workspaceReadsMu.Lock()
		if reader.pending == observation {
			reader.pending = nil
		}
		s.workspaceReadsMu.Unlock()
		if q.Secrets != nil {
			clear(q.Secrets.Environment)
			clear(q.Secrets.Headers)
		}
		q.CallbackURL = ""
	}()
	check := func(tx *store.Tx) error {
		if e := currentWorkspaceReader(tx, reader); e != nil {
			return e
		}
		_, machine, e := activeMachine(tx, q.MachineID)
		if e != nil {
			return e
		}
		if !slices.Contains(machine.WorkerCapabilities, domain.ManagedMCPV1) {
			return mcpUnavailable()
		}
		return tx.WorkerUpdateAdmission(q.MachineID)
	}
	if e := s.Store.Read(ctx, check); e != nil {
		return empty, e
	}
	if q.Action == domain.MCPOperationRead {
		if e := s.Store.Read(ctx, func(tx *store.Tx) error {
			rows, e := all(tx, domain.ManagedMCPKind)
			if e != nil {
				return e
			}
			for _, row := range rows {
				r, e := store.Decode[domain.ManagedMCPRecord](row)
				if e != nil {
					return e
				}
				if r.PendingRequestID == q.AttemptID && r.PendingActorID == q.ActorID && r.Definition.MachineID == q.MachineID && r.Definition.WorkerDeviceID == q.WorkerDeviceID {
					q.DefinitionID = row.ID
				}
			}
			return nil
		}); e != nil {
			return empty, e
		}
	}

	mutation := q.Action != domain.MCPList && q.Action != domain.MCPOperationRead
	if mutation {
		safe := q
		safe.Secrets = nil
		safe.CallbackURL = ""
		safe.WorkerInstanceID = ""
		_, e := s.Store.Mutate(ctx, domain.NewID(), "managed-mcp.admit", safe, func(tx *store.Tx) (any, error) {
			if e := check(tx); e != nil {
				return nil, e
			}
			id := q.DefinitionID
			if q.Definition != nil {
				id = q.Definition.ID
			}
			row, e := tx.Get(domain.ManagedMCPKind, id)
			record := domain.ManagedMCPRecord{}
			expected := uint64(0)
			if e == nil {
				expected = row.Revision
				record, e = store.Decode[domain.ManagedMCPRecord](row)
				if e != nil {
					return nil, e
				}
			} else if domain.SafeError(e).Code != domain.NotFound {
				return nil, e
			}
			if record.PendingRequestID != "" && (record.PendingRequestID != q.RequestID || record.PendingActorID != q.ActorID) {
				return nil, domain.Fail(domain.RecoveryRequired, "The original MCP mutation is unsettled.", "Inspect its original request before another mutation.")
			}
			if q.Action == domain.MCPDelete {
				refs, e := managedMCPReferences(tx, q.MachineID, q.WorkerDeviceID, id)
				if e != nil {
					return nil, e
				}
				if !q.Confirmed || len(refs) > 0 {
					return nil, domain.Fail(domain.Conflict, "MCP deletion is blocked by Agent Worker references.", "Unselect the definition from its referencing Agent Workers and confirm deletion.")
				}
			}
			if expected == 0 {
				if q.Action != domain.MCPSave || q.Definition == nil {
					return nil, domain.Fail(domain.NotFound, "MCP definition is not retained.", "Read the selected Worker catalog first.")
				}
				record.Definition = *q.Definition
			}
			if record.Definition.MachineID != q.MachineID || record.Definition.WorkerDeviceID != q.WorkerDeviceID {
				return nil, domain.Fail(domain.PermissionDenied, "MCP definition belongs to another Worker.", "Select its original Runner Device.")
			}
			record.PendingRequestID = q.RequestID
			record.PendingActorID = q.ActorID
			record.PendingAction = q.Action
			return tx.Put(domain.ManagedMCPKind, id, expected, "", "", record)
		})
		if e != nil {
			return empty, e
		}
	}
	primary, e := s.primaryWorkspaceStream(reader.machine, reader.instance)
	if e != nil || primary.ID != reader.primary {
		return empty, mcpUnavailable()
	}
	raw, _ := json.Marshal(observation.request)
	defer clear(raw)
	if e = s.Store.Read(ctx, func(tx *store.Tx) error {
		if e := check(tx); e != nil {
			return e
		}
		select {
		case reader.requests <- raw:
			return nil
		default:
			return mcpUnavailable()
		}
	}); e != nil {
		return empty, e
	}
	var r domain.ManagedMCPResult
	select {
	case <-ctx.Done():
		return empty, domain.Fail(domain.RecoveryRequired, "MCP result delivery is uncertain.", "Inspect the original request without repeating its effects.")
	case <-reader.done:
		return empty, mcpUnavailable()
	case <-primary.Done:
		return empty, mcpUnavailable()
	case reply := <-observation.result:
		if reply.problem != nil {
			return empty, reply.problem
		}
		if domain.Decode(reply.document, &r) != nil || mcpDefinitions(q, r) != nil {
			return empty, mcpUnavailable()
		}
	}
	if e = s.Store.Read(ctx, check); e != nil {
		return empty, e
	}
	current, e := s.primaryWorkspaceStream(reader.machine, reader.instance)
	if e != nil || current.ID != reader.primary {
		return empty, mcpUnavailable()
	}
	_, e = s.Store.Mutate(ctx, domain.NewID(), "managed-mcp.publish", struct {
		Actor  domain.ID
		Result domain.ManagedMCPResult
	}{q.ActorID, r}, func(tx *store.Tx) (any, error) {
		if e := check(tx); e != nil {
			return nil, e
		}
		for _, d := range r.Definitions {
			row, e := tx.Get(domain.ManagedMCPKind, d.ID)
			expected := uint64(0)
			record := domain.ManagedMCPRecord{Definition: d, Accepted: true}
			if e == nil {
				expected = row.Revision
				old, e := store.Decode[domain.ManagedMCPRecord](row)
				if e != nil {
					return nil, e
				}
				if old.PendingRequestID != "" && (r.Operation == nil || r.Operation.ID != old.PendingRequestID || old.PendingActorID != q.ActorID) {
					continue
				}
				if old.Definition.Revision > d.Revision {
					return nil, mcpUnavailable()
				}
			} else if domain.SafeError(e).Code != domain.NotFound {
				return nil, e
			}
			if _, e = tx.Put(domain.ManagedMCPKind, d.ID, expected, "", "", record); e != nil {
				return nil, e
			}
		}
		if r.Operation != nil && len(r.Definitions) == 0 {
			row, e := tx.Get(domain.ManagedMCPKind, r.Operation.DefinitionID)
			if e != nil {
				return nil, e
			}
			record, e := store.Decode[domain.ManagedMCPRecord](row)
			if e != nil {
				return nil, e
			}
			if record.PendingRequestID == r.Operation.ID && record.PendingActorID == q.ActorID && r.Operation.State != domain.MCPOperationStarted && r.Operation.State != domain.MCPOperationRecovery {
				if record.PendingAction == domain.MCPDelete && r.Operation.State == domain.MCPOperationCompleted {
					record.Deleted = true
				}
				record.PendingRequestID = ""
				record.PendingActorID = ""
				record.PendingAction = ""
				if _, e = tx.Put(domain.ManagedMCPKind, row.ID, row.Revision, "", "", record); e != nil {
					return nil, e
				}
			}
		}
		return nil, nil
	})
	return r, e
}
func mcpWire(d domain.ManagedMCPDefinition) *pb.ManagedMcpDefinition {
	transport := pb.ManagedMcpTransport_MANAGED_MCP_TRANSPORT_STDIO
	if d.Transport == domain.MCPStreamableHTTP {
		transport = pb.ManagedMcpTransport_MANAGED_MCP_TRANSPORT_STREAMABLE_HTTP
	}
	auth := pb.ManagedMcpAuthentication_MANAGED_MCP_AUTHENTICATION_NONE
	if d.Authentication == domain.MCPManualAuthentication {
		auth = pb.ManagedMcpAuthentication_MANAGED_MCP_AUTHENTICATION_MANUAL
	}
	if d.Authentication == domain.MCPOAuthAuthentication {
		auth = pb.ManagedMcpAuthentication_MANAGED_MCP_AUTHENTICATION_OAUTH
	}
	result := &pb.ManagedMcpDefinition{Id: string(d.ID), Revision: d.Revision, MachineId: string(d.MachineID), WorkerDeviceId: string(d.WorkerDeviceID), Name: d.Name, Transport: transport, Command: d.Command, Arguments: d.Arguments, Cwd: d.Cwd, Endpoint: d.Endpoint, Enabled: d.Enabled, Authentication: auth, Authenticated: d.CredentialID != "" && (d.CredentialExpiresAt.IsZero() || time.Now().Before(d.CredentialExpiresAt))}
	if d.OAuth != nil {
		result.Oauth = &pb.ManagedMcpOAuthProfile{ClientId: d.OAuth.ClientID, AuthorizationUrl: d.OAuth.AuthorizationURL, TokenUrl: d.OAuth.TokenURL, RedirectUri: d.OAuth.RedirectURI, Scope: d.OAuth.Scope}
	}
	for _, h := range d.SupportedHarnesses {
		result.SupportedHarnesses = append(result.SupportedHarnesses, string(h))
	}
	return result
}
func mcpOperationWire(o *domain.ManagedMCPOperation) *pb.ManagedMcpOperation {
	if o == nil {
		return nil
	}
	states := map[domain.MCPOperationState]pb.ManagedMcpOperationState{domain.MCPOperationStarted: pb.ManagedMcpOperationState_MANAGED_MCP_OPERATION_STATE_STARTED, domain.MCPOperationCompleted: pb.ManagedMcpOperationState_MANAGED_MCP_OPERATION_STATE_COMPLETED, domain.MCPOperationAwaiting: pb.ManagedMcpOperationState_MANAGED_MCP_OPERATION_STATE_AWAITING_AUTHORIZATION, domain.MCPOperationCanceled: pb.ManagedMcpOperationState_MANAGED_MCP_OPERATION_STATE_CANCELED, domain.MCPOperationRejected: pb.ManagedMcpOperationState_MANAGED_MCP_OPERATION_STATE_REJECTED, domain.MCPOperationRecovery: pb.ManagedMcpOperationState_MANAGED_MCP_OPERATION_STATE_RECOVERY_REQUIRED}
	return &pb.ManagedMcpOperation{RequestId: string(o.ID), DefinitionId: string(o.DefinitionID), State: states[o.State], AuthorizationUrl: o.AuthorizationURL, ExpiresAt: o.ExpiresAt}
}
func (s *Service) ListManagedMcp(ctx context.Context, req *connect.Request[pb.ListManagedMcpRequest]) (*connect.Response[pb.ListManagedMcpResponse], error) {
	q := domain.ManagedMCPRequest{MachineID: domain.ID(req.Msg.MachineId), RequestID: domain.NewID(), Action: domain.MCPList}
	r, e := s.forwardManagedMCP(ctx, q)
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	response := &pb.ListManagedMcpResponse{}
	e = s.Store.Read(ctx, func(tx *store.Tx) error {
		for _, d := range r.Definitions {
			v := mcpWire(d)
			refs, e := managedMCPReferences(tx, d.MachineID, d.WorkerDeviceID, d.ID)
			if e != nil {
				return e
			}
			v.Agents = refs
			response.Definitions = append(response.Definitions, v)
		}
		return nil
	})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(response), nil
}
func (s *Service) SaveManagedMcp(ctx context.Context, req *connect.Request[pb.SaveManagedMcpRequest]) (*connect.Response[pb.SaveManagedMcpResponse], error) {
	q := domain.ManagedMCPRequest{MachineID: domain.ID(req.Msg.MachineId), RequestID: domain.ID(req.Msg.RequestId), ExpectedRevision: req.Msg.ExpectedRevision, Action: domain.MCPSave}
	if req.Msg.Definition == nil {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "MCP definition is required.", "Keep your original draft."), req.Header().Get(rpc.CorrelationHeader))
	}
	v := req.Msg.Definition
	d := domain.ManagedMCPDefinition{ID: domain.ID(v.Id), Revision: q.ExpectedRevision + 1, MachineID: q.MachineID, WorkerDeviceID: domain.ID(v.WorkerDeviceId), Name: v.Name, Command: v.Command, Arguments: v.Arguments, Cwd: v.Cwd, Endpoint: v.Endpoint, Enabled: v.Enabled}
	switch v.Transport {
	case pb.ManagedMcpTransport_MANAGED_MCP_TRANSPORT_STDIO:
		d.Transport = domain.MCPStdio
	case pb.ManagedMcpTransport_MANAGED_MCP_TRANSPORT_STREAMABLE_HTTP:
		d.Transport = domain.MCPStreamableHTTP
	}
	switch v.Authentication {
	case pb.ManagedMcpAuthentication_MANAGED_MCP_AUTHENTICATION_NONE:
		d.Authentication = domain.MCPNoAuthentication
	case pb.ManagedMcpAuthentication_MANAGED_MCP_AUTHENTICATION_MANUAL:
		d.Authentication = domain.MCPManualAuthentication
	case pb.ManagedMcpAuthentication_MANAGED_MCP_AUTHENTICATION_OAUTH:
		d.Authentication = domain.MCPOAuthAuthentication
	}
	if v.Oauth != nil {
		d.OAuth = &domain.MCPOAuthProfile{ClientID: v.Oauth.ClientId, AuthorizationURL: v.Oauth.AuthorizationUrl, TokenURL: v.Oauth.TokenUrl, RedirectURI: v.Oauth.RedirectUri, Scope: v.Oauth.Scope}
	}
	if v.Authenticated || len(v.SupportedHarnesses) > 0 || len(v.Agents) > 0 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Native MCP state cannot be saved by a client.", "Save definition metadata only."), req.Header().Get(rpc.CorrelationHeader))
	}
	q.Definition = &d
	r, e := s.forwardManagedMCP(ctx, q)
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	result := &pb.SaveManagedMcpResponse{Operation: mcpOperationWire(r.Operation), Replayed: r.Replayed}
	if len(r.Definitions) == 1 {
		result.Definition = mcpWire(r.Definitions[0])
	}
	return connect.NewResponse(result), nil
}
func (s *Service) DeleteManagedMcp(ctx context.Context, req *connect.Request[pb.DeleteManagedMcpRequest]) (*connect.Response[pb.DeleteManagedMcpResponse], error) {
	r, e := s.forwardManagedMCP(ctx, domain.ManagedMCPRequest{MachineID: domain.ID(req.Msg.MachineId), RequestID: domain.ID(req.Msg.RequestId), DefinitionID: domain.ID(req.Msg.DefinitionId), ExpectedRevision: req.Msg.ExpectedRevision, Confirmed: req.Msg.Confirmed, Action: domain.MCPDelete})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.DeleteManagedMcpResponse{Operation: mcpOperationWire(r.Operation), Replayed: r.Replayed}), nil
}
func (s *Service) AuthenticateManagedMcp(ctx context.Context, req *connect.Request[pb.AuthenticateManagedMcpRequest]) (*connect.Response[pb.AuthenticateManagedMcpResponse], error) {
	q := domain.ManagedMCPRequest{MachineID: domain.ID(req.Msg.MachineId), RequestID: domain.ID(req.Msg.RequestId), DefinitionID: domain.ID(req.Msg.DefinitionId), ExpectedRevision: req.Msg.ExpectedRevision, Confirmed: req.Msg.Confirmed, AttemptID: domain.ID(req.Msg.AttemptId), CallbackURL: req.Msg.CallbackUrl}
	switch req.Msg.Action {
	case pb.ManagedMcpAuthAction_MANAGED_MCP_AUTH_ACTION_MANUAL:
		q.Action = domain.MCPAuthenticate
		q.Secrets = &domain.MCPSecretInput{Environment: req.Msg.Environment, Headers: req.Msg.Headers}
	case pb.ManagedMcpAuthAction_MANAGED_MCP_AUTH_ACTION_OAUTH_BEGIN:
		q.Action = domain.MCPOAuthBegin
	case pb.ManagedMcpAuthAction_MANAGED_MCP_AUTH_ACTION_OAUTH_COMPLETE:
		q.Action = domain.MCPOAuthComplete
	case pb.ManagedMcpAuthAction_MANAGED_MCP_AUTH_ACTION_OAUTH_CANCEL:
		q.Action = domain.MCPOAuthCancel
	default:
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Choose an MCP authentication operation.", "Use an explicit supported action."), req.Header().Get(rpc.CorrelationHeader))
	}
	r, e := s.forwardManagedMCP(ctx, q)
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	response := &pb.AuthenticateManagedMcpResponse{Operation: mcpOperationWire(r.Operation), Replayed: r.Replayed}
	if len(r.Definitions) == 1 {
		response.Definition = mcpWire(r.Definitions[0])
	}
	return connect.NewResponse(response), nil
}
func (s *Service) GetManagedMcpOperation(ctx context.Context, req *connect.Request[pb.GetManagedMcpOperationRequest]) (*connect.Response[pb.GetManagedMcpOperationResponse], error) {
	r, e := s.forwardManagedMCP(ctx, domain.ManagedMCPRequest{MachineID: domain.ID(req.Msg.MachineId), RequestID: domain.NewID(), Action: domain.MCPOperationRead, AttemptID: domain.ID(req.Msg.RequestId)})
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	response := &pb.GetManagedMcpOperationResponse{Operation: mcpOperationWire(r.Operation)}
	if len(r.Definitions) == 1 {
		response.Definition = mcpWire(r.Definitions[0])
	}
	return connect.NewResponse(response), nil
}
