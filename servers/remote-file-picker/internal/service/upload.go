package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	remotefilepickerv1 "github.com/delinoio/oss/servers/remote-file-picker/gen/proto/remotefilepicker/v1"
	"github.com/delinoio/oss/servers/remote-file-picker/internal/contracts"
)

// checkAuth validates the bearer token from the Authorization header.
func (s *Service) checkAuth(header string) error {
	if header == "" {
		return connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("missing authorization header"))
	}
	token := strings.TrimPrefix(header, "Bearer ")
	if token == header {
		return connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("invalid authorization scheme"))
	}
	if token != s.authToken {
		return connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("invalid auth token"))
	}
	return nil
}

// CreateSignedUploadUrl generates a signed URL for uploading a file and tracks the upload.
func (s *Service) CreateSignedUploadUrl(
	ctx context.Context,
	req *connect.Request[remotefilepickerv1.CreateSignedUploadUrlRequest],
) (*connect.Response[remotefilepickerv1.CreateSignedUploadUrlResponse], error) {
	if err := s.checkAuth(req.Header().Get("Authorization")); err != nil {
		s.logger.WarnContext(ctx, "auth failure",
			slog.String("event", string(contracts.LogEventAuthFailure)),
			slog.String("result", string(contracts.OperationResultDenied)),
		)
		return nil, err
	}

	msg := req.Msg
	if msg.FileName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("file_name is required"))
	}
	if msg.ContentType == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("content_type is required"))
	}
	if msg.ContentLength <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("content_length must be positive"))
	}

	bucket := msg.Bucket
	if bucket == "" {
		bucket = s.bucket
	}

	provider := msg.Provider
	if provider == remotefilepickerv1.StorageProvider_STORAGE_PROVIDER_UNSPECIFIED {
		provider = s.provider
	}

	uploadID := uuid.New().String()
	keyPrefix := msg.KeyPrefix
	if keyPrefix == "" {
		keyPrefix = "uploads"
	}
	objectKey := fmt.Sprintf("%s/%s/%s", keyPrefix, uploadID, msg.FileName)
	signedURL := mockSignedURL(bucket, objectKey)
	now := time.Now().UTC()
	expiresAt := now.Add(15 * time.Minute)

	record := &uploadRecord{
		id:            uploadID,
		fileName:      msg.FileName,
		contentType:   msg.ContentType,
		contentLength: msg.ContentLength,
		provider:      provider,
		bucket:        bucket,
		objectKey:     objectKey,
		signedURL:     signedURL,
		status:        remotefilepickerv1.UploadStatus_UPLOAD_STATUS_PENDING,
		createdAt:     now,
	}
	s.uploads.Store(uploadID, record)

	s.logger.InfoContext(ctx, "upload created",
		slog.String("event", string(contracts.LogEventUploadCreate)),
		slog.String("upload_id", uploadID),
		slog.String("file_name", msg.FileName),
		slog.String("object_key", objectKey),
		slog.String("bucket", bucket),
		slog.String("result", string(contracts.OperationResultSuccess)),
	)

	return connect.NewResponse(&remotefilepickerv1.CreateSignedUploadUrlResponse{
		UploadId:  uploadID,
		SignedUrl: signedURL,
		ObjectKey: objectKey,
		ExpiresAt: timestamppb.New(expiresAt),
	}), nil
}

// ConfirmUpload marks an upload as completed and assigns its public URL.
func (s *Service) ConfirmUpload(
	ctx context.Context,
	req *connect.Request[remotefilepickerv1.ConfirmUploadRequest],
) (*connect.Response[remotefilepickerv1.ConfirmUploadResponse], error) {
	if err := s.checkAuth(req.Header().Get("Authorization")); err != nil {
		s.logger.WarnContext(ctx, "auth failure",
			slog.String("event", string(contracts.LogEventAuthFailure)),
			slog.String("result", string(contracts.OperationResultDenied)),
		)
		return nil, err
	}

	uploadID := req.Msg.UploadId
	if uploadID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("upload_id is required"))
	}

	val, ok := s.uploads.Load(uploadID)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("upload %s not found", uploadID))
	}

	record := val.(*uploadRecord)
	record.status = remotefilepickerv1.UploadStatus_UPLOAD_STATUS_COMPLETED
	record.completedAt = time.Now().UTC()
	record.publicURL = publicURL(record.bucket, record.objectKey)

	s.logger.InfoContext(ctx, "upload confirmed",
		slog.String("event", string(contracts.LogEventUploadConfirm)),
		slog.String("upload_id", uploadID),
		slog.String("public_url", record.publicURL),
		slog.String("result", string(contracts.OperationResultSuccess)),
	)

	return connect.NewResponse(&remotefilepickerv1.ConfirmUploadResponse{
		UploadId:      uploadID,
		Status:        record.status,
		PublicUrl:     record.publicURL,
		ContentLength: record.contentLength,
	}), nil
}

// GetUploadStatus returns the current status of an upload by ID.
func (s *Service) GetUploadStatus(
	ctx context.Context,
	req *connect.Request[remotefilepickerv1.GetUploadStatusRequest],
) (*connect.Response[remotefilepickerv1.GetUploadStatusResponse], error) {
	if err := s.checkAuth(req.Header().Get("Authorization")); err != nil {
		s.logger.WarnContext(ctx, "auth failure",
			slog.String("event", string(contracts.LogEventAuthFailure)),
			slog.String("result", string(contracts.OperationResultDenied)),
		)
		return nil, err
	}

	uploadID := req.Msg.UploadId
	if uploadID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("upload_id is required"))
	}

	val, ok := s.uploads.Load(uploadID)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("upload %s not found", uploadID))
	}

	record := val.(*uploadRecord)

	s.logger.InfoContext(ctx, "upload status queried",
		slog.String("event", string(contracts.LogEventUploadStatusQuery)),
		slog.String("upload_id", uploadID),
		slog.String("status", record.status.String()),
	)

	resp := &remotefilepickerv1.GetUploadStatusResponse{
		UploadId:      uploadID,
		Status:        record.status,
		FileName:      record.fileName,
		ContentType:   record.contentType,
		ContentLength: record.contentLength,
		ObjectKey:     record.objectKey,
		PublicUrl:     record.publicURL,
		CreatedAt:     timestamppb.New(record.createdAt),
	}
	if !record.completedAt.IsZero() {
		resp.CompletedAt = timestamppb.New(record.completedAt)
	}

	return connect.NewResponse(resp), nil
}
