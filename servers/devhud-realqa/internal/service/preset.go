package service

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	realqagithub "github.com/delinoio/oss/servers/devhud-realqa/internal/github"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/rqerr"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/rules"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/proto"
)

var errIdempotencyReplay = errors.New("realqa service: idempotency replay")

type presetInput struct {
	scope        owner
	billingOrg   uuid.UUID
	billingTeam  uuid.UUID
	installation uuid.UUID
	destination  *realqav1.TrackerDestination
	definition   *realqav1.RepositoryIssueDefinitionRef
	name         string
	captureMode  string
	pointer      bool
	selectorMode string
	labels       []string
	assignees    []string
	milestone    pgtype.Int8
	projects     []string
	ruleValues   []*realqav1.ProcessUrlRule
	shortcut     *realqav1.ShortcutDefinition
}

func (service *Preset) CreatePreset(
	ctx context.Context,
	request *connect.Request[realqav1.CreatePresetRequest],
) (*connect.Response[realqav1.CreatePresetResponse], error) {
	if request == nil || request.Msg == nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
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
	if replay, ok, replayErr := service.createReplay(ctx, actor, idempotencyID, digest); ok {
		return replay, replayErr
	}
	scope, err := parseOwner(request.Msg.Owner)
	if err != nil {
		return nil, err
	}
	if _, err = authorizeOwner(ctx, service.dependencies, actor, scope, true, false); err != nil {
		return nil, err
	}
	input, err := service.validateCreateInput(ctx, actor, scope, request.Msg)
	if err != nil {
		return nil, err
	}
	presetID, err := newID(service.dependencies)
	if err != nil {
		return nil, err
	}
	recordID, err := newID(service.dependencies)
	if err != nil {
		return nil, err
	}
	var created *realqav1.Preset
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if err := lockActiveOwnerScope(ctx, queries, scope); err != nil {
				return err
			}
			if existing, lookupErr := queries.GetIdempotencyRecord(
				ctx, idempotencyLookup(actor, idempotencyID),
			); lookupErr == nil {
				if !bytes.Equal(existing.RequestDigest, digest) {
					return idempotencyConflict()
				}
				return errIdempotencyReplay
			} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
				return lookupErr
			}
			count, countErr := queries.CountPresetsForOwner(
				ctx, dbgen.CountPresetsForOwnerParams{
					OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
				},
			)
			if countErr != nil {
				return countErr
			}
			limit := int64(personalPresetLimit)
			if scope.kind == "organization" {
				limit = organizationPresetLimit
			}
			if count >= limit {
				return rqerr.New(connect.CodeResourceExhausted,
					realqav1.ErrorReason_ERROR_REASON_PRESET_LIMIT_EXCEEDED,
					realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0)
			}
			if accessErr := revalidatePresetAccess(ctx, queries, actor, &input); accessErr != nil {
				return accessErr
			}
			if input.shortcut != nil && input.shortcut.Active {
				if shortcutErr := queries.LockShortcutAccount(
					ctx, toPGUUID(actor.accountID)); shortcutErr != nil {
					return shortcutErr
				}
				shortcuts, shortcutErr := queries.CountActiveShortcutsForAccount(
					ctx, toPGUUID(actor.accountID))
				if shortcutErr != nil {
					return shortcutErr
				}
				if shortcuts >= deviceShortcutLimit {
					return shortcutLimitExceeded()
				}
			}
			destinationID, destinationErr := newID(service.dependencies)
			if destinationErr != nil {
				return destinationErr
			}
			destination, destinationErr := queries.UpsertDestination(
				ctx, dbgen.UpsertDestinationParams{
					ID: toPGUUID(destinationID), OwnerKind: scope.kind,
					OwnerID: toPGUUID(scope.id), InstallationID: toPGUUID(input.installation),
					RepositoryID:    input.destination.Repository.RepositoryId,
					RepositoryOwner: input.destination.Repository.Owner,
					RepositoryName:  input.destination.Repository.Name,
				},
			)
			if destinationErr != nil {
				return destinationErr
			}
			if _, createErr := queries.CreatePreset(ctx,
				createPresetParams(presetID, actor.accountID, destination.ID, input)); createErr != nil {
				return createErr
			}
			if createErr := createPresetChildren(ctx, queries, presetID, input); createErr != nil {
				return createErr
			}
			var createErr error
			created, createErr = loadPresetWithQueries(ctx, queries, presetID)
			if createErr != nil {
				return createErr
			}
			responsePayload, marshalErr := proto.MarshalOptions{Deterministic: true}.Marshal(created)
			if marshalErr != nil {
				return marshalErr
			}
			_, createErr = queries.CreateIdempotencyRecord(
				ctx, dbgen.CreateIdempotencyRecordParams{
					ID: toPGUUID(recordID), CallerKind: "user", CallerDigest: actor.digest,
					Operation: "create_preset", IdempotencyKey: toPGUUID(idempotencyID),
					RequestDigest: digest, ResourceID: toPGUUID(presetID),
					ResponsePayload: responsePayload,
				},
			)
			return createErr
		})
	if err != nil {
		if replay, ok, replayErr := service.createReplay(ctx, actor, idempotencyID, digest); ok {
			return replay, replayErr
		}
		return nil, err
	}
	audit(ctx, service.dependencies, actor, "preset_created", scope, presetID, "allow", "success")
	return connect.NewResponse(&realqav1.CreatePresetResponse{
		Preset: created,
		Idempotency: &realqav1.IdempotencyResult{
			Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_PRESET,
			OriginallyCompletedAt: created.CreatedAt,
		},
	}), nil
}

