package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	realqagithub "github.com/delinoio/oss/servers/devhud-realqa/internal/github"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/rqerr"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (service *Tracker) GetGitHubConnection(
	ctx context.Context,
	request *connect.Request[realqav1.GetGitHubConnectionRequest],
) (*connect.Response[realqav1.GetGitHubConnectionResponse], error) {
	if request == nil || request.Msg == nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_OWNER_SCOPE_NOT_FOUND)
	}
	actor, err := resolveCaller(ctx, service.dependencies)
	if err != nil {
		return nil, err
	}
	scope, err := parseOwner(request.Msg.Owner)
	if err != nil {
		return nil, err
	}
	if _, err = authorizeOwner(ctx, service.dependencies, actor, scope, false, false); err != nil {
		return nil, err
	}
	record, err := service.dependencies.Store.Queries().GetGitHubConnectionForOwner(
		ctx, dbgen.GetGitHubConnectionForOwnerParams{
			OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return connect.NewResponse(&realqav1.GetGitHubConnectionResponse{
			Connection: &realqav1.GitHubConnection{
				Owner: ownerProto(scope),
				State: realqav1.GitHubConnectionState_GIT_HUB_CONNECTION_STATE_DISCONNECTED,
			},
		}), nil
	}
	if err != nil {
		return nil, err
	}
	connection, err := connectionProto(record)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&realqav1.GetGitHubConnectionResponse{
		Connection: connection,
	}), nil
}

func (service *Tracker) StartGitHubConnection(
	ctx context.Context,
	request *connect.Request[realqav1.StartGitHubConnectionRequest],
) (*connect.Response[realqav1.StartGitHubConnectionResponse], error) {
	if request == nil || request.Msg == nil || service.dependencies.GitHub == nil {
		return nil, rqerr.New(connect.CodeUnavailable,
			realqav1.ErrorReason_ERROR_REASON_GITHUB_DISCONNECTED,
			realqav1.FailureClass_FAILURE_CLASS_RETRYABLE, 0)
	}
	actor, err := resolveCaller(ctx, service.dependencies)
	if err != nil {
		return nil, err
	}
	scope, err := parseOwner(request.Msg.Owner)
	if err != nil {
		return nil, err
	}
	if _, err = authorizeOwner(ctx, service.dependencies, actor, scope, true, false); err != nil {
		return nil, err
	}
	state, stateDigest, err := newOAuthState()
	target := ""
	if issuer, ok := service.dependencies.GitHub.(GitHubConnectionTargetIssuer); ok {
		target, state, err = issuer.ConnectionTarget(scope.kind, scope.id, actor.accountID)
	} else if issuer, ok := service.dependencies.GitHub.(GitHubOAuthStateIssuer); ok {
		state, err = issuer.OAuthState(scope.kind, scope.id, actor.accountID)
	}
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(state))
	stateDigest = digest[:]
	if target == "" {
		target, err = service.dependencies.GitHub.Target(state)
	}
	if err != nil || validateAuthorizationTarget(target) != nil {
		return nil, rqerr.New(connect.CodeUnavailable,
			realqav1.ErrorReason_ERROR_REASON_GITHUB_DISCONNECTED,
			realqav1.FailureClass_FAILURE_CLASS_RETRYABLE, 0)
	}
	connectionID, err := newID(service.dependencies)
	if err != nil {
		return nil, err
	}
	expires := service.dependencies.Clock.Now().UTC().Add(10 * time.Minute)
	var record dbgen.RealqaGithubConnection
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if lockErr := lockActiveOwnerScope(ctx, queries, scope); lockErr != nil {
				return lockErr
			}
			record, err = queries.StartGitHubConnection(
				ctx, dbgen.StartGitHubConnectionParams{
					ID: toPGUUID(connectionID), OwnerKind: scope.kind,
					OwnerID: toPGUUID(scope.id), OauthStateDigest: stateDigest,
					OauthStateExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
				})
			return err
		})
	if err != nil {
		return nil, err
	}
	storedID, _ := fromPGUUID(record.ID)
	audit(ctx, service.dependencies, actor, "github_connection_started", scope,
		storedID, "allow", "success")
	return connect.NewResponse(&realqav1.StartGitHubConnectionResponse{
		AuthorizationTarget: target,
		ExpiresAt:           timestamppb.New(expires),
	}), nil
}

