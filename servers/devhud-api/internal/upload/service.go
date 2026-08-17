package upload

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"log/slog"
	"strings"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"github.com/delinoio/oss/servers/devhud-api/internal/idgen"
)

var (
	pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	pngIHDR      = []byte("IHDR")
)

const cleanupTimeout = 5 * time.Second

type Service struct {
	repository    domain.UploadRepository
	objects       domain.UploadStorage
	cache         domain.UploadCache
	cursors       *CursorCodec
	clock         domain.Clock
	logger        *slog.Logger
	publicBaseURL string
	removalPNG    []byte
}

func NewService(repository domain.UploadRepository, objects domain.UploadStorage, cache domain.UploadCache, cursors *CursorCodec, clock domain.Clock, logger *slog.Logger, publicBaseURL string, removalPNG []byte) *Service {
	return &Service{repository: repository, objects: objects, cache: cache, cursors: cursors, clock: clock, logger: logger, publicBaseURL: strings.TrimSuffix(publicBaseURL, "/"), removalPNG: append([]byte(nil), removalPNG...)}
}

func (s *Service) Create(ctx context.Context, ownerID string, target domain.UploadTarget, size uint64, checksum [32]byte) (domain.UploadReservation, error) {
	if size == 0 || size > domain.UploadMaximumObjectBytes {
		return domain.UploadReservation{}, &domain.QuotaError{Quota: domain.QuotaObjectBytes, Limit: domain.UploadMaximumObjectBytes, Observed: size}
	}
	return s.repository.CreateUpload(ctx, domain.CreateUpload{OwnerUserID: ownerID, Target: target, SizeBytes: size, SHA256: checksum, Now: s.clock.Now()}, s.objects.PresignPUT)
}

func (s *Service) Finalize(ctx context.Context, ownerID string, binding domain.UploadBinding) (domain.Upload, error) {
	now := s.clock.Now()
	upload, err := s.repository.GetUploadForFinalize(ctx, ownerID, binding, now)
	if err != nil {
		var uploadError *domain.UploadError
		if upload.UploadID != "" && errors.As(err, &uploadError) && uploadError.Failure == domain.UploadFailureReservationExpired {
			s.rejectAndClean(ctx, ownerID, binding, upload, uploadError.Failure, now)
		}
		return domain.Upload{}, err
	}
	object, err := s.objects.InspectStaging(ctx, upload.UploadReservation)
	if err != nil {
		failure := domain.UploadFailureStagingObjectMissing
		if errors.Is(err, domain.ErrObjectPrecondition) {
			failure = domain.UploadFailureStagingObjectChanged
		} else if !errors.Is(err, domain.ErrObjectNotFound) {
			return domain.Upload{}, err
		}
		s.rejectAndClean(ctx, ownerID, binding, upload, failure, now)
		return domain.Upload{}, &domain.UploadError{Failure: failure}
	}
	width, height, failure := validateObject(upload, binding, object)
	if failure != 0 {
		s.rejectAndClean(ctx, ownerID, binding, upload, failure, now)
		return domain.Upload{}, &domain.UploadError{Failure: failure}
	}
	token, err := idgen.Opaque()
	if err != nil {
		return domain.Upload{}, err
	}
	upload, err = s.repository.ClaimUploadPromotion(ctx, ownerID, binding, object, width, height, token, now)
	if err != nil {
		return domain.Upload{}, err
	}
	publicETag, err := s.objects.Promote(ctx, upload, token)
	if err != nil {
		_ = withCleanupContext(ctx, func(cleanup context.Context) error {
			return s.repository.ReleaseUploadPromotion(cleanup, upload.UploadID, token)
		})
		if errors.Is(err, domain.ErrObjectPrecondition) {
			s.rejectAndClean(ctx, ownerID, binding, upload, domain.UploadFailureStagingObjectChanged, now)
			return domain.Upload{}, &domain.UploadError{Failure: domain.UploadFailureStagingObjectChanged}
		}
		return domain.Upload{}, err
	}
	upload, err = s.repository.CompleteUploadPromotion(ctx, upload.UploadID, token, publicETag, now)
	if err != nil {
		return domain.Upload{}, err
	}
	if err := withCleanupContext(ctx, func(cleanup context.Context) error {
		return s.objects.DeleteStaging(cleanup, upload.UploadReservation)
	}); err != nil {
		s.logger.WarnContext(ctx, "staging cleanup deferred to sweeper", "upload_id", upload.UploadID, "error_type", fmt.Sprintf("%T", err))
	} else if err := withCleanupContext(ctx, func(cleanup context.Context) error {
		return s.repository.CompleteExpiredUpload(cleanup, upload.UploadID, now)
	}); err != nil {
		s.logger.WarnContext(ctx, "staging cleanup metadata update failed", "upload_id", upload.UploadID, "error_type", fmt.Sprintf("%T", err))
	}
	return upload, nil
}

