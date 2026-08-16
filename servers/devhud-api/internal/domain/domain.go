package domain

import (
	"context"
	"errors"
	"time"
)

const (
	SettingsMaximumBytes = 1_048_576
	RecoveryWindow       = 30 * 24 * time.Hour
	RequestLogRetention  = 30 * 24 * time.Hour
	AuditRetention       = 180 * 24 * time.Hour
)

type DeletionState int16

const (
	DeletionStateActive       DeletionState = 1
	DeletionStatePending      DeletionState = 2
	DeletionStatePurgeClaimed DeletionState = 3
)

type AdministrativeBlockState int16

const (
	AdministrativeBlockStateUnblocked AdministrativeBlockState = 1
	AdministrativeBlockStateBlocked   AdministrativeBlockState = 2
)

type Identity struct {
	Issuer                string
	Subject               string
	DisplayName           string
	Email                 string
	Fingerprint           []byte
	FingerprintCandidates [][]byte
}

type User struct {
	ID                       string
	Issuer                   string
	Subject                  string
	IdentityFingerprint      []byte
	DisplayName              string
	Email                    string
	DeletionState            DeletionState
	AdministrativeBlockState AdministrativeBlockState
	CreatedAt                time.Time
	UpdatedAt                time.Time
	DeletionRequestedAt      *time.Time
	RecoverableUntil         *time.Time
	RestoreRetryUntil        *time.Time
}

type Settings struct {
	SchemaVersion uint32
	Revision      uint64
	CanonicalJSON []byte
	UpdatedAt     time.Time
}

type RevisionConflict struct {
	Expected uint64
	Current  *Settings
}

func (e *RevisionConflict) Error() string { return "settings revision conflict" }

type AccountFailure int

const (
	AccountFailureRecoveryExpired AccountFailure = iota + 1
	AccountFailurePurgeClaimed
)

type AccountStateError struct {
	Failure AccountFailure
}

type PermissionFailure int

const (
	PermissionFailureAdministrativeBlock PermissionFailure = iota + 1
	PermissionFailureDeletionPending
)

type PermissionError struct {
	Failure PermissionFailure
}

func (e *PermissionError) Error() string { return "account is blocked" }

func (e *AccountStateError) Error() string { return "account state does not allow the operation" }

var (
	ErrIdentityPurged = errors.New("identity was permanently purged")
	ErrNotFound       = errors.New("record not found")
)

type Clock interface {
	Now() time.Time
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

type IDGenerator interface {
	New() (string, error)
}

type Repository interface {
	SchemaCurrent(context.Context) (bool, error)
	Ping(context.Context) error
	ProvisionUser(context.Context, Identity) (User, error)
	GetSettings(context.Context, string) (*Settings, error)
	ReplaceSettings(context.Context, string, uint32, []byte, uint64, time.Time) (Settings, error)
	GetAccount(context.Context, string) (User, error)
	DeleteAccount(context.Context, string, time.Time) (User, error)
	RestoreAccount(context.Context, string, time.Time) (User, error)
	RecordRequest(context.Context, RequestLog) error
	RecordAudit(context.Context, AuditEvent) error
	ClaimPurgeBatch(context.Context, time.Time, int) ([]User, error)
	CompleteAccountPurge(context.Context, User, time.Time) error
	PruneRetention(context.Context, time.Time) (RetentionResult, error)
}

type RequestLog struct {
	ID                   string
	CorrelationID        string
	Procedure            string
	HTTPStatus           int
	DurationMilliseconds int64
	CreatedAt            time.Time
	ExpiresAt            time.Time
}

type AuditAction int16

const (
	AuditActionAccountDeletionRequested AuditAction = iota + 1
	AuditActionAccountRestored
	AuditActionAccountPurgeClaimed
	AuditActionAccountPurged
)

type AuditEvent struct {
	ID                string
	ActorUserID       *string
	TargetUserID      *string
	ActorFingerprint  []byte
	TargetFingerprint []byte
	Action            AuditAction
	CreatedAt         time.Time
	ExpiresAt         time.Time
}

type RetentionResult struct {
	RequestLogsDeleted int64
	AuditEventsDeleted int64
}

type SweepCoordinator interface {
	TryLock(context.Context) (unlock func(context.Context) error, acquired bool, err error)
}

type AccountPurgeAdapter interface {
	PurgeAccount(context.Context, User) error
}
