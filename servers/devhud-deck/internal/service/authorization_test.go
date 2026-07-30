package service

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database"
	deckgithub "github.com/delinoio/oss/servers/devhud-deck/internal/github"
	"github.com/delinoio/oss/servers/devhud-deck/internal/security"
	"github.com/google/uuid"
)

type deniedRepositories struct{}

type reauthenticationRepositories struct {
	deniedRepositories
}

func (deniedRepositories) CanReadRepository(
	context.Context,
	contracts.Viewer,
	*deckv1.Owner,
	string,
	string,
) (bool, error) {
	return false, nil
}

func (deniedRepositories) ReadableRepositoryHashes(
	context.Context,
	contracts.Viewer,
	*deckv1.Owner,
	contracts.RepositoryHashKind,
	[][32]byte,
) (map[[32]byte]struct{}, error) {
	return nil, nil
}

func (reauthenticationRepositories) ReadableRepositoryHashes(
	context.Context,
	contracts.Viewer,
	*deckv1.Owner,
	contracts.RepositoryHashKind,
	[][32]byte,
) (map[[32]byte]struct{}, error) {
	return nil, deckgithub.ErrReauthenticationRequired
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

func TestRepositoryRepairRequiresExactRemovalEvidence(t *testing.T) {
	t.Parallel()
	readableHash := security.Digest([]byte("readable"))
	removedHash := security.Digest([]byte("removed"))
	inaccessibleHash := security.Digest([]byte("inaccessible"))
	readable := map[[32]byte]struct{}{readableHash: {}}
	removed := map[[32]byte]struct{}{removedHash: {}}
	if !repositoryHashesAuthorized(
		[][32]byte{readableHash, removedHash}, readable, removed) {
		t.Fatal("exact removed repository evidence was rejected")
	}
	if repositoryHashesAuthorized(
		[][32]byte{readableHash, inaccessibleHash}, readable, removed) {
		t.Fatal("unproven repository removal was authorized")
	}
	if repositoryHashesAuthorized(
		[][32]byte{removedHash}, readable, nil) {
		t.Fatal("repository removal bypassed normal view reads")
	}
}

func TestDisconnectedViewDefinitionDoesNotRequireProviderCredentials(
	t *testing.T,
) {
	t.Parallel()
	hasher, err := security.NewHasher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	accountID := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	owner := &deckv1.Owner{
		Scope: deckv1.OwnerScope_OWNER_SCOPE_PERSONAL,
		OwnerId: &deckv1.Owner_AccountId{AccountId: &deckv1.UuidV7{
			Value: accountID.String(),
		}},
	}
	service := &View{dependencies: Dependencies{
		Repositories: deniedRepositories{},
		Hasher:       hasher,
	}.withDefaults()}
	authorize := service.viewDefinitionAuthorizer(
		context.Background(), contracts.Viewer{AccountID: accountID}, false)
	err = authorize(database.ViewAuthorization{
		Owner:           owner,
		ConnectionState: deckv1.ConnectionState_CONNECTION_STATE_DISCONNECTED,
	})
	if err != nil {
		t.Fatalf("disconnected view definition error = %v", err)
	}
	repositoryHash := hasher.Sum(
		"view-repository", "secret\x00project")
	err = authorize(database.ViewAuthorization{
		Owner:              owner,
		ConnectionState:    deckv1.ConnectionState_CONNECTION_STATE_CONNECTED,
		RepositoryHashes:   [][32]byte{repositoryHash},
		HasRepositoryIndex: true,
	})
	if !errors.Is(err, database.ErrViewNotVisible) {
		t.Fatalf("connected view authorization error = %v", err)
	}
	if connect.CodeOf(mapAuthorizedViewError(err)) != connect.CodePermissionDenied {
		t.Fatalf("mapped connected view authorization error = %v", err)
	}
	err = authorize(database.ViewAuthorization{
		Owner:           owner,
		ConnectionState: deckv1.ConnectionState_CONNECTION_STATE_CONNECTED,
	})
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("unindexed connected view error = %v", err)
	}
	manage := service.viewDefinitionAuthorizer(
		context.Background(), contracts.Viewer{AccountID: accountID}, true)
	err = manage(database.ViewAuthorization{
		Owner:              owner,
		ConnectionState:    deckv1.ConnectionState_CONNECTION_STATE_CONNECTED,
		RepositoryHashes:   [][32]byte{repositoryHash},
		HasRepositoryIndex: true,
	})
	if !errors.Is(err, database.ErrViewNotVisible) {
		t.Fatalf("view manager bypassed repository authorization: %v", err)
	}
}

