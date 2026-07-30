package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"time"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/imageassets"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/rqerr"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (service *Submission) CreateImageUpload(
	ctx context.Context,
	request *connect.Request[realqav1.CreateImageUploadRequest],
) (*connect.Response[realqav1.CreateImageUploadResponse], error) {
	if request == nil || request.Msg == nil ||
		request.Msg.ExpectedAssetRevision == nil ||
		request.Msg.ExpectedAssetRevision.Value <= 0 {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_STALE_REVISION)
	}
	idempotencyID, err := parseIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	requestDigest, err := digestMessage(request.Msg)
	if err != nil {
		return nil, err
	}
	actor, submissionID, submission, scope, err :=
		service.authorizeSubmissionRequest(ctx, request.Msg.SubmissionId)
	if err != nil {
		return nil, err
	}
	if service.dependencies.UploadSigner == nil {
		return nil, storageUnavailable()
	}
	assetID, err := parseUUIDMessage(request.Msg.AssetId)
	if err != nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_MALFORMED_IMAGE)
	}
	if replay, ok, replayErr := service.imageUploadReplay(
		ctx, actor, idempotencyID, requestDigest, submissionID, assetID); ok {
		return replay, replayErr
	}
	if !isOpenSubmissionState(submission.State) {
		return nil, retentionStateConflict()
	}
	now := service.dependencies.Clock.Now().UTC()
	if !now.Before(submission.UploadDeadline.Time) {
		return nil, rqerr.New(connect.CodeDeadlineExceeded,
			realqav1.ErrorReason_ERROR_REASON_UPLOAD_DEADLINE_EXCEEDED,
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0)
	}
	asset, err := service.dependencies.Store.Queries().GetAssetRecord(
		ctx, dbgen.GetAssetRecordParams{
			ID: toPGUUID(assetID), SubmissionID: toPGUUID(submissionID),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, permissionDenied()
	}
	if err != nil {
		return nil, err
	}
	if asset.Revision != request.Msg.ExpectedAssetRevision.Value {
		return nil, stale(asset.Revision)
	}
	declaration := declarationFromRecord(asset)
	signed, err := service.dependencies.UploadSigner.SignIdempotent(
		now, submission.UploadDeadline.Time, submissionID.String(),
		assetID.String(), declaration, idempotencyID.String())
	if err != nil {
		return nil, storageUnavailable()
	}
	recordID, err := newID(service.dependencies)
	if err != nil {
		return nil, err
	}
	var response *realqav1.CreateImageUploadResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if existing, lookupErr := queries.GetIdempotencyRecord(
				ctx, idempotencyLookupFor(
					actor, idempotencyID, "create_image_upload"),
			); lookupErr == nil {
				if !bytes.Equal(existing.RequestDigest, requestDigest) {
					return idempotencyConflict()
				}
				return errIdempotencyReplay
			} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
				return lookupErr
			}
			lockedSubmission, lockErr := queries.LockSubmissionRecord(
				ctx, toPGUUID(submissionID))
			if lockErr != nil {
				return lockErr
			}
			if !isOpenSubmissionState(lockedSubmission.State) {
				return retentionStateConflict()
			}
			locked, lockErr := queries.LockAssetRecord(
				ctx, dbgen.LockAssetRecordParams{
					ID: toPGUUID(assetID), SubmissionID: toPGUUID(submissionID),
				})
			if lockErr != nil {
				return lockErr
			}
			if locked.Revision != request.Msg.ExpectedAssetRevision.Value {
				return stale(locked.Revision)
			}
			updated, updateErr := queries.AuthorizeAssetUpload(
				ctx, dbgen.AuthorizeAssetUploadParams{
					UploadTokenDigest: signed.TokenDigest[:],
					UploadExpiresAt:   pgTimestamp(signed.ExpiresAt),
					ID:                toPGUUID(assetID),
					SubmissionID:      toPGUUID(submissionID),
					ExpectedRevision:  request.Msg.ExpectedAssetRevision.Value,
				})
			if updateErr != nil {
				return updateErr
			}
			response = &realqav1.CreateImageUploadResponse{
				Asset: updatedAssetProto(updated), SignedPutUrl: signed.URL,
				RequiredContentType: string(declaration.MediaType),
				RequiredSha256:      declaration.SHA256,
				ExpiresAt:           timestamppb.New(signed.ExpiresAt),
				UploadDeadline:      timestamp(submission.UploadDeadline),
				Idempotency: &realqav1.IdempotencyResult{
					Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_IMAGE_UPLOAD,
					OriginallyCompletedAt: timestamppb.New(now),
				},
			}
			stored := proto.Clone(response).(*realqav1.CreateImageUploadResponse)
			stored.SignedPutUrl = ""
			payload, marshalErr := proto.MarshalOptions{
				Deterministic: true,
			}.Marshal(stored)
			if marshalErr != nil {
				return marshalErr
			}
			_, createErr := queries.CreateIdempotencyRecord(
				ctx, dbgen.CreateIdempotencyRecordParams{
					ID: toPGUUID(recordID), CallerKind: "user",
					CallerDigest: actor.digest, Operation: "create_image_upload",
					IdempotencyKey: toPGUUID(idempotencyID),
					RequestDigest:  requestDigest, ResourceID: toPGUUID(assetID),
					ResponsePayload: payload,
				})
			return createErr
		})
	if err != nil {
		if replay, ok, replayErr := service.imageUploadReplay(
			ctx, actor, idempotencyID, requestDigest, submissionID, assetID); ok {
			return replay, replayErr
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		current, getErr := service.dependencies.Store.Queries().GetAssetRecord(
			ctx, dbgen.GetAssetRecordParams{
				ID: toPGUUID(assetID), SubmissionID: toPGUUID(submissionID),
			})
		if getErr == nil {
			return nil, stale(current.Revision)
		}
	}
	if err != nil {
		return nil, err
	}
	audit(ctx, service.dependencies, actor, "image_upload_authorized",
		scope, assetID, "allow", "success")
	return connect.NewResponse(response), nil
}

func (service *Submission) imageUploadReplay(
	ctx context.Context,
	actor caller,
	idempotencyID uuid.UUID,
	digest []byte,
	submissionID uuid.UUID,
	assetID uuid.UUID,
) (*connect.Response[realqav1.CreateImageUploadResponse], bool, error) {
	record, err := service.dependencies.Store.Queries().GetIdempotencyRecord(
		ctx, idempotencyLookupFor(
			actor, idempotencyID, "create_image_upload"))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if !bytes.Equal(record.RequestDigest, digest) {
		return nil, true, idempotencyConflict()
	}
	response := new(realqav1.CreateImageUploadResponse)
	if err = proto.Unmarshal(record.ResponsePayload, response); err != nil {
		return nil, true, err
	}
	if response.Asset == nil || response.Asset.AssetId == nil ||
		response.Asset.AssetId.Value != assetID.String() ||
		response.ExpiresAt == nil || response.UploadDeadline == nil {
		return nil, true, errors.New("realqa images: invalid upload replay state")
	}
	declaration := imageassets.Declaration{
		MediaType:    imageassets.MediaType(response.RequiredContentType),
		EncodedBytes: response.Asset.EncodedBytes,
		Width:        int(response.Asset.PixelWidth),
		Height:       int(response.Asset.PixelHeight),
		SHA256:       response.RequiredSha256,
	}
	signed, err := service.dependencies.UploadSigner.ReplayIdempotent(
		response.ExpiresAt.AsTime(), response.UploadDeadline.AsTime(),
		submissionID.String(), assetID.String(), declaration,
		idempotencyID.String())
	if err != nil {
		return nil, true, err
	}
	response.SignedPutUrl = signed.URL
	response.Idempotency = &realqav1.IdempotencyResult{
		Replayed:              true,
		Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_IMAGE_UPLOAD,
		OriginallyCompletedAt: timestamp(record.CompletedAt),
	}
	return connect.NewResponse(response), true, nil
}

func (service *Submission) LookupUploadGrant(
	ctx context.Context,
	digest [32]byte,
) (imageassets.Grant, error) {
	if service.dependencies.Store == nil {
		return imageassets.Grant{}, errors.New("realqa images: store unavailable")
	}
	row, err := service.dependencies.Store.Queries().GetAssetUploadGrant(
		ctx, digest[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return imageassets.Grant{}, imageassets.ErrUploadGrantNotFound
	}
	if err != nil {
		return imageassets.Grant{}, err
	}
	assetID, err := fromPGUUID(row.ID)
	if err != nil {
		return imageassets.Grant{}, err
	}
	submissionID, err := fromPGUUID(row.SubmissionID)
	if err != nil {
		return imageassets.Grant{}, err
	}
	var tokenDigest [32]byte
	if len(row.UploadTokenDigest) != len(tokenDigest) {
		return imageassets.Grant{}, imageassets.ErrInvalidScope
	}
	copy(tokenDigest[:], row.UploadTokenDigest)
	return imageassets.Grant{
		TokenDigest: tokenDigest, SubmissionID: submissionID.String(),
		AssetID: assetID.String(),
		Declaration: imageassets.Declaration{
			MediaType:    imageassets.MediaType(row.MediaType),
			EncodedBytes: row.DeclaredEncodedBytes,
			Width:        int(row.PixelWidth), Height: int(row.PixelHeight),
			SHA256: hex.EncodeToString(row.SourceSha256),
		},
		ExpiresAt: row.UploadExpiresAt.Time,
		Deadline:  row.UploadDeadline.Time,
	}, nil
}

// StoreUploaded holds the asset row lock while writing its deterministic
// staging object and accepting the upload. Deletion therefore cannot commit
// and drain its staging deletion job between authorization and the object PUT.
func (service *Submission) StoreUploaded(
	ctx context.Context,
	grant imageassets.Grant,
	contentType string,
	body []byte,
) error {
	if service.dependencies.Store == nil || service.dependencies.Objects == nil {
		return errors.New("realqa images: upload storage unavailable")
	}
	assetID, err := parseUUIDv7(grant.AssetID)
	if err != nil {
		return imageassets.ErrInvalidScope
	}
	submissionID, err := parseUUIDv7(grant.SubmissionID)
	if err != nil {
		return imageassets.ErrInvalidScope
	}
	return service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
			asset, lockErr := queries.LockAssetRecord(
				ctx, dbgen.LockAssetRecordParams{
					ID: toPGUUID(assetID), SubmissionID: toPGUUID(submissionID),
				})
			if lockErr != nil {
				return lockErr
			}
			if !hmac.Equal(asset.UploadTokenDigest, grant.TokenDigest[:]) {
				return imageassets.ErrInvalidScope
			}
			if asset.UploadState == "uploaded" {
				return nil
			}
			if asset.UploadState != "put_authorized" {
				return imageassets.ErrInvalidScope
			}
			if putErr := service.dependencies.Objects.Put(
				ctx, imageassets.StagingObjectKey(assetID.String()),
				contentType, body); putErr != nil {
				return putErr
			}
			_, markErr := queries.MarkAssetUploaded(
				ctx, dbgen.MarkAssetUploadedParams{
					ID: toPGUUID(assetID), SubmissionID: toPGUUID(submissionID),
					UploadTokenDigest: grant.TokenDigest[:],
				})
			return markErr
		})
}

