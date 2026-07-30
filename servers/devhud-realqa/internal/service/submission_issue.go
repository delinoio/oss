package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	realqagithub "github.com/delinoio/oss/servers/devhud-realqa/internal/github"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/imageassets"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/rqerr"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const publicAssetURLPrefix = "https://assets.realqa.deli.dev/i/"

func (service *Submission) SubmitIssue(
	ctx context.Context,
	request *connect.Request[realqav1.SubmitIssueRequest],
) (*connect.Response[realqav1.SubmitIssueResponse], error) {
	if request == nil || request.Msg == nil || request.Msg.Issue == nil ||
		request.Msg.ExpectedSubmissionRevision == nil ||
		request.Msg.ExpectedSubmissionRevision.Value <= 0 {
		return nil, invalid(
			realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	if !request.Msg.Issue.PublicImageConfirmation {
		return nil, rqerr.New(
			connect.CodeFailedPrecondition,
			realqav1.ErrorReason_ERROR_REASON_PUBLIC_IMAGE_CONFIRMATION_REQUIRED,
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED,
			0,
		)
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
	clientKey, err := fromPGUUID(submission.ClientIdempotencyKey)
	if err != nil || clientKey != idempotencyID {
		return nil, idempotencyConflict()
	}
	attempt, replayed, err := service.claimIssueSubmission(
		ctx, actor, submissionID, idempotencyID, requestDigest,
		request.Msg.ExpectedSubmissionRevision.Value)
	if err != nil {
		return nil, err
	}
	if attempt.State == "completed" {
		return service.submitIssueResponse(ctx, submissionID, true, attempt)
	}
	if err = service.finalizeTransfer(
		ctx, actor, scope, submissionID, attempt); err != nil {
		return nil, err
	}
	orderedAssetIDs, assets, err := service.issueAssets(
		ctx, submissionID, request.Msg.Issue.OrderedAssetIds)
	if err != nil {
		return nil, err
	}
	if attempt.State == "provider_reconciled" ||
		attempt.State == "promoting" {
		return service.promoteAndCompleteIssue(
			ctx, actor, scope, submissionID, orderedAssetIDs, replayed)
	}
	if err = service.ensureStorageAuthorization(
		ctx, actor, scope, submissionID, assets); err != nil {
		return nil, err
	}
	input, finalBody, err := service.issueInput(
		ctx, actor, submissionID, submission, request.Msg.Issue,
		orderedAssetIDs, assets)
	if err != nil {
		return nil, err
	}
	bodyDigest := sha256.Sum256([]byte(finalBody))
	if err = service.checkpointProviderBody(
		ctx, submissionID, bodyDigest[:]); err != nil {
		return nil, err
	}
	_, err = service.createOrReconcileIssue(
		ctx, actor, submissionID, submission, input, finalBody)
	if err != nil {
		return nil, err
	}
	return service.promoteAndCompleteIssue(
		ctx, actor, scope, submissionID, orderedAssetIDs, replayed)
}

func (service *Submission) promoteAndCompleteIssue(
	ctx context.Context,
	actor caller,
	scope owner,
	submissionID uuid.UUID,
	orderedAssetIDs []uuid.UUID,
	replayed bool,
) (*connect.Response[realqav1.SubmitIssueResponse], error) {
	if _, err := service.dependencies.Store.Queries().MarkIssuePromoting(
		ctx, toPGUUID(submissionID)); err != nil &&
		!errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err := service.PromoteSubmittedAssets(
		ctx, submissionID, orderedAssetIDs); err != nil {
		return nil, err
	}
	completed, err := service.dependencies.Store.Queries().
		CompleteIssueSubmissionAttempt(ctx, toPGUUID(submissionID))
	if err != nil {
		if current, lookupErr := service.dependencies.Store.Queries().
			GetIssueSubmissionAttempt(
				ctx, toPGUUID(submissionID)); lookupErr != nil ||
			current.State != "completed" {
			return nil, err
		} else {
			completed = current
		}
	}
	audit(ctx, service.dependencies, actor, "submission_completed",
		scope, submissionID, "allow", "success")
	service.dependencies.Logger.InfoContext(
		ctx,
		"RealQA submission completed",
		"event", "submission_completed",
	)
	return service.submitIssueResponse(
		ctx, submissionID, replayed, completed)
}

func (service *Submission) claimIssueSubmission(
	ctx context.Context,
	actor caller,
	submissionID uuid.UUID,
	idempotencyID uuid.UUID,
	digest []byte,
	expectedRevision int64,
) (dbgen.RealqaIssueSubmissionAttempt, bool, error) {
	var (
		attempt  dbgen.RealqaIssueSubmissionAttempt
		replayed bool
	)
	err := service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
			record, lockErr := queries.LockSubmissionRecord(
				ctx, toPGUUID(submissionID))
			if lockErr != nil {
				return lockErr
			}
			existing, lookupErr := queries.GetIssueSubmissionAttempt(
				ctx, toPGUUID(submissionID))
			if lookupErr == nil {
				if existing.IdempotencyKey != toPGUUID(idempotencyID) ||
					!bytes.Equal(existing.RequestDigest, digest) {
					return idempotencyConflict()
				}
				if !service.dependencies.Clock.Now().UTC().Before(
					record.TransferReservationExpiresAt.Time) &&
					existing.State != "completed" {
					return uploadExpired()
				}
				attempt = existing
				replayed = true
				return nil
			}
			if !errors.Is(lookupErr, pgx.ErrNoRows) {
				return lookupErr
			}
			now := service.dependencies.Clock.Now().UTC()
			if !now.Before(record.UploadDeadline.Time) {
				return rqerr.New(
					connect.CodeDeadlineExceeded,
					realqav1.ErrorReason_ERROR_REASON_UPLOAD_DEADLINE_EXCEEDED,
					realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED,
					0,
				)
			}
			if record.Revision != expectedRevision {
				return stale(record.Revision)
			}
			if record.State != "ready" {
				return invalid(
					realqav1.ErrorReason_ERROR_REASON_SUBMISSION_STATE_CONFLICT)
			}
			if lockErr = queries.LockUploadSessionAccount(
				ctx, toPGUUID(actor.accountID)); lockErr != nil {
				return lockErr
			}
			recent, countErr := queries.CountRecentIssueSubmissionAttempts(
				ctx, toPGUUID(actor.accountID))
			if countErr != nil {
				return countErr
			}
			if recent >= submissionHourLimit {
				return rqerr.New(
					connect.CodeResourceExhausted,
					realqav1.ErrorReason_ERROR_REASON_RATE_LIMITED,
					realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED,
					0,
				)
			}
			attempt, lockErr = queries.CreateIssueSubmissionAttempt(
				ctx, dbgen.CreateIssueSubmissionAttemptParams{
					SubmissionID:   toPGUUID(submissionID),
					IdempotencyKey: toPGUUID(idempotencyID),
					RequestDigest:  digest,
				})
			return lockErr
		})
	if err != nil {
		return dbgen.RealqaIssueSubmissionAttempt{}, false, err
	}
	return attempt, replayed, nil
}

func (service *Submission) finalizeTransfer(
	ctx context.Context,
	actor caller,
	scope owner,
	submissionID uuid.UUID,
	attempt dbgen.RealqaIssueSubmissionAttempt,
) error {
	if attempt.State != "pending" {
		return nil
	}
	if service.dependencies.Billing == nil {
		_, err := service.dependencies.Store.Queries().
			MarkIssueTransferFinalized(ctx, toPGUUID(submissionID))
		return err
	}
	meters, err := service.dependencies.Billing.Meters(ctx)
	if err != nil || validateBillingMeters(meters) != nil {
		return transferReservationFailed()
	}
	forwardedBearer, ok := service.forwardedBearer(ctx)
	if !ok {
		return reauthenticationRequired()
	}
	finalizationEvent := ""
	err = service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
			record, lockErr := queries.LockSubmissionRecord(
				ctx, toPGUUID(submissionID))
			if lockErr != nil {
				return lockErr
			}
			current, lockErr := queries.GetIssueSubmissionAttempt(
				ctx, toPGUUID(submissionID))
			if lockErr != nil {
				return lockErr
			}
			if current.State != "pending" {
				return nil
			}
			if record.TransferState == "committed" ||
				record.TransferState == "released" {
				_, lockErr = queries.MarkIssueTransferFinalized(
					ctx, toPGUUID(submissionID))
				return lockErr
			}
			if record.TransferState != "reserved" ||
				!service.dependencies.Clock.Now().UTC().Before(
					record.TransferReservationExpiresAt.Time) {
				return uploadExpired()
			}
			organizationID, parseErr := fromPGUUID(
				record.PayerOrganizationID)
			if parseErr != nil {
				return transferReservationFailed()
			}
			reservationID, parseErr := fromPGUUID(
				record.TransferReservationID)
			if parseErr != nil {
				return transferReservationFailed()
			}
			verifiedBytes, sumErr := queries.SumVerifiedDeclaredAssetBytes(
				ctx, toPGUUID(submissionID))
			if sumErr != nil {
				return sumErr
			}
			actualUnits, sumErr := ceilMiB(verifiedBytes)
			if sumErr != nil ||
				actualUnits > record.TransferReservedUnits {
				return transferReservationFailed()
			}
			if actualUnits == 0 {
				releaseKey, parseErr := fromPGUUID(
					record.TransferReleaseIdempotencyKey)
				if parseErr != nil {
					return transferReservationFailed()
				}
				finalized, releaseErr :=
					service.dependencies.Billing.ReleaseTransfer(
						ctx, TransferReleaseRequest{
							OrganizationID:  organizationID,
							ReservationID:   reservationID,
							IdempotencyKey:  releaseKey,
							ForwardedBearer: forwardedBearer,
						})
				if releaseErr != nil ||
					validateFinalTransfer(
						finalized, record, meters, "released", 0) != nil {
					return transferReservationFailed()
				}
				if _, releaseErr = queries.MarkTransferReleased(
					ctx, toPGUUID(submissionID)); releaseErr != nil {
					return releaseErr
				}
				finalizationEvent = "transfer_released"
			} else {
				commitKey, parseErr := fromPGUUID(
					record.TransferCommitIdempotencyKey)
				if parseErr != nil {
					return transferReservationFailed()
				}
				finalized, commitErr :=
					service.dependencies.Billing.CommitTransfer(
						ctx, TransferCommitRequest{
							OrganizationID:  organizationID,
							ReservationID:   reservationID,
							ActualUnits:     actualUnits,
							IdempotencyKey:  commitKey,
							ForwardedBearer: forwardedBearer,
						})
				if commitErr != nil ||
					validateFinalTransfer(
						finalized, record, meters, "committed",
						actualUnits) != nil {
					return transferReservationFailed()
				}
				if _, commitErr = queries.MarkTransferCommitted(
					ctx, dbgen.MarkTransferCommittedParams{
						CommittedUnits: actualUnits,
						ID:             toPGUUID(submissionID),
					}); commitErr != nil {
					return commitErr
				}
				finalizationEvent = "transfer_committed"
			}
			_, lockErr = queries.MarkIssueTransferFinalized(
				ctx, toPGUUID(submissionID))
			return lockErr
		})
	if err != nil {
		return err
	}
	if finalizationEvent != "" {
		audit(ctx, service.dependencies, actor, finalizationEvent,
			scope, submissionID, "allow", "success")
	}
	return nil
}

