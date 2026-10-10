// SPDX-License-Identifier: Apache-2.0
package store

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
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
		return domain.Fail(domain.Conflict, "This session has an unfinished native fork.", "Observe the original fork job before advancing its source boundary.")
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

// A same-question generation cannot compose ExecutionStartupRetry: its closed
// assignment validator rejects Retry. Reserve Fork, execution and first recovery
// before admission; existing allowed startup retries reserve their own execution
// plus recovery separately. These checks grant no native retry authority.
func (t *Tx) CheckSidechatRetryCapacity(child Record, parent Record, actor domain.Principal) error {
	value, err := Decode[domain.Session](child)
	if err != nil || !value.IsSidechat() || value.Fork.SourceSessionID != parent.ID {
		return domain.SidechatUnavailable()
	}
	return t.checkSessionJobCapacity(child, actor, 3, true)
}

func (t *Tx) CheckExecutionRecoveryCapacity(session Record, actor domain.Principal) error {
	return t.checkSessionJobCapacity(session, actor, 1, false)
}

func (t *Tx) CheckExecutionStartupRetryCapacity(session Record, actor domain.Principal) error {
	return t.checkSessionJobCapacity(session, actor, 2, false)
}

// Inspect the original shared deletion envelope and all captured revisions in
// this same mutation. A sibling's admitted but not yet published ownership must
// retain its space; an incoming job replaces only this session's own headroom.
func (t *Tx) checkSessionJobCapacity(session Record, actor domain.Principal, additional int, retryFork bool) error {
	var count int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM entities WHERE kind='job' AND session_id=?`, session.ID).Scan(&count); err != nil {
		return storageError(err)
	}
	if count+additional > 4096 {
		return domain.Fail(domain.ResourceExhausted, "Session execution history is full.", "Retain every earlier generation and use confirmed permanent cleanup.")
	}
	value, err := Decode[domain.Session](session)
	if err != nil {
		return err
	}
	root := session
	if value.IsSidechat() {
		root, err = t.Get(domain.SessionKind, value.Fork.SourceSessionID)
		if err != nil {
			return err
		}
	}
	server := domain.NewID()
	planFor := func(r Record) (SessionDeletion, error) {
		return t.planSessionDeletion(SessionDeletion{Version: 1, ID: domain.NewID(), SessionID: r.ID, ServerID: server, RequestID: domain.NewID(), Actor: actor, ExpectedRevision: r.Revision, Revision: 1, AcceptedAt: r.CreatedAt})
	}
	plan, err := planFor(root)
	if err != nil {
		return err
	}
	records := []Record{root}
	ids, err := t.SidechatDependents(root.ID)
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
		records = append(records, r)
	}
	headroom, err := sessionJobDeletionHeadroom(additional, retryFork)
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.ID == session.ID {
			continue
		}
		pending, fork, err := t.pendingSessionJobHeadroom(r)
		if err != nil {
			return err
		}
		bytes, err := sessionJobDeletionHeadroom(pending, fork)
		if err != nil {
			return err
		}
		headroom += bytes
	}
	raw, err := json.Marshal(plan)
	// Preserve the existing outer-journal margin as well as the serialized upper
	// bound for each prospective original copy and unpublished retry Fork record.
	if err != nil || len(raw)+16384+headroom > domain.MaxSessionDeletionBytes {
		return domain.Fail(domain.ResourceExhausted, "Session execution history reached its deletion bound.", "Retain every earlier generation; delete settled sessions through confirmed cleanup.")
	}
	return nil
}

func (t *Tx) pendingSessionJobHeadroom(r Record) (int, bool, error) {
	session, err := Decode[domain.Session](r)
	if err != nil {
		return 0, false, err
	}
	jobs, fork := 0, false
	if session.SidechatActiveRetry != "" {
		found := false
		for _, generation := range session.SidechatRetries {
			if generation.ID == session.SidechatActiveRetry {
				found = true
				if generation.ExecutionJobID == "" {
					jobs, fork = 3, true
				} else {
					execution, err := t.Get(domain.JobKind, generation.ExecutionJobID)
					if err != nil {
						return 0, false, err
					}
					job, err := Decode[domain.Job](execution)
					if err != nil || execution.SessionID != r.ID || job.Type != domain.ExecuteSessionJob {
						return 0, false, domain.SidechatUnavailable()
					}
					jobs = 1
					if job.State == domain.JobQueued {
						jobs = 2
					}
				}
			}
		}
		if !found {
			return 0, false, domain.SidechatUnavailable()
		}
	} else if session.ActiveExecutionID != "" || session.Recovery != domain.NoRecovery {
		jobs = 2
	}
	if session.ExecutionRecoveryJobID != "" {
		prior, err := t.Get(domain.JobKind, session.ExecutionRecoveryJobID)
		if err != nil {
			return 0, false, err
		}
		job, err := Decode[domain.Job](prior)
		if err != nil || prior.SessionID != r.ID || job.Type != domain.RecoverExecutionJob {
			return 0, false, domain.ExecutionRecoveryUncertain()
		}
		if job.State == domain.JobQueued {
			// An unclaimed recovery still needs its original ownership copy.
			jobs = 1
		} else if job.State == domain.JobClaimed {
			jobs = 0
		}
	}
	return jobs, fork, nil
}

// This conservative metadata-only bound includes a complete extra Worker header
// per future job, all optional copy identities, the largest durable revision and
// Fork checkpoint references. It is never persisted or treated as ownership.
func sessionJobDeletionHeadroom(jobs int, retryFork bool) (int, error) {
	if jobs == 0 && !retryFork {
		return 0, nil
	}
	id := domain.NewID()
	hash := strings.Repeat("f", 64)
	copy := domain.SessionDeletionCopy{SidechatRetry: true, JobID: id, UnpublishedChildProcessID: id, UnpublishedSidechatID: id, Type: domain.RecoverExecutionJob, Revision: (1 << 63) - 1, Digest: hash, InstanceID: id, ExecutionID: id, SnapshotID: id, ActionID: id}
	worker := SessionDeletionWorker{Work: domain.SessionDeletionWork{Version: 1, DeletionID: id, ServerID: id, SessionID: id, MachineID: id, DeviceID: id, Copies: []domain.SessionDeletionCopy{copy}, PreparationDigests: []string{hash}}, Acknowledged: true, RequestID: id}
	raw, err := json.Marshal(worker)
	if err != nil {
		return 0, err
	}
	bytes := jobs * (len(raw) + 1)
	if retryFork {
		raw, err := json.Marshal(domain.SessionDeletionFork{JobID: id, RuntimeID: id, CheckpointDigest: hash, JobInputDigest: hash})
		if err != nil {
			return 0, err
		}
		bytes += len(raw) + len(`,"retry_forks":[]`) + 1
	}
	return bytes, nil
}
