package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/contracts"
	"github.com/delinoio/oss/servers/delibase/internal/database"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPostgreSQLTeamAndInvitationPolicies(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	raw, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close(ctx)
	pseudonymizer, err := safelog.NewPseudonymizer(bytes.Repeat([]byte{0x39}, 32))
	if err != nil {
		t.Fatal(err)
	}
	dependencies := Dependencies{
		Store:          store,
		Clock:          contracts.SystemClock{},
		PolarCustomers: &fakePolarCustomers{},
		IDs:            defaultIDGenerator{},
		Pseudonymizer:  pseudonymizer,
	}
	accountService := NewAccount(dependencies)
	organizationService := NewOrganization(dependencies)
	teamService := NewTeam(dependencies)
	testID := uuidv7.MustNew().String()
	suffix := testID[len(testID)-12:]
	ownerSubject := "team-owner-" + testID
	ownerContext := authenticatedContext(ctx, ownerSubject)

	onboarding, err := accountService.CompleteOnboarding(
		ownerContext,
		connect.NewRequest(&delibasev1.CompleteOnboardingRequest{
			DisplayName:      "Team Owner",
			OrganizationName: "Team Policy Organization",
			OrganizationSlug: "team-policy-" + suffix,
			Idempotency:      idempotency("team-onboard-" + suffix),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	organizationID := onboarding.Msg.OrganizationId
	generalTeamID := onboarding.Msg.GeneralTeamId
	ownerAccountID := onboarding.Msg.Account.AccountId

	type seededAccount struct {
		id      uuid.UUID
		subject string
		context context.Context
	}
	seedAccount := func(label string, organizationRole string) seededAccount {
		t.Helper()
		account := seededAccount{
			id:      uuidv7.MustNew(),
			subject: label + "-" + testID,
		}
		account.context = authenticatedContext(ctx, account.subject)
		err := store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
			created, transactionErr := queries.EnsureAccount(
				ctx,
				dbgen.EnsureAccountParams{
					ID:           pgUUID(account.id),
					LogtoSubject: account.subject,
					DisplayName:  label,
				},
			)
			if transactionErr != nil || organizationRole == "" {
				return transactionErr
			}
			_, transactionErr = queries.CreateOrganizationMembership(
				ctx,
				dbgen.CreateOrganizationMembershipParams{
					OrganizationID: pgUUID(mustUUID(t, organizationID)),
					AccountID:      created.ID,
					Role:           organizationRole,
				},
			)
			return transactionErr
		})
		if err != nil {
			t.Fatal(err)
		}
		return account
	}
	organizationAdmin := seedAccount("Organization Admin", "admin")
	parentMember := seedAccount("Parent Member", "member")
	childMember := seedAccount("Child Member", "member")
	existingAdmin := seedAccount("Existing Admin", "admin")
	inviteeA := seedAccount("Invitee A", "")
	inviteeB := seedAccount("Invitee B", "")
	revokedInvitee := seedAccount("Revoked Invitee", "")
	expiredInvitee := seedAccount("Expired Invitee", "")

	createTeam := func(name string, parent *delibasev1.UuidV7) *delibasev1.Team {
		t.Helper()
		response, createErr := teamService.CreateTeam(
			ownerContext,
			connect.NewRequest(&delibasev1.CreateTeamRequest{
				OrganizationId: organizationID,
				ParentTeamId:   parent,
				Name:           name,
				Idempotency: idempotency(
					"create-team-" + uuidv7.MustNew().String(),
				),
			}),
		)
		if createErr != nil {
			t.Fatal(createErr)
		}
		return response.Msg.Team
	}

	root := createTeam("Root "+suffix, nil)
	levelTwo := createTeam("Level Two "+suffix, root.TeamId)
	levelThree := createTeam("Level Three "+suffix, levelTwo.TeamId)
	levelFour := createTeam("Level Four "+suffix, levelThree.TeamId)
	levelFive := createTeam("Level Five "+suffix, levelFour.TeamId)
	if levelFive.Depth != 4 {
		t.Fatalf("level five depth = %d, want 4", levelFive.Depth)
	}
	_, err = teamService.CreateTeam(
		ownerContext,
		connect.NewRequest(&delibasev1.CreateTeamRequest{
			OrganizationId: organizationID,
			ParentTeamId:   levelFive.TeamId,
			Name:           "Too Deep " + suffix,
			Idempotency:    idempotency("too-deep-" + suffix),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_TEAM_DEPTH_EXCEEDED,
	)
	_, err = teamService.MoveTeam(
		ownerContext,
		connect.NewRequest(&delibasev1.MoveTeamRequest{
			OrganizationId:  organizationID,
			TeamId:          root.TeamId,
			NewParentTeamId: levelThree.TeamId,
			Idempotency:     idempotency("cycle-" + suffix),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_TEAM_CYCLE,
	)
	movingRoot := createTeam("Moving Root "+suffix, nil)
	_ = createTeam("Moving Child "+suffix, movingRoot.TeamId)
	_, err = teamService.MoveTeam(
		ownerContext,
		connect.NewRequest(&delibasev1.MoveTeamRequest{
			OrganizationId:  organizationID,
			TeamId:          movingRoot.TeamId,
			NewParentTeamId: levelFive.TeamId,
			Idempotency:     idempotency("deep-move-" + suffix),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_TEAM_DEPTH_EXCEEDED,
	)

	setMembership := func(teamID *delibasev1.UuidV7, account seededAccount, role delibasev1.TeamRole, key string) {
		t.Helper()
		if _, setErr := teamService.SetTeamMembership(
			ownerContext,
			connect.NewRequest(&delibasev1.SetTeamMembershipRequest{
				OrganizationId: organizationID,
				TeamId:         teamID,
				AccountId:      &delibasev1.UuidV7{Value: account.id.String()},
				Role:           role,
				Idempotency:    idempotency(key + "-" + suffix),
			}),
		); setErr != nil {
			t.Fatal(setErr)
		}
	}
	setMembership(root.TeamId, parentMember, delibasev1.TeamRole_TEAM_ROLE_MEMBER, "parent-member")
	inherited, err := teamService.GetTeam(
		parentMember.context,
		connect.NewRequest(&delibasev1.GetTeamRequest{
			OrganizationId: organizationID,
			TeamId:         levelFive.TeamId,
		}),
	)
	if err != nil ||
		inherited.Msg.CallerAccess.EffectiveRole != delibasev1.TeamRole_TEAM_ROLE_MEMBER ||
		inherited.Msg.CallerAccess.Source !=
			delibasev1.TeamAccessSource_TEAM_ACCESS_SOURCE_ANCESTOR_MEMBERSHIP ||
		inherited.Msg.CallerAccess.SourceTeamId.Value != root.TeamId.Value {
		t.Fatalf("inherited access = %#v, %v", inherited, err)
	}
	_, err = teamService.GetTeam(
		parentMember.context,
		connect.NewRequest(&delibasev1.GetTeamRequest{
			OrganizationId: organizationID,
			TeamId:         generalTeamID,
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_TEAM_ACCESS_DENIED,
	)
	setMembership(root.TeamId, parentMember, delibasev1.TeamRole_TEAM_ROLE_ADMIN, "parent-admin")
	inheritedAdmin, err := teamService.GetTeam(
		parentMember.context,
		connect.NewRequest(&delibasev1.GetTeamRequest{
			OrganizationId: organizationID,
			TeamId:         levelFive.TeamId,
		}),
	)
	if err != nil ||
		inheritedAdmin.Msg.CallerAccess.EffectiveRole !=
			delibasev1.TeamRole_TEAM_ROLE_ADMIN ||
		inheritedAdmin.Msg.CallerAccess.Source !=
			delibasev1.TeamAccessSource_TEAM_ACCESS_SOURCE_ANCESTOR_MEMBERSHIP {
		t.Fatalf("inherited Team Admin access = %#v, %v", inheritedAdmin, err)
	}
	setMembership(levelFive.TeamId, childMember, delibasev1.TeamRole_TEAM_ROLE_MEMBER, "child-member")
	_, err = teamService.GetTeam(
		childMember.context,
		connect.NewRequest(&delibasev1.GetTeamRequest{
			OrganizationId: organizationID,
			TeamId:         root.TeamId,
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_TEAM_ACCESS_DENIED,
	)
	implicit, err := teamService.GetTeam(
		organizationAdmin.context,
		connect.NewRequest(&delibasev1.GetTeamRequest{
			OrganizationId: organizationID,
			TeamId:         levelFive.TeamId,
		}),
	)
	if err != nil ||
		implicit.Msg.CallerAccess.EffectiveRole != delibasev1.TeamRole_TEAM_ROLE_ADMIN ||
		implicit.Msg.CallerAccess.Source !=
			delibasev1.TeamAccessSource_TEAM_ACCESS_SOURCE_ORGANIZATION_ROLE {
		t.Fatalf("implicit admin access = %#v, %v", implicit, err)
	}

	firstPage, err := teamService.ListTeams(
		ownerContext,
		connect.NewRequest(&delibasev1.ListTeamsRequest{
			OrganizationId:     organizationID,
			IncludeDescendants: true,
			Page:               &delibasev1.PageRequest{PageSize: 2},
		}),
	)
	if err != nil || len(firstPage.Msg.Teams) != 2 ||
		firstPage.Msg.Page.NextCursor == "" {
		t.Fatalf("first team page = %#v, %v", firstPage, err)
	}
	secondPage, err := teamService.ListTeams(
		ownerContext,
		connect.NewRequest(&delibasev1.ListTeamsRequest{
			OrganizationId:     organizationID,
			IncludeDescendants: true,
			Page: &delibasev1.PageRequest{
				PageSize: 2,
				Cursor:   firstPage.Msg.Page.NextCursor,
			},
		}),
	)
	if err != nil || len(secondPage.Msg.Teams) == 0 ||
		secondPage.Msg.Teams[0].TeamId.Value == firstPage.Msg.Teams[1].TeamId.Value {
		t.Fatalf("second team page = %#v, %v", secondPage, err)
	}

	_, err = teamService.UpdateTeam(
		ownerContext,
		connect.NewRequest(&delibasev1.UpdateTeamRequest{
			OrganizationId: organizationID,
			TeamId:         generalTeamID,
			Name:           "Renamed General",
			Idempotency:    idempotency("rename-general-" + suffix),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_GENERAL_TEAM_PROTECTED,
	)
	_, err = teamService.DeleteTeamSubtree(
		ownerContext,
		connect.NewRequest(&delibasev1.DeleteTeamSubtreeRequest{
			OrganizationId: organizationID,
			TeamId:         generalTeamID,
			ConfirmSubtree: true,
			Idempotency:    idempotency("delete-general-" + suffix),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_GENERAL_TEAM_PROTECTED,
	)

	otherOrganization, err := organizationService.CreateOrganization(
		ownerContext,
		connect.NewRequest(&delibasev1.CreateOrganizationRequest{
			Name:        "Other Organization",
			Slug:        "other-team-" + suffix,
			Idempotency: idempotency("other-org-" + suffix),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	otherTeam := createTeamForOrganization(
		t,
		teamService,
		ownerContext,
		otherOrganization.Msg.Organization.OrganizationId,
		"Other Team "+suffix,
		"other-team-create-"+suffix,
	)
	_, err = teamService.GetTeam(
		ownerContext,
		connect.NewRequest(&delibasev1.GetTeamRequest{
			OrganizationId: organizationID,
			TeamId:         otherTeam.TeamId,
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeNotFound,
		delibasev1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND,
	)
	_, err = teamService.MoveTeam(
		ownerContext,
		connect.NewRequest(&delibasev1.MoveTeamRequest{
			OrganizationId:  organizationID,
			TeamId:          root.TeamId,
			NewParentTeamId: otherTeam.TeamId,
			Idempotency:     idempotency("cross-org-move-" + suffix),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeInvalidArgument,
		delibasev1.ErrorReason_ERROR_REASON_TEAM_CROSS_ORGANIZATION_PARENT,
	)

	deleteRoot := createTeam("Delete Root "+suffix, nil)
	deleteChild := createTeam("Delete Child "+suffix, deleteRoot.TeamId)
	setMembership(
		deleteChild.TeamId,
		parentMember,
		delibasev1.TeamRole_TEAM_ROLE_MEMBER,
		"delete-child-member",
	)
	_, err = teamService.DeleteTeamSubtree(
		ownerContext,
		connect.NewRequest(&delibasev1.DeleteTeamSubtreeRequest{
			OrganizationId: organizationID,
			TeamId:         deleteRoot.TeamId,
			ConfirmSubtree: false,
			Idempotency:    idempotency("unconfirmed-delete-" + suffix),
		}),
	)
	requireConnectCode(t, err, connect.CodeInvalidArgument)
	deleted, err := teamService.DeleteTeamSubtree(
		ownerContext,
		connect.NewRequest(&delibasev1.DeleteTeamSubtreeRequest{
			OrganizationId: organizationID,
			TeamId:         deleteRoot.TeamId,
			ConfirmSubtree: true,
			Idempotency:    idempotency("confirmed-delete-" + suffix),
		}),
	)
	if err != nil || len(deleted.Msg.DeletedTeamIds) != 2 {
		t.Fatalf("subtree deletion = %#v, %v", deleted, err)
	}
	var deletedMemberships int
	if err = raw.QueryRow(ctx, `
		SELECT count(*)
		FROM team_memberships
		WHERE team_id IN ($1, $2)
	`, mustUUID(t, deleteRoot.TeamId), mustUUID(t, deleteChild.TeamId)).
		Scan(&deletedMemberships); err != nil || deletedMemberships != 0 {
		t.Fatalf("deleted direct memberships = %d, %v", deletedMemberships, err)
	}
	var snapshotCount int
	if err = raw.QueryRow(ctx, `
		SELECT count(*)
		FROM audit_events
		WHERE event_type = 'team.deleted'
		  AND team_id IN ($1, $2)
		  AND team_name_snapshot IN ($3, $4)
	`, mustUUID(t, deleteRoot.TeamId), mustUUID(t, deleteChild.TeamId),
		deleteRoot.Name, deleteChild.Name).Scan(&snapshotCount); err != nil ||
		snapshotCount != 2 {
		t.Fatalf("deleted team snapshots = %d, %v", snapshotCount, err)
	}

	_, err = organizationService.CreateOrganizationInvitation(
		ownerContext,
		connect.NewRequest(&delibasev1.CreateOrganizationInvitationRequest{
			OrganizationId:   organizationID,
			OrganizationRole: delibasev1.OrganizationRole_ORGANIZATION_ROLE_OWNER,
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeInvalidArgument,
		delibasev1.ErrorReason_ERROR_REASON_INVITATION_ROLE_INVALID,
	)
	_, err = organizationService.CreateOrganizationInvitation(
		ownerContext,
		connect.NewRequest(&delibasev1.CreateOrganizationInvitationRequest{
			OrganizationId:   organizationID,
			OrganizationRole: delibasev1.OrganizationRole_ORGANIZATION_ROLE_MEMBER,
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeInvalidArgument,
		delibasev1.ErrorReason_ERROR_REASON_INVITATION_TEAM_REQUIRED,
	)
	_, err = organizationService.CreateOrganizationInvitation(
		ownerContext,
		connect.NewRequest(&delibasev1.CreateOrganizationInvitationRequest{
			OrganizationId:   organizationID,
			OrganizationRole: delibasev1.OrganizationRole_ORGANIZATION_ROLE_MEMBER,
			TeamId:           otherTeam.TeamId,
			TeamRole:         delibasev1.TeamRole_TEAM_ROLE_MEMBER,
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeNotFound,
		delibasev1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND,
	)

	invitation, err := organizationService.CreateOrganizationInvitation(
		ownerContext,
		connect.NewRequest(&delibasev1.CreateOrganizationInvitationRequest{
			OrganizationId:   organizationID,
			OrganizationRole: delibasev1.OrganizationRole_ORGANIZATION_ROLE_MEMBER,
			TeamId:           root.TeamId,
			TeamRole:         delibasev1.TeamRole_TEAM_ROLE_MEMBER,
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	token := invitation.Msg.BearerToken.Token
	expectedHash, err := invitationTokenHash(token)
	if err != nil {
		t.Fatal(err)
	}
	var storedHash []byte
	if err = raw.QueryRow(ctx, `
		SELECT token_hash
		FROM organization_invitations
		WHERE id = $1
	`, mustUUID(t, invitation.Msg.Invitation.InvitationId)).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedHash, expectedHash) ||
		bytes.Contains(storedHash, []byte(token)) || len(storedHash) != sha256Size {
		t.Fatalf("stored invitation token material is not a SHA-256 hash")
	}
	validFor := invitation.Msg.Invitation.ExpiresAt.AsTime().
		Sub(invitation.Msg.Invitation.CreatedAt.AsTime())
	if validFor != 7*24*time.Hour {
		t.Fatalf("invitation validity = %s", validFor)
	}

	firstTimeSubject := "first-time-invitee-" + testID
	firstTimeContext := authenticatedContext(ctx, firstTimeSubject)
	preview, err := organizationService.GetOrganizationInvitation(
		firstTimeContext,
		connect.NewRequest(&delibasev1.GetOrganizationInvitationRequest{
			BearerToken: invitation.Msg.BearerToken,
		}),
	)
	if err != nil || preview.Msg.OrganizationName != "Team Policy Organization" {
		t.Fatalf("first-time invitation preview = %#v, %v", preview, err)
	}
	firstTimeAcceptance, err := organizationService.AcceptOrganizationInvitation(
		firstTimeContext,
		connect.NewRequest(&delibasev1.AcceptOrganizationInvitationRequest{
			BearerToken: invitation.Msg.BearerToken,
			Idempotency: idempotency("accept-first-time-" + suffix),
		}),
	)
	if err != nil || firstTimeAcceptance.Msg.Member.Role !=
		delibasev1.OrganizationRole_ORGANIZATION_ROLE_MEMBER {
		t.Fatalf("first-time invitation acceptance = %#v, %v", firstTimeAcceptance, err)
	}
	firstTimeState, err := accountService.GetAccountState(
		firstTimeContext,
		connect.NewRequest(&delibasev1.GetAccountStateRequest{}),
	)
	if err != nil || firstTimeState.Msg.Account == nil ||
		firstTimeState.Msg.Account.Status !=
			delibasev1.AccountStatus_ACCOUNT_STATUS_ACTIVE ||
		firstTimeState.Msg.OnboardingRequired ||
		len(firstTimeState.Msg.Organizations) != 1 {
		t.Fatalf("first-time invitation account state = %#v, %v", firstTimeState, err)
	}

	accept := func(account seededAccount, key string) *delibasev1.AcceptOrganizationInvitationResponse {
		t.Helper()
		response, acceptErr := organizationService.AcceptOrganizationInvitation(
			account.context,
			connect.NewRequest(&delibasev1.AcceptOrganizationInvitationRequest{
				BearerToken: invitation.Msg.BearerToken,
				Idempotency: idempotency(key + "-" + suffix),
			}),
		)
		if acceptErr != nil {
			t.Fatal(acceptErr)
		}
		return response.Msg
	}
	if accepted := accept(inviteeA, "accept-a"); accepted.Member.Role !=
		delibasev1.OrganizationRole_ORGANIZATION_ROLE_MEMBER {
		t.Fatalf("invitee A role = %s", accepted.Member.Role)
	}
	if accepted := accept(inviteeB, "accept-b"); accepted.Member.Role !=
		delibasev1.OrganizationRole_ORGANIZATION_ROLE_MEMBER {
		t.Fatalf("invitee B role = %s", accepted.Member.Role)
	}
	if accepted := accept(existingAdmin, "accept-existing"); accepted.Member.Role !=
		delibasev1.OrganizationRole_ORGANIZATION_ROLE_ADMIN {
		t.Fatalf("existing role changed to %s", accepted.Member.Role)
	}
	setMembership(root.TeamId, existingAdmin, delibasev1.TeamRole_TEAM_ROLE_ADMIN, "existing-direct-admin")
	if accepted := accept(existingAdmin, "accept-existing-again"); accepted.Member.Role !=
		delibasev1.OrganizationRole_ORGANIZATION_ROLE_ADMIN {
		t.Fatalf("existing role changed on repeat to %s", accepted.Member.Role)
	}
	direct, err := store.Queries().GetTeamMembership(
		ctx,
		dbgen.GetTeamMembershipParams{
			OrganizationID: pgUUID(mustUUID(t, organizationID)),
			TeamID:         pgUUID(mustUUID(t, root.TeamId)),
			AccountID:      pgUUID(existingAdmin.id),
		},
	)
	if err != nil || direct.Role != "admin" {
		t.Fatalf("existing direct team role = %q, %v", direct.Role, err)
	}

	revocable, err := organizationService.CreateOrganizationInvitation(
		ownerContext,
		connect.NewRequest(&delibasev1.CreateOrganizationInvitationRequest{
			OrganizationId:   organizationID,
			OrganizationRole: delibasev1.OrganizationRole_ORGANIZATION_ROLE_ADMIN,
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := organizationService.RevokeOrganizationInvitation(
		ownerContext,
		connect.NewRequest(&delibasev1.RevokeOrganizationInvitationRequest{
			OrganizationId: organizationID,
			InvitationId:   revocable.Msg.Invitation.InvitationId,
			Idempotency:    idempotency("revoke-" + suffix),
		}),
	)
	if err != nil ||
		revoked.Msg.Invitation.Status !=
			delibasev1.InvitationStatus_INVITATION_STATUS_REVOKED {
		t.Fatalf("revocation = %#v, %v", revoked, err)
	}
	replayedRevocation, err := organizationService.RevokeOrganizationInvitation(
		ownerContext,
		connect.NewRequest(&delibasev1.RevokeOrganizationInvitationRequest{
			OrganizationId: organizationID,
			InvitationId:   revocable.Msg.Invitation.InvitationId,
			Idempotency:    idempotency("revoke-" + suffix),
		}),
	)
	if err != nil || !replayedRevocation.Msg.Idempotency.Replayed {
		t.Fatalf("revocation replay = %#v, %v", replayedRevocation, err)
	}
	_, err = organizationService.AcceptOrganizationInvitation(
		revokedInvitee.context,
		connect.NewRequest(&delibasev1.AcceptOrganizationInvitationRequest{
			BearerToken: revocable.Msg.BearerToken,
			Idempotency: idempotency("accept-revoked-" + suffix),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_INVITATION_REVOKED,
	)

	expiredFirst := uuidv7.MustNew()
	expiredSecond := uuidv7.MustNew()
	expiredRaw := append(expiredFirst[:], expiredSecond[:]...)
	expiredToken := base64.RawURLEncoding.EncodeToString(expiredRaw)
	expiredHash, err := invitationTokenHash(expiredToken)
	if err != nil {
		t.Fatal(err)
	}
	expiredID := uuidv7.MustNew()
	if _, err = raw.Exec(ctx, `
		INSERT INTO organization_invitations (
			id, organization_id, token_hash, organization_role,
			created_by_account_id, created_at, expires_at
		) VALUES (
			$1, $2, $3, 'admin', $4,
			statement_timestamp() - interval '8 days',
			statement_timestamp() - interval '1 day'
		)
	`, expiredID, mustUUID(t, organizationID), expiredHash,
		mustUUID(t, ownerAccountID)); err != nil {
		t.Fatal(err)
	}
	_, err = organizationService.GetOrganizationInvitation(
		expiredInvitee.context,
		connect.NewRequest(&delibasev1.GetOrganizationInvitationRequest{
			BearerToken: &delibasev1.InvitationBearerToken{Token: expiredToken},
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_INVITATION_EXPIRED,
	)
	_, err = organizationService.AcceptOrganizationInvitation(
		expiredInvitee.context,
		connect.NewRequest(&delibasev1.AcceptOrganizationInvitationRequest{
			BearerToken: &delibasev1.InvitationBearerToken{Token: expiredToken},
			Idempotency: idempotency("accept-expired-" + suffix),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_INVITATION_EXPIRED,
	)

	invitationPage, err := organizationService.ListOrganizationInvitations(
		ownerContext,
		connect.NewRequest(&delibasev1.ListOrganizationInvitationsRequest{
			OrganizationId: organizationID,
			Page:           &delibasev1.PageRequest{PageSize: 1},
		}),
	)
	if err != nil || len(invitationPage.Msg.Invitations) != 1 ||
		invitationPage.Msg.Page.NextCursor == "" {
		t.Fatalf("invitation page = %#v, %v", invitationPage, err)
	}

	deletionRaceInvitation, err := organizationService.CreateOrganizationInvitation(
		ownerContext,
		connect.NewRequest(&delibasev1.CreateOrganizationInvitationRequest{
			OrganizationId:   otherOrganization.Msg.Organization.OrganizationId,
			OrganizationRole: delibasev1.OrganizationRole_ORGANIZATION_ROLE_ADMIN,
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	deletionRaceMembershipParams := dbgen.ListTeamMembershipsParams{
		OrganizationID: pgUUID(mustUUID(
			t, otherOrganization.Msg.Organization.OrganizationId,
		)),
		TeamID:    pgUUID(mustUUID(t, otherOrganization.Msg.GeneralTeamId)),
		AfterID:   pgUUID(uuid.Nil),
		PageLimit: 2,
	}
	deletionRaceMemberships, err := store.Queries().ListTeamMemberships(
		ctx, deletionRaceMembershipParams,
	)
	if err != nil || len(deletionRaceMemberships) == 0 {
		t.Fatalf(
			"memberships before organization deletion = %#v, %v",
			deletionRaceMemberships,
			err,
		)
	}
	blockingTransaction, err := raw.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blockingTransaction.Rollback(ctx) }()
	if _, err = blockingTransaction.Exec(ctx, `
		SELECT id
		FROM organizations
		WHERE id = $1
		FOR UPDATE
	`, mustUUID(t, otherOrganization.Msg.Organization.OrganizationId)); err != nil {
		t.Fatal(err)
	}
	raceResult := make(chan error, 1)
	go func() {
		_, acceptErr := organizationService.AcceptOrganizationInvitation(
			authenticatedContext(ctx, "deletion-race-invitee-"+testID),
			connect.NewRequest(&delibasev1.AcceptOrganizationInvitationRequest{
				BearerToken: deletionRaceInvitation.Msg.BearerToken,
				Idempotency: idempotency("accept-deletion-race-" + suffix),
			}),
		)
		raceResult <- acceptErr
	}()
	select {
	case acceptErr := <-raceResult:
		t.Fatalf("invitation acceptance bypassed organization lock: %v", acceptErr)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err = blockingTransaction.Exec(ctx, `
		UPDATE organizations
		SET deleted_at = transaction_timestamp(),
		    updated_at = transaction_timestamp()
		WHERE id = $1
	`, mustUUID(t, otherOrganization.Msg.Organization.OrganizationId)); err != nil {
		t.Fatal(err)
	}
	if err = blockingTransaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	deletionRaceMemberships, err = store.Queries().ListTeamMemberships(
		ctx, deletionRaceMembershipParams,
	)
	if err != nil || len(deletionRaceMemberships) != 0 {
		t.Fatalf(
			"memberships after organization deletion = %#v, %v",
			deletionRaceMemberships,
			err,
		)
	}
	select {
	case acceptErr := <-raceResult:
		requireConnectReason(
			t,
			acceptErr,
			connect.CodeNotFound,
			delibasev1.ErrorReason_ERROR_REASON_INVITATION_INVALID,
		)
	case <-time.After(5 * time.Second):
		t.Fatal("invitation acceptance did not resume after organization deletion")
	}
	if _, err = raw.Exec(ctx, `
		UPDATE audit_events
		SET team_name_snapshot = 'rewritten'
		WHERE team_id = $1
	`, mustUUID(t, deleteRoot.TeamId)); err == nil {
		t.Fatal("immutable team audit snapshot was updated")
	}
}

const sha256Size = 32

func createTeamForOrganization(
	t *testing.T,
	service *Team,
	ctx context.Context,
	organizationID *delibasev1.UuidV7,
	name string,
	key string,
) *delibasev1.Team {
	t.Helper()
	response, err := service.CreateTeam(
		ctx,
		connect.NewRequest(&delibasev1.CreateTeamRequest{
			OrganizationId: organizationID,
			Name:           name,
			Idempotency:    idempotency(key),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.Team
}