func (service *Submission) FinalizeImageUpload(
	ctx context.Context,
	request *connect.Request[realqav1.FinalizeImageUploadRequest],
) (*connect.Response[realqav1.FinalizeImageUploadResponse], error) {
	if request == nil || request.Msg == nil ||
		request.Msg.ExpectedAssetRevision == nil ||
		request.Msg.ExpectedAssetRevision.Value <= 0 {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_STALE_REVISION)
	}
	idempotencyID, err := parseIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	requestDigest, err := digestMessage(request.Msg)
	if err != nil {
		return nil, err
	}
	actor, submissionID, submission, scope, err :=
		service.authorizeSubmissionRequest(ctx, request.Msg.SubmissionId)
	if err != nil {
		return nil, err
	}
	assetID, err := parseUUIDMessage(request.Msg.AssetId)
	if err != nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_MALFORMED_IMAGE)
	}
	if replay, ok, replayErr := service.finalizeImageUploadReplay(
		ctx, actor, idempotencyID, requestDigest, submissionID, assetID); ok {
		return replay, replayErr
	}
	if !isOpenSubmissionState(submission.State) {
		return nil, retentionStateConflict()
	}
	if service.dependencies.Objects == nil {
		return nil, storageUnavailable()
	}
	if service.dependencies.Clock.Now().After(submission.UploadExpiresAt.Time) {
		return nil, rqerr.New(connect.CodeDeadlineExceeded,
			realqav1.ErrorReason_ERROR_REASON_UPLOAD_EXPIRED,
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0)
	}
	asset, err := service.dependencies.Store.Queries().GetAssetRecord(
		ctx, dbgen.GetAssetRecordParams{
			ID: toPGUUID(assetID), SubmissionID: toPGUUID(submissionID),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, permissionDenied()
	}
	if err != nil {
		return nil, err
	}
	if asset.Revision != request.Msg.ExpectedAssetRevision.Value {
		return nil, stale(asset.Revision)
	}
	recordID, err := newID(service.dependencies)
	if err != nil {
		return nil, err
	}
	var response *realqav1.FinalizeImageUploadResponse
	var rejectedVerification error
	attemptedVerifiedWrite := false
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if existing, lookupErr := queries.GetIdempotencyRecord(
				ctx, idempotencyLookupFor(
					actor, idempotencyID, "finalize_image_upload"),
			); lookupErr == nil {
				if !bytes.Equal(existing.RequestDigest, requestDigest) {
					return idempotencyConflict()
				}
				return errIdempotencyReplay
			} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
				return lookupErr
			}
			locked, lockErr := queries.LockSubmissionRecord(
				ctx, toPGUUID(submissionID))
			if lockErr != nil {
				return lockErr
			}
			if !isOpenSubmissionState(locked.State) {
				return retentionStateConflict()
			}
			asset, markErr := queries.MarkAssetVerifying(
				ctx, dbgen.MarkAssetVerifyingParams{
					ID: toPGUUID(assetID), SubmissionID: toPGUUID(submissionID),
					ExpectedRevision: request.Msg.ExpectedAssetRevision.Value,
				})
			if markErr != nil {
				return markErr
			}
			object, getErr := service.dependencies.Objects.Get(
				ctx, imageassets.StagingObjectKey(assetID.String()))
			if getErr != nil {
				if errors.Is(getErr, imageassets.ErrObjectNotFound) {
					return verificationFailed()
				}
				return storageUnavailable()
			}
			body := object.Body
			if body == nil {
				return verificationFailed()
			}
			verified, verifyErr := imageassets.Verify(
				declarationFromRecord(asset), body)
			_ = body.Close()
			if errors.Is(verifyErr, imageassets.ErrSourceRead) {
				return storageUnavailable()
			}
			if verifyErr != nil || object.ContentType != asset.MediaType ||
				(object.Size >= 0 &&
					object.Size != asset.DeclaredEncodedBytes) {
				rejected, rejectErr := queries.MarkAssetRejected(
					ctx, dbgen.MarkAssetRejectedParams{
						ID:           toPGUUID(assetID),
						SubmissionID: toPGUUID(submissionID),
					})
				if rejectErr != nil {
					return rejectErr
				}
				if _, rejectErr = queries.RefreshSubmissionAssetState(
					ctx, toPGUUID(submissionID)); rejectErr != nil {
					return rejectErr
				}
				if rejectErr = enqueueAssetObjectDeletions(
					ctx, queries, rejected); rejectErr != nil {
					return rejectErr
				}
				rejectedVerification = verificationError(verifyErr)
				return nil
			}
			if lockErr = queries.CompleteObjectDeletion(
				ctx, dbgen.CompleteObjectDeletionParams{
					AssetID: asset.ID, ObjectKind: string(objectKindVerified),
				}); lockErr != nil {
				return lockErr
			}
			attemptedVerifiedWrite = true
			if putErr := service.dependencies.Objects.Put(
				ctx, imageassets.VerifiedObjectKey(assetID.String()),
				string(verified.MediaType), verified.Body); putErr != nil {
				return storageUnavailable()
			}
			current, sumErr := queries.SumOtherVerifiedAssetBytes(
				ctx, dbgen.SumOtherVerifiedAssetBytesParams{
					SubmissionID: toPGUUID(submissionID),
					AssetID:      toPGUUID(assetID),
				})
			if sumErr != nil {
				return sumErr
			}
			if sumErr = imageassets.ValidateSubmissionTotal(
				current, verified.EncodedBytes); sumErr != nil {
				return sumErr
			}
			checksum, _ := hex.DecodeString(verified.SHA256)
			finalized, sumErr := queries.MarkAssetVerified(
				ctx, dbgen.MarkAssetVerifiedParams{
					EncodedBytes:    verified.EncodedBytes,
					SanitizedSha256: checksum, ID: toPGUUID(assetID),
					SubmissionID: toPGUUID(submissionID),
				})
			if sumErr != nil {
				return sumErr
			}
			if sumErr = enqueueObjectDeletion(
				ctx, queries, asset.ID, objectKindStaging,
				pgtype.Text{}); sumErr != nil {
				return sumErr
			}
			_, sumErr = queries.UpdateSubmissionVerifiedBytes(
				ctx, dbgen.UpdateSubmissionVerifiedBytesParams{
					VerifiedEncodedBytes: current + verified.EncodedBytes,
					SubmissionRecordID:   toPGUUID(submissionID),
				})
			if sumErr != nil {
				return sumErr
			}
			response = &realqav1.FinalizeImageUploadResponse{
				Asset: updatedAssetProto(finalized),
				Idempotency: &realqav1.IdempotencyResult{
					Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_FINALIZE_IMAGE_UPLOAD,
					OriginallyCompletedAt: timestamp(finalized.VerifiedAt),
				},
			}
			payload, marshalErr := proto.MarshalOptions{
				Deterministic: true,
			}.Marshal(response)
			if marshalErr != nil {
				return marshalErr
			}
			_, sumErr = queries.CreateIdempotencyRecord(
				ctx, dbgen.CreateIdempotencyRecordParams{
					ID: toPGUUID(recordID), CallerKind: "user",
					CallerDigest: actor.digest, Operation: "finalize_image_upload",
					IdempotencyKey: toPGUUID(idempotencyID),
					RequestDigest:  requestDigest, ResourceID: toPGUUID(assetID),
					ResponsePayload: payload,
				})
			return sumErr
		})
	if err == nil && rejectedVerification != nil {
		service.drainObjectDeletionsBestEffort(context.WithoutCancel(ctx))
		return nil, rejectedVerification
	}
	if err != nil {
		if replay, ok, replayErr := service.finalizeImageUploadReplay(
			ctx, actor, idempotencyID, requestDigest,
			submissionID, assetID); ok {
			if replayErr != nil {
				return replay, replayErr
			}
			if attemptedVerifiedWrite {
				// A commit result may be ambiguous after the object write.
				if cleanupErr := service.cleanupUnownedVerifiedObject(
					context.WithoutCancel(ctx), submissionID, assetID); cleanupErr != nil {
					return nil, cleanupErr
				}
			}
			return replay, nil
		}
		if attemptedVerifiedWrite {
			if cleanupErr := service.cleanupUnownedVerifiedObject(
				context.WithoutCancel(ctx), submissionID, assetID); cleanupErr != nil {
				return nil, cleanupErr
			}
		}
		if errors.Is(err, imageassets.ErrEncodedTooLarge) {
			return nil, invalid(
				realqav1.ErrorReason_ERROR_REASON_SESSION_TOO_LARGE)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, service.assetMutationConflict(
				ctx, submissionID, assetID)
		}
		return nil, err
	}
	service.drainObjectDeletionsBestEffort(context.WithoutCancel(ctx))
	audit(ctx, service.dependencies, actor, "image_upload_verified",
		scope, assetID, "allow", "success")
	return connect.NewResponse(response), nil
}

func (service *Submission) assetMutationConflict(
	ctx context.Context,
	submissionID uuid.UUID,
	assetID uuid.UUID,
) error {
	current, err := service.dependencies.Store.Queries().GetAssetRecord(
		ctx, dbgen.GetAssetRecordParams{
			ID:           toPGUUID(assetID),
			SubmissionID: toPGUUID(submissionID),
		})
	if err == nil {
		return stale(current.Revision)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return retentionStateConflict()
	}
	return err
}

func (service *Submission) finalizeImageUploadReplay(
	ctx context.Context,
	actor caller,
	idempotencyID uuid.UUID,
	digest []byte,
	submissionID uuid.UUID,
	assetID uuid.UUID,
) (*connect.Response[realqav1.FinalizeImageUploadResponse], bool, error) {
	record, err := service.dependencies.Store.Queries().GetIdempotencyRecord(
		ctx, idempotencyLookupFor(
			actor, idempotencyID, "finalize_image_upload"))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if !bytes.Equal(record.RequestDigest, digest) {
		return nil, true, idempotencyConflict()
	}
	resourceID, err := fromPGUUID(record.ResourceID)
	if err != nil || resourceID != assetID {
		return nil, true, errors.New(
			"realqa images: invalid finalize replay state")
	}
	response := new(realqav1.FinalizeImageUploadResponse)
	if err = proto.Unmarshal(record.ResponsePayload, response); err != nil {
		return nil, true, err
	}
	if response.Asset == nil || response.Asset.AssetId == nil ||
		response.Asset.AssetId.Value != assetID.String() {
		return nil, true, errors.New(
			"realqa images: invalid finalize replay state")
	}
	asset, err := service.dependencies.Store.Queries().GetAssetRecord(
		ctx, dbgen.GetAssetRecordParams{
			ID: toPGUUID(assetID), SubmissionID: toPGUUID(submissionID),
		})
	if err != nil || asset.SubmissionID != toPGUUID(submissionID) {
		return nil, true, errors.New(
			"realqa images: invalid finalize replay scope")
	}
	response.Idempotency = &realqav1.IdempotencyResult{
		Replayed:              true,
		Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_FINALIZE_IMAGE_UPLOAD,
		OriginallyCompletedAt: timestamp(record.CompletedAt),
	}
	return connect.NewResponse(response), true, nil
}

