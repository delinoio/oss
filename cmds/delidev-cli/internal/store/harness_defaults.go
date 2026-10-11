// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Upgrade only current configuration documents. Never touch execution records,
// assignments, receipts, checkpoints or earlier unsupported database layouts.
// The absent typed selection is the idempotency marker inside this transaction.
func upgradeHarnessDefaults(ctx context.Context, db *sql.DB) error {
	sqlTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(err)
	}
	defer sqlTx.Rollback()
	t := &Tx{tx: sqlTx, ctx: ctx, now: time.Now().UTC(), touched: map[domain.ID]bool{}, requestID: domain.NewID()}
	after := domain.ID("")
	count := 0
	for {
		records, err := t.List(Filter{Kind: domain.AgentKind, After: after, Limit: MaxPage})
		if err != nil {
			return err
		}
		for _, record := range records {
			after = record.ID
			agent, err := Decode[domain.Agent](record)
			if err != nil {
				return err
			}
			if agent.HarnessSettings != nil {
				continue
			}
			agent.HarnessSettings = domain.InheritHarnessSelection()
			for i := range agent.Routes {
				agent.Routes[i].ModelMode = domain.SettingInherit
			}
			if err := agent.Validate(); err != nil {
				return err
			}
			if _, err = t.Put(domain.AgentKind, record.ID, record.Revision, record.SessionID, record.ProjectID, agent); err != nil {
				return err
			}
			count++
		}
		if len(records) < MaxPage {
			break
		}
	}
	if err := sqlTx.Commit(); err != nil {
		return storageError(err)
	}
	if count > 0 {
		slog.Info("harness_default_documents_upgraded", "agents", count)
	}
	return nil
}
