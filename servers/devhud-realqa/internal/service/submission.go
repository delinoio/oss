package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"math"
	"time"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

const (
	uploadAcceptWindow = 23 * time.Hour
	stagingLifetime    = 24 * time.Hour
	uploadSessionLimit = 3
)

// CreateSubmission validates the complete declaration set before creating any
// private upload state. Transfer/storage billing activation remains a separate
// integration; this method performs no provider or catalog mutation.
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
	idempotencyID, err := parseIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	requestDigest, err := digestMessage(request.Msg)
	if err != nil {
		return nil, err
	}
	if replay, ok, replayErr := service.submissionReplay(
		ctx, actor, idempotencyID, requestDigest); ok {
		return replay, replayErr
	}
	scope, err := parseOwner(request.Msg.Owner)
	if err != nil {
		return nil, err
	}
	if _, err = authorizeOwner(
		ctx, service.dependencies, actor, scope, false, false); err != nil {
		return nil, err
	}
	payerOrganization, payerTeam, err := authorizeBilling(
		ctx, service.dependencies, actor, scope, request.Msg.Billing)
	if err != nil {
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
	if err = validateImages(request.Msg.Images); err != nil {
		return nil, err
	}
	if service.dependencies.Objects == nil ||
		service.dependencies.UploadSigner == nil {
		return nil, storageUnavailable()
	}
	presetRecord, err := service.dependencies.Store.Queries().GetPresetRecord(
		ctx, toPGUUID(presetID))
	if err != nil {
		return nil, err
	}
	persistedDestination, err := destinationFromPresetRecord(presetRecord)
	if err != nil {
		return nil, err
	}
	if !sameDestinationIdentity(request.Msg.Destination, persistedDestination) {
		return nil, invalid(
			realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	if _, _, err = authorizeRepository(
		ctx, service.dependencies, actor, scope, persistedDestination); err != nil {
		return nil, err
	}
	submissionID, err := newID(service.dependencies)
	if err != nil {
		return nil, err
	}
	recordID, err := newID(service.dependencies)
	if err != nil {
		return nil, err
	}
	assetIDs := make([]uuid.UUID, len(request.Msg.Images))
	for index := range assetIDs {
		assetIDs[index], err = newID(service.dependencies)
		if err != nil {
			return nil, err
		}
	}
	now := service.dependencies.Clock.Now().UTC()
	deadline := now.Add(uploadAcceptWindow)
	expires := now.Add(stagingLifetime)
	var created *realqav1.Submission
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if lockErr := lockActiveOwnerScope(ctx, queries, scope); lockErr != nil {
				return lockErr
			}
			if existing, lookupErr := queries.GetIdempotencyRecord(
				ctx, idempotencyLookupFor(
					actor, idempotencyID, "create_submission"),
			); lookupErr == nil {
				if !bytes.Equal(existing.RequestDigest, requestDigest) {
					return idempotencyConflict()
				}
				return errIdempotencyReplay
			} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
				return lookupErr
			}
			if lockErr := queries.LockUploadSessionAccount(
				ctx, toPGUUID(actor.accountID)); lockErr != nil {
				return lockErr
			}
			open, countErr := queries.CountOpenSubmissionsForAccount(
				ctx, toPGUUID(actor.accountID))
			if countErr != nil {
				return countErr
			}
			if open >= uploadSessionLimit {
				return invalid(
					realqav1.ErrorReason_ERROR_REASON_UPLOAD_CONCURRENCY_LIMITED)
			}
			var total int64
			for _, declaration := range request.Msg.Images {
				total += declaration.EncodedBytes
			}
			_, createErr := queries.CreateSubmissionRecord(
				ctx, dbgen.CreateSubmissionRecordParams{
					ID: toPGUUID(submissionID), OwnerKind: scope.kind,
					OwnerID:              toPGUUID(scope.id),
					CreatedByAccountID:   toPGUUID(actor.accountID),
					PresetID:             toPGUUID(presetID),
					DestinationID:        presetRecord.DestinationID,
					IdempotencyDigest:    requestDigest,
					PayerOrganizationID:  toPGUUID(payerOrganization),
					PayerTeamID:          toPGUUID(payerTeam),
					PresetRevision:       request.Msg.PresetRevision.Value,
					DeclaredEncodedBytes: total,
					UploadDeadline:       pgTimestamp(deadline),
					UploadExpiresAt:      pgTimestamp(expires),
				})
			if createErr != nil {
				return createErr
			}
			for index, declaration := range request.Msg.Images {
				clientID, _ := parseUUIDMessage(declaration.ClientImageId)
				checksum, _ := hex.DecodeString(declaration.Sha256)
				if _, createErr = queries.CreateAssetRecord(
					ctx, dbgen.CreateAssetRecordParams{
						ID:                   toPGUUID(assetIDs[index]),
						SubmissionID:         toPGUUID(submissionID),
						ClientImageID:        toPGUUID(clientID),
						MediaType:            mediaTypeName(declaration.MediaType),
						DeclaredEncodedBytes: declaration.EncodedBytes,
						PixelWidth:           declaration.PixelWidth,
						PixelHeight:          declaration.PixelHeight,
						SourceSha256:         checksum,
					}); createErr != nil {
					return createErr
				}
			}
			created, createErr = loadSubmissionWithQueries(
				ctx, queries, submissionID)
			if createErr != nil {
				return createErr
			}
			payload, marshalErr := proto.MarshalOptions{
				Deterministic: true,
			}.Marshal(created)
			if marshalErr != nil {
				return marshalErr
			}
			_, createErr = queries.CreateIdempotencyRecord(
				ctx, dbgen.CreateIdempotencyRecordParams{
					ID: toPGUUID(recordID), CallerKind: "user",
					CallerDigest: actor.digest, Operation: "create_submission",
					IdempotencyKey: toPGUUID(idempotencyID),
					RequestDigest:  requestDigest, ResourceID: toPGUUID(submissionID),
					ResponsePayload: payload,
				})
			return createErr
		})
	if err != nil {
		if replay, ok, replayErr := service.submissionReplay(
			ctx, actor, idempotencyID, requestDigest); ok {
			return replay, replayErr
		}
		return nil, err
	}
	audit(ctx, service.dependencies, actor, "submission_created",
		scope, submissionID, "allow", "success")
	return connect.NewResponse(&realqav1.CreateSubmissionResponse{
		Submission:                   created,
		TransferReservationExpiresAt: created.TransferReservationExpiresAt,
		UploadDeadline:               created.UploadDeadline,
		Idempotency: &realqav1.IdempotencyResult{
			Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_SUBMISSION,
			OriginallyCompletedAt: created.CreatedAt,
		},
	}), nil
}

