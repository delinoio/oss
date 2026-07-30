// Package contracts defines Deck-owned authorization and runtime contracts.
package contracts

import (
	"context"
	"log/slog"
	"time"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/google/uuid"
)

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

const ProviderRefreshPriceUSDMicros int64 = 50

type RefreshMeter struct {
	MeterID        uuid.UUID
	PriceVersionID uuid.UUID
	ServiceID      uuid.UUID
	USDMicros      int64
}

type UsageReservation struct {
	ID        uuid.UUID
	ExpiresAt time.Time
}

// LiveRefreshUsage is the only Deck billing boundary. Every method requires
// the current request's validated forwarded-user bearer; Deck has no
// background-authorization purpose and never persists this bearer.
type LiveRefreshUsage interface {
	RefreshMeter(context.Context) (RefreshMeter, error)
	ReserveRefresh(
		context.Context,
		string,
		*deckv1.BillingSelection,
		uuid.UUID,
		RefreshMeter,
	) (UsageReservation, error)
	CommitRefresh(context.Context, string, uuid.UUID, uuid.UUID) error
	ReleaseRefresh(context.Context, string, uuid.UUID, uuid.UUID) error
}

type RefreshMetricOutcome uint8

const (
	RefreshMetricUnknown RefreshMetricOutcome = iota
	RefreshMetricCacheHit
	RefreshMetricProviderSuccess
	RefreshMetricProviderFailure
	RefreshMetricBillingFailure
)

// RefreshMetrics accepts only a typed outcome and latency. Repositories,
// queries, titles, identities, URLs, and request credentials cannot be labels.
type RefreshMetrics interface {
	ObserveRefresh(RefreshMetricOutcome, time.Duration)
}

type NoopRefreshMetrics struct{}

func (NoopRefreshMetrics) ObserveRefresh(RefreshMetricOutcome, time.Duration) {}

// LogRefreshMetrics emits only the closed outcome and elapsed milliseconds.
// It intentionally accepts no identity-bearing labels and defines no SLO.
type LogRefreshMetrics struct {
	Logger *slog.Logger
}

func (metrics LogRefreshMetrics) ObserveRefresh(
	outcome RefreshMetricOutcome,
	elapsed time.Duration,
) {
	if metrics.Logger == nil {
		return
	}
	metrics.Logger.Info(
		"Deck refresh completed",
		"event", "deck_refresh_latency",
		"outcome", uint64(outcome),
		"latency_ms", elapsed.Milliseconds(),
	)
}

type OrganizationRole uint8

const (
	OrganizationRoleUnknown OrganizationRole = iota
	OrganizationRoleMember
	OrganizationRoleAdmin
	OrganizationRoleOwner
)

type Membership struct {
	OrganizationID uuid.UUID
	Role           OrganizationRole
}

type TeamMembership struct {
	OrganizationID uuid.UUID
	TeamID         uuid.UUID
}

// Viewer is resolved from current DeliDev account and membership state after
// both access tokens have been independently validated. It contains no bearer.
type Viewer struct {
	AccountID       uuid.UUID
	Subject         string
	GitHubLogin     string
	Memberships     map[uuid.UUID]OrganizationRole
	TeamMemberships map[uuid.UUID]map[uuid.UUID]struct{}
}

func (viewer Viewer) Role(organizationID uuid.UUID) OrganizationRole {
	return viewer.Memberships[organizationID]
}

func (viewer Viewer) IsMember(organizationID uuid.UUID) bool {
	return viewer.Role(organizationID) >= OrganizationRoleMember
}

func (viewer Viewer) CanManage(organizationID uuid.UUID) bool {
	return viewer.Role(organizationID) >= OrganizationRoleAdmin
}

func (viewer Viewer) IsOwner(organizationID uuid.UUID) bool {
	return viewer.Role(organizationID) == OrganizationRoleOwner
}

func (viewer Viewer) CanUseTeam(organizationID, teamID uuid.UUID) bool {
	if teamID == uuid.Nil {
		return viewer.IsMember(organizationID)
	}
	teams := viewer.TeamMemberships[organizationID]
	_, ok := teams[teamID]
	return ok
}

// Directory resolves server-authoritative current DeliDev membership. The
// forwarded bearer has already been validated and is never passed or retained.
type Directory interface {
	ResolveViewer(context.Context, string) (Viewer, error)
}

// RepositoryAuthorizer performs the final current-user GitHub permission
// check before an identity-bearing snapshot leaves the service.
type RepositoryHashKind uint8

const (
	RepositoryHashKindView RepositoryHashKind = iota + 1
	RepositoryHashKindSnapshot
)

type RepositoryAuthorizer interface {
	CanReadRepository(
		context.Context,
		Viewer,
		*deckv1.Owner,
		string,
		string,
	) (bool, error)
	ReadableRepositoryHashes(
		context.Context,
		Viewer,
		*deckv1.Owner,
		RepositoryHashKind,
		[][32]byte,
	) (map[[32]byte]struct{}, error)
}

type DenyAllRepositories struct{}

func (DenyAllRepositories) CanReadRepository(
	context.Context,
	Viewer,
	*deckv1.Owner,
	string,
	string,
) (bool, error) {
	return false, nil
}

func (DenyAllRepositories) ReadableRepositoryHashes(
	context.Context,
	Viewer,
	*deckv1.Owner,
	RepositoryHashKind,
	[][32]byte,
) (map[[32]byte]struct{}, error) {
	return nil, nil
}