func validateObject(upload domain.Upload, binding domain.UploadBinding, object domain.UploadObject) (uint32, uint32, domain.UploadFailure) {
	if object.ETag == "" || object.ETag != binding.ObservedETag {
		return 0, 0, domain.UploadFailureStagingObjectChanged
	}
	if object.SizeBytes != upload.SizeBytes || binding.SizeBytes != upload.SizeBytes {
		return 0, 0, domain.UploadFailureSizeMismatch
	}
	if object.ContentType != "image/png" {
		return 0, 0, domain.UploadFailureInvalidContentType
	}
	if len(object.Checksum) != 32 || !bytes.Equal(object.Checksum, upload.SHA256[:]) || binding.SHA256 != upload.SHA256 {
		return 0, 0, domain.UploadFailureChecksumMismatch
	}
	if !validPNGHeader(object.Header) {
		return 0, 0, domain.UploadFailureInvalidPNGSignature
	}
	width := binary.BigEndian.Uint32(object.Header[16:20])
	height := binary.BigEndian.Uint32(object.Header[20:24])
	if width == 0 || height == 0 || width > domain.UploadMaximumRasterAxis || height > domain.UploadMaximumRasterAxis || uint64(width)*uint64(height) > domain.UploadMaximumRasterPixels {
		return 0, 0, domain.UploadFailureUnsafeDimensions
	}
	return width, height, 0
}

func validPNGHeader(header []byte) bool {
	if len(header) < 33 || !bytes.Equal(header[:8], pngSignature) || binary.BigEndian.Uint32(header[8:12]) != 13 || !bytes.Equal(header[12:16], pngIHDR) {
		return false
	}
	if binary.BigEndian.Uint32(header[29:33]) != crc32.ChecksumIEEE(header[12:29]) || header[26] != 0 || header[27] != 0 || header[28] > 1 {
		return false
	}
	switch header[25] {
	case 0:
		return header[24] == 1 || header[24] == 2 || header[24] == 4 || header[24] == 8 || header[24] == 16
	case 2, 4, 6:
		return header[24] == 8 || header[24] == 16
	case 3:
		return header[24] == 1 || header[24] == 2 || header[24] == 4 || header[24] == 8
	default:
		return false
	}
}

func (s *Service) rejectAndClean(ctx context.Context, ownerID string, binding domain.UploadBinding, upload domain.Upload, failure domain.UploadFailure, now time.Time) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if err := s.repository.RejectUpload(cleanup, ownerID, binding, failure, now); err != nil {
		s.logger.WarnContext(ctx, "invalid upload rejection state update failed", "upload_id", upload.UploadID, "error_type", fmt.Sprintf("%T", err))
		return
	}
	if err := s.objects.DeleteStaging(cleanup, upload.UploadReservation); err != nil {
		s.logger.WarnContext(ctx, "invalid staging cleanup deferred to sweeper", "upload_id", upload.UploadID, "error_type", fmt.Sprintf("%T", err))
		return
	}
	if err := s.repository.CompleteExpiredUpload(cleanup, upload.UploadID, now); err != nil {
		s.logger.WarnContext(ctx, "invalid staging cleanup metadata update failed", "upload_id", upload.UploadID, "error_type", fmt.Sprintf("%T", err))
	}
}

func (s *Service) List(ctx context.Context, ownerID string, states []domain.UploadState, submissionID, pageToken string, pageSize uint32) (domain.UploadList, string, error) {
	if pageSize == 0 {
		pageSize = domain.UploadDefaultPageSize
	}
	if pageSize > domain.UploadMaximumPageSize {
		return domain.UploadList{}, "", errors.New("page size exceeds maximum")
	}
	var cursor *domain.UploadCursor
	if pageToken != "" {
		decoded, err := s.cursors.Decode(pageToken, ownerID, states, submissionID, s.clock.Now())
		if err != nil {
			return domain.UploadList{}, "", err
		}
		cursor = &decoded
	}
	result, err := s.repository.ListUploads(ctx, ownerID, states, submissionID, cursor, pageSize)
	if err != nil {
		return domain.UploadList{}, "", err
	}
	var next string
	if result.Next != nil {
		next, err = s.cursors.Encode(ownerID, states, submissionID, *result.Next, s.clock.Now())
		if err != nil {
			return domain.UploadList{}, "", err
		}
	}
	return result, next, nil
}

func (s *Service) Delete(ctx context.Context, ownerID, uploadID string) (domain.Upload, error) {
	return s.remove(ctx, ownerID, "", uploadID, domain.RemovalReasonOwnerDeleted, 0, nil)
}

func (s *Service) RemoveAsAdministrator(ctx context.Context, actorID, uploadID string, reason domain.RemovalReason, expected domain.UploadState, rationale string, event domain.AuditEvent) (domain.Upload, error) {
	if reason != domain.RemovalReasonAdministratorDeleted && reason != domain.RemovalReasonAdministratorQuarantined {
		return domain.Upload{}, errors.New("invalid administrator removal reason")
	}
	return s.remove(ctx, "", actorID, uploadID, reason, expected, &domain.AdministratorUploadAudit{ActorUserID: actorID, Rationale: rationale, Event: event})
}

