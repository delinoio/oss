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
	view, err := service.getRefreshViewMetadata(ctx, viewer, viewID)
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
	viewID, requestID, err := validateRefreshIdentity(
		request.Msg.GetViewId(), request.Msg.GetRefreshRequestId())
	if err != nil || !validRefreshTrace(
		request.Msg.GetOrigin(), request.Msg.GetClientKind()) {
		return nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	requestDigest := refreshRequestDigest(request.Msg)
	subjectHash := service.dependencies.Hasher.Sum(
		"refresh-subject", viewer.Subject)
	viewerHash := service.dependencies.Hasher.Sum(
		"snapshot-viewer", viewer.AccountID.String())

	// Exact attempts are resolved before current view/provider authorization.
	// Completed results and nonterminal recovery need neither repository
	// access nor a retained view definition.
	attempt, lookupErr := service.dependencies.Store.GetRefreshAttempt(
		ctx, subjectHash, requestID, requestDigest)
	if lookupErr == nil {
		if attempt.ViewID != viewID || attempt.ViewerHash != viewerHash {
			return nil, rpcerr.New(connect.CodeInternal,
				deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
		}
		if attempt.State == database.RefreshAttemptCompleted {
			replayed, replayErr := replayRefreshAttempt(attempt)
			if replayErr != nil {
				return nil, mapRefreshError(replayErr)
			}
			return connect.NewResponse(replayed), nil
		}
		if refreshAttemptNeedsPreAuthorizationRecovery(attempt) {
			if service.dependencies.LiveUsage == nil {
				return nil, rpcerr.New(connect.CodeUnavailable,
					deckv1.ErrorReason_ERROR_REASON_BILLING_CATALOG_UNAVAILABLE)
			}
			var response *deckv1.RefreshViewResponse
			err := service.dependencies.Store.WithRefreshLock(
				ctx, attempt.ViewID, attempt.ViewerHash, func() error {
					current, currentErr :=
						service.dependencies.Store.GetRefreshAttempt(
							ctx, subjectHash, requestID, requestDigest)
					if currentErr != nil {
						return currentErr
					}
					if current.State == database.RefreshAttemptCompleted {
						response, currentErr = replayRefreshAttempt(current)
						return currentErr
					}
					if current.State == database.RefreshAttemptCreated {
						var completed bool
						completed, currentErr =
							service.reserveCreatedRefreshAttempt(
								ctx,
								persistedRefreshAttemptView(
									service.dependencies.Hasher, current),
								subjectHash, current.ViewerHash, requestID,
								&current, forwardedToken, &response,
								&metricOutcome)
						if currentErr != nil || completed {
							return currentErr
						}
					}
					return service.finalizePendingRefreshAccounting(
						ctx, subjectHash, requestID, current, forwardedToken,
						&response, &metricOutcome)
				})
			if err != nil {
				return nil, mapRefreshError(err)
			}
			return connect.NewResponse(response), nil
		}
	}
	if lookupErr != nil && !errors.Is(lookupErr, database.ErrNotFound) {
		return nil, mapDatabaseError(lookupErr)
	}

	if service.dependencies.LiveUsage == nil {
		return nil, rpcerr.New(connect.CodeUnavailable,
			deckv1.ErrorReason_ERROR_REASON_BILLING_CATALOG_UNAVAILABLE)
	}
	if errors.Is(lookupErr, database.ErrNotFound) {
		if request.Msg.GetBillingPreflightToken() == "" {
			return nil, rpcerr.New(connect.CodeFailedPrecondition,
				deckv1.ErrorReason_ERROR_REASON_BILLING_PREFLIGHT_REQUIRED)
		}
	}
	view, err := service.getRefreshViewMetadata(ctx, viewer, viewID)
	if err != nil {
		return nil, err
	}

	var response *deckv1.RefreshViewResponse
	err = service.dependencies.Store.WithRefreshLock(
		ctx, viewID, viewerHash, func() error {
			current, currentErr := service.dependencies.Store.GetRefreshAttempt(
				ctx, subjectHash, requestID, requestDigest)
			replayed := currentErr == nil
			currentTime := service.dependencies.Clock.Now().UTC()
			if errors.Is(currentErr, database.ErrNotFound) {
				meter, meterErr := service.dependencies.LiveUsage.RefreshMeter(ctx)
				if meterErr != nil || !validRefreshMeter(meter) {
					return rpcerr.New(connect.CodeUnavailable,
						deckv1.ErrorReason_ERROR_REASON_BILLING_CATALOG_UNAVAILABLE)
				}
				if preflightErr := validateRefreshPreflight(
					service.dependencies.Hasher,
					request.Msg.GetBillingPreflightToken(),
					viewer.Subject, view, requestID, meter,
					request.Msg.GetOrigin(), request.Msg.GetClientKind(),
					currentTime); preflightErr != nil {
					return preflightErr
				}
				organizationID, teamID, billingErr :=
					refreshBillingIDs(view.GetBilling())
				if billingErr != nil {
					return billingErr
				}
				current, replayed, currentErr =
					service.dependencies.Store.BeginRefreshAttempt(
						ctx, database.BeginRefreshAttemptParams{
							SubjectHash: subjectHash, RequestID: requestID,
							RequestDigest: requestDigest, ViewID: viewID,
							ViewRevision:   view.GetRevision().GetValue(),
							ViewerHash:     viewerHash,
							Origin:         request.Msg.GetOrigin(),
							ClientKind:     request.Msg.GetClientKind(),
							OrganizationID: organizationID, TeamID: teamID,
							Meter: meter, Now: currentTime,
						})
			}
			if errors.Is(currentErr, database.ErrRefreshRateLimited) {
				return rpcerr.RetryAfter(
					deckv1.ErrorReason_ERROR_REASON_RATE_LIMITED, time.Minute)
			}
			if currentErr != nil {
				return currentErr
			}
			if replayed && current.State == database.RefreshAttemptCompleted {
				response, currentErr = replayRefreshAttempt(current)
				return currentErr
			}
			recoveredCreated := false
			if replayed && current.State == database.RefreshAttemptCreated {
				var completed bool
				completed, currentErr = service.reserveCreatedRefreshAttempt(
					ctx, view, subjectHash, viewerHash, requestID, &current,
					forwardedToken, &response, &metricOutcome)
				if currentErr != nil || completed {
					return currentErr
				}
				recoveredCreated = true
			}
			canUseShortcut := current.State == database.RefreshAttemptCreated ||
				recoveredCreated
			if canUseShortcut && view.GetConnectionState() ==
				deckv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
				response = disconnectedRefreshResponse(view)
				metricOutcome = contracts.RefreshMetricProviderFailure
				return service.completeRefreshWithoutDispatch(
					ctx, subjectHash, requestID, current, forwardedToken,
					response, &metricOutcome)
			}
			if canUseShortcut {
				eligible, eligibleErr := service.refreshEligible(
					ctx, viewer, view, viewerHash,
					request.Msg.GetOrigin(), currentTime)
				if eligibleErr != nil {
					return eligibleErr
				}
				if !eligible {
					truncated, refreshedAt, freshness, resultCount, stateErr :=
						service.currentSnapshotState(
							ctx, viewID, viewerHash, currentTime)
					if stateErr != nil {
						return stateErr
					}
					response = refreshResponse(
						view, deckv1.RefreshOutcome_REFRESH_OUTCOME_AUTOMATIC_REFRESH_NOT_ELIGIBLE,
						deckv1.BillingDisposition_BILLING_DISPOSITION_FREE_NOT_ELIGIBLE,
						freshness, refreshedAt, truncated, resultCount, false)
					return service.completeRefreshWithoutDispatch(
						ctx, subjectHash, requestID, current, forwardedToken,
						response, &metricOutcome)
				}
			}
			cacheOutcome, usesCache := refreshCacheOutcome(
				request.Msg.GetOrigin())
			if canUseShortcut && usesCache {
				recentAutomatic := false
				if cacheOutcome ==
					deckv1.RefreshOutcome_REFRESH_OUTCOME_COALESCED {
					recentAutomatic, err =
						service.dependencies.Store.HasRecentAutomaticRefreshAttempt(
							ctx, viewID, viewerHash, requestID,
							currentTime.Add(-refreshCacheWindow))
					if err != nil {
						return err
					}
				}
				snapshots, truncated, refreshedAt, stateErr :=
					service.dependencies.Store.ListAllSnapshots(
						ctx, viewID, viewerHash)
				if stateErr != nil {
					return stateErr
				}
				freshness := refreshFreshness(currentTime, refreshedAt)
				resultCount := len(snapshots)
				if refreshCacheAvailable(
					cacheOutcome, recentAutomatic, refreshedAt, currentTime) {
					if widgetErr :=
						service.dependencies.Store.WithViewRevisionLock(
							ctx, viewID, current.ViewRevision,
							func(persistence *database.RefreshPersistence) error {
								return persistence.UpdateWidgetSnapshots(
									ctx, viewer.AccountID, viewID, snapshots,
									truncated, refreshedAt, currentTime)
							}); widgetErr != nil {
						return widgetErr
					}
					response = refreshResponse(
						view, cacheOutcome, cacheBilling(cacheOutcome),
						freshness, refreshedAt,
						truncated, resultCount, false)
					metricOutcome = contracts.RefreshMetricCacheHit
					return service.completeRefreshWithoutDispatch(
						ctx, subjectHash, requestID, current, forwardedToken,
						response, &metricOutcome)
				}
			}
			// A nonterminal attempt advances only because this active request
			// supplies a fresh forwarded-user bearer. Its original billing
			// inputs remain authoritative after catalog or view changes.
			if recoveredCreated {
				return service.dispatchReservedRefresh(
					ctx, viewer, view, subjectHash, viewerHash, requestID,
					current, forwardedToken, &response, &metricOutcome)
			}
			return service.advanceProviderRefresh(
				ctx, viewer, view, subjectHash, viewerHash, requestID,
				current, forwardedToken, &response, &metricOutcome)
		})
	if err != nil {
		return nil, mapRefreshError(err)
	}
	return connect.NewResponse(response), nil
}

