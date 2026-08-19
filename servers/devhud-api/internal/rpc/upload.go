package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"github.com/delinoio/oss/protos/gen/go/devhud/v1/devhudv1connect"
	"github.com/delinoio/oss/servers/devhud-api/internal/auth"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	uploadmanager "github.com/delinoio/oss/servers/devhud-api/internal/upload"
	googleuuid "github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type UploadService struct {
	service *uploadmanager.Service
	logger  *slog.Logger
}

func NewUploadService(service *uploadmanager.Service, logger *slog.Logger) *UploadService {
	return &UploadService{service: service, logger: logger}
}

func (s *UploadService) CreateUpload(ctx context.Context, request *connect.Request[devhudv1.CreateUploadRequest]) (*connect.Response[devhudv1.CreateUploadResponse], error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, unauthenticatedError(ctx)
	}
	if request.Msg.GetContentType() != devhudv1.UploadContentType_UPLOAD_CONTENT_TYPE_PNG {
		return nil, NewError(connect.CodeInvalidArgument, "content_type must be PNG", CorrelationID(ctx))
	}
	if len(request.Msg.GetExpectedSha256()) != 32 {
		return nil, NewError(connect.CodeInvalidArgument, "expected_sha256 must contain exactly 32 raw bytes", CorrelationID(ctx))
	}
	if request.Msg.GetExpectedSizeBytes() == 0 {
		return nil, NewError(connect.CodeInvalidArgument, "expected_size_bytes must be nonzero", CorrelationID(ctx))
	}
	target, err := uploadTarget(request.Msg.GetTarget())
	if err != nil {
		return nil, NewError(connect.CodeInvalidArgument, err.Error(), CorrelationID(ctx))
	}
	var checksum [32]byte
	copy(checksum[:], request.Msg.GetExpectedSha256())
	reservation, err := s.service.Create(ctx, user.ID, target, request.Msg.GetExpectedSizeBytes(), checksum)
	if err != nil {
		return nil, s.mapError(ctx, devhudv1connect.UploadServiceCreateUploadProcedure, "create upload", err)
	}
	return connect.NewResponse(&devhudv1.CreateUploadResponse{Metadata: metadata(CorrelationID(ctx)), Reservation: &devhudv1.UploadReservation{
		UploadId: uuidMessage(reservation.UploadID), SubmissionId: uuidMessage(reservation.SubmissionID),
		UploadGroupId: uuidMessage(reservation.UploadGroupID), ReservationId: uuidMessage(reservation.ReservationID),
		StagingGeneration: reservation.StagingGeneration, SignedPutUrl: reservation.SignedPUT.URL,
		RequiredHeaders: &devhudv1.SignedUploadHeaders{ContentType: reservation.SignedPUT.ContentType, ChecksumSha256Base64: reservation.SignedPUT.ChecksumSHA256Base64, ContentLength: reservation.SizeBytes},
		ExpiresAt:       timestamppb.New(reservation.SignedURLExpiresAt), PublicUrl: s.service.PublicURL(reservation.PublicID),
		StagingExpiresAt: timestamppb.New(reservation.StagingExpiresAt),
	}}), nil
}

func (s *UploadService) FinalizeUpload(ctx context.Context, request *connect.Request[devhudv1.FinalizeUploadRequest]) (*connect.Response[devhudv1.FinalizeUploadResponse], error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, unauthenticatedError(ctx)
	}
	for name, value := range map[string]*devhudv1.UuidV7{
		"upload_id": request.Msg.GetUploadId(), "submission_id": request.Msg.GetSubmissionId(),
		"upload_group_id": request.Msg.GetUploadGroupId(), "reservation_id": request.Msg.GetReservationId(),
	} {
		if !validUUIDv7(value) {
			return nil, NewError(connect.CodeInvalidArgument, name+" must be a canonical UUID v7", CorrelationID(ctx))
		}
	}
	if request.Msg.GetStagingGeneration() == 0 || request.Msg.GetExpectedSizeBytes() == 0 || len(request.Msg.GetExpectedSha256()) != 32 || !validETag(request.Msg.GetObservedEtag()) {
		return nil, NewError(connect.CodeInvalidArgument, "finalization binding is invalid", CorrelationID(ctx))
	}
	var checksum [32]byte
	copy(checksum[:], request.Msg.GetExpectedSha256())
	binding := domain.UploadBinding{
		UploadID: request.Msg.GetUploadId().GetValue(), SubmissionID: request.Msg.GetSubmissionId().GetValue(),
		UploadGroupID: request.Msg.GetUploadGroupId().GetValue(), ReservationID: request.Msg.GetReservationId().GetValue(),
		StagingGeneration: request.Msg.GetStagingGeneration(), SizeBytes: request.Msg.GetExpectedSizeBytes(),
		SHA256: checksum, ObservedETag: request.Msg.GetObservedEtag(),
	}
	upload, err := s.service.Finalize(ctx, user.ID, binding)
	if err != nil {
		return nil, s.mapError(ctx, devhudv1connect.UploadServiceFinalizeUploadProcedure, "finalize upload", err)
	}
	return connect.NewResponse(&devhudv1.FinalizeUploadResponse{Metadata: metadata(CorrelationID(ctx)), Upload: s.uploadMessage(upload)}), nil
}

