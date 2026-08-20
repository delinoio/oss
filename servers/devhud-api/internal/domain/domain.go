package domain

import (
	"context"
	"errors"
	"time"
)

const (
	SettingsMaximumBytes              = 1_048_576
	RecoveryWindow                    = 30 * 24 * time.Hour
	RequestLogRetention               = 30 * 24 * time.Hour
	AuditRetention                    = 180 * 24 * time.Hour
	CrashReportRetention              = 30 * 24 * time.Hour
	CrashReportMaximumRetainedPerUser = 100
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
	Roles                 []string
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
	ErrIdentityPurged      = errors.New("identity was permanently purged")
	ErrNotFound            = errors.New("record not found")
	ErrCorrelationConflict = errors.New("client correlation already identifies a different crash report")
	ErrCrashReportQuota    = errors.New("crash report quota exhausted")
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
	SubmitCrashReport(context.Context, string, CrashReport) (CrashReport, error)
	RecordRequest(context.Context, RequestLog) error
	RecordAudit(context.Context, AuditEvent) error
	ClaimPurgeBatch(context.Context, time.Time, int) ([]User, error)
	CompleteAccountPurge(context.Context, User, time.Time) error
	PruneRetention(context.Context, time.Time, int) (RetentionResult, error)
}

type RequestLog struct {
	ID                   string
	CorrelationID        string
	Procedure            string
	HTTPStatus           int
	RPCStatusCode        RPCStatusCode
	DurationMilliseconds int64
	CreatedAt            time.Time
	ExpiresAt            time.Time
}

type RPCStatusCode string

const (
	RPCStatusCodeOK                 RPCStatusCode = "OK"
	RPCStatusCodeCanceled           RPCStatusCode = "CANCELED"
	RPCStatusCodeUnknown            RPCStatusCode = "UNKNOWN"
	RPCStatusCodeInvalidArgument    RPCStatusCode = "INVALID_ARGUMENT"
	RPCStatusCodeDeadlineExceeded   RPCStatusCode = "DEADLINE_EXCEEDED"
	RPCStatusCodeNotFound           RPCStatusCode = "NOT_FOUND"
	RPCStatusCodeAlreadyExists      RPCStatusCode = "ALREADY_EXISTS"
	RPCStatusCodePermissionDenied   RPCStatusCode = "PERMISSION_DENIED"
	RPCStatusCodeResourceExhausted  RPCStatusCode = "RESOURCE_EXHAUSTED"
	RPCStatusCodeFailedPrecondition RPCStatusCode = "FAILED_PRECONDITION"
	RPCStatusCodeAborted            RPCStatusCode = "ABORTED"
	RPCStatusCodeOutOfRange         RPCStatusCode = "OUT_OF_RANGE"
	RPCStatusCodeUnimplemented      RPCStatusCode = "UNIMPLEMENTED"
	RPCStatusCodeInternal           RPCStatusCode = "INTERNAL"
	RPCStatusCodeUnavailable        RPCStatusCode = "UNAVAILABLE"
	RPCStatusCodeDataLoss           RPCStatusCode = "DATA_LOSS"
	RPCStatusCodeUnauthenticated    RPCStatusCode = "UNAUTHENTICATED"
)

type AuditAction int16

const (
	AuditActionAccountDeletionRequested AuditAction = iota + 1
	AuditActionAccountRestored
	AuditActionAccountPurgeClaimed
	AuditActionAccountPurged
	AuditActionUploadQuarantined
	AuditActionUploadDeleted
	AuditActionUploadAccountPurged
	AuditActionUserBlocked
	AuditActionUserUnblocked
)

type AuditEvent struct {
	ID                string
	ActorUserID       *string
	TargetUserID      *string
	ActorFingerprint  []byte
	TargetFingerprint []byte
	TargetUploadID    *string
	Reason            string
	Action            AuditAction
	CorrelationID     string
	Outcome           AuditOutcome
	RejectionReason   AuditRejectionReason
	CreatedAt         time.Time
	ExpiresAt         time.Time
}

type RetentionResult struct {
	RequestLogsDeleted  int64
	AuditEventsDeleted  int64
	CrashReportsDeleted int64
}

// CrashReport contains only the bounded, user-previewed diagnostic fields
// accepted by the public contract. OwnerUserID exists solely for authorization
// and account-deletion cascading; it must never be emitted as telemetry.
type CrashReport struct {
	ID                    string
	OwnerUserID           string
	RequestCorrelationID  string
	ClientCorrelationID   string
	PayloadSHA256         []byte
	ReportSchemaVersion   uint32
	AppVersion            string
	BuildID               string
	Platform              int16
	Architecture          int16
	OSVersion             string
	TauriRevision         string
	CEFRevision           string
	OccurredAt            time.Time
	Component             int16
	Severity              int16
	ErrorCode             string
	RedactedSummary       string
	RedactedStackTrace    string
	RelatedCorrelationIDs []string
	DurationMilliseconds  uint64
	AcceptedAt            time.Time
	ExpiresAt             time.Time
}

type SweepCoordinator interface {
	TryLock(context.Context) (unlock func(context.Context) error, acquired bool, err error)
}

type AccountPurgeAdapter interface {
	PurgeAccount(context.Context, User) error
}