func replayRefreshAttempt(
	attempt database.RefreshAttempt,
) (*deckv1.RefreshViewResponse, error) {
	if attempt.Response == nil || attempt.Response.Idempotency == nil {
		return nil, errors.New("deck refresh: completed attempt has no response")
	}
	replayed := proto.Clone(attempt.Response).(*deckv1.RefreshViewResponse)
	replayed.Idempotency.Replayed = true
	return replayed, nil
}

func refreshAttemptNeedsOnlyAccounting(
	attempt database.RefreshAttempt,
) bool {
	return attempt.State == database.RefreshAttemptReserved ||
		attempt.State == database.RefreshAttemptDispatched
}

func refreshAttemptNeedsPreAuthorizationRecovery(
	attempt database.RefreshAttempt,
) bool {
	return attempt.State == database.RefreshAttemptCreated ||
		refreshAttemptNeedsOnlyAccounting(attempt)
}

func (service *View) finalizePendingRefreshAccounting(
	ctx context.Context,
	subjectHash [32]byte,
	requestID uuid.UUID,
	attempt database.RefreshAttempt,
	forwardedToken string,
	response **deckv1.RefreshViewResponse,
	metricOutcome *contracts.RefreshMetricOutcome,
) error {
	if !refreshAttemptNeedsOnlyAccounting(attempt) ||
		attempt.OrganizationID.Version() != 7 ||
		attempt.ReservationID.Version() != 7 ||
		attempt.ViewID.Version() != 7 ||
		attempt.ViewRevision == 0 ||
		!validRefreshMeter(attempt.Meter) {
		return errors.New("deck refresh: invalid persisted accounting attempt")
	}
	now := service.dependencies.Clock.Now().UTC()
	pending := attempt.Response
	if pending == nil {
		pending = synthesizedRefreshAccountingResponse(
			service.dependencies.Hasher, attempt)
	}
	disposition, err := finalizeRefreshReservation(
		ctx, service.dependencies.LiveUsage, forwardedToken,
		attempt.OrganizationID, attempt.ReservationID,
		attempt.State == database.RefreshAttemptDispatched)
	if err != nil {
		*metricOutcome = contracts.RefreshMetricBillingFailure
		return err
	}
	pending.BillingDisposition = disposition
	*response = pending
	*metricOutcome = contracts.RefreshMetricProviderFailure
	return service.dependencies.Store.SaveRefreshResponse(
		ctx, subjectHash, requestID, pending, true, now)
}