func (service *Submission) GetSubmission(
	ctx context.Context,
	request *connect.Request[realqav1.GetSubmissionRequest],
) (*connect.Response[realqav1.GetSubmissionResponse], error) {
	if request == nil || request.Msg == nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	_, id, _, _, err := service.authorizeSubmissionOwnerRequest(
		ctx, request.Msg.SubmissionId)
	if err != nil {
		return nil, err
	}
	submission, err := service.loadSubmission(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&realqav1.GetSubmissionResponse{
		Submission: submission,
	}), nil
}

func (service *Submission) ListSubmissions(
	ctx context.Context,
	request *connect.Request[realqav1.ListSubmissionsRequest],
) (*connect.Response[realqav1.ListSubmissionsResponse], error) {
	if request == nil || request.Msg == nil {
		return nil, invalid(
			realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	actor, err := resolveCaller(ctx, service.dependencies)
	if err != nil {
		return nil, err
	}
	scope, err := parseOwner(request.Msg.Owner)
	if err != nil {
		return nil, err
	}
	access, err := authorizeOwner(
		ctx, service.dependencies, actor, scope, false, false)
	if err != nil {
		return nil, err
	}
	canManage := scope.kind == "organization" &&
		(access.Role == "owner" || access.Role == "admin")
	size, after, err := page(request.Msg.Page)
	if err != nil {
		return nil, err
	}
	rows, err := service.dependencies.Store.Queries().ListSubmissionRecords(
		ctx, dbgen.ListSubmissionRecordsParams{
			OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
			AfterID: pageLowerBound(after), AccountID: toPGUUID(actor.accountID),
			CanManage: canManage, PageLimit: size + 1,
		})
	if err != nil {
		return nil, err
	}
	hasMore := len(rows) > int(size)
	if hasMore {
		rows = rows[:size]
	}
	response := &realqav1.ListSubmissionsResponse{
		Submissions: make([]*realqav1.SubmissionSummary, 0, len(rows)),
		Page:        &realqav1.PageResponse{},
	}
	var last uuid.UUID
	recoveries := make([]storageRecoveryNotification, 0, len(rows))
	for _, row := range rows {
		last, err = fromPGUUID(row.ID)
		if err != nil {
			return nil, err
		}
		assets, listErr := service.dependencies.Store.Queries().
			ListSubmissionAssets(ctx, row.ID)
		if listErr != nil {
			return nil, listErr
		}
		summary := &realqav1.SubmissionSummary{
			SubmissionId: &realqav1.UuidV7{Value: last.String()},
			State:        submissionState(row.State),
			Assets:       make([]*realqav1.AssetSummary, 0, len(assets)),
			CreatedAt:    timestamp(row.CreatedAt), UpdatedAt: timestamp(row.UpdatedAt),
			SubmittedAt: timestamp(row.SubmittedAt),
		}
		if row.ProviderIssueID.Valid && row.ProviderIssueUrl.Valid {
			summary.ProviderIssue = &realqav1.ProviderIssueReference{
				Tracker:  realqav1.TrackerKind_TRACKER_KIND_GITHUB_COM,
				IssueId:  row.ProviderIssueID.String,
				IssueUrl: row.ProviderIssueUrl.String,
			}
		}
		for _, asset := range assets {
			id := uuid.UUID(asset.ID.Bytes)
			summary.Assets = append(summary.Assets, &realqav1.AssetSummary{
				AssetId:     &realqav1.UuidV7{Value: id.String()},
				UploadState: uploadState(asset.UploadState),
				AssetState:  assetState(asset.State),
				CreatedAt:   timestamp(asset.CreatedAt),
				RemovedAt:   timestamp(asset.RemovedAt),
			})
		}
		recovery, err := storageRecoveryForSubmission(
			ctx, service.dependencies.Store.Queries(), row.ID)
		if err != nil {
			return nil, err
		}
		summary.StorageBillingRecovery = recovery.message
		if recovery.message != nil {
			recoveries = append(recoveries, recovery)
		}
		response.Submissions = append(response.Submissions, summary)
	}
	response.Page.NextCursor = cursor(last, hasMore)
	if len(recoveries) > 0 {
		err = service.dependencies.Store.WithinTransaction(
			ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
				for _, recovery := range recoveries {
					if markErr := markStorageRecoveryNotified(
						ctx, queries, recovery); markErr != nil {
						return markErr
					}
				}
				return nil
			})
		if err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(response), nil
}

func (service *Submission) DeleteImage(
	ctx context.Context,
	request *connect.Request[realqav1.DeleteImageRequest],
) (*connect.Response[realqav1.DeleteImageResponse], error) {
	if request == nil || request.Msg == nil ||
		request.Msg.ExpectedSubmissionRevision == nil ||
		request.Msg.ExpectedAssetRevision == nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_STALE_REVISION)
	}
	idempotencyID, err := parseIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	requestDigest, err := digestMessage(request.Msg)
	if err != nil {
		return nil, err
	}
	actor, submissionID, submission, scope, err :=
		service.authorizeSubmissionOwnerRequest(
			ctx, request.Msg.SubmissionId)
	if err != nil {
		return nil, err
	}
	assetID, err := parseUUIDMessage(request.Msg.AssetId)
	if err != nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_MALFORMED_IMAGE)
	}
	if replay, ok, replayErr := service.deleteImageReplay(
		ctx, actor, idempotencyID, requestDigest, submissionID, assetID); ok {
		return replay, replayErr
	}
	if submission.Revision != request.Msg.ExpectedSubmissionRevision.Value {
		return nil, stale(submission.Revision)
	}
	recordID, err := newID(service.dependencies)
	if err != nil {
		return nil, err
	}
	var removed dbgen.RealqaAsset
	var response *realqav1.DeleteImageResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if existing, lookupErr := queries.GetIdempotencyRecord(
				ctx, idempotencyLookupFor(
					actor, idempotencyID, "delete_image"),
			); lookupErr == nil {
				if !bytes.Equal(existing.RequestDigest, requestDigest) {
					return idempotencyConflict()
				}
				return errIdempotencyReplay
			} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
				return lookupErr
			}
			locked, lockErr := queries.LockSubmissionRecord(
				ctx, toPGUUID(submissionID))
			if lockErr != nil {
				return lockErr
			}
			if locked.Revision != request.Msg.ExpectedSubmissionRevision.Value {
				return stale(locked.Revision)
			}
			removed, lockErr = queries.TombstoneAsset(
				ctx, dbgen.TombstoneAssetParams{
					AssetRecordID:     toPGUUID(assetID),
					AssetSubmissionID: toPGUUID(submissionID),
					ExpectedRevision:  request.Msg.ExpectedAssetRevision.Value,
				})
			if lockErr != nil {
				if errors.Is(lockErr, pgx.ErrNoRows) {
					current, currentErr := queries.GetAssetRecord(
						ctx, dbgen.GetAssetRecordParams{
							ID:           toPGUUID(assetID),
							SubmissionID: toPGUUID(submissionID),
						})
					if currentErr == nil {
						if current.Revision ==
							request.Msg.ExpectedAssetRevision.Value &&
							isTerminalAssetState(current.State) {
							return retentionStateConflict()
						}
						return stale(current.Revision)
					}
					if !errors.Is(currentErr, pgx.ErrNoRows) {
						return currentErr
					}
				}
				return lockErr
			}
			if _, lockErr = queries.CloseStorageRetentionForAsset(
				ctx, dbgen.CloseStorageRetentionForAssetParams{
					Cutoff:  removed.RemovedAt,
					AssetID: removed.ID,
				}); lockErr != nil {
				return lockErr
			}
			if lockErr = enqueueAssetObjectDeletions(
				ctx, queries, removed); lockErr != nil {
				return lockErr
			}
			updatedRecord, lockErr := queries.TouchSubmissionAfterAssetDeletion(
				ctx, dbgen.TouchSubmissionAfterAssetDeletionParams{
					ID:               toPGUUID(submissionID),
					ExpectedRevision: request.Msg.ExpectedSubmissionRevision.Value,
				})
			if lockErr != nil {
				return lockErr
			}
			if _, lockErr = queries.MarkSubmissionStorageClosurePending(
				ctx, dbgen.MarkSubmissionStorageClosurePendingParams{
					Cutoff:       removed.RemovedAt,
					SubmissionID: toPGUUID(submissionID),
				}); lockErr != nil {
				return lockErr
			}
			if updatedRecord.VerifiedEncodedBytes == 0 {
				resolved, resolveErr := queries.ResolveStorageRecovery(
					ctx, dbgen.ResolveStorageRecoveryParams{
						RecoveredAt:        removed.RemovedAt,
						TargetSubmissionID: toPGUUID(submissionID),
					})
				if resolveErr == nil {
					updatedRecord = resolved
				} else if !errors.Is(resolveErr, pgx.ErrNoRows) {
					return resolveErr
				}
			}
			updated, lockErr := loadSubmissionWithRecord(ctx, queries, updatedRecord)
			if lockErr != nil {
				return lockErr
			}
			response = &realqav1.DeleteImageResponse{
				Submission: updated,
				Idempotency: &realqav1.IdempotencyResult{
					Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_IMAGE,
					OriginallyCompletedAt: timestamp(removed.RemovedAt),
				},
			}
			payload, marshalErr := proto.MarshalOptions{
				Deterministic: true,
			}.Marshal(response)
			if marshalErr != nil {
				return marshalErr
			}
			_, lockErr = queries.CreateIdempotencyRecord(
				ctx, dbgen.CreateIdempotencyRecordParams{
					ID: toPGUUID(recordID), CallerKind: "user",
					CallerDigest: actor.digest, Operation: "delete_image",
					IdempotencyKey: toPGUUID(idempotencyID),
					RequestDigest:  requestDigest, ResourceID: toPGUUID(assetID),
					ResponsePayload: payload,
				})
			return lockErr
		})
	if err != nil {
		if replay, ok, replayErr := service.deleteImageReplay(
			ctx, actor, idempotencyID, requestDigest,
			submissionID, assetID); ok {
			return replay, replayErr
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, stale(request.Msg.ExpectedAssetRevision.Value)
	}
	if err != nil {
		return nil, err
	}
	service.drainObjectDeletionsBestEffort(context.WithoutCancel(ctx))
	service.bestEffortIssueUpdate(
		context.WithoutCancel(ctx), submission, []dbgen.RealqaAsset{removed})
	audit(ctx, service.dependencies, actor, "image_deleted",
		scope, assetID, "allow", "success")
	return connect.NewResponse(response), nil
}