func validateFinalTransfer(
	finalized TransferReservation,
	record dbgen.RealqaSubmission,
	meters BillingMeters,
	status string,
	committedUnits int64,
) error {
	reservationID, err := fromPGUUID(record.TransferReservationID)
	organizationID, organizationErr := fromPGUUID(
		record.PayerOrganizationID)
	teamID, teamErr := fromPGUUID(record.PayerTeamID)
	accountID, accountErr := fromPGUUID(record.CreatedByAccountID)
	submissionID, submissionErr := fromPGUUID(record.ID)
	if err != nil || organizationErr != nil || teamErr != nil ||
		accountErr != nil || submissionErr != nil ||
		finalized.ID != reservationID ||
		finalized.OrganizationID != organizationID ||
		finalized.TeamID != teamID ||
		finalized.MeterID != meters.Transfer.ID ||
		finalized.PriceVersionID != meters.Transfer.PriceVersionID ||
		finalized.UserAccountID != accountID ||
		finalized.ServiceIdentityID != meters.Transfer.ServiceIdentityID ||
		finalized.MaximumUnits != record.TransferReservedUnits ||
		finalized.USDMicrosPerUnit != transferPriceUSDMicros ||
		finalized.Status != status ||
		finalized.CommittedUnits != committedUnits ||
		finalized.ClientReference != "realqa-transfer:"+
			submissionID.String() ||
		!finalized.CreatedAt.Truncate(time.Microsecond).Equal(
			record.TransferReservationCreatedAt.Time) ||
		!finalized.ExpiresAt.Truncate(time.Microsecond).Equal(
			record.TransferReservationExpiresAt.Time) {
		return transferReservationFailed()
	}
	return nil
}

