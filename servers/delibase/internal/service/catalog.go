package service

import (
	"context"
	"encoding/base64"
	"errors"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultCatalogPageSize int32 = 25
	maxCatalogPageSize     int32 = 100
	detailCatalogLimit     int32 = 1000
)

// ListCatalogApps returns a stable UUID-keyset page of enabled public apps.
func (service *Catalog) ListCatalogApps(ctx context.Context, request *connect.Request[delibasev1.ListCatalogAppsRequest]) (*connect.Response[delibasev1.ListCatalogAppsResponse], error) {
	cursor, size, err := catalogPage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	queries, err := service.catalogQueries()
	if err != nil {
		return nil, err
	}
	rows, err := queries.ListPublicCatalogApps(ctx, dbgen.ListPublicCatalogAppsParams{ID: cursor, Limit: size + 1})
	if err != nil {
		return nil, catalogStorageError(err)
	}
	response := &delibasev1.ListCatalogAppsResponse{Page: &delibasev1.PageResponse{}}
	if int32(len(rows)) > size {
		rows = rows[:size]
		response.Page.NextCursor = encodeCatalogCursor(rows[len(rows)-1].ID)
	}
	for _, row := range rows {
		response.Apps = append(response.Apps, catalogApp(row))
	}
	return connect.NewResponse(response), nil
}

// GetCatalogApp returns an enabled app and its currently effective enabled meters.
func (service *Catalog) GetCatalogApp(ctx context.Context, request *connect.Request[delibasev1.GetCatalogAppRequest]) (*connect.Response[delibasev1.GetCatalogAppResponse], error) {
	if request.Msg.GetAppSlug() == "" {
		return nil, invalidCatalogRequest("app_slug is required")
	}
	queries, err := service.catalogQueries()
	if err != nil {
		return nil, err
	}
	app, err := queries.GetPublicCatalogAppBySlug(ctx, request.Msg.GetAppSlug())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, catalogNotFound()
	}
	if err != nil {
		return nil, catalogStorageError(err)
	}
	response := &delibasev1.GetCatalogAppResponse{App: catalogAppFromDetail(app)}
	for cursor := zeroCatalogID(); ; {
		meters, err := queries.ListPublicCatalogMeters(ctx, dbgen.ListPublicCatalogMetersParams{AppID: app.ID, ID: cursor, Limit: detailCatalogLimit})
		if err != nil {
			return nil, catalogStorageError(err)
		}
		for _, meter := range meters {
			response.Meters = append(response.Meters, catalogMeter(meter))
		}
		if int32(len(meters)) < detailCatalogLimit {
			break
		}
		cursor = meters[len(meters)-1].ID
	}
	return connect.NewResponse(response), nil
}

// ListCatalogMeters returns a stable UUID-keyset page of enabled meters for an enabled app.
func (service *Catalog) ListCatalogMeters(ctx context.Context, request *connect.Request[delibasev1.ListCatalogMetersRequest]) (*connect.Response[delibasev1.ListCatalogMetersResponse], error) {
	appID, err := catalogRequestID(request.Msg.GetAppId())
	if err != nil {
		return nil, err
	}
	cursor, size, err := catalogPage(request.Msg.GetPage())
	if err != nil {
		return nil, err
	}
	queries, err := service.catalogQueries()
	if err != nil {
		return nil, err
	}
	rows, err := queries.ListPublicCatalogMeters(ctx, dbgen.ListPublicCatalogMetersParams{AppID: appID, ID: cursor, Limit: size + 1})
	if err != nil {
		return nil, catalogStorageError(err)
	}
	response := &delibasev1.ListCatalogMetersResponse{Page: &delibasev1.PageResponse{}}
	if int32(len(rows)) > size {
		rows = rows[:size]
		response.Page.NextCursor = encodeCatalogCursor(rows[len(rows)-1].ID)
	}
	for _, row := range rows {
		response.Meters = append(response.Meters, catalogMeter(row))
	}
	return connect.NewResponse(response), nil
}

// GetCatalogMeter returns a currently effective enabled public meter.
func (service *Catalog) GetCatalogMeter(ctx context.Context, request *connect.Request[delibasev1.GetCatalogMeterRequest]) (*connect.Response[delibasev1.GetCatalogMeterResponse], error) {
	id, err := catalogRequestID(request.Msg.GetMeterId())
	if err != nil {
		return nil, err
	}
	queries, err := service.catalogQueries()
	if err != nil {
		return nil, err
	}
	row, err := queries.GetPublicCatalogMeter(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, catalogNotFound()
	}
	if err != nil {
		return nil, catalogStorageError(err)
	}
	return connect.NewResponse(&delibasev1.GetCatalogMeterResponse{Meter: catalogMeter(dbgen.ListPublicCatalogMetersRow(row))}), nil
}

