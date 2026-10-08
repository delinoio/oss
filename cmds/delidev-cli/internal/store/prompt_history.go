// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const MaxProjectPromptHistory = 100

// Acceptance uses the serialized receipt transaction's durable event order.
// Client clocks and UUID creation order cannot reorder concurrent submissions.
func (t *Tx) AppendProjectPromptHistory(project domain.ID, prompt string) error {
	if _, err := t.Get(domain.ProjectKind, project); err != nil {
		return err
	}
	var sequence uint64
	if err := t.tx.QueryRowContext(t.ctx, "SELECT COALESCE(MAX(sequence),0)+1 FROM events").Scan(&sequence); err != nil {
		return storageError(err)
	}
	value := domain.ProjectPromptHistory{Prompt: prompt, AcceptedAt: t.now, AcceptanceSequence: sequence}
	if err := value.Validate(); err != nil {
		return err
	}
	if _, err := t.Put(domain.ProjectPromptHistoryKind, domain.NewID(), 0, "", project, value); err != nil {
		return err
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT id FROM entities WHERE kind=? AND project_id=? ORDER BY json_extract(body,'$.acceptance_sequence') DESC LIMIT -1 OFFSET ?", domain.ProjectPromptHistoryKind, project, MaxProjectPromptHistory)
	if err != nil {
		return storageError(err)
	}
	ids := []domain.ID{}
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return storageError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return storageError(err)
	}
	for _, id := range ids {
		row, err := t.Get(domain.ProjectPromptHistoryKind, id)
		if err != nil {
			return err
		}
		if err := t.Delete(domain.ProjectPromptHistoryKind, id, row.Revision); err != nil {
			return err
		}
	}

	return nil
}
func (t *Tx) projectPromptHistory(project domain.ID, before uint64, limit int) ([]Record, error) {
	query := "SELECT id FROM entities WHERE kind=? AND project_id=?"
	args := []any{domain.ProjectPromptHistoryKind, project}
	if before != 0 {
		query += " AND json_extract(body,'$.acceptance_sequence')<?"
		args = append(args, before)
	}
	query += " ORDER BY json_extract(body,'$.acceptance_sequence') DESC LIMIT ?"
	args = append(args, limit)
	// Sort identities only. Sorting full maximum-size documents spills private text
	// to large temporary pages and makes bounded history reads unnecessarily costly.
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, storageError(err)
	}
	ids := []domain.ID{}
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, storageError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, storageError(err)
	}
	out := make([]Record, 0, len(ids))
	for _, id := range ids {
		row, err := t.Get(domain.ProjectPromptHistoryKind, id)
		if err != nil {
			return nil, err
		}
		value, err := Decode[domain.ProjectPromptHistory](row)
		if err != nil {
			return nil, err
		}
		if row.SessionID != "" || row.ProjectID != project || row.Revision != 1 || value.Validate() != nil {
			return nil, corrupt()
		}
		out = append(out, row)
	}
	return out, nil
}

func (t *Tx) ListProjectPromptHistory(project domain.ID, before uint64, limit int) ([]Record, error) {
	if err := project.Validate(); err != nil {
		return nil, err
	}
	if limit < 1 || limit > MaxProjectPromptHistory+1 {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid prompt history page size.", "Request at most 100 entries.")
	}
	if _, err := t.Get(domain.ProjectKind, project); err != nil {
		return nil, err
	}
	return t.projectPromptHistory(project, before, limit)
}
func (t *Tx) ClearProjectPromptHistory(project domain.ID) (uint32, error) {
	rows, err := t.ListProjectPromptHistory(project, 0, MaxProjectPromptHistory+1)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if err := t.Delete(domain.ProjectPromptHistoryKind, row.ID, row.Revision); err != nil {
			return 0, err
		}
	}
	return uint32(len(rows)), nil
}

// Validate the closed document and project-only ownership in live/backup images.
func validateProjectPromptHistory(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "SELECT e.id,e.kind,e.revision,e.session_id,e.project_id,e.body,e.created_at,e.updated_at FROM entities e WHERE e.kind='project_prompt_history'")
	if err != nil {
		return storageError(err)
	}
	defer rows.Close()
	counts := map[domain.ID]int{}
	sequences := map[uint64]bool{}
	for rows.Next() {
		row, err := scan(rows)
		if err != nil {
			return corrupt()
		}
		value, err := Decode[domain.ProjectPromptHistory](row)
		if err != nil || value.Validate() != nil || row.ID.Validate() != nil || row.ProjectID.Validate() != nil || row.SessionID != "" || row.Revision != 1 || sequences[value.AcceptanceSequence] {
			return corrupt()
		}
		sequences[value.AcceptanceSequence] = true
		counts[row.ProjectID]++
		if counts[row.ProjectID] > MaxProjectPromptHistory {
			return corrupt()
		}
	}
	if err := rows.Err(); err != nil {
		return storageError(err)
	}
	rows.Close()
	var orphan bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM entities e WHERE e.kind='project_prompt_history' AND NOT EXISTS(SELECT 1 FROM entities p WHERE p.id=e.project_id AND p.kind='project'))").Scan(&orphan); err != nil {
		return storageError(err)
	}
	if orphan {
		return corrupt()
	}
	return nil
}
