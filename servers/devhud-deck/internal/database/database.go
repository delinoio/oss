// Package database owns Deck's PostgreSQL/sqlc transaction and encrypted
// persistence boundary.
package database

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/db/migrations"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database/dbgen"
	"github.com/delinoio/oss/servers/devhud-deck/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrNotFound            = errors.New("deck database: not found")
	ErrIdempotencyConflict = errors.New("deck database: idempotency conflict")
	ErrDeletionInProgress  = errors.New("deck database: deletion in progress")
	ErrAccountSwitch       = errors.New("deck database: device belongs to another account")
	ErrInstallationOwned   = errors.New("deck database: installation already has an owner")
)

type LimitError struct {
	Organization bool
}

func (e *LimitError) Error() string { return "deck database: view limit reached" }

type StaleError struct {
	ResourceID uuid.UUID
	Revision   uint64
}

func (e *StaleError) Error() string { return "deck database: stale revision" }

type Store struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
	cipher  *security.Cipher
	hasher  *security.Hasher
}

func Open(
	ctx context.Context,
	databaseURL string,
	cipher *security.Cipher,
	hasher *security.Hasher,
) (*Store, error) {
	if cipher == nil || hasher == nil {
		return nil, errors.New("deck database: security dependencies are required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("deck database: invalid connection configuration")
	}
	config.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("deck database: pool initialization failed")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("deck database: connectivity check failed")
	}
	if err := migrations.Run(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	store := &Store{
		pool: pool, queries: dbgen.New(pool), cipher: cipher, hasher: hasher,
	}
	if err := store.RewrapGitHubCredentials(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

func (store *Store) Ping(ctx context.Context) error {
	if store == nil || store.queries == nil {
		return errors.New("deck database: unavailable")
	}
	if _, err := store.queries.Ping(ctx); err != nil {
		return errors.New("deck database: readiness check failed")
	}
	return nil
}

func (store *Store) Queries() dbgen.Querier {
	if store == nil {
		return nil
	}
	return store.queries
}

func (store *Store) withinTransaction(
	ctx context.Context,
	callback func(*dbgen.Queries) error,
) error {
	if store == nil || store.pool == nil {
		return errors.New("deck database: unavailable")
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return errors.New("deck database: transaction start failed")
	}
	defer func() { _ = transaction.Rollback(context.WithoutCancel(ctx)) }()
	if err := callback(store.queries.WithTx(transaction)); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return errors.New("deck database: transaction commit failed")
	}
	return nil
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != uuid.Nil}
}

func uuidValue(value pgtype.UUID) uuid.UUID {
	if !value.Valid {
		return uuid.Nil
	}
	return uuid.UUID(value.Bytes)
}

func pgTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: !value.IsZero()}
}

func pgInt2(value int16, valid bool) pgtype.Int2 {
	return pgtype.Int2{Int16: value, Valid: valid}
}

func (store *Store) sealProto(label string, message proto.Message) ([]byte, error) {
	serialized, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return nil, errors.New("deck database: encode payload failed")
	}
	return store.cipher.Seal(label, serialized)
}

func (store *Store) openProto(label string, ciphertext []byte, message proto.Message) error {
	serialized, err := store.cipher.Open(label, ciphertext)
	if err != nil {
		return err
	}
	if err := proto.Unmarshal(serialized, message); err != nil {
		return errors.New("deck database: decode payload failed")
	}
	return nil
}

// UpsertIdentity and SyncMemberships are the narrow ingestion boundary for
// current DeliDev state. They are not Connect procedures.
func (store *Store) UpsertIdentity(
	ctx context.Context,
	accountID uuid.UUID,
	subject, githubLogin string,
) error {
	login, err := store.cipher.Seal("github-login", []byte(githubLogin))
	if err != nil {
		return err
	}
	return store.queries.UpsertDeckAccount(ctx, dbgen.UpsertDeckAccountParams{
		AccountID:             pgUUID(accountID),
		LogtoSubject:          subject,
		GithubLoginCiphertext: login,
	})
}

