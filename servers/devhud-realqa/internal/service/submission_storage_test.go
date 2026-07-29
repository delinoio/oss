package service

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

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
