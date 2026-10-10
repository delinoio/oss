// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Upgrade before publishing the Store: one transaction covers every live Agent.
// A restart sees either all original selections or the completed typed upgrade.
// Sessions, immutable assignments, receipts and native history are never edited.
func upgradeHarnessAgents(ctx context.Context, db *sql.DB) error {
	sqlTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(err)
	}
	defer sqlTx.Rollback()
	transaction := &Tx{tx: sqlTx, ctx: ctx, now: time.Now().UTC().Truncate(time.Millisecond), touched: map[domain.ID]bool{}}
	rows, err := sqlTx.QueryContext(ctx, "SELECT "+recordColumns+" FROM entities WHERE kind='agent' ORDER BY id LIMIT 10001")
	if err != nil {
		return storageError(err)
	}
	records := []Record{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			rows.Close()
			return err
		}
		records = append(records, r)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return storageError(err)
	}
	if closeErr != nil {
		return storageError(closeErr)
	}
	if len(records) > 10000 {
		return domain.Fail(domain.ResourceExhausted, "Agent inheritance upgrade exceeds its bound.", "Preserve the original configuration and reduce the live Agent inventory before upgrading.")
	}
	count := 0
	for _, record := range records {
		raw, changed, err := domain.UpgradeHarnessAgent(record.Data)
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		if _, err = transaction.Put(domain.AgentKind, record.ID, record.Revision, record.SessionID, record.ProjectID, raw); err != nil {
			return err
		}
		count++
	}
	if err := sqlTx.Commit(); err != nil {
		return storageError(err)
	}
	if count > 0 {
		slog.Info("harness_agent_inheritance_upgraded", "agents", count, "phase", "committed")
	}
	return nil
}