func (service *Preset) validateCreateInput(
	ctx context.Context,
	actor caller,
	scope owner,
	request *realqav1.CreatePresetRequest,
) (presetInput, error) {
	return service.validateInput(ctx, actor, scope, presetInput{
		scope:       scope,
		destination: request.Destination,
		definition:  request.IssueDefinition,
		name:        request.Name,
		pointer:     request.IncludePointerByDefault,
		labels:      request.DefaultLabels,
		assignees:   request.DefaultAssignees,
		ruleValues:  request.ProcessUrlRules,
		shortcut:    request.Shortcut,
	}, request.Billing, request.DefaultCaptureMode, request.DefaultSelectorMode,
		request.ProviderExtension)
}

func (service *Preset) validateInput(
	ctx context.Context,
	actor caller,
	scope owner,
	input presetInput,
	billing *realqav1.BillingScope,
	capture realqav1.CaptureMode,
	selector realqav1.SelectorMode,
	extension *realqav1.ProviderExtension,
) (presetInput, error) {
	if strings.TrimSpace(input.name) != input.name || input.name == "" ||
		len(input.name) > 120 || strings.ContainsAny(input.name, "\x00\r\n") {
		return presetInput{}, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	var err error
	input.captureMode, err = captureModeName(capture)
	if err != nil {
		return presetInput{}, err
	}
	input.selectorMode, err = selectorModeName(selector)
	if err != nil {
		return presetInput{}, err
	}
	input.billingOrg, input.billingTeam, err = authorizeBilling(
		ctx, service.dependencies, actor, scope, billing)
	if err != nil {
		return presetInput{}, err
	}
	liveDefinitions, err := service.refreshProviderSelection(
		ctx, actor, scope, input.destination)
	if err != nil {
		return presetInput{}, err
	}
	var repository dbgen.RealqaRepositoryAccess
	input.installation, repository, err = authorizeRepository(
		ctx, service.dependencies, actor, scope, input.destination)
	if err != nil {
		return presetInput{}, err
	}
	input.destination = proto.Clone(input.destination).(*realqav1.TrackerDestination)
	input.destination.Repository = &realqav1.GitHubRepositoryRef{
		RepositoryId: repository.RepositoryID,
		Owner:        repository.RepositoryOwner,
		Name:         repository.RepositoryName,
	}
	if input.definition != nil {
		input.definition = proto.Clone(input.definition).(*realqav1.RepositoryIssueDefinitionRef)
	}
	if liveDefinitions != nil {
		err = validateLiveDefinition(&input, *liveDefinitions)
	} else {
		err = service.validateDefinition(ctx, &input)
	}
	if err != nil {
		return presetInput{}, err
	}
	input.labels, err = cleanStringList(input.labels, 100, 255)
	if err != nil {
		return presetInput{}, err
	}
	input.assignees, err = cleanStringList(input.assignees, 100, 255)
	if err != nil {
		return presetInput{}, err
	}
	input.projects = []string{}
	if extension != nil {
		github := extension.GetGithub()
		if github == nil || github.MilestoneNumber < 0 {
			return presetInput{}, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
		}
		if github.MilestoneNumber > 0 {
			input.milestone = pgtype.Int8{Int64: github.MilestoneNumber, Valid: true}
		}
		input.projects, err = cleanStringList(github.ProjectNodeIds, 20, 255)
		if err != nil {
			return presetInput{}, err
		}
	}
	compiled := make([]rules.Rule, 0, len(input.ruleValues))
	for _, value := range input.ruleValues {
		if value == nil {
			return presetInput{}, invalid(realqav1.ErrorReason_ERROR_REASON_RULE_INVALID)
		}
		if _, err = parseUUIDMessage(value.RuleId); err != nil {
			return presetInput{}, invalid(realqav1.ErrorReason_ERROR_REASON_RULE_INVALID)
		}
		compiled = append(compiled, rules.Rule{
			ExactProcessName: value.ExactProcessName,
			TitlePattern:     value.SafeWindowTitlePattern,
			URLTemplate:      value.UrlTemplate,
			Enabled:          value.Enabled,
		})
	}
	if _, err = rules.Compile(compiled); err != nil {
		return presetInput{}, invalid(realqav1.ErrorReason_ERROR_REASON_RULE_INVALID)
	}
	if input.shortcut != nil {
		if _, err = parseUUIDMessage(input.shortcut.ShortcutId); err != nil ||
			input.shortcut.Accelerator == "" ||
			len(input.shortcut.Accelerator) > 128 ||
			strings.ContainsAny(input.shortcut.Accelerator, "\x00\r\n") {
			return presetInput{}, invalid(
				realqav1.ErrorReason_ERROR_REASON_DEVICE_SHORTCUT_LIMIT_EXCEEDED)
		}
	}
	return input, nil
}

func (service *Preset) refreshProviderSelection(
	ctx context.Context,
	actor caller,
	scope owner,
	destination *realqav1.TrackerDestination,
) (*realqagithub.RepositoryDefinitions, error) {
	if service.dependencies.GitHubProvider == nil {
		return nil, nil
	}
	if destination == nil || destination.InstallationId == nil ||
		destination.Repository == nil {
		return nil, invalid(
			realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	installationID, err := parseUUIDv7(destination.InstallationId.Value)
	repositoryID, parseErr := strconv.ParseInt(
		destination.Repository.RepositoryId, 10, 64)
	if err != nil || parseErr != nil || repositoryID <= 0 {
		return nil, invalid(
			realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	installation, err := service.dependencies.Store.Queries().GetGitHubInstallation(
		ctx, toPGUUID(installationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, permissionDenied()
	}
	if err != nil {
		return nil, err
	}
	installationOwnerID, err := fromPGUUID(installation.OwnerID)
	if err != nil || installation.OwnerKind != scope.kind ||
		installationOwnerID != scope.id {
		return nil, permissionDenied()
	}
	definitions, err := service.dependencies.GitHubProvider.GetRepositoryDefinitions(
		ctx, actor.accountID, installationID, realqagithub.Repository{
			ID: repositoryID, NodeID: "R_" + strconv.FormatInt(repositoryID, 10),
			Owner: destination.Repository.Owner, Name: destination.Repository.Name,
			IssuesEnabled: true, CanSubmit: true,
		})
	if errors.Is(err, realqagithub.ErrCallerAuthorizationUnavailable) {
		return nil, nil
	}
	if err != nil {
		return nil, rqerr.New(connect.CodePermissionDenied,
			realqav1.ErrorReason_ERROR_REASON_PROVIDER_PERMISSION_DENIED,
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0)
	}
	return &definitions, nil
}

func validateLiveDefinition(
	input *presetInput,
	definitions realqagithub.RepositoryDefinitions,
) error {
	if input == nil || input.definition == nil {
		return invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_SCHEMA_INVALID)
	}
	kind, err := definitionKindName(input.definition.Kind)
	if err != nil {
		return err
	}
	matches := func(definition realqagithub.DefinitionRef) bool {
		return string(definition.Kind) == kind &&
			definition.ID == input.definition.DefinitionId &&
			definition.ETag == input.definition.Etag &&
			definition.Path == input.definition.Path
	}
	for _, definition := range definitions.Markdown {
		if matches(definition.Definition) {
			input.definition.Name = definition.Definition.Name
			return nil
		}
	}
	for _, definition := range definitions.Forms {
		if matches(definition.Definition) {
			input.definition.Name = definition.Definition.Name
			return nil
		}
	}
	return rqerr.New(connect.CodeFailedPrecondition,
		realqav1.ErrorReason_ERROR_REASON_PROVIDER_SCHEMA_INVALID,
		realqav1.FailureClass_FAILURE_CLASS_CONFLICT, 0)
}

func revalidatePresetAccess(
	ctx context.Context,
	queries *dbgen.Queries,
	actor caller,
	input *presetInput,
) error {
	payer := owner{kind: "organization", id: input.billingOrg}
	if payer != input.scope {
		if err := lockActiveOwnerScope(ctx, queries, payer); err != nil {
			return err
		}
	}
	allowed, err := queries.HasPayerTeamAccess(ctx, dbgen.HasPayerTeamAccessParams{
		AccountID:      toPGUUID(actor.accountID),
		OrganizationID: toPGUUID(input.billingOrg),
		TeamID:         toPGUUID(input.billingTeam),
	})
	if err != nil {
		return err
	}
	if !allowed {
		return permissionDenied()
	}
	repository, err := queries.GetRepositorySubmitAccessForOwner(
		ctx, dbgen.GetRepositorySubmitAccessForOwnerParams{
			InstallationID: toPGUUID(input.installation),
			AccountID:      toPGUUID(actor.accountID),
			RepositoryID:   input.destination.Repository.RepositoryId,
			OwnerKind:      input.scope.kind,
			OwnerID:        toPGUUID(input.scope.id),
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return rqerr.New(connect.CodePermissionDenied,
			realqav1.ErrorReason_ERROR_REASON_PROVIDER_PERMISSION_DENIED,
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0)
	}
	if err != nil {
		return err
	}
	input.destination.Repository = &realqav1.GitHubRepositoryRef{
		RepositoryId: repository.RepositoryID,
		Owner:        repository.RepositoryOwner,
		Name:         repository.RepositoryName,
	}
	return nil
}

func (service *Preset) validateDefinition(ctx context.Context, input *presetInput) error {
	if input == nil || input.definition == nil || input.definition.DefinitionId == "" ||
		input.definition.Etag == "" || input.definition.Path == "" {
		return invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_SCHEMA_INVALID)
	}
	kind, err := definitionKindName(input.definition.Kind)
	if err != nil {
		return err
	}
	definitions, err := service.dependencies.Store.Queries().ListRepositoryDefinitions(
		ctx, dbgen.ListRepositoryDefinitionsParams{
			InstallationID: toPGUUID(input.installation),
			RepositoryID:   input.destination.Repository.RepositoryId,
		},
	)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		if definition.Kind == kind &&
			definition.DefinitionID == input.definition.DefinitionId &&
			definition.Etag == input.definition.Etag &&
			definition.Path == input.definition.Path {
			input.definition.Name = definition.Name
			return nil
		}
	}
	return rqerr.New(connect.CodeFailedPrecondition,
		realqav1.ErrorReason_ERROR_REASON_PROVIDER_SCHEMA_INVALID,
		realqav1.FailureClass_FAILURE_CLASS_CONFLICT, 0)
}

func createPresetParams(
	presetID uuid.UUID,
	accountID uuid.UUID,
	destinationID pgtype.UUID,
	input presetInput,
) dbgen.CreatePresetParams {
	kind, _ := definitionKindName(input.definition.Kind)
	return dbgen.CreatePresetParams{
		ID: toPGUUID(presetID), OwnerKind: input.scope.kind,
		OwnerID: toPGUUID(input.scope.id), CreatedByAccountID: toPGUUID(accountID),
		PayerOrganizationID: toPGUUID(input.billingOrg),
		PayerTeamID:         toPGUUID(input.billingTeam), DestinationID: destinationID,
		Name: input.name, CaptureMode: input.captureMode, IncludePointer: input.pointer,
		SelectorMode: input.selectorMode, IssueDefinitionKind: kind,
		IssueDefinitionID:   input.definition.DefinitionId,
		IssueDefinitionName: input.definition.Name,
		IssueDefinitionPath: input.definition.Path,
		IssueDefinitionEtag: input.definition.Etag,
		DefaultLabels:       input.labels, DefaultAssignees: input.assignees,
		MilestoneNumber: input.milestone, ProjectNodeIds: input.projects,
	}
}

func createPresetChildren(
	ctx context.Context,
	queries *dbgen.Queries,
	presetID uuid.UUID,
	input presetInput,
) error {
	for index, value := range input.ruleValues {
		ruleID, _ := parseUUIDMessage(value.RuleId)
		if err := queries.CreateProcessURLRule(ctx, dbgen.CreateProcessURLRuleParams{
			ID: toPGUUID(ruleID), PresetID: toPGUUID(presetID), Ordinal: int32(index),
			ExactProcessName:       value.ExactProcessName,
			SafeWindowTitlePattern: value.SafeWindowTitlePattern,
			UrlTemplate:            value.UrlTemplate, Enabled: value.Enabled,
		}); err != nil {
			return err
		}
	}
	if input.shortcut != nil {
		shortcutID, _ := parseUUIDMessage(input.shortcut.ShortcutId)
		return queries.CreateShortcut(ctx, dbgen.CreateShortcutParams{
			ID: toPGUUID(shortcutID), PresetID: toPGUUID(presetID),
			Accelerator: input.shortcut.Accelerator, Active: input.shortcut.Active,
		})
	}
	return nil
}

func (service *Preset) createReplay(
	ctx context.Context,
	actor caller,
	idempotencyID uuid.UUID,
	digest []byte,
) (*connect.Response[realqav1.CreatePresetResponse], bool, error) {
	record, err := service.dependencies.Store.Queries().GetIdempotencyRecord(
		ctx, idempotencyLookup(actor, idempotencyID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if !bytes.Equal(record.RequestDigest, digest) {
		return nil, true, idempotencyConflict()
	}
	preset := new(realqav1.Preset)
	if len(record.ResponsePayload) > 0 {
		if err := proto.Unmarshal(record.ResponsePayload, preset); err != nil {
			return nil, true, err
		}
	} else {
		presetID, parseErr := fromPGUUID(record.ResourceID)
		if parseErr != nil {
			return nil, true, parseErr
		}
		preset, err = service.loadPreset(ctx, presetID)
		if err != nil {
			return nil, true, err
		}
	}
	return connect.NewResponse(&realqav1.CreatePresetResponse{
		Preset: preset,
		Idempotency: &realqav1.IdempotencyResult{
			Replayed:              true,
			Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_PRESET,
			OriginallyCompletedAt: timestamp(record.CompletedAt),
		},
	}), true, nil
}

func idempotencyLookup(actor caller, id uuid.UUID) dbgen.GetIdempotencyRecordParams {
	return idempotencyLookupFor(actor, id, "create_preset")
}

func idempotencyLookupFor(
	actor caller,
	id uuid.UUID,
	operation string,
) dbgen.GetIdempotencyRecordParams {
	return dbgen.GetIdempotencyRecordParams{
		CallerKind: "user", CallerDigest: actor.digest,
		Operation: operation, IdempotencyKey: toPGUUID(id),
	}
}

func idempotencyConflict() error {
	return rqerr.New(connect.CodeAlreadyExists,
		realqav1.ErrorReason_ERROR_REASON_IDEMPOTENCY_CONFLICT,
		realqav1.FailureClass_FAILURE_CLASS_CONFLICT, 0)
}

func parseIdempotency(value *realqav1.IdempotencyKey) (uuid.UUID, error) {
	if value == nil {
		return uuid.Nil, idempotencyConflict()
	}
	id, err := parseUUIDMessage(value.Value)
	if err != nil {
		return uuid.Nil, idempotencyConflict()
	}
	return id, nil
}

func parseUUIDMessage(value *realqav1.UuidV7) (uuid.UUID, error) {
	if value == nil {
		return uuid.Nil, errors.New("UUID is required")
	}
	return parseUUIDv7(value.Value)
}

func (service *Preset) ListPresets(
	ctx context.Context,
	request *connect.Request[realqav1.ListPresetsRequest],
) (*connect.Response[realqav1.ListPresetsResponse], error) {
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
	var response *realqav1.ListPresetsResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	}, func(queries *dbgen.Queries) error {
		rows, listErr := queries.ListPresetRecords(
			ctx, dbgen.ListPresetRecordsParams{
				OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
				AfterID: pageLowerBound(after), PageLimit: size + 1,
			})
		if listErr != nil {
			return listErr
		}
		hasMore := len(rows) > int(size)
		if hasMore {
			rows = rows[:size]
		}
		response = &realqav1.ListPresetsResponse{
			Presets: make([]*realqav1.Preset, 0, len(rows)),
			Page:    &realqav1.PageResponse{},
		}
		var last uuid.UUID
		for _, row := range rows {
			last, listErr = fromPGUUID(row.ID)
			if listErr != nil {
				return listErr
			}
			preset, loadErr := loadPresetWithQueries(ctx, queries, last)
			if loadErr != nil {
				return loadErr
			}
			response.Presets = append(response.Presets, preset)
		}
		response.Page.NextCursor = cursor(last, hasMore)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func (service *Preset) GetPreset(
	ctx context.Context,
	request *connect.Request[realqav1.GetPresetRequest],
) (*connect.Response[realqav1.GetPresetResponse], error) {
	if request == nil || request.Msg == nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	actor, err := resolveCaller(ctx, service.dependencies)
	if err != nil {
		return nil, err
	}
	id, err := parseUUIDMessage(request.Msg.PresetId)
	if err != nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	preset, err := service.loadPreset(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rqerr.New(connect.CodeNotFound,
			realqav1.ErrorReason_ERROR_REASON_OWNER_SCOPE_NOT_FOUND,
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0)
	}
	if err != nil {
		return nil, err
	}
	scope, _ := parseOwner(preset.Owner)
	if _, err = authorizeOwner(ctx, service.dependencies, actor, scope, false, false); err != nil {
		return nil, err
	}
	return connect.NewResponse(&realqav1.GetPresetResponse{Preset: preset}), nil
}

func (service *Preset) UpdatePreset(
	ctx context.Context,
	request *connect.Request[realqav1.UpdatePresetRequest],
) (*connect.Response[realqav1.UpdatePresetResponse], error) {
	if request == nil || request.Msg == nil || request.Msg.Preset == nil ||
		request.Msg.ExpectedRevision == nil || request.Msg.ExpectedRevision.Value <= 0 {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_STALE_REVISION)
	}
	actor, err := resolveCaller(ctx, service.dependencies)
	if err != nil {
		return nil, err
	}
	presetID, err := parseUUIDMessage(request.Msg.Preset.PresetId)
	if err != nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	current, err := service.loadPreset(ctx, presetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rqerr.New(connect.CodeNotFound,
			realqav1.ErrorReason_ERROR_REASON_OWNER_SCOPE_NOT_FOUND,
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0)
	}
	if err != nil {
		return nil, err
	}
	scope, err := parseOwner(current.Owner)
	if err != nil {
		return nil, err
	}
	requestScope, err := parseOwner(request.Msg.Preset.Owner)
	if err != nil || requestScope != scope {
		return nil, permissionDenied()
	}
	if _, err = authorizeOwner(ctx, service.dependencies, actor, scope, true, false); err != nil {
		return nil, err
	}
	if request.Msg.ExpectedRevision.Value != current.Revision.Value ||
		(request.Msg.Preset.Revision != nil &&
			request.Msg.Preset.Revision.Value != request.Msg.ExpectedRevision.Value) {
		return nil, stale(current.Revision.Value)
	}
	input, err := service.validateInput(ctx, actor, scope, presetInput{
		scope: scope, destination: request.Msg.Preset.Destination,
		definition: request.Msg.Preset.IssueDefinition, name: request.Msg.Preset.Name,
		pointer:    request.Msg.Preset.IncludePointerByDefault,
		labels:     request.Msg.Preset.DefaultLabels,
		assignees:  request.Msg.Preset.DefaultAssignees,
		ruleValues: request.Msg.Preset.ProcessUrlRules,
		shortcut:   request.Msg.Preset.Shortcut,
	}, request.Msg.Preset.Billing, request.Msg.Preset.DefaultCaptureMode,
		request.Msg.Preset.DefaultSelectorMode, request.Msg.Preset.ProviderExtension)
	if err != nil {
		return nil, err
	}
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if lockErr := lockActiveOwnerScope(ctx, queries, scope); lockErr != nil {
				return lockErr
			}
			locked, lockErr := queries.LockPreset(ctx, toPGUUID(presetID))
			if lockErr != nil {
				return lockErr
			}
			if locked.Revision != request.Msg.ExpectedRevision.Value {
				return stale(locked.Revision)
			}
			if accessErr := revalidatePresetAccess(ctx, queries, actor, &input); accessErr != nil {
				return accessErr
			}
			if input.shortcut != nil && input.shortcut.Active {
				if shortcutErr := queries.LockShortcutAccount(
					ctx, locked.CreatedByAccountID); shortcutErr != nil {
					return shortcutErr
				}
				shortcuts, shortcutErr := queries.CountOtherActiveShortcutsForAccount(
					ctx, dbgen.CountOtherActiveShortcutsForAccountParams{
						AccountID: locked.CreatedByAccountID,
						PresetID:  toPGUUID(presetID),
					})
				if shortcutErr != nil {
					return shortcutErr
				}
				if shortcuts >= deviceShortcutLimit {
					return shortcutLimitExceeded()
				}
			}
			destinationID, destinationErr := newID(service.dependencies)
			if destinationErr != nil {
				return destinationErr
			}
			destination, destinationErr := queries.UpsertDestination(ctx,
				dbgen.UpsertDestinationParams{
					ID: toPGUUID(destinationID), OwnerKind: scope.kind,
					OwnerID: toPGUUID(scope.id), InstallationID: toPGUUID(input.installation),
					RepositoryID:    input.destination.Repository.RepositoryId,
					RepositoryOwner: input.destination.Repository.Owner,
					RepositoryName:  input.destination.Repository.Name,
				})
			if destinationErr != nil {
				return destinationErr
			}
			params := updatePresetParams(presetID, request.Msg.ExpectedRevision.Value,
				destination.ID, input)
			if _, updateErr := queries.UpdatePreset(ctx, params); updateErr != nil {
				if errors.Is(updateErr, pgx.ErrNoRows) {
					return stale(locked.Revision)
				}
				return updateErr
			}
			if deleteErr := queries.DeleteProcessURLRules(ctx, toPGUUID(presetID)); deleteErr != nil {
				return deleteErr
			}
			for index, value := range input.ruleValues {
				ruleID, _ := parseUUIDMessage(value.RuleId)
				if createErr := queries.CreateProcessURLRule(ctx,
					dbgen.CreateProcessURLRuleParams{
						ID: toPGUUID(ruleID), PresetID: toPGUUID(presetID),
						Ordinal: int32(index), ExactProcessName: value.ExactProcessName,
						SafeWindowTitlePattern: value.SafeWindowTitlePattern,
						UrlTemplate:            value.UrlTemplate, Enabled: value.Enabled,
					}); createErr != nil {
					return createErr
				}
			}
			if input.shortcut != nil {
				shortcutID, _ := parseUUIDMessage(input.shortcut.ShortcutId)
				return queries.UpsertShortcut(ctx, dbgen.UpsertShortcutParams{
					ID: toPGUUID(shortcutID), PresetID: toPGUUID(presetID),
					Accelerator: input.shortcut.Accelerator, Active: input.shortcut.Active,
				})
			}
			return queries.DeleteShortcut(ctx, toPGUUID(presetID))
		})
	if err != nil {
		return nil, err
	}
	updated, err := service.loadPreset(ctx, presetID)
	if err != nil {
		return nil, err
	}
	audit(ctx, service.dependencies, actor, "preset_updated", scope, presetID, "allow", "success")
	return connect.NewResponse(&realqav1.UpdatePresetResponse{Preset: updated}), nil
}

func updatePresetParams(
	presetID uuid.UUID,
	expected int64,
	destinationID pgtype.UUID,
	input presetInput,
) dbgen.UpdatePresetParams {
	kind, _ := definitionKindName(input.definition.Kind)
	return dbgen.UpdatePresetParams{
		PayerOrganizationID: toPGUUID(input.billingOrg),
		PayerTeamID:         toPGUUID(input.billingTeam), DestinationID: destinationID,
		Name: input.name, CaptureMode: input.captureMode, IncludePointer: input.pointer,
		SelectorMode: input.selectorMode, IssueDefinitionKind: kind,
		IssueDefinitionID:   input.definition.DefinitionId,
		IssueDefinitionName: input.definition.Name,
		IssueDefinitionPath: input.definition.Path,
		IssueDefinitionEtag: input.definition.Etag,
		DefaultLabels:       input.labels, DefaultAssignees: input.assignees,
		MilestoneNumber: input.milestone, ProjectNodeIds: input.projects,
		ID: toPGUUID(presetID), ExpectedRevision: expected,
	}
}

func (service *Preset) DeletePreset(
	ctx context.Context,
	request *connect.Request[realqav1.DeletePresetRequest],
) (*connect.Response[realqav1.DeletePresetResponse], error) {
	if request == nil || request.Msg == nil || request.Msg.ExpectedRevision == nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_STALE_REVISION)
	}
	actor, err := resolveCaller(ctx, service.dependencies)
	if err != nil {
		return nil, err
	}
	presetID, err := parseUUIDMessage(request.Msg.PresetId)
	if err != nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	current, err := service.loadPreset(ctx, presetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rqerr.New(connect.CodeNotFound,
			realqav1.ErrorReason_ERROR_REASON_OWNER_SCOPE_NOT_FOUND,
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED, 0)
	}
	if err != nil {
		return nil, err
	}
	scope, _ := parseOwner(current.Owner)
	if _, err = authorizeOwner(ctx, service.dependencies, actor, scope, true, false); err != nil {
		return nil, err
	}
	if current.Revision.Value != request.Msg.ExpectedRevision.Value {
		return nil, stale(current.Revision.Value)
	}
	deleted, err := service.dependencies.Store.Queries().DeletePresetAtRevision(
		ctx, dbgen.DeletePresetAtRevisionParams{
			ID: toPGUUID(presetID), ExpectedRevision: request.Msg.ExpectedRevision.Value,
		})
	if errors.Is(err, pgx.ErrNoRows) {
		latest, lookupErr := service.loadPreset(ctx, presetID)
		if lookupErr == nil {
			return nil, stale(latest.Revision.Value)
		}
		return nil, stale(current.Revision.Value)
	}
	if err != nil {
		return nil, err
	}
	audit(ctx, service.dependencies, actor, "preset_deleted", scope, presetID, "allow", "success")
	return connect.NewResponse(&realqav1.DeletePresetResponse{
		PresetId:        request.Msg.PresetId,
		DeletedRevision: rqerr.Revision(int64(deleted)),
	}), nil
}

func (service *Preset) loadPreset(ctx context.Context, id uuid.UUID) (*realqav1.Preset, error) {
	var preset *realqav1.Preset
	err := service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	}, func(queries *dbgen.Queries) error {
		var loadErr error
		preset, loadErr = loadPresetWithQueries(ctx, queries, id)
		return loadErr
	})
	return preset, err
}