func synthesizedRefreshAccountingResponse(
	hasher *security.Hasher,
	attempt database.RefreshAttempt,
) *deckv1.RefreshViewResponse {
	return refreshResponse(
		persistedRefreshAttemptView(hasher, attempt),
		deckv1.RefreshOutcome_REFRESH_OUTCOME_PROVIDER_FAILED,
		deckv1.BillingDisposition_BILLING_DISPOSITION_RESERVED,
		deckv1.FreshnessState_FRESHNESS_STATE_UNSPECIFIED,
		time.Time{}, false, 0, false)
}

func persistedRefreshAttemptView(
	hasher *security.Hasher,
	attempt database.RefreshAttempt,
) *deckv1.View {
	return &deckv1.View{
		ViewId: &deckv1.UuidV7{Value: attempt.ViewID.String()},
		Revision: &deckv1.Revision{
			Value: attempt.ViewRevision,
			Etag:  hasher.ETag(attempt.ViewID, attempt.ViewRevision),
		},
	}
}

func disconnectedRefreshResponse(
	view *deckv1.View,
) *deckv1.RefreshViewResponse {
	return refreshResponse(
		view,
		deckv1.RefreshOutcome_REFRESH_OUTCOME_DISCONNECTED,
		deckv1.BillingDisposition_BILLING_DISPOSITION_FREE_NOT_ELIGIBLE,
		deckv1.FreshnessState_FRESHNESS_STATE_DISCONNECTED,
		time.Time{}, false, 0, false)
}