func (s *UploadService) ListUploads(ctx context.Context, request *connect.Request[devhudv1.ListUploadsRequest]) (*connect.Response[devhudv1.ListUploadsResponse], error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, unauthenticatedError(ctx)
	}
	states, err := uploadStates(request.Msg.GetStates())
	if err != nil {
		return nil, NewError(connect.CodeInvalidArgument, err.Error(), CorrelationID(ctx))
	}
	submissionID := ""
	if request.Msg.GetSubmissionId() != nil {
		if !validUUIDv7(request.Msg.GetSubmissionId()) {
			return nil, NewError(connect.CodeInvalidArgument, "submission_id must be a canonical UUID v7", CorrelationID(ctx))
		}
		submissionID = request.Msg.GetSubmissionId().GetValue()
	}
	pageSize, pageToken := uint32(0), ""
	if request.Msg.GetPage() != nil {
		pageSize, pageToken = request.Msg.GetPage().GetPageSize(), request.Msg.GetPage().GetPageToken()
	}
	if pageSize > domain.UploadMaximumPageSize {
		return nil, NewError(connect.CodeInvalidArgument, "page size exceeds maximum", CorrelationID(ctx), &devhudv1.PaginationFailure{Reason: devhudv1.PaginationFailureReason_PAGINATION_FAILURE_REASON_PAGE_SIZE_TOO_LARGE})
	}
	if len(pageToken) > domain.UploadMaximumPageTokenBytes {
		return nil, NewError(connect.CodeInvalidArgument, "invalid page token", CorrelationID(ctx), &devhudv1.PaginationFailure{Reason: devhudv1.PaginationFailureReason_PAGINATION_FAILURE_REASON_TOKEN_INVALID})
	}
	result, next, err := s.service.List(ctx, user.ID, states, submissionID, pageToken, pageSize)
	if err != nil {
		return nil, s.mapError(ctx, devhudv1connect.UploadServiceListUploadsProcedure, "list uploads", err)
	}
	messages := make([]*devhudv1.Upload, 0, len(result.Uploads))
	for _, item := range result.Uploads {
		messages = append(messages, s.uploadMessage(item))
	}
	return connect.NewResponse(&devhudv1.ListUploadsResponse{Metadata: metadata(CorrelationID(ctx)), Uploads: messages, NextPageToken: next}), nil
}

func (s *UploadService) DeleteUpload(ctx context.Context, request *connect.Request[devhudv1.DeleteUploadRequest]) (*connect.Response[devhudv1.DeleteUploadResponse], error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, unauthenticatedError(ctx)
	}
	if !validUUIDv7(request.Msg.GetUploadId()) {
		return nil, NewError(connect.CodeInvalidArgument, "upload_id must be a canonical UUID v7", CorrelationID(ctx))
	}
	upload, err := s.service.Delete(ctx, user.ID, request.Msg.GetUploadId().GetValue())
	if err != nil {
		return nil, s.mapError(ctx, devhudv1connect.UploadServiceDeleteUploadProcedure, "delete upload", err)
	}
	return connect.NewResponse(&devhudv1.DeleteUploadResponse{Metadata: metadata(CorrelationID(ctx)), Upload: s.uploadMessage(upload)}), nil
}

