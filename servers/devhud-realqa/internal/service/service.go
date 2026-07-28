// Package service implements the private devhud.realqa.v1 Connect services.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1/realqav1connect"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/rqerr"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/requestmeta"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	personalPresetLimit     = 50
	organizationPresetLimit = 250
	deviceShortcutLimit     = 20
	defaultPageSize         = 50
	maxPageSize             = 100
)

type IDGenerator interface {
	New() (uuid.UUID, error)
}

type defaultIDs struct{}

func (defaultIDs) New() (uuid.UUID, error) { return uuidv7.New() }

type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type GitHubAuthorization interface {
	Target(state string) (string, error)
}

type Dependencies struct {
	Store         *database.Store
	IDs           IDGenerator
	Clock         Clock
	GitHub        GitHubAuthorization
	Pseudonymizer *safelog.Pseudonymizer
	Logger        *slog.Logger
}

func (dependencies Dependencies) defaults() Dependencies {
	if dependencies.IDs == nil {
		dependencies.IDs = defaultIDs{}
	}
	if dependencies.Clock == nil {
		dependencies.Clock = systemClock{}
	}
	if dependencies.Logger == nil {
		dependencies.Logger = slog.New(slog.DiscardHandler)
	}
	return dependencies
}

type Preset struct {
	realqav1connect.UnimplementedRealQAPresetServiceHandler
	dependencies Dependencies
}

type Tracker struct {
	realqav1connect.UnimplementedRealQATrackerServiceHandler
	dependencies Dependencies
}

type Submission struct {
	realqav1connect.UnimplementedRealQASubmissionServiceHandler
	dependencies Dependencies
}

func NewPreset(dependencies Dependencies) *Preset {
	return &Preset{dependencies: dependencies.defaults()}
}

func NewTracker(dependencies Dependencies) *Tracker {
	return &Tracker{dependencies: dependencies.defaults()}
}

func NewSubmission(dependencies Dependencies) *Submission {
	return &Submission{dependencies: dependencies.defaults()}
}

var (
	_ realqav1connect.RealQAPresetServiceHandler     = (*Preset)(nil)
	_ realqav1connect.RealQATrackerServiceHandler    = (*Tracker)(nil)
	_ realqav1connect.RealQASubmissionServiceHandler = (*Submission)(nil)
)

type caller struct {
	accountID uuid.UUID
	subject   string
	digest    []byte
	actor     safelog.ActorPseudonym
}

func resolveCaller(ctx context.Context, dependencies Dependencies) (caller, error) {
	if dependencies.Store == nil {
		return caller{}, errors.New("realqa service: store unavailable")
	}
	principal, ok := auth.PrincipalFromContext(ctx)
	if !ok || principal.User == nil || principal.User.Subject == "" {
		return caller{}, rqerr.New(
			connect.CodeUnauthenticated,
			realqav1.ErrorReason_ERROR_REASON_AUTHENTICATION_REQUIRED,
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0,
		)
	}
	digest, err := dependencies.Store.SubjectDigest(principal.User.Subject)
	if err != nil {
		return caller{}, err
	}
	identity, err := dependencies.Store.Queries().GetIdentityBySubjectDigest(ctx, digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return caller{}, rqerr.New(
			connect.CodePermissionDenied,
			realqav1.ErrorReason_ERROR_REASON_PERMISSION_DENIED,
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0,
		)
	}
	if err != nil {
		return caller{}, err
	}
	accountID, err := fromPGUUID(identity.AccountID)
	if err != nil {
		return caller{}, err
	}
	return caller{
		accountID: accountID,
		subject:   principal.User.Subject,
		digest:    digest,
		actor:     dependencies.Pseudonymizer.Actor(principal.User.Subject),
	}, nil
}

type owner struct {
	kind string
	id   uuid.UUID
}

func parseOwner(value *realqav1.OwnerScope) (owner, error) {
	if value == nil {
		return owner{}, invalid(realqav1.ErrorReason_ERROR_REASON_OWNER_SCOPE_NOT_FOUND)
	}
	var raw string
	result := owner{}
	switch value.Kind {
	case realqav1.OwnerScopeKind_OWNER_SCOPE_KIND_PERSONAL:
		result.kind = "personal"
		if value.GetPersonalAccountId() != nil {
			raw = value.GetPersonalAccountId().Value
		}
	case realqav1.OwnerScopeKind_OWNER_SCOPE_KIND_ORGANIZATION:
		result.kind = "organization"
		if value.GetOrganizationId() != nil {
			raw = value.GetOrganizationId().Value
		}
	default:
		return owner{}, invalid(realqav1.ErrorReason_ERROR_REASON_OWNER_SCOPE_NOT_FOUND)
	}
	id, err := parseUUIDv7(raw)
	if err != nil {
		return owner{}, invalid(realqav1.ErrorReason_ERROR_REASON_OWNER_SCOPE_NOT_FOUND)
	}
	result.id = id
	return result, nil
}