func (service *Tracker) ListGitHubInstallations(
	ctx context.Context,
	request *connect.Request[realqav1.ListGitHubInstallationsRequest],
) (*connect.Response[realqav1.ListGitHubInstallationsResponse], error) {
	if request == nil || request.Msg == nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_OWNER_SCOPE_NOT_FOUND)
	}
	actor, err := resolveCaller(ctx, service.dependencies)
	if err != nil {
		return nil, err
	}
	scope, err := parseOwner(request.Msg.Owner)
	if err != nil {
		return nil, err
	}
	if _, err = authorizeOwner(ctx, service.dependencies, actor, scope, false, false); err != nil {
		return nil, err
	}
	size, after, err := page(request.Msg.Page)
	if err != nil {
		return nil, err
	}
	rows, err := service.dependencies.Store.Queries().ListGitHubInstallations(
		ctx, dbgen.ListGitHubInstallationsParams{
			OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
			AfterID: pageLowerBound(after), PageLimit: size + 1,
		})
	if err != nil {
		return nil, err
	}
	hasMore := len(rows) > int(size)
	if hasMore {
		rows = rows[:size]
	}
	response := &realqav1.ListGitHubInstallationsResponse{
		Installations: make([]*realqav1.GitHubInstallation, 0, len(rows)),
		Page:          &realqav1.PageResponse{},
	}
	var last uuid.UUID
	for _, row := range rows {
		last, err = fromPGUUID(row.ID)
		if err != nil {
			return nil, err
		}
		response.Installations = append(response.Installations,
			&realqav1.GitHubInstallation{
				InstallationId: &realqav1.UuidV7{Value: last.String()},
				Owner:          ownerProto(scope), ProviderInstallationId: row.ProviderInstallationID,
				AccountLogin: row.AccountLogin, Revision: rqerr.Revision(row.Revision),
			})
	}
	response.Page.NextCursor = cursor(last, hasMore)
	return connect.NewResponse(response), nil
}