func loadPresetWithQueries(
	ctx context.Context,
	queries dbgen.Querier,
	id uuid.UUID,
) (*realqav1.Preset, error) {
	row, err := queries.GetPresetRecord(ctx, toPGUUID(id))
	if err != nil {
		return nil, err
	}
	ruleRows, err := queries.ListProcessURLRules(ctx, toPGUUID(id))
	if err != nil {
		return nil, err
	}
	ownerID, err := fromPGUUID(row.OwnerID)
	if err != nil {
		return nil, err
	}
	billingOrg, err := fromPGUUID(row.PayerOrganizationID)
	if err != nil {
		return nil, err
	}
	billingTeam, err := fromPGUUID(row.PayerTeamID)
	if err != nil {
		return nil, err
	}
	installationID, err := fromPGUUID(row.InstallationID)
	if err != nil {
		return nil, err
	}
	capture, err := captureModeValue(row.CaptureMode)
	if err != nil {
		return nil, err
	}
	selector, err := selectorModeValue(row.SelectorMode)
	if err != nil {
		return nil, err
	}
	definitionKind, err := definitionKindValue(row.IssueDefinitionKind)
	if err != nil {
		return nil, err
	}
	result := &realqav1.Preset{
		PresetId: &realqav1.UuidV7{Value: id.String()},
		Owner:    ownerProto(owner{kind: row.OwnerKind, id: ownerID}),
		Billing: &realqav1.BillingScope{
			OrganizationId: &realqav1.UuidV7{Value: billingOrg.String()},
			TeamId:         &realqav1.UuidV7{Value: billingTeam.String()},
		},
		Name: row.Name, DefaultCaptureMode: capture,
		IncludePointerByDefault: row.IncludePointer,
		DefaultSelectorMode:     selector,
		Destination: &realqav1.TrackerDestination{
			Tracker:        realqav1.TrackerKind_TRACKER_KIND_GITHUB_COM,
			InstallationId: &realqav1.UuidV7{Value: installationID.String()},
			Repository: &realqav1.GitHubRepositoryRef{
				RepositoryId: row.RepositoryID,
				Owner:        row.RepositoryOwner, Name: row.RepositoryName,
			},
		},
		IssueDefinition: &realqav1.RepositoryIssueDefinitionRef{
			Kind: definitionKind, DefinitionId: row.IssueDefinitionID,
			Name: row.IssueDefinitionName, Path: row.IssueDefinitionPath,
			Etag: row.IssueDefinitionEtag,
		},
		DefaultLabels:    append([]string(nil), row.DefaultLabels...),
		DefaultAssignees: append([]string(nil), row.DefaultAssignees...),
		ProviderExtension: &realqav1.ProviderExtension{
			Provider: &realqav1.ProviderExtension_Github{
				Github: &realqav1.GitHubProviderExtension{
					ProjectNodeIds: append([]string(nil), row.ProjectNodeIds...),
				},
			},
		},
		Revision:  rqerr.Revision(row.Revision),
		CreatedAt: timestamp(row.CreatedAt), UpdatedAt: timestamp(row.UpdatedAt),
	}
	if row.MilestoneNumber.Valid {
		result.ProviderExtension.GetGithub().MilestoneNumber = row.MilestoneNumber.Int64
	}
	result.ProcessUrlRules = make([]*realqav1.ProcessUrlRule, 0, len(ruleRows))
	for _, ruleRow := range ruleRows {
		ruleID, ruleErr := fromPGUUID(ruleRow.ID)
		if ruleErr != nil {
			return nil, ruleErr
		}
		result.ProcessUrlRules = append(result.ProcessUrlRules, &realqav1.ProcessUrlRule{
			RuleId:                 &realqav1.UuidV7{Value: ruleID.String()},
			ExactProcessName:       ruleRow.ExactProcessName,
			SafeWindowTitlePattern: ruleRow.SafeWindowTitlePattern,
			UrlTemplate:            ruleRow.UrlTemplate, Enabled: ruleRow.Enabled,
		})
	}
	if row.ShortcutID.Valid {
		shortcutID, shortcutErr := fromPGUUID(row.ShortcutID)
		if shortcutErr != nil {
			return nil, shortcutErr
		}
		result.Shortcut = &realqav1.ShortcutDefinition{
			ShortcutId:  &realqav1.UuidV7{Value: shortcutID.String()},
			Accelerator: row.Accelerator.String, Active: row.ShortcutActive.Bool,
		}
	}
	return result, nil
}

