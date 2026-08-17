package upload

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

func TestAdministratorReasonValidation(t *testing.T) {
	for _, reason := range []string{
		"Quarantined after repeated policy violations.",
		"Expected yes / no",
		"Reviewed incident from 2026/08/15.",
		"Reviewed https://docs.example.com/policy?v=42#quarantine",
	} {
		if err := validateAdministratorReason(reason); err != nil {
			t.Fatalf("safe reason %q rejected: %v", reason, err)
		}
	}

	for _, reason := range []string{
		"Authorization: Bearer unsafe-value",
		"refresh_token=unsafe-value",
		"Authentication failure exposed eyJhbGciOiJIUzI1NiJ9.payload.signature",
		"See /Users/example/private/incident.txt",
		"See src/private/incident.txt",
		"source:src/private/app.ts:10",
		"frame:C:\\Users\\alice\\app.ts:10",
		"source=%2Fworkspace%2Fprivate%2Fapp.ts",
		"https://example.com/audit?to%6ben=unsafe-value",
		"file:///Users/example/private/incident.txt",
	} {
		if err := validateAdministratorReason(reason); err == nil {
			t.Fatalf("sensitive reason %q was accepted", reason)
		}
	}
}

func TestAdministratorUnfilteredCursorCannotBeReusedWithOwnerFilter(t *testing.T) {
	events := []string{}
	next := domain.UploadCursor{CreatedAt: testNow.Add(-time.Minute), UploadID: "0198b123-4567-7abc-8def-012345678901"}
	repository := &fakeRepository{events: &events, administratorList: domain.UploadList{Next: &next}}
	hooks := NewAdministratorHooks(newTestService(t, repository, &fakeStorage{events: &events}, &fakeCache{events: &events}))
	_, token, err := hooks.ListUploads(context.Background(), "actor", domain.AdminUploadFilters{}, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if repository.administratorListOwner != "" || token == "" {
		t.Fatalf("owner = %q, token = %q", repository.administratorListOwner, token)
	}
	if _, _, err := hooks.ListUploads(context.Background(), "different-actor", domain.AdminUploadFilters{}, token, 1); err == nil {
		t.Fatal("unfiltered cursor was accepted with an owner filter")
	}
}

func TestAdministratorRemovalCarriesAuditIntoCompletion(t *testing.T) {
	events := []string{}
	checksum := sha256.Sum256([]byte("image"))
	repository := &fakeRepository{events: &events, upload: testUpload(checksum)}
	hooks := NewAdministratorHooks(newTestService(t, repository, &fakeStorage{events: &events}, &fakeCache{events: &events}))
	if _, err := hooks.RemoveUpload(context.Background(), "actor", repository.upload.UploadID, domain.RemovalReasonAdministratorDeleted, repository.upload.State, "Reviewed policy violation.", domain.AuditEvent{}); err != nil {
		t.Fatal(err)
	}
	if repository.completedAudit == nil || repository.completedAudit.ActorUserID != "actor" || repository.completedAudit.Rationale != "Reviewed policy violation." {
		t.Fatalf("audit = %+v", repository.completedAudit)
	}
}

func TestAdministratorReasonIsRejectedBeforeRemoval(t *testing.T) {
	events := []string{}
	repository := &fakeRepository{events: &events}
	hooks := NewAdministratorHooks(newTestService(t, repository, &fakeStorage{events: &events}, &fakeCache{events: &events}))
	if _, err := hooks.RemoveUpload(context.Background(), "actor", "upload", domain.RemovalReasonAdministratorDeleted, domain.UploadStateFinalized, "token=unsafe-value", domain.AuditEvent{}); err == nil {
		t.Fatal("sensitive administrator reason was accepted")
	}
	if len(events) != 0 {
		t.Fatalf("invalid reason triggered side effects: %v", events)
	}
}
