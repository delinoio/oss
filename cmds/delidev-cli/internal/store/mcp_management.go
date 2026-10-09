// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// MCPMetadata is a content-free coordination fence. It owns no Worker secrets
// or native process and shares the existing transaction/backup metadata table.
type MCPMetadata struct {
	MachineID           domain.ID `json:"machine_id"`
	DeviceID            domain.ID `json:"device_id"`
	DefinitionID        domain.ID `json:"definition_id"`
	Revision            uint64    `json:"revision,string"`
	Enabled             bool      `json:"enabled"`
	AuthenticationReady bool      `json:"authentication_ready"`
	PendingID           domain.ID `json:"pending_id,omitempty"`
	PendingActor        domain.ID `json:"pending_actor,omitempty"`
	PendingDigest       string    `json:"pending_digest,omitempty"`
	Deleted             bool      `json:"deleted"`
}

func (t *Tx) MCPMetadata(machine, definition domain.ID) (MCPMetadata, bool, error) {
	var v MCPMetadata
	var raw string
	e := t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", "mcp-management:"+string(machine)+":"+string(definition)).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return v, false, nil
	}
	if e != nil {
		return v, false, storageError(e)
	}
	if len(raw) > 4096 || domain.Decode([]byte(raw), &v) != nil || v.MachineID != machine || v.DefinitionID != definition || v.DeviceID.Validate() != nil {
		return v, false, domain.Fail(domain.RecoveryRequired, "Invalid MCP coordination metadata.", "Retain original Worker catalog evidence for recovery.")
	}
	return v, true, nil
}
func (t *Tx) PutMCPMetadata(v MCPMetadata) error {
	if e := t.writeAllowed(); e != nil {
		return e
	}
	if v.MachineID.Validate() != nil || v.DefinitionID.Validate() != nil || v.DeviceID.Validate() != nil {
		return domain.Fail(domain.InvalidArgument, "Invalid MCP owner.", "Use the paired Worker and definition identity.")
	}
	key := "mcp-management:" + string(v.MachineID) + ":" + string(v.DefinitionID)
	var existing int
	if e := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM metadata WHERE key=?", key).Scan(&existing); e != nil {
		return storageError(e)
	}
	if existing == 0 {
		var count int
		if e := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM metadata WHERE key LIKE ?", "mcp-management:"+string(v.MachineID)+":%").Scan(&count); e != nil {
			return storageError(e)
		}
		if count >= 4096 {
			return domain.Fail(domain.ResourceExhausted, "The original Worker MCP metadata ledger is full.", "Preserve original receipts and reconcile existing definitions before adding another identity.")
		}
	}
	raw, e := json.Marshal(v)
	if e != nil || len(raw) > 4096 {
		return domain.Fail(domain.InvalidArgument, "Invalid MCP metadata.", "Use bounded metadata.")
	}
	_, e = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "mcp-management:"+string(v.MachineID)+":"+string(v.DefinitionID), string(raw))
	return storageError(e)
}
func (t *Tx) MCPReferences(machine, definition domain.ID) ([]domain.ID, error) {

	ids := []domain.ID{}
	filter := Filter{Kind: domain.AgentKind, Limit: MaxPage}
	for {
		rows, e := t.List(filter)
		if e != nil {
			return nil, e
		}
		for _, row := range rows {
			agent, e := Decode[domain.Agent](row)
			if e != nil {
				return nil, e
			}
			if agent.MCPSelections != nil && !agent.MCPSelections.RebindingRequired {
				for _, selection := range agent.MCPSelections.Selections {
					if selection.MachineID == machine && selection.ServerID == definition {
						ids = append(ids, row.ID)
						if len(ids) > 1000 {
							return nil, domain.Fail(domain.ResourceExhausted, "MCP Agent reference count exceeds its bound.", "Explicitly reduce references before deletion.")
						}
						break
					}
				}
			}
		}
		if len(rows) < MaxPage {
			break
		}
		filter.After = rows[len(rows)-1].ID
	}

	return ids, nil
}

// UpdateMCPMetadata serializes Worker admission and Agent references in the same
// database transaction. Its values contain no credential or callback bytes.
func (s *Store) UpdateMCPMetadata(ctx context.Context, apply func(*Tx) error) error {
	s.gate.RLock()
	defer s.gate.RUnlock()
	if s.deletionFault {
		return domain.SessionDeletionPending()
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return storageError(e)
	}
	defer tx.Rollback()
	t := &Tx{tx: tx, ctx: ctx}
	if e = t.Authorize(); e != nil {
		return e
	}
	if e = apply(t); e != nil {
		return e
	}
	return storageError(tx.Commit())
}
