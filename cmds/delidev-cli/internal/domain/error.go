package domain

import (
	"context"
	"errors"
)

// Code is a stable automation contract, independent of transport error strings.
type Code string

const (
	InvalidArgument      Code = "invalid_argument"
	NotFound             Code = "not_found"
	Conflict             Code = "conflict"
	Unauthenticated      Code = "unauthenticated"
	PermissionDenied     Code = "permission_denied"
	Unavailable          Code = "unavailable"
	ServerUnavailable    Code = "server_unavailable"
	ConfirmationRequired Code = "confirmation_required"
	MissingInput         Code = "missing_input"
	Unsupported          Code = "unsupported"
	RecoveryRequired     Code = "recovery_required"
	BudgetReached        Code = "budget_reached"
	ResourceExhausted    Code = "resource_exhausted"
	CursorExpired        Code = "cursor_expired"
	ProviderDisabled     Code = "provider_disabled"
	Canceled             Code = "canceled"
	Internal             Code = "internal"
)

// QuotaExhaustedCause is presentation metadata for complete exhaustion proof.
const QuotaExhaustedCause = "quota_exhausted"

// TimeoutCause marks an Unavailable error that was produced by an expired
// deadline. It survives normalization to a safe error, so later consumers can
// still tell a timeout from other Unavailable failures.
const TimeoutCause = "timeout"

type Error struct {
	Code          Code   `json:"code"`
	Message       string `json:"message"`
	Guidance      string `json:"guidance,omitempty"`
	Cause         string `json:"cause,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

func (e *Error) IsQuotaExhausted() bool {
	return e.Code == MissingInput && e.Cause == QuotaExhaustedCause
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// Is lets a timeout-marked error match context.DeadlineExceeded after it has
// been normalized by SafeTimeoutError.
func (e *Error) Is(target error) bool {
	return target == context.DeadlineExceeded && e.Code == Unavailable && e.Cause == TimeoutCause
}

func Fail(code Code, message, guidance string) *Error {
	return &Error{Code: code, Message: message, Guidance: guidance}
}

// SafeError is the only boundary for errors from untrusted providers/filesystems.
// Error text from those systems may contain keys, prompts, paths, or URLs.
func SafeError(err error) *Error {
	var known *Error
	if errors.As(err, &known) {
		return known
	}
	if errors.Is(err, context.Canceled) {
		return Fail(Canceled, "The operation was canceled.", "Inspect the accepted operation before retrying.")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Fail(Unavailable, "The operation timed out.", "Inspect the operation and connection, then retry with the same request ID.")
	}
	return Fail(Internal, "The operation failed.", "Inspect doctor and the correlated server diagnostic.")
}

// SafeTimeoutError is SafeError that keeps timeout identity: an Unavailable
// result of an expired deadline carries TimeoutCause and matches
// context.DeadlineExceeded with errors.Is. Every other error, including an
// Unavailable error with its own cause, is normalized exactly like SafeError.
func SafeTimeoutError(err error) *Error {
	safe := SafeError(err)
	if safe.Code != Unavailable || safe.Cause != "" || !errors.Is(err, context.DeadlineExceeded) {
		return safe
	}
	marked := *safe
	marked.Cause = TimeoutCause
	return &marked
}

func (e *Error) ExitCode() int {
	switch e.Code {
	case InvalidArgument, MissingInput:
		return 2
	case Unauthenticated, PermissionDenied:
		return 3
	case ServerUnavailable, Unavailable:
		return 4
	case Conflict, ConfirmationRequired, RecoveryRequired, CursorExpired, ProviderDisabled, BudgetReached:
		return 5
	case Unsupported:
		return 6
	case ResourceExhausted:
		return 7
	case NotFound:
		return 8
	case Canceled:
		return 130
	default:
		return 1
	}
}
