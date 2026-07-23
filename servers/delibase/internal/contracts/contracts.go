// Package contracts defines injectable delibase-owned runtime dependencies.
package contracts

import (
	"context"
	"time"
)

// Clock keeps time-dependent business rules deterministic in tests.
type Clock interface {
	Now() time.Time
}

// SystemClock is the production clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

// PolarClient is the future hosted billing integration boundary. Business
// services depend on this interface, not a provider SDK or global client.
type PolarClient interface {
	CreateCheckout(context.Context, CheckoutRequest) (Checkout, error)
	CreatePortalSession(context.Context, PortalRequest) (PortalSession, error)
}

type CheckoutRequest struct {
	OrganizationID string
	ReturnURL      string
}

type Checkout struct {
	ID  string
	URL string
}

type PortalRequest struct {
	OrganizationID string
	ReturnURL      string
}

type PortalSession struct {
	URL string
}

// IdentityManager is the future Logto Management API deletion boundary.
type IdentityManager interface {
	DeleteUser(context.Context, string) error
}
