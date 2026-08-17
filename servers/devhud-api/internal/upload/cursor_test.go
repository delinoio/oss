package upload

import (
	"testing"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

func TestCursorIsEncryptedScopedBoundedAndExpiring(t *testing.T) {
	codec, err := NewCursorCodec([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	states := []domain.UploadState{domain.UploadStateFinalized}
	cursor := domain.UploadCursor{CreatedAt: now.Add(-time.Minute), UploadID: "0198b123-4567-7abc-8def-012345678901"}
	token, err := codec.Encode("owner-a", states, "submission-a", cursor, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) > domain.UploadMaximumPageTokenBytes || token == cursor.UploadID {
		t.Fatalf("token shape = %q", token)
	}
	decoded, err := codec.Decode(token, "owner-a", states, "submission-a", now)
	if err != nil || decoded != cursor {
		t.Fatalf("decoded = %+v, error = %v", decoded, err)
	}
	for name, test := range map[string]struct {
		owner      string
		states     []domain.UploadState
		submission string
		at         time.Time
	}{
		"owner":      {"owner-b", states, "submission-a", now},
		"state":      {"owner-a", []domain.UploadState{domain.UploadStateDeleted}, "submission-a", now},
		"submission": {"owner-a", states, "submission-b", now},
		"expired":    {"owner-a", states, "submission-a", now.Add(cursorLifetime)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := codec.Decode(token, test.owner, test.states, test.submission, test.at); err == nil {
				t.Fatal("out-of-scope token was accepted")
			}
		})
	}
	tampered := token[:len(token)-1] + "A"
	if _, err := codec.Decode(tampered, "owner-a", states, "submission-a", now); err == nil {
		t.Fatal("tampered token was accepted")
	}
}
