// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/jackc/pgx/v5"
	"time"
)

func (w *Worker) retain(ctx context.Context, tx pgx.Tx, now time.Time) error {
	// Normalized quantities and price attribution are accounting evidence; keep
	// them past raw-usage retention while their accounting retention is required.
	cutoff := now.Add(-time.Duration(max(w.Config.RawUsageRetentionSeconds, w.Config.AccountingRetentionSeconds)) * time.Second)
	if w.Config.AccountingRetentionSeconds <= 0 || w.Config.RawUsageRetentionSeconds <= 0 {
		return nil
	}
	if _, e := tx.Exec(ctx, "SELECT set_config('delcore.retention','on',true)"); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, `INSERT INTO usage_replay SELECT service_id,event_key,id,fingerprint FROM usage_events WHERE tenant_id IN (SELECT id FROM tenants WHERE deleted_at<$1) ON CONFLICT DO NOTHING`, cutoff); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, "DELETE FROM ledger WHERE tenant_id IN (SELECT id FROM tenants WHERE deleted_at<$1)", cutoff); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, "DELETE FROM usage_events WHERE tenant_id IN (SELECT id FROM tenants WHERE deleted_at<$1)", cutoff); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, "UPDATE provider_transactions SET amount=0 WHERE tenant_id IN (SELECT id FROM tenants WHERE deleted_at<$1)", cutoff); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, "DELETE FROM settlements WHERE tenant_id IN (SELECT id FROM tenants WHERE deleted_at<$1)", cutoff); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, "SELECT set_config('delcore.retention','off',true)"); e != nil {
		return e
	}
	if w.Config.AuditRetentionSeconds > 0 {
		if _, e := tx.Exec(ctx, "DELETE FROM audit WHERE created_at<$1 AND (tenant_id IS NULL OR tenant_id IN (SELECT id FROM tenants WHERE deleted_at IS NOT NULL))", now.Add(-time.Duration(w.Config.AuditRetentionSeconds)*time.Second)); e != nil {
			return e
		}
	}
	return nil
}