func (service *Submission) issueAssets(
	ctx context.Context,
	submissionID uuid.UUID,
	messages []*realqav1.UuidV7,
) ([]uuid.UUID, []dbgen.RealqaAsset, error) {
	ids := make([]uuid.UUID, 0, len(messages))
	pgIDs := make([]pgtype.UUID, 0, len(messages))
	seen := make(map[uuid.UUID]struct{}, len(messages))
	for _, message := range messages {
		id, err := parseUUIDMessage(message)
		if err != nil {
			return nil, nil, retentionStateConflict()
		}
		if _, exists := seen[id]; exists {
			return nil, nil, retentionStateConflict()
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
		pgIDs = append(pgIDs, toPGUUID(id))
	}
	if len(ids) == 0 {
		return ids, []dbgen.RealqaAsset{}, nil
	}
	found, err := service.dependencies.Store.Queries().
		ListIssueSubmissionAssets(
			ctx, dbgen.ListIssueSubmissionAssetsParams{
				SubmissionID: toPGUUID(submissionID),
				AssetIds:     pgIDs,
			})
	if err != nil || len(found) != len(ids) {
		return nil, nil, retentionStateConflict()
	}
	byID := make(map[uuid.UUID]dbgen.RealqaAsset, len(found))
	for _, asset := range found {
		id, parseErr := fromPGUUID(asset.ID)
		if parseErr != nil || asset.UploadState != "verified" ||
			(asset.State != "verified_unlinked" &&
				asset.State != "public_retained") {
			return nil, nil, retentionStateConflict()
		}
		byID[id] = asset
	}
	ordered := make([]dbgen.RealqaAsset, 0, len(ids))
	for _, id := range ids {
		asset, ok := byID[id]
		if !ok {
			return nil, nil, retentionStateConflict()
		}
		ordered = append(ordered, asset)
	}
	return ids, ordered, nil
}

func (service *Submission) ensureStorageAuthorization(
	ctx context.Context,
	actor caller,
	scope owner,
	submissionID uuid.UUID,
	assets []dbgen.RealqaAsset,
) error {
	if service.dependencies.Billing == nil {
		return nil
	}
	var retainedBytes int64
	for _, asset := range assets {
		if asset.EncodedBytes <= 0 ||
			retainedBytes > math.MaxInt64-asset.EncodedBytes {
			return storageAuthorizationFailed()
		}
		retainedBytes += asset.EncodedBytes
	}
	maximumUnits, err := ceilMiB(retainedBytes)
	if err != nil {
		return storageAuthorizationFailed()
	}
	if maximumUnits == 0 {
		return nil
	}
	submission, err := service.dependencies.Store.Queries().
		GetSubmissionRecord(ctx, toPGUUID(submissionID))
	if err != nil {
		return err
	}
	organizationID, err := fromPGUUID(submission.PayerOrganizationID)
	if err != nil {
		return storageAuthorizationFailed()
	}
	teamID, err := fromPGUUID(submission.PayerTeamID)
	if err != nil {
		return storageAuthorizationFailed()
	}
	meters, err := service.dependencies.Billing.Meters(ctx)
	if err != nil || validateBillingMeters(meters) != nil {
		return storageAuthorizationFailed()
	}
	forwardedBearer, ok := service.forwardedBearer(ctx)
	if !ok {
		return reauthenticationRequired()
	}
	idempotencyKey, err := derivedUUIDv7(
		submissionID, "storage-authorization")
	if err != nil {
		return err
	}
	requestMaterial := []byte(
		"realqa-storage-authorization:v1:" +
			scope.kind + ":" + scope.id.String() + ":" +
			organizationID.String() + ":" + teamID.String() + ":" +
			meters.Storage.ServiceIdentityID.String() + ":" +
			meters.Storage.ID.String() + ":" +
			meters.Storage.PriceVersionID.String() + ":" +
			"REALQA_STORAGE:UTC_DAY:" +
			strconv.FormatInt(maximumUnits, 10))
	requestDigest := sha256.Sum256(requestMaterial)
	existing, err := service.dependencies.Store.Queries().
		GetStorageAuthorizationAttempt(ctx, toPGUUID(submissionID))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, err = service.dependencies.Store.Queries().
			CreateStorageAuthorizationAttempt(
				ctx, dbgen.CreateStorageAuthorizationAttemptParams{
					SubmissionID:      toPGUUID(submissionID),
					IdempotencyKey:    toPGUUID(idempotencyKey),
					RequestDigest:     requestDigest[:],
					ServiceIdentityID: toPGUUID(meters.Storage.ServiceIdentityID),
					MeterID:           toPGUUID(meters.Storage.ID),
					MaximumUnits:      maximumUnits,
				})
		if errors.Is(err, pgx.ErrNoRows) {
			existing, err = service.dependencies.Store.Queries().
				GetStorageAuthorizationAttempt(
					ctx, toPGUUID(submissionID))
		}
	}
	if err != nil {
		return err
	}
	if existing.IdempotencyKey != toPGUUID(idempotencyKey) ||
		!bytes.Equal(existing.RequestDigest, requestDigest[:]) ||
		existing.ServiceIdentityID !=
			toPGUUID(meters.Storage.ServiceIdentityID) ||
		existing.MeterID != toPGUUID(meters.Storage.ID) ||
		existing.MaximumUnits != maximumUnits {
		return rqerr.New(
			connect.CodeFailedPrecondition,
			realqav1.ErrorReason_ERROR_REASON_STORAGE_AUTHORIZATION_SUBSTITUTION,
			realqav1.FailureClass_FAILURE_CLASS_CONFLICT,
			0,
		)
	}
	if existing.State == "active" {
		return nil
	}
	return service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
			submission, lockErr := queries.LockSubmissionRecord(
				ctx, toPGUUID(submissionID))
			if lockErr != nil {
				return lockErr
			}
			existing, lookupErr := queries.GetStorageAuthorizationAttempt(
				ctx, toPGUUID(submissionID))
			if lookupErr != nil {
				return lookupErr
			}
			if existing.State == "active" {
				return nil
			}
			if existing.State != "pending" {
				return storageAuthorizationFailed()
			}
			organizationID, parseErr := fromPGUUID(
				submission.PayerOrganizationID)
			if parseErr != nil {
				return storageAuthorizationFailed()
			}
			teamID, parseErr := fromPGUUID(submission.PayerTeamID)
			if parseErr != nil {
				return storageAuthorizationFailed()
			}
			authorizationRequest := StorageAuthorizationRequest{
				OwnerKind:         scope.kind,
				OwnerID:           scope.id,
				OrganizationID:    organizationID,
				TeamID:            teamID,
				ServiceIdentityID: meters.Storage.ServiceIdentityID,
				MeterID:           meters.Storage.ID,
				FeatureResourceID: submissionID,
				MaximumUnits:      maximumUnits,
				IdempotencyKey:    idempotencyKey,
				ForwardedBearer:   forwardedBearer,
			}
			authorization, createErr :=
				service.dependencies.Billing.CreateStorageAuthorization(
					ctx, authorizationRequest)
			if createErr != nil ||
				validateStorageAuthorization(
					authorization, authorizationRequest,
					actor.accountID) != nil {
				return storageAuthorizationFailed()
			}
			completed, createErr := queries.CompleteStorageAuthorizationAttempt(
				ctx, dbgen.CompleteStorageAuthorizationAttemptParams{
					AuthorizationID: toPGUUID(authorization.ID),
					AuthorizationRevision: pgtype.Int8{
						Int64: authorization.Revision, Valid: true,
					},
					SubmissionID: toPGUUID(submissionID),
				})
			if createErr != nil {
				return createErr
			}
			_, createErr = queries.CreateStorageAuthorizationBinding(
				ctx, dbgen.CreateStorageAuthorizationBindingParams{
					AuthorizationID:     toPGUUID(authorization.ID),
					SubmissionID:        toPGUUID(submissionID),
					MappingRevision:     completed.MappingRevision,
					AuthorizerAccountID: toPGUUID(actor.accountID),
					OwnerKind:           scope.kind,
					OwnerID:             toPGUUID(scope.id),
					OrganizationID:      toPGUUID(organizationID),
					TeamID:              toPGUUID(teamID),
					ServiceIdentityID: toPGUUID(
						authorization.ServiceIdentityID),
					MeterID:               toPGUUID(authorization.MeterID),
					MaximumUnits:          authorization.MaximumUnits,
					Status:                authorization.Status,
					AuthorizationRevision: authorization.Revision,
				})
			return createErr
		})
}

