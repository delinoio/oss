package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Attempt binding and uncertainty can change without changing the PR set.
// Fingerprint every retained revision under the same read transaction so a
// cursor never silently skips an attempt whose state changed between pages.
func (t *Tx) PRRemediationHistoryFingerprint(setID domain.ID) (string, error) {
	r, set, err := t.GetPRProblemSet(setID)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s:%d\n", r.ID, r.Revision)
	rows, err := t.tx.QueryContext(t.ctx, `SELECT a.id,a.sequence,e.revision FROM pr_remediation_attempts a JOIN entities e ON a.id=e.id WHERE a.set_id=? ORDER BY a.sequence LIMIT ?`, setID, domain.MaxPRRemediationAttempts+1)
	if err != nil {
		return "", storageError(err)
	}
	defer rows.Close()
	var count uint32
	for rows.Next() {
		var id domain.ID
		var sequence uint32
		var revision uint64
		if err := rows.Scan(&id, &sequence, &revision); err != nil {
			return "", storageError(err)
		}
		count++
		if count > domain.MaxPRRemediationAttempts || sequence != count || id.Validate() != nil || revision == 0 || revision >= 1<<63 {
			return "", prRemediationConflict()
		}
		fmt.Fprintf(h, "%s:%d:%d\n", id, sequence, revision)
	}
	if err := rows.Err(); err != nil {
		return "", storageError(err)
	}
	if set.Remediation == nil && count != 0 || set.Remediation != nil && set.Remediation.Sequence != count {
		return "", prRemediationConflict()
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (t *Tx) ListPRRemediationAttempts(setID, after domain.ID, limit int) ([]Record, bool, error) {
	set, _, err := t.GetPRProblemSet(setID)
	if err != nil {
		return nil, false, err
	}
	if limit < 1 || limit > 50 || after != "" && after.Validate() != nil {
		return nil, false, domain.Fail(domain.InvalidArgument, "Invalid remediation-history page.", "Use 1 through 50 entries and the original cursor.")
	}
	before := uint32(domain.MaxPRRemediationAttempts + 1)
	if after != "" {
		_, previous, err := t.GetPRRemediationAttempt(after)
		if err != nil {
			return nil, false, err
		}
		if previous.SetID != setID {
			return nil, false, prRemediationConflict()
		}
		before = previous.Sequence
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT id FROM pr_remediation_attempts WHERE set_id=? AND sequence<? ORDER BY sequence DESC LIMIT ?", setID, before, limit+1)
	if err != nil {
		return nil, false, storageError(err)
	}
	ids := []domain.ID{}
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, false, storageError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, storageError(err)
	}
	result := []Record{}
	size := len(set.Data)
	for _, id := range ids {
		r, _, err := t.GetPRRemediationAttempt(id)
		if err != nil {
			return nil, false, err
		}
		if len(result) == limit || size+len(r.Data) > 1<<20 {
			if len(result) == 0 {
				return nil, false, domain.Fail(domain.ResourceExhausted, "The original remediation attempt exceeds the history page bound.", "Preserve the complete original record; no history was truncated.")
			}
			return result, true, nil
		}
		size += len(r.Data)
		result = append(result, r)
	}
	return result, false, nil
}