func ownerProto(value owner) *realqav1.OwnerScope {
	result := &realqav1.OwnerScope{}
	id := &realqav1.UuidV7{Value: value.id.String()}
	if value.kind == "personal" {
		result.Kind = realqav1.OwnerScopeKind_OWNER_SCOPE_KIND_PERSONAL
		result.Owner = &realqav1.OwnerScope_PersonalAccountId{PersonalAccountId: id}
	} else {
		result.Kind = realqav1.OwnerScopeKind_OWNER_SCOPE_KIND_ORGANIZATION
		result.Owner = &realqav1.OwnerScope_OrganizationId{OrganizationId: id}
	}
	return result
}

func authorizeOwner(
	ctx context.Context,
	dependencies Dependencies,
	actor caller,
	scope owner,
	manage bool,
	ownerOnly bool,
) (dbgen.RealqaOwnerBinding, error) {
	access, err := dependencies.Store.Queries().GetOwnerAccess(
		ctx, dbgen.GetOwnerAccessParams{
			AccountID: toPGUUID(actor.accountID),
			OwnerKind: scope.kind,
			OwnerID:   toPGUUID(scope.id),
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.RealqaOwnerBinding{}, permissionDenied()
	}
	if err != nil {
		return dbgen.RealqaOwnerBinding{}, err
	}
	if scope.kind == "personal" && actor.accountID != scope.id {
		return dbgen.RealqaOwnerBinding{}, permissionDenied()
	}
	if ownerOnly && access.Role != "owner" {
		return dbgen.RealqaOwnerBinding{}, permissionDenied()
	}
	if manage && scope.kind == "organization" &&
		access.Role != "owner" && access.Role != "admin" {
		return dbgen.RealqaOwnerBinding{}, permissionDenied()
	}
	return access, nil
}

func authorizeBilling(
	ctx context.Context,
	dependencies Dependencies,
	actor caller,
	scope owner,
	billing *realqav1.BillingScope,
) (uuid.UUID, uuid.UUID, error) {
	if billing == nil || billing.OrganizationId == nil || billing.TeamId == nil {
		return uuid.Nil, uuid.Nil, invalid(realqav1.ErrorReason_ERROR_REASON_PERMISSION_DENIED)
	}
	organizationID, err := parseUUIDv7(billing.OrganizationId.Value)
	if err != nil {
		return uuid.Nil, uuid.Nil, invalid(realqav1.ErrorReason_ERROR_REASON_PERMISSION_DENIED)
	}
	teamID, err := parseUUIDv7(billing.TeamId.Value)
	if err != nil {
		return uuid.Nil, uuid.Nil, invalid(realqav1.ErrorReason_ERROR_REASON_PERMISSION_DENIED)
	}
	if scope.kind == "organization" && organizationID != scope.id {
		return uuid.Nil, uuid.Nil, permissionDenied()
	}
	allowed, err := dependencies.Store.Queries().HasPayerTeamAccess(
		ctx, dbgen.HasPayerTeamAccessParams{
			AccountID: toPGUUID(actor.accountID), OrganizationID: toPGUUID(organizationID),
			TeamID: toPGUUID(teamID),
		},
	)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if !allowed {
		return uuid.Nil, uuid.Nil, permissionDenied()
	}
	return organizationID, teamID, nil
}

func authorizeRepository(
	ctx context.Context,
	dependencies Dependencies,
	actor caller,
	scope owner,
	destination *realqav1.TrackerDestination,
) (uuid.UUID, dbgen.RealqaRepositoryAccess, error) {
	if destination == nil ||
		destination.Tracker != realqav1.TrackerKind_TRACKER_KIND_GITHUB_COM ||
		destination.InstallationId == nil || destination.Repository == nil ||
		destination.Repository.RepositoryId == "" ||
		destination.Repository.Owner == "" || destination.Repository.Name == "" {
		return uuid.Nil, dbgen.RealqaRepositoryAccess{},
			invalid(realqav1.ErrorReason_ERROR_REASON_UNSUPPORTED_TRACKER_HOST)
	}
	installationID, err := parseUUIDv7(destination.InstallationId.Value)
	if err != nil {
		return uuid.Nil, dbgen.RealqaRepositoryAccess{},
			invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	repository, err := dependencies.Store.Queries().GetRepositorySubmitAccessForOwner(
		ctx, dbgen.GetRepositorySubmitAccessForOwnerParams{
			InstallationID: toPGUUID(installationID),
			AccountID:      toPGUUID(actor.accountID),
			RepositoryID:   destination.Repository.RepositoryId,
			OwnerKind:      scope.kind,
			OwnerID:        toPGUUID(scope.id),
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		audit(ctx, dependencies, actor, "repository_access_denied", owner{},
			installationID, "deny", "failure")
		return uuid.Nil, dbgen.RealqaRepositoryAccess{}, rqerr.New(
			connect.CodePermissionDenied,
			realqav1.ErrorReason_ERROR_REASON_PROVIDER_PERMISSION_DENIED,
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0,
		)
	}
	if err != nil {
		return uuid.Nil, dbgen.RealqaRepositoryAccess{}, err
	}
	return installationID, repository, nil
}

func parseUUIDv7(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id.Version() != 7 || id.String() != value {
		return uuid.Nil, errors.New("invalid UUID v7")
	}
	return id, nil
}

func toPGUUID(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: value, Valid: value != uuid.Nil}
}

func pageLowerBound(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: value, Valid: true}
}

func fromPGUUID(value pgtype.UUID) (uuid.UUID, error) {
	if !value.Valid {
		return uuid.Nil, errors.New("invalid stored UUID")
	}
	id := uuid.UUID(value.Bytes)
	if id.Version() != 7 {
		return uuid.Nil, errors.New("invalid stored UUID version")
	}
	return id, nil
}

func newID(dependencies Dependencies) (uuid.UUID, error) {
	id, err := dependencies.IDs.New()
	if err != nil || id.Version() != 7 {
		return uuid.Nil, errors.New("realqa service: UUID generation failed")
	}
	return id, nil
}

func invalid(reason realqav1.ErrorReason) error {
	return rqerr.New(connect.CodeInvalidArgument, reason,
		realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0)
}

func permissionDenied() error {
	return rqerr.New(connect.CodePermissionDenied,
		realqav1.ErrorReason_ERROR_REASON_PERMISSION_DENIED,
		realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0)
}

func stale(revision int64) error {
	return rqerr.New(connect.CodeAborted,
		realqav1.ErrorReason_ERROR_REASON_STALE_REVISION,
		realqav1.FailureClass_FAILURE_CLASS_CONFLICT, revision)
}

func digestMessage(message proto.Message) ([]byte, error) {
	serialized, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return nil, errors.New("realqa service: request digest failed")
	}
	sum := sha256.Sum256(serialized)
	return sum[:], nil
}