func (service *Tracker) DisconnectGitHubConnection(
	ctx context.Context,
	request *connect.Request[realqav1.DisconnectGitHubConnectionRequest],
) (*connect.Response[realqav1.DisconnectGitHubConnectionResponse], error) {
	if request == nil || request.Msg == nil || request.Msg.ExpectedRevision == nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_STALE_REVISION)
	}
	actor, err := resolveCaller(ctx, service.dependencies)
	if err != nil {
		return nil, err
	}
	idempotencyID, err := parseIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	digest, err := digestMessage(request.Msg)
	if err != nil {
		return nil, err
	}
	if replay, ok, replayErr := service.disconnectReplay(
		ctx, actor, idempotencyID, digest,
	); ok {
		return replay, replayErr
	}
	scope, err := parseOwner(request.Msg.Owner)
	if err != nil {
		return nil, err
	}
	if _, err = authorizeOwner(ctx, service.dependencies, actor, scope, true, false); err != nil {
		return nil, err
	}
	idempotencyRecordID, err := newID(service.dependencies)
	if err != nil {
		return nil, err
	}
	var record dbgen.RealqaGithubConnection
	var completedAt pgtype.Timestamptz
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			existing, lookupErr := queries.GetIdempotencyRecord(ctx,
				idempotencyLookupFor(actor, idempotencyID, "disconnect_github_connection"))
			if lookupErr == nil {
				if !bytes.Equal(existing.RequestDigest, digest) {
					return idempotencyConflict()
				}
				return errIdempotencyReplay
			}
			if !errors.Is(lookupErr, pgx.ErrNoRows) {
				return lookupErr
			}
			record, err = queries.DisconnectGitHubConnection(
				ctx, dbgen.DisconnectGitHubConnectionParams{
					OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
					ExpectedRevision: request.Msg.ExpectedRevision.Value,
				})
			if err != nil {
				return err
			}
			connection, connectionErr := connectionProto(record)
			if connectionErr != nil {
				return connectionErr
			}
			responsePayload, marshalErr := proto.MarshalOptions{
				Deterministic: true,
			}.Marshal(connection)
			if marshalErr != nil {
				return marshalErr
			}
			idempotencyRecord, createErr := queries.CreateIdempotencyRecord(
				ctx, dbgen.CreateIdempotencyRecordParams{
					ID: toPGUUID(idempotencyRecordID), CallerKind: "user",
					CallerDigest: actor.digest, Operation: "disconnect_github_connection",
					IdempotencyKey: toPGUUID(idempotencyID), RequestDigest: digest,
					ResourceID: record.ID, ResponsePayload: responsePayload,
				})
			completedAt = idempotencyRecord.CompletedAt
			return createErr
		})
	if err != nil {
		if replay, ok, replayErr := service.disconnectReplay(
			ctx, actor, idempotencyID, digest,
		); ok {
			return replay, replayErr
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		current, lookupErr := service.dependencies.Store.Queries().GetGitHubConnectionForOwner(
			ctx, dbgen.GetGitHubConnectionForOwnerParams{
				OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
			})
		if lookupErr == nil {
			return nil, stale(current.Revision)
		}
		return nil, rqerr.New(connect.CodeNotFound,
			realqav1.ErrorReason_ERROR_REASON_GITHUB_DISCONNECTED,
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0)
	}
	if err != nil {
		return nil, err
	}
	connection, err := connectionProto(record)
	if err != nil {
		return nil, err
	}
	connectionID, _ := fromPGUUID(record.ID)
	audit(ctx, service.dependencies, actor, "github_connection_disconnected",
		scope, connectionID, "allow", "success")
	return connect.NewResponse(&realqav1.DisconnectGitHubConnectionResponse{
		Connection: connection,
		Idempotency: &realqav1.IdempotencyResult{
			Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_DISCONNECT_GITHUB_CONNECTION,
			OriginallyCompletedAt: timestamp(completedAt),
		},
	}), nil
}

func (service *Tracker) disconnectReplay(
	ctx context.Context,
	actor caller,
	idempotencyID uuid.UUID,
	digest []byte,
) (*connect.Response[realqav1.DisconnectGitHubConnectionResponse], bool, error) {
	record, err := service.dependencies.Store.Queries().GetIdempotencyRecord(
		ctx, idempotencyLookupFor(actor, idempotencyID, "disconnect_github_connection"))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if !bytes.Equal(record.RequestDigest, digest) {
		return nil, true, idempotencyConflict()
	}
	connection := new(realqav1.GitHubConnection)
	if err := proto.Unmarshal(record.ResponsePayload, connection); err != nil {
		return nil, true, err
	}
	return connect.NewResponse(&realqav1.DisconnectGitHubConnectionResponse{
		Connection: connection,
		Idempotency: &realqav1.IdempotencyResult{
			Replayed:              true,
			Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_DISCONNECT_GITHUB_CONNECTION,
			OriginallyCompletedAt: timestamp(record.CompletedAt),
		},
	}), true, nil
}

