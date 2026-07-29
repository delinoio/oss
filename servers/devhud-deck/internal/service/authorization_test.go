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
