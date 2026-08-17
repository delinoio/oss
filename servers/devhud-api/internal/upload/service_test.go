package upload

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

func TestValidateObjectChecksEveryPNGDimensionBoundary(t *testing.T) {
	checksum := sha256.Sum256([]byte("image"))
	upload := testUpload(checksum)
	binding := testBinding(checksum)
	tests := []struct {
		name          string
		width, height uint32
		mutate        func(*domain.UploadObject)
		want          domain.UploadFailure
	}{
		{name: "maximum", width: 4096, height: 4096},
		{name: "width overflow", width: 4097, height: 1, want: domain.UploadFailureUnsafeDimensions},
		{name: "height overflow", width: 1, height: 4097, want: domain.UploadFailureUnsafeDimensions},
		{name: "zero", width: 0, height: 1, want: domain.UploadFailureUnsafeDimensions},
		{name: "pixel overflow", width: 4096, height: 4097, want: domain.UploadFailureUnsafeDimensions},
		{name: "wrong type", width: 1, height: 1, mutate: func(object *domain.UploadObject) { object.ContentType = "image/png; charset=binary" }, want: domain.UploadFailureInvalidContentType},
		{name: "wrong checksum", width: 1, height: 1, mutate: func(object *domain.UploadObject) { object.Checksum[0] ^= 0xff }, want: domain.UploadFailureChecksumMismatch},
		{name: "wrong signature", width: 1, height: 1, mutate: func(object *domain.UploadObject) { object.Header[0] = 0 }, want: domain.UploadFailureInvalidPNGSignature},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			object := testObject(checksum, test.width, test.height)
			if test.mutate != nil {
				test.mutate(&object)
			}
			width, height, failure := validateObject(upload, binding, object)
			if failure != test.want {
				t.Fatalf("failure = %v, want %v", failure, test.want)
			}
			if failure == 0 && (width != test.width || height != test.height) {
				t.Fatalf("dimensions = %dx%d", width, height)
			}
		})
	}
}

func TestObjectByteQuotaAcceptsExactBoundaryAndRejectsCrossingIt(t *testing.T) {
	checksum := sha256.Sum256([]byte("image"))
	events := []string{}
	repository := &fakeRepository{upload: testUpload(checksum), events: &events}
	service := newTestService(t, repository, &fakeStorage{events: &events}, &fakeCache{events: &events})
	if _, err := service.Create(context.Background(), "owner", domain.UploadTarget{Kind: domain.UploadTargetNewSubmission}, domain.UploadMaximumObjectBytes, checksum); err != nil {
		t.Fatalf("exact 50 MiB boundary failed: %v", err)
	}
	if _, err := service.Create(context.Background(), "owner", domain.UploadTarget{Kind: domain.UploadTargetNewSubmission}, domain.UploadMaximumObjectBytes+1, checksum); err == nil {
		t.Fatal("object above 50 MiB succeeded")
	} else {
		var quota *domain.QuotaError
		if !errors.As(err, &quota) || quota.Quota != domain.QuotaObjectBytes || quota.Observed != domain.UploadMaximumObjectBytes+1 {
			t.Fatalf("quota error = %#v", err)
		}
	}
}

func TestPublicURLIsExactAndStable(t *testing.T) {
	checksum := sha256.Sum256([]byte("image"))
	events := []string{}
	service := newTestService(t, &fakeRepository{upload: testUpload(checksum), events: &events}, &fakeStorage{events: &events}, &fakeCache{events: &events})
	publicID := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if got, want := service.PublicURL(publicID), "https://assets.example.com/"+publicID+".png"; got != want {
		t.Fatalf("public URL = %q, want %q", got, want)
	}
}

