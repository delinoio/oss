package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type scheduleReceipt struct {
	ID domain.ID `json:"schedule_id"`
}

func scheduleActor(ctx context.Context) (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return actor, domain.Fail(domain.PermissionDenied, "Only an owner or paired client can manage schedules.", "Use an authorized product client; Worker credentials cannot configure or initiate scheduled work.")
	}
	return actor, nil
}

func (s *Service) readSchedule(ctx context.Context, id domain.ID) (*pb.Resource, error) {
	var result *pb.Resource
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		r, err := tx.Get(domain.ScheduleKind, id)
		if err != nil {
			return err
		}
		value, err := store.Decode[domain.Schedule](r)
		if err != nil {
			return err
		}
		if err := value.Validate(); err != nil {
			return err
		}
		result = rpc.Resource(r)
		return nil
	})
	return result, err
}

func (s *Service) scheduleReceiptResource(ctx context.Context, result store.Result) (*pb.Resource, error) {
	var refs scheduleReceipt
	if err := json.Unmarshal(result.Data, &refs); err != nil {
		return nil, err
	}
	if refs.ID == "" {
		return nil, domain.Fail(domain.NotFound, "The accepted schedule was deleted.", "The original request cannot recreate deleted configuration.")
	}
	return s.readSchedule(ctx, refs.ID)
}