func (service *Submission) issueInput(
	ctx context.Context,
	actor caller,
	submissionID uuid.UUID,
	submission dbgen.RealqaSubmission,
	message *realqav1.IssueSubmission,
	orderedIDs []uuid.UUID,
	assets []dbgen.RealqaAsset,
) (realqagithub.IssueInput, string, error) {
	if service.dependencies.GitHubProvider == nil ||
		service.dependencies.GitHubIssues == nil ||
		submission.DestinationID.Valid == false ||
		submission.PresetID.Valid == false {
		return realqagithub.IssueInput{}, "", providerUnavailable()
	}
	presetID, err := fromPGUUID(submission.PresetID)
	if err != nil {
		return realqagithub.IssueInput{}, "", providerUnavailable()
	}
	preset, err := NewPreset(service.dependencies).loadPreset(ctx, presetID)
	if err != nil || preset.IssueDefinition == nil ||
		message.RepositoryResponse == nil ||
		!sameDefinition(
			preset.IssueDefinition,
			message.RepositoryResponse.Definition) {
		return realqagithub.IssueInput{}, "", providerSchemaConflict()
	}
	installationID, err := parseUUIDMessage(
		preset.Destination.InstallationId)
	if err != nil {
		return realqagithub.IssueInput{}, "", providerUnavailable()
	}
	repository := realqagithub.Repository{
		ID: parseProviderRepositoryID(
			preset.Destination.Repository.RepositoryId),
		NodeID:              "R_" + preset.Destination.Repository.RepositoryId,
		Owner:               preset.Destination.Repository.Owner,
		Name:                preset.Destination.Repository.Name,
		IssuesEnabled:       true,
		CanSubmit:           true,
		CanSetIssueMetadata: true,
	}
	definitions, err :=
		service.dependencies.GitHubProvider.GetRepositoryDefinitions(
			ctx, actor.accountID, installationID, repository)
	if err != nil {
		return realqagithub.IssueInput{}, "", providerUnavailable()
	}
	response, issueType, err := renderRepositoryResponse(
		preset.IssueDefinition, message.RepositoryResponse, definitions)
	if err != nil {
		return realqagithub.IssueInput{}, "", providerSchemaConflict()
	}
	images := make([]realqagithub.InlineImage, 0, len(assets))
	for index, asset := range assets {
		publicID := asset.PublicID.String
		if !asset.PublicID.Valid {
			publicID, err = imageassets.NewPublicID()
			if err != nil {
				return realqagithub.IssueInput{}, "", storageUnavailable()
			}
			asset, err = service.dependencies.Store.Queries().
				ReserveAssetPublicID(
					ctx, dbgen.ReserveAssetPublicIDParams{
						PublicID: pgtype.Text{
							String: publicID, Valid: true,
						},
						ID:           toPGUUID(orderedIDs[index]),
						SubmissionID: toPGUUID(submissionID),
					})
			if err != nil {
				return realqagithub.IssueInput{}, "", storageUnavailable()
			}
			publicID = asset.PublicID.String
		}
		images = append(images, realqagithub.InlineImage{
			AltText: "RealQA capture " + strconv.Itoa(index+1),
			URL:     publicAssetURLPrefix + publicID,
		})
	}
	input := realqagithub.IssueInput{
		SubmissionID:       submissionID,
		Title:              message.Title,
		IssueType:          issueType,
		RepositoryResponse: response,
		Capture:            captureInput(message.Capture),
		Images:             images,
		Labels:             make([]realqagithub.Label, 0, len(message.Labels)),
		Assignees: make(
			[]realqagithub.Assignee, 0, len(message.Assignees)),
		Extension: extensionInput(
			message.ProviderExtension,
			service.dependencies.GitHubProjectPermission),
	}
	for _, value := range message.Labels {
		input.Labels = append(
			input.Labels, realqagithub.Label{Name: value})
	}
	for _, value := range message.Assignees {
		input.Assignees = append(
			input.Assignees, realqagithub.Assignee{Login: value})
	}
	body, err := realqagithub.ComposeBody(input)
	if err != nil {
		if strings.Contains(err.Error(), "exceeds 60,000") {
			return realqagithub.IssueInput{}, "", finalBodyTooLarge()
		}
		return realqagithub.IssueInput{}, "", providerValidationFailed()
	}
	return input, body, nil
}