func (store *Store) SyncMemberships(
	ctx context.Context,
	accountID uuid.UUID,
	memberships []contracts.Membership,
	teamMemberships []contracts.TeamMembership,
) error {
	if accountID == uuid.Nil || accountID.Version() != 7 {
		return errors.New("deck database: invalid membership account")
	}
	organizations := make(map[uuid.UUID]struct{}, len(memberships))
	for _, membership := range memberships {
		if membership.OrganizationID == uuid.Nil ||
			membership.OrganizationID.Version() != 7 ||
			membership.Role < contracts.OrganizationRoleMember ||
			membership.Role > contracts.OrganizationRoleOwner {
			return errors.New("deck database: invalid organization membership")
		}
		organizations[membership.OrganizationID] = struct{}{}
	}
	for _, membership := range teamMemberships {
		_, organizationActive := organizations[membership.OrganizationID]
		if !organizationActive || membership.TeamID == uuid.Nil ||
			membership.TeamID.Version() != 7 {
			return errors.New("deck database: invalid team membership")
		}
	}
	return store.withinTransaction(ctx, func(queries *dbgen.Queries) error {
		account := pgUUID(accountID)
		if err := queries.DeactivateTeamMembershipsForAccount(ctx, account); err != nil {
			return err
		}
		if err := queries.DeactivateOrganizationMembershipsForAccount(ctx, account); err != nil {
			return err
		}
		for _, membership := range memberships {
			if err := queries.UpsertOrganizationMembership(ctx,
				dbgen.UpsertOrganizationMembershipParams{
					OrganizationID: pgUUID(membership.OrganizationID),
					AccountID:      account,
					Role:           int16(membership.Role),
				}); err != nil {
				return err
			}
		}
		for _, membership := range teamMemberships {
			if err := queries.UpsertTeamMembership(ctx, dbgen.UpsertTeamMembershipParams{
				OrganizationID: pgUUID(membership.OrganizationID),
				TeamID:         pgUUID(membership.TeamID),
				AccountID:      account,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (store *Store) ResolveViewer(
	ctx context.Context,
	subject string,
) (contracts.Viewer, error) {
	account, err := store.queries.GetDeckAccountBySubject(ctx, subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.Viewer{}, ErrNotFound
	}
	if err != nil {
		return contracts.Viewer{}, errors.New("deck database: identity lookup failed")
	}
	login, err := store.cipher.Open("github-login", account.GithubLoginCiphertext)
	if err != nil {
		return contracts.Viewer{}, err
	}
	viewer := contracts.Viewer{
		AccountID:       uuidValue(account.AccountID),
		Subject:         subject,
		GitHubLogin:     string(login),
		Memberships:     make(map[uuid.UUID]contracts.OrganizationRole),
		TeamMemberships: make(map[uuid.UUID]map[uuid.UUID]struct{}),
	}
	memberships, err := store.queries.ListOrganizationMembershipsForAccount(
		ctx, account.AccountID)
	if err != nil {
		return contracts.Viewer{}, errors.New("deck database: membership lookup failed")
	}
	for _, membership := range memberships {
		viewer.Memberships[uuidValue(membership.OrganizationID)] =
			contracts.OrganizationRole(membership.Role)
	}
	teams, err := store.queries.ListTeamMembershipsForAccount(ctx, account.AccountID)
	if err != nil {
		return contracts.Viewer{}, errors.New("deck database: team membership lookup failed")
	}
	for _, membership := range teams {
		organizationID := uuidValue(membership.OrganizationID)
		if viewer.TeamMemberships[organizationID] == nil {
			viewer.TeamMemberships[organizationID] = make(map[uuid.UUID]struct{})
		}
		viewer.TeamMemberships[organizationID][uuidValue(membership.TeamID)] = struct{}{}
	}
	return viewer, nil
}

type CreateViewParams struct {
	ID             uuid.UUID
	IdempotencyKey uuid.UUID
	SubjectHash    [32]byte
	RequestDigest  [32]byte
	OwnerHash      [32]byte
	View           *deckv1.View
	Now            time.Time
}

func ownerIDFromViewRow(row dbgen.DeckView) pgtype.UUID {
	if row.OwnerScope == int16(deckv1.OwnerScope_OWNER_SCOPE_PERSONAL) {
		return row.OwnerAccountID
	}
	return row.OwnerOrganizationID
}

func (store *Store) CreateView(
	ctx context.Context,
	params CreateViewParams,
) (*deckv1.View, bool, error) {
	row, err := store.encodeView(params.View)
	if err != nil {
		return nil, false, err
	}
	row.ViewID = pgUUID(params.ID)
	row.CreatedAt = pgTime(params.Now)
	row.UpdatedAt = pgTime(params.Now)
	var result *deckv1.View
	replayed := false
	err = store.withinTransaction(ctx, func(queries *dbgen.Queries) error {
		replay, replayErr := queries.GetCreateViewIdempotency(ctx,
			dbgen.GetCreateViewIdempotencyParams{
				SubjectHash:    params.SubjectHash[:],
				IdempotencyKey: pgUUID(params.IdempotencyKey),
			})
		if replayErr == nil {
			if !bytes.Equal(replay.RequestDigest, params.RequestDigest[:]) {
				return ErrIdempotencyConflict
			}
			result = &deckv1.View{}
			replayErr = store.openProto(
				"view-create-replay", replay.ResponseCiphertext, result)
			replayed = true
			return replayErr
		}
		if !errors.Is(replayErr, pgx.ErrNoRows) {
			return replayErr
		}
		if err := queries.EnsureOwnerLock(ctx, params.OwnerHash[:]); err != nil {
			return err
		}
		if _, err := queries.LockOwner(ctx, params.OwnerHash[:]); err != nil {
			return err
		}
		tombstoned, err := queries.IsOwnerTombstoned(ctx, params.OwnerHash[:])
		if err != nil {
			return err
		}
		if tombstoned {
			return ErrDeletionInProgress
		}
		connection, connectionErr := queries.GetGitHubConnectionByOwnerForUpdate(
			ctx, dbgen.GetGitHubConnectionByOwnerForUpdateParams{
				OwnerScope: row.OwnerScope,
				OwnerID:    ownerIDFromViewRow(row),
			})
		if connectionErr == nil {
			row.ConnectionState = connection.State
		} else if !errors.Is(connectionErr, pgx.ErrNoRows) {
			return connectionErr
		}
		switch params.View.Owner.GetScope() {
		case deckv1.OwnerScope_OWNER_SCOPE_PERSONAL:
			count, err := queries.CountPersonalViews(ctx, row.OwnerAccountID)
			if err != nil {
				return err
			}
			if count >= 50 {
				return &LimitError{}
			}
		case deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION:
			count, err := queries.CountOrganizationViews(ctx, row.OwnerOrganizationID)
			if err != nil {
				return err
			}
			if count >= 250 {
				return &LimitError{Organization: true}
			}
		default:
			return errors.New("deck database: invalid owner")
		}
		stored, err := queries.InsertView(ctx, dbgen.InsertViewParams{
			ViewID:                 row.ViewID,
			OwnerScope:             row.OwnerScope,
			OwnerAccountID:         row.OwnerAccountID,
			OwnerOrganizationID:    row.OwnerOrganizationID,
			BillingOrganizationID:  row.BillingOrganizationID,
			BillingTeamID:          row.BillingTeamID,
			NameCiphertext:         row.NameCiphertext,
			QueryCiphertext:        row.QueryCiphertext,
			Kind:                   row.Kind,
			Sort:                   row.Sort,
			Grouping:               row.Grouping,
			NotificationCiphertext: row.NotificationCiphertext,
			ConnectionState:        row.ConnectionState,
			CreatedAt:              row.CreatedAt,
			UpdatedAt:              row.UpdatedAt,
		})
		if err != nil {
			return err
		}
		result, err = store.decodeView(stored)
		if err != nil {
			return err
		}
		responseCiphertext, err := store.sealProto("view-create-replay", result)
		if err != nil {
			return err
		}
		return queries.InsertCreateViewIdempotency(ctx,
			dbgen.InsertCreateViewIdempotencyParams{
				SubjectHash:        params.SubjectHash[:],
				IdempotencyKey:     pgUUID(params.IdempotencyKey),
				RequestDigest:      params.RequestDigest[:],
				OwnerHash:          params.OwnerHash[:],
				ViewID:             row.ViewID,
				ResponseCiphertext: responseCiphertext,
			})
	})
	if err != nil {
		return nil, false, err
	}
	return result, replayed, nil
}

func (store *Store) GetView(ctx context.Context, id uuid.UUID) (*deckv1.View, error) {
	row, err := store.queries.GetView(ctx, pgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.New("deck database: view lookup failed")
	}
	return store.decodeView(row)
}

func (store *Store) ListViews(
	ctx context.Context,
	ownerScope deckv1.OwnerScope,
	ownerID, after uuid.UUID,
	limit int32,
) ([]*deckv1.View, error) {
	var rows []dbgen.DeckView
	var err error
	switch ownerScope {
	case deckv1.OwnerScope_OWNER_SCOPE_PERSONAL:
		rows, err = store.queries.ListPersonalViews(ctx, dbgen.ListPersonalViewsParams{
			OwnerAccountID: pgUUID(ownerID), AfterViewID: pgUUIDAllowNil(after),
			PageLimit: limit,
		})
	case deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION:
		rows, err = store.queries.ListOrganizationViews(ctx,
			dbgen.ListOrganizationViewsParams{
				OwnerOrganizationID: pgUUID(ownerID), AfterViewID: pgUUIDAllowNil(after),
				PageLimit: limit,
			})
	default:
		return nil, errors.New("deck database: invalid owner")
	}
	if err != nil {
		return nil, errors.New("deck database: list views failed")
	}
	views := make([]*deckv1.View, 0, len(rows))
	for _, row := range rows {
		view, err := store.decodeView(row)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func pgUUIDAllowNil(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func (store *Store) UpdateView(
	ctx context.Context,
	id uuid.UUID,
	expected uint64,
	view *deckv1.View,
	queryChanged bool,
	now time.Time,
) (*deckv1.View, error) {
	row, err := store.encodeView(view)
	if err != nil {
		return nil, err
	}
	var updated dbgen.DeckView
	err = store.withinTransaction(ctx, func(queries *dbgen.Queries) error {
		updated, err = queries.UpdateView(ctx, dbgen.UpdateViewParams{
			BillingOrganizationID:  row.BillingOrganizationID,
			BillingTeamID:          row.BillingTeamID,
			NameCiphertext:         row.NameCiphertext,
			QueryCiphertext:        row.QueryCiphertext,
			Sort:                   row.Sort,
			Grouping:               row.Grouping,
			NotificationCiphertext: row.NotificationCiphertext,
			UpdatedAt:              pgTime(now),
			ViewID:                 pgUUID(id),
			ExpectedRevision:       int64(expected),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			current, currentErr := queries.GetView(ctx, pgUUID(id))
			if errors.Is(currentErr, pgx.ErrNoRows) {
				return ErrNotFound
			}
			if currentErr != nil {
				return errors.New("deck database: stale lookup failed")
			}
			return &StaleError{ResourceID: id, Revision: uint64(current.Revision)}
		}
		if err != nil {
			return errors.New("deck database: update view failed")
		}
		if !queryChanged {
			return nil
		}
		if err := queries.DeleteAllViewSnapshots(ctx, pgUUID(id)); err != nil {
			return err
		}
		if err := queries.DeleteAllViewSnapshotStates(ctx, pgUUID(id)); err != nil {
			return err
		}
		return store.resetDeviceWidgetSnapshots(ctx, queries, id, now)
	})
	if err != nil {
		return nil, err
	}
	return store.decodeView(updated)
}

func (store *Store) DeleteView(
	ctx context.Context,
	id uuid.UUID,
	expected uint64,
	now time.Time,
) (uint64, error) {
	var revision int64
	err := store.withinTransaction(ctx, func(queries *dbgen.Queries) error {
		var err error
		revision, err = queries.DeleteView(ctx, dbgen.DeleteViewParams{
			ViewID: pgUUID(id), ExpectedRevision: int64(expected),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			current, currentErr := queries.GetView(ctx, pgUUID(id))
			if errors.Is(currentErr, pgx.ErrNoRows) {
				return ErrNotFound
			}
			if currentErr != nil {
				return errors.New("deck database: stale lookup failed")
			}
			return &StaleError{ResourceID: id, Revision: uint64(current.Revision)}
		}
		if err != nil {
			return errors.New("deck database: delete view failed")
		}
		return store.scrubDeviceViewState(
			ctx, queries, []pgtype.UUID{pgUUID(id)}, now)
	})
	if err != nil {
		return 0, err
	}
	return uint64(revision), nil
}

func (store *Store) encodeView(view *deckv1.View) (dbgen.DeckView, error) {
	if view == nil || view.Owner == nil || view.Query == nil {
		return dbgen.DeckView{}, errors.New("deck database: incomplete view")
	}
	name, err := store.cipher.Seal("view-name", []byte(view.Name))
	if err != nil {
		return dbgen.DeckView{}, err
	}
	queryCiphertext, err := store.sealProto("view-query", view.Query)
	if err != nil {
		return dbgen.DeckView{}, err
	}
	notification := view.NotificationPreference
	if notification == nil {
		notification = &deckv1.ViewNotificationPreference{}
	}
	notificationCiphertext, err := store.sealProto("view-notification", notification)
	if err != nil {
		return dbgen.DeckView{}, err
	}
	row := dbgen.DeckView{
		OwnerScope:             int16(view.Owner.Scope),
		NameCiphertext:         name,
		QueryCiphertext:        queryCiphertext,
		Kind:                   int16(view.Kind),
		Sort:                   int16(view.Sort),
		Grouping:               int16(view.Grouping),
		NotificationCiphertext: notificationCiphertext,
		ConnectionState:        int16(view.ConnectionState),
	}
	if account := view.Owner.GetAccountId(); account != nil {
		id, err := uuid.Parse(account.Value)
		if err != nil {
			return dbgen.DeckView{}, errors.New("deck database: invalid owner account")
		}
		row.OwnerAccountID = pgUUID(id)
	}
	if organization := view.Owner.GetOrganizationId(); organization != nil {
		id, err := uuid.Parse(organization.Value)
		if err != nil {
			return dbgen.DeckView{}, errors.New("deck database: invalid owner organization")
		}
		row.OwnerOrganizationID = pgUUID(id)
	}
	if view.Billing != nil {
		if value := view.Billing.GetOrganizationId().GetValue(); value != "" {
			id, err := uuid.Parse(value)
			if err != nil {
				return dbgen.DeckView{}, errors.New("deck database: invalid billing organization")
			}
			row.BillingOrganizationID = pgUUID(id)
		}
		if value := view.Billing.GetTeamId().GetValue(); value != "" {
			id, err := uuid.Parse(value)
			if err != nil {
				return dbgen.DeckView{}, errors.New("deck database: invalid billing team")
			}
			row.BillingTeamID = pgUUID(id)
		}
	}
	return row, nil
}

func (store *Store) decodeView(row dbgen.DeckView) (*deckv1.View, error) {
	name, err := store.cipher.Open("view-name", row.NameCiphertext)
	if err != nil {
		return nil, err
	}
	query := &deckv1.ViewQuery{}
	if err := store.openProto("view-query", row.QueryCiphertext, query); err != nil {
		return nil, err
	}
	notification := &deckv1.ViewNotificationPreference{}
	if err := store.openProto("view-notification", row.NotificationCiphertext, notification); err != nil {
		return nil, err
	}
	id := uuidValue(row.ViewID)
	view := &deckv1.View{
		ViewId:                 uuidProto(id),
		Name:                   string(name),
		Kind:                   deckv1.ViewKind(row.Kind),
		Query:                  query,
		Sort:                   deckv1.ViewSort(row.Sort),
		Grouping:               deckv1.ViewGrouping(row.Grouping),
		NotificationPreference: notification,
		ConnectionState:        deckv1.ConnectionState(row.ConnectionState),
		Revision:               revisionProto(store.hasher, id, uint64(row.Revision)),
		CreatedAt:              timestampProto(row.CreatedAt),
		UpdatedAt:              timestampProto(row.UpdatedAt),
	}
	if row.OwnerScope == int16(deckv1.OwnerScope_OWNER_SCOPE_PERSONAL) {
		view.Owner = &deckv1.Owner{
			Scope: deckv1.OwnerScope_OWNER_SCOPE_PERSONAL,
			OwnerId: &deckv1.Owner_AccountId{AccountId: uuidProto(
				uuidValue(row.OwnerAccountID))},
		}
	} else {
		view.Owner = &deckv1.Owner{
			Scope: deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION,
			OwnerId: &deckv1.Owner_OrganizationId{OrganizationId: uuidProto(
				uuidValue(row.OwnerOrganizationID))},
		}
	}
	if row.BillingOrganizationID.Valid || row.BillingTeamID.Valid {
		view.Billing = &deckv1.BillingSelection{}
		if row.BillingOrganizationID.Valid {
			view.Billing.OrganizationId = uuidProto(uuidValue(row.BillingOrganizationID))
		}
		if row.BillingTeamID.Valid {
			view.Billing.TeamId = uuidProto(uuidValue(row.BillingTeamID))
		}
	}
	return view, nil
}

func uuidProto(id uuid.UUID) *deckv1.UuidV7 {
	if id == uuid.Nil {
		return nil
	}
	return &deckv1.UuidV7{Value: id.String()}
}

func revisionProto(hasher *security.Hasher, id uuid.UUID, revision uint64) *deckv1.Revision {
	return &deckv1.Revision{Value: revision, Etag: hasher.ETag(id, revision)}
}

func timestampProto(value pgtype.Timestamptz) *timestamppb.Timestamp {
	if !value.Valid {
		return nil
	}
	return timestamppb.New(value.Time.UTC())
}

func (store *Store) ReplaceSnapshots(
	ctx context.Context,
	viewID uuid.UUID,
	viewerHash [32]byte,
	snapshots []*deckv1.PullRequestResult,
	refreshedAt time.Time,
) (bool, error) {
	truncated := len(snapshots) > 500
	if truncated {
		snapshots = snapshots[:500]
	}
	return truncated, store.withinTransaction(ctx, func(queries *dbgen.Queries) error {
		if err := queries.DeleteViewSnapshots(ctx, dbgen.DeleteViewSnapshotsParams{
			ViewID: pgUUID(viewID), ViewerHash: viewerHash[:],
		}); err != nil {
			return err
		}
		if err := queries.DeleteViewSnapshotState(ctx, dbgen.DeleteViewSnapshotStateParams{
			ViewID: pgUUID(viewID), ViewerHash: viewerHash[:],
		}); err != nil {
			return err
		}
		for index, snapshot := range snapshots {
			repository := snapshot.GetRepository()
			if repository == nil || repository.GetOwner() == "" ||
				repository.GetName() == "" || snapshot.GetNumber() == 0 ||
				snapshot.GetNumber() > math.MaxInt64 {
				return errors.New("deck database: snapshot repository is required")
			}
			repositoryHash := store.SnapshotRepositoryHash(repository)
			repositoryCiphertext, err := store.sealProto(
				"pr-snapshot-repository", repository)
			if err != nil {
				return err
			}
			ciphertext, err := store.sealProto("pr-snapshot", snapshot)
			if err != nil {
				return err
			}
			if err := queries.InsertViewSnapshot(ctx, dbgen.InsertViewSnapshotParams{
				ViewID: pgUUID(viewID), ViewerHash: viewerHash[:],
				Ordinal: int32(index), RepositoryHash: repositoryHash[:],
				PullRequestNumber:    int64(snapshot.GetNumber()),
				RepositoryCiphertext: repositoryCiphertext,
				SnapshotCiphertext:   ciphertext,
			}); err != nil {
				return err
			}
		}
		return queries.UpdateViewSnapshotState(ctx, dbgen.UpdateViewSnapshotStateParams{
			SnapshotTruncated:   truncated,
			SnapshotRefreshedAt: pgTime(refreshedAt),
			ViewID:              pgUUID(viewID),
			ViewerHash:          viewerHash[:],
		})
	})
}

func (store *Store) ListSnapshots(
	ctx context.Context,
	viewID uuid.UUID,
	viewerHash [32]byte,
	readableRepositories map[[32]byte]struct{},
) ([]*deckv1.PullRequestResult, bool, time.Time, error) {
	_, err := store.queries.GetView(ctx, pgUUID(viewID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, time.Time{}, ErrNotFound
	}
	if err != nil {
		return nil, false, time.Time{}, errors.New("deck database: snapshot state failed")
	}
	state, stateErr := store.queries.GetViewSnapshotState(ctx,
		dbgen.GetViewSnapshotStateParams{
			ViewID: pgUUID(viewID), ViewerHash: viewerHash[:],
		})
	if stateErr != nil && !errors.Is(stateErr, pgx.ErrNoRows) {
		return nil, false, time.Time{}, errors.New("deck database: snapshot state failed")
	}
	rows, err := store.queries.ListViewSnapshots(ctx, dbgen.ListViewSnapshotsParams{
		ViewID: pgUUID(viewID), ViewerHash: viewerHash[:],
		AfterOrdinal: 0, PageLimit: 500,
	})
	if err != nil {
		return nil, false, time.Time{}, errors.New("deck database: list snapshots failed")
	}
	results := make([]*deckv1.PullRequestResult, 0, len(rows))
	for _, row := range rows {
		if len(row.RepositoryHash) != 32 {
			return nil, false, time.Time{},
				errors.New("deck database: invalid snapshot repository index")
		}
		var repositoryHash [32]byte
		copy(repositoryHash[:], row.RepositoryHash)
		if _, readable := readableRepositories[repositoryHash]; !readable {
			continue
		}
		repository := &deckv1.RepositoryReference{}
		if err := store.openProto(
			"pr-snapshot-repository", row.RepositoryCiphertext, repository); err != nil {
			return nil, false, time.Time{}, err
		}
		result := &deckv1.PullRequestResult{}
		if err := store.openProto("pr-snapshot", row.SnapshotCiphertext, result); err != nil {
			return nil, false, time.Time{}, err
		}
		results = append(results, result)
	}
	var refreshedAt time.Time
	if stateErr == nil && state.RefreshedAt.Valid {
		refreshedAt = state.RefreshedAt.Time.UTC()
	}
	return results, stateErr == nil && state.Truncated, refreshedAt, nil
}

func (store *Store) HasSnapshot(
	ctx context.Context,
	viewID uuid.UUID,
	viewerHash [32]byte,
	reference *deckv1.PullRequestReference,
) (bool, error) {
	_, err := store.GetSnapshot(ctx, viewID, viewerHash, reference)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (store *Store) GetSnapshot(
	ctx context.Context,
	viewID uuid.UUID,
	viewerHash [32]byte,
	reference *deckv1.PullRequestReference,
) (*deckv1.PullRequestResult, error) {
	if reference == nil || reference.Repository == nil ||
		reference.Number == 0 || reference.Number > math.MaxInt64 {
		return nil, ErrNotFound
	}
	repositoryHash := store.SnapshotRepositoryHash(reference.Repository)
	row, err := store.queries.GetViewSnapshotByReference(
		ctx, dbgen.GetViewSnapshotByReferenceParams{
			ViewID: pgUUID(viewID), ViewerHash: viewerHash[:],
			RepositoryHash:    repositoryHash[:],
			PullRequestNumber: int64(reference.Number),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.New("deck database: get snapshot failed")
	}
	repository := &deckv1.RepositoryReference{}
	if err := store.openProto(
		"pr-snapshot-repository", row.RepositoryCiphertext, repository); err != nil {
		return nil, err
	}
	if !strings.EqualFold(repository.Owner, reference.Repository.Owner) ||
		!strings.EqualFold(repository.Name, reference.Repository.Name) {
		return nil, ErrNotFound
	}
	result := &deckv1.PullRequestResult{}
	if err := store.openProto(
		"pr-snapshot", row.SnapshotCiphertext, result); err != nil {
		return nil, err
	}
	if result.Number != reference.Number {
		return nil, ErrNotFound
	}
	return result, nil
}

func (store *Store) SnapshotRepositoryHash(
	repository *deckv1.RepositoryReference,
) [32]byte {
	return store.hasher.Sum(
		"pr-snapshot-repository",
		strings.ToLower(repository.GetOwner())+"\x00"+
			strings.ToLower(repository.GetName()),
	)
}

func (store *Store) String() string {
	if store == nil {
		return "deck database unavailable"
	}
	return fmt.Sprintf("deck database pool=%t", store.pool != nil)
}