func (service *View) completeRefreshWithoutDispatch(
	ctx context.Context,
	subjectHash [32]byte,
	requestID uuid.UUID,
	attempt database.RefreshAttempt,
	forwardedToken string,
	response *deckv1.RefreshViewResponse,
	metricOutcome *contracts.RefreshMetricOutcome,
) error {
	now := service.dependencies.Clock.Now().UTC()
	switch attempt.State {
	case database.RefreshAttemptCreated:
	case database.RefreshAttemptReserved:
		response.BillingDisposition =
			deckv1.BillingDisposition_BILLING_DISPOSITION_RESERVED
		if err := service.dependencies.Store.SaveRefreshPendingResponse(
			ctx, subjectHash, requestID, response, now); err != nil {
			return err
		}
		disposition, err := finalizeRefreshReservation(
			ctx, service.dependencies.LiveUsage, forwardedToken,
			attempt.OrganizationID, attempt.ReservationID, false)
		if err != nil {
			*metricOutcome = contracts.RefreshMetricBillingFailure
			return err
		}
		response.BillingDisposition = disposition
	default:
		return errors.New("deck refresh: invalid shortcut attempt")
	}
	return service.dependencies.Store.SaveRefreshResponse(
		ctx, subjectHash, requestID, response, true, now)
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
	response **deckv1.RefreshViewResponse,
	metricOutcome *contracts.RefreshMetricOutcome,
) error {
	if attempt.State == database.RefreshAttemptCreated {
		completed, err := service.reserveCreatedRefreshAttempt(
			ctx, view, subjectHash, viewerHash, requestID, &attempt,
			forwardedToken, response, metricOutcome)
		if err != nil || completed {
			return err
		}
	} else if refreshAttemptNeedsOnlyAccounting(attempt) {
		return service.finalizePendingRefreshAccounting(
			ctx, subjectHash, requestID, attempt, forwardedToken,
			response, metricOutcome)
	} else {
		return errors.New("deck refresh: invalid persisted attempt state")
	}
	return service.dispatchReservedRefresh(
		ctx, viewer, view, subjectHash, viewerHash, requestID, attempt,
		forwardedToken, response, metricOutcome)
}