func (service *Submission) checkpointProviderBody(
	ctx context.Context,
	submissionID uuid.UUID,
	digest []byte,
) error {
	_, err := service.dependencies.Store.Queries().MarkIssueProviderPending(
		ctx, dbgen.MarkIssueProviderPendingParams{
			FinalBodyDigest: digest,
			SubmissionID:    toPGUUID(submissionID),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		current, lookupErr := service.dependencies.Store.Queries().
			GetIssueSubmissionAttempt(ctx, toPGUUID(submissionID))
		if lookupErr != nil {
			return lookupErr
		}
		if current.State == "provider_reconciled" ||
			current.State == "promoting" ||
			current.State == "completed" {
			if !bytes.Equal(current.FinalBodyDigest, digest) {
				return idempotencyConflict()
			}
			return nil
		}
	}
	return err
}

func (service *Submission) createOrReconcileIssue(
	ctx context.Context,
	actor caller,
	submissionID uuid.UUID,
	submission dbgen.RealqaSubmission,
	input realqagithub.IssueInput,
	finalBody string,
) (realqagithub.Issue, error) {
	var result realqagithub.Issue
	err := service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
			record, lockErr := queries.LockSubmissionRecord(
				ctx, toPGUUID(submissionID))
			if lockErr != nil {
				return lockErr
			}
			attempt, lockErr := queries.GetIssueSubmissionAttempt(
				ctx, toPGUUID(submissionID))
			if lockErr != nil {
				return lockErr
			}
			if attempt.ProviderIssueID.Valid &&
				attempt.ProviderIssueUrl.Valid {
				result = realqagithub.Issue{
					ID: parseProviderIssueID(
						attempt.ProviderIssueID.String),
					URL:  attempt.ProviderIssueUrl.String,
					Body: finalBody,
				}
				return nil
			}
			if _, lockErr = queries.SetSubmissionSubmitting(
				ctx, toPGUUID(submissionID)); lockErr != nil &&
				record.State != "submitting" {
				return lockErr
			}
			destination, loadErr := queries.GetDestinationRecord(
				ctx, submission.DestinationID)
			if loadErr != nil {
				return providerUnavailable()
			}
			installationID, loadErr := fromPGUUID(
				destination.InstallationID)
			if loadErr != nil {
				return providerUnavailable()
			}
			repository := realqagithub.Repository{
				ID:                  parseProviderRepositoryID(destination.RepositoryID),
				NodeID:              "R_" + destination.RepositoryID,
				Owner:               destination.RepositoryOwner,
				Name:                destination.RepositoryName,
				IssuesEnabled:       true,
				CanSubmit:           true,
				CanSetIssueMetadata: true,
			}
			result, loadErr = service.dependencies.GitHubIssues.CreateIssue(
				ctx, actor.accountID, installationID, repository, input)
			if loadErr != nil {
				switch {
				case errors.Is(
					loadErr, realqagithub.ErrAmbiguousCreate):
					return ambiguousProviderResult()
				case errors.Is(
					loadErr,
					realqagithub.ErrCallerAuthorizationUnavailable):
					return reauthenticationRequired()
				case errors.Is(
					loadErr, realqagithub.ErrProviderRejected),
					errors.Is(
						loadErr,
						realqagithub.ErrRepositorySubmissionUnavailable):
					return providerValidationFailed()
				}
				return providerUnavailable()
			}
			if result.ID <= 0 || result.URL == "" ||
				result.Body != finalBody {
				return ambiguousProviderResult()
			}
			_, loadErr = queries.MarkIssueProviderReconciled(
				ctx, dbgen.MarkIssueProviderReconciledParams{
					ProviderIssueID: pgtype.Text{
						String: strconv.FormatInt(result.ID, 10), Valid: true,
					},
					ProviderIssueUrl: pgtype.Text{
						String: result.URL, Valid: true,
					},
					SubmissionID: toPGUUID(submissionID),
				})
			return loadErr
		})
	if err != nil {
		return realqagithub.Issue{}, err
	}
	return result, nil
}

