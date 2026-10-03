// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Only a private restore candidate has historical ownership without local
// native authority. Live startup must settle every original external owner.
type historicalSubscriptionRetirement struct{}

func migration028(ctx context.Context, tx *sql.Tx, original int) error {
	rows, err := tx.QueryContext(ctx, "SELECT "+recordColumns+" FROM entities ORDER BY id LIMIT 100001")
	if err != nil {
		return storageError(err)
	}
	records := []Record{}
	for rows.Next() {
		r, e := scan(rows)
		if e != nil {
			rows.Close()
			return storageError(e)
		}
		records = append(records, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return storageError(err)
	}
	if len(records) > 100000 {
		return domain.Fail(domain.ResourceExhausted, "Subscription retirement exceeds its configuration bound.", "Preserve the backup and reduce the configuration scope before migration.")
	}
	retired, accounts, providers, affectedAgents := map[domain.ID]bool{}, map[domain.ID]bool{}, map[domain.ID]bool{}, map[domain.ID]bool{}
	for _, r := range records {
		var fields map[string]json.RawMessage
		if json.Unmarshal(r.Data, &fields) != nil {
			return corrupt()
		}
		var typ domain.AccountType
		var protocol domain.APIProtocol
		var provider domain.ID
		var service domain.SubscriptionService
		json.Unmarshal(fields["type"], &typ)
		json.Unmarshal(fields["protocol"], &protocol)
		json.Unmarshal(fields["provider_id"], &provider)
		json.Unmarshal(fields["subscription_service"], &service)
		if r.Kind == domain.AccountKind && typ == domain.SubscriptionAccount && (provider != "" || !service.Valid()) {
			retired[r.ID], accounts[r.ID] = true, true
		}
		if r.Kind == domain.ProviderKind && protocol == domain.NativeSubscription {
			retired[r.ID], providers[r.ID] = true, true
		}
	}
	for _, r := range records {
		if r.Kind != domain.ModelKind && r.Kind != domain.AccountKind {
			continue
		}
		var fields struct {
			ProviderID domain.ID          `json:"provider_id"`
			Type       domain.AccountType `json:"type"`
		}
		if json.Unmarshal(r.Data, &fields) != nil {
			return corrupt()
		}
		if providers[fields.ProviderID] {
			if r.Kind == domain.AccountKind && fields.Type != domain.SubscriptionAccount {
				return domain.SubscriptionReconfigurationRequired()
			}
			retired[r.ID] = true
			if r.Kind == domain.AccountKind {
				accounts[r.ID] = true
			}
		}
	}
	if len(retired) > 10000 {
		return domain.Fail(domain.ResourceExhausted, "Legacy subscription configuration exceeds its retirement bound.", "Preserve the original backup and reduce configuration before upgrading.")
	}
	accountIDs := make([]domain.ID, 0, len(accounts))
	for id := range accounts {
		accountIDs = append(accountIDs, id)
	}
	accountSet, _ := json.Marshal(accountIDs)
	historical, _ := ctx.Value(historicalSubscriptionRetirement{}).(bool)
	if !historical {
		for _, r := range records {
			if accounts[r.ID] {
				var a domain.Account
				if domain.Decode(r.Data, &a) != nil {
					return corrupt()
				}
				if a.Connection != nil || a.Removal != nil || a.Health != domain.AccountDisconnected || a.Subscription != nil && (a.Subscription.Generation != "" || a.Subscription.Pending != nil || a.Subscription.Lease != nil || a.Subscription.RecoveryRequired || a.Subscription.IdentityCommitment != "") {
					return retirementOwnershipRequired()
				}
			}
			if r.Kind == domain.DeviceKind {
				var d domain.Device
				if domain.Decode(r.Data, &d) != nil {
					return corrupt()
				}
				for _, p := range d.BrowserProfiles {
					if accounts[p.Data.AccountID] && p.Data.State != domain.BrowserProfileRemoved {
						return retirementOwnershipRequired()
					}
				}
			}
			if r.Kind == domain.SessionKind || r.Kind == domain.JobKind {
				// JSON tree comparison uses original account references, never text or a
				// guessed service. It covers immutable and successor selections alike.
				var affected, unsettled bool
				if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM json_tree(?) WHERE key IN ('account_id','initial_account_id','selected_account_id') AND value IN (SELECT value FROM json_each(?)))", r.Data, accountSet).Scan(&affected); err != nil {
					return storageError(err)
				}
				if affected {
					if err := tx.QueryRowContext(ctx, `SELECT COALESCE(json_extract(?,'$.state') IN ('claimed','uncertain'),0) OR COALESCE(json_extract(?,'$.active_execution_id'),'')<>'' OR COALESCE(json_extract(?,'$.outcome')='running',0) OR COALESCE(json_extract(?,'$.recovery') IN ('required','reconciling'),0) OR COALESCE(json_extract(?,'$.archive')='archiving',0) OR COALESCE(json_extract(?,'$.preparation.state') IN ('stopping','uncertain'),0)`, r.Data, r.Data, r.Data, r.Data, r.Data, r.Data).Scan(&unsettled); err != nil {
						return storageError(err)
					}
					if unsettled {
						return retirementOwnershipRequired()
					}
				}
			}
		}
	}
	if _, err := tx.ExecContext(ctx, subscriptionIdentitySchema); err != nil {
		return storageError(err)
	}
	now := time.Now().UTC()
	mutation := &Tx{ctx: ctx, tx: tx, now: now, touched: map[domain.ID]bool{}}
	for _, r := range records {
		if retired[r.ID] {
			if _, err := tx.ExecContext(ctx, "INSERT INTO retired_configurations("+recordColumns+") VALUES(?,?,?,?,?,?,?,?)", r.ID, r.Kind, r.Revision, r.SessionID, r.ProjectID, r.Data, r.CreatedAt.UnixMilli(), r.UpdatedAt.UnixMilli()); err != nil {
				return storageError(err)
			}
		}
		if r.Kind == domain.AgentKind {
			var a domain.Agent
			if domain.Decode(r.Data, &a) != nil {
				return corrupt()
			}
			links := make([]domain.WeightedAccount, 0, len(a.Accounts))
			changed := retired[a.ModelID]
			for _, link := range a.Accounts {
				if accounts[link.ID] {
					changed = true
				} else {
					links = append(links, link)
				}
			}
			if changed {
				affectedAgents[r.ID] = true
				if err := retirementUpdate(mutation, r, map[string]any{"accounts": links, "reconfiguration_required": true}); err != nil {
					return err
				}
			}
		}
		if r.Kind == domain.ProjectKind {
			var p domain.Project
			if domain.Decode(r.Data, &p) != nil {
				return corrupt()
			}
			ids := make([]domain.ID, 0, len(p.Accounts.IDs))
			changed := false
			for _, id := range p.Accounts.IDs {
				if accounts[id] {
					changed = true
				} else {
					ids = append(ids, id)
				}
			}
			if changed {
				p.Accounts.IDs = ids
				if err := retirementUpdate(mutation, r, map[string]any{"accounts": p.Accounts}); err != nil {
					return err
				}
			}
		}
	}
	for _, r := range records {
		if r.Kind == domain.ScheduleKind {
			var s domain.Schedule
			if domain.Decode(r.Data, &s) != nil {
				return corrupt()
			}
			if affectedAgents[s.Definition.AgentID] {
				if s.ConfigurationRevision >= 1<<63-1 {
					return corrupt()
				}
				s.Definition.Enabled = false
				s.NextRunAt = nil
				s.ConfigurationRevision++
				if err := retirementUpdate(mutation, r, map[string]any{"definition": s.Definition, "next_run_at": nil, "configuration_revision": s.ConfigurationRevision, "problem": domain.SubscriptionReconfigurationRequired()}); err != nil {
					return err
				}
			}
		}
		if retired[r.ID] {
			if _, err := tx.ExecContext(ctx, "INSERT INTO tombstones(id,kind,created_at) VALUES(?,?,?)", r.ID, r.Kind, now.UnixMilli()); err != nil {
				return storageError(err)
			}
			redacted, _ := json.Marshal(map[string]any{"deleted": true, "id": r.ID})
			if _, err := tx.ExecContext(ctx, "UPDATE receipts SET result="+deletedReceiptProjection+" WHERE id IN (SELECT request_id FROM receipt_entities WHERE entity_id=?)", redacted, r.ID); err != nil {
				return storageError(err)
			}
			if err := mutation.event(r, Deleted); err != nil {
				return err
			}
		}
	}
	// Snapshot and usage bodies have no cascading dependency on configuration.
	// Only live catalog suppressions reference providers and must disappear first.
	if _, err := tx.ExecContext(ctx, "DELETE FROM model_suppressions WHERE provider_id IN (SELECT id FROM retired_configurations WHERE kind='provider'); DELETE FROM entities WHERE id IN (SELECT id FROM retired_configurations)"); err != nil {
		return storageError(err)
	}
	return nil
}