func (service *Submission) DeleteSubmissionAssets(
	ctx context.Context,
	request *connect.Request[realqav1.DeleteSubmissionAssetsRequest],
) (*connect.Response[realqav1.DeleteSubmissionAssetsResponse], error) {
	if request == nil || request.Msg == nil ||
		request.Msg.ExpectedSubmissionRevision == nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_STALE_REVISION)
	}
	idempotencyID, err := parseIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	requestDigest, err := digestMessage(request.Msg)
	if err != nil {
		return nil, err
	}
	actor, submissionID, submission, scope, err :=
		service.authorizeSubmissionOwnerRequest(
			ctx, request.Msg.SubmissionId)
	if err != nil {
		return nil, err
	}
	if replay, ok, replayErr := service.deleteSubmissionAssetsReplay(
		ctx, actor, idempotencyID, requestDigest, submissionID); ok {
		return replay, replayErr
	}
	if submission.Revision != request.Msg.ExpectedSubmissionRevision.Value {
		return nil, stale(submission.Revision)
	}
	recordID, err := newID(service.dependencies)
	if err != nil {
		return nil, err
	}
	var (
		removed  []dbgen.RealqaAsset
		response *realqav1.DeleteSubmissionAssetsResponse
	)
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if existing, lookupErr := queries.GetIdempotencyRecord(
				ctx, idempotencyLookupFor(
					actor, idempotencyID, "delete_submission_assets"),
			); lookupErr == nil {
				if !bytes.Equal(existing.RequestDigest, requestDigest) {
					return idempotencyConflict()
				}
				return errIdempotencyReplay
			} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
				return lookupErr
			}
			locked, lockErr := queries.LockSubmissionRecord(
				ctx, toPGUUID(submissionID))
			if lockErr != nil {
				return lockErr
			}
			if locked.Revision != request.Msg.ExpectedSubmissionRevision.Value {
				return stale(locked.Revision)
			}
			removed, lockErr = queries.TombstoneSubmissionAssets(
				ctx, toPGUUID(submissionID))
			if lockErr != nil {
				return lockErr
			}
			if len(removed) == 0 {
				return retentionStateConflict()
			}
			for _, asset := range removed {
				if lockErr = enqueueAssetObjectDeletions(
					ctx, queries, asset); lockErr != nil {
					return lockErr
				}
			}
			cutoff := service.dependencies.Clock.Now().UTC()
			if len(removed) > 0 && removed[0].RemovedAt.Valid {
				cutoff = removed[0].RemovedAt.Time
			}
			if _, lockErr = queries.CloseStorageRetentionForSubmission(
				ctx, dbgen.CloseStorageRetentionForSubmissionParams{
					Cutoff:       pgTimestamp(cutoff),
					SubmissionID: toPGUUID(submissionID),
				}); lockErr != nil {
				return lockErr
			}
			updated, lockErr := queries.MarkSubmissionAssetsDeleted(
				ctx, dbgen.MarkSubmissionAssetsDeletedParams{
					ID:               toPGUUID(submissionID),
					ExpectedRevision: request.Msg.ExpectedSubmissionRevision.Value,
				})
			if lockErr != nil {
				return lockErr
			}
			if _, lockErr = queries.MarkSubmissionStorageClosurePending(
				ctx, dbgen.MarkSubmissionStorageClosurePendingParams{
					Cutoff:       pgTimestamp(cutoff),
					SubmissionID: toPGUUID(submissionID),
				}); lockErr != nil {
				return lockErr
			}
			resolved, resolveErr := queries.ResolveStorageRecovery(
				ctx, dbgen.ResolveStorageRecoveryParams{
					RecoveredAt:        pgTimestamp(cutoff),
					TargetSubmissionID: toPGUUID(submissionID),
				})
			if resolveErr == nil {
				updated = resolved
			} else if !errors.Is(resolveErr, pgx.ErrNoRows) {
				return resolveErr
			}
			result, lockErr := loadSubmissionWithRecord(ctx, queries, updated)
			if lockErr != nil {
				return lockErr
			}
			response = &realqav1.DeleteSubmissionAssetsResponse{
				Submission: result,
				Idempotency: &realqav1.IdempotencyResult{
					Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_SUBMISSION_ASSETS,
					OriginallyCompletedAt: result.UpdatedAt,
				},
			}
			payload, marshalErr := proto.MarshalOptions{
				Deterministic: true,
			}.Marshal(response)
			if marshalErr != nil {
				return marshalErr
			}
			_, lockErr = queries.CreateIdempotencyRecord(
				ctx, dbgen.CreateIdempotencyRecordParams{
					ID: toPGUUID(recordID), CallerKind: "user",
					CallerDigest:    actor.digest,
					Operation:       "delete_submission_assets",
					IdempotencyKey:  toPGUUID(idempotencyID),
					RequestDigest:   requestDigest,
					ResourceID:      toPGUUID(submissionID),
					ResponsePayload: payload,
				})
			return lockErr
		})
	if err != nil {
		if replay, ok, replayErr := service.deleteSubmissionAssetsReplay(
			ctx, actor, idempotencyID, requestDigest, submissionID); ok {
			return replay, replayErr
		}
		return nil, err
	}
	service.drainObjectDeletionsBestEffort(context.WithoutCancel(ctx))
	service.bestEffortIssueUpdate(
		context.WithoutCancel(ctx), submission, removed)
	audit(ctx, service.dependencies, actor, "submission_assets_deleted",
		scope, submissionID, "allow", "success")
	return connect.NewResponse(response), nil
}

func (service *Submission) deleteImageReplay(
	ctx context.Context,
	actor caller,
	idempotencyID uuid.UUID,
	digest []byte,
	submissionID uuid.UUID,
	assetID uuid.UUID,
) (*connect.Response[realqav1.DeleteImageResponse], bool, error) {
	record, err := service.dependencies.Store.Queries().GetIdempotencyRecord(
		ctx, idempotencyLookupFor(actor, idempotencyID, "delete_image"))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if !bytes.Equal(record.RequestDigest, digest) {
		return nil, true, idempotencyConflict()
	}
	resourceID, err := fromPGUUID(record.ResourceID)
	if err != nil || resourceID != assetID {
		return nil, true, errors.New(
			"realqa images: invalid image deletion replay state")
	}
	response := new(realqav1.DeleteImageResponse)
	if err = proto.Unmarshal(record.ResponsePayload, response); err != nil {
		return nil, true, err
	}
	if response.Submission == nil || response.Submission.SubmissionId == nil ||
		response.Submission.SubmissionId.Value != submissionID.String() {
		return nil, true, errors.New(
			"realqa images: invalid image deletion replay state")
	}
	response.Idempotency = &realqav1.IdempotencyResult{
		Replayed:              true,
		Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_IMAGE,
		OriginallyCompletedAt: timestamp(record.CompletedAt),
	}
	return connect.NewResponse(response), true, nil
}

func (service *Submission) deleteSubmissionAssetsReplay(
	ctx context.Context,
	actor caller,
	idempotencyID uuid.UUID,
	digest []byte,
	submissionID uuid.UUID,
) (*connect.Response[realqav1.DeleteSubmissionAssetsResponse], bool, error) {
	record, err := service.dependencies.Store.Queries().GetIdempotencyRecord(
		ctx, idempotencyLookupFor(
			actor, idempotencyID, "delete_submission_assets"))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if !bytes.Equal(record.RequestDigest, digest) {
		return nil, true, idempotencyConflict()
	}
	resourceID, err := fromPGUUID(record.ResourceID)
	if err != nil || resourceID != submissionID {
		return nil, true, errors.New(
			"realqa images: invalid asset deletion replay state")
	}
	response := new(realqav1.DeleteSubmissionAssetsResponse)
	if err = proto.Unmarshal(record.ResponsePayload, response); err != nil {
		return nil, true, err
	}
	if response.Submission == nil || response.Submission.SubmissionId == nil ||
		response.Submission.SubmissionId.Value != submissionID.String() {
		return nil, true, errors.New(
			"realqa images: invalid asset deletion replay state")
	}
	response.Idempotency = &realqav1.IdempotencyResult{
		Replayed:              true,
		Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_SUBMISSION_ASSETS,
		OriginallyCompletedAt: timestamp(record.CompletedAt),
	}
	return connect.NewResponse(response), true, nil
}

// PromoteSubmittedAssets copies verified private objects to opaque durable
// keys, then atomically makes each identifier discoverable by public GET.
func (service *Submission) PromoteSubmittedAssets(
	ctx context.Context,
	submissionID uuid.UUID,
	orderedAssetIDs []uuid.UUID,
) error {
	if service.dependencies.Objects == nil {
		return storageUnavailable()
	}
	seen := make(map[uuid.UUID]struct{}, len(orderedAssetIDs))
	for _, assetID := range orderedAssetIDs {
		if _, duplicate := seen[assetID]; duplicate {
			return invalid(
				realqav1.ErrorReason_ERROR_REASON_RETENTION_STATE_CONFLICT)
		}
		seen[assetID] = struct{}{}
		asset, err := service.dependencies.Store.Queries().GetAssetRecord(
			ctx, dbgen.GetAssetRecordParams{
				ID: toPGUUID(assetID), SubmissionID: toPGUUID(submissionID),
			})
		if err = validatePromotionCandidate(asset, err); err != nil {
			return err
		}
		if asset.State == "public_retained" && asset.PublicID.Valid {
			continue
		}
		if asset.State != "verified_unlinked" {
			return invalid(
				realqav1.ErrorReason_ERROR_REASON_RETENTION_STATE_CONFLICT)
		}
		source, err := service.dependencies.Objects.Get(
			ctx, imageassets.VerifiedObjectKey(assetID.String()))
		if err != nil {
			return storageUnavailable()
		}
		body, err := readPromotionBody(source.Body, asset.EncodedBytes)
		if err != nil {
			return err
		}
		publicID := asset.PublicID.String
		if !asset.PublicID.Valid {
			publicID, err = imageassets.NewPublicID()
			if err != nil {
				return err
			}
			asset, err = service.dependencies.Store.Queries().
				ReserveAssetPublicID(ctx, dbgen.ReserveAssetPublicIDParams{
					PublicID:     pgtype.Text{String: publicID, Valid: true},
					ID:           toPGUUID(assetID),
					SubmissionID: toPGUUID(submissionID),
				})
			if err != nil {
				return err
			}
			publicID = asset.PublicID.String
		}
		var (
			publicKey            = imageassets.PublicObjectKey(publicID)
			attemptedPublicWrite bool
			alreadyPromoted      bool
		)
		err = service.dependencies.Store.WithinTransaction(
			ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
				if _, lockErr := queries.LockCurrentStorageAuthorizationBinding(
					ctx, toPGUUID(submissionID)); lockErr != nil &&
					!errors.Is(lockErr, pgx.ErrNoRows) {
					return lockErr
				}
				submission, lockErr := queries.LockSubmissionRecord(
					ctx, toPGUUID(submissionID))
				if lockErr != nil {
					return lockErr
				}
				if !isPromotionSubmissionState(submission.State) {
					return retentionStateConflict()
				}
				locked, lockErr := queries.LockAssetRecord(
					ctx, dbgen.LockAssetRecordParams{
						ID:           toPGUUID(assetID),
						SubmissionID: toPGUUID(submissionID),
					})
				if lockErr = validatePromotionCandidate(locked, lockErr); lockErr != nil {
					return lockErr
				}
				if locked.State == "public_retained" && locked.PublicID.Valid {
					alreadyPromoted = true
					return nil
				}
				if locked.State != "verified_unlinked" {
					return retentionStateConflict()
				}
				if !locked.PublicID.Valid ||
					locked.PublicID.String != publicID {
					return retentionStateConflict()
				}
				asset = locked
				if lockErr = queries.CompleteObjectDeletion(
					ctx, dbgen.CompleteObjectDeletionParams{
						AssetID: asset.ID, ObjectKind: string(objectKindPublic),
						PublicID: asset.PublicID,
					}); lockErr != nil {
					return lockErr
				}
				attemptedPublicWrite = true
				if putErr := service.dependencies.Objects.Put(
					ctx, publicKey, asset.MediaType, body); putErr != nil {
					return storageUnavailable()
				}
				if _, promoteErr := queries.PromoteAsset(
					ctx, dbgen.PromoteAssetParams{
						PublicID:     pgtype.Text{String: publicID, Valid: true},
						ID:           toPGUUID(assetID),
						SubmissionID: toPGUUID(submissionID),
					}); promoteErr != nil {
					return promoteErr
				}
				if _, promoteErr := queries.BeginStorageRetention(
					ctx, dbgen.BeginStorageRetentionParams{
						StartsAt: pgTimestamp(
							service.dependencies.Clock.Now().UTC()),
						AssetID:      toPGUUID(assetID),
						SubmissionID: toPGUUID(submissionID),
					}); promoteErr != nil {
					return promoteErr
				}
				return enqueueObjectDeletion(
					ctx, queries, asset.ID, objectKindVerified, pgtype.Text{})
			})
		if err == nil && alreadyPromoted {
			continue
		}
		if err != nil {
			if !attemptedPublicWrite {
				return err
			}
			cleanupCtx := context.WithoutCancel(ctx)
			current, lookupErr := service.dependencies.Store.Queries().
				GetAssetRecord(cleanupCtx, dbgen.GetAssetRecordParams{
					ID:           toPGUUID(assetID),
					SubmissionID: toPGUUID(submissionID),
				})
			if lookupErr == nil && assetOwnsPublicObject(current, publicID) {
				service.drainObjectDeletionsBestEffort(cleanupCtx)
				continue
			}
			if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
				return storageUnavailable()
			}
			if cleanupErr := service.dependencies.Objects.Delete(
				cleanupCtx, publicKey); cleanupErr != nil {
				enqueueErr := service.dependencies.Store.Queries().
					EnqueueObjectDeletion(
						cleanupCtx, dbgen.EnqueueObjectDeletionParams{
							AssetID: asset.ID, ObjectKind: string(objectKindPublic),
							PublicID: pgtype.Text{
								String: publicID, Valid: true,
							},
						})
				if enqueueErr != nil {
					return errors.Join(err, enqueueErr)
				}
				service.drainObjectDeletionsBestEffort(cleanupCtx)
			}
			return err
		}
		service.drainObjectDeletionsBestEffort(context.WithoutCancel(ctx))
	}
	_, err := service.dependencies.Store.Queries().MarkSubmissionSubmitted(
		ctx, toPGUUID(submissionID))
	if errors.Is(err, pgx.ErrNoRows) {
		current, lookupErr := service.dependencies.Store.Queries().
			GetSubmissionRecord(ctx, toPGUUID(submissionID))
		if lookupErr == nil && current.State == "submitted" {
			return nil
		}
		if lookupErr != nil {
			return lookupErr
		}
	}
	return err
}