func TestDisconnectedOrganizationViewDefinitionIsManagerOnly(t *testing.T) {
	t.Parallel()
	organizationID := uuid.MustParse("01900000-0000-7000-8000-000000000003")
	owner := &deckv1.Owner{
		Scope: deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION,
		OwnerId: &deckv1.Owner_OrganizationId{
			OrganizationId: &deckv1.UuidV7{Value: organizationID.String()},
		},
	}
	service := &View{dependencies: Dependencies{}.withDefaults()}
	authorization := database.ViewAuthorization{
		Owner:           owner,
		ConnectionState: deckv1.ConnectionState_CONNECTION_STATE_DISCONNECTED,
	}
	member := contracts.Viewer{
		Memberships: map[uuid.UUID]contracts.OrganizationRole{
			organizationID: contracts.OrganizationRoleMember,
		},
	}
	err := service.viewDefinitionAuthorizer(
		context.Background(), member, false,
	)(authorization)
	if !errors.Is(err, database.ErrViewNotVisible) {
		t.Fatalf("organization member authorization error = %v", err)
	}
	manager := contracts.Viewer{
		Memberships: map[uuid.UUID]contracts.OrganizationRole{
			organizationID: contracts.OrganizationRoleAdmin,
		},
	}
	err = service.viewDefinitionAuthorizer(
		context.Background(), manager, false,
	)(authorization)
	if err != nil {
		t.Fatalf("organization manager authorization error = %v", err)
	}
}

func TestConnectedViewWithoutRepositoryQualifiersRequiresProviderCredentials(
	t *testing.T,
) {
	t.Parallel()
	accountID := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	organizationID := uuid.MustParse("01900000-0000-7000-8000-000000000003")
	service := &View{dependencies: Dependencies{
		Repositories: reauthenticationRepositories{},
	}.withDefaults()}
	authorize := service.viewDefinitionAuthorizer(
		context.Background(),
		contracts.Viewer{
			AccountID: accountID,
			Memberships: map[uuid.UUID]contracts.OrganizationRole{
				organizationID: contracts.OrganizationRoleMember,
			},
		},
		false,
	)
	err := authorize(database.ViewAuthorization{
		Owner: &deckv1.Owner{
			Scope: deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION,
			OwnerId: &deckv1.Owner_OrganizationId{
				OrganizationId: &deckv1.UuidV7{Value: organizationID.String()},
			},
		},
		ConnectionState:    deckv1.ConnectionState_CONNECTION_STATE_CONNECTED,
		HasRepositoryIndex: true,
	})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("connected zero-repository view authorization error = %v", err)
	}
	manage := service.viewDefinitionAuthorizer(
		context.Background(),
		contracts.Viewer{
			AccountID: accountID,
			Memberships: map[uuid.UUID]contracts.OrganizationRole{
				organizationID: contracts.OrganizationRoleOwner,
			},
		},
		true,
	)
	err = manage(database.ViewAuthorization{
		Owner: &deckv1.Owner{
			Scope: deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION,
			OwnerId: &deckv1.Owner_OrganizationId{
				OrganizationId: &deckv1.UuidV7{Value: organizationID.String()},
			},
		},
		ConnectionState:    deckv1.ConnectionState_CONNECTION_STATE_CONNECTED,
		HasRepositoryIndex: true,
	})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("view manager without credentials authorization error = %v", err)
	}
}