func (service *Catalog) catalogQueries() (dbgen.Querier, error) {
	if service == nil || service.dependencies.Store == nil || service.dependencies.Store.Queries() == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("catalog is unavailable"))
	}
	return service.dependencies.Store.Queries(), nil
}

func catalogPage(page *delibasev1.PageRequest) (pgtype.UUID, int32, error) {
	size := defaultCatalogPageSize
	if page == nil {
		return zeroCatalogID(), size, nil
	}
	if page.GetPageSize() < 0 {
		return pgtype.UUID{}, 0, invalidCatalogRequest("page_size is invalid")
	}
	if page.GetPageSize() > 0 {
		size = page.GetPageSize()
		if size > maxCatalogPageSize {
			size = maxCatalogPageSize
		}
	}
	if page.GetCursor() == "" {
		return zeroCatalogID(), size, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(page.GetCursor())
	if err != nil {
		return pgtype.UUID{}, 0, invalidCatalogRequest("cursor is invalid")
	}
	id, err := uuid.Parse(string(raw))
	if err != nil || id == uuid.Nil || id.Version() != 7 || id.String() != string(raw) {
		return pgtype.UUID{}, 0, invalidCatalogRequest("cursor is invalid")
	}
	return pgtype.UUID{Bytes: [16]byte(id), Valid: true}, size, nil
}

func catalogRequestID(value *delibasev1.UuidV7) (pgtype.UUID, error) {
	if value == nil {
		return pgtype.UUID{}, invalidCatalogRequest("identifier is required")
	}
	id, err := uuid.Parse(value.GetValue())
	if err != nil || id == uuid.Nil || id.Version() != 7 || id.String() != value.GetValue() {
		return pgtype.UUID{}, invalidCatalogRequest("identifier is invalid")
	}
	return pgtype.UUID{Bytes: [16]byte(id), Valid: true}, nil
}

func zeroCatalogID() pgtype.UUID { return pgtype.UUID{Valid: true} }
func encodeCatalogCursor(id pgtype.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(uuid.UUID(id.Bytes).String()))
}
func invalidCatalogRequest(message string) error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New(message))
}
func catalogNotFound() error {
	failure := connect.NewError(connect.CodeNotFound, errors.New("catalog resource not found"))
	detail, err := connect.NewErrorDetail(&delibasev1.ErrorDetail{
		Reason: delibasev1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND,
	})
	if err == nil {
		failure.AddDetail(detail)
	}
	return failure
}
func catalogStorageError(error) error {
	return connect.NewError(connect.CodeUnavailable, errors.New("catalog is unavailable"))
}

func catalogApp(row dbgen.ListPublicCatalogAppsRow) *delibasev1.CatalogApp {
	return &delibasev1.CatalogApp{AppId: &delibasev1.UuidV7{Value: uuid.UUID(row.ID.Bytes).String()}, Slug: row.Slug, Name: row.Name, Summary: row.Summary, Description: row.Description, IconUrl: row.IconUrl, Enabled: row.Enabled}
}
func catalogAppFromDetail(row dbgen.GetPublicCatalogAppBySlugRow) *delibasev1.CatalogApp {
	return &delibasev1.CatalogApp{AppId: &delibasev1.UuidV7{Value: uuid.UUID(row.ID.Bytes).String()}, Slug: row.Slug, Name: row.Name, Summary: row.Summary, Description: row.Description, IconUrl: row.IconUrl, Enabled: row.Enabled}
}
func catalogMeter(row dbgen.ListPublicCatalogMetersRow) *delibasev1.CatalogMeter {
	price := &delibasev1.CatalogPrice{PriceVersionId: &delibasev1.UuidV7{Value: uuid.UUID(row.PriceVersionID.Bytes).String()}, UsdMicrosPerUnit: &delibasev1.UsdMicros{Value: row.UsdMicrosPerUnit}, EffectiveFrom: timestamppb.New(row.EffectiveFrom.Time)}
	if row.EffectiveUntil.Valid {
		price.EffectiveUntil = timestamppb.New(row.EffectiveUntil.Time)
	}
	return &delibasev1.CatalogMeter{MeterId: &delibasev1.UuidV7{Value: uuid.UUID(row.ID.Bytes).String()}, AppId: &delibasev1.UuidV7{Value: uuid.UUID(row.AppID.Bytes).String()}, Key: row.MeterKey, Name: row.Name, Description: row.Description, UnitName: row.UnitName, UnitPrecision: row.UnitPrecision, ReservationTtlSeconds: row.ReservationTtlSeconds, CurrentPrice: price, Enabled: row.Enabled}
}
