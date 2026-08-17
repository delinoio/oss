package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"github.com/delinoio/oss/protos/gen/go/devhud/v1/devhudv1connect"
	"github.com/delinoio/oss/servers/devhud-api/internal/auth"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	uploadmanager "github.com/delinoio/oss/servers/devhud-api/internal/upload"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const maximumAdminSearchBytes = 512

type AdminService struct {
	repository         domain.AdminRepository
	uploads            domain.UploadAdministration
	clock              domain.Clock
	ids                domain.IDGenerator
	logger             *slog.Logger
	cursors            *adminCursorCodec
	publicAssetBaseURL *url.URL
}

func NewAdminService(repository domain.AdminRepository, uploads domain.UploadAdministration, clock domain.Clock, ids domain.IDGenerator, logger *slog.Logger, cursorKey []byte, rawPublicAssetBaseURL string) (*AdminService, error) {
	cursors, err := newAdminCursorCodec(cursorKey)
	if err != nil {
		return nil, err
	}
	publicAssetBaseURL, err := url.Parse(rawPublicAssetBaseURL)
	if err != nil || !publicAssetBaseURL.IsAbs() || publicAssetBaseURL.Hostname() == "" {
		return nil, errors.New("public asset base URL must be absolute")
	}
	return &AdminService{repository: repository, uploads: uploads, clock: clock, ids: ids, logger: logger, cursors: cursors, publicAssetBaseURL: publicAssetBaseURL}, nil
}

func (s *AdminService) ListUsers(ctx context.Context, request *connect.Request[devhudv1.ListUsersRequest]) (*connect.Response[devhudv1.ListUsersResponse], error) {
	principal, err := requireAdministrator(ctx)
	if err != nil {
		return nil, err
	}
	query := normalizeAdminSearch(request.Msg.GetQuery())
	if !utf8.ValidString(request.Msg.GetQuery()) || len(query) > maximumAdminSearchBytes {
		return nil, NewError(connect.CodeInvalidArgument, "query must be well-formed Unicode and at most 512 UTF-8 bytes", CorrelationID(ctx))
	}
	pageSize, pageToken, err := adminPage(request.Msg.GetPage())
	if err != nil {
		return nil, adminPaginationError(ctx, err)
	}
	var cursor *domain.UserCursor
	if pageToken != "" {
		createdAt, id, err := s.cursors.decode(pageToken, "users", principal.User.ID, query, s.clock.Now())
		if err != nil {
			return nil, adminPaginationError(ctx, err)
		}
		cursor = &domain.UserCursor{CreatedAt: createdAt, UserID: id}
	}
	result, err := s.repository.ListUsers(ctx, query, cursor, pageSize)
	if err != nil {
		return nil, s.internal(ctx, devhudv1connect.AdminServiceListUsersProcedure, err)
	}
	users := make([]*devhudv1.AdminUser, 0, len(result.Users))
	for _, user := range result.Users {
		users = append(users, adminUserMessage(user))
	}
	next := ""
	if result.Next != nil {
		next, err = s.cursors.encode("users", principal.User.ID, query, result.Next.UserID, result.Next.CreatedAt, s.clock.Now())
		if err != nil {
			return nil, s.internal(ctx, devhudv1connect.AdminServiceListUsersProcedure, err)
		}
	}
	return connect.NewResponse(&devhudv1.ListUsersResponse{Metadata: metadata(CorrelationID(ctx)), Users: users, NextPageToken: next}), nil
}

