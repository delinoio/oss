package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/contracts"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/redact"
	"github.com/delinoio/oss/servers/internal/requestmeta"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultPageSize = 50
	maximumPageSize = 100
)

var (
	slugPattern           = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$`)
)

func userSubject(ctx context.Context) (string, error) {
	principal, ok := auth.PrincipalFromContext(ctx)
	if !ok || principal.User == nil {
		return "", serviceError(
			connect.CodeUnauthenticated,
			delibasev1.ErrorReason_ERROR_REASON_AUTHENTICATION_REQUIRED,
		)
	}
	subject := strings.TrimSpace(principal.User.Subject)
	userID := strings.TrimSpace(principal.User.UserID)
	if principal.User.Type != auth.TokenTypeUser || subject == "" ||
		len(subject) > 255 || (userID != "" && userID != subject) {
		return "", serviceError(
			connect.CodeUnauthenticated,
			delibasev1.ErrorReason_ERROR_REASON_AUTHENTICATION_INVALID,
		)
	}
	return subject, nil
}

func activeAccount(
	ctx context.Context,
	queries *dbgen.Queries,
	subject string,
) (dbgen.Account, error) {
	account, err := queries.LockAccountByLogtoSubject(ctx, subject)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dbgen.Account{}, serviceError(
				connect.CodeFailedPrecondition,
				delibasev1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND,
			)
		}
		return dbgen.Account{}, databaseError(err)
	}
	if account.Status != "active" {
		return dbgen.Account{}, serviceError(
			connect.CodePermissionDenied,
			delibasev1.ErrorReason_ERROR_REASON_RESOURCE_DELETED,
		)
	}
	return account, nil
}

func organizationRole(value string) delibasev1.OrganizationRole {
	switch value {
	case "owner":
		return delibasev1.OrganizationRole_ORGANIZATION_ROLE_OWNER
	case "admin":
		return delibasev1.OrganizationRole_ORGANIZATION_ROLE_ADMIN
	case "member":
		return delibasev1.OrganizationRole_ORGANIZATION_ROLE_MEMBER
	default:
		return delibasev1.OrganizationRole_ORGANIZATION_ROLE_UNSPECIFIED
	}
}

func organizationRoleName(value delibasev1.OrganizationRole) (string, bool) {
	switch value {
	case delibasev1.OrganizationRole_ORGANIZATION_ROLE_OWNER:
		return "owner", true
	case delibasev1.OrganizationRole_ORGANIZATION_ROLE_ADMIN:
		return "admin", true
	case delibasev1.OrganizationRole_ORGANIZATION_ROLE_MEMBER:
		return "member", true
	default:
		return "", false
	}
}

func teamRole(value string) delibasev1.TeamRole {
	switch value {
	case "admin":
		return delibasev1.TeamRole_TEAM_ROLE_ADMIN
	case "member":
		return delibasev1.TeamRole_TEAM_ROLE_MEMBER
	default:
		return delibasev1.TeamRole_TEAM_ROLE_UNSPECIFIED
	}
}

func teamRoleName(value delibasev1.TeamRole) (string, bool) {
	switch value {
	case delibasev1.TeamRole_TEAM_ROLE_ADMIN:
		return "admin", true
	case delibasev1.TeamRole_TEAM_ROLE_MEMBER:
		return "member", true
	default:
		return "", false
	}
}

func teamAccessSource(value string) delibasev1.TeamAccessSource {
	switch value {
	case "organization_role":
		return delibasev1.TeamAccessSource_TEAM_ACCESS_SOURCE_ORGANIZATION_ROLE
	case "direct_membership":
		return delibasev1.TeamAccessSource_TEAM_ACCESS_SOURCE_DIRECT_MEMBERSHIP
	case "ancestor_membership":
		return delibasev1.TeamAccessSource_TEAM_ACCESS_SOURCE_ANCESTOR_MEMBERSHIP
	default:
		return delibasev1.TeamAccessSource_TEAM_ACCESS_SOURCE_UNSPECIFIED
	}
}

func invitationStatus(value string) delibasev1.InvitationStatus {
	switch value {
	case "active":
		return delibasev1.InvitationStatus_INVITATION_STATUS_ACTIVE
	case "revoked":
		return delibasev1.InvitationStatus_INVITATION_STATUS_REVOKED
	case "expired":
		return delibasev1.InvitationStatus_INVITATION_STATUS_EXPIRED
	default:
		return delibasev1.InvitationStatus_INVITATION_STATUS_UNSPECIFIED
	}
}

func accountStatus(value string) delibasev1.AccountStatus {
	switch value {
	case "active":
		return delibasev1.AccountStatus_ACCOUNT_STATUS_ACTIVE
	case "disabled":
		return delibasev1.AccountStatus_ACCOUNT_STATUS_DISABLED
	case "deleted":
		return delibasev1.AccountStatus_ACCOUNT_STATUS_DELETED
	default:
		return delibasev1.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED
	}
}

func accountMessage(row dbgen.Account) *delibasev1.Account {
	return &delibasev1.Account{
		AccountId:   uuidMessage(row.ID),
		Status:      accountStatus(row.Status),
		DisplayName: row.DisplayName,
		CreatedAt:   timestamp(row.CreatedAt),
		UpdatedAt:   timestamp(row.UpdatedAt),
	}
}

func organizationMessage(row dbgen.Organization) *delibasev1.Organization {
	status := delibasev1.OrganizationStatus_ORGANIZATION_STATUS_ACTIVE
	if row.DeletedAt.Valid {
		status = delibasev1.OrganizationStatus_ORGANIZATION_STATUS_DELETION_PENDING
	}
	return &delibasev1.Organization{
		OrganizationId: uuidMessage(row.ID),
		Name:           row.Name,
		Slug:           row.Slug,
		Status:         status,
		CreatedAt:      timestamp(row.CreatedAt),
		UpdatedAt:      timestamp(row.UpdatedAt),
	}
}

func memberMessage(
	accountID pgtype.UUID,
	displayName string,
	role string,
	joinedAt pgtype.Timestamptz,
) *delibasev1.OrganizationMember {
	return &delibasev1.OrganizationMember{
		AccountId:   uuidMessage(accountID),
		DisplayName: displayName,
		Role:        organizationRole(role),
		JoinedAt:    timestamp(joinedAt),
	}
}

func teamMessage(
	id pgtype.UUID,
	organizationID pgtype.UUID,
	parentTeamID pgtype.UUID,
	name string,
	depth int32,
	protectedGeneral bool,
	createdAt pgtype.Timestamptz,
	updatedAt pgtype.Timestamptz,
) *delibasev1.Team {
	return &delibasev1.Team{
		TeamId:           uuidMessage(id),
		OrganizationId:   uuidMessage(organizationID),
		ParentTeamId:     uuidMessage(parentTeamID),
		Name:             name,
		Depth:            depth,
		ProtectedGeneral: protectedGeneral,
		CreatedAt:        timestamp(createdAt),
		UpdatedAt:        timestamp(updatedAt),
	}
}

func teamMembershipMessage(
	teamID pgtype.UUID,
	accountID pgtype.UUID,
	displayName string,
	role string,
	createdAt pgtype.Timestamptz,
) *delibasev1.TeamMembership {
	return &delibasev1.TeamMembership{
		TeamId:      uuidMessage(teamID),
		AccountId:   uuidMessage(accountID),
		DisplayName: displayName,
		Role:        teamRole(role),
		CreatedAt:   timestamp(createdAt),
	}
}

func effectiveTeamAccessMessage(
	teamID pgtype.UUID,
	accountID pgtype.UUID,
	effectiveRole string,
	source string,
	sourceTeamID pgtype.UUID,
	organizationRoleValue string,
) *delibasev1.EffectiveTeamAccess {
	return &delibasev1.EffectiveTeamAccess{
		TeamId:           uuidMessage(teamID),
		AccountId:        uuidMessage(accountID),
		EffectiveRole:    teamRole(effectiveRole),
		Source:           teamAccessSource(source),
		SourceTeamId:     uuidMessage(sourceTeamID),
		OrganizationRole: organizationRole(organizationRoleValue),
	}
}

func invitationMessage(
	id pgtype.UUID,
	organizationID pgtype.UUID,
	organizationRoleValue string,
	targetTeamID pgtype.UUID,
	teamRoleValue pgtype.Text,
	status string,
	createdAt pgtype.Timestamptz,
	expiresAt pgtype.Timestamptz,
	revokedAt pgtype.Timestamptz,
) *delibasev1.OrganizationInvitation {
	teamRoleName := ""
	if teamRoleValue.Valid {
		teamRoleName = teamRoleValue.String
	}
	return &delibasev1.OrganizationInvitation{
		InvitationId:     uuidMessage(id),
		OrganizationId:   uuidMessage(organizationID),
		OrganizationRole: organizationRole(organizationRoleValue),
		TeamId:           uuidMessage(targetTeamID),
		TeamRole:         teamRole(teamRoleName),
		Status:           invitationStatus(status),
		CreatedAt:        timestamp(createdAt),
		ExpiresAt:        timestamp(expiresAt),
		RevokedAt:        timestamp(revokedAt),
	}
}

func uuidMessage(value pgtype.UUID) *delibasev1.UuidV7 {
	if !value.Valid {
		return nil
	}
	return &delibasev1.UuidV7{Value: uuid.UUID(value.Bytes).String()}
}

func timestamp(value pgtype.Timestamptz) *timestamppb.Timestamp {
	if !value.Valid {
		return nil
	}
	return timestamppb.New(value.Time.UTC())
}

func pgUUID(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(value), Valid: true}
}

func parseUUIDv7(value *delibasev1.UuidV7) (uuid.UUID, error) {
	if value == nil {
		return uuid.Nil, invalidArgument()
	}
	parsed, err := uuid.Parse(value.Value)
	if err != nil || parsed.Version() != 7 || parsed.Variant() != uuid.RFC4122 ||
		parsed.String() != value.Value {
		return uuid.Nil, invalidArgument()
	}
	return parsed, nil
}

func validateName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 120 ||
		strings.ContainsAny(value, "\x00\r\n") {
		return "", invalidArgument()
	}
	return value, nil
}

func validateDisplayName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 120 ||
		strings.ContainsAny(value, "\x00\r\n") {
		return "", invalidArgument()
	}
	return value, nil
}

func validateSlug(value string) (string, error) {
	if value != strings.ToLower(value) || len(value) < 3 || len(value) > 63 ||
		!slugPattern.MatchString(value) {
		return "", serviceError(
			connect.CodeInvalidArgument,
			delibasev1.ErrorReason_ERROR_REASON_SLUG_INVALID,
		)
	}
	return value, nil
}

func validateIdempotency(value *delibasev1.IdempotencyKey) (string, error) {
	if value == nil || !idempotencyKeyPattern.MatchString(value.Key) ||
		redact.Text(value.Key) != value.Key {
		return "", serviceError(
			connect.CodeInvalidArgument,
			delibasev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_REQUIRED,
		)
	}
	return value.Key, nil
}

func requestDigest(parts ...string) []byte {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(part))
	}
	return hash.Sum(nil)
}

func providerIdempotencyKey(
	operation string,
	subject string,
	organizationID uuid.UUID,
	key string,
) string {
	return "delibase:v1:" + hex.EncodeToString(requestDigest(
		operation,
		subject,
		organizationID.String(),
		key,
	))
}

func replay(
	ctx context.Context,
	queries *dbgen.Queries,
	subject string,
	operation string,
	key string,
	digest []byte,
	target proto.Message,
) (bool, time.Time, error) {
	return replayForCaller(
		ctx,
		queries,
		"user",
		idempotencyCallerID(subject),
		operation,
		key,
		digest,
		target,
	)
}

func replayForCaller(
	ctx context.Context,
	queries *dbgen.Queries,
	callerKind string,
	callerID string,
	operation string,
	key string,
	digest []byte,
	target proto.Message,
) (bool, time.Time, error) {
	return replayForCallerWithConflictReason(
		ctx,
		queries,
		callerKind,
		callerID,
		operation,
		key,
		digest,
		target,
		delibasev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_CONFLICT,
	)
}

func backgroundReplayForCaller(
	ctx context.Context,
	queries *dbgen.Queries,
	callerKind string,
	callerID string,
	operation string,
	key string,
	digest []byte,
	target proto.Message,
) (bool, time.Time, error) {
	return replayForCallerWithConflictReason(
		ctx,
		queries,
		callerKind,
		callerID,
		operation,
		key,
		digest,
		target,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
	)
}

func replayForCallerWithConflictReason(
	ctx context.Context,
	queries *dbgen.Queries,
	callerKind string,
	callerID string,
	operation string,
	key string,
	digest []byte,
	target proto.Message,
	conflictReason delibasev1.ErrorReason,
) (bool, time.Time, error) {
	scope := dbgen.DeleteExpiredIdempotencyRecordParams{
		CallerKind:     callerKind,
		CallerID:       callerID,
		Operation:      operation,
		IdempotencyKey: key,
	}
	if _, err := queries.DeleteExpiredIdempotencyRecord(ctx, scope); err != nil {
		return false, time.Time{}, databaseError(err)
	}
	record, err := queries.GetIdempotencyRecord(ctx, dbgen.GetIdempotencyRecordParams(scope))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, time.Time{}, nil
	}
	if err != nil {
		return false, time.Time{}, databaseError(err)
	}
	if !bytes.Equal(record.RequestHash, digest) {
		return false, time.Time{}, serviceError(
			connect.CodeAborted,
			conflictReason,
		)
	}
	if record.ConnectCode.Valid {
		return false, time.Time{}, serviceError(
			connect.CodeAborted,
			conflictReason,
		)
	}
	if err := proto.Unmarshal(record.ResponsePayload, target); err != nil {
		return false, time.Time{}, serviceError(
			connect.CodeInternal,
			delibasev1.ErrorReason_ERROR_REASON_RESOURCE_CONFLICT,
		)
	}
	return true, record.CreatedAt.Time.UTC(), nil
}

func replayWithActiveAccount(
	ctx context.Context,
	queries *dbgen.Queries,
	subject string,
	operation string,
	key string,
	digest []byte,
	target proto.Message,
) (dbgen.Account, bool, time.Time, error) {
	replayed, completedAt, err := replay(
		ctx, queries, subject, operation, key, digest, target,
	)
	if err != nil || replayed {
		return dbgen.Account{}, replayed, completedAt, err
	}
	account, err := activeAccount(ctx, queries, subject)
	if err != nil {
		return dbgen.Account{}, false, time.Time{}, err
	}
	replayed, completedAt, err = replay(
		ctx, queries, subject, operation, key, digest, target,
	)
	return account, replayed, completedAt, err
}

func replayWithActiveAccountForOrganization(
	ctx context.Context,
	queries *dbgen.Queries,
	subject string,
	organizationID uuid.UUID,
	operation string,
	key string,
	digest []byte,
	target proto.Message,
) (dbgen.Account, bool, time.Time, error) {
	replayed, completedAt, err := replay(
		ctx, queries, subject, operation, key, digest, target,
	)
	if err != nil || replayed {
		return dbgen.Account{}, replayed, completedAt, err
	}
	if _, err = queries.LockOrganizationForMutation(
		ctx, pgUUID(organizationID),
	); err != nil {
		return dbgen.Account{}, false, time.Time{}, membershipReadError(err)
	}
	account, err := activeAccount(ctx, queries, subject)
	if err != nil {
		return dbgen.Account{}, false, time.Time{}, err
	}
	replayed, completedAt, err = replay(
		ctx, queries, subject, operation, key, digest, target,
	)
	return account, replayed, completedAt, err
}

func persistIdempotency(
	ctx context.Context,
	dependencies Dependencies,
	queries *dbgen.Queries,
	subject string,
	operation string,
	key string,
	digest []byte,
	response proto.Message,
) (time.Time, error) {
	return persistIdempotencyForCaller(
		ctx,
		dependencies,
		queries,
		"user",
		idempotencyCallerID(subject),
		operation,
		key,
		digest,
		response,
	)
}

func persistIdempotencyForCaller(
	ctx context.Context,
	dependencies Dependencies,
	queries *dbgen.Queries,
	callerKind string,
	callerID string,
	operation string,
	key string,
	digest []byte,
	response proto.Message,
) (time.Time, error) {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(response)
	if err != nil {
		return time.Time{}, serviceError(connect.CodeInternal, 0)
	}
	id, err := dependencies.IDs.New()
	if err != nil {
		return time.Time{}, serviceError(connect.CodeInternal, 0)
	}
	record, err := queries.InsertIdempotencyRecord(ctx, dbgen.InsertIdempotencyRecordParams{
		ID:              pgUUID(id),
		CallerKind:      callerKind,
		CallerID:        callerID,
		Operation:       operation,
		IdempotencyKey:  key,
		RequestHash:     digest,
		ResponsePayload: encoded,
		ConnectCode:     pgtype.Int4{},
	})
	if err != nil {
		return time.Time{}, databaseError(err)
	}
	return record.CreatedAt.Time.UTC(), nil
}

func idempotencyCallerID(subject string) string {
	digest := sha256.Sum256([]byte(subject))
	return "caller:v1:" + hex.EncodeToString(digest[:16])
}

func subjectDigest(subject string) []byte {
	digest := sha256.Sum256([]byte(subject))
	return digest[:]
}

func setIdempotency(
	result **delibasev1.IdempotencyResult,
	operation delibasev1.IdempotentOperation,
	replayed bool,
	completedAt time.Time,
) {
	if replayed && *result != nil {
		originallyCompletedAt := (*result).OriginallyCompletedAt
		if originallyCompletedAt != nil && originallyCompletedAt.IsValid() {
			completedAt = originallyCompletedAt.AsTime()
		}
	}
	*result = &delibasev1.IdempotencyResult{
		Replayed:              replayed,
		Operation:             operation,
		OriginallyCompletedAt: timestamppb.New(completedAt.UTC()),
	}
}

func createOrganizationBundle(
	ctx context.Context,
	queries *dbgen.Queries,
	accountID pgtype.UUID,
	organization dbgen.Organization,
	generalTeamID uuid.UUID,
	polarCustomerID string,
) error {
	if _, err := queries.CreateOrganizationMembership(
		ctx,
		dbgen.CreateOrganizationMembershipParams{
			OrganizationID: organization.ID,
			AccountID:      accountID,
			Role:           "owner",
		},
	); err != nil {
		return databaseError(err)
	}
	team, err := queries.CreateGeneralTeam(ctx, dbgen.CreateGeneralTeamParams{
		ID:             pgUUID(generalTeamID),
		OrganizationID: organization.ID,
	})
	if err != nil {
		return databaseError(err)
	}
	if _, err := queries.CreateTeamMembership(ctx, dbgen.CreateTeamMembershipParams{
		OrganizationID: organization.ID,
		TeamID:         team.ID,
		AccountID:      accountID,
		Role:           "admin",
	}); err != nil {
		return databaseError(err)
	}
	if _, err := queries.CreatePolarCustomer(ctx, dbgen.CreatePolarCustomerParams{
		OrganizationID:  organization.ID,
		PolarCustomerID: polarCustomerID,
	}); err != nil {
		return databaseError(err)
	}
	return nil
}

func ensurePolarCustomer(
	ctx context.Context,
	dependencies Dependencies,
	organizationID uuid.UUID,
	name string,
) (string, error) {
	if dependencies.PolarCustomers == nil {
		return "", serviceError(connect.CodeInternal, 0)
	}
	customer, err := dependencies.PolarCustomers.EnsureCustomer(
		ctx,
		contracts.CustomerRequest{
			OrganizationID: organizationID.String(),
			Name:           name,
		},
	)
	if err != nil || customer.ID == "" {
		return "", serviceError(connect.CodeUnavailable, 0)
	}
	if _, err := uuid.Parse(customer.ID); err != nil {
		return "", serviceError(connect.CodeUnavailable, 0)
	}
	return customer.ID, nil
}

func appendAudit(
	ctx context.Context,
	dependencies Dependencies,
	queries *dbgen.Queries,
	event reliability.AuditEventType,
	actor safelog.ActorPseudonym,
	organizationID uuid.UUID,
) error {
	id, err := dependencies.IDs.New()
	if err != nil {
		return serviceError(connect.CodeInternal, 0)
	}
	metadata, _ := requestmeta.FromContext(ctx)
	_, err = reliability.AppendAudit(ctx, queries, reliability.AuditInput{
		ID:             id,
		OccurredAt:     dependencies.Clock.Now().UTC(),
		EventType:      event,
		Actor:          actor,
		OrganizationID: organizationID,
		Result:         safelog.ResultSuccess,
		Metadata: reliability.AuditMetadata{
			RequestID: metadata.RequestID,
			TraceID:   metadata.TraceID,
		},
	})
	if err != nil {
		return databaseError(err)
	}
	return nil
}

func appendTeamAudit(
	ctx context.Context,
	dependencies Dependencies,
	queries *dbgen.Queries,
	event reliability.AuditEventType,
	actor safelog.ActorPseudonym,
	organizationID uuid.UUID,
	teamID uuid.UUID,
) error {
	team, err := queries.GetTeamByID(ctx, pgUUID(teamID))
	if err != nil {
		return databaseError(err)
	}
	id, err := dependencies.IDs.New()
	if err != nil {
		return serviceError(connect.CodeInternal, 0)
	}
	metadata, _ := requestmeta.FromContext(ctx)
	_, err = reliability.AppendAudit(ctx, queries, reliability.AuditInput{
		ID:               id,
		OccurredAt:       dependencies.Clock.Now().UTC(),
		EventType:        event,
		Actor:            actor,
		OrganizationID:   organizationID,
		TeamID:           teamID,
		TeamNameSnapshot: team.Name,
		Result:           safelog.ResultSuccess,
		Metadata: reliability.AuditMetadata{
			RequestID: metadata.RequestID,
			TraceID:   metadata.TraceID,
		},
	})
	if err != nil {
		return databaseError(err)
	}
	return nil
}

func actorFor(dependencies Dependencies, subject string) (safelog.ActorPseudonym, error) {
	if dependencies.Pseudonymizer == nil {
		return "", serviceError(connect.CodeInternal, 0)
	}
	actor := dependencies.Pseudonymizer.Actor(subject)
	if actor == "" {
		return "", serviceError(connect.CodeInternal, 0)
	}
	return actor, nil
}

func page(request *delibasev1.PageRequest) (int32, pgtype.UUID, error) {
	size := int32(defaultPageSize)
	cursor := pgtype.UUID{Bytes: [16]byte(uuid.Nil), Valid: true}
	if request == nil {
		return size, cursor, nil
	}
	if request.PageSize < 0 || request.PageSize > maximumPageSize {
		return 0, pgtype.UUID{}, invalidArgument()
	}
	if request.PageSize > 0 {
		size = request.PageSize
	}
	if request.Cursor == "" {
		return size, cursor, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(request.Cursor)
	if err != nil || len(raw) != 16 {
		return 0, pgtype.UUID{}, invalidArgument()
	}
	var parsed uuid.UUID
	copy(parsed[:], raw)
	if parsed.Version() != 7 || parsed.Variant() != uuid.RFC4122 {
		return 0, pgtype.UUID{}, invalidArgument()
	}
	return size, pgUUID(parsed), nil
}

func nextCursor(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(id.Bytes[:])
}

func invalidArgument() error {
	return serviceError(connect.CodeInvalidArgument, 0)
}

func serviceError(code connect.Code, reason delibasev1.ErrorReason) error {
	failure := connect.NewError(code, errors.New("request failed"))
	if reason != delibasev1.ErrorReason_ERROR_REASON_UNSPECIFIED {
		detail, err := connect.NewErrorDetail(&delibasev1.ErrorDetail{Reason: reason})
		if err == nil {
			failure.AddDetail(detail)
		}
	}
	return failure
}

func databaseError(err error) error {
	if err == nil {
		return nil
	}
	var connectFailure *connect.Error
	if errors.As(err, &connectFailure) {
		return connectFailure
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return serviceError(
			connect.CodeNotFound,
			delibasev1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND,
		)
	}
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) {
		switch postgres.Code {
		case "23505":
			if strings.Contains(postgres.ConstraintName, "slug") ||
				strings.Contains(postgres.Message, "slug") {
				return serviceError(
					connect.CodeAlreadyExists,
					delibasev1.ErrorReason_ERROR_REASON_SLUG_CONFLICT,
				)
			}
			if strings.Contains(
				postgres.ConstraintName,
				"usage_reservations_service_client_reference",
			) {
				return serviceError(
					connect.CodeAlreadyExists,
					delibasev1.ErrorReason_ERROR_REASON_CLIENT_REFERENCE_CONFLICT,
				)
			}
			if strings.Contains(postgres.ConstraintName, "idempotency_records") {
				return serviceError(
					connect.CodeAborted,
					delibasev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_CONFLICT,
				)
			}
			if strings.Contains(
				postgres.ConstraintName,
				"usage_records_one_background_period_settlement",
			) {
				return serviceError(
					connect.CodeResourceExhausted,
					delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_PERIOD_LIMIT_EXCEEDED,
				)
			}
			return serviceError(
				connect.CodeAborted,
				delibasev1.ErrorReason_ERROR_REASON_RESOURCE_CONFLICT,
			)
		case "23514":
			switch {
			case strings.Contains(postgres.Message, "retain at least one owner"),
				strings.Contains(postgres.Message, "active owner"):
				return serviceError(
					connect.CodeFailedPrecondition,
					delibasev1.ErrorReason_ERROR_REASON_LAST_OWNER_BLOCKER,
				)
			case strings.Contains(postgres.Message, "finalized reservations"):
				return serviceError(
					connect.CodeFailedPrecondition,
					delibasev1.ErrorReason_ERROR_REASON_ORGANIZATION_DELETION_BLOCKED,
				)
			case strings.Contains(postgres.Message, "five levels"):
				return serviceError(
					connect.CodeFailedPrecondition,
					delibasev1.ErrorReason_ERROR_REASON_TEAM_DEPTH_EXCEEDED,
				)
			case strings.Contains(postgres.Message, "cycle"):
				return serviceError(
					connect.CodeFailedPrecondition,
					delibasev1.ErrorReason_ERROR_REASON_TEAM_CYCLE,
				)
			case strings.Contains(postgres.Message, "General team"):
				return generalTeamProtected()
			case strings.Contains(postgres.Message, "reservation has expired"):
				return serviceError(
					connect.CodeFailedPrecondition,
					delibasev1.ErrorReason_ERROR_REASON_RESERVATION_EXPIRED,
				)
			case strings.Contains(postgres.Message, "exceeds reservation maximum"):
				return serviceError(
					connect.CodeInvalidArgument,
					delibasev1.ErrorReason_ERROR_REASON_COMMIT_UNITS_EXCEED_RESERVED,
				)
			case strings.Contains(postgres.Message, "overage capacity"),
				strings.Contains(postgres.Message, "overage limit"):
				return serviceError(
					connect.CodeResourceExhausted,
					delibasev1.ErrorReason_ERROR_REASON_OVERAGE_LIMIT_EXHAUSTED,
				)
			case strings.Contains(postgres.Message, "credit capacity"):
				return serviceError(
					connect.CodeFailedPrecondition,
					delibasev1.ErrorReason_ERROR_REASON_AVAILABLE_FUNDS_EXHAUSTED,
				)
			case strings.Contains(
				postgres.Message,
				"background authorization binding was substituted",
			):
				return serviceError(
					connect.CodePermissionDenied,
					delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_SUBSTITUTION,
				)
			case strings.Contains(
				postgres.Message,
				"background authorization access is unavailable",
			):
				return serviceError(
					connect.CodePermissionDenied,
					delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_ACCESS_LOST,
				)
			case strings.Contains(
				postgres.Message,
				"background authorization period limit exceeded",
			):
				return serviceError(
					connect.CodeResourceExhausted,
					delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_PERIOD_LIMIT_EXCEEDED,
				)
			}
			return invalidArgument()
		case "23503":
			return serviceError(
				connect.CodeFailedPrecondition,
				delibasev1.ErrorReason_ERROR_REASON_ORGANIZATION_DELETION_BLOCKED,
			)
		}
	}
	return serviceError(connect.CodeInternal, 0)
}