func (service *Tracker) ListRepositories(
	ctx context.Context,
	request *connect.Request[realqav1.ListRepositoriesRequest],
) (*connect.Response[realqav1.ListRepositoriesResponse], error) {
	if request == nil || request.Msg == nil ||
		len(request.Msg.Query) > 255 || strings.ContainsAny(request.Msg.Query, "\x00\r\n") {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	actor, installation, err := service.authorizeInstallation(ctx, request.Msg.InstallationId)
	if err != nil {
		return nil, err
	}
	size := int32(defaultPageSize)
	if request.Msg.Page != nil {
		if request.Msg.Page.PageSize < 0 || request.Msg.Page.PageSize > maxPageSize {
			return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
		}
		if request.Msg.Page.PageSize > 0 {
			size = request.Msg.Page.PageSize
		}
	}
	after := ""
	if request.Msg.Page != nil && request.Msg.Page.Cursor != "" {
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(request.Msg.Page.Cursor)
		if decodeErr != nil || len(decoded) == 0 || len(decoded) > 255 {
			return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
		}
		after = string(decoded)
	}
	if service.dependencies.GitHubProvider != nil {
		page, providerErr := service.dependencies.GitHubProvider.ListRepositories(
			ctx, actor.accountID, installation, realqagithub.RepositoryPageRequest{
				Query: request.Msg.Query, Cursor: after, PageSize: int(size),
			})
		if providerErr != nil &&
			!errors.Is(providerErr, realqagithub.ErrCallerAuthorizationUnavailable) {
			return nil, rqerr.New(connect.CodeUnavailable,
				realqav1.ErrorReason_ERROR_REASON_GITHUB_DISCONNECTED,
				realqav1.FailureClass_FAILURE_CLASS_RETRYABLE, 0)
		}
		if providerErr == nil {
			response := &realqav1.ListRepositoriesResponse{
				Repositories: make(
					[]*realqav1.Repository, 0, len(page.Repositories)),
				Page: &realqav1.PageResponse{},
			}
			for _, row := range page.Repositories {
				response.Repositories = append(response.Repositories, &realqav1.Repository{
					Repository: &realqav1.GitHubRepositoryRef{
						RepositoryId: strconv.FormatInt(row.ID, 10),
						Owner:        row.Owner, Name: row.Name,
					},
					InstallationId: &realqav1.UuidV7{Value: installation.String()},
					IssuesEnabled:  row.IssuesEnabled, CallerCanSubmit: row.CanSubmit,
				})
			}
			if page.NextCursor != "" {
				response.Page.NextCursor = base64.RawURLEncoding.EncodeToString(
					[]byte(page.NextCursor))
			}
			return connect.NewResponse(response), nil
		}
	}
	rows, err := service.dependencies.Store.Queries().ListAccessibleRepositories(
		ctx, dbgen.ListAccessibleRepositoriesParams{
			InstallationID: toPGUUID(installation), AccountID: toPGUUID(actor.accountID),
			Query: request.Msg.Query, AfterID: after, PageLimit: size + 1,
		})
	if err != nil {
		return nil, err
	}
	hasMore := len(rows) > int(size)
	if hasMore {
		rows = rows[:size]
	}
	response := &realqav1.ListRepositoriesResponse{
		Repositories: make([]*realqav1.Repository, 0, len(rows)),
		Page:         &realqav1.PageResponse{},
	}
	last := ""
	for _, row := range rows {
		last = row.RepositoryID
		response.Repositories = append(response.Repositories, &realqav1.Repository{
			Repository: &realqav1.GitHubRepositoryRef{
				RepositoryId: row.RepositoryID, Owner: row.RepositoryOwner,
				Name: row.RepositoryName,
			},
			InstallationId: &realqav1.UuidV7{Value: installation.String()},
			IssuesEnabled:  row.IssuesEnabled, CallerCanSubmit: row.CanSubmit,
		})
	}
	if hasMore {
		response.Page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(last))
	}
	return connect.NewResponse(response), nil
}