func (s *AdminService) SetUserBlocked(ctx context.Context, request *connect.Request[devhudv1.SetUserBlockedRequest]) (*connect.Response[devhudv1.SetUserBlockedResponse], error) {
	principal, err := requireAdministrator(ctx)
	if err != nil {
		return nil, err
	}
	targetID := request.Msg.GetUserId().GetValue()
	target, okTarget := domainBlockState(request.Msg.GetTargetState())
	expected, okExpected := domainBlockState(request.Msg.GetExpectedState())
	reason := norm.NFC.String(strings.TrimSpace(request.Msg.GetReason()))
	reasonErr := uploadmanager.ValidateAdministratorReason(reason, s.publicAssetBaseURL)
	auditReason := reason
	if reasonErr != nil {
		auditReason = ""
	}
	action := domain.AuditActionUserBlocked
	if target == domain.AdministrativeBlockStateUnblocked {
		action = domain.AuditActionUserUnblocked
	}
	event, eventErr := s.mutationEvent(ctx, principal.User.ID, targetID, "", action, auditReason)
	if eventErr != nil {
		return nil, internalError(ctx)
	}
	if !validUUIDv7(request.Msg.GetUserId()) || !okTarget || !okExpected || target == expected || reasonErr != nil {
		s.reject(ctx, event, domain.AuditRejectionInvalidArgument)
		return nil, NewError(connect.CodeInvalidArgument, "user_id, distinct expected/target states, and a non-empty safe reason are required", CorrelationID(ctx))
	}
	user, err := s.repository.SetUserBlocked(ctx, principal.User.ID, targetID, expected, target, event, s.clock.Now())
	if err != nil {
		var conflict *domain.AdminConflictError
		if errors.As(err, &conflict) {
			return nil, NewError(connect.CodeAborted, "user changed concurrently", CorrelationID(ctx), adminConflictMessage(conflict))
		}
		if errors.Is(err, domain.ErrNotFound) {
			return nil, NewError(connect.CodeNotFound, "user was not found", CorrelationID(ctx))
		}
		var permission *domain.PermissionError
		if errors.As(err, &permission) {
			return nil, permissionError(ctx, permission)
		}
		return nil, s.internal(ctx, devhudv1connect.AdminServiceSetUserBlockedProcedure, err)
	}
	event.Outcome = domain.AuditOutcomeAccepted
	return connect.NewResponse(&devhudv1.SetUserBlockedResponse{Metadata: metadata(CorrelationID(ctx)), User: adminUserMessage(user), AuditEvent: auditEventMessage(event)}), nil
}

func (s *AdminService) GetUserUsage(ctx context.Context, request *connect.Request[devhudv1.GetUserUsageRequest]) (*connect.Response[devhudv1.GetUserUsageResponse], error) {
	if _, err := requireAdministrator(ctx); err != nil {
		return nil, err
	}
	if !validUUIDv7(request.Msg.GetUserId()) {
		return nil, NewError(connect.CodeInvalidArgument, "user_id must be a canonical UUID v7", CorrelationID(ctx))
	}
	if s.uploads == nil {
		return nil, NewError(connect.CodeFailedPrecondition, "official uploads are unavailable", CorrelationID(ctx))
	}
	usage, err := s.uploads.GetUsage(ctx, request.Msg.GetUserId().GetValue())
	if err != nil {
		return nil, s.internal(ctx, devhudv1connect.AdminServiceGetUserUsageProcedure, err)
	}
	counters := []*devhudv1.UsageCounter{
		{Quota: devhudv1.QuotaKind_QUOTA_KIND_SIGNED_URLS_ROLLING_HOUR, Used: usage.SignedURLsRollingHour, Limit: domain.UploadMaximumURLsPerHour, Window: durationpb.New(time.Hour)},
		{Quota: devhudv1.QuotaKind_QUOTA_KIND_UPLOAD_BYTES_ROLLING_DAY, Used: usage.UploadBytesRollingDay, Limit: domain.UploadMaximumRollingDayBytes, Window: durationpb.New(24 * time.Hour)},
		{Quota: devhudv1.QuotaKind_QUOTA_KIND_STORED_BYTES, Used: usage.StoredBytes, Limit: domain.UploadMaximumStoredBytes},
		{Quota: devhudv1.QuotaKind_QUOTA_KIND_OBJECT_BYTES, Limit: domain.UploadMaximumObjectBytes},
	}
	ids := make([]string, 0, len(usage.SubmissionImages))
	for submissionID := range usage.SubmissionImages {
		ids = append(ids, submissionID)
	}
	slices.Sort(ids)
	for _, submissionID := range ids {
		counters = append(counters, &devhudv1.UsageCounter{Quota: devhudv1.QuotaKind_QUOTA_KIND_SUBMISSION_IMAGES, Used: usage.SubmissionImages[submissionID], Limit: domain.UploadMaximumSubmissionImages, SubmissionId: uuid(submissionID)})
	}
	return connect.NewResponse(&devhudv1.GetUserUsageResponse{Metadata: metadata(CorrelationID(ctx)), UserId: request.Msg.GetUserId(), Counters: counters}), nil
}

