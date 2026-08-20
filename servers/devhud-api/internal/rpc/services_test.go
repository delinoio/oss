package rpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"github.com/delinoio/oss/protos/gen/go/devhud/v1/devhudv1connect"
	"github.com/delinoio/oss/servers/devhud-api/internal/auth"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

func TestReplaceSettingsReturnsTypedConflict(t *testing.T) {
	current := &domain.Settings{SchemaVersion: 3, Revision: 7, CanonicalJSON: []byte(`{"theme":"dark"}`), UpdatedAt: time.Now()}
	repository := &serviceRepository{replaceSettings: func(_ context.Context, userID string, schemaVersion uint32, body []byte, revision uint64, _ time.Time) (domain.Settings, error) {
		if userID != "018f7c1e-7b4a-7abc-8def-0123456789ab" || schemaVersion != 3 || string(body) != canonicalSettingsV3 || revision != 6 {
			t.Fatalf("unexpected replacement inputs: user=%q schema=%d body=%q revision=%d", userID, schemaVersion, body, revision)
		}
		return domain.Settings{}, &domain.RevisionConflict{Expected: revision, Current: current}
	}}
	service := NewSettingsService(repository, serviceClock{}, testServiceLogger())
	ctx := authenticatedContext()
	_, err := service.ReplaceSettings(ctx, connect.NewRequest(&devhudv1.ReplaceSettingsRequest{
		SchemaVersion: 3, CanonicalJson: []byte(canonicalSettingsV3), ExpectedRevision: 6,
	}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", connect.CodeOf(err))
	}
	connectError := new(connect.Error)
	if !errors.As(err, &connectError) {
		t.Fatalf("error = %v", err)
	}
	var foundMetadata, foundConflict bool
	for _, detail := range connectError.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			t.Fatal(valueErr)
		}
		switch typed := value.(type) {
		case *devhudv1.ErrorMetadata:
			foundMetadata = typed.GetCorrelationId().GetValue() == testCorrelationID
		case *devhudv1.SettingsRevisionConflict:
			foundConflict = typed.GetExpectedRevision() == 6 && typed.GetCurrentSnapshot().GetRevision() == 7
		}
	}
	if !foundMetadata || !foundConflict {
		t.Fatalf("missing typed details: metadata=%v conflict=%v", foundMetadata, foundConflict)
	}
}

func TestReplaceSettingsRejectsSecretsBeforeRepositoryPersistence(t *testing.T) {
	called := false
	repository := &serviceRepository{replaceSettings: func(context.Context, string, uint32, []byte, uint64, time.Time) (domain.Settings, error) {
		called = true
		return domain.Settings{}, nil
	}}
	malicious := strings.Replace(
		canonicalSettingsV2,
		`"profiles":[]`,
		`"profiles":[{"id":"018f47a2-7b3c-7def-8abc-1234567890ab","kind":"fine-grained","name":"Work","token":"plain"}]`,
		1,
	)

	_, err := NewSettingsService(repository, serviceClock{}, testServiceLogger()).ReplaceSettings(authenticatedContext(), connect.NewRequest(&devhudv1.ReplaceSettingsRequest{
		SchemaVersion: 2,
		CanonicalJson: []byte(malicious),
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
	if called {
		t.Fatal("repository was called for invalid settings")
	}
}

func TestGetSettingsMapsTransactionalEligibilityFailures(t *testing.T) {
	for name, failure := range map[string]domain.PermissionFailure{
		"administrator": domain.PermissionFailureAdministrativeBlock,
		"deletion":      domain.PermissionFailureDeletionPending,
	} {
		t.Run(name, func(t *testing.T) {
			repository := &serviceRepository{getSettings: func(_ context.Context, userID string) (*domain.Settings, error) {
				if userID != "018f7c1e-7b4a-7abc-8def-0123456789ab" {
					t.Fatalf("settings read used user %q", userID)
				}
				return nil, &domain.PermissionError{Failure: failure}
			}}
			_, err := NewSettingsService(repository, serviceClock{}, testServiceLogger()).GetSettings(authenticatedContext(), connect.NewRequest(&devhudv1.GetSettingsRequest{}))
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("code = %v, want PermissionDenied", connect.CodeOf(err))
			}
		})
	}
}

func TestGetSettingsMapsCompletedPurge(t *testing.T) {
	repository := &serviceRepository{getSettings: func(context.Context, string) (*domain.Settings, error) {
		return nil, domain.ErrNotFound
	}}
	_, err := NewSettingsService(repository, serviceClock{}, testServiceLogger()).GetSettings(authenticatedContext(), connect.NewRequest(&devhudv1.GetSettingsRequest{}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", connect.CodeOf(err))
	}
	connectError := new(connect.Error)
	if !errors.As(err, &connectError) {
		t.Fatalf("error = %v", err)
	}
	for _, detail := range connectError.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			t.Fatal(valueErr)
		}
		if failure, ok := value.(*devhudv1.PermissionFailure); ok && failure.GetReason() == devhudv1.PermissionFailureReason_PERMISSION_FAILURE_REASON_ACCOUNT_DELETION_PENDING {
			return
		}
	}
	t.Fatal("missing deletion-complete permission failure detail")
}

func TestReplaceSettingsMapsCompletedPurge(t *testing.T) {
	repository := &serviceRepository{replaceSettings: func(context.Context, string, uint32, []byte, uint64, time.Time) (domain.Settings, error) {
		return domain.Settings{}, domain.ErrNotFound
	}}
	_, err := NewSettingsService(repository, serviceClock{}, testServiceLogger()).ReplaceSettings(authenticatedContext(), connect.NewRequest(&devhudv1.ReplaceSettingsRequest{
		SchemaVersion: 1,
		CanonicalJson: []byte(canonicalSettingsV1),
	}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", connect.CodeOf(err))
	}
	connectError := new(connect.Error)
	if !errors.As(err, &connectError) {
		t.Fatalf("error = %v", err)
	}
	for _, detail := range connectError.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			t.Fatal(valueErr)
		}
		if failure, ok := value.(*devhudv1.PermissionFailure); ok && failure.GetReason() == devhudv1.PermissionFailureReason_PERMISSION_FAILURE_REASON_ACCOUNT_DELETION_PENDING {
			return
		}
	}
	t.Fatal("missing deletion-complete permission failure detail")
}

