// SPDX-License-Identifier: Apache-2.0
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const forkImageSnapshotPrefix = "fork-image-snapshot:"
const forkImageCutoverKey = "fork-image-snapshot-cutover"

type forkImageSnapshot struct {
	Version     int                      `json:"version"`
	JobID       domain.ID                `json:"job_id"`
	SourceID    domain.ID                `json:"source_session_id"`
	ChildID     domain.ID                `json:"child_session_id"`
	ExecutionID domain.ID                `json:"execution_id"`
	TurnID      domain.NativeIdentity    `json:"native_turn_id"`
	InputDigest string                   `json:"input_digest"`
	Attachments []domain.ImageAttachment `json:"attachments"`
}

func forkSnapshotBinding(job domain.ID, input domain.ForkJobInput, raw []byte) forkImageSnapshot {
	digest := sha256.Sum256(raw)
	return forkImageSnapshot{Version: 1, JobID: job, SourceID: input.SourceSessionID, ChildID: input.ChildSessionID, ExecutionID: input.Completion.ExecutionID, TurnID: input.Completion.NativeTurnID, InputDigest: hex.EncodeToString(digest[:]), Attachments: []domain.ImageAttachment{}}
}

// Freeze at admission, before any later waiting input can be accepted. Sequence
// is acceptance history, so it cannot identify a native prefix after movement.
func (t *Tx) FreezeForkImages(jobID domain.ID, input domain.ForkJobInput, raw []byte) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	if jobID.Validate() != nil || input.Validate() != nil {
		return domain.InvalidImageInput()
	}
	for _, id := range append([]domain.ID{input.Completion.InputID}, forkAcceptedInputIDs(input)...) {
		row, err := t.Get(domain.QueueKind, id)
		if err != nil {
			return err
		}
		value, err := Decode[domain.QueuedInput](row)
		if err != nil || row.SessionID != input.SourceSessionID || value.Delivery != domain.InputAccepted || value.ExecutionID != input.Completion.ExecutionID {
			return domain.InvalidImageInput()
		}
	}
	snapshot := forkSnapshotBinding(jobID, input, raw)
	rows, err := t.sessionImageUploads(input.SourceSessionID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		_, upload, err := t.ImageUploadRecord(row.ID)
		if err != nil {
			return err
		}
		if !slices.Contains(upload.Owners, input.SourceSessionID) {
			continue
		}
		if upload.Quarantined || upload.State != domain.ImageClaimed {
			return domain.SessionDeletionPending()
		}
		if upload.SessionID == input.SourceSessionID {
			queue, err := t.Get(domain.QueueKind, upload.InputID)
			if err != nil {
				return err
			}
			value, err := Decode[domain.QueuedInput](queue)
			if err != nil || queue.SessionID != input.SourceSessionID {
				return domain.InvalidImageInput()
			}
			if upload.GeneratedExecutionID != "" && upload.GeneratedExecutionID != value.ExecutionID {
				return domain.InvalidImageInput()
			}
			if value.Delivery != domain.InputAccepted || upload.GeneratedExecutionID == "" && !slices.Contains(value.Attachments, upload.Attachment) {
				continue
			}
		}
		snapshot.Attachments = append(snapshot.Attachments, upload.Attachment)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if _, err = t.tx.ExecContext(t.ctx, "INSERT OR IGNORE INTO metadata(key,value) VALUES(?,?)", forkImageCutoverKey, strconv.FormatInt(t.now.UnixMilli(), 10)); err != nil {
		return storageError(err)
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?)", forkImageSnapshotPrefix+string(jobID), string(encoded))
	return storageError(err)
}
func forkAcceptedInputIDs(input domain.ForkJobInput) []domain.ID {
	ids := []domain.ID{}
	for _, binding := range input.Progress.AcceptedInputs {
		ids = append(ids, binding.InputID)
	}
	return ids
}

func (t *Tx) frozenForkImageUploads(jobID domain.ID, input domain.ForkJobInput) ([]domain.ImageUpload, error) {
	row, err := t.Get(domain.JobKind, jobID)
	if err != nil {
		return nil, err
	}
	job, err := Decode[domain.Job](row)
	if err != nil {
		return nil, err
	}
	bound := forkSnapshotBinding(jobID, input, job.Input)
	var raw string
	err = t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", forkImageSnapshotPrefix+string(jobID)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		var cutover string
		e := t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", forkImageCutoverKey).Scan(&cutover)
		if errors.Is(e, sql.ErrNoRows) {
			return t.forkImageUploads(input)
		}
		boundary, parse := strconv.ParseInt(cutover, 10, 64)
		if e != nil || parse != nil || row.CreatedAt.UnixMilli() >= boundary {
			return nil, queueOrderRecovery()
		}
		return t.forkImageUploads(input)
	}
	if err != nil {
		return nil, storageError(err)
	}
	var snapshot forkImageSnapshot
	if len(raw) > 1<<20 || domain.Decode([]byte(raw), &snapshot) != nil || snapshot.Version != 1 || snapshot.JobID != bound.JobID || snapshot.SourceID != bound.SourceID || snapshot.ChildID != bound.ChildID || snapshot.ExecutionID != bound.ExecutionID || snapshot.TurnID != bound.TurnID || snapshot.InputDigest != bound.InputDigest || snapshot.Attachments == nil || len(snapshot.Attachments) > domain.MaxSessionImageAttachments {
		return nil, domain.InvalidImageInput()
	}
	result := []domain.ImageUpload{}
	seen := map[domain.ID]bool{}
	for _, ref := range snapshot.Attachments {
		if ref.Validate() != nil || seen[ref.ID] {
			return nil, domain.InvalidImageInput()
		}
		seen[ref.ID] = true
		_, upload, err := t.ImageUploadRecord(ref.ID)
		if err != nil {
			return nil, err
		}
		if upload.Attachment != ref || upload.Quarantined || upload.State != domain.ImageClaimed || !slices.Contains(upload.Owners, input.SourceSessionID) {
			return nil, domain.InvalidImageInput()
		}
		result = append(result, upload)
	}
	return result, nil
}

func (t *Tx) InheritForkImagesForJob(jobID domain.ID, input domain.ForkJobInput) error {
	if input.Purpose == domain.SidechatFork {
		return nil
	}
	if input.Purpose != domain.IndependentFork {
		return domain.InvalidImageInput()
	}
	values, err := t.frozenForkImageUploads(jobID, input)
	if err != nil {
		return err
	}
	return t.inheritForkImageUploads(input, values)
}

// Retain snapshots while either the original job or dependent child remains.
func (t *Tx) retireForkImageSnapshots(session domain.ID) error {
	_, err := t.tx.ExecContext(t.ctx, `DELETE FROM metadata WHERE key LIKE 'fork-image-snapshot:%' AND (json_extract(value,'$.source_session_id')=? OR json_extract(value,'$.child_session_id')=?) AND NOT EXISTS(SELECT 1 FROM entities WHERE id=json_extract(metadata.value,'$.job_id')) AND NOT EXISTS(SELECT 1 FROM entities WHERE id=json_extract(metadata.value,'$.child_session_id'))`, session, session)
	return storageError(err)
}
