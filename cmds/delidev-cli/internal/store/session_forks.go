// SPDX-License-Identifier: Apache-2.0
package store

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// A durable fork job reserves only its original source boundary. Uncertainty
// keeps that reservation; another request cannot bypass native ownership loss.
func (t *Tx) RequireNoSessionFork(session domain.ID) error {
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM entities WHERE kind='job' AND session_id=? AND json_extract(body,'$.type')='fork-session' AND json_extract(body,'$.state') IN ('queued','claimed','uncertain'))`, session).Scan(&exists)
	if err != nil {
		return storageError(err)
	}
	if exists {
		return domain.Fail(domain.Conflict, "This session has an unfinished native fork.", "Observe the original fork job before advancing its source boundary.")
	}
	return nil
}

func (t *Tx) RequireForkActor(actor domain.Principal) error {
	if actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice {
		return domain.Fail(domain.PermissionDenied, "Fork requires owner or paired-client authority.", "Use the original authorized client.")
	}
	if actor.Type == domain.OwnerDevice && actor.DeviceID == "" {
		return nil
	}
	r, err := t.Get(domain.DeviceKind, actor.DeviceID)
	if err != nil {
		return err
	}
	device, err := Decode[domain.Device](r)
	if err != nil || device.Revoked || device.Type != actor.Type {
		return domain.Fail(domain.PermissionDenied, "The original fork client is no longer authorized.", "Preserve the accepted operation; do not replace its actor.")
	}
	return nil
}

// Original unpublished child creation still reserves parent deletion. Retry
// runtimes belong to the existing child and are enumerated by its frozen plan.
func (t *Tx) RequireNoOriginalSessionFork(session domain.ID) error {
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM entities WHERE kind='job' AND session_id=? AND json_extract(body,'$.type')='fork-session' AND json_extract(body,'$.input.retry') IS NULL AND json_extract(body,'$.state') IN ('queued','claimed','uncertain'))`, session).Scan(&exists)
	if err != nil {
		return storageError(err)
	}
	if exists {
		return domain.SessionDeletionPending()
	}
	return nil
}

func (t *Tx) DirectSidechatQuestion(session domain.ID) (Record, domain.QueuedInput, error) {
	// Retry inputs never hide a second direct question behind pagination.
	selected, err := t.tx.QueryContext(t.ctx, `SELECT id FROM entities WHERE kind='queue' AND session_id=? AND json_extract(body,'$.sidechat_retry_generation') IS NULL ORDER BY id LIMIT 2`, session)
	if err != nil {
		return Record{}, domain.QueuedInput{}, storageError(err)
	}
	var ids []domain.ID
	for selected.Next() {
		var id domain.ID
		if err := selected.Scan(&id); err != nil {
			selected.Close()
			return Record{}, domain.QueuedInput{}, storageError(err)
		}
		ids = append(ids, id)
	}
	err = selected.Err()
	selected.Close()
	if err != nil {
		return Record{}, domain.QueuedInput{}, storageError(err)
	}
	if len(ids) != 1 {
		return Record{}, domain.QueuedInput{}, domain.SidechatUnavailable()
	}
	r, err := t.Get(domain.QueueKind, ids[0])
	if err != nil {
		return r, domain.QueuedInput{}, err
	}
	q, err := Decode[domain.QueuedInput](r)
	if err != nil || len(q.Attachments) != 0 || len(q.Skills) != 0 || q.Delivery != domain.InputAccepted || q.ExecutionID.Validate() != nil || q.NativeRequestID.Validate() != nil {
		return r, q, domain.SidechatUnavailable()
	}
	return r, q, nil
}

// Reserve within existing job and deletion envelopes before any native effect.
// The bounded margin covers one fresh Fork and one exact question execution.
func (t *Tx) CheckSidechatRetryCapacity(child Record, parent Record, actor domain.Principal) error {
	server := domain.NewID()
	planFor := func(r Record) (SessionDeletion, error) {
		return t.planSessionDeletion(SessionDeletion{Version: 1, ID: domain.NewID(), SessionID: r.ID, ServerID: server, RequestID: domain.NewID(), Actor: actor, ExpectedRevision: r.Revision, Revision: 1, AcceptedAt: r.CreatedAt})
	}
	plan, err := planFor(parent)
	if err != nil {
		return err
	}
	ids, err := t.SidechatDependents(parent.ID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		r, err := t.Get(domain.SessionKind, id)
		if err != nil {
			return err
		}
		dependent, err := planFor(r)
		if err != nil {
			return err
		}
		plan.Dependents = append(plan.Dependents, dependent)
	}
	raw, err := json.Marshal(plan)
	if err != nil || len(raw)+16384 > domain.MaxSessionDeletionBytes {
		return domain.Fail(domain.ResourceExhausted, "Sidechat retry history reached its deletion bound.", "Retain every earlier generation; delete settled Sidechats through confirmed cleanup.")
	}
	var count int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM entities WHERE kind='job' AND session_id=?`, child.ID).Scan(&count); err != nil {
		return storageError(err)
	}
	if count+2 > 4096 {
		return domain.Fail(domain.ResourceExhausted, "Sidechat execution history is full.", "Retain every earlier generation and use confirmed permanent cleanup.")
	}
	return nil
}