func (s *AdminService) ListUploads(ctx context.Context, request *connect.Request[devhudv1.AdminServiceListUploadsRequest]) (*connect.Response[devhudv1.AdminServiceListUploadsResponse], error) {
	principal, err := requireAdministrator(ctx)
	if err != nil {
		return nil, err
	}
	if s.uploads == nil {
		return nil, NewError(connect.CodeFailedPrecondition, "official uploads are unavailable", CorrelationID(ctx))
	}
	filters := domain.AdminUploadFilters{}
	for name, value := range map[string]*devhudv1.UuidV7{"owner_user_id": request.Msg.GetOwnerUserId(), "submission_id": request.Msg.GetSubmissionId(), "upload_group_id": request.Msg.GetUploadGroupId()} {
		if value != nil && !validUUIDv7(value) {
			return nil, NewError(connect.CodeInvalidArgument, name+" must be a canonical UUID v7", CorrelationID(ctx))
		}
	}
	filters.OwnerUserID = request.Msg.GetOwnerUserId().GetValue()
	filters.SubmissionID = request.Msg.GetSubmissionId().GetValue()
	filters.UploadGroupID = request.Msg.GetUploadGroupId().GetValue()
	filters.States, err = uploadStates(request.Msg.GetStates())
	if err != nil {
		return nil, NewError(connect.CodeInvalidArgument, err.Error(), CorrelationID(ctx))
	}
	pageSize, pageToken, err := adminPage(request.Msg.GetPage())
	if err != nil {
		return nil, adminPaginationError(ctx, err)
	}
	result, next, err := s.uploads.ListUploads(ctx, principal.User.ID, filters, pageToken, pageSize)
	if err != nil {
		if strings.Contains(err.Error(), "page token") {
			return nil, adminPaginationError(ctx, err)
		}
		return nil, s.internal(ctx, devhudv1connect.AdminServiceListUploadsProcedure, err)
	}
	messages := make([]*devhudv1.AdminUpload, 0, len(result.Uploads))
	for _, upload := range result.Uploads {
		messages = append(messages, adminUploadMessage(upload))
	}
	return connect.NewResponse(&devhudv1.AdminServiceListUploadsResponse{Metadata: metadata(CorrelationID(ctx)), Uploads: messages, NextPageToken: next}), nil
}

func (s *AdminService) QuarantineUpload(ctx context.Context, request *connect.Request[devhudv1.QuarantineUploadRequest]) (*connect.Response[devhudv1.QuarantineUploadResponse], error) {
	upload, event, err := s.mutateUpload(ctx, request.Msg.GetUploadId(), request.Msg.GetExpectedState(), request.Msg.GetReason(), domain.RemovalReasonAdministratorQuarantined)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&devhudv1.QuarantineUploadResponse{Metadata: metadata(CorrelationID(ctx)), Upload: adminUploadMessage(upload), AuditEvent: auditEventMessage(event)}), nil
}

func (s *AdminService) DeleteUpload(ctx context.Context, request *connect.Request[devhudv1.AdminServiceDeleteUploadRequest]) (*connect.Response[devhudv1.AdminServiceDeleteUploadResponse], error) {
	upload, event, err := s.mutateUpload(ctx, request.Msg.GetUploadId(), request.Msg.GetExpectedState(), request.Msg.GetReason(), domain.RemovalReasonAdministratorDeleted)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&devhudv1.AdminServiceDeleteUploadResponse{Metadata: metadata(CorrelationID(ctx)), Upload: adminUploadMessage(upload), AuditEvent: auditEventMessage(event)}), nil
}