func TestRestoreAccountUsesAuthenticatedOwnerAndMapsPurgeClaim(t *testing.T) {
	repository := &serviceRepository{restoreAccount: func(_ context.Context, userID string, _ time.Time) (domain.User, error) {
		if userID != "018f7c1e-7b4a-7abc-8def-0123456789ab" {
			t.Fatalf("restore used user %q", userID)
		}
		return domain.User{}, &domain.AccountStateError{Failure: domain.AccountFailurePurgeClaimed}
	}}
	_, err := NewAccountService(repository, serviceClock{}, testServiceLogger()).RestoreAccount(authenticatedContext(), connect.NewRequest(&devhudv1.RestoreAccountRequest{}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

func TestDeleteAccountMapsCompletedPurge(t *testing.T) {
	repository := &serviceRepository{deleteAccount: func(_ context.Context, userID string, _ time.Time) (domain.User, error) {
		if userID != "018f7c1e-7b4a-7abc-8def-0123456789ab" {
			t.Fatalf("delete used user %q", userID)
		}
		return domain.User{}, domain.ErrNotFound
	}}
	_, err := NewAccountService(repository, serviceClock{}, testServiceLogger()).DeleteAccount(authenticatedContext(), connect.NewRequest(&devhudv1.DeleteAccountRequest{}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
	connectError := new(connect.Error)
	if !errors.As(err, &connectError) {
		t.Fatalf("error = %v", err)
	}
	for _, detail := range connectError.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			t.Fatal(valueErr)
		}
		if failure, ok := value.(*devhudv1.AccountFailure); ok && failure.GetReason() == devhudv1.AccountFailureReason_ACCOUNT_FAILURE_REASON_PURGE_CLAIMED {
			return
		}
	}
	t.Fatal("missing purge-completed account failure detail")
}

func TestAccountRepositoryFailuresAreLoggedBeforeInternalResponse(t *testing.T) {
	repository := &serviceRepository{getAccount: func(context.Context, string) (domain.User, error) {
		return domain.User{}, errors.New("database timeout")
	}}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	_, err := NewAccountService(repository, serviceClock{}, logger).GetAccount(authenticatedContext(), connect.NewRequest(&devhudv1.GetAccountRequest{}))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
	if strings.Contains(err.Error(), "database timeout") {
		t.Fatalf("internal response exposed repository error: %v", err)
	}
	for _, value := range []string{
		"account repository operation failed",
		testCorrelationID,
		devhudv1connect.AccountServiceGetAccountProcedure,
		"database timeout",
	} {
		if !strings.Contains(logs.String(), value) {
			t.Fatalf("log %q does not contain %q", logs.String(), value)
		}
	}
}

func TestSettingsReadFailuresAreLoggedBeforeInternalResponse(t *testing.T) {
	repository := &serviceRepository{getSettings: func(context.Context, string) (*domain.Settings, error) {
		return nil, errors.New("database timeout")
	}}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	_, err := NewSettingsService(repository, serviceClock{}, logger).GetSettings(authenticatedContext(), connect.NewRequest(&devhudv1.GetSettingsRequest{}))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
	if strings.Contains(err.Error(), "database timeout") {
		t.Fatalf("internal response exposed repository error: %v", err)
	}
	for _, value := range []string{
		"settings repository operation failed",
		testCorrelationID,
		devhudv1connect.SettingsServiceGetSettingsProcedure,
		"database timeout",
	} {
		if !strings.Contains(logs.String(), value) {
			t.Fatalf("log %q does not contain %q", logs.String(), value)
		}
	}
}

func TestSettingsWriteFailuresAreLoggedBeforeInternalResponse(t *testing.T) {
	repository := &serviceRepository{replaceSettings: func(context.Context, string, uint32, []byte, uint64, time.Time) (domain.Settings, error) {
		return domain.Settings{}, errors.New("database timeout")
	}}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	_, err := NewSettingsService(repository, serviceClock{}, logger).ReplaceSettings(authenticatedContext(), connect.NewRequest(&devhudv1.ReplaceSettingsRequest{
		SchemaVersion: 1,
		CanonicalJson: []byte(canonicalSettingsV1),
	}))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
	if strings.Contains(err.Error(), "database timeout") {
		t.Fatalf("internal response exposed repository error: %v", err)
	}
	for _, value := range []string{
		"settings repository operation failed",
		testCorrelationID,
		devhudv1connect.SettingsServiceReplaceSettingsProcedure,
		"database timeout",
	} {
		if !strings.Contains(logs.String(), value) {
			t.Fatalf("log %q does not contain %q", logs.String(), value)
		}
	}
}

const testCorrelationID = "018f7c1e-7b4a-7abc-8def-0123456789ac"

func authenticatedContext() context.Context {
	ctx := WithCorrelationID(context.Background(), testCorrelationID)
	return auth.WithUser(ctx, domain.User{ID: "018f7c1e-7b4a-7abc-8def-0123456789ab"})
}

func testServiceLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

type serviceClock struct{}

func (serviceClock) Now() time.Time { return time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC) }

type serviceRepository struct {
	getAccount        func(context.Context, string) (domain.User, error)
	getSettings       func(context.Context, string) (*domain.Settings, error)
	replaceSettings   func(context.Context, string, uint32, []byte, uint64, time.Time) (domain.Settings, error)
	deleteAccount     func(context.Context, string, time.Time) (domain.User, error)
	restoreAccount    func(context.Context, string, time.Time) (domain.User, error)
	submitCrashReport func(context.Context, string, domain.CrashReport) (domain.CrashReport, error)
}

func (*serviceRepository) SchemaCurrent(context.Context) (bool, error) { return true, nil }
func (*serviceRepository) Ping(context.Context) error                  { return nil }
func (*serviceRepository) ProvisionUser(context.Context, domain.Identity) (domain.User, error) {
	return domain.User{}, nil
}
func (repository *serviceRepository) GetSettings(ctx context.Context, userID string) (*domain.Settings, error) {
	return repository.getSettings(ctx, userID)
}
func (repository *serviceRepository) ReplaceSettings(ctx context.Context, userID string, schemaVersion uint32, body []byte, revision uint64, now time.Time) (domain.Settings, error) {
	return repository.replaceSettings(ctx, userID, schemaVersion, body, revision, now)
}
func (repository *serviceRepository) GetAccount(ctx context.Context, userID string) (domain.User, error) {
	return repository.getAccount(ctx, userID)
}
func (repository *serviceRepository) DeleteAccount(ctx context.Context, userID string, now time.Time) (domain.User, error) {
	return repository.deleteAccount(ctx, userID, now)
}
func (repository *serviceRepository) RestoreAccount(ctx context.Context, userID string, now time.Time) (domain.User, error) {
	return repository.restoreAccount(ctx, userID, now)
}
func (repository *serviceRepository) SubmitCrashReport(ctx context.Context, userID string, report domain.CrashReport) (domain.CrashReport, error) {
	if repository.submitCrashReport == nil {
		return domain.CrashReport{}, nil
	}
	return repository.submitCrashReport(ctx, userID, report)
}
func (*serviceRepository) RecordRequest(context.Context, domain.RequestLog) error { return nil }
func (*serviceRepository) RecordAudit(context.Context, domain.AuditEvent) error   { return nil }
func (*serviceRepository) ClaimPurgeBatch(context.Context, time.Time, int) ([]domain.User, error) {
	return nil, nil
}
func (*serviceRepository) CompleteAccountPurge(context.Context, domain.User, time.Time) error {
	return nil
}
func (*serviceRepository) PruneRetention(context.Context, time.Time, int) (domain.RetentionResult, error) {
	return domain.RetentionResult{}, nil
}
