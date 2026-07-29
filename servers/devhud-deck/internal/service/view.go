package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/audit"
	"github.com/delinoio/oss/servers/devhud-deck/internal/authn"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database"
	deckgithub "github.com/delinoio/oss/servers/devhud-deck/internal/github"
	"github.com/delinoio/oss/servers/devhud-deck/internal/query"
	"github.com/delinoio/oss/servers/devhud-deck/internal/rpcerr"
	"github.com/delinoio/oss/servers/devhud-deck/internal/security"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (service *View) ListViews(
	ctx context.Context,
	request *connect.Request[deckv1.ListViewsRequest],
) (*connect.Response[deckv1.ListViewsResponse], error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	ownerID, err := authorizeOwner(viewer, request.Msg.Owner, false)
	if err != nil {
		return nil, err
	}
	pageLimit := pageSize(request.Msg.Page, 50, 100)
	after := uuid.Nil
	if cursor := request.Msg.GetPage().GetCursor(); cursor != "" {
		payload, decodeErr := service.dependencies.Hasher.DecodeCursor(
			"views:"+request.Msg.Owner.Scope.String()+":"+ownerID.String(), cursor, 16)
		if decodeErr != nil {
			return nil, rpcerr.New(connect.CodeInvalidArgument,
				deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
		}
		after = uuid.UUID(payload)
	}
	views, err := service.dependencies.Store.ListViewsAuthorized(
		ctx, request.Msg.Owner.Scope, ownerID, after, int32(pageLimit+1),
		service.viewDefinitionAuthorizer(ctx, viewer, false))
	if err != nil {
		return nil, mapAuthorizedViewError(err)
	}
	nextCursor := ""
	if len(views) > int(pageLimit) {
		last, parseErr := parseUUID(views[pageLimit-1].ViewId)
		if parseErr != nil {
			return nil, parseErr
		}
		nextCursor = service.dependencies.Hasher.EncodeCursor(
			"views:"+request.Msg.Owner.Scope.String()+":"+ownerID.String(), last[:])
		views = views[:pageLimit]
	}
	limit := uint32(50)
	if request.Msg.Owner.Scope == deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION {
		limit = 250
	}
	return connect.NewResponse(&deckv1.ListViewsResponse{
		Views: views, Page: &deckv1.PageResponse{NextCursor: nextCursor},
		OwnerViewLimit: limit,
	}), nil
}

func (service *View) GetView(
	ctx context.Context,
	request *connect.Request[deckv1.GetViewRequest],
) (*connect.Response[deckv1.GetViewResponse], error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseUUID(request.Msg.ViewId)
	if err != nil {
		return nil, err
	}
	view, err := service.getAuthorizedView(ctx, viewer, id, false)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&deckv1.GetViewResponse{View: view}), nil
}

func (service *View) CreateView(
	ctx context.Context,
	request *connect.Request[deckv1.CreateViewRequest],
) (*connect.Response[deckv1.CreateViewResponse], error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	idempotencyID, err := parseUUID(request.Msg.GetIdempotencyKey().GetValue())
	if err != nil || request.Msg.View == nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	ownerID, err := authorizeOwner(viewer, request.Msg.View.Owner, true)
	if err != nil {
		return nil, err
	}
	if err := authorizeBilling(
		viewer, request.Msg.View.Owner, request.Msg.View.Billing); err != nil {
		return nil, err
	}
	canonicalQuery, err := query.Parse(request.Msg.View.GetQuery().GetRawQuery())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	view, err := newView(request.Msg.View, canonicalQuery)
	if err != nil {
		return nil, err
	}
	allowed, err := service.canReadViewRepositories(ctx, viewer, view)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, rpcerr.New(connect.CodePermissionDenied,
			deckv1.ErrorReason_ERROR_REASON_GITHUB_PERMISSION_DENIED)
	}
	viewID, err := service.dependencies.IDs.New()
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal,
			deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
	}
	serialized, err := proto.MarshalOptions{Deterministic: true}.Marshal(view)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal,
			deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
	}
	digest := security.Digest(serialized)
	view.ViewId = &deckv1.UuidV7{Value: viewID.String()}
	subjectHash := service.dependencies.Hasher.Sum("subject", viewer.Subject)
	ownerHash := service.dependencies.Hasher.Sum(
		"owner", request.Msg.View.Owner.Scope.String()+":"+ownerID.String())
	now := service.dependencies.Clock.Now().UTC()
	created, replayed, err := service.dependencies.Store.CreateView(
		ctx, database.CreateViewParams{
			ID: viewID, IdempotencyKey: idempotencyID,
			SubjectHash: subjectHash, RequestDigest: digest, OwnerHash: ownerHash,
			View: view, Now: now,
		})
	if err != nil {
		return nil, mapDatabaseError(err)
	}
	if !replayed {
		if err := service.recordAudit(ctx, viewer.Subject, audit.EventViewCreated,
			created.Owner.Scope, ownerHash[:], audit.ResourceView, viewID,
			audit.OutcomeSuccess); err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(&deckv1.CreateViewResponse{
		View: created,
		Idempotency: &deckv1.IdempotencyResult{
			Operation: deckv1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_VIEW,
			Replayed:  replayed,
		},
	}), nil
}