func page(request *realqav1.PageRequest) (int32, uuid.UUID, error) {
	size := int32(defaultPageSize)
	after := uuid.Nil
	if request == nil {
		return size, after, nil
	}
	if request.PageSize < 0 || request.PageSize > maxPageSize {
		return 0, uuid.Nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	if request.PageSize > 0 {
		size = request.PageSize
	}
	if request.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(request.Cursor)
		if err != nil || len(raw) != 16 {
			return 0, uuid.Nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
		}
		copy(after[:], raw)
		if after.Version() != 7 {
			return 0, uuid.Nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
		}
	}
	return size, after, nil
}

func cursor(id uuid.UUID, hasMore bool) string {
	if !hasMore || id == uuid.Nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(id[:])
}

func shortcutLimitExceeded() error {
	return rqerr.New(connect.CodeResourceExhausted,
		realqav1.ErrorReason_ERROR_REASON_DEVICE_SHORTCUT_LIMIT_EXCEEDED,
		realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0)
}

func audit(
	ctx context.Context,
	dependencies Dependencies,
	actor caller,
	event string,
	scope owner,
	resource uuid.UUID,
	decision string,
	result string,
) {
	if dependencies.Store == nil {
		return
	}
	id, err := newID(dependencies)
	if err != nil {
		return
	}
	params := dbgen.InsertAuditParams{
		ID:             toPGUUID(id),
		EventType:      event,
		ActorReference: string(actor.actor),
		Decision:       decision,
		Result:         result,
	}
	if params.ActorReference == "" {
		params.ActorReference = "system"
	}
	if scope.id != uuid.Nil {
		params.OwnerKind = pgtype.Text{String: scope.kind, Valid: true}
		params.OwnerID = toPGUUID(scope.id)
	}
	if resource != uuid.Nil {
		params.ResourceID = toPGUUID(resource)
	}
	if metadata, ok := requestmeta.FromContext(ctx); ok {
		params.RequestID = pgtype.Text{String: metadata.RequestID, Valid: metadata.RequestID != ""}
		params.TraceID = pgtype.Text{String: metadata.TraceID, Valid: metadata.TraceID != ""}
	}
	if err = dependencies.Store.Queries().InsertAudit(ctx, params); err != nil {
		safelog.Record(ctx, dependencies.Logger, slog.LevelError, safelog.EventIntegration,
			safelog.Fields{
				Actor: actor.actor, Decision: safelog.DecisionDeny,
				Result: safelog.ResultFailure,
			})
	}
}

func newOAuthState() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, errors.New("realqa service: OAuth state generation failed")
	}
	state := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(state))
	return state, digest[:], nil
}

func validateAuthorizationTarget(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" ||
		parsed.User != nil || parsed.Fragment != "" ||
		(parsed.Path != "/login/oauth/authorize" && parsed.Path != "/apps/realqa/installations/new") {
		return errors.New("realqa service: invalid GitHub authorization target")
	}
	return nil
}

func timestamp(value pgtype.Timestamptz) *timestamppb.Timestamp {
	if !value.Valid {
		return nil
	}
	return timestamppb.New(value.Time)
}

func cleanStringList(values []string, maxItems, maxBytes int) ([]string, error) {
	if len(values) > maxItems {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	result := make([]string, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if strings.TrimSpace(value) != value || value == "" || len(value) > maxBytes ||
			strings.ContainsAny(value, "\x00\r\n") {
			return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
		}
		seen[value] = struct{}{}
		result[index] = value
	}
	return result, nil
}