func (s *AdminService) mutateUpload(ctx context.Context, uploadID *devhudv1.UuidV7, expectedValue devhudv1.UploadState, rawReason string, removal domain.RemovalReason) (domain.Upload, domain.AuditEvent, error) {
	principal, err := requireAdministrator(ctx)
	if err != nil {
		return domain.Upload{}, domain.AuditEvent{}, err
	}
	action := domain.AuditActionUploadDeleted
	if removal == domain.RemovalReasonAdministratorQuarantined {
		action = domain.AuditActionUploadQuarantined
	}
	reason := norm.NFC.String(strings.TrimSpace(rawReason))
	reasonErr := uploadmanager.ValidateAdministratorReason(reason, s.publicAssetBaseURL)
	auditReason := reason
	if reasonErr != nil {
		auditReason = ""
	}
	event, eventErr := s.mutationEvent(ctx, principal.User.ID, "", uploadID.GetValue(), action, auditReason)
	if eventErr != nil {
		return domain.Upload{}, domain.AuditEvent{}, internalError(ctx)
	}
	expected, ok := adminExpectedUploadState(expectedValue)
	if s.uploads == nil {
		s.reject(ctx, event, domain.AuditRejectionFailedPrecondition)
		return domain.Upload{}, domain.AuditEvent{}, NewError(connect.CodeFailedPrecondition, "official uploads are unavailable", CorrelationID(ctx))
	}
	if !validUUIDv7(uploadID) || !ok || reasonErr != nil ||
		(removal == domain.RemovalReasonAdministratorQuarantined && expected != domain.UploadStateFinalized) ||
		(removal == domain.RemovalReasonAdministratorDeleted && expected != domain.UploadStatePending && expected != domain.UploadStateFinalized && expected != domain.UploadStateQuarantined) {
		s.reject(ctx, event, domain.AuditRejectionInvalidArgument)
		return domain.Upload{}, domain.AuditEvent{}, NewError(connect.CodeInvalidArgument, "upload_id, allowed expected state, and a non-empty safe reason are required", CorrelationID(ctx))
	}
	upload, err := s.uploads.RemoveUpload(ctx, principal.User.ID, uploadID.GetValue(), removal, expected, reason, event)
	if err != nil {
		rejection := domain.AuditRejectionOperationFailed
		code, message := connect.CodeInternal, "upload mutation failed"
		var conflict *domain.AdminConflictError
		if errors.As(err, &conflict) {
			rejection, code, message = domain.AuditRejectionConcurrentUpdate, connect.CodeAborted, "upload changed concurrently"
			s.reject(ctx, event, rejection)
			return domain.Upload{}, domain.AuditEvent{}, NewError(code, message, CorrelationID(ctx), adminConflictMessage(conflict))
		}
		if errors.Is(err, domain.ErrNotFound) {
			rejection, code, message = domain.AuditRejectionTargetNotFound, connect.CodeNotFound, "upload was not found"
		} else {
			var permission *domain.PermissionError
			if errors.As(err, &permission) {
				rejection, code, message = domain.AuditRejectionActorBlocked, connect.CodePermissionDenied, "administrator account is blocked"
			}
			var uploadFailure *domain.UploadError
			if errors.As(err, &uploadFailure) {
				rejection, code, message = domain.AuditRejectionFailedPrecondition, connect.CodeFailedPrecondition, "upload state does not allow the mutation"
			}
		}
		s.reject(ctx, event, rejection)
		if code == connect.CodeInternal {
			s.logger.ErrorContext(ctx, "administrator upload mutation failed", "correlation_id", CorrelationID(ctx), "error_type", fmt.Sprintf("%T", err))
		}
		return domain.Upload{}, domain.AuditEvent{}, NewError(code, message, CorrelationID(ctx))
	}
	event.Outcome = domain.AuditOutcomeAccepted
	return upload, event, nil
}