func newView(
	input *deckv1.CreateViewInput,
	canonicalQuery *deckv1.ViewQuery,
) (*deckv1.View, error) {
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 200 ||
		input.Kind != deckv1.ViewKind_VIEW_KIND_GITHUB_PULL_REQUESTS {
		return nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	sort := input.Sort
	if sort == deckv1.ViewSort_VIEW_SORT_UNSPECIFIED {
		sort = deckv1.ViewSort_VIEW_SORT_RECENTLY_UPDATED
	}
	if sort < deckv1.ViewSort_VIEW_SORT_RECENTLY_UPDATED ||
		sort > deckv1.ViewSort_VIEW_SORT_REVIEW_STATE {
		return nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	grouping := input.Grouping
	if grouping == deckv1.ViewGrouping_VIEW_GROUPING_UNSPECIFIED {
		grouping = deckv1.ViewGrouping_VIEW_GROUPING_NONE
	}
	if grouping < deckv1.ViewGrouping_VIEW_GROUPING_NONE ||
		grouping > deckv1.ViewGrouping_VIEW_GROUPING_STATUS {
		return nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	return &deckv1.View{
		Owner:                  proto.Clone(input.Owner).(*deckv1.Owner),
		Billing:                cloneBilling(input.Billing),
		Name:                   strings.TrimSpace(input.Name),
		Kind:                   input.Kind,
		Query:                  canonicalQuery,
		Sort:                   sort,
		Grouping:               grouping,
		NotificationPreference: cloneNotification(input.NotificationPreference),
		ConnectionState:        deckv1.ConnectionState_CONNECTION_STATE_DISCONNECTED,
	}, nil
}

func cloneBilling(value *deckv1.BillingSelection) *deckv1.BillingSelection {
	if value == nil {
		return nil
	}
	return proto.Clone(value).(*deckv1.BillingSelection)
}

func cloneNotification(
	value *deckv1.ViewNotificationPreference,
) *deckv1.ViewNotificationPreference {
	if value == nil {
		return &deckv1.ViewNotificationPreference{}
	}
	return proto.Clone(value).(*deckv1.ViewNotificationPreference)
}

func (service *View) UpdateView(
	ctx context.Context,
	request *connect.Request[deckv1.UpdateViewRequest],
) (*connect.Response[deckv1.UpdateViewResponse], error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseUUID(request.Msg.ViewId)
	if err != nil || request.Msg.View == nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	expected, err := validateExpected(request.Msg.ExpectedRevision, id,
		service.dependencies.Hasher)
	if err != nil {
		return nil, err
	}
	current, err := service.getAuthorizedView(ctx, viewer, id, true)
	if err != nil {
		return nil, err
	}
	authorizedOwnerID, _ := ownerID(current.Owner)
	if err := authorizeBilling(
		viewer, current.Owner, request.Msg.View.Billing); err != nil {
		return nil, err
	}
	updatedQuery, err := query.Apply(current.Query, request.Msg.View.Query)
	if err != nil || strings.TrimSpace(request.Msg.View.Name) == "" ||
		len(request.Msg.View.Name) > 200 {
		return nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	updated := proto.Clone(current).(*deckv1.View)
	updated.Billing = cloneBilling(request.Msg.View.Billing)
	updated.Name = strings.TrimSpace(request.Msg.View.Name)
	updated.Query = updatedQuery
	updated.Sort = request.Msg.View.Sort
	updated.Grouping = request.Msg.View.Grouping
	updated.NotificationPreference = cloneNotification(
		request.Msg.View.NotificationPreference)
	if updated.Sort == deckv1.ViewSort_VIEW_SORT_UNSPECIFIED {
		updated.Sort = deckv1.ViewSort_VIEW_SORT_RECENTLY_UPDATED
	}
	if updated.Grouping == deckv1.ViewGrouping_VIEW_GROUPING_UNSPECIFIED {
		updated.Grouping = deckv1.ViewGrouping_VIEW_GROUPING_NONE
	}
	if updated.Sort < deckv1.ViewSort_VIEW_SORT_RECENTLY_UPDATED ||
		updated.Sort > deckv1.ViewSort_VIEW_SORT_REVIEW_STATE ||
		updated.Grouping < deckv1.ViewGrouping_VIEW_GROUPING_NONE ||
		updated.Grouping > deckv1.ViewGrouping_VIEW_GROUPING_STATUS {
		return nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	allowed, err := service.canReadViewRepositories(ctx, viewer, updated)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, rpcerr.New(connect.CodePermissionDenied,
			deckv1.ErrorReason_ERROR_REASON_GITHUB_PERMISSION_DENIED)
	}
	queryChanged := !proto.Equal(current.Query, updated.Query)
	updated, err = service.dependencies.Store.UpdateView(
		ctx, id, expected, updated, queryChanged,
		service.dependencies.Clock.Now().UTC())
	if err != nil {
		return nil, service.mapStaleWithETag(err)
	}
	ownerHash := service.dependencies.Hasher.Sum(
		"owner", current.Owner.Scope.String()+":"+authorizedOwnerID.String())
	if err := service.recordAudit(ctx, viewer.Subject, audit.EventViewUpdated,
		current.Owner.Scope, ownerHash[:], audit.ResourceView, id,
		audit.OutcomeSuccess); err != nil {
		return nil, err
	}
	return connect.NewResponse(&deckv1.UpdateViewResponse{View: updated}), nil
}

func (service *View) DeleteView(
	ctx context.Context,
	request *connect.Request[deckv1.DeleteViewRequest],
) (*connect.Response[deckv1.DeleteViewResponse], error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseUUID(request.Msg.ViewId)
	if err != nil {
		return nil, err
	}
	expected, err := validateExpected(request.Msg.ExpectedRevision, id,
		service.dependencies.Hasher)
	if err != nil {
		return nil, err
	}
	current, err := service.getAuthorizedView(ctx, viewer, id, true)
	if err != nil {
		return nil, err
	}
	authorizedOwnerID, _ := ownerID(current.Owner)
	deletedRevision, err := service.dependencies.Store.DeleteView(
		ctx, id, expected, service.dependencies.Clock.Now().UTC())
	if err != nil {
		return nil, service.mapStaleWithETag(err)
	}
	ownerHash := service.dependencies.Hasher.Sum(
		"owner", current.Owner.Scope.String()+":"+authorizedOwnerID.String())
	if err := service.recordAudit(ctx, viewer.Subject, audit.EventViewDeleted,
		current.Owner.Scope, ownerHash[:], audit.ResourceView, id,
		audit.OutcomeSuccess); err != nil {
		return nil, err
	}
	return connect.NewResponse(&deckv1.DeleteViewResponse{
		ViewId: request.Msg.ViewId,
		DeletedRevision: &deckv1.Revision{
			Value: deletedRevision,
			Etag:  service.dependencies.Hasher.ETag(id, deletedRevision),
		},
	}), nil
}

func (service *View) ListPullRequests(
	ctx context.Context,
	request *connect.Request[deckv1.ListPullRequestsRequest],
) (*connect.Response[deckv1.ListPullRequestsResponse], error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	viewID, err := parseUUID(request.Msg.ViewId)
	if err != nil {
		return nil, err
	}
	view, err := service.getAuthorizedView(ctx, viewer, viewID, false)
	if err != nil {
		return nil, err
	}
	if _, err := query.ResolveViewer(view.Query.RawQuery, viewer.GitHubLogin); err != nil {
		return nil, rpcerr.New(connect.CodeInternal,
			deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
	}
	readableRepositories, err := service.dependencies.Repositories.
		ListReadableRepositories(ctx, viewer, view.Owner)
	if err != nil {
		if errors.Is(err, deckgithub.ErrReauthenticationRequired) {
			return nil, rpcerr.New(connect.CodeFailedPrecondition,
				deckv1.ErrorReason_ERROR_REASON_DISCONNECTED)
		}
		return nil, rpcerr.New(connect.CodeUnavailable,
			deckv1.ErrorReason_ERROR_REASON_DEPENDENCY_UNAVAILABLE)
	}
	readableRepositoryHashes := make(map[[32]byte]struct{},
		len(readableRepositories))
	for _, repository := range readableRepositories {
		hash := service.dependencies.Store.SnapshotRepositoryHash(
			&deckv1.RepositoryReference{
				Owner: repository.Owner,
				Name:  repository.Name,
			})
		readableRepositoryHashes[hash] = struct{}{}
	}
	viewerHash := service.dependencies.Hasher.Sum("snapshot-viewer", viewer.AccountID.String())
	snapshots, truncated, refreshedAt, err := service.dependencies.Store.ListSnapshots(
		ctx, viewID, viewerHash, readableRepositoryHashes)
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, err
		}
		return nil, mapDatabaseError(err)
	}
	authorized := snapshots
	pageLimit := pageSize(request.Msg.Page, 50, 100)
	offset := uint32(0)
	cursorKind := "pull-requests:" + viewID.String() + ":" + viewer.AccountID.String()
	if cursor := request.Msg.GetPage().GetCursor(); cursor != "" {
		payload, decodeErr := service.dependencies.Hasher.DecodeCursor(
			cursorKind, cursor, 4)
		if decodeErr != nil {
			return nil, rpcerr.New(connect.CodeInvalidArgument,
				deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
		}
		offset = decodeOffset(payload)
	}
	if offset > uint32(len(authorized)) {
		return nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	end := offset + pageLimit
	if end > uint32(len(authorized)) {
		end = uint32(len(authorized))
	}
	nextCursor := ""
	if end < uint32(len(authorized)) {
		nextCursor = service.dependencies.Hasher.EncodeCursor(
			cursorKind, encodeOffset(end))
	}
	freshness := deckv1.FreshnessState_FRESHNESS_STATE_NEVER_REFRESHED
	var refreshed *timestamppb.Timestamp
	if !refreshedAt.IsZero() {
		refreshed = timestamppb.New(refreshedAt)
		freshness = deckv1.FreshnessState_FRESHNESS_STATE_FRESH
	}
	return connect.NewResponse(&deckv1.ListPullRequestsResponse{
		PullRequests: authorized[offset:end],
		Page:         &deckv1.PageResponse{NextCursor: nextCursor},
		Truncated:    truncated,
		ResultLimit:  500,
		Freshness:    freshness,
		RefreshedAt:  refreshed,
		ViewRevision: view.Revision,
	}), nil
}

func (service *View) DeleteFeatureData(
	ctx context.Context,
	request *connect.Request[deckv1.DeleteFeatureDataRequest],
) (*connect.Response[deckv1.DeleteFeatureDataResponse], error) {
	now := service.dependencies.Clock.Now().UTC()
	var params database.DeleteFeatureDataParams
	var organization bool
	actorSubject := "lifecycle"
	ownerScope := deckv1.OwnerScope_OWNER_SCOPE_UNSPECIFIED
	if lifecycle := request.Msg.GetDelibaseLifecycle(); lifecycle != nil {
		if !authn.IsLifecycle(ctx) {
			return nil, rpcerr.New(connect.CodePermissionDenied,
				deckv1.ErrorReason_ERROR_REASON_PERMISSION_DENIED)
		}
		target := lifecycle.GetAccountId()
		trigger := database.DeletionTriggerAccountLifecycle
		if target == nil {
			target = lifecycle.GetOrganizationId()
			trigger = database.DeletionTriggerOrganizationLifecycle
			organization = true
		}
		targetID, err := parseUUID(target)
		if err != nil {
			return nil, err
		}
		replayID, err := parseUUID(lifecycle.DeletionJobId)
		if err != nil {
			return nil, err
		}
		params = database.DeleteFeatureDataParams{
			JobID: replayID, ReplayKey: replayID, TargetID: targetID,
			TargetHash: service.dependencies.Hasher.Sum(
				"owner", deletionOwnerLabel(organization)+":"+targetID.String()),
			Trigger: trigger, AcceptedAt: now,
		}
	} else {
		viewer, err := viewerFromContext(ctx)
		if err != nil {
			return nil, err
		}
		ownerRequest := request.Msg.GetOwnerRequest()
		if ownerRequest == nil {
			return nil, rpcerr.New(connect.CodeInvalidArgument,
				deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
		}
		targetID, err := ownerID(ownerRequest.Owner)
		if err != nil {
			return nil, err
		}
		switch ownerRequest.Owner.Scope {
		case deckv1.OwnerScope_OWNER_SCOPE_PERSONAL:
			if targetID != viewer.AccountID {
				return nil, rpcerr.New(connect.CodePermissionDenied,
					deckv1.ErrorReason_ERROR_REASON_PERMISSION_DENIED)
			}
		case deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION:
			if !viewer.IsOwner(targetID) {
				return nil, rpcerr.New(connect.CodePermissionDenied,
					deckv1.ErrorReason_ERROR_REASON_PERMISSION_DENIED)
			}
			organization = true
		default:
			return nil, rpcerr.New(connect.CodeInvalidArgument,
				deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
		}
		replayID, err := parseUUID(ownerRequest.GetIdempotencyKey().GetValue())
		if err != nil {
			return nil, err
		}
		jobID, err := service.dependencies.IDs.New()
		if err != nil {
			return nil, rpcerr.New(connect.CodeInternal,
				deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
		}
		ownerScope = ownerRequest.Owner.Scope
		actorSubject = viewer.Subject
		params = database.DeleteFeatureDataParams{
			JobID: jobID,
			ReplayKey: service.ownerDeletionReplayKey(
				viewer.Subject, ownerRequest.Owner.Scope, targetID, replayID),
			TargetID: targetID,
			TargetHash: service.dependencies.Hasher.Sum(
				"owner", deletionOwnerLabel(organization)+":"+targetID.String()),
			Trigger: database.DeletionTriggerOwner, AcceptedAt: now,
		}
	}
	var result database.DeletionResult
	var err error
	if organization {
		result, err = service.dependencies.Store.DeleteOrganizationFeatureData(ctx, params)
	} else {
		result, err = service.dependencies.Store.DeleteFeatureData(ctx, params)
	}
	if err != nil {
		return nil, mapDatabaseError(err)
	}
	eventType := audit.EventFeatureDeletionAccepted
	if params.Trigger == database.DeletionTriggerAccountLifecycle {
		eventType = audit.EventAccountLifecycleDeletion
	}
	if params.Trigger == database.DeletionTriggerOrganizationLifecycle {
		eventType = audit.EventOrganizationLifecycleDeletion
	}
	if err := service.recordAudit(ctx, actorSubject, eventType, ownerScope,
		params.TargetHash[:], audit.ResourceOwner, result.JobID,
		audit.OutcomeSuccess); err != nil {
		return nil, err
	}
	return connect.NewResponse(&deckv1.DeleteFeatureDataResponse{
		DeletionJobId: &deckv1.UuidV7{Value: result.JobID.String()},
		State:         deckv1.FeatureDeletionState_FEATURE_DELETION_STATE_COMPLETED,
		AcceptedAt:    timestamppb.New(result.AcceptedAt),
		Idempotency: &deckv1.IdempotencyResult{
			Operation: deckv1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_FEATURE_DATA,
			Replayed:  result.Replayed,
		},
	}), nil
}

func (service *View) ownerDeletionReplayKey(
	subject string,
	scope deckv1.OwnerScope,
	ownerID uuid.UUID,
	requested uuid.UUID,
) uuid.UUID {
	actorHash := service.dependencies.Hasher.Sum(
		"delete-feature-data-actor", subject)
	sum := service.dependencies.Hasher.Sum(
		"delete-feature-data-idempotency",
		string(actorHash[:])+"\x00"+scope.String()+"\x00"+
			ownerID.String()+"\x00"+requested.String())
	var scoped uuid.UUID
	copy(scoped[:6], requested[:6])
	copy(scoped[6:], sum[:len(scoped)-6])
	scoped[6] = scoped[6]&0x0f | 0x70
	scoped[8] = scoped[8]&0x3f | 0x80
	return scoped
}

func deletionOwnerLabel(organization bool) string {
	if organization {
		return "OWNER_SCOPE_ORGANIZATION"
	}
	return "OWNER_SCOPE_PERSONAL"
}

func (service *View) GetRefreshPreflight(
	context.Context,
	*connect.Request[deckv1.GetRefreshPreflightRequest],
) (*connect.Response[deckv1.GetRefreshPreflightResponse], error) {
	return nil, rpcerr.New(connect.CodeUnavailable,
		deckv1.ErrorReason_ERROR_REASON_BILLING_CATALOG_UNAVAILABLE)
}

func (service *View) RefreshView(
	context.Context,
	*connect.Request[deckv1.RefreshViewRequest],
) (*connect.Response[deckv1.RefreshViewResponse], error) {
	return nil, rpcerr.New(connect.CodeUnavailable,
		deckv1.ErrorReason_ERROR_REASON_BILLING_CATALOG_UNAVAILABLE)
}

func (service *View) mapStaleWithETag(err error) error {
	var stale *database.StaleError
	if !errors.As(err, &stale) || stale.Revision == 0 {
		return mapDatabaseError(err)
	}
	return rpcerr.Conflict(
		deckv1.ErrorReason_ERROR_REASON_STALE_REVISION,
		&deckv1.UuidV7{Value: stale.ResourceID.String()},
		&deckv1.Revision{
			Value: stale.Revision,
			Etag: service.dependencies.Hasher.ETag(
				stale.ResourceID, stale.Revision),
		},
	)
}

func (service *View) recordAudit(
	ctx context.Context,
	subject string,
	eventType audit.EventType,
	ownerScope deckv1.OwnerScope,
	targetHash []byte,
	resourceType audit.ResourceType,
	resourceID uuid.UUID,
	outcome audit.Outcome,
) error {
	id, err := service.dependencies.IDs.New()
	if err != nil || service.dependencies.Audits == nil ||
		service.dependencies.Pseudonymizer == nil {
		return rpcerr.New(connect.CodeInternal,
			deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
	}
	actor := service.dependencies.Pseudonymizer.Actor(subject)
	if actor == "" {
		return rpcerr.New(connect.CodeInternal,
			deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
	}
	err = service.dependencies.Audits.Record(ctx, audit.Event{
		ID: id, Type: eventType, ActorPseudonym: string(actor),
		OwnerScope: int16(ownerScope), TargetHash: targetHash,
		ResourceType: resourceType, ResourceID: resourceID,
		Outcome: outcome, OccurredAt: service.dependencies.Clock.Now().UTC(),
	})
	if err != nil {
		safelog.Record(ctx, service.dependencies.Logger, slog.LevelError,
			safelog.EventAuthorization, safelog.Fields{
				Actor:  safelog.ActorPseudonym(actor),
				Result: safelog.ResultFailure,
			})
		return rpcerr.New(connect.CodeInternal,
			deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
	}
	return nil
}

func (service *View) resolvedQueryForViewer(
	view *deckv1.View,
	login string,
) (string, error) {
	return query.ResolveViewer(view.GetQuery().GetRawQuery(), login)
}

func (service *View) canReadViewRepositories(
	ctx context.Context,
	viewer contracts.Viewer,
	view *deckv1.View,
) (bool, error) {
	if view == nil || view.Query == nil || view.Query.Builder == nil {
		return false, rpcerr.New(connect.CodeInternal,
			deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
	}
	for _, clause := range view.Query.Builder.Clauses {
		repository := clause.GetRepository()
		if repository == nil {
			continue
		}
		allowed, err := service.dependencies.Repositories.CanReadRepository(
			ctx, viewer, view.Owner,
			repository.Owner, repository.Repository)
		if err != nil {
			if errors.Is(err, deckgithub.ErrReauthenticationRequired) {
				return false, rpcerr.New(connect.CodeFailedPrecondition,
					deckv1.ErrorReason_ERROR_REASON_DISCONNECTED)
			}
			return false, rpcerr.New(connect.CodeUnavailable,
				deckv1.ErrorReason_ERROR_REASON_DEPENDENCY_UNAVAILABLE)
		}
		if !allowed {
			return false, nil
		}
	}
	return true, nil
}

func (service *View) getAuthorizedView(
	ctx context.Context,
	viewer contracts.Viewer,
	id uuid.UUID,
	manage bool,
) (*deckv1.View, error) {
	view, err := service.dependencies.Store.GetViewAuthorized(
		ctx, id, service.viewDefinitionAuthorizer(ctx, viewer, manage))
	if err != nil {
		return nil, mapAuthorizedViewError(err)
	}
	return view, nil
}

func (service *View) viewDefinitionAuthorizer(
	ctx context.Context,
	viewer contracts.Viewer,
	manage bool,
) database.ViewAuthorizer {
	var readable map[[32]byte]struct{}
	var readableErr error
	loaded := false
	return func(authorization database.ViewAuthorization) error {
		if _, err := authorizeOwner(viewer, authorization.Owner, manage); err != nil {
			return err
		}
		if manage {
			return nil
		}
		if authorization.ConnectionState ==
			deckv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
			return nil
		}
		if !authorization.HasRepositoryIndex {
			return rpcerr.New(connect.CodeInternal,
				deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
		}
		if !loaded {
			loaded = true
			repositories, err := service.dependencies.Repositories.
				ListReadableRepositories(ctx, viewer, authorization.Owner)
			if err != nil {
				if errors.Is(err, deckgithub.ErrReauthenticationRequired) {
					readableErr = rpcerr.New(connect.CodeFailedPrecondition,
						deckv1.ErrorReason_ERROR_REASON_DISCONNECTED)
				} else {
					readableErr = rpcerr.New(connect.CodeUnavailable,
						deckv1.ErrorReason_ERROR_REASON_DEPENDENCY_UNAVAILABLE)
				}
			} else {
				readable = make(map[[32]byte]struct{}, len(repositories))
				for _, repository := range repositories {
					hash := service.dependencies.Hasher.Sum(
						"view-repository",
						strings.ToLower(repository.Owner)+"\x00"+
							strings.ToLower(repository.Name))
					readable[hash] = struct{}{}
				}
			}
		}
		if readableErr != nil {
			return readableErr
		}
		for _, hash := range authorization.RepositoryHashes {
			if _, allowed := readable[hash]; !allowed {
				return database.ErrViewNotVisible
			}
		}
		return nil
	}
}

func mapAuthorizedViewError(err error) error {
	if errors.Is(err, database.ErrViewNotVisible) {
		return rpcerr.New(connect.CodePermissionDenied,
			deckv1.ErrorReason_ERROR_REASON_GITHUB_PERMISSION_DENIED)
	}
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return err
	}
	return mapDatabaseError(err)
}