func retirementOwnershipRequired() error {
	return domain.Fail(domain.RecoveryRequired, "Legacy subscriptions still own protected or native resources.", "Use the previous server to logout, settle original execution and browser cleanup, then retry migration. The backup and original database are preserved.")
}

func retirementUpdate(t *Tx, r Record, changes map[string]any) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(r.Data, &fields) != nil || r.Revision >= 1<<63-1 {
		return corrupt()
	}
	for key, value := range changes {
		raw, err := json.Marshal(value)
		if err != nil {
			return storageError(err)
		}
		fields[key] = raw
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return storageError(err)
	}
	if _, err := t.tx.ExecContext(t.ctx, "UPDATE entities SET body=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?", raw, t.now.UnixMilli(), r.ID, r.Revision); err != nil {
		return storageError(err)
	}
	r.Revision++
	r.Data = raw
	r.UpdatedAt = t.now
	return t.event(r, Updated)
}

const subscriptionIdentitySchema = `
ALTER TABLE pricing_versions ADD COLUMN subscription_service TEXT NOT NULL DEFAULT '';
CREATE TABLE retired_configurations (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK(kind IN ('account','provider','model')),
 revision INTEGER NOT NULL CHECK(revision>0), session_id TEXT NOT NULL, project_id TEXT NOT NULL,
 body BLOB NOT NULL CHECK(length(body)<=1048576), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX subscription_model_canonical ON entities(json_extract(body,'$.subscription_service'),json_extract(body,'$.native_id')) WHERE kind='model' AND json_extract(body,'$.source_kind')='subscription';
INSERT INTO metadata(key,value) VALUES('subscription_identity_layout','service-accounts-v2');
`
