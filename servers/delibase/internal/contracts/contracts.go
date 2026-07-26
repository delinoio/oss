// Package contracts defines injectable delibase-owned runtime dependencies.
package contracts

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ErrCheckoutNotCreated marks a definitive provider rejection before a hosted
// checkout exists. Unmarked provider errors are treated as ambiguous so the
// same idempotency scope can recover them without admitting a distinct checkout.
var ErrCheckoutNotCreated = errors.New("contracts: checkout was not created")

// ValidPolarProviderID reports whether value is accepted by Polar identifier
// fields used across catalog configuration and provider requests.
func ValidPolarProviderID(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 255 &&
		!strings.ContainsAny(value, "/\x00\r\n")
}

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

// PolarCustomerManager creates or retrieves the provider customer bound to a
// delibase organization before the organization is committed locally.
type PolarCustomerManager interface {
	EnsureCustomer(context.Context, CustomerRequest) (Customer, error)
}

type PolarUsageReporter interface {
	ReportUsage(context.Context, UsageEvent) error
}

type UsageEvent struct {
	Name               string
	ExternalCustomerID string
	ExternalID         string
	Units              int64
	Timestamp          time.Time
}

type CustomerRequest struct {
	OrganizationID string
	Name           string
}

type Customer struct {
	ID string
}

type CheckoutRequest struct {
	OrganizationID string
	SuccessURL     string
	CancelURL      string
	IdempotencyKey string
}

type Checkout struct {
	ID        string
	URL       string
	ExpiresAt time.Time
}

type PortalRequest struct {
	OrganizationID string
	ReturnURL      string
	IdempotencyKey string
}

type PortalSession struct {
	URL       string
	ExpiresAt time.Time
}

// IdentityManager is the future Logto Management API deletion boundary.
type IdentityManager interface {
	DeleteUser(context.Context, string) error
}
