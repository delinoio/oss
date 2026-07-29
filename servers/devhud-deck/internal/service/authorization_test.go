package service

import (
	"context"
	"testing"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/google/uuid"
)

type deniedRepositories struct{}

func (deniedRepositories) CanReadRepository(
	context.Context,
	contracts.Viewer,
	string,
	string,
) (bool, error) {
	return false, nil
}

func TestPersonalAndOrganizationOwnership(t *testing.T) {
	t.Parallel()
	accountID := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	otherAccountID := uuid.MustParse("01900000-0000-7000-8000-000000000002")
	organizationID := uuid.MustParse("01900000-0000-7000-8000-000000000003")
	viewer := contracts.Viewer{
		AccountID: accountID,
		Memberships: map[uuid.UUID]contracts.OrganizationRole{
			organizationID: contracts.OrganizationRoleMember,
		},
	}
	personal := &deckv1.Owner{
		Scope: deckv1.OwnerScope_OWNER_SCOPE_PERSONAL,
		OwnerId: &deckv1.Owner_AccountId{AccountId: &deckv1.UuidV7{
			Value: accountID.String(),
		}},
	}
	if _, err := authorizeOwner(viewer, personal, true); err != nil {
		t.Fatalf("personal owner rejected: %v", err)
	}
	personal.OwnerId = &deckv1.Owner_AccountId{AccountId: &deckv1.UuidV7{
		Value: otherAccountID.String(),
	}}
	if _, err := authorizeOwner(viewer, personal, false); err == nil {
		t.Fatal("another personal owner was accepted")
	}
	organization := &deckv1.Owner{
		Scope: deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION,
		OwnerId: &deckv1.Owner_OrganizationId{OrganizationId: &deckv1.UuidV7{
			Value: organizationID.String(),
		}},
	}
	if _, err := authorizeOwner(viewer, organization, false); err != nil {
		t.Fatalf("organization member read rejected: %v", err)
	}
	if _, err := authorizeOwner(viewer, organization, true); err == nil {
		t.Fatal("organization member was allowed to manage")
	}
	viewer.Memberships[organizationID] = contracts.OrganizationRoleAdmin
	if _, err := authorizeOwner(viewer, organization, true); err != nil {
		t.Fatalf("organization admin manage rejected: %v", err)
	}
}

func TestViewRepositoryPermissionFailsBeforeIdentityBearingData(t *testing.T) {
	t.Parallel()
	service := &View{dependencies: Dependencies{
		Repositories: deniedRepositories{},
	}.withDefaults()}
	allowed, err := service.canReadViewRepositories(context.Background(),
		contracts.Viewer{}, &deckv1.View{Query: &deckv1.ViewQuery{
			Builder: &deckv1.QueryBuilder{Clauses: []*deckv1.QueryClause{{
				Clause: &deckv1.QueryClause_Repository{
					Repository: &deckv1.RepositoryQualifier{
						Owner: "secret", Repository: "project",
					},
				},
			}}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("unauthorized repository was allowed")
	}
}

func TestRepositoryAuthorizationDefaultsToDeny(t *testing.T) {
	t.Parallel()
	dependencies := Dependencies{}.withDefaults()
	allowed, err := dependencies.Repositories.CanReadRepository(
		context.Background(), contracts.Viewer{}, "secret", "project")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("missing repository authorizer failed open")
	}
}

func TestOrganizationBillingMustMatchOwner(t *testing.T) {
	t.Parallel()
	ownerID := uuid.MustParse("01900000-0000-7000-8000-000000000003")
	otherID := uuid.MustParse("01900000-0000-7000-8000-000000000004")
	viewer := contracts.Viewer{
		Memberships: map[uuid.UUID]contracts.OrganizationRole{
			ownerID: contracts.OrganizationRoleAdmin,
			otherID: contracts.OrganizationRoleMember,
		},
	}
	owner := &deckv1.Owner{
		Scope: deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION,
		OwnerId: &deckv1.Owner_OrganizationId{OrganizationId: &deckv1.UuidV7{
			Value: ownerID.String(),
		}},
	}
	billing := &deckv1.BillingSelection{
		OrganizationId: &deckv1.UuidV7{Value: otherID.String()},
	}
	if err := authorizeBilling(viewer, owner, billing); err == nil {
		t.Fatal("organization view accepted another organization's billing scope")
	}
	billing.OrganizationId.Value = ownerID.String()
	if err := authorizeBilling(viewer, owner, billing); err != nil {
		t.Fatalf("owning organization billing rejected: %v", err)
	}
}

type fixedETag string

func (etag fixedETag) ETag(uuid.UUID, uint64) string { return string(etag) }

func TestValidateExpectedRequiresMatchingETag(t *testing.T) {
	t.Parallel()
	resourceID := uuid.MustParse("01900000-0000-7000-8000-000000000005")
	if _, err := validateExpected(
		&deckv1.Revision{Value: 1}, resourceID, fixedETag("current")); err == nil {
		t.Fatal("revision without ETag was accepted")
	}
	if _, err := validateExpected(
		&deckv1.Revision{Value: 1, Etag: "guessed"},
		resourceID, fixedETag("current")); err == nil {
		t.Fatal("revision with a mismatched ETag was accepted")
	}
	revision, err := validateExpected(
		&deckv1.Revision{Value: 1, Etag: "current"},
		resourceID, fixedETag("current"))
	if err != nil || revision != 1 {
		t.Fatalf("matching ETag rejected: revision=%d err=%v", revision, err)
	}
}
