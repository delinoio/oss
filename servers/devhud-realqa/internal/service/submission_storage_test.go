package service

import (
	"errors"
	"io"
	"strings"
	"testing"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestValidateImagesRejectsEmptyDeclarationSet(t *testing.T) {
	requireServiceError(t, validateImages(nil), connect.CodeInvalidArgument,
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