func captureModeName(value realqav1.CaptureMode) (string, error) {
	switch value {
	case realqav1.CaptureMode_CAPTURE_MODE_REGION:
		return "region", nil
	case realqav1.CaptureMode_CAPTURE_MODE_WINDOW:
		return "window", nil
	case realqav1.CaptureMode_CAPTURE_MODE_FULL_DISPLAY:
		return "full_display", nil
	case realqav1.CaptureMode_CAPTURE_MODE_MULTI_MONITOR:
		return "multi_monitor", nil
	case realqav1.CaptureMode_CAPTURE_MODE_CHROME_VISIBLE_VIEWPORT:
		return "chrome_visible_viewport", nil
	default:
		return "", invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
}

func captureModeValue(value string) (realqav1.CaptureMode, error) {
	switch value {
	case "region":
		return realqav1.CaptureMode_CAPTURE_MODE_REGION, nil
	case "window":
		return realqav1.CaptureMode_CAPTURE_MODE_WINDOW, nil
	case "full_display":
		return realqav1.CaptureMode_CAPTURE_MODE_FULL_DISPLAY, nil
	case "multi_monitor":
		return realqav1.CaptureMode_CAPTURE_MODE_MULTI_MONITOR, nil
	case "chrome_visible_viewport":
		return realqav1.CaptureMode_CAPTURE_MODE_CHROME_VISIBLE_VIEWPORT, nil
	default:
		return 0, errors.New("invalid stored capture mode")
	}
}

func selectorModeName(value realqav1.SelectorMode) (string, error) {
	switch value {
	case realqav1.SelectorMode_SELECTOR_MODE_NORMAL:
		return "normal", nil
	case realqav1.SelectorMode_SELECTOR_MODE_DOM:
		return "dom", nil
	default:
		return "", invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
}

func selectorModeValue(value string) (realqav1.SelectorMode, error) {
	switch value {
	case "normal":
		return realqav1.SelectorMode_SELECTOR_MODE_NORMAL, nil
	case "dom":
		return realqav1.SelectorMode_SELECTOR_MODE_DOM, nil
	default:
		return 0, errors.New("invalid stored selector mode")
	}
}

func definitionKindName(value realqav1.RepositoryIssueDefinitionKind) (string, error) {
	switch value {
	case realqav1.RepositoryIssueDefinitionKind_REPOSITORY_ISSUE_DEFINITION_KIND_MARKDOWN_TEMPLATE:
		return "markdown_template", nil
	case realqav1.RepositoryIssueDefinitionKind_REPOSITORY_ISSUE_DEFINITION_KIND_ISSUE_FORM:
		return "issue_form", nil
	default:
		return "", invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_SCHEMA_INVALID)
	}
}

func definitionKindValue(value string) (realqav1.RepositoryIssueDefinitionKind, error) {
	switch value {
	case "markdown_template":
		return realqav1.RepositoryIssueDefinitionKind_REPOSITORY_ISSUE_DEFINITION_KIND_MARKDOWN_TEMPLATE, nil
	case "issue_form":
		return realqav1.RepositoryIssueDefinitionKind_REPOSITORY_ISSUE_DEFINITION_KIND_ISSUE_FORM, nil
	default:
		return 0, errors.New("invalid stored definition kind")
	}
}
