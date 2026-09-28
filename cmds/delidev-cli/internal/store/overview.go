package store

import (
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type OverviewCounts struct {
	ActiveSessions      uint64
	PendingInteractions uint64
	RegisteredWorkers   uint64
	ConnectedWorkers    uint64
}

// Count retained execution ownership, including uncertain cleanup, rather than
// guessing which processes are running. Read state never resolves a request.
func (t *Tx) Overview(active map[domain.ID]bool, now time.Time) (OverviewCounts, error) {
	var result OverviewCounts
	if err := t.Authorize(); err != nil {
		return result, err
	}
	for _, query := range []struct {
		sql string
		out *uint64
	}{
		{`SELECT COUNT(*) FROM entities WHERE kind='session' AND COALESCE(json_extract(body,'$.active_execution_id'),'')<>''`, &result.ActiveSessions},
		{`SELECT COUNT(*) FROM entities i JOIN entities s ON s.id=i.session_id AND s.kind='session'
WHERE i.kind='interaction' AND json_extract(i.body,'$.closure')='open'
AND json_extract(i.body,'$.response') IS NULL AND json_extract(i.body,'$.approval_response') IS NULL
AND json_extract(s.body,'$.archive')='active'
AND json_extract(s.body,'$.active_execution_id')=json_extract(i.body,'$.execution_id')`, &result.PendingInteractions},
		{`SELECT COUNT(*) FROM entities WHERE kind='machine'`, &result.RegisteredWorkers},
	} {
		if err := t.tx.QueryRowContext(t.ctx, query.sql).Scan(query.out); err != nil {
			return OverviewCounts{}, storageError(err)
		}
	}
	for machine := range active {
		if !active[machine] || machine.Validate() != nil {
			continue
		}
		var authorized bool
		err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM entities m WHERE m.id=? AND m.kind='machine'
AND json_extract(m.body,'$.disabled')=0 AND EXISTS(SELECT 1 FROM entities d WHERE d.kind='device'
AND json_extract(d.body,'$.type')='worker' AND json_extract(d.body,'$.revoked')=0
AND json_extract(d.body,'$.machine_id')=m.id))`, machine).Scan(&authorized)
		if err != nil {
			return OverviewCounts{}, storageError(err)
		}
		if !authorized {
			continue
		}
		fresh, err := t.WorkerAvailableAt(machine, now, now)
		if err != nil {
			return OverviewCounts{}, err
		}
		if fresh {
			result.ConnectedWorkers++
		}
	}
	return result, nil
}
