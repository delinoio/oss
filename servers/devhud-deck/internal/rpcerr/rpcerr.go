// Package rpcerr creates typed, display-safe Deck Connect failures.
package rpcerr

import (
	"errors"

	"connectrpc.com/connect"
	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
)

func New(code connect.Code, reason deckv1.ErrorReason) error {
	failure := connect.NewError(code, errors.New(message(code)))
	detail, err := connect.NewErrorDetail(&deckv1.ErrorDetail{Reason: reason})
	if err == nil {
		failure.AddDetail(detail)
	}
	return failure
}

func Conflict(
	reason deckv1.ErrorReason,
	resourceID *deckv1.UuidV7,
	revision *deckv1.Revision,
) error {
	failure := connect.NewError(connect.CodeAborted, errors.New("request conflict"))
	detail, err := connect.NewErrorDetail(&deckv1.ErrorDetail{
		Reason:          reason,
		ResourceId:      resourceID,
		CurrentRevision: revision,
	})
	if err == nil {
		failure.AddDetail(detail)
	}
	return failure
}

func message(code connect.Code) string {
	switch code {
	case connect.CodeUnauthenticated:
		return "authentication required"
	case connect.CodePermissionDenied:
		return "permission denied"
	case connect.CodeInvalidArgument:
		return "invalid request"
	case connect.CodeNotFound:
		return "resource not found"
	case connect.CodeAlreadyExists, connect.CodeAborted, connect.CodeFailedPrecondition:
		return "request conflict"
	case connect.CodeResourceExhausted:
		return "limit reached"
	case connect.CodeUnavailable:
		return "service unavailable"
	default:
		return "internal error"
	}
}
