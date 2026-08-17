package upload

import (
	"context"
	"testing"

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

func TestAdministratorReasonIsRejectedBeforeRemoval(t *testing.T) {
	events := []string{}
	repository := &fakeRepository{events: &events}
	hooks := NewAdministratorHooks(newTestService(t, repository, &fakeStorage{events: &events}, &fakeCache{events: &events}))
	if _, err := hooks.RemoveUpload(context.Background(), "actor", "upload", domain.RemovalReasonAdministratorDeleted, "token=unsafe-value"); err == nil {
		t.Fatal("sensitive administrator reason was accepted")
	}
	if len(events) != 0 {
		t.Fatalf("invalid reason triggered side effects: %v", events)
	}
}
