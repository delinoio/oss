package upload

import (
	"context"
	"errors"
	"strings"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

// AdministratorHooks supplies metadata-only operations to AdminService. It
// intentionally exposes neither staging keys nor signed URLs.
type AdministratorHooks struct{ service *Service }

func NewAdministratorHooks(service *Service) *AdministratorHooks {
	return &AdministratorHooks{service: service}
}

func (h *AdministratorHooks) ListUploads(ctx context.Context, actorID string, filters domain.AdminUploadFilters, pageToken string, pageSize uint32) (domain.UploadList, string, error) {
	if pageSize == 0 {
		pageSize = domain.UploadDefaultPageSize
	}
	if pageSize > domain.UploadMaximumPageSize {
		return domain.UploadList{}, "", errors.New("page size exceeds maximum")
	}
	var cursor *domain.UploadCursor
	if pageToken != "" {
		decoded, err := h.service.cursors.DecodeAdministrator(pageToken, actorID, filters, h.service.clock.Now())
		if err != nil {
			return domain.UploadList{}, "", err
		}
		cursor = &decoded
	}
	result, err := h.service.repository.ListUploadsForAdministrator(ctx, filters, cursor, pageSize)
	if err != nil {
		return domain.UploadList{}, "", err
	}
	var next string
	if result.Next != nil {
		next, err = h.service.cursors.EncodeAdministrator(actorID, filters, *result.Next, h.service.clock.Now())
	}
	return result, next, err
}

func (h *AdministratorHooks) GetUsage(ctx context.Context, ownerID string) (domain.UploadUsage, error) {
	return h.service.repository.GetUploadUsage(ctx, ownerID, h.service.clock.Now())
}

func (h *AdministratorHooks) RemoveUpload(ctx context.Context, actorID, uploadID string, reason domain.RemovalReason, expected domain.UploadState, rationale string, event domain.AuditEvent) (domain.Upload, error) {
	rationale = strings.TrimSpace(rationale)
	if err := validateAdministratorReason(rationale); err != nil {
		return domain.Upload{}, err
	}
	return h.service.RemoveAsAdministrator(ctx, actorID, uploadID, reason, expected, rationale, event)
}

var _ domain.UploadAdministration = (*AdministratorHooks)(nil)
