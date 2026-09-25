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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Reserve 1 MiB of the 5 MiB transport limit for cursors and envelopes.
// JSON expands document bytes to Base64, independently of the binary size.
const maxResourcePageBytes = 4 << 20

func resourceWireSize(resource *pb.Resource) (int, error) {
	encoded, err := protojson.Marshal(resource)
	if err != nil {
		return 0, err
	}
	return max(proto.Size(resource), len(encoded)) + 16, nil
}

func (s *Service) filter(input *pb.Filter) (store.Filter, error) {
	if input == nil {
		return store.Filter{}, domain.Fail(domain.MissingInput, "A resource filter is required.", "Select a resource kind.")
	}
	kind, err := rpc.Kind(input.Kind)
	if err != nil {
		return store.Filter{}, err
	}
	limit := input.PageSize
	if limit == 0 {
		limit = 50
	}
	if limit > store.MaxPage {
		return store.Filter{}, domain.Fail(domain.InvalidArgument, "Page size exceeds 200.", "Request a smaller page.")
	}
	f := store.Filter{Kind: kind, SessionID: domain.ID(input.SessionId), ProjectID: domain.ID(input.ProjectId), Limit: int(limit)}
	if input.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(input.PageToken, scope(f))
		if err != nil {
			return f, err
		}
		f.After = cursor.After
	}
	return f, nil
}
func (s *Service) GetResource(ctx context.Context, req *connect.Request[pb.GetResourceRequest]) (*connect.Response[pb.GetResourceResponse], error) {
	kind, err := rpc.Kind(req.Msg.Kind)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	record, err := s.Store.Get(ctx, kind, domain.ID(req.Msg.Id))
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.GetResourceResponse{Resource: rpc.Resource(record)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) ListResources(ctx context.Context, req *connect.Request[pb.ListResourcesRequest]) (*connect.Response[pb.ListResourcesResponse], error) {
	f, err := s.filter(req.Msg.Filter)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	records, err := s.Store.List(ctx, f)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	result := &pb.ListResourcesResponse{}
	used := 0
	for _, r := range records {
		resource := rpc.Resource(r)
		size, err := resourceWireSize(resource)
		if err != nil {
			return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
		}
		if used+size > maxResourcePageBytes {
			if len(result.Resources) == 0 {
				return nil, rpc.Error(domain.Fail(domain.ResourceExhausted, "A resource exceeds the list response limit.", "Read the resource by its identity."), req.Header().Get(rpc.CorrelationHeader))
			}
			break
		}
		result.Resources = append(result.Resources, resource)
		used += size
	}
	if len(result.Resources) < len(records) || len(records) == f.Limit {
		last := result.Resources[len(result.Resources)-1]
		result.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope(f), After: domain.ID(last.Id)})
		if err != nil {
			return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
		}
	}
	response := connect.NewResponse(result)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) GetSnapshot(ctx context.Context, req *connect.Request[pb.GetSnapshotRequest]) (*connect.Response[pb.GetSnapshotResponse], error) {
	f, err := s.filter(req.Msg.Filter)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	if f.After != "" {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Snapshots cannot start midway through a resource scope.", "Omit the page token and narrow the scope instead."), req.Header().Get(rpc.CorrelationHeader))
	}
	records, sequence, err := s.Store.Snapshot(ctx, f)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	result := &pb.GetSnapshotResponse{}
	used := 0
	for _, r := range records {
		resource := rpc.Resource(r)
		size, err := resourceWireSize(resource)
		if err != nil {
			return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
		}
		if used+size > maxResourcePageBytes {
			return nil, rpc.Error(domain.Fail(domain.ResourceExhausted, "The snapshot scope exceeds its response byte limit.", "Use a narrower session/project scope; do not treat partial state as a coherent snapshot."), req.Header().Get(rpc.CorrelationHeader))
		}
		result.Resources = append(result.Resources, resource)
		used += size
	}
	result.Cursor, err = s.Identity.EncodeCursor(security.Cursor{Scope: "events:" + string(f.SessionID), Sequence: sequence})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(result)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) WatchEvents(ctx context.Context, req *connect.Request[pb.WatchEventsRequest], stream *connect.ServerStream[pb.WatchEventsResponse]) error {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	stream.ResponseHeader().Set(rpc.CorrelationHeader, correlation)
	cursor, err := s.Identity.DecodeCursor(req.Msg.Cursor, "events:"+req.Msg.SessionId)
	if err != nil {
		return rpc.Error(err, correlation)
	}
	// No per-subscriber queue: fetch a bounded durable page, then synchronously
	// send it under backpressure. HTTP write deadlines terminate slow consumers.
	// The periodic wake also checks durable cursor validity without polling history.
	for {
		changed := s.Store.Changed()
		events, err := s.Store.Events(ctx, cursor.Sequence, domain.ID(req.Msg.SessionId), store.MaxPage)
		if err != nil {
			return rpc.Error(err, correlation)
		}
		for _, event := range events {
			token, err := s.Identity.EncodeCursor(security.Cursor{Scope: cursor.Scope, Sequence: event.Cursor})
			if err != nil {
				return rpc.Error(err, correlation)
			}
			action := pb.EventAction_EVENT_ACTION_UPDATED
			switch event.Action {
			case store.Created:
				action = pb.EventAction_EVENT_ACTION_CREATED
			case store.Deleted:
				action = pb.EventAction_EVENT_ACTION_DELETED
			}
			controller, ok := ctx.Value(writeControllerKey{}).(*http.ResponseController)
			if !ok {
				return rpc.Error(domain.Fail(domain.Internal, "Streaming deadline controller is unavailable.", "Reconnect to the server."), correlation)
			}
			if err := controller.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
				return rpc.Error(err, correlation)
			}
			if err := stream.Send(&pb.WatchEventsResponse{Cursor: token, Id: string(event.ID), EntityId: string(event.EntityID), Kind: rpc.WireKind(event.Kind), SessionId: string(event.SessionID), Revision: event.Revision, Action: action, Time: event.Time.Format(time.RFC3339Nano)}); err != nil {
				return rpc.Error(err, correlation)
			}
			if err := controller.SetWriteDeadline(time.Time{}); err != nil {
				return rpc.Error(err, correlation)
			}
			cursor.Sequence = event.Cursor
		}
		if len(events) == store.MaxPage {
			continue
		}
		timer := time.NewTimer(30 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return rpc.Error(ctx.Err(), correlation)
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}
func (s *Service) SaveConfiguration(ctx context.Context, req *connect.Request[pb.SaveConfigurationRequest]) (*connect.Response[pb.SaveConfigurationResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if req.Msg.Mutation == nil || req.Msg.SchemaVersion != 1 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "A version 1 configuration and mutation identity are required.", "Use schema_version=1, a UUID-v7 request ID, and the current expected revision."), correlation)
	}
	kind, err := rpc.Kind(req.Msg.Kind)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if kind == domain.AccountKind || kind == domain.ProviderKind {
		unlock, err := s.lockAccounts(ctx)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		defer unlock()
	}
	result, err := SaveConfiguration(ctx, s.Store, ConfigurationMutation{RequestID: domain.ID(req.Msg.Mutation.RequestId), ID: domain.ID(req.Msg.Mutation.Id), ExpectedRevision: req.Msg.Mutation.ExpectedRevision, Kind: kind, Document: req.Msg.DocumentJson})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var record store.Record
	if err := json.Unmarshal(result.Data, &record); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if record.Kind == "" {
		return nil, rpc.Error(domain.Fail(domain.NotFound, "The accepted entity was subsequently deleted.", "The original request cannot recreate it; use a new request ID for new work."), correlation)
	}
	if kind == domain.ProviderKind {
		current, err := s.Store.Get(ctx, domain.ProviderKind, record.ID)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		provider, err := store.Decode[domain.Provider](current)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		if !provider.Discovery {
			s.cancelCatalogChecks(record.ID)
		}
	}
	message := &pb.SaveConfigurationResponse{RequestId: string(result.RequestID), Replayed: result.Replayed}
	if record.Kind == domain.JobKind {
		current, err := s.Store.Get(ctx, domain.JobKind, record.ID)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		message.Job = rpc.Resource(current)
		job, err := store.Decode[domain.Job](current)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		if job.State == domain.JobSucceeded {
			var output repositorySaveOutput
			if err := domain.Decode(job.Output, &output); err != nil {
				return nil, rpc.Error(err, correlation)
			}
			saved, err := s.Store.Get(ctx, domain.RepositoryKind, output.ID)
			if err != nil {
				return nil, rpc.Error(err, correlation)
			}
			message.Resource = rpc.Resource(saved)
		}
	} else {
		message.Resource = rpc.Resource(record)
	}
	response := connect.NewResponse(message)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) PreviewRouting(ctx context.Context, req *connect.Request[pb.PreviewRoutingRequest]) (*connect.Response[pb.PreviewRoutingResponse], error) {
	route, err := PreviewRouting(ctx, s.Store, domain.ID(req.Msg.AgentId), domain.ID(req.Msg.ProjectId))
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	raw, err := json.Marshal(route)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.PreviewRoutingResponse{RouteJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) CreateBackup(ctx context.Context, req *connect.Request[pb.CreateBackupRequest]) (*connect.Response[pb.CreateBackupResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	// Persist the operation identity before filesystem work. Retries reuse the
	// same backup path and validate a completed file instead of creating another.
	receipt, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "backup.create", struct{}{}, func(*store.Tx) (any, error) {
		return struct {
			ID domain.ID `json:"id"`
		}{domain.NewID()}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var accepted struct {
		ID domain.ID `json:"id"`
	}
	if err := domain.Decode(receipt.Data, &accepted); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	id, err := s.Store.BackupID(ctx, accepted.ID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.CreateBackupResponse{Id: string(id), RequestId: req.Msg.RequestId, Replayed: receipt.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) DeleteConfiguration(ctx context.Context, req *connect.Request[pb.DeleteConfigurationRequest]) (*connect.Response[pb.DeleteConfigurationResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	meta := req.Msg.Mutation
	if meta == nil || meta.Id == "" || meta.ExpectedRevision == 0 {
		return nil, rpc.Error(domain.Fail(domain.MissingInput, "Deletion requires a request ID, entity ID, and expected revision.", "Read the current entity before deleting it."), correlation)
	}
	kind, err := rpc.Kind(req.Msg.Kind)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	switch kind {
	case domain.ProjectKind, domain.RepositoryKind, domain.AgentKind, domain.AccountKind, domain.ProviderKind, domain.ModelKind, domain.TemplateKind:
	default:
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "This entity cannot be deleted through configuration.", "Use its dedicated lifecycle operation."), correlation)
	}
	input := struct {
		ID       string      `json:"id"`
		Revision uint64      `json:"revision"`
		Kind     domain.Kind `json:"kind"`
	}{meta.Id, meta.ExpectedRevision, kind}
	if kind == domain.AccountKind {
		unlock, err := s.lockAccounts(ctx)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		defer unlock()
		_, replayed, err := s.Store.Replay(ctx, domain.ID(meta.RequestId), "configuration.delete", input)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		if !replayed {
			var keyless bool
			err = s.Store.Read(ctx, func(tx *store.Tx) error {
				if err := tx.Authorize(); err != nil {
					return err
				}
				if err := validateDeletion(tx, kind, domain.ID(meta.Id)); err != nil {
					return err
				}
				_, account, err := accountFromTx(tx, domain.ID(meta.Id), meta.ExpectedRevision)
				if err != nil {
					return err
				}
				keyless, err = accountWithoutCredentials(tx, account)
				return err
			})
			if err != nil {
				return nil, rpc.Error(err, correlation)
			}
			if !keyless {
				vault, err := s.secrets()
				if err != nil {
					return nil, rpc.Error(err, correlation)
				}
				refs, err := vault.UnremovedReferences(ctx, domain.ID(meta.Id))
				if err != nil {
					return nil, rpc.Error(err, correlation)
				}
				if len(refs) != 0 {
					return nil, rpc.Error(domain.Fail(domain.Conflict, "The account retains protected credential intents.", "Disconnect the account and complete credential cleanup before deleting it."), correlation)
				}
			}
		}
	}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "configuration.delete", input, func(tx *store.Tx) (any, error) {
		if err := validateDeletion(tx, kind, domain.ID(meta.Id)); err != nil {
			return nil, err
		}
		if err := disableReferencedSchedules(tx, kind, domain.ID(meta.Id)); err != nil {
			return nil, err
		}
		if err := tx.Delete(kind, domain.ID(meta.Id), meta.ExpectedRevision); err != nil {
			return nil, err
		}
		return struct {
			ID      string `json:"id"`
			Deleted bool   `json:"deleted"`
		}{meta.Id, true}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "configuration_deleted", "kind", kind, "entity_id", meta.Id, "request_id", meta.RequestId, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.DeleteConfigurationResponse{Id: meta.Id, RequestId: meta.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func validateDeletion(tx *store.Tx, kind domain.Kind, id domain.ID) error {
	existing, err := tx.Get(kind, id)
	if err != nil {
		return err
	}
	conflict := func() error {
		return domain.Fail(domain.Conflict, "This configuration is still referenced.", "Reconfigure its dependents before deleting it.")
	}
	if kind == domain.AccountKind {
		account, err := store.Decode[domain.Account](existing)
		if err != nil {
			return err
		}
		if account.Health != domain.AccountDisconnected || account.Connection != nil || account.Removal != nil {
			return domain.Fail(domain.Conflict, "Connected accounts require credential and device cleanup before deletion.", "Disconnect the account and complete its protected-resource cleanup first.")
		}
	}
	for _, ownerKind := range []domain.Kind{domain.ProjectKind, domain.AgentKind, domain.ModelKind, domain.AccountKind} {
		records, err := all(tx, ownerKind)
		if err != nil {
			return err
		}
		for _, record := range records {
			if record.ID == id {
				continue
			}
			switch ownerKind {
			case domain.ProjectKind:
				project, err := store.Decode[domain.Project](record)
				if err != nil {
					return err
				}
				if kind == domain.AccountKind && slices.Contains(project.Accounts.IDs, id) {
					return conflict()
				}
				if kind == domain.RepositoryKind {
					for _, ref := range project.Repositories {
						if ref == id {
							return conflict()
						}
					}
				}
			case domain.AgentKind:
				agent, err := store.Decode[domain.Agent](record)
				if err != nil {
					return err
				}
				if kind == domain.AccountKind {
					for _, account := range agent.Accounts {
						if account.ID == id {
							return conflict()
						}
					}
				}
				if kind == domain.ModelKind && agent.ModelID == id {
					return conflict()
				}
				if kind == domain.TemplateKind {
					for _, ref := range agent.Templates {
						if ref == id {
							return conflict()
						}
					}
				}
			case domain.ModelKind:
				model, err := store.Decode[domain.Model](record)
				if err != nil {
					return err
				}
				if kind == domain.ProviderKind && model.ProviderID == id {
					return conflict()
				}
			case domain.AccountKind:
				account, err := store.Decode[domain.Account](record)
				if err != nil {
					return err
				}
				if kind == domain.ProviderKind && account.ProviderID == id {
					return conflict()
				}
			}
		}
	}
	if kind == domain.AccountKind {
		filter := store.Filter{Kind: domain.SessionKind, Limit: store.MaxPage}
		count := 0
		for {
			records, err := tx.List(filter)
			if err != nil {
				return err
			}
			count += len(records)
			if count > 10000 {
				return domain.Fail(domain.ResourceExhausted, "Account reference validation exceeded its session bound.", "Reduce the retained scope before deleting the account.")
			}
			for _, record := range records {
				session, err := store.Decode[domain.Session](record)
				if err != nil {
					return err
				}
				if session.CurrentExecution != nil && session.CurrentExecution.AccountID == id {
					return conflict()
				}
				if initial := session.InitialExecution; initial != nil {
					if initial.InitialAccountID == id || initial.Route.Selected == id {
						return conflict()
					}
					for _, account := range initial.Configuration.Accounts {
						if account.ID == id {
							return conflict()
						}
					}
					for _, candidate := range initial.Route.Candidates {
						if candidate.ID == id {
							return conflict()
						}
					}
					if session.ProjectID != "" {
						policy, err := tx.ExecutionProjectPolicy(session)
						if err != nil {
							return err
						}
						if slices.Contains(policy.Accounts.IDs, id) {
							return conflict()
						}
					}
				}
			}
			if len(records) < filter.Limit {
				break
			}
			filter.After = records[len(records)-1].ID
		}
	}
	return nil
}
