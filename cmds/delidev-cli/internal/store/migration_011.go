// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func migration011(ctx context.Context, tx *sql.Tx, original int) error {
	var legacySchedules int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM entities WHERE kind IN ('schedule','occurrence')").Scan(&legacySchedules); err != nil {
		return storageError(err)
	}
	if legacySchedules != 0 {
		return domain.Fail(domain.RecoveryRequired, "Legacy schedule ownership is unrecognized.", "Preserve the original database and pre-migration backup; do not infer execution authority from unvalidated records.")
	}
	if _, err := tx.ExecContext(ctx, scheduleSchema); err != nil {
		return storageError(err)
	}
	return nil
}

const scheduleSchema = `
CREATE INDEX schedule_due ON entities(json_extract(body,'$.definition.enabled'),json_extract(body,'$.next_run_at'),id) WHERE kind='schedule';
CREATE UNIQUE INDEX occurrence_position ON entities(json_extract(body,'$.schedule_id'),json_extract(body,'$.sequence')) WHERE kind='occurrence';
CREATE UNIQUE INDEX occurrence_cron_due ON entities(json_extract(body,'$.schedule_id'),json_extract(body,'$.due_at')) WHERE kind='occurrence' AND json_extract(body,'$.trigger')='cron';
CREATE INDEX occurrence_pending ON entities(json_extract(body,'$.state'),json_extract(body,'$.schedule_id'),json_extract(body,'$.sequence'),id) WHERE kind='occurrence';
CREATE INDEX session_schedule ON entities(json_extract(body,'$.schedule_origin.schedule_id'),id) WHERE kind='session';
ALTER TABLE worker_instances ADD COLUMN available_since INTEGER NOT NULL DEFAULT 0;
UPDATE worker_instances SET available_since=last_seen;
PRAGMA user_version=11;
`
