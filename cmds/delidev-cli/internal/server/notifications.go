package server

import (
	"context"
	"google.golang.org/protobuf/encoding/protojson"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func notificationPreferencesWire(v domain.NotificationPreferences) *pb.NotificationPreferences {
	value := &pb.NotificationPreferences{Revision: v.Revision, Interactions: v.Interactions, Terminals: v.Terminals}
	if v.Situations != nil {
		p := v.Situations
		value.Situations = &pb.SituationNotificationPreferences{Questions: p.Questions, Approvals: p.Approvals, Succeeded: p.Succeeded, Failed: p.Failed, Stopped: p.Stopped, ServerLost: p.ServerLost, ServerRestored: p.ServerRestored, WorkerUnavailable: p.WorkerUnavailable, WorkerAvailable: p.WorkerAvailable, QuotaExhausted: p.QuotaExhausted, ScheduleStartFailed: p.ScheduleStartFailed, ScheduleOffline: p.ScheduleOffline}
	}
	return value
}
func notificationPreferencesDomain(v *pb.NotificationPreferences) domain.NotificationPreferences {
	value := domain.NotificationPreferences{Revision: v.Revision, Interactions: v.Interactions, Terminals: v.Terminals}
	if v.Situations != nil {
		p := v.Situations
		value.Situations = &domain.SituationNotificationPreferences{Questions: p.Questions, Approvals: p.Approvals, Succeeded: p.Succeeded, Failed: p.Failed, Stopped: p.Stopped, ServerLost: p.ServerLost, ServerRestored: p.ServerRestored, WorkerUnavailable: p.WorkerUnavailable, WorkerAvailable: p.WorkerAvailable, QuotaExhausted: p.QuotaExhausted, ScheduleStartFailed: p.ScheduleStartFailed, ScheduleOffline: p.ScheduleOffline}
	}
	return value
}
func notificationCandidateWire(v domain.NotificationCandidate) *pb.NotificationCandidate {
	kind := map[domain.NotificationKind]pb.NotificationKind{domain.SubscriptionRecoveryNotification: pb.NotificationKind_NOTIFICATION_KIND_SUBSCRIPTION_RECOVERY, domain.RequestNotification: pb.NotificationKind_NOTIFICATION_KIND_REQUEST, domain.SucceededNotification: pb.NotificationKind_NOTIFICATION_KIND_SUCCEEDED, domain.FailedNotification: pb.NotificationKind_NOTIFICATION_KIND_FAILED, domain.StoppedNotification: pb.NotificationKind_NOTIFICATION_KIND_STOPPED, domain.QuestionNotification: pb.NotificationKind_NOTIFICATION_KIND_QUESTION, domain.ApprovalNotification: pb.NotificationKind_NOTIFICATION_KIND_APPROVAL, domain.WorkerUnavailableNotification: pb.NotificationKind_NOTIFICATION_KIND_WORKER_UNAVAILABLE, domain.WorkerAvailableNotification: pb.NotificationKind_NOTIFICATION_KIND_WORKER_AVAILABLE, domain.QuotaExhaustedNotification: pb.NotificationKind_NOTIFICATION_KIND_QUOTA_EXHAUSTED, domain.ScheduleStartFailedNotification: pb.NotificationKind_NOTIFICATION_KIND_SCHEDULE_START_FAILED, domain.ScheduleServerOfflineNotification: pb.NotificationKind_NOTIFICATION_KIND_SCHEDULE_SERVER_OFFLINE, domain.ScheduleWorkerOfflineNotification: pb.NotificationKind_NOTIFICATION_KIND_SCHEDULE_WORKER_OFFLINE}[v.Kind]
	return &pb.NotificationCandidate{InboxId: string(v.InboxID), SessionId: string(v.SessionID), Kind: kind, AccountId: string(v.AccountID), MachineId: string(v.MachineID), OccurrenceId: string(v.OccurrenceID)}
}
func notificationDeliveryWire(v domain.NotificationDelivery) *pb.NotificationDelivery {
	state := map[domain.NotificationState]pb.NotificationState{domain.NotificationClaimed: pb.NotificationState_NOTIFICATION_STATE_CLAIMED, domain.NotificationSubmitted: pb.NotificationState_NOTIFICATION_STATE_SUBMITTED, domain.NotificationDenied: pb.NotificationState_NOTIFICATION_STATE_DENIED, domain.NotificationFailed: pb.NotificationState_NOTIFICATION_STATE_FAILED, domain.NotificationUncertain: pb.NotificationState_NOTIFICATION_STATE_UNCERTAIN}[v.State]
	return &pb.NotificationDelivery{Candidate: notificationCandidateWire(v.NotificationCandidate), ClaimId: string(v.ClaimID), State: state}
}
func notificationReportState(v pb.NotificationState) (domain.NotificationState, error) {
	state := map[pb.NotificationState]domain.NotificationState{pb.NotificationState_NOTIFICATION_STATE_SUBMITTED: domain.NotificationSubmitted, pb.NotificationState_NOTIFICATION_STATE_DENIED: domain.NotificationDenied, pb.NotificationState_NOTIFICATION_STATE_FAILED: domain.NotificationFailed, pb.NotificationState_NOTIFICATION_STATE_UNCERTAIN: domain.NotificationUncertain}[v]
	if !state.Reportable() {
		return "", domain.Fail(domain.InvalidArgument, "Unknown notification report state.", "Use submitted, denied, failed or uncertain; OS acceptance does not prove human observation.")
	}
	return state, nil
}
func (s *Service) readNotificationPreferences(ctx context.Context) (domain.NotificationPreferences, error) {
	var value domain.NotificationPreferences
	err := s.Store.Read(ctx, func(tx *store.Tx) error { var err error; value, err = tx.NotificationPreferences(); return err })
	return value, err
}
func (s *Service) GetNotificationPreferences(ctx context.Context, req *connect.Request[pb.GetNotificationPreferencesRequest]) (*connect.Response[pb.GetNotificationPreferencesResponse], error) {
	if req.Msg.Situations {
		if err := s.Store.EnsureSituationNotificationPreferences(ctx); err != nil {
			return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
		}
	}
	value, err := s.readNotificationPreferences(ctx)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.GetNotificationPreferencesResponse{Preferences: notificationPreferencesWire(value)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) SetNotificationPreferences(ctx context.Context, req *connect.Request[pb.SetNotificationPreferencesRequest]) (*connect.Response[pb.SetNotificationPreferencesResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := inboxActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var result store.Result
	if req.Msg.Changes != nil {
		if req.Msg.Preferences != nil || req.Msg.ExpectedRevision == 0 {
			return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Use one revision-bound notification changes shape.", "Do not mix complete preferences and changes."), correlation)
		}
		raw, marshalErr := protojson.Marshal(req.Msg.Changes)
		if marshalErr != nil {
			return nil, rpc.Error(domain.SafeError(marshalErr), correlation)
		}
		identity := struct {
			Actor    domain.Principal
			Revision uint64
			Changes  string
		}{actor, req.Msg.ExpectedRevision, string(raw)}
		result, err = s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "notification.preferences.changes", identity, func(tx *store.Tx) (any, error) {
			v, err := tx.InitializeSituationNotificationPreferences()
			if err != nil {
				return nil, err
			}
			if v.Revision != identity.Revision || v.Situations == nil {
				return nil, domain.Fail(domain.Conflict, "Notification preferences changed or are unavailable.", "Read current individual choices and their revision before a new change.")
			}
			changes := req.Msg.Changes
			if changes.Questions != nil {
				v.Situations.Questions = *changes.Questions
			}
			if changes.Approvals != nil {
				v.Situations.Approvals = *changes.Approvals
			}
			if changes.Succeeded != nil {
				v.Situations.Succeeded = *changes.Succeeded
			}
			if changes.Failed != nil {
				v.Situations.Failed = *changes.Failed
			}
			if changes.Stopped != nil {
				v.Situations.Stopped = *changes.Stopped
			}
			if changes.ServerLost != nil {
				v.Situations.ServerLost = *changes.ServerLost
			}
			if changes.ServerRestored != nil {
				v.Situations.ServerRestored = *changes.ServerRestored
			}
			if changes.WorkerUnavailable != nil {
				v.Situations.WorkerUnavailable = *changes.WorkerUnavailable
			}
			if changes.WorkerAvailable != nil {
				v.Situations.WorkerAvailable = *changes.WorkerAvailable
			}
			if changes.QuotaExhausted != nil {
				v.Situations.QuotaExhausted = *changes.QuotaExhausted
			}
			if changes.ScheduleStartFailed != nil {
				v.Situations.ScheduleStartFailed = *changes.ScheduleStartFailed
			}
			if changes.ScheduleOffline != nil {
				v.Situations.ScheduleOffline = *changes.ScheduleOffline
			}
			_, err = tx.SetNotificationPreferences(v)
			return struct{}{}, err
		})
	} else {
		if req.Msg.ExpectedRevision != 0 {
			return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "An expected revision requires typed notification changes.", "Use the complete preferences revision otherwise."), correlation)
		}
		if req.Msg.Preferences == nil {
			return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Notification preferences are required.", "Read this client's current preferences and retain their revision."), correlation)
		}
		v := req.Msg.Preferences
		identity := struct {
			Actor       domain.Principal
			Preferences domain.NotificationPreferences
		}{actor, notificationPreferencesDomain(v)}
		result, err = s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "notification.preferences", identity, func(tx *store.Tx) (any, error) {
			_, err := tx.SetNotificationPreferences(identity.Preferences)
			return struct{}{}, err
		})
	}

	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	current, err := s.readNotificationPreferences(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "notification_preferences_recorded", "correlation_id", correlation, "request_id", result.RequestID, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.SetNotificationPreferencesResponse{Preferences: notificationPreferencesWire(current), RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) ListNotificationCandidates(ctx context.Context, req *connect.Request[pb.ListNotificationCandidatesRequest]) (*connect.Response[pb.ListNotificationCandidatesResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	limit := req.Msg.Limit
	if limit == 0 {
		limit = 20
	}
	if limit > 50 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Invalid notification batch size.", "Use at most 50 candidates."), req.Header().Get(rpc.CorrelationHeader))
	}
	value := &pb.ListNotificationCandidatesResponse{}
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		rows, more, err := tx.NotificationCandidates(int(limit))
		if err != nil {
			return err
		}
		value.More = more
		for _, row := range rows {
			value.Candidates = append(value.Candidates, notificationCandidateWire(row))
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(value)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

type notificationClaimReceipt struct {
	InboxID domain.ID `json:"inbox_id"`
	Created bool      `json:"created"`
}

func (s *Service) readNotificationDelivery(ctx context.Context, inbox domain.ID) (domain.NotificationDelivery, error) {
	var value domain.NotificationDelivery
	err := s.Store.Read(ctx, func(tx *store.Tx) error { var err error; value, err = tx.NotificationDelivery(inbox); return err })
	return value, err
}
func (s *Service) GetNotificationDelivery(ctx context.Context, req *connect.Request[pb.GetNotificationDeliveryRequest]) (*connect.Response[pb.GetNotificationDeliveryResponse], error) {
	value, err := s.readNotificationDelivery(ctx, domain.ID(req.Msg.InboxId))
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.GetNotificationDeliveryResponse{Delivery: notificationDeliveryWire(value)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) ClaimNotification(ctx context.Context, req *connect.Request[pb.ClaimNotificationRequest]) (*connect.Response[pb.ClaimNotificationResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := inboxActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	identity := struct {
		Actor   domain.Principal
		InboxID domain.ID
	}{actor, domain.ID(req.Msg.InboxId)}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "notification.claim", identity, func(tx *store.Tx) (any, error) {
		value, created, err := tx.ClaimNotification(identity.InboxID, domain.ID(req.Msg.RequestId))
		return notificationClaimReceipt{value.InboxID, created}, err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt notificationClaimReceipt
	if domain.Decode(result.Data, &receipt) != nil || receipt.InboxID != identity.InboxID {
		return nil, rpc.Error(domain.Fail(domain.RecoveryRequired, "The original notification claim is inconsistent.", "Keep the retained inbox; never repeat uncertain native presentation."), correlation)
	}
	current, err := s.readNotificationDelivery(ctx, identity.InboxID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	mayPresent := receipt.Created && !result.Replayed && current.ClaimID == result.RequestID && current.State == domain.NotificationClaimed
	s.logger.InfoContext(ctx, "notification_claim_recorded", "correlation_id", correlation, "inbox_id", current.InboxID, "request_id", result.RequestID, "may_present", mayPresent, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ClaimNotificationResponse{Delivery: notificationDeliveryWire(current), MayPresent: mayPresent, RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) ReportNotification(ctx context.Context, req *connect.Request[pb.ReportNotificationRequest]) (*connect.Response[pb.ReportNotificationResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := inboxActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	state, err := notificationReportState(req.Msg.State)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	identity := struct {
		Actor            domain.Principal
		InboxID, ClaimID domain.ID
		State            domain.NotificationState
	}{actor, domain.ID(req.Msg.InboxId), domain.ID(req.Msg.ClaimId), state}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "notification.report", identity, func(tx *store.Tx) (any, error) {
		value, err := tx.ReportNotification(identity.InboxID, identity.ClaimID, state)
		return struct {
			InboxID domain.ID `json:"inbox_id"`
		}{value.InboxID}, err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	current, err := s.readNotificationDelivery(ctx, identity.InboxID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "notification_presentation_recorded", "correlation_id", correlation, "inbox_id", current.InboxID, "request_id", result.RequestID, "state", current.State, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ReportNotificationResponse{Delivery: notificationDeliveryWire(current), RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