func readPromotionBody(source io.ReadCloser, expectedBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(
		source, imageassets.MaxImageEncodedBytes+1))
	_ = source.Close()
	if err != nil {
		return nil, storageUnavailable()
	}
	if int64(len(body)) != expectedBytes {
		return nil, verificationFailed()
	}
	return body, nil
}

func (service *Submission) PublicAsset(
	ctx context.Context,
	publicID string,
) (imageassets.PublicRecord, error) {
	row, err := service.dependencies.Store.Queries().GetPublicAsset(
		ctx, pgtype.Text{String: publicID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return imageassets.PublicRecord{}, imageassets.ErrObjectNotFound
		}
		return imageassets.PublicRecord{}, err
	}
	if row.State == "removed_placeholder" {
		return imageassets.PublicRecord{
			State: imageassets.PublicStateRemoved,
		}, nil
	}
	if row.State != "public_retained" || !row.PublicID.Valid {
		return imageassets.PublicRecord{}, imageassets.ErrObjectNotFound
	}
	return imageassets.PublicRecord{
		State:       imageassets.PublicStateRetained,
		ObjectKey:   imageassets.PublicObjectKey(row.PublicID.String),
		ContentType: row.MediaType,
	}, nil
}

// CleanupExpiredStaging removes terminal private data and tombstones any
// partially promoted public data at the 24-hour boundary. It never performs a
// billing or provider mutation.
func (service *Submission) CleanupExpiredStaging(
	ctx context.Context,
	cutoff time.Time,
	batchSize int32,
) (int, error) {
	if batchSize <= 0 || batchSize > 1000 {
		batchSize = 100
	}
	var cleaned []dbgen.RealqaAsset
	err := service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			rows, listErr := queries.ListExpiredSubmissionAssets(
				ctx, dbgen.ListExpiredSubmissionAssetsParams{
					Cutoff: pgTimestamp(cutoff), BatchLimit: batchSize,
				})
			if listErr != nil {
				return listErr
			}
			for _, row := range rows {
				submission, lockErr := queries.LockExpiredSubmissionRecord(
					ctx, dbgen.LockExpiredSubmissionRecordParams{
						ID: row.SubmissionID, Cutoff: pgTimestamp(cutoff),
					})
				if errors.Is(lockErr, pgx.ErrNoRows) {
					continue
				}
				if lockErr != nil {
					return lockErr
				}
				if !submission.SubmittedAt.Valid &&
					isCleanupTerminalizableSubmissionState(submission.State) {
					submission, lockErr = queries.MarkSubmissionAssetsDeleted(
						ctx, dbgen.MarkSubmissionAssetsDeletedParams{
							ID: submission.ID, ExpectedRevision: submission.Revision,
						})
					if lockErr != nil {
						return lockErr
					}
				}
				locked, lockErr := queries.LockExpiredSubmissionAsset(
					ctx, dbgen.LockExpiredSubmissionAssetParams{
						ID: row.ID, SubmissionID: row.SubmissionID,
						Cutoff: pgTimestamp(cutoff),
					})
				if errors.Is(lockErr, pgx.ErrNoRows) {
					continue
				}
				if lockErr != nil {
					return lockErr
				}
				var (
					value      dbgen.RealqaAsset
					cleanupErr error
				)
				if locked.PublicID.Valid {
					value, cleanupErr = queries.TombstoneAsset(
						ctx, dbgen.TombstoneAssetParams{
							AssetRecordID:     locked.ID,
							AssetSubmissionID: locked.SubmissionID,
							ExpectedRevision:  locked.Revision,
						})
				} else {
					value, cleanupErr = queries.ExpireAsset(ctx, locked.ID)
				}
				if cleanupErr != nil {
					return cleanupErr
				}
				if locked.State == "public_retained" {
					if _, cleanupErr = queries.CloseStorageRetentionForAsset(
						ctx, dbgen.CloseStorageRetentionForAssetParams{
							Cutoff:  value.RemovedAt,
							AssetID: value.ID,
						}); cleanupErr != nil {
						return cleanupErr
					}
				}
				closurePending, cleanupErr := queries.
					MarkSubmissionStorageClosurePending(
						ctx,
						dbgen.MarkSubmissionStorageClosurePendingParams{
							Cutoff:       value.RemovedAt,
							SubmissionID: value.SubmissionID,
						})
				if cleanupErr != nil {
					return cleanupErr
				}
				if closurePending > 0 {
					if _, resolveErr := queries.ResolveStorageRecovery(
						ctx, dbgen.ResolveStorageRecoveryParams{
							RecoveredAt:        value.RemovedAt,
							TargetSubmissionID: value.SubmissionID,
						}); resolveErr != nil &&
						!errors.Is(resolveErr, pgx.ErrNoRows) {
						return resolveErr
					}
				}
				if submission.SubmittedAt.Valid &&
					locked.State == "verified_unlinked" {
					if _, cleanupErr = queries.TouchSubmissionAfterAssetDeletion(
						ctx, dbgen.TouchSubmissionAfterAssetDeletionParams{
							ID:               submission.ID,
							ExpectedRevision: submission.Revision,
						}); cleanupErr != nil {
						return cleanupErr
					}
				}
				if cleanupErr = enqueueAssetObjectDeletions(
					ctx, queries, value); cleanupErr != nil {
					return cleanupErr
				}
				cleaned = append(cleaned, value)
			}
			return nil
		})
	if err != nil {
		return 0, err
	}
	service.drainObjectDeletionsBestEffort(context.WithoutCancel(ctx))
	return len(cleaned), nil
}

func (service *Submission) RunStagingCleanup(
	ctx context.Context,
	interval time.Duration,
) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	cleanup := func() {
		if _, err := service.CleanupExpiredStaging(
			ctx, service.dependencies.Clock.Now().UTC(), 100); err != nil {
			safelog.Record(ctx, service.dependencies.Logger, slog.LevelError,
				safelog.EventIntegration, safelog.Fields{
					Decision: safelog.DecisionDeny,
					Result:   safelog.ResultFailure,
				})
		}
		if _, err := service.DrainObjectDeletions(ctx, 1000); err != nil {
			safelog.Record(ctx, service.dependencies.Logger, slog.LevelError,
				safelog.EventIntegration, safelog.Fields{
					Decision: safelog.DecisionDeny,
					Result:   safelog.ResultFailure,
				})
		}
	}
	cleanup()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}

// DeleteIssueAssets is the issue-deletion webhook path. The caller is expected
// to authenticate the webhook before invoking it.
func (service *Submission) DeleteIssueAssets(
	ctx context.Context,
	providerIssueID string,
) error {
	if providerIssueID == "" {
		return errors.New("realqa images: issue reference is required")
	}
	var removed []dbgen.RealqaAsset
	err := service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			issueID := pgtype.Text{String: providerIssueID, Valid: true}
			submissions, listErr := queries.LockIssueSubmissionRecords(
				ctx, issueID)
			if listErr != nil {
				return listErr
			}
			rows, listErr := queries.ListIssueAssets(
				ctx, issueID)
			if listErr != nil {
				return listErr
			}
			affected := make(map[pgtype.UUID]struct{}, len(submissions))
			for _, row := range rows {
				value, removeErr := queries.TombstoneAsset(
					ctx, dbgen.TombstoneAssetParams{
						AssetRecordID: row.ID, AssetSubmissionID: row.SubmissionID,
						ExpectedRevision: row.Revision,
					})
				if removeErr != nil {
					return removeErr
				}
				if _, removeErr = queries.CloseStorageRetentionForAsset(
					ctx, dbgen.CloseStorageRetentionForAssetParams{
						Cutoff:  value.RemovedAt,
						AssetID: value.ID,
					}); removeErr != nil {
					return removeErr
				}
				if removeErr = enqueueAssetObjectDeletions(
					ctx, queries, value); removeErr != nil {
					return removeErr
				}
				removed = append(removed, value)
				affected[row.SubmissionID] = struct{}{}
			}
			for _, submission := range submissions {
				if _, ok := affected[submission.ID]; !ok {
					continue
				}
				_, refreshErr := queries.TouchSubmissionAfterAssetDeletion(
					ctx, dbgen.TouchSubmissionAfterAssetDeletionParams{
						ID:               submission.ID,
						ExpectedRevision: submission.Revision,
					})
				if refreshErr != nil {
					return refreshErr
				}
				cutoff := service.dependencies.Clock.Now().UTC()
				for _, asset := range removed {
					if asset.SubmissionID == submission.ID &&
						asset.RemovedAt.Valid {
						cutoff = asset.RemovedAt.Time
						break
					}
				}
				closurePending, refreshErr := queries.
					MarkSubmissionStorageClosurePending(
						ctx,
						dbgen.MarkSubmissionStorageClosurePendingParams{
							Cutoff:       pgTimestamp(cutoff),
							SubmissionID: submission.ID,
						})
				if refreshErr != nil {
					return refreshErr
				}
				if closurePending == 0 {
					continue
				}
				if _, resolveErr := queries.ResolveStorageRecovery(
					ctx, dbgen.ResolveStorageRecoveryParams{
						RecoveredAt:        pgTimestamp(cutoff),
						TargetSubmissionID: submission.ID,
					}); resolveErr != nil &&
					!errors.Is(resolveErr, pgx.ErrNoRows) {
					return resolveErr
				}
			}
			return nil
		})
	if err != nil {
		return err
	}
	service.drainObjectDeletionsBestEffort(context.WithoutCancel(ctx))
	return nil
}