func (service *Submission) submitIssueResponse(
	ctx context.Context,
	submissionID uuid.UUID,
	replayed bool,
	attempt dbgen.RealqaIssueSubmissionAttempt,
) (*connect.Response[realqav1.SubmitIssueResponse], error) {
	submission, err := service.loadSubmission(ctx, submissionID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&realqav1.SubmitIssueResponse{
		Submission: submission,
		Idempotency: &realqav1.IdempotencyResult{
			Replayed:              replayed,
			Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_SUBMIT_ISSUE,
			OriginallyCompletedAt: timestamp(attempt.CompletedAt),
		},
	}), nil
}

func renderRepositoryResponse(
	selected *realqav1.RepositoryIssueDefinitionRef,
	response *realqav1.RepositoryIssueResponse,
	definitions realqagithub.RepositoryDefinitions,
) (string, string, error) {
	switch selected.Kind {
	case realqav1.RepositoryIssueDefinitionKind_REPOSITORY_ISSUE_DEFINITION_KIND_MARKDOWN_TEMPLATE:
		if len(response.FormAnswers) != 0 ||
			!utf8.ValidString(response.MarkdownBody) ||
			strings.Contains(response.MarkdownBody, "\x00") {
			return "", "", errors.New("invalid Markdown response")
		}
		for _, definition := range definitions.Markdown {
			if definitionMatches(selected, definition.Definition) {
				return response.MarkdownBody, definition.IssueType, nil
			}
		}
	case realqav1.RepositoryIssueDefinitionKind_REPOSITORY_ISSUE_DEFINITION_KIND_ISSUE_FORM:
		if response.MarkdownBody != "" {
			return "", "", errors.New("unexpected Markdown response")
		}
		answers := make(
			[]realqagithub.FormAnswer, 0, len(response.FormAnswers))
		for _, answer := range response.FormAnswers {
			if answer == nil {
				return "", "", errors.New("invalid form answer")
			}
			answers = append(answers, realqagithub.FormAnswer{
				FieldID: answer.FieldId,
				Values:  append([]string(nil), answer.Values...),
			})
		}
		for _, definition := range definitions.Forms {
			if definitionMatches(selected, definition.Definition) {
				rendered, err := realqagithub.RenderIssueForm(
					definition, answers)
				return rendered, definition.IssueType, err
			}
		}
	}
	return "", "", errors.New("repository definition changed")
}

