package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"connectrpc.com/connect"
	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/authn"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database"
	deckgithub "github.com/delinoio/oss/servers/devhud-deck/internal/github"
	"github.com/delinoio/oss/servers/devhud-deck/internal/query"
	"github.com/delinoio/oss/servers/devhud-deck/internal/rpcerr"
	"github.com/delinoio/oss/servers/devhud-deck/internal/security"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	refreshCacheWindow       = 5 * time.Minute
	refreshPreflightLifetime = 5 * time.Minute
	automaticViewWindow      = 30 * 24 * time.Hour
	refreshPreflightSize     = 163
)

type refreshRateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	requests map[string][]time.Time
}

func newRefreshRateLimiter(limit int, window time.Duration) *refreshRateLimiter {
	return &refreshRateLimiter{
		limit: limit, window: window, requests: make(map[string][]time.Time),
	}
}

func (limiter *refreshRateLimiter) Allow(actor string, now time.Time) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	cutoff := now.Add(-limiter.window)
	entries := limiter.requests[actor]
	first := 0
	for first < len(entries) && !entries[first].After(cutoff) {
		first++
	}
	entries = entries[first:]
	if len(entries) >= limiter.limit {
		limiter.requests[actor] = entries
		return false
	}
	limiter.requests[actor] = append(entries, now)
	return true
}