// DeleteBillingExpiredAssets uses the same tombstone-first path as an explicit
// range deletion, so a delibase outage cannot extend public readability.
func (service *Submission) DeleteBillingExpiredAssets(
	ctx context.Context,
	submissionID uuid.UUID,
) error {
	_, err := service.deleteBillingExpiredAssets(ctx, submissionID, nil)
	return err
}

type storageRecoveryExpiryClaim struct {
	ID     pgtype.UUID
	Cutoff time.Time
}

func (service *Submission) deleteBillingExpiredAssets(
	ctx context.Context,
	submissionID uuid.UUID,
	expiry *storageRecoveryExpiryClaim,
) (bool, error) {
	submission, err := service.dependencies.Store.Queries().GetSubmissionRecord(
		ctx, toPGUUID(submissionID))
	if err != nil {
		return false, err
	}
	var removed []dbgen.RealqaAsset
	claimed := expiry == nil
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			locked, lockErr := queries.LockSubmissionRecord(
				ctx, toPGUUID(submissionID))
			if lockErr != nil {
				return lockErr
			}
			if expiry != nil {
				if locked.State != "storage_billing_grace" {
					return nil
				}
				if _, lockErr = queries.MarkStorageRecoveryExpired(
					ctx, dbgen.MarkStorageRecoveryExpiredParams{
						ExpiredAt:    pgTimestamp(expiry.Cutoff),
						ID:           expiry.ID,
						SubmissionID: toPGUUID(submissionID),
						Cutoff:       pgTimestamp(expiry.Cutoff),
					}); lockErr != nil {
					if errors.Is(lockErr, pgx.ErrNoRows) {
						return nil
					}
					return lockErr
				}
				claimed = true
			}
			var removeErr error
			removed, removeErr = queries.TombstoneSubmissionAssets(
				ctx, toPGUUID(submissionID))
			if removeErr != nil {
				return removeErr
			}
			for _, asset := range removed {
				if removeErr = enqueueAssetObjectDeletions(
					ctx, queries, asset); removeErr != nil {
					return removeErr
				}
			}
			cutoff := service.dependencies.Clock.Now().UTC()
			if len(removed) > 0 && removed[0].RemovedAt.Valid {
				cutoff = removed[0].RemovedAt.Time
			}
			if _, removeErr = queries.CloseStorageRetentionForSubmission(
				ctx, dbgen.CloseStorageRetentionForSubmissionParams{
					Cutoff:       pgTimestamp(cutoff),
					SubmissionID: toPGUUID(submissionID),
				}); removeErr != nil {
				return removeErr
			}
			if len(removed) == 0 {
				_, removeErr = queries.MarkSubmissionStorageClosurePending(
					ctx, dbgen.MarkSubmissionStorageClosurePendingParams{
						Cutoff:       pgTimestamp(cutoff),
						SubmissionID: toPGUUID(submissionID),
					})
				return removeErr
			}
			_, lockErr = queries.MarkSubmissionAssetsDeleted(
				ctx, dbgen.MarkSubmissionAssetsDeletedParams{
					ID:               toPGUUID(submissionID),
					ExpectedRevision: locked.Revision,
				})
			if lockErr != nil {
				return lockErr
			}
			_, lockErr = queries.MarkSubmissionStorageClosurePending(
				ctx, dbgen.MarkSubmissionStorageClosurePendingParams{
					Cutoff:       pgTimestamp(cutoff),
					SubmissionID: toPGUUID(submissionID),
				})
			return lockErr
		})
	if err != nil {
		return false, err
	}
	if !claimed {
		return false, nil
	}
	service.drainObjectDeletionsBestEffort(context.WithoutCancel(ctx))
	service.bestEffortIssueUpdate(
		context.WithoutCancel(ctx), submission, removed)
	return true, nil
}

func (service *Submission) authorizeSubmissionRequest(
	ctx context.Context,
	message *realqav1.UuidV7,
) (caller, uuid.UUID, dbgen.RealqaSubmission, owner, error) {
	return service.authorizeSubmissionRequestWithRepository(
		ctx, message, true)
}

func (service *Submission) authorizeSubmissionOwnerRequest(
	ctx context.Context,
	message *realqav1.UuidV7,
) (caller, uuid.UUID, dbgen.RealqaSubmission, owner, error) {
	return service.authorizeSubmissionRequestWithRepository(
		ctx, message, false)
}

