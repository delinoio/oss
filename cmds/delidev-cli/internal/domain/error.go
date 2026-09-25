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
	ResourceExhausted    Code = "resource_exhausted"
	CursorExpired        Code = "cursor_expired"
	Canceled             Code = "canceled"
	Internal             Code = "internal"
)

type Error struct {
	Code          Code   `json:"code"`
	Message       string `json:"message"`
	Guidance      string `json:"guidance,omitempty"`
	Cause         string `json:"cause,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }
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

func (e *Error) ExitCode() int {
	switch e.Code {
	case InvalidArgument, MissingInput:
		return 2
	case Unauthenticated, PermissionDenied:
		return 3
	case ServerUnavailable, Unavailable:
		return 4
	case Conflict, ConfirmationRequired, RecoveryRequired, CursorExpired:
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