func destinationFromPresetRecord(
	record dbgen.GetPresetRecordRow,
) (*realqav1.TrackerDestination, error) {
	installationID, err := fromPGUUID(record.InstallationID)
	if err != nil {
		return nil, err
	}
	return &realqav1.TrackerDestination{
		Tracker:        realqav1.TrackerKind_TRACKER_KIND_GITHUB_COM,
		InstallationId: &realqav1.UuidV7{Value: installationID.String()},
		Repository: &realqav1.GitHubRepositoryRef{
			RepositoryId: record.RepositoryID,
			Owner:        record.RepositoryOwner,
			Name:         record.RepositoryName,
		},
	}, nil
}

func sameDestinationIdentity(
	requested *realqav1.TrackerDestination,
	persisted *realqav1.TrackerDestination,
) bool {
	return requested != nil && persisted != nil &&
		requested.Tracker == persisted.Tracker &&
		requested.InstallationId != nil && persisted.InstallationId != nil &&
		requested.InstallationId.Value == persisted.InstallationId.Value &&
		requested.Repository != nil && persisted.Repository != nil &&
		requested.Repository.RepositoryId == persisted.Repository.RepositoryId
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
		if err != nil || len(checksum) != 32 ||
			hex.EncodeToString(checksum) != image.Sha256 {
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

func (service *Submission) submissionReplay(
	ctx context.Context,
	actor caller,
	idempotencyID uuid.UUID,
	digest []byte,
) (*connect.Response[realqav1.CreateSubmissionResponse], bool, error) {
	record, err := service.dependencies.Store.Queries().GetIdempotencyRecord(
		ctx, idempotencyLookupFor(actor, idempotencyID, "create_submission"))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if !bytes.Equal(record.RequestDigest, digest) {
		return nil, true, idempotencyConflict()
	}
	submission := new(realqav1.Submission)
	if len(record.ResponsePayload) > 0 {
		if err = proto.Unmarshal(record.ResponsePayload, submission); err != nil {
			return nil, true, err
		}
	} else {
		id, parseErr := fromPGUUID(record.ResourceID)
		if parseErr != nil {
			return nil, true, parseErr
		}
		submission, err = service.loadSubmission(ctx, id)
		if err != nil {
			return nil, true, err
		}
	}
	return connect.NewResponse(&realqav1.CreateSubmissionResponse{
		Submission:                   submission,
		TransferReservationExpiresAt: submission.TransferReservationExpiresAt,
		UploadDeadline:               submission.UploadDeadline,
		Idempotency: &realqav1.IdempotencyResult{
			Replayed:              true,
			Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_SUBMISSION,
			OriginallyCompletedAt: timestamp(record.CompletedAt),
		},
	}), true, nil
}