func (service *View) reserveCreatedRefreshAttempt(
	ctx context.Context,
	view *deckv1.View,
	subjectHash [32]byte,
	viewerHash [32]byte,
	requestID uuid.UUID,
	attempt *database.RefreshAttempt,
	forwardedToken string,
	response **deckv1.RefreshViewResponse,
	metricOutcome *contracts.RefreshMetricOutcome,
) (bool, error) {
	if attempt.State != database.RefreshAttemptCreated ||
		attempt.OrganizationID.Version() != 7 ||
		attempt.TeamID.Version() != 7 || !validRefreshMeter(attempt.Meter) {
		return false, errors.New(
			"deck refresh: invalid persisted billing inputs")
	}
	billing := &deckv1.BillingSelection{
		OrganizationId: &deckv1.UuidV7{
			Value: attempt.OrganizationID.String(),
		},
		TeamId: &deckv1.UuidV7{Value: attempt.TeamID.String()},
	}
	reservation, reserveErr := service.dependencies.LiveUsage.ReserveRefresh(
		ctx, forwardedToken, billing, requestID, attempt.Meter)
	if reserveErr != nil {
		*metricOutcome = contracts.RefreshMetricBillingFailure
		if !errors.Is(
			reserveErr, contracts.ErrRefreshReservationRejected) {
			return false, reserveErr
		}
		truncated, refreshedAt, freshness, resultCount, stateErr :=
			service.currentSnapshotState(
				ctx, refreshViewID(view), viewerHash,
				service.dependencies.Clock.Now().UTC())
		if stateErr != nil && !errors.Is(stateErr, database.ErrNotFound) {
			return false, stateErr
		}
		if errors.Is(stateErr, database.ErrNotFound) {
			truncated = false
			refreshedAt = time.Time{}
			freshness =
				deckv1.FreshnessState_FRESHNESS_STATE_UNSPECIFIED
			resultCount = 0
		}
		*response = refreshResponse(
			view, deckv1.RefreshOutcome_REFRESH_OUTCOME_RESERVATION_REJECTED,
			deckv1.BillingDisposition_BILLING_DISPOSITION_REJECTED,
			freshness, refreshedAt, truncated, resultCount, false)
		return true, service.dependencies.Store.SaveRefreshResponse(
			ctx, subjectHash, requestID, *response, true,
			service.dependencies.Clock.Now().UTC())
	}
	if err := service.dependencies.Store.MarkRefreshReserved(
		ctx, subjectHash, requestID, reservation.ID,
		service.dependencies.Clock.Now().UTC()); err != nil {
		return false, err
	}
	attempt.State = database.RefreshAttemptReserved
	attempt.ReservationID = reservation.ID
	return false, nil
}

