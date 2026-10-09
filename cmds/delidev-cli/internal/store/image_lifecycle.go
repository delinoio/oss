// SPDX-License-Identifier: Apache-2.0
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Image records are bounded metadata. Their empty entity session scope keeps
// independent Fork ownership outside the source session's cascading purge.
func (t *Tx) ImageUploadRecord(id domain.ID) (Record, domain.ImageUpload, error) {
	r, err := t.Get(domain.JobKind, id)
	if err != nil {
		return r, domain.ImageUpload{}, err
	}
	j, err := Decode[domain.Job](r)
	var v domain.ImageUpload
	if err != nil || j.Type != domain.ImageAttachmentJob || j.State != domain.JobSucceeded || r.SessionID != "" || domain.Decode(j.Input, &v) != nil || validateImageUpload(v) != nil || v.Attachment.ID != id || j.MachineID != v.Attachment.MachineID {
		return r, v, domain.InvalidImageInput()
	}
	return r, v, nil
}

func validateImageUpload(v domain.ImageUpload) error {
	if v.GeneratedExecutionID != "" && (v.GeneratedExecutionID.Validate() != nil || v.State == domain.ImageUploading || v.State == domain.ImageReady || v.InputID == "" || v.SessionID == "") {
		return domain.InvalidImageInput()
	}

	if v.Version != 1 || v.Attachment.Validate() != nil || v.WorkerDeviceID.Validate() != nil || v.DraftID.Validate() != nil || v.OperationID.Validate() != nil || v.MachineRevision == 0 || v.UploadedBytes > v.Attachment.ByteLength || len(v.Owners) > 4096 || (v.Actor.Type != domain.OwnerDevice && v.Actor.Type != domain.ClientDevice) || v.Actor.MachineID != "" || v.Actor.DeviceID != "" && v.Actor.DeviceID.Validate() != nil || v.Actor.Type == domain.ClientDevice && v.Actor.DeviceID.Validate() != nil || v.SessionID != "" && v.SessionID.Validate() != nil || v.InputID != "" && v.InputID.Validate() != nil {
		return domain.InvalidImageInput()
	}
	seen := map[domain.ID]bool{}
	for _, owner := range v.Owners {
		if owner.Validate() != nil || seen[owner] {
			return domain.InvalidImageInput()
		}
		seen[owner] = true
	}
	switch v.State {
	case domain.ImageUploading:
		if v.InputID != "" || len(v.Owners) != 0 {
			return domain.InvalidImageInput()
		}
	case domain.ImageReady:
		if v.InputID != "" || len(v.Owners) != 0 || v.UploadedBytes != v.Attachment.ByteLength {
			return domain.InvalidImageInput()
		}
	case domain.ImageClaimed:
		if v.InputID == "" || v.SessionID == "" || len(v.Owners) == 0 || v.UploadedBytes != v.Attachment.ByteLength {
			return domain.InvalidImageInput()
		}
	case domain.ImageDeleting, domain.ImageDeleted:
		if len(v.Owners) != 0 {
			return domain.InvalidImageInput()
		}
	default:
		return domain.InvalidImageInput()
	}
	return nil
}

func (t *Tx) PutImageUpload(row Record, v domain.ImageUpload) (Record, error) {
	if err := t.ValidateImageCapacity(v); err != nil {
		return row, err
	}
	if err := validateImageUpload(v); err != nil {
		return row, err
	}
	j, err := Decode[domain.Job](row)
	if err != nil || j.Type != domain.ImageAttachmentJob || row.ID != v.Attachment.ID || row.SessionID != "" {
		return row, domain.InvalidImageInput()
	}
	j.Input, err = json.Marshal(v)
	if err != nil {
		return row, storageError(err)
	}
	return t.PutJob(row.ID, row.Revision, "", "", j)
}