func (service *Tracker) GetRepositoryIssueSchema(
	ctx context.Context,
	request *connect.Request[realqav1.GetRepositoryIssueSchemaRequest],
) (*connect.Response[realqav1.GetRepositoryIssueSchemaResponse], error) {
	if request == nil || request.Msg == nil || request.Msg.Repository == nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_SCHEMA_INVALID)
	}
	actor, installation, err := service.authorizeInstallation(ctx, request.Msg.InstallationId)
	if err != nil {
		return nil, err
	}
	if service.dependencies.GitHubProvider != nil {
		repositoryID, parseErr := strconv.ParseInt(
			request.Msg.Repository.RepositoryId, 10, 64)
		if parseErr != nil || repositoryID <= 0 {
			return nil, invalid(
				realqav1.ErrorReason_ERROR_REASON_PROVIDER_SCHEMA_INVALID)
		}
		repository := realqagithub.Repository{
			ID: repositoryID, NodeID: "R_" + strconv.FormatInt(repositoryID, 10),
			Owner: request.Msg.Repository.Owner, Name: request.Msg.Repository.Name,
			IssuesEnabled: true, CanSubmit: true,
		}
		definitions, providerErr :=
			service.dependencies.GitHubProvider.GetRepositoryDefinitions(
				ctx, actor.accountID, installation, repository)
		if providerErr != nil &&
			!errors.Is(providerErr, realqagithub.ErrCallerAuthorizationUnavailable) {
			return nil, rqerr.New(connect.CodeUnavailable,
				realqav1.ErrorReason_ERROR_REASON_PROVIDER_SCHEMA_INVALID,
				realqav1.FailureClass_FAILURE_CLASS_RETRYABLE, 0)
		}
		if providerErr == nil {
			schema := repositoryDefinitionsProto(request.Msg.Repository, definitions)
			if err = service.persistRepositoryDefinitions(
				ctx, installation, request.Msg.Repository.RepositoryId, schema,
			); err != nil {
				return nil, err
			}
			return connect.NewResponse(&realqav1.GetRepositoryIssueSchemaResponse{
				Schema: schema,
			}), nil
		}
	}
	access, err := service.dependencies.Store.Queries().GetRepositorySubmitAccess(
		ctx, dbgen.GetRepositorySubmitAccessParams{
			InstallationID: toPGUUID(installation), AccountID: toPGUUID(actor.accountID),
			RepositoryID: request.Msg.Repository.RepositoryId,
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, permissionDenied()
	}
	if err != nil {
		return nil, err
	}
	rows, err := service.dependencies.Store.Queries().ListRepositoryDefinitions(
		ctx, dbgen.ListRepositoryDefinitionsParams{
			InstallationID: toPGUUID(installation),
			RepositoryID:   request.Msg.Repository.RepositoryId,
		})
	if err != nil {
		return nil, err
	}
	schema := &realqav1.RepositoryIssueSchema{
		Repository: &realqav1.GitHubRepositoryRef{
			RepositoryId: access.RepositoryID,
			Owner:        access.RepositoryOwner,
			Name:         access.RepositoryName,
		},
		MarkdownTemplates: []*realqav1.MarkdownIssueTemplate{},
		IssueForms:        []*realqav1.IssueForm{},
	}
	var revision int64 = 1
	for _, row := range rows {
		if row.Revision > revision {
			revision = row.Revision
		}
		kind, kindErr := definitionKindValue(row.Kind)
		if kindErr != nil {
			return nil, kindErr
		}
		definition := &realqav1.RepositoryIssueDefinitionRef{
			Kind:         kind,
			DefinitionId: row.DefinitionID,
			Name:         row.Name,
			Path:         row.Path,
			Etag:         row.Etag,
		}
		switch row.Kind {
		case "markdown_template":
			item := &realqav1.MarkdownIssueTemplate{}
			if err = protojson.Unmarshal(row.SchemaPayload, item); err != nil {
				return nil, rqerr.New(connect.CodeUnavailable,
					realqav1.ErrorReason_ERROR_REASON_PROVIDER_SCHEMA_INVALID,
					realqav1.FailureClass_FAILURE_CLASS_RETRYABLE, 0)
			}
			item.Definition = definition
			schema.MarkdownTemplates = append(schema.MarkdownTemplates, item)
		case "issue_form":
			item := &realqav1.IssueForm{}
			if err = protojson.Unmarshal(row.SchemaPayload, item); err != nil {
				return nil, rqerr.New(connect.CodeUnavailable,
					realqav1.ErrorReason_ERROR_REASON_PROVIDER_SCHEMA_INVALID,
					realqav1.FailureClass_FAILURE_CLASS_RETRYABLE, 0)
			}
			item.Definition = definition
			schema.IssueForms = append(schema.IssueForms, item)
		default:
			return nil, errors.New("realqa tracker: invalid stored definition")
		}
	}
	schema.Revision = rqerr.Revision(revision)
	return connect.NewResponse(&realqav1.GetRepositoryIssueSchemaResponse{
		Schema: schema,
	}), nil
}

