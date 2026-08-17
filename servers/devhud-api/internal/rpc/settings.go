package rpc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"unicode/utf8"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"github.com/delinoio/oss/protos/gen/go/devhud/v1/devhudv1connect"
	"github.com/delinoio/oss/servers/devhud-api/internal/auth"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"github.com/gowebpki/jcs"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type SettingsService struct {
	repository domain.Repository
	clock      domain.Clock
	logger     *slog.Logger
}

func NewSettingsService(repository domain.Repository, clock domain.Clock, logger *slog.Logger) *SettingsService {
	return &SettingsService{repository: repository, clock: clock, logger: logger}
}

func (s *SettingsService) GetSettings(ctx context.Context, _ *connect.Request[devhudv1.GetSettingsRequest]) (*connect.Response[devhudv1.GetSettingsResponse], error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, unauthenticatedError(ctx)
	}
	snapshot, err := s.repository.GetSettings(ctx, user.ID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, deletionCompletePermissionError(ctx)
		}
		var permission *domain.PermissionError
		if errors.As(err, &permission) {
			return nil, permissionError(ctx, permission)
		}
		s.logger.ErrorContext(ctx, "settings repository operation failed",
			"correlation_id", CorrelationID(ctx),
			"procedure", devhudv1connect.SettingsServiceGetSettingsProcedure,
			"error", err,
		)
		return nil, internalError(ctx)
	}
	return connect.NewResponse(&devhudv1.GetSettingsResponse{
		Metadata: metadata(CorrelationID(ctx)),
		Snapshot: settingsMessage(snapshot),
	}), nil
}

func (s *SettingsService) ReplaceSettings(ctx context.Context, request *connect.Request[devhudv1.ReplaceSettingsRequest]) (*connect.Response[devhudv1.ReplaceSettingsResponse], error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, unauthenticatedError(ctx)
	}
	if request.Msg.SchemaVersion == 0 {
		return nil, NewError(connect.CodeInvalidArgument, "schema_version must be nonzero", CorrelationID(ctx))
	}
	if err := validateCanonicalJSON(request.Msg.CanonicalJson); err != nil {
		return nil, NewError(connect.CodeInvalidArgument, err.Error(), CorrelationID(ctx))
	}
	if err := validateDevHudSettings(request.Msg.CanonicalJson, request.Msg.SchemaVersion); err != nil {
		return nil, NewError(connect.CodeInvalidArgument, err.Error(), CorrelationID(ctx))
	}
	snapshot, err := s.repository.ReplaceSettings(ctx, user.ID, request.Msg.SchemaVersion, request.Msg.CanonicalJson, request.Msg.ExpectedRevision, s.clock.Now())
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, deletionCompletePermissionError(ctx)
		}
		var conflict *domain.RevisionConflict
		if errors.As(err, &conflict) {
			return nil, NewError(connect.CodeAborted, "settings revision conflict", CorrelationID(ctx), &devhudv1.SettingsRevisionConflict{
				ExpectedRevision: conflict.Expected,
				CurrentSnapshot:  settingsMessage(conflict.Current),
			})
		}
		var permission *domain.PermissionError
		if errors.As(err, &permission) {
			return nil, permissionError(ctx, permission)
		}
		s.logger.ErrorContext(ctx, "settings repository operation failed",
			"correlation_id", CorrelationID(ctx),
			"procedure", devhudv1connect.SettingsServiceReplaceSettingsProcedure,
			"error", err,
		)
		return nil, internalError(ctx)
	}
	return connect.NewResponse(&devhudv1.ReplaceSettingsResponse{
		Metadata: metadata(CorrelationID(ctx)),
		Snapshot: settingsMessage(&snapshot),
	}), nil
}

func validateCanonicalJSON(value []byte) error {
	if len(value) > domain.SettingsMaximumBytes {
		return errors.New("canonical_json exceeds 1 MiB")
	}
	if !utf8.Valid(value) {
		return errors.New("canonical_json must be valid UTF-8")
	}
	if len(value) >= 3 && bytes.Equal(value[:3], []byte{0xef, 0xbb, 0xbf}) {
		return errors.New("canonical_json must not contain a UTF-8 BOM")
	}
	canonical, err := jcs.Transform(value)
	if err != nil {
		return errors.New("canonical_json must be valid RFC 8785 JSON")
	}
	if !bytes.Equal(canonical, value) {
		return errors.New("canonical_json must use RFC 8785 canonical encoding")
	}
	return nil
}

func settingsMessage(settings *domain.Settings) *devhudv1.SettingsSnapshot {
	if settings == nil {
		return nil
	}
	return &devhudv1.SettingsSnapshot{
		SchemaVersion: settings.SchemaVersion,
		Revision:      settings.Revision,
		CanonicalJson: append([]byte(nil), settings.CanonicalJSON...),
		UpdatedAt:     timestamppb.New(settings.UpdatedAt),
	}
}
