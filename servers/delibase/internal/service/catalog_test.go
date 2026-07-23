package service

import (
	"testing"

	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestCatalogPageCursorRoundTripAndBounds(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("0198a000-0000-7000-8000-000000000001")
	cursor := encodeCatalogCursor(pgtype.UUID{Bytes: [16]byte(id), Valid: true})
	decoded, size, err := catalogPage(&delibasev1.PageRequest{PageSize: 999, Cursor: cursor})
	if err != nil || decoded.Bytes != [16]byte(id) || size != maxCatalogPageSize {
		t.Fatalf("catalogPage() = %#v, %d, %v", decoded, size, err)
	}
	if _, _, err := catalogPage(&delibasev1.PageRequest{PageSize: -1}); err == nil {
		t.Fatal("catalogPage() accepted a negative page size")
	}
	if _, _, err := catalogPage(&delibasev1.PageRequest{Cursor: "not-a-cursor"}); err == nil {
		t.Fatal("catalogPage() accepted a malformed cursor")
	}
}

func TestCatalogRequestIDRequiresCanonicalUUIDv7(t *testing.T) {
	t.Parallel()
	if _, err := catalogRequestID(&delibasev1.UuidV7{Value: "00000000-0000-0000-0000-000000000000"}); err == nil {
		t.Fatal("catalogRequestID() accepted nil UUID")
	}
	if _, err := catalogRequestID(&delibasev1.UuidV7{Value: "0198A000-0000-7000-8000-000000000001"}); err == nil {
		t.Fatal("catalogRequestID() accepted a non-canonical UUID")
	}
}