func (service *Tracker) persistRepositoryDefinitions(
	ctx context.Context,
	installationID uuid.UUID,
	repositoryID string,
	schema *realqav1.RepositoryIssueSchema,
) error {
	if schema == nil {
		return errors.New("realqa tracker: provider schema is unavailable")
	}
	return service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			parameters := dbgen.DeleteRepositoryDefinitionsParams{
				InstallationID: toPGUUID(installationID),
				RepositoryID:   repositoryID,
			}
			if _, err := queries.DeleteRepositoryDefinitions(
				ctx, parameters,
			); err != nil {
				return err
			}
			for _, item := range schema.MarkdownTemplates {
				payload, err := protojson.Marshal(item)
				if err != nil {
					return err
				}
				definition := item.Definition
				if definition == nil {
					return errors.New("realqa tracker: provider definition is invalid")
				}
				if err = queries.UpsertRepositoryDefinition(
					ctx, dbgen.UpsertRepositoryDefinitionParams{
						InstallationID: toPGUUID(installationID),
						RepositoryID:   repositoryID,
						Kind:           "markdown_template",
						DefinitionID:   definition.DefinitionId,
						Name:           definition.Name,
						Path:           definition.Path,
						Etag:           definition.Etag,
						SchemaPayload:  payload,
					}); err != nil {
					return err
				}
			}
			for _, item := range schema.IssueForms {
				payload, err := protojson.Marshal(item)
				if err != nil {
					return err
				}
				definition := item.Definition
				if definition == nil {
					return errors.New("realqa tracker: provider definition is invalid")
				}
				if err = queries.UpsertRepositoryDefinition(
					ctx, dbgen.UpsertRepositoryDefinitionParams{
						InstallationID: toPGUUID(installationID),
						RepositoryID:   repositoryID,
						Kind:           "issue_form",
						DefinitionID:   definition.DefinitionId,
						Name:           definition.Name,
						Path:           definition.Path,
						Etag:           definition.Etag,
						SchemaPayload:  payload,
					}); err != nil {
					return err
				}
			}
			return nil
		})
}