func (service *Submission) authorizeSubmissionRequestWithRepository(
	ctx context.Context,
	message *realqav1.UuidV7,
	requireRepositoryAccess bool,
) (caller, uuid.UUID, dbgen.RealqaSubmission, owner, error) {
	actor, err := resolveCaller(ctx, service.dependencies)
	if err != nil {
		return caller{}, uuid.Nil, dbgen.RealqaSubmission{}, owner{}, err
	}
	submissionID, err := parseUUIDMessage(message)
	if err != nil {
		return caller{}, uuid.Nil, dbgen.RealqaSubmission{}, owner{},
			invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	submission, err := service.dependencies.Store.Queries().GetSubmissionRecord(
		ctx, toPGUUID(submissionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return caller{}, uuid.Nil, dbgen.RealqaSubmission{}, owner{},
			permissionDenied()
	}
	if err != nil {
		return caller{}, uuid.Nil, dbgen.RealqaSubmission{}, owner{}, err
	}
	scopeID, err := fromPGUUID(submission.OwnerID)
	if err != nil {
		return caller{}, uuid.Nil, dbgen.RealqaSubmission{}, owner{}, err
	}
	scope := owner{kind: submission.OwnerKind, id: scopeID}
	access, err := authorizeOwner(
		ctx, service.dependencies, actor, scope, false, false)
	if err != nil {
		return caller{}, uuid.Nil, dbgen.RealqaSubmission{}, owner{}, err
	}
	creatorID, err := fromPGUUID(submission.CreatedByAccountID)
	if err != nil {
		return caller{}, uuid.Nil, dbgen.RealqaSubmission{}, owner{}, err
	}
	if creatorID == actor.accountID {
		return actor, submissionID, submission, scope, nil
	}
	if !submission.SubmittedAt.Valid ||
		!isRetainedSubmissionState(submission.State) {
		return caller{}, uuid.Nil, dbgen.RealqaSubmission{}, owner{},
			permissionDenied()
	}
	if !requireRepositoryAccess && scope.kind == "organization" &&
		(access.Role == "owner" || access.Role == "admin") {
		return actor, submissionID, submission, scope, nil
	}
	if !submission.DestinationID.Valid {
		return caller{}, uuid.Nil, dbgen.RealqaSubmission{}, owner{},
			permissionDenied()
	}
	destination, err := service.dependencies.Store.Queries().GetDestinationRecord(
		ctx, submission.DestinationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return caller{}, uuid.Nil, dbgen.RealqaSubmission{}, owner{},
			permissionDenied()
	}
	if err != nil {
		return caller{}, uuid.Nil, dbgen.RealqaSubmission{}, owner{}, err
	}
	installationID, err := fromPGUUID(destination.InstallationID)
	if err != nil {
		return caller{}, uuid.Nil, dbgen.RealqaSubmission{}, owner{}, err
	}
	if _, _, err = authorizeRepository(ctx, service.dependencies, actor, scope,
		&realqav1.TrackerDestination{
			Tracker: realqav1.TrackerKind_TRACKER_KIND_GITHUB_COM,
			InstallationId: &realqav1.UuidV7{
				Value: installationID.String(),
			},
			Repository: &realqav1.GitHubRepositoryRef{
				RepositoryId: destination.RepositoryID,
				Owner:        destination.RepositoryOwner,
				Name:         destination.RepositoryName,
			},
		}); err != nil {
		return caller{}, uuid.Nil, dbgen.RealqaSubmission{}, owner{}, err
	}
	return actor, submissionID, submission, scope, nil
}

func isTerminalAssetState(state string) bool {
	switch state {
	case "removed_placeholder", "deleted", "expired":
		return true
	default:
		return false
	}
}

func isOpenSubmissionState(state string) bool {
	switch state {
	case "draft", "uploading", "ready":
		return true
	default:
		return false
	}
}

func isPromotionSubmissionState(state string) bool {
	switch state {
	case "ready", "submitting", "reconciling", "submitted":
		return true
	default:
		return false
	}
}

func isCleanupTerminalizableSubmissionState(state string) bool {
	switch state {
	case "draft", "uploading", "ready", "submitting", "reconciling", "failed":
		return true
	default:
		return false
	}
}

func isRetainedSubmissionState(state string) bool {
	switch state {
	case "submitted", "storage_billing_grace", "assets_deleted":
		return true
	default:
		return false
	}
}

func retentionStateConflict() error {
	return invalid(realqav1.ErrorReason_ERROR_REASON_RETENTION_STATE_CONFLICT)
}

func assetOwnsPublicObject(asset dbgen.RealqaAsset, publicID string) bool {
	return asset.UploadState == "verified" &&
		asset.State == "public_retained" &&
		asset.PublicID.Valid &&
		asset.PublicID.String == publicID
}

func validatePromotionCandidate(asset dbgen.RealqaAsset, lookupErr error) error {
	if errors.Is(lookupErr, pgx.ErrNoRows) {
		return retentionStateConflict()
	}
	if lookupErr != nil {
		return storageUnavailable()
	}
	if asset.UploadState != "verified" {
		return retentionStateConflict()
	}
	return nil
}

func (service *Submission) loadSubmission(
	ctx context.Context,
	id uuid.UUID,
) (*realqav1.Submission, error) {
	record, err := service.dependencies.Store.Queries().GetSubmissionRecord(
		ctx, toPGUUID(id))
	if err != nil {
		return nil, err
	}
	return loadSubmissionWithRecord(
		ctx, service.dependencies.Store.Queries(), record)
}

func loadSubmissionWithQueries(
	ctx context.Context,
	queries *dbgen.Queries,
	id uuid.UUID,
) (*realqav1.Submission, error) {
	record, err := queries.GetSubmissionRecord(ctx, toPGUUID(id))
	if err != nil {
		return nil, err
	}
	return loadSubmissionWithRecord(ctx, queries, record)
}

func loadSubmissionWithRecord(
	ctx context.Context,
	queries dbgen.Querier,
	record dbgen.RealqaSubmission,
) (*realqav1.Submission, error) {
	id, err := fromPGUUID(record.ID)
	if err != nil {
		return nil, err
	}
	ownerID, err := fromPGUUID(record.OwnerID)
	if err != nil {
		return nil, err
	}
	result := &realqav1.Submission{
		SubmissionId:   &realqav1.UuidV7{Value: id.String()},
		Owner:          ownerProto(owner{kind: record.OwnerKind, id: ownerID}),
		PresetRevision: revision(record.PresetRevision),
		State:          submissionState(record.State), Revision: revision(record.Revision),
		UploadDeadline:               timestamp(record.UploadDeadline),
		TransferReservationExpiresAt: timestamp(record.UploadExpiresAt),
		CreatedAt:                    timestamp(record.CreatedAt), UpdatedAt: timestamp(record.UpdatedAt),
		SubmittedAt: timestamp(record.SubmittedAt),
	}
	if record.PayerOrganizationID.Valid && record.PayerTeamID.Valid {
		organizationID, orgErr := fromPGUUID(record.PayerOrganizationID)
		teamID, teamErr := fromPGUUID(record.PayerTeamID)
		if orgErr != nil || teamErr != nil {
			return nil, errors.New("realqa service: invalid stored payer")
		}
		result.Billing = &realqav1.BillingScope{
			OrganizationId: &realqav1.UuidV7{Value: organizationID.String()},
			TeamId:         &realqav1.UuidV7{Value: teamID.String()},
		}
	}
	if record.PresetID.Valid {
		presetID, parseErr := fromPGUUID(record.PresetID)
		if parseErr != nil {
			return nil, parseErr
		}
		result.PresetId = &realqav1.UuidV7{Value: presetID.String()}
	}
	if record.DestinationID.Valid {
		destination, destinationErr := queries.GetDestinationRecord(
			ctx, record.DestinationID)
		if destinationErr != nil {
			return nil, destinationErr
		}
		installationID, parseErr := fromPGUUID(destination.InstallationID)
		if parseErr != nil {
			return nil, parseErr
		}
		result.Destination = &realqav1.TrackerDestination{
			Tracker: realqav1.TrackerKind_TRACKER_KIND_GITHUB_COM,
			InstallationId: &realqav1.UuidV7{
				Value: installationID.String(),
			},
			Repository: &realqav1.GitHubRepositoryRef{
				RepositoryId: destination.RepositoryID,
				Owner:        destination.RepositoryOwner,
				Name:         destination.RepositoryName,
			},
		}
	}
	if record.ProviderIssueID.Valid && record.ProviderIssueUrl.Valid {
		result.ProviderIssue = &realqav1.ProviderIssueReference{
			Tracker:  realqav1.TrackerKind_TRACKER_KIND_GITHUB_COM,
			IssueId:  record.ProviderIssueID.String,
			IssueUrl: record.ProviderIssueUrl.String,
		}
	}
	authorization, authorizationErr :=
		queries.GetStorageAuthorizationAttempt(ctx, record.ID)
	if authorizationErr == nil && authorization.AuthorizationID.Valid &&
		authorization.MappingRevision > 0 {
		authorizationID, parseErr := fromPGUUID(
			authorization.AuthorizationID)
		if parseErr != nil {
			return nil, parseErr
		}
		result.StorageAuthorizationId = &realqav1.UuidV7{
			Value: authorizationID.String(),
		}
		result.StorageAuthorizationMappingRevision =
			revision(authorization.MappingRevision)
	} else if authorizationErr != nil &&
		!errors.Is(authorizationErr, pgx.ErrNoRows) {
		return nil, authorizationErr
	}
	recovery, err := storageRecoveryForSubmission(ctx, queries, record.ID)
	if err != nil {
		return nil, err
	}
	result.StorageBillingRecovery = recovery.message
	if result.StorageBillingRecovery != nil {
		result.FailureClass =
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED
		result.FailureReason =
			realqav1.ErrorReason_ERROR_REASON_STORAGE_BILLING_GRACE
	}
	assets, err := queries.ListSubmissionAssets(ctx, record.ID)
	if err != nil {
		return nil, err
	}
	result.Assets = make([]*realqav1.ImageAsset, 0, len(assets))
	for _, asset := range assets {
		result.Assets = append(result.Assets, updatedAssetProto(asset))
	}
	if err = markStorageRecoveryNotified(ctx, queries, recovery); err != nil {
		return nil, err
	}
	return result, nil
}

type storageRecoveryNotification struct {
	id      pgtype.UUID
	message *realqav1.StorageBillingRecovery
}

func storageRecoveryForSubmission(
	ctx context.Context,
	queries dbgen.Querier,
	submissionID pgtype.UUID,
) (storageRecoveryNotification, error) {
	recovery, err := queries.GetActiveStorageRecovery(ctx, submissionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return storageRecoveryNotification{}, nil
	}
	if err != nil {
		return storageRecoveryNotification{}, err
	}
	binding, err := queries.GetStorageAuthorizationBinding(
		ctx, recovery.AuthorizationID)
	if err != nil {
		return storageRecoveryNotification{}, err
	}
	result := &realqav1.StorageBillingRecovery{
		AuthorizationState: storageAuthorizationState(binding.Status),
		Reason:             storageRecoveryReasonProto(recovery.Reason),
		NotificationState: storageNotificationStateProto(
			recovery.NotificationState),
		GraceStartedAt: timestamp(recovery.GraceStartedAt),
		GraceExpiresAt: timestamp(recovery.GraceExpiresAt),
	}
	switch recovery.Reason {
	case "payment_required", "overage_required", "billing_unavailable":
		result.Actions = []realqav1.StorageRecoveryAction{
			realqav1.StorageRecoveryAction_STORAGE_RECOVERY_ACTION_PAYMENT,
			realqav1.StorageRecoveryAction_STORAGE_RECOVERY_ACTION_REBIND,
			realqav1.StorageRecoveryAction_STORAGE_RECOVERY_ACTION_REVOKE,
		}
	case "authorization_revoked", "authorization_access_lost":
		result.Actions = []realqav1.StorageRecoveryAction{
			realqav1.StorageRecoveryAction_STORAGE_RECOVERY_ACTION_REBIND,
		}
	default:
		result.Actions = []realqav1.StorageRecoveryAction{
			realqav1.StorageRecoveryAction_STORAGE_RECOVERY_ACTION_REBIND,
			realqav1.StorageRecoveryAction_STORAGE_RECOVERY_ACTION_REVOKE,
		}
	}
	return storageRecoveryNotification{id: recovery.ID, message: result}, nil
}

func markStorageRecoveryNotified(
	ctx context.Context,
	queries dbgen.Querier,
	recovery storageRecoveryNotification,
) error {
	if recovery.message == nil {
		return nil
	}
	notified, err := queries.MarkStorageRecoveryNotified(ctx, recovery.id)
	if err != nil {
		return err
	}
	recovery.message.NotificationState = storageNotificationStateProto(
		notified.NotificationState)
	return nil
}

func storageAuthorizationState(
	value string,
) realqav1.StorageAuthorizationState {
	switch value {
	case "active":
		return realqav1.StorageAuthorizationState_STORAGE_AUTHORIZATION_STATE_ACTIVE
	case "revoked":
		return realqav1.StorageAuthorizationState_STORAGE_AUTHORIZATION_STATE_REVOKED
	case "access_lost":
		return realqav1.StorageAuthorizationState_STORAGE_AUTHORIZATION_STATE_ACCESS_LOST
	case "resource_deleted":
		return realqav1.StorageAuthorizationState_STORAGE_AUTHORIZATION_STATE_RESOURCE_DELETED
	case "owner_deleted":
		return realqav1.StorageAuthorizationState_STORAGE_AUTHORIZATION_STATE_OWNER_DELETED
	default:
		return realqav1.StorageAuthorizationState_STORAGE_AUTHORIZATION_STATE_UNSPECIFIED
	}
}

func storageRecoveryReasonProto(
	value string,
) realqav1.StorageRecoveryReason {
	switch value {
	case "authorization_revoked":
		return realqav1.StorageRecoveryReason_STORAGE_RECOVERY_REASON_AUTHORIZATION_REVOKED
	case "authorization_access_lost":
		return realqav1.StorageRecoveryReason_STORAGE_RECOVERY_REASON_AUTHORIZATION_ACCESS_LOST
	case "payment_required":
		return realqav1.StorageRecoveryReason_STORAGE_RECOVERY_REASON_PAYMENT_REQUIRED
	case "overage_required":
		return realqav1.StorageRecoveryReason_STORAGE_RECOVERY_REASON_OVERAGE_REQUIRED
	case "billing_unavailable":
		return realqav1.StorageRecoveryReason_STORAGE_RECOVERY_REASON_BILLING_UNAVAILABLE
	case "github_disconnected":
		return realqav1.StorageRecoveryReason_STORAGE_RECOVERY_REASON_GITHUB_DISCONNECTED
	case "security_conflict":
		return realqav1.StorageRecoveryReason_STORAGE_RECOVERY_REASON_SECURITY_CONFLICT
	default:
		return realqav1.StorageRecoveryReason_STORAGE_RECOVERY_REASON_UNSPECIFIED
	}
}

func storageNotificationStateProto(
	value string,
) realqav1.StorageNotificationState {
	if value == "pending" {
		return realqav1.StorageNotificationState_STORAGE_NOTIFICATION_STATE_PENDING
	}
	if value == "notified" {
		return realqav1.StorageNotificationState_STORAGE_NOTIFICATION_STATE_NOTIFIED
	}
	return realqav1.StorageNotificationState_STORAGE_NOTIFICATION_STATE_UNSPECIFIED
}

func updatedAssetProto(asset dbgen.RealqaAsset) *realqav1.ImageAsset {
	id := uuid.UUID(asset.ID.Bytes)
	clientID := uuid.UUID(asset.ClientImageID.Bytes)
	checksum := asset.SourceSha256
	if len(asset.SanitizedSha256) == 32 &&
		asset.UploadState == "verified" {
		checksum = asset.SanitizedSha256
	}
	encodedBytes := asset.DeclaredEncodedBytes
	if asset.UploadState == "verified" {
		encodedBytes = asset.EncodedBytes
	}
	return &realqav1.ImageAsset{
		AssetId:       &realqav1.UuidV7{Value: id.String()},
		ClientImageId: &realqav1.UuidV7{Value: clientID.String()},
		MediaType:     mediaTypeProto(asset.MediaType), EncodedBytes: encodedBytes,
		PixelWidth: asset.PixelWidth, PixelHeight: asset.PixelHeight,
		Sha256:      hex.EncodeToString(checksum),
		UploadState: uploadState(asset.UploadState),
		AssetState:  assetState(asset.State), Revision: revision(asset.Revision),
		CreatedAt: timestamp(asset.CreatedAt), VerifiedAt: timestamp(asset.VerifiedAt),
		RemovedAt: timestamp(asset.RemovedAt),
	}
}

func declarationFromRecord(asset dbgen.RealqaAsset) imageassets.Declaration {
	return imageassets.Declaration{
		MediaType:    imageassets.MediaType(asset.MediaType),
		EncodedBytes: asset.DeclaredEncodedBytes,
		Width:        int(asset.PixelWidth), Height: int(asset.PixelHeight),
		SHA256: hex.EncodeToString(asset.SourceSha256),
	}
}

type objectKind string

const (
	objectKindStaging  objectKind = "staging"
	objectKindVerified objectKind = "verified"
	objectKindPublic   objectKind = "public"
)

func enqueueAssetObjectDeletions(
	ctx context.Context,
	queries *dbgen.Queries,
	asset dbgen.RealqaAsset,
) error {
	for _, kind := range []objectKind{objectKindStaging, objectKindVerified} {
		if err := enqueueObjectDeletion(
			ctx, queries, asset.ID, kind, pgtype.Text{}); err != nil {
			return err
		}
	}
	if asset.PublicID.Valid {
		return enqueueObjectDeletion(
			ctx, queries, asset.ID, objectKindPublic, asset.PublicID)
	}
	return nil
}

func enqueueObjectDeletion(
	ctx context.Context,
	queries *dbgen.Queries,
	assetID pgtype.UUID,
	kind objectKind,
	publicID pgtype.Text,
) error {
	return queries.EnqueueObjectDeletion(ctx, dbgen.EnqueueObjectDeletionParams{
		AssetID: assetID, ObjectKind: string(kind), PublicID: publicID,
	})
}

func (service *Submission) cleanupUnownedVerifiedObject(
	ctx context.Context,
	submissionID uuid.UUID,
	assetID uuid.UUID,
) error {
	current, err := service.dependencies.Store.Queries().GetAssetRecord(
		ctx, dbgen.GetAssetRecordParams{
			ID: toPGUUID(assetID), SubmissionID: toPGUUID(submissionID),
		})
	if err == nil && current.UploadState == "verified" &&
		current.State == "verified_unlinked" {
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err = service.dependencies.Store.Queries().EnqueueObjectDeletion(
		ctx, dbgen.EnqueueObjectDeletionParams{
			AssetID: toPGUUID(assetID), ObjectKind: string(objectKindVerified),
		}); err != nil {
		return err
	}
	service.drainObjectDeletionsBestEffort(ctx)
	return nil
}

func (service *Submission) drainObjectDeletionsBestEffort(ctx context.Context) {
	// Request paths get one short opportunistic attempt. The periodic cleanup
	// worker owns bulk retries so an R2 backlog cannot hold an RPC open.
	boundedCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := service.DrainObjectDeletions(boundedCtx, 1); err != nil {
		safelog.Record(ctx, service.dependencies.Logger, slog.LevelError,
			safelog.EventIntegration, safelog.Fields{
				Decision: safelog.DecisionDeny,
				Result:   safelog.ResultFailure,
			})
	}
}

func (service *Submission) DrainObjectDeletions(
	ctx context.Context,
	batchSize int32,
) (int, error) {
	if service.dependencies.Objects == nil {
		return 0, nil
	}
	if batchSize <= 0 || batchSize > 1000 {
		batchSize = 100
	}
	cutoff := pgTimestamp(service.dependencies.Clock.Now().UTC())
	jobs, err := service.dependencies.Store.Queries().ListPendingObjectDeletions(
		ctx, dbgen.ListPendingObjectDeletionsParams{
			Cutoff: cutoff, BatchLimit: batchSize,
		})
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, candidate := range jobs {
		deleted := false
		err = service.dependencies.Store.WithinTransaction(
			ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
				job, lockErr := queries.LockObjectDeletion(
					ctx, dbgen.LockObjectDeletionParams{
						AssetID: candidate.AssetID, ObjectKind: candidate.ObjectKind,
						PublicID: candidate.PublicID, Cutoff: cutoff,
					})
				if errors.Is(lockErr, pgx.ErrNoRows) {
					return nil
				}
				if lockErr != nil {
					return lockErr
				}
				assetID, parseErr := fromPGUUID(job.AssetID)
				if parseErr != nil {
					return parseErr
				}
				var key string
				switch objectKind(job.ObjectKind) {
				case objectKindStaging:
					key = imageassets.StagingObjectKey(assetID.String())
				case objectKindVerified:
					key = imageassets.VerifiedObjectKey(assetID.String())
				case objectKindPublic:
					if !job.PublicID.Valid {
						return errors.New(
							"realqa service: invalid public object deletion")
					}
					key = imageassets.PublicObjectKey(job.PublicID.String)
				default:
					return errors.New(
						"realqa service: invalid object deletion kind")
				}
				if deleteErr := service.dependencies.Objects.Delete(
					ctx, key); deleteErr != nil {
					safelog.Record(ctx, service.dependencies.Logger, slog.LevelError,
						safelog.EventIntegration, safelog.Fields{
							Decision: safelog.DecisionDeny,
							Result:   safelog.ResultFailure,
						})
					return queries.RetryObjectDeletion(
						ctx, dbgen.RetryObjectDeletionParams{
							AssetID: job.AssetID, ObjectKind: job.ObjectKind,
							PublicID: job.PublicID,
						})
				}
				if lockErr = queries.CompleteObjectDeletion(
					ctx, dbgen.CompleteObjectDeletionParams{
						AssetID: job.AssetID, ObjectKind: job.ObjectKind,
						PublicID: job.PublicID,
					}); lockErr != nil {
					return lockErr
				}
				deleted = true
				return nil
			})
		if err != nil {
			return completed, err
		}
		if deleted {
			completed++
		}
	}
	return completed, nil
}

func (service *Submission) bestEffortIssueUpdate(
	ctx context.Context,
	submission dbgen.RealqaSubmission,
	assets []dbgen.RealqaAsset,
) {
	if service.dependencies.IssueUpdater == nil ||
		!submission.ProviderIssueID.Valid {
		return
	}
	publicIDs := make([]string, 0, len(assets))
	for _, asset := range assets {
		if asset.PublicID.Valid {
			publicIDs = append(publicIDs, asset.PublicID.String)
		}
	}
	if len(publicIDs) == 0 {
		return
	}
	if err := service.dependencies.IssueUpdater.RemoveImageReferences(
		ctx, submission.ProviderIssueID.String, publicIDs); err != nil {
		safelog.Record(ctx, service.dependencies.Logger, slog.LevelWarn,
			safelog.EventIntegration, safelog.Fields{
				Decision: safelog.DecisionDeny, Result: safelog.ResultFailure,
			})
	}
}

func mediaTypeName(value realqav1.ImageMediaType) string {
	if value == realqav1.ImageMediaType_IMAGE_MEDIA_TYPE_WEBP {
		return string(imageassets.MediaTypeWebP)
	}
	return string(imageassets.MediaTypePNG)
}

func mediaTypeProto(value string) realqav1.ImageMediaType {
	if value == string(imageassets.MediaTypeWebP) {
		return realqav1.ImageMediaType_IMAGE_MEDIA_TYPE_WEBP
	}
	return realqav1.ImageMediaType_IMAGE_MEDIA_TYPE_PNG
}

func revision(value int64) *realqav1.Revision {
	return &realqav1.Revision{
		Value: value, Etag: `"realqa-r` + strconv.FormatInt(value, 10) + `"`,
	}
}

func pgTimestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: !value.IsZero()}
}

