package service

import (
	"context"
	"encoding/hex"
	"math"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/rqerr"
)

// CreateSubmission validates the owner, payer, preset revision, image bounds,
// and the current caller's GitHub repository access. Billing catalog records
// intentionally remain inactive, so this foundation fails unavailable before
// creating durable upload state or performing a provider/billing mutation.
func (service *Submission) CreateSubmission(
	ctx context.Context,
	request *connect.Request[realqav1.CreateSubmissionRequest],
) (*connect.Response[realqav1.CreateSubmissionResponse], error) {
	if request == nil || request.Msg == nil ||
		request.Msg.PresetRevision == nil || request.Msg.PresetRevision.Value <= 0 {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	actor, err := resolveCaller(ctx, service.dependencies)
	if err != nil {
		return nil, err
	}
	scope, err := parseOwner(request.Msg.Owner)
	if err != nil {
		return nil, err
	}
	if _, err = authorizeOwner(ctx, service.dependencies, actor, scope, false, false); err != nil {
		return nil, err
	}
	if _, _, err = authorizeBilling(ctx, service.dependencies, actor, scope,
		request.Msg.Billing); err != nil {
		return nil, err
	}
	if _, err = authorizeRepository(ctx, service.dependencies, actor,
		request.Msg.Destination); err != nil {
		return nil, err
	}
	presetID, err := parseUUIDMessage(request.Msg.PresetId)
	if err != nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	preset, err := NewPreset(service.dependencies).loadPreset(ctx, presetID)
	if err != nil {
		return nil, err
	}
	presetOwner, err := parseOwner(preset.Owner)
	if err != nil || presetOwner != scope {
		return nil, permissionDenied()
	}
	if preset.Revision.Value != request.Msg.PresetRevision.Value {
		return nil, stale(preset.Revision.Value)
	}
	if _, err = parseIdempotency(request.Msg.Idempotency); err != nil {
		return nil, err
	}
	if err = validateImages(request.Msg.Images); err != nil {
		return nil, err
	}
	return nil, rqerr.New(connect.CodeUnavailable,
		realqav1.ErrorReason_ERROR_REASON_TRANSFER_RESERVATION_FAILED,
		realqav1.FailureClass_FAILURE_CLASS_RETRYABLE, 0)
}

func validateImages(images []*realqav1.ImageDeclaration) error {
	var total int64
	for _, image := range images {
		if image == nil || image.EncodedBytes <= 0 ||
			image.EncodedBytes > int64(
				realqav1.RealQALimit_REAL_QA_LIMIT_MAX_IMAGE_ENCODED_BYTES) ||
			image.PixelWidth <= 0 || image.PixelHeight <= 0 ||
			(image.MediaType != realqav1.ImageMediaType_IMAGE_MEDIA_TYPE_PNG &&
				image.MediaType != realqav1.ImageMediaType_IMAGE_MEDIA_TYPE_WEBP) {
			return invalid(realqav1.ErrorReason_ERROR_REASON_IMAGE_TOO_LARGE)
		}
		if _, err := parseUUIDMessage(image.ClientImageId); err != nil {
			return invalid(realqav1.ErrorReason_ERROR_REASON_MALFORMED_IMAGE)
		}
		pixels := int64(image.PixelWidth) * int64(image.PixelHeight)
		if pixels <= 0 || pixels >
			int64(realqav1.RealQALimit_REAL_QA_LIMIT_MAX_DECODED_IMAGE_PIXELS) {
			return invalid(realqav1.ErrorReason_ERROR_REASON_DECODED_IMAGE_TOO_LARGE)
		}
		checksum, err := hex.DecodeString(image.Sha256)
		if err != nil || len(checksum) != 32 {
			return invalid(realqav1.ErrorReason_ERROR_REASON_MALFORMED_IMAGE)
		}
		if total > math.MaxInt64-image.EncodedBytes {
			return invalid(realqav1.ErrorReason_ERROR_REASON_SESSION_TOO_LARGE)
		}
		total += image.EncodedBytes
		if total > int64(
			realqav1.RealQALimit_REAL_QA_LIMIT_MAX_SESSION_ENCODED_BYTES) {
			return invalid(realqav1.ErrorReason_ERROR_REASON_SESSION_TOO_LARGE)
		}
	}
	return nil
}