func (service *View) dispatchReservedRefresh(
	ctx context.Context,
	viewer contracts.Viewer,
	view *deckv1.View,
	subjectHash [32]byte,
	viewerHash [32]byte,
	requestID uuid.UUID,
	attempt database.RefreshAttempt,
	forwardedToken string,
	response **deckv1.RefreshViewResponse,
	metricOutcome *contracts.RefreshMetricOutcome,
) error {
	if attempt.State != database.RefreshAttemptReserved ||
		attempt.OrganizationID.Version() != 7 ||
		attempt.ReservationID.Version() != 7 ||
		attempt.ViewID.Version() != 7 ||
		attempt.ViewRevision == 0 ||
		!validRefreshMeter(attempt.Meter) {
		return errors.New("deck refresh: invalid reserved attempt")
	}

	dispatched := false
	var dispatchOnce sync.Once
	var dispatchErr error
	// Repository authorization may contact GitHub, so reject an already-stale
	// attempt before using the dispatch-observed provider context.
	providerErr := service.dependencies.Store.CheckViewRevision(
		ctx, refreshViewID(view), attempt.ViewRevision)
	providerCtx := ctx
	var providerView *deckv1.View
	if providerErr == nil {
		providerCtx = deckgithub.WithDispatchObserver(ctx, func() error {
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
		providerView, providerErr = service.getRefreshProviderAuthorizedView(
			providerCtx, viewer, refreshViewID(view))
	}
	if providerErr == nil &&
		providerView.GetRevision().GetValue() != attempt.ViewRevision {
		providerErr = &database.StaleError{
			ResourceID: refreshViewID(view),
			Revision:   providerView.GetRevision().GetValue(),
		}
	}
	var previous []*deckv1.PullRequestResult
	if providerErr == nil {
		previous, _, _, providerErr =
			service.dependencies.Store.ListAllSnapshots(
				ctx, refreshViewID(view), viewerHash)
	}
	var snapshots, notificationSnapshots []*deckv1.PullRequestResult
	truncated := false
	if providerErr == nil {
		snapshots, notificationSnapshots, truncated, providerErr =
			service.performGitHubRefresh(
				providerCtx, viewer, providerView, previous)
	}
	now := service.dependencies.Clock.Now().UTC()
	outcome := refreshOutcomeForProviderError(providerErr)
	freshness := deckv1.FreshnessState_FRESHNESS_STATE_STALE
	refreshedAt := time.Time{}
	resultCount := 0
	if providerErr == nil {
		providerErr = service.dependencies.Store.WithViewRevisionLock(
			ctx, refreshViewID(view), view.GetRevision().GetValue(),
			func(persistence *database.RefreshPersistence) error {
				storeTruncated, storeErr :=
					persistence.ReplaceSnapshots(
						ctx, refreshViewID(view), viewerHash, snapshots, now)
				if storeErr != nil {
					return storeErr
				}
				truncated = truncated || storeTruncated
				preferences, preferenceErr :=
					persistence.ActiveNotificationPreferences(
						ctx, viewer.AccountID, refreshViewID(view), now)
				if preferenceErr != nil {
					return preferenceErr
				}
				writes := notificationWrites(
					previous, notificationSnapshots,
					preferences, viewer.GitHubLogin)
				if err := persistence.CreateNotificationEvents(
					ctx, refreshViewID(view), viewerHash, writes, now); err != nil {
					return err
				}
				return persistence.UpdateWidgetSnapshots(
					ctx, viewer.AccountID, refreshViewID(view),
					snapshots, truncated, now, now)
			})
		outcome = refreshOutcomeForProviderError(providerErr)
		if providerErr == nil {
			freshness = deckv1.FreshnessState_FRESHNESS_STATE_FRESH
			refreshedAt = now
			resultCount = len(snapshots)
		}
	}
	if providerErr != nil {
		var stateErr error
		truncated, refreshedAt, freshness, resultCount, stateErr =
			service.currentSnapshotState(
				ctx, refreshViewID(view), viewerHash, now)
		if stateErr != nil {
			providerErr = stateErr
		}
	}
	pending := refreshResponse(
		view, outcome,
		deckv1.BillingDisposition_BILLING_DISPOSITION_RESERVED,
		freshness, refreshedAt,
		truncated, resultCount, false)
	if err := service.dependencies.Store.SaveRefreshPendingResponse(
		ctx, subjectHash, requestID, pending, now); err != nil {
		return err
	}
	if dispatched {
		disposition, err := finalizeRefreshReservation(
			ctx, service.dependencies.LiveUsage, forwardedToken,
			attempt.OrganizationID, attempt.ReservationID, true)
		if err != nil {
			*metricOutcome = contracts.RefreshMetricBillingFailure
			return err
		}
		pending.BillingDisposition = disposition
	} else {
		disposition, err := finalizeRefreshReservation(
			ctx, service.dependencies.LiveUsage, forwardedToken,
			attempt.OrganizationID, attempt.ReservationID, false)
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
	previous []*deckv1.PullRequestResult,
) ([]*deckv1.PullRequestResult, []*deckv1.PullRequestResult, bool, error) {
	if service.dependencies.GitHubClient == nil {
		return nil, nil, false, deckgithub.ErrProvider
	}
	ownerID, err := ownerID(view.GetOwner())
	if err != nil {
		return nil, nil, false, err
	}
	connection, err := service.dependencies.Store.GetGitHubConnection(
		ctx, int16(view.GetOwner().GetScope()), ownerID, viewer.AccountID, true)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) ||
			errors.Is(err, deckgithub.ErrPermissionDenied) {
			return nil, nil, false, deckgithub.ErrReauthenticationRequired
		}
		return nil, nil, false, err
	}
	connection, err = refreshGitHubConnectionCredential(
		ctx, service.dependencies.Store, service.dependencies.GitHubBroker,
		viewer.AccountID, connection, service.dependencies.Clock.Now().UTC())
	if err != nil {
		return nil, nil, false, err
	}
	resolved, err := query.ResolveViewer(
		view.GetQuery().GetRawQuery(), viewer.GitHubLogin)
	if err != nil {
		return nil, nil, false, err
	}
	results := make([]*deckv1.PullRequestResult, 0, 100)
	cursor := ""
	truncated := false
	for len(results) <= 500 {
		page, pageErr := service.dependencies.GitHubClient.SearchPullRequests(
			ctx, connection.Installation.ID, connection.Credential, resolved,
			deckgithub.Page{Cursor: cursor, Limit: 100})
		if pageErr != nil {
			return nil, nil, false, pageErr
		}
		for _, pullRequest := range page.PullRequests {
			reference := &deckv1.PullRequestReference{
				Repository: &deckv1.RepositoryReference{
					Owner: pullRequest.Repository.Owner,
					Name:  pullRequest.Repository.Name,
				},
				Number: pullRequest.Number,
			}
			metadata, metadataErr :=
				service.dependencies.GitHubClient.PullRequestSnapshotMetadata(
					ctx, connection.Installation.ID, connection.Credential,
					connection.Installation.Permissions,
					deckgithub.PullRequestRef{
						Repository: pullRequest.Repository,
						Number:     pullRequest.Number,
					})
			if metadataErr != nil {
				return nil, nil, false, metadataErr
			}
			detail := service.pullRequestDetail(
				refreshViewID(view), reference,
				&deckv1.PullRequestResult{}, metadata)
			results = append(results, detail.Result)
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
	results, resultTruncated := retainedRefreshResults(results)
	truncated = truncated || resultTruncated
	notificationSnapshots := append(
		[]*deckv1.PullRequestResult(nil), results...)
	for _, snapshot := range previousOnlySnapshots(previous, results) {
		if snapshot.GetLifecycleState() !=
			deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_OPEN {
			continue
		}
		repository := snapshot.GetRepository()
		metadata, metadataErr :=
			service.dependencies.GitHubClient.PullRequestSnapshotMetadata(
				ctx, connection.Installation.ID, connection.Credential,
				connection.Installation.Permissions,
				deckgithub.PullRequestRef{
					Repository: deckgithub.Repository{
						Owner: repository.GetOwner(), Name: repository.GetName(),
					},
					Number: snapshot.GetNumber(),
				})
		if metadataErr != nil {
			service.dependencies.Logger.Warn(
				"Deck refresh skipped optional historical notification probe",
				"event", "deck_refresh_notification_probe_skipped",
				"provider_outcome",
				int32(refreshOutcomeForProviderError(metadataErr)),
			)
			continue
		}
		transitioned := service.pullRequestDetail(
			refreshViewID(view),
			&deckv1.PullRequestReference{
				Repository: repository, Number: snapshot.GetNumber(),
			},
			snapshot, metadata).Result
		if transitioned.GetLifecycleState() ==
			deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_CLOSED ||
			transitioned.GetLifecycleState() ==
				deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_MERGED {
			notificationSnapshots = append(notificationSnapshots, transitioned)
		}
	}
	return results, notificationSnapshots, truncated, nil
}

func previousOnlySnapshots(
	previous []*deckv1.PullRequestResult,
	current []*deckv1.PullRequestResult,
) []*deckv1.PullRequestResult {
	currentKeys := make(map[string]struct{}, len(current))
	for _, snapshot := range current {
		currentKeys[pullRequestSnapshotKey(snapshot)] = struct{}{}
	}
	missing := make([]*deckv1.PullRequestResult, 0)
	for _, snapshot := range previous {
		if _, ok := currentKeys[pullRequestSnapshotKey(snapshot)]; !ok {
			missing = append(missing, snapshot)
		}
	}
	return missing
}

func retainedRefreshResults(
	results []*deckv1.PullRequestResult,
) ([]*deckv1.PullRequestResult, bool) {
	if len(results) <= 500 {
		return results, false
	}
	return results[:500], true
}

func (service *View) currentSnapshotState(
	ctx context.Context,
	viewID uuid.UUID,
	viewerHash [32]byte,
	now time.Time,
) (bool, time.Time, deckv1.FreshnessState, int, error) {
	snapshots, truncated, refreshedAt, err :=
		service.dependencies.Store.ListAllSnapshots(ctx, viewID, viewerHash)
	if err != nil {
		return false, time.Time{},
			deckv1.FreshnessState_FRESHNESS_STATE_UNSPECIFIED, 0, err
	}
	return truncated, refreshedAt, refreshFreshness(now, refreshedAt),
		len(snapshots), nil
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
	preferences, err :=
		service.dependencies.Store.ActiveNotificationPreferences(
			ctx, viewer.AccountID, refreshViewID(view), now)
	if err != nil {
		return false, err
	}
	if hasEnabledNotificationPreference(preferences) {
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

func hasEnabledNotificationPreference(
	preferences []*deckv1.ViewNotificationPreference,
) bool {
	for _, preference := range preferences {
		if !preference.GetEnabled() {
			continue
		}
		for _, transition := range preference.GetTransitions() {
			if supportedNotificationTransition(transition) {
				return true
			}
		}
	}
	return false
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

func refreshCacheAvailable(
	outcome deckv1.RefreshOutcome,
	recentAutomatic bool,
	refreshedAt time.Time,
	now time.Time,
) bool {
	if outcome == deckv1.RefreshOutcome_REFRESH_OUTCOME_COALESCED &&
		recentAutomatic {
		return true
	}
	return !refreshedAt.IsZero() &&
		now.Before(refreshedAt.Add(refreshCacheWindow))
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
