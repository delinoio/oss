// Package contracts defines Deck-owned authorization and runtime contracts.
package contracts

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

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
type RepositoryAuthorizer interface {
	CanReadRepository(context.Context, Viewer, string, string) (bool, error)
}

type DenyAllRepositories struct{}

func (DenyAllRepositories) CanReadRepository(
	context.Context,
	Viewer,
	string,
	string,
) (bool, error) {
	return false, nil
}
