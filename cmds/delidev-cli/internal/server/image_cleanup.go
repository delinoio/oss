// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"time"
)

// Only an explicit retained deletion intent is replayed. Startup and reconnect
// do not expire drafts, reclaim accepted content, or create new image uploads.
func (s *Service) runImageDraftCleanups(parent context.Context) {
	ctx := domain.WithPrincipal(parent, domain.Principal{Type: domain.OwnerDevice})
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.reconcileImageDraftCleanups(ctx); err != nil && parent.Err() == nil {
			s.logger.WarnContext(parent, "image_cleanup_scan_failed", "code", domain.SafeError(err).Code)
		}
		select {
		case <-parent.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Service) reconcileImageDraftCleanups(ctx context.Context) error {
	var after domain.ID
	for {
		rows, err := s.Store.List(ctx, store.Filter{Kind: domain.JobKind, After: after, Limit: store.MaxPage})
		if err != nil {
			return err
		}
		for _, row := range rows {
			after = row.ID
			job, err := store.Decode[domain.Job](row)
			if err != nil {
				return err
			}
			if job.Type != domain.ImageAttachmentJob {
				continue
			}
			var value domain.ImageUpload
			if domain.Decode(job.Input, &value) != nil || value.Version != 1 || value.Attachment.ID != row.ID || value.Attachment.Validate() != nil {
				return domain.InvalidImageInput()
			}
			if value.State != domain.ImageDeleting || value.InputID != "" || len(value.Owners) != 0 {
				continue
			}
			s.imageTransfersMu.Lock()
			reader := s.imageTransferReaders[value.Attachment.MachineID]
			available := reader != nil && reader.pending == nil
			s.imageTransfersMu.Unlock()
			if !available {
				continue
			}
			attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = s.cleanupImageUpload(attempt, value)
			cancel()
			if err != nil && ctx.Err() == nil {
				s.logger.WarnContext(ctx, "image_cleanup_pending", "attachment_id", value.Attachment.ID, "machine_id", value.Attachment.MachineID, "code", domain.SafeError(err).Code)
			}
		}
		if len(rows) < store.MaxPage {
			return nil
		}
	}
}

// Count the original draft's retained staging authority inside Begin's atomic
// transaction. Deleted tombstones do not consume image selection slots.
func validateImageDraftBound(tx *store.Tx, candidate domain.ImageUpload) error {
	var after domain.ID
	count := 1
	total := candidate.Attachment.ByteLength
	for {
		rows, err := tx.List(store.Filter{Kind: domain.JobKind, After: after, Limit: store.MaxPage})
		if err != nil {
			return err
		}
		for _, row := range rows {
			after = row.ID
			job, err := store.Decode[domain.Job](row)
			if err != nil {
				return err
			}
			if job.Type != domain.ImageAttachmentJob {
				continue
			}
			var value domain.ImageUpload
			if domain.Decode(job.Input, &value) != nil {
				return domain.InvalidImageInput()
			}
			if value.Actor != candidate.Actor || value.DraftID != candidate.DraftID || value.State == domain.ImageDeleted || value.State == domain.ImageDeleting {
				continue
			}
			if value.OperationID != candidate.OperationID || value.Attachment.MachineID != candidate.Attachment.MachineID || value.SessionID != candidate.SessionID {
				return domain.InvalidImageInput()
			}
			count++
			total += value.Attachment.ByteLength
			if count > domain.MaxInputImages || total > domain.MaxInputImagesBytes {
				return domain.InvalidImageInput()
			}
		}
		if len(rows) < store.MaxPage {
			return nil
		}
	}
}