func (s *AdminService) ListAuditEvents(ctx context.Context, request *connect.Request[devhudv1.ListAuditEventsRequest]) (*connect.Response[devhudv1.ListAuditEventsResponse], error) {
	principal, err := requireAdministrator(ctx)
	if err != nil {
		return nil, err
	}
	filters := domain.AuditFilters{}
	for name, value := range map[string]*devhudv1.UuidV7{"actor_user_id": request.Msg.GetActorUserId(), "target_user_id": request.Msg.GetTargetUserId(), "target_upload_id": request.Msg.GetTargetUploadId(), "correlation_id": request.Msg.GetCorrelationId()} {
		if value != nil && !validUUIDv7(value) {
			return nil, NewError(connect.CodeInvalidArgument, name+" must be a canonical UUID v7", CorrelationID(ctx))
		}
	}
	filters.ActorUserID, filters.TargetUserID = request.Msg.GetActorUserId().GetValue(), request.Msg.GetTargetUserId().GetValue()
	filters.TargetUploadID, filters.CorrelationID = request.Msg.GetTargetUploadId().GetValue(), request.Msg.GetCorrelationId().GetValue()
	for _, value := range request.Msg.GetActions() {
		action, ok := domainAuditAction(value)
		if !ok {
			return nil, NewError(connect.CodeInvalidArgument, "actions contains an unsupported value", CorrelationID(ctx))
		}
		filters.Actions = append(filters.Actions, action)
	}
	for _, value := range request.Msg.GetOutcomes() {
		if value != devhudv1.AuditOutcome_AUDIT_OUTCOME_ACCEPTED && value != devhudv1.AuditOutcome_AUDIT_OUTCOME_REJECTED {
			return nil, NewError(connect.CodeInvalidArgument, "outcomes contains an unsupported value", CorrelationID(ctx))
		}
		filters.Outcomes = append(filters.Outcomes, domain.AuditOutcome(value))
	}
	slices.Sort(filters.Actions)
	filters.Actions = slices.Compact(filters.Actions)
	slices.Sort(filters.Outcomes)
	filters.Outcomes = slices.Compact(filters.Outcomes)
	pageSize, pageToken, err := adminPage(request.Msg.GetPage())
	if err != nil {
		return nil, adminPaginationError(ctx, err)
	}
	scopeBytes, _ := json.Marshal(filters)
	scope := string(scopeBytes)
	var cursor *domain.AuditCursor
	if pageToken != "" {
		createdAt, id, err := s.cursors.decode(pageToken, "audit", principal.User.ID, scope, s.clock.Now())
		if err != nil {
			return nil, adminPaginationError(ctx, err)
		}
		cursor = &domain.AuditCursor{CreatedAt: createdAt, AuditID: id}
	}
	result, err := s.repository.ListAuditEvents(ctx, filters, cursor, pageSize)
	if err != nil {
		return nil, s.internal(ctx, devhudv1connect.AdminServiceListAuditEventsProcedure, err)
	}
	events := make([]*devhudv1.AuditEvent, 0, len(result.Events))
	for _, event := range result.Events {
		events = append(events, auditEventMessage(event))
	}
	next := ""
	if result.Next != nil {
		next, err = s.cursors.encode("audit", principal.User.ID, scope, result.Next.AuditID, result.Next.CreatedAt, s.clock.Now())
		if err != nil {
			return nil, s.internal(ctx, devhudv1connect.AdminServiceListAuditEventsProcedure, err)
		}
	}
	return connect.NewResponse(&devhudv1.ListAuditEventsResponse{Metadata: metadata(CorrelationID(ctx)), AuditEvents: events, NextPageToken: next}), nil
}

func requireAdministrator(ctx context.Context) (auth.Principal, error) {
	principal, ok := auth.PrincipalFromContext(ctx)
	if !ok {
		return auth.Principal{}, unauthenticatedError(ctx)
	}
	if !slices.Contains(principal.Roles, domain.AdminRole) {
		return auth.Principal{}, adminRolePermissionError(ctx)
	}
	return principal, nil
}

func adminPage(page *devhudv1.PageRequest) (uint32, string, error) {
	if page == nil {
		return domain.AdminDefaultPageSize, "", nil
	}
	size := page.GetPageSize()
	if size == 0 {
		size = domain.AdminDefaultPageSize
	}
	if size > domain.AdminMaximumPageSize {
		return 0, "", errors.New("page size exceeds maximum")
	}
	if len(page.GetPageToken()) > domain.AdminMaximumTokenSize {
		return 0, "", errors.New("invalid page token")
	}
	return size, page.GetPageToken(), nil
}

