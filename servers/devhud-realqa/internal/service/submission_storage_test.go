package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestValidateImagesRejectsEmptyDeclarationSet(t *testing.T) {
	requireServiceError(t, validateImages(nil), connect.CodeInvalidArgument,
		realqav1.ErrorReason_ERROR_REASON_MALFORMED_IMAGE,
		realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED)
}

func TestValidateImagesRejectsDuplicateClientIDs(t *testing.T) {
	clientID := &realqav1.UuidV7{Value: uuidv7.MustNew().String()}
	image := &realqav1.ImageDeclaration{
		ClientImageId: clientID,
		MediaType:     realqav1.ImageMediaType_IMAGE_MEDIA_TYPE_PNG,
		EncodedBytes:  1,
		PixelWidth:    1,
		PixelHeight:   1,
		Sha256:        strings.Repeat("0", 64),
	}
	requireServiceError(t, validateImages([]*realqav1.ImageDeclaration{
		image, image,
	}), connect.CodeInvalidArgument,
		realqav1.ErrorReason_ERROR_REASON_MALFORMED_IMAGE,
		realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED)
}

func TestReadPromotionBodyClassifiesFailures(t *testing.T) {
	t.Run("stream failure is retryable", func(t *testing.T) {
		_, err := readPromotionBody(io.NopCloser(failingReader{}), 1)
		requireServiceError(t, err, connect.CodeUnavailable,
			realqav1.ErrorReason_ERROR_REASON_TRANSFER_RESERVATION_FAILED,
			realqav1.FailureClass_FAILURE_CLASS_RETRYABLE)
	})
	t.Run("length mismatch requires user action", func(t *testing.T) {
		_, err := readPromotionBody(
			io.NopCloser(strings.NewReader("short")), 6)
		requireServiceError(t, err, connect.CodeInvalidArgument,
			realqav1.ErrorReason_ERROR_REASON_UPLOAD_VERIFICATION_FAILED,
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED)
	})
}

func TestValidatePromotionCandidate(t *testing.T) {
	transient := errors.New("transient lookup failure")
	if err := validatePromotionCandidate(
		dbgen.RealqaAsset{}, transient,
	); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("transient lookup error = %v", err)
	}
	if err := validatePromotionCandidate(
		dbgen.RealqaAsset{}, pgx.ErrNoRows,
	); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("missing asset error = %v", err)
	}
	if err := validatePromotionCandidate(
		dbgen.RealqaAsset{UploadState: "uploaded"}, nil,
	); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("non-verified asset error = %v", err)
	}
	if err := validatePromotionCandidate(
		dbgen.RealqaAsset{UploadState: "verified"}, nil,
	); err != nil {
		t.Fatalf("verified asset error = %v", err)
	}
}

func TestAssetOwnsPublicObject(t *testing.T) {
	asset := dbgen.RealqaAsset{
		UploadState: "verified",
		State:       "public_retained",
		PublicID:    pgtype.Text{String: "owned-public-id", Valid: true},
	}
	if !assetOwnsPublicObject(asset, "owned-public-id") {
		t.Fatal("matching retained public object was not owned")
	}
	if assetOwnsPublicObject(asset, "other-public-id") {
		t.Fatal("different public object was treated as owned")
	}
}

func TestTerminalAssetStates(t *testing.T) {
	for _, state := range []string{
		"removed_placeholder", "deleted", "expired",
	} {
		if !isTerminalAssetState(state) {
			t.Errorf("state %q was not terminal", state)
		}
	}
	if isTerminalAssetState("public_retained") {
		t.Fatal("retained asset was terminal")
	}
}

