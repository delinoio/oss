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

func replay(
	ctx context.Context,
	queries *dbgen.Queries,
	subject string,
	operation string,
	key string,
	digest []byte,
	target proto.Message,
) (bool, time.Time, error) {
	record, err := queries.GetIdempotencyRecord(ctx, dbgen.GetIdempotencyRecordParams{
		CallerKind:     "user",
		CallerID:       idempotencyCallerID(subject),
		Operation:      operation,
		IdempotencyKey: key,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, time.Time{}, nil
	}
	if err != nil {
		return false, time.Time{}, databaseError(err)
	}
	if !bytes.Equal(record.RequestHash, digest) {
		return false, time.Time{}, serviceError(
			connect.CodeAborted,
			delibasev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_CONFLICT,
		)
	}
	if record.ConnectCode.Valid {
		return false, time.Time{}, serviceError(
			connect.CodeAborted,
			delibasev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_CONFLICT,
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
		CallerKind:      "user",
		CallerID:        idempotencyCallerID(subject),
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
	organizationID uuid.UUID,
	generalTeamID uuid.UUID,
	name string,
	slug string,
) (dbgen.Organization, error) {
	organization, err := queries.CreateOrganization(ctx, dbgen.CreateOrganizationParams{
		ID:   pgUUID(organizationID),
		Name: name,
		Slug: slug,
	})
	if err != nil {
		return dbgen.Organization{}, databaseError(err)
	}
	if _, err := queries.CreateOrganizationMembership(
		ctx,
		dbgen.CreateOrganizationMembershipParams{
			OrganizationID: organization.ID,
			AccountID:      accountID,
			Role:           "owner",
		},
	); err != nil {
		return dbgen.Organization{}, databaseError(err)
	}
	team, err := queries.CreateGeneralTeam(ctx, dbgen.CreateGeneralTeamParams{
		ID:             pgUUID(generalTeamID),
		OrganizationID: organization.ID,
	})
	if err != nil {
		return dbgen.Organization{}, databaseError(err)
	}
	if _, err := queries.CreateTeamMembership(ctx, dbgen.CreateTeamMembershipParams{
		OrganizationID: organization.ID,
		TeamID:         team.ID,
		AccountID:      accountID,
		Role:           "admin",
	}); err != nil {
		return dbgen.Organization{}, databaseError(err)
	}
	if _, err := queries.CreatePendingPolarCustomer(ctx, organization.ID); err != nil {
		return dbgen.Organization{}, databaseError(err)
	}
	return organization, nil
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