func adminPaginationError(ctx context.Context, err error) error {
	reason := devhudv1.PaginationFailureReason_PAGINATION_FAILURE_REASON_TOKEN_INVALID
	if strings.Contains(err.Error(), "page size") {
		reason = devhudv1.PaginationFailureReason_PAGINATION_FAILURE_REASON_PAGE_SIZE_TOO_LARGE
	} else if strings.Contains(err.Error(), "expired") {
		reason = devhudv1.PaginationFailureReason_PAGINATION_FAILURE_REASON_TOKEN_EXPIRED
	} else if strings.Contains(err.Error(), "scope") {
		reason = devhudv1.PaginationFailureReason_PAGINATION_FAILURE_REASON_TOKEN_SCOPE_MISMATCH
	}
	return NewError(connect.CodeInvalidArgument, err.Error(), CorrelationID(ctx), &devhudv1.PaginationFailure{Reason: reason})
}

func normalizeAdminSearch(value string) string {
	return norm.NFC.String(cases.Fold().String(strings.TrimSpace(norm.NFC.String(value))))
}

func (s *AdminService) mutationEvent(ctx context.Context, actorID, targetUserID, targetUploadID string, action domain.AuditAction, reason string) (domain.AuditEvent, error) {
	id, err := s.ids.New()
	if err != nil {
		return domain.AuditEvent{}, err
	}
	now := s.clock.Now()
	event := domain.AuditEvent{ID: id, CorrelationID: CorrelationID(ctx), Action: action, Reason: reason, CreatedAt: now, ExpiresAt: now.Add(domain.AuditRetention)}
	event.ActorUserID = &actorID
	if targetUserID != "" {
		event.TargetUserID = &targetUserID
	}
	if targetUploadID != "" {
		event.TargetUploadID = &targetUploadID
	}
	return event, nil
}

func (s *AdminService) reject(ctx context.Context, event domain.AuditEvent, reason domain.AuditRejectionReason) {
	event.Outcome, event.RejectionReason = domain.AuditOutcomeRejected, reason
	if err := s.repository.RecordAdministratorAudit(ctx, event); err != nil {
		s.logger.WarnContext(ctx, "administrator rejection audit failed", "correlation_id", CorrelationID(ctx), "error_type", fmt.Sprintf("%T", err))
	}
}

func (s *AdminService) internal(ctx context.Context, procedure string, err error) error {
	s.logger.ErrorContext(ctx, "administrator operation failed", "correlation_id", CorrelationID(ctx), "procedure", procedure, "error_type", fmt.Sprintf("%T", err))
	return internalError(ctx)
}

func adminUserMessage(user domain.User) *devhudv1.AdminUser {
	message := &devhudv1.AdminUser{
		UserId: uuid(user.ID), LogtoSubject: user.Subject, DisplayName: user.DisplayName, Email: user.Email,
		DeletionState: deletionState(user.DeletionState), AdministrativeBlockState: administrativeBlockState(user.AdministrativeBlockState),
		CreatedAt: timestamppb.New(user.CreatedAt), UpdatedAt: timestamppb.New(user.UpdatedAt),
	}
	if user.RecoverableUntil != nil {
		message.RecoverableUntil = timestamppb.New(*user.RecoverableUntil)
	}
	return message
}

func adminUploadMessage(upload domain.Upload) *devhudv1.AdminUpload {
	message := &devhudv1.AdminUpload{
		OwnerUserId: uuid(upload.OwnerUserID), UploadId: uuid(upload.UploadID), SubmissionId: uuid(upload.SubmissionID), UploadGroupId: uuid(upload.UploadGroupID),
		State: protocolUploadState(upload), ContentType: devhudv1.UploadContentType_UPLOAD_CONTENT_TYPE_PNG,
		SizeBytes: upload.SizeBytes, Sha256: append([]byte(nil), upload.SHA256[:]...), StagingGeneration: upload.StagingGeneration,
		Width: upload.Width, Height: upload.Height, CreatedAt: timestamppb.New(upload.CreatedAt),
	}
	if upload.FinalizedAt != nil {
		message.FinalizedAt = timestamppb.New(*upload.FinalizedAt)
	}
	if upload.RemovedAt != nil {
		message.RemovedAt = timestamppb.New(*upload.RemovedAt)
	}
	return message
}

