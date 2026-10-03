package server

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (s *Service) ListRequestDiagnostics(ctx context.Context, req *connect.Request[pb.ListRequestDiagnosticsRequest]) (*connect.Response[pb.ListRequestDiagnosticsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Only an owner or paired client can read request diagnostics.", "Use an authorized product client."), correlation)
	}
	session, execution := domain.ID(req.Msg.SessionId), domain.ID(req.Msg.ExecutionId)
	limit := req.Msg.PageSize
	if limit == 0 {
		limit = 50
	}
	if session.Validate() != nil || execution != "" && execution.Validate() != nil || limit > 100 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Invalid diagnostic selection.", "Select an original session and a page size from 1 to 100."), correlation)
	}
	scope := fmt.Sprintf("request-diagnostics:v1:%s:%s:%d", session, execution, limit)
	var after domain.ID
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		after = cursor.After
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result := &pb.ListRequestDiagnosticsResponse{}
	err := s.Store.Read(bounded, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		values, more, err := tx.ListRequestDiagnostics(session, execution, after, int(limit))
		if err != nil {
			return err
		}
		for _, v := range values {
			row := &pb.RequestDiagnostic{Id: string(v.ID), Revision: v.Revision, SessionId: string(v.SessionID), ExecutionId: string(v.ExecutionID), AccountId: string(v.AccountID), ConnectionId: string(v.ConnectionID), ProviderId: string(v.ProviderID), SubscriptionService: rpc.WireSubscriptionService(v.SubscriptionService), ModelId: string(v.ModelID), Source: pb.RequestDiagnosticSource(v.Source), Operation: pb.RequestDiagnosticOperation(v.Operation), State: pb.RequestDiagnosticState(v.State), Purpose: string(v.Purpose), Harness: string(v.Harness), InputId: string(v.InputID), PublicationRequestId: string(v.PublicationRequestID), CorrelationId: string(v.CorrelationID), NativeRequestId: v.NativeRequestID, NativeResponseId: v.NativeResponseID, ProviderRequestId: v.ProviderRequestID, NativeThreadId: v.NativeThreadID, NativeTurnId: v.NativeTurnID, RequestedEffort: v.RequestedEffort, RequestedServiceTier: v.RequestedServiceTier, EffectiveEffort: v.EffectiveEffort, EffectiveServiceTier: v.EffectiveServiceTier, ObservedAt: v.ObservedAt.Format(time.RFC3339Nano), DurationMs: v.DurationMS, HttpAttempted: v.HTTPAttempted, HttpStatus: v.HTTPStatus, ErrorCode: string(v.ErrorCode)}
			if v.FinishedAt != nil {
				finished := v.FinishedAt.Format(time.RFC3339Nano)
				row.FinishedAt = &finished
			}
			result.Records = append(result.Records, row)
		}
		if more {
			result.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: values[len(values)-1].ID})
		}
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.DebugContext(ctx, "request_diagnostics_read", "correlation_id", correlation, "session_id", session, "record_count", len(result.Records))
	response := connect.NewResponse(result)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

// This projection shares the original validated native event transaction. It
// never reads message bodies, usage samples, paths or mutable account labels.
func projectNativeDiagnostic(tx *store.Tx, input domain.ExecutionJobInput, event domain.ExecutionEvent, publication domain.ID) error {
	if event.Kind != domain.ExecutionThreadBound && event.Kind != domain.ExecutionInputAccepted && event.Kind != domain.ExecutionTurnFinished {
		return nil
	}
	var value domain.RequestDiagnostic
	var expected uint64
	if event.Kind == domain.ExecutionThreadBound {
		value = domain.RequestDiagnostic{ID: input.TurnRequestID, SessionID: input.SessionID, ExecutionID: input.ExecutionID, AccountID: input.AccountID, ConnectionID: input.ConnectionID, ProviderID: input.Configuration.ProviderID, SubscriptionService: input.Configuration.SubscriptionService, ModelID: input.Configuration.ModelID, Source: domain.DiagnosticNativeInput, Operation: domain.DiagnosticInput, State: domain.DiagnosticInProgress, Purpose: domain.ConversationUsage, Harness: input.Configuration.Harness, InputID: input.InputID, NativeRequestID: string(input.TurnRequestID), NativeThreadID: event.NativeThreadID, RequestedEffort: domain.DiagnosticEffort(input.Configuration.Effort), RequestedServiceTier: domain.DiagnosticServiceTier(input.Configuration.Options.ServiceTier), ObservedAt: time.Now().UTC()}
		if event.Observed.Effort != nil {
			value.EffectiveEffort = domain.DiagnosticEffort(*event.Observed.Effort)
		}
		if event.Observed.ServiceTier != nil {
			value.EffectiveServiceTier = domain.DiagnosticServiceTier(*event.Observed.ServiceTier)
		}
	} else {
		var err error
		value, err = tx.RequestDiagnostic(input.TurnRequestID)
		// Historical executions started before diagnostics existed. Missing
		// provenance stays unavailable; never backfill a guessed start/settings.
		if domain.SafeError(err).Code == domain.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		expected = value.Revision
		value.NativeTurnID = event.NativeTurnID
		if event.Kind == domain.ExecutionTurnFinished {
			finished := time.Now().UTC()
			value.FinishedAt = &finished
			value.ErrorCode = event.ProblemCode
			switch event.Outcome {
			case domain.ExecutionSucceeded:
				value.State = domain.DiagnosticSucceeded
			case domain.ExecutionStopped:
				value.State = domain.DiagnosticCanceled
			default:
				value.State = domain.RequestDiagnosticFailed
			}
		}
	}
	value.PublicationRequestID = publication
	return tx.PutRequestDiagnostic(value, expected)
}