func sameDefinition(
	left *realqav1.RepositoryIssueDefinitionRef,
	right *realqav1.RepositoryIssueDefinitionRef,
) bool {
	return left != nil && right != nil &&
		left.Kind == right.Kind &&
		left.DefinitionId == right.DefinitionId &&
		left.Path == right.Path &&
		left.Etag == right.Etag
}

func definitionMatches(
	selected *realqav1.RepositoryIssueDefinitionRef,
	current realqagithub.DefinitionRef,
) bool {
	return current.ID == selected.DefinitionId &&
		current.Path == selected.Path && current.ETag == selected.Etag
}

func captureInput(
	value *realqav1.CaptureMetadata,
) realqagithub.CaptureMetadata {
	if value == nil {
		return realqagithub.CaptureMetadata{}
	}
	result := realqagithub.CaptureMetadata{
		SanitizedURL: value.SanitizedUrl,
		Environment: make(
			[]realqagithub.CaptureField, 0, len(value.Environment)),
	}
	for _, field := range value.Environment {
		if field != nil {
			result.Environment = append(
				result.Environment,
				realqagithub.CaptureField{
					Key: field.Key, Value: field.Value,
				})
		}
	}
	if value.DomSelection != nil {
		result.DOM = &realqagithub.DOMMetadata{
			CSSSelector:    value.DomSelection.CssSelector,
			Tag:            value.DomSelection.Tag,
			Role:           value.DomSelection.Role,
			AccessibleName: value.DomSelection.AccessibleName,
			ViewportWidth:  value.DomSelection.ViewportWidth,
			ViewportHeight: value.DomSelection.ViewportHeight,
		}
	}
	return result
}