func auditEventMessage(event domain.AuditEvent) *devhudv1.AuditEvent {
	message := &devhudv1.AuditEvent{
		AuditEventId: uuid(event.ID), Action: protocolAuditAction(event.Action), Reason: event.Reason,
		CreatedAt: timestamppb.New(event.CreatedAt), CorrelationId: uuid(event.CorrelationID),
		Outcome: devhudv1.AuditOutcome(event.Outcome), RejectionReason: devhudv1.AuditRejectionReason(event.RejectionReason),
	}
	if event.ActorUserID != nil {
		message.ActorUserId = uuid(*event.ActorUserID)
	}
	if event.TargetUserID != nil {
		message.TargetUserId = uuid(*event.TargetUserID)
	}
	if event.TargetUploadID != nil {
		message.TargetUploadId = uuid(*event.TargetUploadID)
	}
	return message
}

func protocolAuditAction(value domain.AuditAction) devhudv1.AuditAction {
	switch value {
	case domain.AuditActionUserBlocked:
		return devhudv1.AuditAction_AUDIT_ACTION_USER_BLOCKED
	case domain.AuditActionUserUnblocked:
		return devhudv1.AuditAction_AUDIT_ACTION_USER_UNBLOCKED
	case domain.AuditActionUploadQuarantined:
		return devhudv1.AuditAction_AUDIT_ACTION_UPLOAD_QUARANTINED
	case domain.AuditActionUploadDeleted:
		return devhudv1.AuditAction_AUDIT_ACTION_UPLOAD_DELETED
	case domain.AuditActionAccountDeletionRequested:
		return devhudv1.AuditAction_AUDIT_ACTION_ACCOUNT_DELETION_REQUESTED
	case domain.AuditActionAccountRestored:
		return devhudv1.AuditAction_AUDIT_ACTION_ACCOUNT_RESTORED
	case domain.AuditActionAccountPurgeClaimed:
		return devhudv1.AuditAction_AUDIT_ACTION_ACCOUNT_PURGE_CLAIMED
	case domain.AuditActionAccountPurged:
		return devhudv1.AuditAction_AUDIT_ACTION_ACCOUNT_PURGED
	case domain.AuditActionUploadAccountPurged:
		return devhudv1.AuditAction_AUDIT_ACTION_UPLOAD_ACCOUNT_PURGED
	default:
		return devhudv1.AuditAction_AUDIT_ACTION_UNSPECIFIED
	}
}

func domainAuditAction(value devhudv1.AuditAction) (domain.AuditAction, bool) {
	for _, action := range []domain.AuditAction{
		domain.AuditActionUserBlocked, domain.AuditActionUserUnblocked, domain.AuditActionUploadQuarantined,
		domain.AuditActionUploadDeleted, domain.AuditActionAccountDeletionRequested, domain.AuditActionAccountRestored,
		domain.AuditActionAccountPurgeClaimed, domain.AuditActionAccountPurged, domain.AuditActionUploadAccountPurged,
	} {
		if protocolAuditAction(action) == value {
			return action, true
		}
	}
	return 0, false
}

func domainBlockState(value devhudv1.AdministrativeBlockState) (domain.AdministrativeBlockState, bool) {
	switch value {
	case devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_UNBLOCKED:
		return domain.AdministrativeBlockStateUnblocked, true
	case devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_BLOCKED:
		return domain.AdministrativeBlockStateBlocked, true
	default:
		return 0, false
	}
}

func adminExpectedUploadState(value devhudv1.UploadState) (domain.UploadState, bool) {
	switch value {
	case devhudv1.UploadState_UPLOAD_STATE_PENDING:
		return domain.UploadStatePending, true
	case devhudv1.UploadState_UPLOAD_STATE_FINALIZED:
		return domain.UploadStateFinalized, true
	case devhudv1.UploadState_UPLOAD_STATE_QUARANTINED:
		return domain.UploadStateQuarantined, true
	default:
		return 0, false
	}
}

func adminConflictMessage(conflict *domain.AdminConflictError) *devhudv1.AdminMutationConflict {
	message := &devhudv1.AdminMutationConflict{}
	if conflict.User != nil {
		message.Current = &devhudv1.AdminMutationConflict_User{User: adminUserMessage(*conflict.User)}
	}
	if conflict.Upload != nil {
		message.Current = &devhudv1.AdminMutationConflict_Upload{Upload: adminUploadMessage(*conflict.Upload)}
	}
	return message
}

var _ devhudv1connect.AdminServiceHandler = (*AdminService)(nil)
