// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"errors"
	"net"
)

// TransportFailureKind distinguishes preconnection infrastructure failures from
// timeouts and broken established connections without exposing raw error text.
type TransportFailureKind uint8

const (
	TransportFailureUnknown TransportFailureKind = iota
	TransportFailureDNS
	TransportFailureDial
	TransportFailureTimeout
	TransportFailureConnection
	TransportFailureCanceled
)

type TransportFailure struct {
	Kind TransportFailureKind
	Sent bool
}

// PhysicalTransportObserver is an optional extension of Config.ResponseObserver.
// A failure never authorizes replay. Persistence must fence the supplied account version.
type PhysicalTransportObserver interface {
	ObserveTransportFailure(context.Context, Principal, Target, Credential, TransportFailure) error
}

func classifyTransportFailure(ctx context.Context, err error, sent bool) TransportFailure {
	f := TransportFailure{Sent: sent}
	var dns *net.DNSError
	var op *net.OpError
	var ne net.Error
	switch {
	case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
		f.Kind = TransportFailureCanceled
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		f.Kind = TransportFailureTimeout
	case errors.As(err, &ne) && ne.Timeout():
		f.Kind = TransportFailureTimeout
	case errors.As(err, &dns):
		f.Kind = TransportFailureDNS
	case errors.As(err, &op):
		if op.Op == "dial" && !sent {
			f.Kind = TransportFailureDial
		} else {
			f.Kind = TransportFailureConnection
		}
	}
	return f
}
func (h *Handler) observeTransportFailure(ctx context.Context, p Principal, t Target, c Credential, err error, sent bool) error {
	observer, ok := h.c.ResponseObserver.(PhysicalTransportObserver)
	if !ok {
		return nil
	}
	return observer.ObserveTransportFailure(ctx, p, t, c, classifyTransportFailure(ctx, err, sent))
}