func TestRepositoryAuthorizationDefaultsToDeny(t *testing.T) {
	t.Parallel()
	dependencies := Dependencies{}.withDefaults()
	allowed, err := dependencies.Repositories.CanReadRepository(
		context.Background(), contracts.Viewer{}, nil, "secret", "project")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("missing repository authorizer failed open")
	}
}

func TestOwnerDeletionReplayKeyScopesCallerAndOwner(t *testing.T) {
	t.Parallel()
	hasher, err := security.NewHasher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	service := &View{dependencies: Dependencies{Hasher: hasher}.withDefaults()}
	ownerID := uuid.MustParse("01900000-0000-7000-8000-000000000003")
	requested := uuid.MustParse("01900000-0000-7000-8000-000000000004")
	first := service.ownerDeletionReplayKey(
		"subject-1", deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION,
		ownerID, requested)
	replay := service.ownerDeletionReplayKey(
		"subject-1", deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION,
		ownerID, requested)
	otherCaller := service.ownerDeletionReplayKey(
		"subject-2", deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION,
		ownerID, requested)
	otherOwner := service.ownerDeletionReplayKey(
		"subject-1", deckv1.OwnerScope_OWNER_SCOPE_PERSONAL,
		ownerID, requested)
	if first != replay || first == otherCaller || first == otherOwner {
		t.Fatalf("scoped deletion keys = %s %s %s %s",
			first, replay, otherCaller, otherOwner)
	}
	if first.Version() != 7 || first.Variant() != uuid.RFC4122 {
		t.Fatalf("scoped deletion key is not UUID v7: %s", first)
	}
}

func TestOrganizationBillingMustMatchOwner(t *testing.T) {
	t.Parallel()
	ownerID := uuid.MustParse("01900000-0000-7000-8000-000000000003")
	otherID := uuid.MustParse("01900000-0000-7000-8000-000000000004")
	teamID := uuid.MustParse("01900000-0000-7000-8000-000000000005")
	viewer := contracts.Viewer{
		Memberships: map[uuid.UUID]contracts.OrganizationRole{
			ownerID: contracts.OrganizationRoleAdmin,
			otherID: contracts.OrganizationRoleMember,
		},
		TeamMemberships: map[uuid.UUID]map[uuid.UUID]struct{}{
			ownerID: {teamID: {}},
			otherID: {teamID: {}},
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
		TeamId:         &deckv1.UuidV7{Value: teamID.String()},
	}
	if err := authorizeBilling(viewer, owner, billing); err == nil {
		t.Fatal("organization view accepted another organization's billing scope")
	}
	billing.OrganizationId.Value = ownerID.String()
	if err := authorizeBilling(viewer, owner, billing); err != nil {
		t.Fatalf("owning organization billing rejected: %v", err)
	}
}

func TestBillingRequiresAnExplicitOrganizationAndTeam(t *testing.T) {
	t.Parallel()
	organizationID := uuid.MustParse("01900000-0000-7000-8000-000000000006")
	teamID := uuid.MustParse("01900000-0000-7000-8000-000000000007")
	viewer := contracts.Viewer{
		Memberships: map[uuid.UUID]contracts.OrganizationRole{
			organizationID: contracts.OrganizationRoleMember,
		},
		TeamMemberships: map[uuid.UUID]map[uuid.UUID]struct{}{
			organizationID: {teamID: {}},
		},
	}
	for _, billing := range []*deckv1.BillingSelection{
		nil,
		{},
		{OrganizationId: &deckv1.UuidV7{Value: organizationID.String()}},
		{TeamId: &deckv1.UuidV7{Value: teamID.String()}},
	} {
		if err := authorizeBilling(viewer, nil, billing); connect.CodeOf(err) !=
			connect.CodeInvalidArgument {
			t.Fatalf("incomplete billing selection accepted: %#v, %v",
				billing, err)
		}
	}
	if err := authorizeBilling(viewer, nil, &deckv1.BillingSelection{
		OrganizationId: &deckv1.UuidV7{Value: organizationID.String()},
		TeamId:         &deckv1.UuidV7{Value: teamID.String()},
	}); err != nil {
		t.Fatalf("complete authorized billing selection rejected: %v", err)
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