func (s *Service) SaveSchedule(ctx context.Context, req *connect.Request[pb.SaveScheduleRequest]) (*connect.Response[pb.SaveScheduleResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := scheduleActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	if meta == nil || req.Msg.SchemaVersion != 1 || (meta.Id == "" && meta.ExpectedRevision != 0) {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "A version-1 schedule definition and mutation identity are required.", "Create with revision zero; edits require the schedule ID and current resource revision."), correlation)
	}
	if err := domain.ID(meta.RequestId).Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if meta.Id != "" {
		if err := domain.ID(meta.Id).Validate(); err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	var definition domain.ScheduleDefinition
	if err := domain.Decode(req.Msg.DefinitionJson, &definition); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	definition.ApplyDefaults()
	if err := definition.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var proof *domain.LocalOrigin
	var digest [sha256.Size]byte
	if req.Msg.LocalWorkerToken != "" {
		proof, digest, err = s.authenticateLocalOrigin(ctx, definition.Selection(), req.Msg.LocalWorkerToken)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	identity := struct {
		ID         domain.ID
		Revision   uint64
		Definition domain.ScheduleDefinition
		Actor      domain.Principal
		Proof      *domain.LocalOrigin
	}{domain.ID(meta.Id), meta.ExpectedRevision, definition, actor, proof}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "schedule.save", identity, func(tx *store.Tx) (any, error) {
		id := domain.ID(meta.Id)
		if id == "" {
			id = domain.NewID()
		}
		value := domain.Schedule{Definition: definition, ConfigurationRevision: 1, CreatedBy: actor.DeviceID}
		var previous domain.Schedule
		if meta.ExpectedRevision != 0 {
			r, err := tx.Get(domain.ScheduleKind, id)
			if err != nil {
				return nil, err
			}
			if r.Revision != meta.ExpectedRevision {
				return nil, scheduleUnchanged
			}
			previous, err = store.Decode[domain.Schedule](r)
			if err != nil {
				return nil, err
			}
			value.ConfigurationRevision = previous.ConfigurationRevision + 1
			value.CreatedBy = previous.CreatedBy
			value.LastOccurrence = previous.LastOccurrence
		}
		if proof != nil {
			current, err := tx.Authenticate(digest[:])
			if err != nil {
				return nil, err
			}
			if current.Type != domain.WorkerDevice || current.MachineID != proof.MachineID || current.DeviceID != proof.DeviceID {
				return nil, localOriginRequired()
			}
			value.LocalOrigin = proof
		} else if definition.Workspace == domain.Local {
			if meta.ExpectedRevision == 0 || previous.Definition.Workspace != domain.Local || previous.Definition.MachineID != definition.MachineID {
				return nil, localOriginRequired()
			}
			value.LocalOrigin = previous.LocalOrigin
		}
		selection := definition.Selection()
		if err := validateSessionSelection(tx, selection); err != nil {
			return nil, err
		}
		if err := validateLocalOrigin(tx, acceptedSession(selection, value.LocalOrigin, value.CreatedBy)); err != nil {
			return nil, err
		}
		next, err := definition.NextRun(time.Now().UTC())
		if err != nil {
			return nil, err
		}
		if definition.Enabled {
			value.NextRunAt = &next
			// Prompt/selection edits preserve an already published due instant.
			// Changing or re-enabling the calendar starts strictly in the future.
			if previous.Definition.Enabled && previous.Definition.Cron == definition.Cron && previous.Definition.Timezone == definition.Timezone {
				value.NextRunAt = previous.NextRunAt
			}
		}
		if _, err := tx.PutSchedule(id, meta.ExpectedRevision, value); err != nil {
			return nil, err
		}
		return scheduleReceipt{ID: id}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	resource, err := s.scheduleReceiptResource(ctx, result)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "schedule_saved", "schedule_id", resource.Id, "revision", resource.Revision, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.SaveScheduleResponse{Schedule: resource, RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) GetSchedule(ctx context.Context, req *connect.Request[pb.GetScheduleRequest]) (*connect.Response[pb.GetScheduleResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := scheduleActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	resource, err := s.readSchedule(ctx, domain.ID(req.Msg.Id))
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.GetScheduleResponse{Schedule: resource})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) ControlSchedule(ctx context.Context, req *connect.Request[pb.ControlScheduleRequest]) (*connect.Response[pb.ControlScheduleResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := scheduleActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	if err := validateSessionMutation(meta); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if req.Msg.Action != pb.ScheduleAction_SCHEDULE_ACTION_PAUSE && req.Msg.Action != pb.ScheduleAction_SCHEDULE_ACTION_RESUME {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "An explicit schedule action is required.", "Select pause or resume."), correlation)
	}
	enabled := req.Msg.Action == pb.ScheduleAction_SCHEDULE_ACTION_RESUME
	identity := struct {
		ID       domain.ID
		Revision uint64
		Enabled  bool
		Actor    domain.Principal
	}{domain.ID(meta.Id), meta.ExpectedRevision, enabled, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "schedule.control", identity, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.ScheduleKind, domain.ID(meta.Id))
		if err != nil {
			return nil, err
		}
		if r.Revision != meta.ExpectedRevision {
			return nil, scheduleUnchanged
		}
		v, err := store.Decode[domain.Schedule](r)
		if err != nil {
			return nil, err
		}
		if enabled && v.Problem != nil {
			return nil, scheduleReconfigurationRequired()
		}
		if v.Definition.Enabled == enabled {
			return scheduleReceipt{ID: r.ID}, nil
		}
		v.Definition.Enabled = enabled
		v.ConfigurationRevision++
		v.NextRunAt = nil
		if enabled {
			if err := validateSessionSelection(tx, v.Definition.Selection()); err != nil {
				return nil, err
			}
			if err := validateLocalOrigin(tx, acceptedSession(v.Definition.Selection(), v.LocalOrigin, v.CreatedBy)); err != nil {
				return nil, err
			}
			next, err := v.Definition.NextRun(time.Now().UTC())
			if err != nil {
				return nil, err
			}
			v.NextRunAt = &next
		}
		if _, err := tx.PutSchedule(r.ID, r.Revision, v); err != nil {
			return nil, err
		}
		return scheduleReceipt{ID: r.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	resource, err := s.scheduleReceiptResource(ctx, result)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "schedule_controlled", "schedule_id", meta.Id, "enabled", enabled, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ControlScheduleResponse{Schedule: resource, RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func scheduleReconfigurationRequired() error {
	return domain.Fail(domain.Conflict, "The schedule requires reconfiguration before activation.", "Edit its selected project, Agent Worker and execution Worker to clear the retained disabling problem.")
}

func (s *Service) DeleteSchedule(ctx context.Context, req *connect.Request[pb.DeleteScheduleRequest]) (*connect.Response[pb.DeleteScheduleResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := scheduleActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	if err := validateSessionMutation(meta); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	identity := struct {
		ID       domain.ID
		Revision uint64
		Actor    domain.Principal
	}{domain.ID(meta.Id), meta.ExpectedRevision, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "schedule.delete", identity, func(tx *store.Tx) (any, error) {
		if err := tx.Delete(domain.ScheduleKind, domain.ID(meta.Id), meta.ExpectedRevision); err != nil {
			return nil, err
		}
		return scheduleReceipt{ID: domain.ID(meta.Id)}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "schedule_deleted", "schedule_id", meta.Id, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.DeleteScheduleResponse{Id: meta.Id, RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) RunScheduleNow(ctx context.Context, req *connect.Request[pb.RunScheduleNowRequest]) (*connect.Response[pb.RunScheduleNowResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := scheduleActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	meta := req.Msg.Mutation
	if err := validateSessionMutation(meta); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	// Do not load a new revision before receipt lookup: an exact retry still
	// addresses the original acceptance after later timer/configuration changes.
	result, err := s.acceptScheduleOccurrence(ctx, store.Record{ID: domain.ID(meta.Id), Kind: domain.ScheduleKind, Revision: meta.ExpectedRevision}, domain.ManualOccurrence, domain.ID(meta.RequestId), time.Now)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var refs occurrenceReceipt
	if err := json.Unmarshal(result.Data, &refs); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if refs.OccurrenceID == "" {
		return nil, rpc.Error(domain.Fail(domain.NotFound, "The original schedule receipt was removed with its source.", "Inspect retained occurrence history; an old request cannot recreate work."), correlation)
	}
	response := connect.NewResponse(&pb.RunScheduleNowResponse{RequestId: string(result.RequestID), Replayed: result.Replayed})
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		r, err := tx.Get(domain.OccurrenceKind, refs.OccurrenceID)
		if err != nil {
			return err
		}
		v, err := store.Decode[domain.ScheduleOccurrence](r)
		if err != nil {
			return err
		}
		if v.ScheduleID != refs.ScheduleID || v.ScheduleID != domain.ID(meta.Id) {
			return domain.Fail(domain.RecoveryRequired, "The occurrence receipt has inconsistent ownership.", "Preserve its original schedule and session records.")
		}
		response.Msg.Occurrence = rpc.Resource(r)
		if v.SessionID != "" {
			session, err := tx.Get(domain.SessionKind, v.SessionID)
			if err != nil {
				return err
			}
			response.Msg.Session = rpc.Resource(session)
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func scheduleLimit(value uint32) (int, error) {
	if value == 0 {
		return 50, nil
	}
	if value > store.MaxPage {
		return 0, domain.Fail(domain.InvalidArgument, "Invalid schedule page size.", "Use between 1 and 200 records per page.")
	}
	return int(value), nil
}

func (s *Service) ListSchedules(ctx context.Context, req *connect.Request[pb.ListSchedulesRequest]) (*connect.Response[pb.ListSchedulesResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := scheduleActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	limit, err := scheduleLimit(req.Msg.PageSize)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	f := store.ScheduleFilter{ProjectID: domain.ID(req.Msg.ProjectId), Enabled: req.Msg.Enabled, Limit: limit}
	if err := f.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	enabled := "all"
	if f.Enabled != nil {
		enabled = fmt.Sprint(*f.Enabled)
	}
	scope := fmt.Sprintf("schedules:%s:%s", f.ProjectID, enabled)
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		if cursor.After == "" {
			return nil, rpc.Error(domain.Fail(domain.CursorExpired, "The schedule cursor has no position.", "Restart pagination."), correlation)
		}
		f.After, f.Epoch = cursor.After, cursor.Sequence
	}
	response := connect.NewResponse(&pb.ListSchedulesResponse{})
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		rows, more, epoch, err := tx.SchedulePage(f)
		if err != nil {
			return err
		}
		for _, r := range rows {
			response.Msg.Schedules = append(response.Msg.Schedules, rpc.Resource(r))
		}
		if more && len(rows) > 0 {
			response.Msg.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: rows[len(rows)-1].ID, Sequence: epoch})
		}
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) ListScheduleOccurrences(ctx context.Context, req *connect.Request[pb.ListScheduleOccurrencesRequest]) (*connect.Response[pb.ListScheduleOccurrencesResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := scheduleActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	limit, err := scheduleLimit(req.Msg.PageSize)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	schedule := domain.ID(req.Msg.ScheduleId)
	if err := schedule.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	scope := "schedule-history:" + string(schedule)
	var after domain.ID
	var epoch uint64
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		if cursor.After == "" {
			return nil, rpc.Error(domain.Fail(domain.CursorExpired, "The occurrence cursor has no position.", "Restart history pagination."), correlation)
		}
		after, epoch = cursor.After, cursor.Sequence
	}
	response := connect.NewResponse(&pb.ListScheduleOccurrencesResponse{})
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		rows, more, current, err := tx.ScheduleOccurrencePage(schedule, after, limit, epoch)
		if err != nil {
			return err
		}
		for _, r := range rows {
			response.Msg.Occurrences = append(response.Msg.Occurrences, rpc.Resource(r))
		}
		if more && len(rows) > 0 {
			response.Msg.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: rows[len(rows)-1].ID, Sequence: current})
		}
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) GetScheduleOccurrence(ctx context.Context, req *connect.Request[pb.GetScheduleOccurrenceRequest]) (*connect.Response[pb.GetScheduleOccurrenceResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := scheduleActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := domain.ID(req.Msg.ScheduleId).Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.GetScheduleOccurrenceResponse{})
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		r, err := tx.Get(domain.OccurrenceKind, domain.ID(req.Msg.Id))
		if err != nil {
			return err
		}
		v, err := store.Decode[domain.ScheduleOccurrence](r)
		if err != nil {
			return err
		}
		if v.ScheduleID != domain.ID(req.Msg.ScheduleId) {
			return domain.Fail(domain.NotFound, "This occurrence does not belong to the selected schedule.", "Use its original schedule identity.")
		}
		response.Msg.Occurrence = rpc.Resource(r)
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

// Configuration deletion and future scheduling disablement share one transaction.
// Accepted occurrences/sessions are independent and are never rewritten here.
func disableReferencedSchedules(tx *store.Tx, kind domain.Kind, id domain.ID) error {
	if kind != domain.ProjectKind && kind != domain.AgentKind {
		return nil
	}
	records, err := all(tx, domain.ScheduleKind)
	if err != nil {
		return err
	}
	for _, r := range records {
		value, err := store.Decode[domain.Schedule](r)
		if err != nil {
			return err
		}
		if (kind != domain.ProjectKind || value.Definition.ProjectID != id) && (kind != domain.AgentKind || value.Definition.AgentID != id) {
			continue
		}
		value.Definition.Enabled = false
		value.NextRunAt = nil
		value.ConfigurationRevision++
		value.Problem = domain.Fail(domain.NotFound, "A selected schedule configuration was deleted.", "Edit this schedule to select an existing project and Agent Worker before enabling it or using Run now.")
		if _, err := tx.PutSchedule(r.ID, r.Revision, value); err != nil {
			return err
		}
	}
	return nil
}