func (s *Service) remove(ctx context.Context, ownerID, actorID, uploadID string, reason domain.RemovalReason, expected domain.UploadState, audit *domain.AdministratorUploadAudit) (domain.Upload, error) {
	token, err := idgen.Opaque()
	if err != nil {
		return domain.Upload{}, err
	}
	upload, err := s.repository.ClaimUploadRemoval(ctx, ownerID, actorID, uploadID, reason, expected, token, s.clock.Now())
	if err != nil || upload.State == domain.UploadStateDeleted || upload.State == domain.UploadStateQuarantined {
		return upload, err
	}
	if upload.FinalizedAt == nil {
		if err := s.objects.DeleteStaging(ctx, upload.UploadReservation); err != nil {
			_ = withCleanupContext(ctx, func(cleanup context.Context) error {
				return s.repository.ReleaseUploadRemoval(cleanup, uploadID, token)
			})
			return domain.Upload{}, err
		}
		_ = withCleanupContext(ctx, func(cleanup context.Context) error {
			return s.repository.CompleteExpiredUpload(cleanup, uploadID, s.clock.Now())
		})
		return s.repository.CompleteUploadRemoval(ctx, uploadID, token, s.clock.Now(), audit)
	}
	if upload.ReplacementETag == "" {
		replacementETag, err := s.objects.ReplacePublic(ctx, upload, s.removalPNG)
		if err != nil {
			_ = withCleanupContext(ctx, func(cleanup context.Context) error {
				return s.repository.ReleaseUploadRemoval(cleanup, uploadID, token)
			})
			return domain.Upload{}, err
		}
		upload, err = s.repository.RecordUploadReplacement(ctx, uploadID, token, replacementETag)
		if err != nil {
			return domain.Upload{}, err
		}
	}
	publicURL := s.PublicURL(upload.PublicID)
	if err := s.cache.PurgeAndRevalidate(ctx, publicURL, s.removalPNG); err != nil {
		return domain.Upload{}, err
	}
	return s.repository.CompleteUploadRemoval(ctx, uploadID, token, s.clock.Now(), audit)
}

func (s *Service) PublicURL(publicID string) string { return s.publicBaseURL + "/" + publicID + ".png" }

func (s *Service) SweepExpiredUploads(ctx context.Context, now time.Time, limit int) (domain.StagingSweepResult, error) {
	uploads, err := s.repository.ClaimExpiredUploads(ctx, now, limit)
	if err != nil {
		return domain.StagingSweepResult{}, err
	}
	result := domain.StagingSweepResult{Claimed: len(uploads)}
	for _, upload := range uploads {
		if upload.State == domain.UploadStatePublishing {
			publicETag, err := s.objects.Promote(ctx, upload, upload.OperationToken)
			if err != nil {
				s.logger.WarnContext(ctx, "upload promotion reconciliation failed", "upload_id", upload.UploadID, "error_type", fmt.Sprintf("%T", err))
				continue
			}
			upload, err = s.repository.CompleteUploadPromotion(ctx, upload.UploadID, upload.OperationToken, publicETag, now)
			if err != nil {
				return result, err
			}
		}
		if err := s.objects.DeleteStaging(ctx, upload.UploadReservation); err != nil {
			s.logger.WarnContext(ctx, "staging expiry cleanup failed", "upload_id", upload.UploadID, "error_type", fmt.Sprintf("%T", err))
			continue
		}
		if err := s.repository.CompleteExpiredUpload(ctx, upload.UploadID, now); err != nil {
			return result, err
		}
		result.Deleted++
	}
	return result, nil
}

func (s *Service) PurgeAccount(ctx context.Context, user domain.User) error {
	for {
		uploads, err := s.repository.ListAccountUploadsForPurge(ctx, user.ID, 100)
		if err != nil {
			return err
		}
		if len(uploads) == 0 {
			break
		}
		for _, candidate := range uploads {
			if candidate.State == domain.UploadStateExpired || candidate.State == domain.UploadStateRejected || candidate.State == domain.UploadStateDeleted || candidate.State == domain.UploadStateQuarantined {
				if err := s.objects.DeleteStaging(ctx, candidate.UploadReservation); err != nil {
					return fmt.Errorf("purge expired staging %s: %w", candidate.UploadID, err)
				}
				if err := s.repository.CompleteExpiredUpload(ctx, candidate.UploadID, s.clock.Now()); err != nil {
					return err
				}
				continue
			}
			if _, err := s.remove(ctx, "", "", candidate.UploadID, domain.RemovalReasonAccountPurged, 0, nil); err != nil {
				return fmt.Errorf("purge upload %s: %w", candidate.UploadID, err)
			}
		}
	}
	return s.repository.RemoveAccountUploadMetadata(ctx, user.ID)
}

func withCleanupContext(ctx context.Context, operation func(context.Context) error) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	return operation(cleanup)
}

var _ domain.UploadStagingSweeper = (*Service)(nil)
var _ domain.AccountPurgeAdapter = (*Service)(nil)