// Keep admission and cleanup on the same distinct-reference obligation set.
// Removed inputs retain owners; uncertain deletion retains the original scope.
// Only confirmed terminal removal or release to an independent owner frees it.
const sessionImageObligation = `(EXISTS(SELECT 1 FROM json_each(entities.body,'$.input.owners') WHERE value=?) OR (json_extract(body,'$.input.session_id')=? AND json_extract(body,'$.input.state')!='deleted' AND (coalesce(json_extract(body,'$.input.input_id'),'')='' OR json_extract(body,'$.input.state')='deleting')))`

func imageUploadScopes(v domain.ImageUpload) []domain.ID {
	scopes := slices.Clone(v.Owners)
	if v.SessionID != "" && v.State != domain.ImageDeleted && (v.InputID == "" || v.State == domain.ImageDeleting) && !slices.Contains(scopes, v.SessionID) {
		scopes = append(scopes, v.SessionID)
	}
	return scopes
}

// ValidateImageCapacity runs in the same write transaction as upload admission,
// input claims and Fork ownership. Check before any new metadata or Worker bytes.
func (t *Tx) ValidateImageCapacity(v domain.ImageUpload) error {
	if err := validateImageUpload(v); err != nil {
		return err
	}
	// Unchanged original obligations already own their slot. Do not rescan all
	// independent owners on chunk updates or release, and never block cleanup.
	var previous []domain.ID
	_, original, err := t.ImageUploadRecord(v.Attachment.ID)
	if err == nil {
		previous = imageUploadScopes(original)
	} else if domain.SafeError(err).Code != domain.NotFound {
		return err
	}
	for _, session := range imageUploadScopes(v) {
		if slices.Contains(previous, session) {
			continue
		}
		var count int
		if err := t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM entities WHERE kind='job' AND json_extract(body,'$.type')=? AND id!=? AND "+sessionImageObligation, domain.ImageAttachmentJob, v.Attachment.ID, session, session).Scan(&count); err != nil {
			return storageError(err)
		}
		if count >= domain.MaxSessionImageAttachments {
			return domain.Fail(domain.ResourceExhausted, "This session has reached its retained image limit.", "Remove unclaimed image drafts and confirm their cleanup, or start an independent session. Retained or uncertain images cannot be discarded.")
		}
	}
	return nil
}

func (t *Tx) sessionImageUploads(session domain.ID) ([]Record, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+recordColumns+" FROM entities WHERE kind='job' AND json_extract(body,'$.type')=? AND "+sessionImageObligation+" ORDER BY id LIMIT 4097", domain.ImageAttachmentJob, session, session)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	var result []Record
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, storageError(err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, storageError(err)
	}
	if len(result) > domain.MaxSessionImageAttachments {
		return nil, domain.SessionDeletionPending()
	}
	return result, nil
}

