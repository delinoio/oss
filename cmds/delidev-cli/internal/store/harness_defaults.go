// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"log/slog"
	"time"
)

// UpgradeHarnessInheritance changes only current configuration documents in one
// transaction. Historical sessions, receipts and execution digests are untouched.
// The nonnil selection is the durable completion marker, including empty Agents.
func (s *Store) upgradeHarnessInheritance(ctx context.Context) error {
	sqltx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(err)
	}
	defer sqltx.Rollback()
	tx := &Tx{tx: sqltx, ctx: ctx, now: time.Now().UTC(), touched: map[domain.ID]bool{}, queueTouched: map[domain.ID]bool{}}
	after := domain.ID("")
	count := 0
	scanned := 0
	for {
		records, err := tx.List(Filter{Kind: domain.AgentKind, After: after, Limit: MaxPage})
		if err != nil {
			return err
		}
		for _, record := range records {
			scanned++
			if scanned > 10000 {
				return domain.Fail(domain.ResourceExhausted, "Harness configuration upgrade exceeds its bound.", "Preserve the configuration and reduce the Agent scope before retrying.")
			}
			agent, err := Decode[domain.Agent](record)
			if err != nil {
				return err
			}
			if agent.HarnessSelection == nil {
				agent.HarnessSelection = &domain.HarnessSelection{}
				agent.Effort = ""
				agent.Options = domain.AgentOptions{Permission: domain.PermissionDefault}
				for i := range agent.Routes {
					agent.Routes[i].ModelSelection = &domain.InheritedValue[domain.InlineModel]{State: domain.InheritValue}
				}
				// Legacy top-level routes retain their original source/account shape. Only
				// source-routed current Agents are executable under the current baseline.
				if _, err = tx.Put(domain.AgentKind, record.ID, record.Revision, record.SessionID, record.ProjectID, agent); err != nil {
					return err
				}
				count++
			}
			after = record.ID
		}
		if len(records) < MaxPage {
			break
		}
	}
	if err = sqltx.Commit(); err != nil {
		return storageError(err)
	}
	if count > 0 {
		slog.Info("harness_configuration_upgrade", "phase", "complete", "agents", count)
	}
	return nil
}