func (s *UploadService) mapError(ctx context.Context, procedure, operation string, err error) error {
	var permission *domain.PermissionError
	if errors.As(err, &permission) {
		return permissionError(ctx, permission)
	}
	if errors.Is(err, domain.ErrNotFound) {
		return NewError(connect.CodeNotFound, "upload was not found", CorrelationID(ctx))
	}
	var quota *domain.QuotaError
	if errors.As(err, &quota) {
		detail := &devhudv1.QuotaFailure{Quota: quotaKind(quota.Quota), Limit: quota.Limit, Observed: quota.Observed}
		if !quota.RetryAt.IsZero() {
			detail.RetryAt = timestamppb.New(quota.RetryAt)
		}
		return NewError(connect.CodeResourceExhausted, "upload quota exhausted", CorrelationID(ctx), detail)
	}
	var failure *domain.UploadError
	if errors.As(err, &failure) {
		return NewError(connect.CodeFailedPrecondition, "upload finalization failed", CorrelationID(ctx), &devhudv1.UploadFailure{Reason: uploadFailureReason(failure.Failure)})
	}
	if strings.Contains(err.Error(), "page size") {
		return NewError(connect.CodeInvalidArgument, err.Error(), CorrelationID(ctx), &devhudv1.PaginationFailure{Reason: devhudv1.PaginationFailureReason_PAGINATION_FAILURE_REASON_PAGE_SIZE_TOO_LARGE})
	}
	if strings.Contains(err.Error(), "page token") {
		reason := devhudv1.PaginationFailureReason_PAGINATION_FAILURE_REASON_TOKEN_INVALID
		if strings.Contains(err.Error(), "expired") {
			reason = devhudv1.PaginationFailureReason_PAGINATION_FAILURE_REASON_TOKEN_EXPIRED
		} else if strings.Contains(err.Error(), "scope") {
			reason = devhudv1.PaginationFailureReason_PAGINATION_FAILURE_REASON_TOKEN_SCOPE_MISMATCH
		}
		return NewError(connect.CodeInvalidArgument, err.Error(), CorrelationID(ctx), &devhudv1.PaginationFailure{Reason: reason})
	}
	s.logger.ErrorContext(ctx, "upload operation failed", "correlation_id", CorrelationID(ctx), "procedure", procedure, "operation", operation, "error_type", fmt.Sprintf("%T", err))
	return internalError(ctx)
}

func (s *UploadService) uploadMessage(upload domain.Upload) *devhudv1.Upload {
	message := &devhudv1.Upload{
		UploadId: uuidMessage(upload.UploadID), SubmissionId: uuidMessage(upload.SubmissionID), UploadGroupId: uuidMessage(upload.UploadGroupID),
		State: protocolUploadState(upload), ContentType: devhudv1.UploadContentType_UPLOAD_CONTENT_TYPE_PNG,
		SizeBytes: upload.SizeBytes, Sha256: append([]byte(nil), upload.SHA256[:]...), StagingGeneration: upload.StagingGeneration,
		Width: upload.Width, Height: upload.Height, PublicUrl: s.service.PublicURL(upload.PublicID), CreatedAt: timestamppb.New(upload.CreatedAt),
	}
	if upload.FinalizedAt != nil {
		message.FinalizedAt = timestamppb.New(*upload.FinalizedAt)
	}
	if upload.RemovedAt != nil {
		message.RemovedAt = timestamppb.New(*upload.RemovedAt)
	}
	return message
}