func TestFinalizePromotesRecordedETagAndCleansStaging(t *testing.T) {
	checksum := sha256.Sum256([]byte("image"))
	events := []string{}
	repository := &fakeRepository{upload: testUpload(checksum), events: &events}
	storage := &fakeStorage{object: testObject(checksum, 1200, 800), events: &events}
	service := newTestService(t, repository, storage, &fakeCache{events: &events})
	upload, err := service.Finalize(context.Background(), "owner", testBinding(checksum))
	if err != nil {
		t.Fatal(err)
	}
	if upload.State != domain.UploadStateFinalized || upload.Width != 1200 || upload.Height != 800 {
		t.Fatalf("upload = %+v", upload)
	}
	want := []string{"get", "inspect", "claim", "promote", "complete", "delete-staging", "staging-deleted"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestFinalizeInvalidObjectRejectsAndCleansWithoutQuotaClaim(t *testing.T) {
	checksum := sha256.Sum256([]byte("image"))
	events := []string{}
	repository := &fakeRepository{upload: testUpload(checksum), events: &events}
	storage := &fakeStorage{object: testObject(checksum, 1, 1), events: &events}
	storage.object.Checksum[0] ^= 0xff
	service := newTestService(t, repository, storage, &fakeCache{events: &events})
	_, err := service.Finalize(context.Background(), "owner", testBinding(checksum))
	var uploadError *domain.UploadError
	if !errors.As(err, &uploadError) || uploadError.Failure != domain.UploadFailureChecksumMismatch {
		t.Fatalf("error = %v", err)
	}
	want := []string{"get", "inspect", "reject", "delete-staging", "staging-deleted"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestFinalizeQuotaFailureDoesNotDeleteRetryableStaging(t *testing.T) {
	checksum := sha256.Sum256([]byte("image"))
	events := []string{}
	repository := &fakeRepository{upload: testUpload(checksum), events: &events, claimError: &domain.QuotaError{Quota: domain.QuotaStoredBytes}}
	storage := &fakeStorage{object: testObject(checksum, 1, 1), events: &events}
	service := newTestService(t, repository, storage, &fakeCache{events: &events})
	_, err := service.Finalize(context.Background(), "owner", testBinding(checksum))
	var quota *domain.QuotaError
	if !errors.As(err, &quota) {
		t.Fatalf("error = %v", err)
	}
	want := []string{"get", "inspect", "claim"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestFinalizeExpiredReservationRejectsAndCleansStaging(t *testing.T) {
	checksum := sha256.Sum256([]byte("image"))
	events := []string{}
	repository := &fakeRepository{
		upload:   testUpload(checksum),
		events:   &events,
		getError: &domain.UploadError{Failure: domain.UploadFailureReservationExpired},
	}
	service := newTestService(t, repository, &fakeStorage{events: &events}, &fakeCache{events: &events})
	_, err := service.Finalize(context.Background(), "owner", testBinding(checksum))
	var uploadError *domain.UploadError
	if !errors.As(err, &uploadError) || uploadError.Failure != domain.UploadFailureReservationExpired {
		t.Fatalf("error = %v", err)
	}
	want := []string{"get", "reject", "delete-staging", "staging-deleted"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestFinalizedDeletionReplacesOriginBeforeCacheAndTombstone(t *testing.T) {
	checksum := sha256.Sum256([]byte("image"))
	events := []string{}
	upload := testUpload(checksum)
	finalized := testNow
	upload.State, upload.FinalizedAt, upload.PublicETag = domain.UploadStateFinalized, &finalized, `"original"`
	repository := &fakeRepository{upload: upload, events: &events}
	storage := &fakeStorage{events: &events}
	service := newTestService(t, repository, storage, &fakeCache{events: &events})
	removed, err := service.Delete(context.Background(), "owner", upload.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	if removed.State != domain.UploadStateDeleted {
		t.Fatalf("state = %v", removed.State)
	}
	want := []string{"claim-remove", "replace-public", "record-replacement", "purge-revalidate", "complete-remove"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestPendingDeletionNeverCreatesPublicObject(t *testing.T) {
	checksum := sha256.Sum256([]byte("image"))
	events := []string{}
	repository := &fakeRepository{upload: testUpload(checksum), events: &events}
	storage := &fakeStorage{events: &events}
	service := newTestService(t, repository, storage, &fakeCache{events: &events})
	if _, err := service.Delete(context.Background(), "owner", repository.upload.UploadID); err != nil {
		t.Fatal(err)
	}
	want := []string{"claim-remove", "delete-staging", "staging-deleted", "complete-remove"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func newTestService(t *testing.T, repository *fakeRepository, storage *fakeStorage, cache *fakeCache) *Service {
	t.Helper()
	codec, err := NewCursorCodec([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	return NewService(repository, storage, cache, codec, fixedClock{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), "https://assets.example.com", []byte("marker"))
}

var testNow = time.Date(2026, 8, 17, 1, 0, 0, 0, time.UTC)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return testNow }

func testUpload(checksum [32]byte) domain.Upload {
	return domain.Upload{UploadReservation: domain.UploadReservation{
		UploadID: "0198b123-4567-7abc-8def-012345678901", OwnerUserID: "owner",
		SubmissionID: "0198b123-4567-7abc-8def-012345678902", UploadGroupID: "0198b123-4567-7abc-8def-012345678903",
		ReservationID: "0198b123-4567-7abc-8def-012345678904", PublicID: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		StagingID: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", StagingGeneration: 7,
		SizeBytes: 5, SHA256: checksum, CreatedAt: testNow.Add(-time.Minute), SignedURLExpiresAt: testNow.Add(time.Minute), StagingExpiresAt: testNow.Add(time.Hour),
	}, State: domain.UploadStatePending}
}

func testBinding(checksum [32]byte) domain.UploadBinding {
	upload := testUpload(checksum)
	return domain.UploadBinding{UploadID: upload.UploadID, SubmissionID: upload.SubmissionID, UploadGroupID: upload.UploadGroupID,
		ReservationID: upload.ReservationID, StagingGeneration: upload.StagingGeneration, SizeBytes: upload.SizeBytes, SHA256: checksum, ObservedETag: `"etag"`}
}

func testObject(checksum [32]byte, width, height uint32) domain.UploadObject {
	header := make([]byte, 33)
	copy(header, pngSignature)
	binary.BigEndian.PutUint32(header[8:12], 13)
	copy(header[12:16], "IHDR")
	binary.BigEndian.PutUint32(header[16:20], width)
	binary.BigEndian.PutUint32(header[20:24], height)
	return domain.UploadObject{ETag: `"etag"`, SizeBytes: 5, ContentType: "image/png", Checksum: append([]byte(nil), checksum[:]...), Header: header}
}

type fakeStorage struct {
	object domain.UploadObject
	events *[]string
}

func (s *fakeStorage) event(value string) { *s.events = append(*s.events, value) }
func (s *fakeStorage) PresignPUT(context.Context, domain.UploadReservation) (domain.SignedPUT, error) {
	s.event("sign")
	return domain.SignedPUT{}, nil
}
func (s *fakeStorage) InspectStaging(context.Context, domain.UploadReservation) (domain.UploadObject, error) {
	s.event("inspect")
	return s.object, nil
}
func (s *fakeStorage) Promote(context.Context, domain.Upload, string) (string, error) {
	s.event("promote")
	return `"public"`, nil
}
func (s *fakeStorage) DeleteStaging(context.Context, domain.UploadReservation) error {
	s.event("delete-staging")
	return nil
}
func (s *fakeStorage) ReplacePublic(context.Context, domain.Upload, []byte) (string, error) {
	s.event("replace-public")
	return `"marker"`, nil
}

type fakeCache struct{ events *[]string }

func (c *fakeCache) PurgeAndRevalidate(context.Context, string, []byte) error {
	*c.events = append(*c.events, "purge-revalidate")
	return nil
}

type fakeRepository struct {
	upload     domain.Upload
	events     *[]string
	getError   error
	claimError error
}

func (r *fakeRepository) event(value string) { *r.events = append(*r.events, value) }
func (r *fakeRepository) CreateUpload(_ context.Context, command domain.CreateUpload, sign func(context.Context, domain.UploadReservation) (domain.SignedPUT, error)) (domain.UploadReservation, error) {
	return r.upload.UploadReservation, nil
}
func (r *fakeRepository) GetUploadForFinalize(context.Context, string, domain.UploadBinding, time.Time) (domain.Upload, error) {
	r.event("get")
	return r.upload, r.getError
}
func (r *fakeRepository) ClaimUploadPromotion(_ context.Context, _ string, _ domain.UploadBinding, _ domain.UploadObject, width, height uint32, token string, _ time.Time) (domain.Upload, error) {
	r.event("claim")
	if r.claimError != nil {
		return domain.Upload{}, r.claimError
	}
	r.upload.State, r.upload.Width, r.upload.Height, r.upload.StagingETag, r.upload.OperationToken = domain.UploadStatePublishing, width, height, `"etag"`, token
	return r.upload, nil
}
func (r *fakeRepository) CompleteUploadPromotion(context.Context, string, string, string, time.Time) (domain.Upload, error) {
	r.event("complete")
	r.upload.State, r.upload.FinalizedAt = domain.UploadStateFinalized, &testNow
	return r.upload, nil
}
func (r *fakeRepository) ReleaseUploadPromotion(context.Context, string, string) error {
	r.event("release")
	return nil
}
func (r *fakeRepository) RejectUpload(context.Context, string, domain.UploadBinding, domain.UploadFailure, time.Time) error {
	r.event("reject")
	return nil
}
func (r *fakeRepository) ListUploads(context.Context, string, []domain.UploadState, string, *domain.UploadCursor, uint32) (domain.UploadList, error) {
	return domain.UploadList{}, nil
}
func (r *fakeRepository) ListUploadsForAdministrator(context.Context, string, []domain.UploadState, *domain.UploadCursor, uint32) (domain.UploadList, error) {
	return domain.UploadList{}, nil
}
func (r *fakeRepository) ClaimUploadRemoval(_ context.Context, _ string, _ string, reason domain.RemovalReason, token string, _ time.Time) (domain.Upload, error) {
	r.event("claim-remove")
	r.upload.State, r.upload.RemovalReason, r.upload.OperationToken = domain.UploadStateRemoving, reason, token
	return r.upload, nil
}
func (r *fakeRepository) RecordUploadReplacement(_ context.Context, _, _, etag string) (domain.Upload, error) {
	r.event("record-replacement")
	r.upload.ReplacementETag = etag
	return r.upload, nil
}
func (r *fakeRepository) CompleteUploadRemoval(context.Context, string, string, time.Time) (domain.Upload, error) {
	r.event("complete-remove")
	if r.upload.RemovalReason == domain.RemovalReasonAdministratorQuarantined {
		r.upload.State = domain.UploadStateQuarantined
	} else {
		r.upload.State = domain.UploadStateDeleted
	}
	return r.upload, nil
}
func (r *fakeRepository) ReleaseUploadRemoval(context.Context, string, string) error {
	r.event("release-remove")
	return nil
}
func (r *fakeRepository) ClaimExpiredUploads(context.Context, time.Time, int) ([]domain.Upload, error) {
	return nil, nil
}
func (r *fakeRepository) CompleteExpiredUpload(context.Context, string, time.Time) error {
	r.event("staging-deleted")
	return nil
}
func (r *fakeRepository) ListAccountUploadsForPurge(context.Context, string, int) ([]domain.Upload, error) {
	return nil, nil
}
func (r *fakeRepository) RemoveAccountUploadMetadata(context.Context, string) error { return nil }
func (r *fakeRepository) GetUploadUsage(context.Context, string, time.Time) (domain.UploadUsage, error) {
	return domain.UploadUsage{}, nil
}
func (r *fakeRepository) RecordAdministratorUploadAudit(context.Context, string, string, domain.RemovalReason, string, time.Time) error {
	return nil
}