func extensionInput(
	value *realqav1.ProviderExtension,
	permission realqagithub.ProjectPermission,
) realqagithub.ProviderExtension {
	result := realqagithub.ProviderExtension{}
	if value == nil || value.GetGithub() == nil {
		return result
	}
	extension := value.GetGithub()
	if extension.MilestoneNumber > 0 {
		result.Milestone = &realqagithub.Milestone{
			Number: extension.MilestoneNumber,
		}
	}
	for _, project := range extension.ProjectNodeIds {
		result.Projects = append(result.Projects, realqagithub.Project{
			NodeID: project, Permission: permission,
		})
	}
	return result
}

func parseProviderRepositoryID(value string) int64 {
	result, _ := strconv.ParseInt(value, 10, 64)
	return result
}

func parseProviderIssueID(value string) int64 {
	result, _ := strconv.ParseInt(value, 10, 64)
	return result
}

func providerUnavailable() error {
	return rqerr.New(
		connect.CodeUnavailable,
		realqav1.ErrorReason_ERROR_REASON_GITHUB_DISCONNECTED,
		realqav1.FailureClass_FAILURE_CLASS_RETRYABLE,
		0,
	)
}

func providerSchemaConflict() error {
	return rqerr.New(
		connect.CodeFailedPrecondition,
		realqav1.ErrorReason_ERROR_REASON_PROVIDER_SCHEMA_INVALID,
		realqav1.FailureClass_FAILURE_CLASS_CONFLICT,
		0,
	)
}

func providerValidationFailed() error {
	return invalid(
		realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
}

func finalBodyTooLarge() error {
	return rqerr.New(
		connect.CodeResourceExhausted,
		realqav1.ErrorReason_ERROR_REASON_FINAL_BODY_TOO_LARGE,
		realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED,
		0,
	)
}

func ambiguousProviderResult() error {
	return rqerr.New(
		connect.CodeUnavailable,
		realqav1.ErrorReason_ERROR_REASON_AMBIGUOUS_PROVIDER_RESULT,
		realqav1.FailureClass_FAILURE_CLASS_RETRYABLE,
		0,
	)
}

func reauthenticationRequired() error {
	return rqerr.New(
		connect.CodeUnauthenticated,
		realqav1.ErrorReason_ERROR_REASON_REAUTHENTICATION_REQUIRED,
		realqav1.FailureClass_FAILURE_CLASS_REAUTHENTICATION_REQUIRED,
		0,
	)
}

func uploadExpired() error {
	return rqerr.New(
		connect.CodeDeadlineExceeded,
		realqav1.ErrorReason_ERROR_REASON_UPLOAD_EXPIRED,
		realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED,
		0,
	)
}