func submissionState(value string) realqav1.SubmissionState {
	return map[string]realqav1.SubmissionState{
		"draft":                 realqav1.SubmissionState_SUBMISSION_STATE_DRAFT,
		"uploading":             realqav1.SubmissionState_SUBMISSION_STATE_UPLOADING,
		"ready":                 realqav1.SubmissionState_SUBMISSION_STATE_READY,
		"submitting":            realqav1.SubmissionState_SUBMISSION_STATE_SUBMITTING,
		"reconciling":           realqav1.SubmissionState_SUBMISSION_STATE_RECONCILING,
		"submitted":             realqav1.SubmissionState_SUBMISSION_STATE_SUBMITTED,
		"failed":                realqav1.SubmissionState_SUBMISSION_STATE_FAILED,
		"storage_billing_grace": realqav1.SubmissionState_SUBMISSION_STATE_STORAGE_BILLING_GRACE,
		"assets_deleted":        realqav1.SubmissionState_SUBMISSION_STATE_ASSETS_DELETED,
		"deleted":               realqav1.SubmissionState_SUBMISSION_STATE_DELETED,
	}[value]
}

func uploadState(value string) realqav1.UploadState {
	return map[string]realqav1.UploadState{
		"declared":       realqav1.UploadState_UPLOAD_STATE_DECLARED,
		"put_authorized": realqav1.UploadState_UPLOAD_STATE_PUT_AUTHORIZED,
		"uploaded":       realqav1.UploadState_UPLOAD_STATE_UPLOADED,
		"verifying":      realqav1.UploadState_UPLOAD_STATE_VERIFYING,
		"verified":       realqav1.UploadState_UPLOAD_STATE_VERIFIED,
		"rejected":       realqav1.UploadState_UPLOAD_STATE_REJECTED,
		"expired":        realqav1.UploadState_UPLOAD_STATE_EXPIRED,
		"deleted":        realqav1.UploadState_UPLOAD_STATE_DELETED,
	}[value]
}

func assetState(value string) realqav1.AssetState {
	return map[string]realqav1.AssetState{
		"private_staging":     realqav1.AssetState_ASSET_STATE_PRIVATE_STAGING,
		"verified_unlinked":   realqav1.AssetState_ASSET_STATE_VERIFIED_UNLINKED,
		"public_retained":     realqav1.AssetState_ASSET_STATE_PUBLIC_RETAINED,
		"removed_placeholder": realqav1.AssetState_ASSET_STATE_REMOVED_PLACEHOLDER,
		"expired":             realqav1.AssetState_ASSET_STATE_EXPIRED,
		"deleted":             realqav1.AssetState_ASSET_STATE_DELETED,
	}[value]
}

func storageUnavailable() error {
	return rqerr.New(connect.CodeUnavailable,
		realqav1.ErrorReason_ERROR_REASON_TRANSFER_RESERVATION_FAILED,
		realqav1.FailureClass_FAILURE_CLASS_RETRYABLE, 0)
}

func verificationFailed() error {
	return rqerr.New(connect.CodeInvalidArgument,
		realqav1.ErrorReason_ERROR_REASON_UPLOAD_VERIFICATION_FAILED,
		realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0)
}

func verificationError(err error) error {
	switch {
	case errors.Is(err, imageassets.ErrMediaType):
		return invalid(
			realqav1.ErrorReason_ERROR_REASON_UNSUPPORTED_IMAGE_MEDIA_TYPE)
	case errors.Is(err, imageassets.ErrDecodedTooLarge):
		return invalid(
			realqav1.ErrorReason_ERROR_REASON_DECOMPRESSION_BOMB)
	case errors.Is(err, imageassets.ErrEncodedTooLarge):
		return invalid(realqav1.ErrorReason_ERROR_REASON_IMAGE_TOO_LARGE)
	case errors.Is(err, imageassets.ErrMalformed):
		return invalid(realqav1.ErrorReason_ERROR_REASON_MALFORMED_IMAGE)
	default:
		return verificationFailed()
	}
}