func (service *Tracker) authorizeInstallation(
	ctx context.Context,
	value *realqav1.UuidV7,
) (caller, uuid.UUID, error) {
	actor, err := resolveCaller(ctx, service.dependencies)
	if err != nil {
		return caller{}, uuid.Nil, err
	}
	id, err := parseUUIDMessage(value)
	if err != nil {
		return caller{}, uuid.Nil, invalid(
			realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	installation, err := service.dependencies.Store.Queries().GetGitHubInstallation(
		ctx, toPGUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return caller{}, uuid.Nil, rqerr.New(connect.CodeNotFound,
			realqav1.ErrorReason_ERROR_REASON_GITHUB_DISCONNECTED,
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0)
	}
	if err != nil {
		return caller{}, uuid.Nil, err
	}
	ownerID, err := fromPGUUID(installation.OwnerID)
	if err != nil {
		return caller{}, uuid.Nil, err
	}
	if _, err = authorizeOwner(ctx, service.dependencies, actor,
		owner{kind: installation.OwnerKind, id: ownerID}, false, false); err != nil {
		return caller{}, uuid.Nil, err
	}
	return actor, id, nil
}

func connectionProto(record dbgen.RealqaGithubConnection) (*realqav1.GitHubConnection, error) {
	id, err := fromPGUUID(record.ID)
	if err != nil {
		return nil, err
	}
	ownerID, err := fromPGUUID(record.OwnerID)
	if err != nil {
		return nil, err
	}
	var state realqav1.GitHubConnectionState
	switch record.State {
	case "disconnected":
		state = realqav1.GitHubConnectionState_GIT_HUB_CONNECTION_STATE_DISCONNECTED
	case "pending":
		state = realqav1.GitHubConnectionState_GIT_HUB_CONNECTION_STATE_PENDING
	case "connected":
		state = realqav1.GitHubConnectionState_GIT_HUB_CONNECTION_STATE_CONNECTED
	default:
		return nil, errors.New("realqa tracker: invalid connection state")
	}
	return &realqav1.GitHubConnection{
		ConnectionId: &realqav1.UuidV7{Value: id.String()},
		Owner:        ownerProto(owner{kind: record.OwnerKind, id: ownerID}),
		State:        state, GithubLogin: record.GithubLogin,
		Revision:    rqerr.Revision(record.Revision),
		ConnectedAt: timestamp(record.ConnectedAt),
	}, nil
}

func repositoryDefinitionsProto(
	repository *realqav1.GitHubRepositoryRef,
	definitions realqagithub.RepositoryDefinitions,
) *realqav1.RepositoryIssueSchema {
	result := &realqav1.RepositoryIssueSchema{
		Repository: repository, MarkdownTemplates: []*realqav1.MarkdownIssueTemplate{},
		IssueForms: []*realqav1.IssueForm{}, Revision: rqerr.Revision(1),
	}
	for _, template := range definitions.Markdown {
		result.MarkdownTemplates = append(result.MarkdownTemplates,
			&realqav1.MarkdownIssueTemplate{
				Definition:       repositoryDefinitionRefProto(template.Definition),
				TitlePrefix:      template.TitlePrefix,
				DefaultLabels:    append([]string(nil), template.DefaultLabels...),
				DefaultAssignees: append([]string(nil), template.DefaultAssignees...),
				BodyTemplate:     template.Body,
			})
	}
	for _, form := range definitions.Forms {
		item := &realqav1.IssueForm{
			Definition:       repositoryDefinitionRefProto(form.Definition),
			TitlePrefix:      form.TitlePrefix,
			DefaultLabels:    append([]string(nil), form.DefaultLabels...),
			DefaultAssignees: append([]string(nil), form.DefaultAssignees...),
			Fields:           []*realqav1.IssueFormField{},
		}
		for _, field := range form.Fields {
			protoField := &realqav1.IssueFormField{
				FieldId: field.ID, Kind: issueFormFieldKindProto(field.Kind),
				Label: field.Label, Description: field.Description,
				Placeholder: field.Placeholder, Required: field.Required,
				Options: []*realqav1.IssueFormOption{},
			}
			if field.Kind == realqagithub.FormFieldMarkdown {
				protoField.Description = field.Markdown
			}
			for _, option := range field.Options {
				protoField.Options = append(protoField.Options,
					&realqav1.IssueFormOption{
						Value: option.Label, Label: option.Label,
						Required: option.Required,
					})
			}
			item.Fields = append(item.Fields, protoField)
		}
		result.IssueForms = append(result.IssueForms, item)
	}
	return result
}

func repositoryDefinitionRefProto(
	definition realqagithub.DefinitionRef,
) *realqav1.RepositoryIssueDefinitionRef {
	kind := realqav1.RepositoryIssueDefinitionKind_REPOSITORY_ISSUE_DEFINITION_KIND_MARKDOWN_TEMPLATE
	if definition.Kind == realqagithub.DefinitionForm {
		kind = realqav1.RepositoryIssueDefinitionKind_REPOSITORY_ISSUE_DEFINITION_KIND_ISSUE_FORM
	}
	return &realqav1.RepositoryIssueDefinitionRef{
		Kind: kind, DefinitionId: definition.ID, Name: definition.Name,
		Path: definition.Path, Etag: definition.ETag,
	}
}

func issueFormFieldKindProto(
	kind realqagithub.FormFieldKind,
) realqav1.IssueFormFieldKind {
	switch kind {
	case realqagithub.FormFieldMarkdown:
		return realqav1.IssueFormFieldKind_ISSUE_FORM_FIELD_KIND_MARKDOWN
	case realqagithub.FormFieldInput:
		return realqav1.IssueFormFieldKind_ISSUE_FORM_FIELD_KIND_INPUT
	case realqagithub.FormFieldTextarea:
		return realqav1.IssueFormFieldKind_ISSUE_FORM_FIELD_KIND_TEXTAREA
	case realqagithub.FormFieldDropdown:
		return realqav1.IssueFormFieldKind_ISSUE_FORM_FIELD_KIND_DROPDOWN
	case realqagithub.FormFieldCheckboxes:
		return realqav1.IssueFormFieldKind_ISSUE_FORM_FIELD_KIND_CHECKBOXES
	default:
		return realqav1.IssueFormFieldKind_ISSUE_FORM_FIELD_KIND_UNSPECIFIED
	}
}