// Freeze only the accepted transcript prefix. Queued later inputs and staging
// acquire no child owner. Already inherited images precede this child's inputs.
func (t *Tx) forkImageUploads(input domain.ForkJobInput) ([]domain.ImageUpload, error) {
	if domain.UniqueIDs([]domain.ID{input.SourceSessionID, input.ChildSessionID, input.Completion.ExecutionID}) != nil {
		return nil, domain.InvalidImageInput()
	}
	rows, err := t.sessionImageUploads(input.SourceSessionID)
	if err != nil {
		return nil, err
	}
	var candidates []domain.ImageUpload
	for _, row := range rows {
		_, v, err := t.ImageUploadRecord(row.ID)
		if err != nil {
			return nil, err
		}
		if slices.Contains(v.Owners, input.SourceSessionID) {
			if v.Quarantined || v.State != domain.ImageClaimed {
				return nil, domain.SessionDeletionPending()
			}
			candidates = append(candidates, v)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	ids := []domain.ID{input.Completion.InputID}
	for _, binding := range input.Progress.AcceptedInputs {
		ids = append(ids, binding.InputID)
	}
	var boundary uint64
	for _, id := range ids {
		row, err := t.Get(domain.QueueKind, id)
		if err != nil {
			return nil, err
		}
		value, err := Decode[domain.QueuedInput](row)
		if err != nil || row.SessionID != input.SourceSessionID || value.Delivery != domain.InputAccepted || value.ExecutionID != input.Completion.ExecutionID {
			return nil, domain.SessionDeletionPending()
		}
		boundary = max(boundary, value.Sequence)
	}
	var result []domain.ImageUpload
	for _, v := range candidates {
		if v.SessionID != input.SourceSessionID {
			result = append(result, v)
			continue
		}
		row, err := t.Get(domain.QueueKind, v.InputID)
		if err != nil {
			return nil, err
		}
		value, err := Decode[domain.QueuedInput](row)
		if err != nil || row.SessionID != v.SessionID {
			return nil, domain.SessionDeletionPending()
		}
		if value.Delivery == domain.InputAccepted && value.Sequence <= boundary && (v.GeneratedExecutionID != "" || slices.Contains(value.Attachments, v.Attachment)) {
			result = append(result, v)
		}
	}
	return result, nil
}

func (t *Tx) InheritForkImages(input domain.ForkJobInput) error {
	if input.Purpose == domain.SidechatFork {
		return nil
	}
	if input.Purpose != domain.IndependentFork {
		return domain.InvalidImageInput()
	}
	values, err := t.forkImageUploads(input)
	if err != nil {
		return err
	}
	for _, v := range values {
		if slices.Contains(v.Owners, input.ChildSessionID) {
			continue
		}
		if len(v.Owners) >= 4096 {
			return domain.SessionDeletionPending()
		}
		r, _, err := t.ImageUploadRecord(v.Attachment.ID)
		if err != nil {
			return err
		}
		v.Owners = append(v.Owners, input.ChildSessionID)
		if _, err := t.PutImageUpload(r, v); err != nil {
			return err
		}
	}
	return nil
}

// Sidechat borrows only its immutable parent fork-point prefix. That read
// reference never becomes deletion ownership or permits parent mutation.
func (t *Tx) ImageAttachmentReadable(session domain.ID, ref domain.ImageAttachment) error {
	if ref.Validate() != nil {
		return domain.InvalidImageInput()
	}
	r, err := t.Get(domain.SessionKind, session)
	if err != nil {
		return err
	}
	s, err := Decode[domain.Session](r)
	if err != nil {
		return err
	}
	deleting, err := t.SessionDeleting(session)
	if err != nil || deleting {
		return domain.SessionDeletionPending()
	}
	_, v, err := t.ImageUploadRecord(ref.ID)
	if err != nil {
		return err
	}
	if v.Attachment != ref || v.Quarantined || v.State != domain.ImageClaimed {
		return domain.InvalidImageInput()
	}
	if slices.Contains(v.Owners, session) {
		return nil
	}
	if !s.IsSidechat() || s.Fork == nil || !slices.Contains(v.Owners, s.Fork.SourceSessionID) {
		return domain.InvalidImageInput()
	}
	if pending, err := t.SessionDeleting(s.Fork.SourceSessionID); err != nil || pending {
		return domain.SessionDeletionPending()
	}
	jobRow, err := t.Get(domain.JobKind, s.Fork.JobID)
	if err != nil {
		return err
	}
	job, err := Decode[domain.Job](jobRow)
	var input domain.ForkJobInput
	digest := sha256.Sum256(job.Input)
	if err != nil || job.State != domain.JobSucceeded || jobRow.SessionID != s.Fork.SourceSessionID || job.MachineID != s.MachineID || job.AssignedDeviceID != s.Fork.WorkerDeviceID || hex.EncodeToString(digest[:]) != s.Fork.JobInputDigest || job.Type != domain.ForkSessionJob || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.RuntimeID != s.Fork.RuntimeID || input.Purpose != domain.SidechatFork || input.ChildSessionID != session || input.SourceSessionID != s.Fork.SourceSessionID || input.Completion.ExecutionID != s.Fork.SourceExecutionID || input.Completion.NativeTurnID != s.Fork.SourceTurnID {
		return domain.InvalidImageInput()
	}
	values, err := t.forkImageUploads(input)
	if err != nil {
		return err
	}
	for _, original := range values {
		if original.Attachment == ref {
			return nil
		}
	}
	return domain.InvalidImageInput()
}

func (t *Tx) planImageSessionDeletion(v SessionDeletion) (SessionDeletion, error) {
	rows, err := t.sessionImageUploads(v.SessionID)
	if err != nil {
		return v, err
	}
	for _, row := range rows {
		_, upload, err := t.ImageUploadRecord(row.ID)
		if err != nil {
			return v, err
		}
		owned := slices.Contains(upload.Owners, v.SessionID)
		staging := upload.InputID == "" && upload.SessionID == v.SessionID && upload.State != domain.ImageDeleted
		preserved := upload.GeneratedExecutionID != "" && owned && len(upload.Owners) > 1
		if !staging && (!owned || len(upload.Owners) > 1) && !preserved {
			continue
		}
		index := -1
		for i, worker := range v.Workers {
			if worker.Work.DeviceID == upload.WorkerDeviceID {
				if worker.Work.MachineID != upload.Attachment.MachineID {
					return v, domain.SessionDeletionPending()
				}
				index = i
			}
		}
		if index < 0 {
			v.Workers = append(v.Workers, SessionDeletionWorker{Work: domain.SessionDeletionWork{Version: 1, DeletionID: v.ID, ServerID: v.ServerID, SessionID: v.SessionID, MachineID: upload.Attachment.MachineID, DeviceID: upload.WorkerDeviceID, Copies: []domain.SessionDeletionCopy{}, PreparationDigests: []string{}}})
			index = len(v.Workers) - 1
		}
		if preserved {
			v.Workers[index].Work.PreservedGeneratedImages = append(v.Workers[index].Work.PreservedGeneratedImages, upload.Attachment)
		} else {
			v.Workers[index].Work.Images = append(v.Workers[index].Work.Images, upload.Attachment)
		}
	}
	return v, nil
}

// The synchronized external intent precedes this transaction. Dropping a
// source owner cannot erase another Fork's lifetime or start native cleanup.
func (t *Tx) applyImageSessionDeletion(v SessionDeletion) error {
	rows, err := t.sessionImageUploads(v.SessionID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		r, upload, err := t.ImageUploadRecord(row.ID)
		if err != nil {
			return err
		}
		owned := slices.Contains(upload.Owners, v.SessionID)
		staging := upload.InputID == "" && upload.SessionID == v.SessionID && upload.State != domain.ImageDeleted
		if !owned && !staging {
			continue
		}
		if owned {
			upload.Owners = slices.DeleteFunc(upload.Owners, func(id domain.ID) bool { return id == v.SessionID })
		}
		if len(upload.Owners) == 0 {
			// The immutable plan must include this exact original reference and
			// device before a last owner may be released.
			planned := false
			for _, worker := range v.Workers {
				if worker.Work.DeviceID == upload.WorkerDeviceID && slices.Contains(worker.Work.Images, upload.Attachment) {
					planned = true
				}
			}
			if !planned {
				return domain.SessionDeletionPending()
			}
			upload.State = domain.ImageDeleting
		}
		if _, err = t.PutImageUpload(r, upload); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tx) purgeSessionImages(v SessionDeletion) error {
	for _, worker := range v.Workers {
		if !worker.Acknowledged && len(worker.Work.Images) != 0 {
			return domain.SessionDeletionPending()
		}
		for _, ref := range worker.Work.Images {
			row, upload, err := t.ImageUploadRecord(ref.ID)
			if domain.SafeError(err).Code == domain.NotFound {
				var deleted bool
				if err := t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM tombstones WHERE id=? AND kind='job')", ref.ID).Scan(&deleted); err != nil {
					return storageError(err)
				}
				if deleted {
					continue
				}
				return domain.SessionDeletionPending()
			}
			if err != nil {
				return err
			}
			if upload.Attachment != ref || upload.WorkerDeviceID != worker.Work.DeviceID || len(upload.Owners) != 0 || (upload.State != domain.ImageDeleting && upload.State != domain.ImageDeleted) {
				return domain.SessionDeletionPending()
			}
			if err := t.deleteSessionRecord(row); err != nil {
				return err
			}
		}
	}
	return nil
}
