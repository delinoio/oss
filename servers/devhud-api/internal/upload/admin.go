package upload

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

// AdministratorHooks supplies metadata-only operations to the future
// AdminService. It intentionally exposes neither staging keys nor signed URLs.
type AdministratorHooks struct{ service *Service }

func NewAdministratorHooks(service *Service) *AdministratorHooks {
	return &AdministratorHooks{service: service}
}

func (h *AdministratorHooks) ListUploads(ctx context.Context, ownerID string, states []domain.UploadState, pageToken string, pageSize uint32) (domain.UploadList, string, error) {
	if pageSize == 0 {
		pageSize = domain.UploadDefaultPageSize
	}
	if pageSize > domain.UploadMaximumPageSize {
		return domain.UploadList{}, "", errors.New("page size exceeds maximum")
	}
	var cursor *domain.UploadCursor
	if pageToken != "" {
		decoded, err := h.service.cursors.Decode(pageToken, ownerID, states, "", h.service.clock.Now())
		if err != nil {
			return domain.UploadList{}, "", err
		}
		cursor = &decoded
	}
	result, err := h.service.repository.ListUploadsForAdministrator(ctx, ownerID, states, cursor, pageSize)
	if err != nil {
		return domain.UploadList{}, "", err
	}
	var next string
	if result.Next != nil {
		next, err = h.service.cursors.Encode(ownerID, states, "", *result.Next, h.service.clock.Now())
	}
	return result, next, err
}

func (h *AdministratorHooks) GetUsage(ctx context.Context, ownerID string) (domain.UploadUsage, error) {
	return h.service.repository.GetUploadUsage(ctx, ownerID, h.service.clock.Now())
}

func (h *AdministratorHooks) RemoveUpload(ctx context.Context, actorID, uploadID string, reason domain.RemovalReason, rationale string) (domain.Upload, error) {
	rationale = strings.TrimSpace(rationale)
	if rationale == "" || len(rationale) > 4096 || !utf8.ValidString(rationale) {
		return domain.Upload{}, errors.New("administrator reason must contain 1 to 4096 bytes")
	}
	upload, err := h.service.RemoveAsAdministrator(ctx, uploadID, reason)
	if err != nil {
		return domain.Upload{}, err
	}
	if err := h.service.repository.RecordAdministratorUploadAudit(ctx, actorID, uploadID, reason, rationale, h.service.clock.Now()); err != nil {
		return domain.Upload{}, err
	}
	return upload, nil
}

var _ domain.UploadAdministration = (*AdministratorHooks)(nil)