func TestLoadSubmissionMarksRecoveryAfterResponseAssembly(t *testing.T) {
	now := time.Now().UTC()
	submissionID := toPGUUID(uuidv7.MustNew())
	ownerID := toPGUUID(uuidv7.MustNew())
	authorizationID := toPGUUID(uuidv7.MustNew())
	queries := &storageRecoveryQuerier{
		recovery: dbgen.RealqaStorageRecovery{
			ID:                toPGUUID(uuidv7.MustNew()),
			SubmissionID:      submissionID,
			AuthorizationID:   authorizationID,
			Reason:            "payment_required",
			NotificationState: "pending",
			GraceStartedAt:    pgTimestamp(now),
			GraceExpiresAt:    pgTimestamp(now.Add(30 * 24 * time.Hour)),
		},
		binding: dbgen.RealqaStorageAuthorizationBinding{
			AuthorizationID: authorizationID,
			Status:          "active",
		},
		assetsErr: errors.New("asset read failed"),
	}
	record := dbgen.RealqaSubmission{
		ID:        submissionID,
		OwnerKind: "personal",
		OwnerID:   ownerID,
		State:     "storage_billing_grace",
	}

	if _, err := loadSubmissionWithRecord(
		context.Background(), queries, record,
	); !errors.Is(err, queries.assetsErr) {
		t.Fatalf("load error = %v, want asset read failure", err)
	}
	if queries.marked {
		t.Fatal("recovery was marked notified before response assembly failed")
	}

	queries.assetsErr = nil
	submission, err := loadSubmissionWithRecord(
		context.Background(), queries, record)
	if err != nil {
		t.Fatalf("load completed response: %v", err)
	}
	if !queries.marked {
		t.Fatal("completed response did not mark recovery notified")
	}
	if submission.StorageBillingRecovery == nil ||
		submission.StorageBillingRecovery.NotificationState !=
			realqav1.StorageNotificationState_STORAGE_NOTIFICATION_STATE_NOTIFIED {
		t.Fatalf("notification state = %v, want notified",
			submission.GetStorageBillingRecovery().GetNotificationState())
	}
}

type storageRecoveryQuerier struct {
	dbgen.Querier
	recovery  dbgen.RealqaStorageRecovery
	binding   dbgen.RealqaStorageAuthorizationBinding
	assetsErr error
	marked    bool
}

func (queries *storageRecoveryQuerier) GetStorageAuthorizationAttempt(
	context.Context,
	pgtype.UUID,
) (dbgen.RealqaStorageAuthorizationAttempt, error) {
	return dbgen.RealqaStorageAuthorizationAttempt{}, pgx.ErrNoRows
}

func (queries *storageRecoveryQuerier) GetActiveStorageRecovery(
	context.Context,
	pgtype.UUID,
) (dbgen.RealqaStorageRecovery, error) {
	return queries.recovery, nil
}

func (queries *storageRecoveryQuerier) GetStorageAuthorizationBinding(
	context.Context,
	pgtype.UUID,
) (dbgen.RealqaStorageAuthorizationBinding, error) {
	return queries.binding, nil
}

func (queries *storageRecoveryQuerier) ListSubmissionAssets(
	context.Context,
	pgtype.UUID,
) ([]dbgen.RealqaAsset, error) {
	return nil, queries.assetsErr
}

func (queries *storageRecoveryQuerier) MarkStorageRecoveryNotified(
	context.Context,
	pgtype.UUID,
) (dbgen.RealqaStorageRecovery, error) {
	queries.marked = true
	queries.recovery.NotificationState = "notified"
	return queries.recovery, nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("fixture stream failure")
}

func requireServiceError(
	t *testing.T,
	err error,
	code connect.Code,
	reason realqav1.ErrorReason,
	failure realqav1.FailureClass,
) {
	t.Helper()
	if connect.CodeOf(err) != code {
		t.Fatalf("error code = %v, want %v", connect.CodeOf(err), code)
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error type = %T, want *connect.Error", err)
	}
	for _, detail := range connectErr.Details() {
		value, detailErr := detail.Value()
		typed, ok := value.(*realqav1.ErrorDetail)
		if detailErr == nil && ok {
			if typed.Reason != reason || typed.FailureClass != failure {
				t.Fatalf("error detail = (%v, %v), want (%v, %v)",
					typed.Reason, typed.FailureClass, reason, failure)
			}
			return
		}
	}
	t.Fatal("typed error detail is missing")
}