func (service *View) GetRefreshPreflight(
	ctx context.Context,
	request *connect.Request[deckv1.GetRefreshPreflightRequest],
) (*connect.Response[deckv1.GetRefreshPreflightResponse], error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	viewID, requestID, err := validateRefreshIdentity(
		request.Msg.GetViewId(), request.Msg.GetRefreshRequestId())
	if err != nil || !validRefreshTrace(
		request.Msg.GetOrigin(), request.Msg.GetClientKind()) {
		return nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	view, err := service.getAuthorizedView(ctx, viewer, viewID, false)
	if err != nil {
		return nil, err
	}
	organizationID, teamID, err := refreshBillingIDs(view.GetBilling())
	if err != nil || service.dependencies.LiveUsage == nil {
		return nil, rpcerr.New(connect.CodeUnavailable,
			deckv1.ErrorReason_ERROR_REASON_BILLING_CATALOG_UNAVAILABLE)
	}
	meter, err := service.dependencies.LiveUsage.RefreshMeter(ctx)
	if err != nil || !validRefreshMeter(meter) {
		return nil, rpcerr.New(connect.CodeUnavailable,
			deckv1.ErrorReason_ERROR_REASON_BILLING_CATALOG_UNAVAILABLE)
	}
	now := service.dependencies.Clock.Now().UTC()
	expiresAt := now.Add(refreshPreflightLifetime)
	token := encodeRefreshPreflight(
		service.dependencies.Hasher, viewer.Subject, viewID, requestID,
		organizationID, teamID, meter, request.Msg.GetOrigin(),
		request.Msg.GetClientKind(), expiresAt)
	return connect.NewResponse(&deckv1.GetRefreshPreflightResponse{
		ProviderRefreshPrice: &deckv1.UsdMicros{
			Value: contracts.ProviderRefreshPriceUSDMicros,
		},
		PreflightToken: token,
		ExpiresAt:      timestamppb.New(expiresAt),
	}), nil
}

func (service *View) RefreshView(
	ctx context.Context,
	request *connect.Request[deckv1.RefreshViewRequest],
) (*connect.Response[deckv1.RefreshViewResponse], error) {
	startedAt := service.dependencies.Clock.Now().UTC()
	metricOutcome := contracts.RefreshMetricUnknown
	defer func() {
		service.dependencies.RefreshMetrics.ObserveRefresh(
			metricOutcome,
			service.dependencies.Clock.Now().UTC().Sub(startedAt),
		)
	}()
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	forwardedToken, ok := authn.ForwardedDelibaseTokenFromContext(ctx)
	if !ok {
		return nil, rpcerr.New(connect.CodeUnauthenticated,
			deckv1.ErrorReason_ERROR_REASON_AUTHENTICATION_REQUIRED)
	}
	if service.dependencies.LiveUsage == nil {
		return nil, rpcerr.New(connect.CodeUnavailable,
			deckv1.ErrorReason_ERROR_REASON_BILLING_CATALOG_UNAVAILABLE)
	}
	viewID, requestID, err := validateRefreshIdentity(
		request.Msg.GetViewId(), request.Msg.GetRefreshRequestId())
	if err != nil || !validRefreshTrace(
		request.Msg.GetOrigin(), request.Msg.GetClientKind()) {
		return nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	view, err := service.getAuthorizedView(ctx, viewer, viewID, false)
	if err != nil {
		return nil, err
	}
	requestDigest := refreshRequestDigest(request.Msg)
	subjectHash := service.dependencies.Hasher.Sum(
		"refresh-subject", viewer.Subject)
	viewerHash := service.dependencies.Hasher.Sum(
		"snapshot-viewer", viewer.AccountID.String())

	// An exact durable replay is resolved before preflight expiry or a later
	// catalog change. It still requires current view authorization above.
	attempt, lookupErr := service.dependencies.Store.GetRefreshAttempt(
		ctx, subjectHash, requestID, requestDigest)
	if lookupErr == nil && attempt.State == database.RefreshAttemptCompleted {
		replayed := proto.Clone(attempt.Response).(*deckv1.RefreshViewResponse)
		replayed.Idempotency.Replayed = true
		return connect.NewResponse(replayed), nil
	}
	if lookupErr != nil && !errors.Is(lookupErr, database.ErrNotFound) {
		return nil, mapDatabaseError(lookupErr)
	}

	var meter contracts.RefreshMeter
	if errors.Is(lookupErr, database.ErrNotFound) {
		if request.Msg.GetBillingPreflightToken() == "" {
			return nil, rpcerr.New(connect.CodeFailedPrecondition,
				deckv1.ErrorReason_ERROR_REASON_BILLING_PREFLIGHT_REQUIRED)
		}
		meter, err = service.dependencies.LiveUsage.RefreshMeter(ctx)
		if err != nil || !validRefreshMeter(meter) {
			return nil, rpcerr.New(connect.CodeUnavailable,
				deckv1.ErrorReason_ERROR_REASON_BILLING_CATALOG_UNAVAILABLE)
		}
		if err := validateRefreshPreflight(
			service.dependencies.Hasher, request.Msg.GetBillingPreflightToken(),
			viewer.Subject, view, requestID, meter, request.Msg.GetOrigin(),
			request.Msg.GetClientKind(), startedAt); err != nil {
			return nil, err
		}
	} else {
		// A nonterminal attempt can advance only because this request supplies
		// a fresh forwarded-user bearer. No worker resumes it.
		meter, err = service.dependencies.LiveUsage.RefreshMeter(ctx)
		if err != nil || !validRefreshMeter(meter) {
			return nil, rpcerr.New(connect.CodeUnavailable,
				deckv1.ErrorReason_ERROR_REASON_BILLING_CATALOG_UNAVAILABLE)
		}
	}

	var response *deckv1.RefreshViewResponse
	err = service.dependencies.Store.WithRefreshLock(
		ctx, viewID, viewerHash, func() error {
			current, replayed, beginErr := service.dependencies.Store.BeginRefreshAttempt(
				ctx, database.BeginRefreshAttemptParams{
					SubjectHash: subjectHash, RequestID: requestID,
					RequestDigest: requestDigest, ViewID: viewID,
					ViewerHash: viewerHash, Origin: request.Msg.GetOrigin(),
					ClientKind: request.Msg.GetClientKind(), Now: startedAt,
				})
			if beginErr != nil {
				return beginErr
			}
			if replayed && current.State == database.RefreshAttemptCompleted {
				response = proto.Clone(current.Response).(*deckv1.RefreshViewResponse)
				response.Idempotency.Replayed = true
				return nil
			}
			if request.Msg.GetOrigin() ==
				deckv1.RefreshOrigin_REFRESH_ORIGIN_MANUAL &&
				current.State == database.RefreshAttemptCreated &&
				!service.manualRefreshes.Allow(viewer.Subject, startedAt) {
				return rpcerr.RetryAfter(
					deckv1.ErrorReason_ERROR_REASON_RATE_LIMITED, time.Minute)
			}
			if current.State == database.RefreshAttemptCreated {
				eligible, eligibleErr := service.refreshEligible(
					ctx, viewer, view, viewerHash, request.Msg.GetOrigin(), startedAt)
				if eligibleErr != nil {
					return eligibleErr
				}
				if !eligible {
					response = refreshResponse(
						view, deckv1.RefreshOutcome_REFRESH_OUTCOME_AUTOMATIC_REFRESH_NOT_ELIGIBLE,
						deckv1.BillingDisposition_BILLING_DISPOSITION_FREE_NOT_ELIGIBLE,
						deckv1.FreshnessState_FRESHNESS_STATE_STALE,
						time.Time{}, false, 0, false)
					return service.dependencies.Store.SaveRefreshResponse(
						ctx, subjectHash, requestID, response, true, startedAt)
				}
			}
			cacheOutcome, usesCache := refreshCacheOutcome(
				request.Msg.GetOrigin())
			if current.State == database.RefreshAttemptCreated && usesCache {
				truncated, refreshedAt, stateErr :=
					service.dependencies.Store.SnapshotState(
						ctx, viewID, viewerHash)
				if stateErr != nil {
					return stateErr
				}
				if !refreshedAt.IsZero() &&
					startedAt.Before(refreshedAt.Add(refreshCacheWindow)) {
					snapshots, _, _, listErr :=
						service.dependencies.Store.ListAllSnapshots(
							ctx, viewID, viewerHash)
					if listErr != nil {
						return listErr
					}
					response = refreshResponse(
						view, cacheOutcome, cacheBilling(cacheOutcome),
						refreshFreshness(startedAt, refreshedAt), refreshedAt,
						truncated, len(snapshots), false)
					metricOutcome = contracts.RefreshMetricCacheHit
					return service.dependencies.Store.SaveRefreshResponse(
						ctx, subjectHash, requestID, response, true, startedAt)
				}
			}
			return service.advanceProviderRefresh(
				ctx, viewer, view, subjectHash, viewerHash, requestID,
				current, forwardedToken, meter, &response, &metricOutcome)
		})
	if err != nil {
		return nil, mapRefreshError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *View) advanceProviderRefresh(
	ctx context.Context,
	viewer contracts.Viewer,
	view *deckv1.View,
	subjectHash [32]byte,
	viewerHash [32]byte,
	requestID uuid.UUID,
	attempt database.RefreshAttempt,
	forwardedToken string,
	meter contracts.RefreshMeter,
	response **deckv1.RefreshViewResponse,
	metricOutcome *contracts.RefreshMetricOutcome,
) error {
	organizationID, _, err := refreshBillingIDs(view.GetBilling())
	if err != nil {
		return err
	}
	if attempt.State == database.RefreshAttemptCreated {
		reservation, reserveErr := service.dependencies.LiveUsage.ReserveRefresh(
			ctx, forwardedToken, view.GetBilling(), requestID, meter)
		if reserveErr != nil {
			*response = refreshResponse(
				view, deckv1.RefreshOutcome_REFRESH_OUTCOME_RESERVATION_REJECTED,
				deckv1.BillingDisposition_BILLING_DISPOSITION_REJECTED,
				deckv1.FreshnessState_FRESHNESS_STATE_STALE,
				time.Time{}, false, 0, false)
			*metricOutcome = contracts.RefreshMetricBillingFailure
			return service.dependencies.Store.SaveRefreshResponse(
				ctx, subjectHash, requestID, *response, true,
				service.dependencies.Clock.Now().UTC())
		}
		if err := service.dependencies.Store.MarkRefreshReserved(
			ctx, subjectHash, requestID, reservation.ID,
			service.dependencies.Clock.Now().UTC()); err != nil {
			_ = service.dependencies.LiveUsage.ReleaseRefresh(
				ctx, forwardedToken, organizationID, reservation.ID)
			return err
		}
		attempt.State = database.RefreshAttemptReserved
		attempt.ReservationID = reservation.ID
	}
	if attempt.State == database.RefreshAttemptDispatched {
		pending := attempt.Response
		if pending == nil {
			pending = refreshResponse(
				view, deckv1.RefreshOutcome_REFRESH_OUTCOME_PROVIDER_FAILED,
				deckv1.BillingDisposition_BILLING_DISPOSITION_RESERVED,
				deckv1.FreshnessState_FRESHNESS_STATE_STALE,
				time.Time{}, false, 0, false)
		}
		disposition, err := finalizeRefreshReservation(
			ctx, service.dependencies.LiveUsage, forwardedToken,
			organizationID, attempt.ReservationID, true)
		if err != nil {
			*metricOutcome = contracts.RefreshMetricBillingFailure
			return err
		}
		pending.BillingDisposition = disposition
		*response = pending
		*metricOutcome = contracts.RefreshMetricProviderFailure
		return service.dependencies.Store.SaveRefreshResponse(
			ctx, subjectHash, requestID, pending, true,
			service.dependencies.Clock.Now().UTC())
	}
	if attempt.State == database.RefreshAttemptReserved &&
		attempt.Response != nil {
		pending := attempt.Response
		disposition, err := finalizeRefreshReservation(
			ctx, service.dependencies.LiveUsage, forwardedToken,
			organizationID, attempt.ReservationID, false)
		if err != nil {
			*metricOutcome = contracts.RefreshMetricBillingFailure
			return err
		}
		pending.BillingDisposition = disposition
		*response = pending
		*metricOutcome = contracts.RefreshMetricProviderFailure
		return service.dependencies.Store.SaveRefreshResponse(
			ctx, subjectHash, requestID, pending, true,
			service.dependencies.Clock.Now().UTC())
	}

	dispatched := false
	var dispatchOnce sync.Once
	var dispatchErr error
	providerCtx := deckgithub.WithDispatchObserver(ctx, func() error {
		dispatchOnce.Do(func() {
			dispatchErr = service.dependencies.Store.MarkRefreshDispatched(
				ctx, subjectHash, requestID,
				service.dependencies.Clock.Now().UTC())
			if dispatchErr == nil {
				dispatched = true
			}
		})
		return dispatchErr
	})
	snapshots, truncated, providerErr := service.performGitHubRefresh(
		providerCtx, viewer, view)
	now := service.dependencies.Clock.Now().UTC()
	outcome := refreshOutcomeForProviderError(providerErr)
	freshness := deckv1.FreshnessState_FRESHNESS_STATE_STALE
	refreshedAt := time.Time{}
	resultCount := 0
	if providerErr == nil {
		previous, _, _, previousErr :=
			service.dependencies.Store.ListAllSnapshots(
				ctx, refreshViewID(view), viewerHash)
		if previousErr != nil {
			providerErr = previousErr
		}
		var storeTruncated bool
		if providerErr == nil {
			storeTruncated, providerErr =
				service.dependencies.Store.ReplaceSnapshots(
					ctx, refreshViewID(view), viewerHash, snapshots, now)
			truncated = truncated || storeTruncated
		}
		if providerErr == nil {
			writes := notificationWrites(
				previous, snapshots, view.GetNotificationPreference(),
				viewer.GitHubLogin)
			providerErr = service.dependencies.Store.CreateNotificationEvents(
				ctx, refreshViewID(view), viewerHash, writes, now)
		}
		if providerErr == nil {
			providerErr = service.dependencies.Store.UpdateWidgetSnapshots(
				ctx, viewer.AccountID, refreshViewID(view),
				snapshots, truncated, now)
		}
		outcome = refreshOutcomeForProviderError(providerErr)
		if providerErr == nil {
			freshness = deckv1.FreshnessState_FRESHNESS_STATE_FRESH
			refreshedAt = now
			resultCount = len(snapshots)
		}
	}
	pending := refreshResponse(
		view, outcome,
		deckv1.BillingDisposition_BILLING_DISPOSITION_RESERVED,
		freshness, refreshedAt,
		truncated, resultCount, false)
	if err := service.dependencies.Store.SaveRefreshPendingResponse(
		ctx, subjectHash, requestID, pending, now); err != nil {
		if !dispatched {
			_ = service.dependencies.LiveUsage.ReleaseRefresh(
				ctx, forwardedToken, organizationID, attempt.ReservationID)
		}
		return err
	}
	if dispatched {
		disposition, err := finalizeRefreshReservation(
			ctx, service.dependencies.LiveUsage, forwardedToken,
			organizationID, attempt.ReservationID, true)
		if err != nil {
			*metricOutcome = contracts.RefreshMetricBillingFailure
			return err
		}
		pending.BillingDisposition = disposition
	} else {
		disposition, err := finalizeRefreshReservation(
			ctx, service.dependencies.LiveUsage, forwardedToken,
			organizationID, attempt.ReservationID, false)
		if err != nil {
			*metricOutcome = contracts.RefreshMetricBillingFailure
			return err
		}
		pending.BillingDisposition = disposition
	}
	*response = pending
	if providerErr == nil {
		*metricOutcome = contracts.RefreshMetricProviderSuccess
	} else {
		*metricOutcome = contracts.RefreshMetricProviderFailure
	}
	return service.dependencies.Store.SaveRefreshResponse(
		ctx, subjectHash, requestID, pending, true, now)
}

func finalizeRefreshReservation(
	ctx context.Context,
	usage contracts.LiveRefreshUsage,
	forwardedToken string,
	organizationID uuid.UUID,
	reservationID uuid.UUID,
	dispatched bool,
) (deckv1.BillingDisposition, error) {
	if dispatched {
		if err := usage.CommitRefresh(
			ctx, forwardedToken, organizationID, reservationID); err != nil {
			return deckv1.BillingDisposition_BILLING_DISPOSITION_RESERVED, err
		}
		return deckv1.BillingDisposition_BILLING_DISPOSITION_COMMITTED, nil
	}
	if err := usage.ReleaseRefresh(
		ctx, forwardedToken, organizationID, reservationID); err != nil {
		return deckv1.BillingDisposition_BILLING_DISPOSITION_RESERVED, err
	}
	return deckv1.BillingDisposition_BILLING_DISPOSITION_RELEASED, nil
}

func (service *View) performGitHubRefresh(
	ctx context.Context,
	viewer contracts.Viewer,
	view *deckv1.View,
) ([]*deckv1.PullRequestResult, bool, error) {
	if service.dependencies.GitHubClient == nil {
		return nil, false, deckgithub.ErrProvider
	}
	ownerID, err := ownerID(view.GetOwner())
	if err != nil {
		return nil, false, err
	}
	connection, err := service.dependencies.Store.GetGitHubConnection(
		ctx, int16(view.GetOwner().GetScope()), ownerID, viewer.AccountID, true)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) ||
			errors.Is(err, deckgithub.ErrPermissionDenied) {
			return nil, false, deckgithub.ErrReauthenticationRequired
		}
		return nil, false, err
	}
	connection, err = refreshGitHubConnectionCredential(
		ctx, service.dependencies.Store, service.dependencies.GitHubBroker,
		viewer.AccountID, connection, service.dependencies.Clock.Now().UTC())
	if err != nil {
		return nil, false, err
	}
	resolved, err := query.ResolveViewer(
		view.GetQuery().GetRawQuery(), viewer.GitHubLogin)
	if err != nil {
		return nil, false, err
	}
	results := make([]*deckv1.PullRequestResult, 0, 100)
	cursor := ""
	truncated := false
	for len(results) <= 500 {
		page, pageErr := service.dependencies.GitHubClient.SearchPullRequests(
			ctx, connection.Installation.ID, connection.Credential, resolved,
			deckgithub.Page{Cursor: cursor, Limit: 100})
		if pageErr != nil {
			return nil, false, pageErr
		}
		for _, pullRequest := range page.PullRequests {
			results = append(results, refreshSnapshot(pullRequest))
			if len(results) > 500 {
				truncated = true
				break
			}
		}
		truncated = truncated || page.Truncated
		if cursor = page.NextCursor; cursor == "" || len(results) > 500 {
			break
		}
	}
	return results, truncated, nil
}

func refreshSnapshot(
	value deckgithub.SearchPullRequest,
) *deckv1.PullRequestResult {
	lifecycle := deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_CLOSED
	if value.IsMerged {
		lifecycle = deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_MERGED
	} else if value.IsOpen {
		lifecycle = deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_OPEN
	}
	assignees := make([]*deckv1.GitHubUser, 0, len(value.Assignees))
	for _, assignee := range value.Assignees {
		assignees = append(assignees, &deckv1.GitHubUser{Login: assignee.Login})
	}
	revision := uint64(value.UpdatedAt.UnixNano())
	if revision == 0 {
		revision = 1
	}
	return &deckv1.PullRequestResult{
		Repository: &deckv1.RepositoryReference{
			Owner: value.Repository.Owner, Name: value.Repository.Name,
		},
		Number: value.Number, Title: value.Title,
		Author:  &deckv1.PullRequestAuthor{Login: value.Author.Login},
		IsDraft: value.IsDraft, UpdatedAt: timestamppb.New(value.UpdatedAt.UTC()),
		Revision:       &deckv1.Revision{Value: revision},
		LifecycleState: lifecycle, Assignees: assignees,
		Labels: append([]string(nil), value.Labels...),
	}
}

func validateRefreshIdentity(
	viewMessage *deckv1.UuidV7,
	requestMessage *deckv1.IdempotencyKey,
) (uuid.UUID, uuid.UUID, error) {
	viewID, err := parseUUID(viewMessage)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	requestID, err := parseUUID(requestMessage.GetValue())
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return viewID, requestID, nil
}

func validRefreshTrace(
	origin deckv1.RefreshOrigin,
	client deckv1.RefreshClientKind,
) bool {
	switch origin {
	case deckv1.RefreshOrigin_REFRESH_ORIGIN_AUTOMATIC:
		return client >= deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_DESKTOP &&
			client <=
				deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_OS_BACKGROUND_TASK
	case deckv1.RefreshOrigin_REFRESH_ORIGIN_WIDGET:
		return client == deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_WIDGET
	case deckv1.RefreshOrigin_REFRESH_ORIGIN_MANUAL,
		deckv1.RefreshOrigin_REFRESH_ORIGIN_VIEW_OPEN,
		deckv1.RefreshOrigin_REFRESH_ORIGIN_SHORTCUT:
		return client == deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_DESKTOP ||
			client == deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_MOBILE
	default:
		return false
	}
}

func (service *View) refreshEligible(
	ctx context.Context,
	viewer contracts.Viewer,
	view *deckv1.View,
	viewerHash [32]byte,
	origin deckv1.RefreshOrigin,
	now time.Time,
) (bool, error) {
	if origin != deckv1.RefreshOrigin_REFRESH_ORIGIN_AUTOMATIC &&
		origin != deckv1.RefreshOrigin_REFRESH_ORIGIN_WIDGET {
		return true, nil
	}
	if view.GetNotificationPreference().GetEnabled() {
		return true, nil
	}
	attached, err := service.dependencies.Store.HasActiveViewDeviceAttachment(
		ctx, viewer.AccountID, refreshViewID(view), now)
	if err != nil || attached {
		return attached, err
	}
	return service.dependencies.Store.ViewOpenedSince(
		ctx, refreshViewID(view), viewerHash,
		now.Add(-automaticViewWindow))
}

func refreshRequestDigest(request *deckv1.RefreshViewRequest) [32]byte {
	copy := proto.Clone(request).(*deckv1.RefreshViewRequest)
	copy.BillingPreflightToken = ""
	serialized, _ := proto.MarshalOptions{Deterministic: true}.Marshal(copy)
	return security.Digest(serialized)
}

func validRefreshMeter(meter contracts.RefreshMeter) bool {
	return meter.MeterID.Version() == 7 &&
		meter.PriceVersionID.Version() == 7 &&
		meter.ServiceID.Version() == 7 &&
		meter.USDMicros == contracts.ProviderRefreshPriceUSDMicros
}

func refreshBillingIDs(
	billing *deckv1.BillingSelection,
) (uuid.UUID, uuid.UUID, error) {
	if billing == nil {
		return uuid.Nil, uuid.Nil, rpcerr.New(connect.CodeFailedPrecondition,
			deckv1.ErrorReason_ERROR_REASON_BILLING_CATALOG_UNAVAILABLE)
	}
	organizationID, err := parseUUID(billing.GetOrganizationId())
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	teamID, err := parseUUID(billing.GetTeamId())
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return organizationID, teamID, nil
}

func encodeRefreshPreflight(
	hasher *security.Hasher,
	subject string,
	viewID uuid.UUID,
	requestID uuid.UUID,
	organizationID uuid.UUID,
	teamID uuid.UUID,
	meter contracts.RefreshMeter,
	origin deckv1.RefreshOrigin,
	client deckv1.RefreshClientKind,
	expiresAt time.Time,
) string {
	payload := make([]byte, refreshPreflightSize)
	payload[0] = 1
	binary.BigEndian.PutUint64(payload[1:9], uint64(expiresAt.Unix()))
	copy(payload[9:25], viewID[:])
	copy(payload[25:41], requestID[:])
	subjectHash := hasher.Sum("refresh-preflight-subject", subject)
	copy(payload[41:73], subjectHash[:])
	copy(payload[73:89], organizationID[:])
	copy(payload[89:105], teamID[:])
	copy(payload[105:121], meter.MeterID[:])
	copy(payload[121:137], meter.PriceVersionID[:])
	copy(payload[137:153], meter.ServiceID[:])
	binary.BigEndian.PutUint64(payload[153:161], uint64(meter.USDMicros))
	payload[161] = byte(origin)
	payload[162] = byte(client)
	return hasher.EncodeCursor("refresh-preflight-v1", payload)
}

func validateRefreshPreflight(
	hasher *security.Hasher,
	token string,
	subject string,
	view *deckv1.View,
	requestID uuid.UUID,
	meter contracts.RefreshMeter,
	origin deckv1.RefreshOrigin,
	client deckv1.RefreshClientKind,
	now time.Time,
) error {
	payload, err := hasher.DecodeCursor(
		"refresh-preflight-v1", token, refreshPreflightSize)
	if err != nil || payload[0] != 1 {
		return rpcerr.New(connect.CodeFailedPrecondition,
			deckv1.ErrorReason_ERROR_REASON_BILLING_PREFLIGHT_MISMATCH)
	}
	expiresAt := time.Unix(int64(binary.BigEndian.Uint64(payload[1:9])), 0).UTC()
	if !expiresAt.After(now) {
		return rpcerr.New(connect.CodeFailedPrecondition,
			deckv1.ErrorReason_ERROR_REASON_BILLING_PREFLIGHT_EXPIRED)
	}
	viewID := refreshViewID(view)
	organizationID, teamID, billingErr := refreshBillingIDs(view.GetBilling())
	subjectHash := hasher.Sum("refresh-preflight-subject", subject)
	price := int64(binary.BigEndian.Uint64(payload[153:161]))
	if billingErr != nil ||
		!bytes.Equal(payload[9:25], viewID[:]) ||
		!bytes.Equal(payload[25:41], requestID[:]) ||
		!bytes.Equal(payload[41:73], subjectHash[:]) ||
		!bytes.Equal(payload[73:89], organizationID[:]) ||
		!bytes.Equal(payload[89:105], teamID[:]) ||
		!bytes.Equal(payload[105:121], meter.MeterID[:]) ||
		!bytes.Equal(payload[121:137], meter.PriceVersionID[:]) ||
		!bytes.Equal(payload[137:153], meter.ServiceID[:]) ||
		price != contracts.ProviderRefreshPriceUSDMicros ||
		payload[161] != byte(origin) || payload[162] != byte(client) {
		return rpcerr.New(connect.CodeFailedPrecondition,
			deckv1.ErrorReason_ERROR_REASON_BILLING_PREFLIGHT_MISMATCH)
	}
	return nil
}

func refreshOutcomeForProviderError(err error) deckv1.RefreshOutcome {
	var rateLimit *deckgithub.RateLimitError
	switch {
	case err == nil:
		return deckv1.RefreshOutcome_REFRESH_OUTCOME_REFRESHED
	case errors.Is(err, deckgithub.ErrConcurrencyLimited):
		return deckv1.RefreshOutcome_REFRESH_OUTCOME_PROVIDER_CONCURRENCY_LIMITED
	case errors.As(err, &rateLimit), errors.Is(err, deckgithub.ErrRateLimited):
		return deckv1.RefreshOutcome_REFRESH_OUTCOME_PROVIDER_RATE_LIMITED
	case errors.Is(err, deckgithub.ErrTimeout):
		return deckv1.RefreshOutcome_REFRESH_OUTCOME_PROVIDER_TIMEOUT
	case errors.Is(err, deckgithub.ErrPermissionDenied):
		return deckv1.RefreshOutcome_REFRESH_OUTCOME_PROVIDER_PERMISSION_DENIED
	case errors.Is(err, deckgithub.ErrReauthenticationRequired):
		return deckv1.RefreshOutcome_REFRESH_OUTCOME_DISCONNECTED
	default:
		return deckv1.RefreshOutcome_REFRESH_OUTCOME_PROVIDER_FAILED
	}
}

func refreshFreshness(
	now time.Time,
	refreshedAt time.Time,
) deckv1.FreshnessState {
	switch {
	case refreshedAt.IsZero():
		return deckv1.FreshnessState_FRESHNESS_STATE_NEVER_REFRESHED
	case !now.Before(refreshedAt.Add(refreshCacheWindow)):
		return deckv1.FreshnessState_FRESHNESS_STATE_STALE
	default:
		return deckv1.FreshnessState_FRESHNESS_STATE_FRESH
	}
}

func refreshCacheOutcome(
	origin deckv1.RefreshOrigin,
) (deckv1.RefreshOutcome, bool) {
	switch origin {
	case deckv1.RefreshOrigin_REFRESH_ORIGIN_AUTOMATIC,
		deckv1.RefreshOrigin_REFRESH_ORIGIN_WIDGET:
		return deckv1.RefreshOutcome_REFRESH_OUTCOME_COALESCED, true
	case deckv1.RefreshOrigin_REFRESH_ORIGIN_VIEW_OPEN,
		deckv1.RefreshOrigin_REFRESH_ORIGIN_SHORTCUT:
		return deckv1.RefreshOutcome_REFRESH_OUTCOME_CACHE_HIT, true
	default:
		return deckv1.RefreshOutcome_REFRESH_OUTCOME_UNSPECIFIED, false
	}
}

func cacheBilling(
	outcome deckv1.RefreshOutcome,
) deckv1.BillingDisposition {
	if outcome == deckv1.RefreshOutcome_REFRESH_OUTCOME_COALESCED {
		return deckv1.BillingDisposition_BILLING_DISPOSITION_FREE_COALESCED
	}
	return deckv1.BillingDisposition_BILLING_DISPOSITION_FREE_CACHE_HIT
}

func refreshResponse(
	view *deckv1.View,
	outcome deckv1.RefreshOutcome,
	billing deckv1.BillingDisposition,
	freshness deckv1.FreshnessState,
	refreshedAt time.Time,
	truncated bool,
	resultCount int,
	replayed bool,
) *deckv1.RefreshViewResponse {
	response := &deckv1.RefreshViewResponse{
		ViewId: view.GetViewId(), Outcome: outcome,
		BillingDisposition: billing, Freshness: freshness,
		ViewRevision: view.GetRevision(), Truncated: truncated,
		ResultCount: uint32(resultCount),
		Idempotency: &deckv1.IdempotencyResult{
			Operation: deckv1.IdempotentOperation_IDEMPOTENT_OPERATION_REFRESH_VIEW,
			Replayed:  replayed,
		},
	}
	if !refreshedAt.IsZero() {
		response.RefreshedAt = timestamppb.New(refreshedAt.UTC())
	}
	return response
}

func mapRefreshError(err error) error {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return err
	}
	return mapDatabaseError(err)
}

func refreshViewID(view *deckv1.View) uuid.UUID {
	id, _ := uuid.Parse(view.GetViewId().GetValue())
	return id
}
