package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"github.com/delinoio/oss/servers/devhud-api/internal/auth"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

func TestReplaceSettingsReturnsTypedConflict(t *testing.T) {
	current := &domain.Settings{SchemaVersion: 2, Revision: 7, CanonicalJSON: []byte(`{"theme":"dark"}`), UpdatedAt: time.Now()}
	repository := &serviceRepository{replaceSettings: func(_ context.Context, userID string, schemaVersion uint32, body []byte, revision uint64, _ time.Time) (domain.Settings, error) {
		if userID != "018f7c1e-7b4a-7abc-8def-0123456789ab" || schemaVersion != 2 || string(body) != `{"theme":"light"}` || revision != 6 {
			t.Fatalf("unexpected replacement inputs: user=%q schema=%d body=%q revision=%d", userID, schemaVersion, body, revision)
		}
		return domain.Settings{}, &domain.RevisionConflict{Expected: revision, Current: current}
	}}
	service := NewSettingsService(repository, serviceClock{})
	ctx := authenticatedContext()
	_, err := service.ReplaceSettings(ctx, connect.NewRequest(&devhudv1.ReplaceSettingsRequest{
		SchemaVersion: 2, CanonicalJson: []byte(`{"theme":"light"}`), ExpectedRevision: 6,
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

func TestGetSettingsRejectsAdministrativeAndDeletionBlocks(t *testing.T) {
	for name, user := range map[string]domain.User{
		"administrator": {ID: "018f7c1e-7b4a-7abc-8def-0123456789ab", DeletionState: domain.DeletionStateActive, AdministrativeBlockState: domain.AdministrativeBlockStateBlocked},
		"deletion":      {ID: "018f7c1e-7b4a-7abc-8def-0123456789ab", DeletionState: domain.DeletionStatePending, AdministrativeBlockState: domain.AdministrativeBlockStateUnblocked},
	} {
		t.Run(name, func(t *testing.T) {
			repository := &serviceRepository{getAccount: func(context.Context, string) (domain.User, error) { return user, nil }}
			_, err := NewSettingsService(repository, serviceClock{}).GetSettings(authenticatedContext(), connect.NewRequest(&devhudv1.GetSettingsRequest{}))
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("code = %v, want PermissionDenied", connect.CodeOf(err))
			}
		})
	}
}

func TestRestoreAccountUsesAuthenticatedOwnerAndMapsPurgeClaim(t *testing.T) {
	repository := &serviceRepository{restoreAccount: func(_ context.Context, userID string, _ time.Time) (domain.User, error) {
		if userID != "018f7c1e-7b4a-7abc-8def-0123456789ab" {
			t.Fatalf("restore used user %q", userID)
		}
		return domain.User{}, &domain.AccountStateError{Failure: domain.AccountFailurePurgeClaimed}
	}}
	_, err := NewAccountService(repository, serviceClock{}).RestoreAccount(authenticatedContext(), connect.NewRequest(&devhudv1.RestoreAccountRequest{}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

const testCorrelationID = "018f7c1e-7b4a-7abc-8def-0123456789ac"

func authenticatedContext() context.Context {
	ctx := WithCorrelationID(context.Background(), testCorrelationID)
	return auth.WithUser(ctx, domain.User{ID: "018f7c1e-7b4a-7abc-8def-0123456789ab"})
}

type serviceClock struct{}

func (serviceClock) Now() time.Time { return time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC) }

type serviceRepository struct {
	getAccount      func(context.Context, string) (domain.User, error)
	replaceSettings func(context.Context, string, uint32, []byte, uint64, time.Time) (domain.Settings, error)
	restoreAccount  func(context.Context, string, time.Time) (domain.User, error)
}

func (*serviceRepository) SchemaCurrent(context.Context) (bool, error) { return true, nil }
func (*serviceRepository) Ping(context.Context) error                  { return nil }
func (*serviceRepository) ProvisionUser(context.Context, domain.Identity) (domain.User, error) {
	return domain.User{}, nil
}
func (*serviceRepository) GetSettings(context.Context, string) (*domain.Settings, error) {
	return nil, nil
}
func (repository *serviceRepository) ReplaceSettings(ctx context.Context, userID string, schemaVersion uint32, body []byte, revision uint64, now time.Time) (domain.Settings, error) {
	return repository.replaceSettings(ctx, userID, schemaVersion, body, revision, now)
}
func (repository *serviceRepository) GetAccount(ctx context.Context, userID string) (domain.User, error) {
	return repository.getAccount(ctx, userID)
}
func (*serviceRepository) DeleteAccount(context.Context, string, time.Time) (domain.User, error) {
	return domain.User{}, nil
}
func (repository *serviceRepository) RestoreAccount(ctx context.Context, userID string, now time.Time) (domain.User, error) {
	return repository.restoreAccount(ctx, userID, now)
}
func (*serviceRepository) RecordRequest(context.Context, domain.RequestLog) error { return nil }
func (*serviceRepository) RecordAudit(context.Context, domain.AuditEvent) error   { return nil }
func (*serviceRepository) ClaimPurgeBatch(context.Context, time.Time, int) ([]domain.User, error) {
	return nil, nil
}
func (*serviceRepository) CompleteAccountPurge(context.Context, domain.User, time.Time) error {
	return nil
}
func (*serviceRepository) PruneRetention(context.Context, time.Time) (domain.RetentionResult, error) {
	return domain.RetentionResult{}, nil
}