func uploadTarget(target *devhudv1.CreateUploadTarget) (domain.UploadTarget, error) {
	if target == nil {
		return domain.UploadTarget{}, errors.New("target is required")
	}
	switch value := target.GetTarget().(type) {
	case *devhudv1.CreateUploadTarget_NewSubmission:
		if value.NewSubmission == nil {
			return domain.UploadTarget{}, errors.New("new_submission is required")
		}
		return domain.UploadTarget{Kind: domain.UploadTargetNewSubmission}, nil
	case *devhudv1.CreateUploadTarget_NewGroup:
		if value.NewGroup == nil || !validUUIDv7(value.NewGroup.GetSubmissionId()) {
			return domain.UploadTarget{}, errors.New("new_group submission_id must be a canonical UUID v7")
		}
		return domain.UploadTarget{Kind: domain.UploadTargetNewGroup, SubmissionID: value.NewGroup.GetSubmissionId().GetValue()}, nil
	case *devhudv1.CreateUploadTarget_ExistingGroup:
		if value.ExistingGroup == nil || !validUUIDv7(value.ExistingGroup.GetSubmissionId()) || !validUUIDv7(value.ExistingGroup.GetUploadGroupId()) {
			return domain.UploadTarget{}, errors.New("existing_group identifiers must be canonical UUID v7 values")
		}
		return domain.UploadTarget{Kind: domain.UploadTargetExistingGroup, SubmissionID: value.ExistingGroup.GetSubmissionId().GetValue(), UploadGroupID: value.ExistingGroup.GetUploadGroupId().GetValue()}, nil
	default:
		return domain.UploadTarget{}, errors.New("target selection is required")
	}
}

func validUUIDv7(value *devhudv1.UuidV7) bool {
	if value == nil || value.GetValue() == "" || strings.ToLower(value.GetValue()) != value.GetValue() {
		return false
	}
	parsed, err := googleuuid.Parse(value.GetValue())
	return err == nil && parsed.Version() == 7 && parsed.String() == value.GetValue()
}

func validETag(value string) bool {
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, "\r\n")
}

func uploadStates(values []devhudv1.UploadState) ([]domain.UploadState, error) {
	states := make([]domain.UploadState, 0, len(values)*2)
	for _, value := range values {
		switch value {
		case devhudv1.UploadState_UPLOAD_STATE_PENDING:
			states = append(states, domain.UploadStatePending, domain.UploadStatePublishing)
		case devhudv1.UploadState_UPLOAD_STATE_FINALIZED:
			states = append(states, domain.UploadStateFinalized)
		case devhudv1.UploadState_UPLOAD_STATE_QUARANTINED:
			states = append(states, domain.UploadStateQuarantined)
		case devhudv1.UploadState_UPLOAD_STATE_DELETED:
			states = append(states, domain.UploadStateDeleted)
		case devhudv1.UploadState_UPLOAD_STATE_EXPIRED:
			states = append(states, domain.UploadStateExpired)
		case devhudv1.UploadState_UPLOAD_STATE_REJECTED:
			states = append(states, domain.UploadStateRejected)
		default:
			return nil, errors.New("states contains an unsupported value")
		}
	}
	slices.Sort(states)
	return slices.Compact(states), nil
}

func protocolUploadState(upload domain.Upload) devhudv1.UploadState {
	switch upload.State {
	case domain.UploadStatePending, domain.UploadStatePublishing:
		return devhudv1.UploadState_UPLOAD_STATE_PENDING
	case domain.UploadStateFinalized:
		return devhudv1.UploadState_UPLOAD_STATE_FINALIZED
	case domain.UploadStateRemoving:
		if upload.RemovedAt != nil {
			return devhudv1.UploadState_UPLOAD_STATE_QUARANTINED
		}
		if upload.FinalizedAt == nil {
			return devhudv1.UploadState_UPLOAD_STATE_PENDING
		}
		return devhudv1.UploadState_UPLOAD_STATE_FINALIZED
	case domain.UploadStateQuarantined:
		return devhudv1.UploadState_UPLOAD_STATE_QUARANTINED
	case domain.UploadStateDeleted:
		return devhudv1.UploadState_UPLOAD_STATE_DELETED
	case domain.UploadStateExpired:
		return devhudv1.UploadState_UPLOAD_STATE_EXPIRED
	case domain.UploadStateRejected:
		return devhudv1.UploadState_UPLOAD_STATE_REJECTED
	default:
		return devhudv1.UploadState_UPLOAD_STATE_UNSPECIFIED
	}
}

func quotaKind(value domain.Quota) devhudv1.QuotaKind {
	return devhudv1.QuotaKind(value)
}

func uploadFailureReason(value domain.UploadFailure) devhudv1.UploadFailureReason {
	return devhudv1.UploadFailureReason(value)
}

func uuidMessage(value string) *devhudv1.UuidV7 { return &devhudv1.UuidV7{Value: value} }

var _ devhudv1connect.UploadServiceHandler = (*UploadService)(nil)
