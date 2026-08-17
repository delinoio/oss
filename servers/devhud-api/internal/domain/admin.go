package domain

import (
	"context"
	"time"
)

const (
	AdminRole             = "devhud-admin"
	AdminDefaultPageSize  = uint32(50)
	AdminMaximumPageSize  = uint32(100)
	AdminMaximumTokenSize = 2048
)

type AuditOutcome int16

const (
	AuditOutcomeAccepted AuditOutcome = iota + 1
	AuditOutcomeRejected
)

type AuditRejectionReason int16

const (
	AuditRejectionUnauthenticated AuditRejectionReason = iota + 1
	AuditRejectionAdminRoleRequired
	AuditRejectionActorBlocked
	AuditRejectionInvalidArgument
	AuditRejectionTargetNotFound
	AuditRejectionConcurrentUpdate
	AuditRejectionFailedPrecondition
	AuditRejectionOperationFailed
)

type UserCursor struct {
	CreatedAt time.Time
	UserID    string
}

type UserList struct {
	Users []User
	Next  *UserCursor
}

type AuditCursor struct {
	CreatedAt time.Time
	AuditID   string
}

type AuditFilters struct {
	ActorUserID    string
	TargetUserID   string
	TargetUploadID string
	CorrelationID  string
	Actions        []AuditAction
	Outcomes       []AuditOutcome
}

type AuditList struct {
	Events []AuditEvent
	Next   *AuditCursor
}

type AdminConflictError struct {
	User   *User
	Upload *Upload
}

func (e *AdminConflictError) Error() string { return "administrator mutation conflicted" }

type AdminRepository interface {
	ListUsers(context.Context, string, *UserCursor, uint32) (UserList, error)
	SetUserBlocked(context.Context, string, string, AdministrativeBlockState, AdministrativeBlockState, AuditEvent, time.Time) (User, error)
	RecordAdministratorAudit(context.Context, AuditEvent) error
	ListAuditEvents(context.Context, AuditFilters, *AuditCursor, uint32) (AuditList, error)
}
