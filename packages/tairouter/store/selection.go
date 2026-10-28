// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"github.com/delinoio/oss/packages/tairouter/core"
	"time"
)

type SelectionStrategy string

const (
	SelectionPriority   SelectionStrategy = "priority"
	SelectionRoundRobin SelectionStrategy = "round-robin"
	SelectionLRU        SelectionStrategy = "least-recently-used"
	SelectionWeighted   SelectionStrategy = "weighted"
)

type SelectionCandidate struct {
	ID, Version      string
	Weight, Priority int
}
type SelectionRequest struct {
	GroupID, Version, Model, AffinityKey, PinnedID string
	Strategy                                       SelectionStrategy
	Candidates                                     []SelectionCandidate
	Exclude                                        []string
	AffinityTTL                                    time.Duration
}
type SelectionReceipt struct {
	ID, CandidateID         string
	Scope                   core.Scope
	GroupID, Version, Model string
}

func (s *Store) SelectAccount(ctx context.Context, l Lease, r SelectionRequest) (SelectionReceipt, error) {
	return s.selectCandidate(ctx, l, r, false)
}

// SelectRoute selects opaque route-target IDs. GroupID and Version must name the
// current private route, so stale routing snapshots cannot update its cursor.
func (s *Store) SelectRoute(ctx context.Context, l Lease, r SelectionRequest) (SelectionReceipt, error) {
	return s.selectCandidate(ctx, l, r, true)
}
func (s *Store) selectCandidate(ctx context.Context, l Lease, r SelectionRequest, route bool) (out SelectionReceipt, e error) {
	if r.GroupID == "" || r.Version == "" || len(r.Candidates) == 0 || len(r.Candidates) > 256 {
		return out, ErrConflict
	}
	if r.AffinityTTL == 0 {
		r.AffinityTTL = 24 * time.Hour
	}
	if r.AffinityTTL < 0 || r.AffinityTTL > 24*time.Hour {
		return out, ErrConflict
	}
	switch r.Strategy {
	case SelectionPriority, SelectionRoundRobin, SelectionLRU, SelectionWeighted:
	default:
		return out, ErrConflict
	}
	sc := l.Principal.Scope
	encoded, _ := json.Marshal([]string{r.GroupID, r.Version, r.Model})
	group := hex.EncodeToString(core.Hash(string(encoded)))
	excluded := map[string]bool{}
	for _, id := range r.Exclude {
		excluded[id] = true
	}
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkLease(ctx, tx, l); e != nil {
			return e
		}
		if route {
			if e := s.checkFlowTarget(ctx, tx, sc, "route", r.GroupID, r.Version); e != nil {
				return e
			}
		}
		type candidate struct {
			SelectionCandidate
			last, weight int64
		}
		var eligible []candidate
		seen := map[string]bool{}
		for _, c := range r.Candidates {
			if c.ID == "" || seen[c.ID] || c.Weight < 0 || c.Weight > 1000000 {
				return ErrConflict
			}
			seen[c.ID] = true
			if excluded[c.ID] {
				continue
			}
			if !route {
				if c.Version == "" {
					return ErrConflict
				}
				var valid bool
				if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM private_data d WHERE d.org_id=$1 AND d.user_id=$2 AND d.kind='provider' AND d.id=$3 AND d.version=$4 AND NOT EXISTS(SELECT 1 FROM private_refresh r WHERE r.org_id=d.org_id AND r.user_id=d.user_id AND r.kind=d.kind AND r.id=d.id AND r.version=d.version) AND NOT EXISTS(SELECT 1 FROM account_cooldowns c WHERE c.org_id=d.org_id AND c.user_id=d.user_id AND c.id=d.id AND c.version=d.version AND c.model IN ('',$5) AND c.until_at>clock_timestamp()))`, sc.OrganizationID, sc.UserID, c.ID, c.Version, r.Model).Scan(&valid); e != nil {
					return e
				}
				if !valid {
					continue
				}
			}
			if c.Weight == 0 {
				c.Weight = 1
			}
			var item candidate
			item.SelectionCandidate = c
			e := tx.QueryRowContext(ctx, "INSERT INTO selection_state(org_id,user_id,group_id,candidate_id,credential_version) VALUES($1,$2,$3,$4,$5) ON CONFLICT(org_id,user_id,group_id,candidate_id) DO UPDATE SET last_selected=CASE WHEN selection_state.credential_version=excluded.credential_version THEN selection_state.last_selected ELSE 0 END,current_weight=CASE WHEN selection_state.credential_version=excluded.credential_version THEN selection_state.current_weight ELSE 0 END,credential_version=excluded.credential_version RETURNING last_selected,current_weight", sc.OrganizationID, sc.UserID, group, c.ID, c.Version).Scan(&item.last, &item.weight)
			if e != nil {
				return e
			}
			eligible = append(eligible, item)
		}
		if !route && len(eligible) > 1 && r.PinnedID == "" {
			var preferred []candidate
			for _, c := range eligible {
				var denied bool
				if e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM account_soft_denials WHERE org_id=$1 AND user_id=$2 AND id=$3 AND version=$4 AND model=$5 AND until_at>clock_timestamp())", sc.OrganizationID, sc.UserID, c.ID, c.Version, r.Model).Scan(&denied); e != nil {
					return e
				}
				if !denied {
					preferred = append(preferred, c)
				}
			}
			if len(preferred) > 0 {
				eligible = preferred
			}
		}
		if r.PinnedID != "" {
			var pinned []candidate
			for _, c := range eligible {
				if c.ID == r.PinnedID {
					pinned = append(pinned, c)
				}
			}
			eligible = pinned
		}
		if len(eligible) == 0 {
			return ErrNotFound
		}
		selected := -1
		if r.AffinityKey != "" {
			var id string
			e := tx.QueryRowContext(ctx, "SELECT candidate_id FROM selection_affinity WHERE org_id=$1 AND user_id=$2 AND group_id=$3 AND affinity_hash=$4 AND expires_at>clock_timestamp()", sc.OrganizationID, sc.UserID, group, core.Hash(r.AffinityKey)).Scan(&id)
			if e != nil && e != sql.ErrNoRows {
				return e
			}
			for n, c := range eligible {
				if c.ID == id {
					selected = n
				}
			}
		}
		if selected < 0 {
			selected = 0
			switch r.Strategy {
			case SelectionPriority:
				for n, c := range eligible {
					if c.Priority < eligible[selected].Priority {
						selected = n
					}
				}
			case SelectionLRU:
				for n, c := range eligible {
					if c.last < eligible[selected].last {
						selected = n
					}
				}
			case SelectionRoundRobin:
				latest := -1
				var seq int64
				for n, c := range eligible {
					if c.last > seq {
						seq = c.last
						latest = n
					}
				}
				selected = (latest + 1) % len(eligible)
			case SelectionWeighted:
				var total int64
				for n := range eligible {
					eligible[n].weight += int64(eligible[n].Weight)
					total += int64(eligible[n].Weight)
					if eligible[n].weight > eligible[selected].weight {
						selected = n
					}
				}
				eligible[selected].weight -= total
				for _, c := range eligible {
					if _, e := tx.ExecContext(ctx, "UPDATE selection_state SET current_weight=$5 WHERE org_id=$1 AND user_id=$2 AND group_id=$3 AND candidate_id=$4", sc.OrganizationID, sc.UserID, group, c.ID, c.weight); e != nil {
						return e
					}
				}
			}
		}
		chosen := eligible[selected]
		if _, e := tx.ExecContext(ctx, "UPDATE selection_state SET last_selected=nextval('selection_sequence') WHERE org_id=$1 AND user_id=$2 AND group_id=$3 AND candidate_id=$4", sc.OrganizationID, sc.UserID, group, chosen.ID); e != nil {
			return e
		}
		if r.AffinityKey != "" {
			if _, e := tx.ExecContext(ctx, "INSERT INTO selection_affinity(org_id,user_id,group_id,affinity_hash,candidate_id,expires_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()+$6*interval '1 second') ON CONFLICT(org_id,user_id,group_id,affinity_hash) DO UPDATE SET candidate_id=excluded.candidate_id,expires_at=excluded.expires_at", sc.OrganizationID, sc.UserID, group, core.Hash(r.AffinityKey), chosen.ID, r.AffinityTTL.Seconds()); e != nil {
				return e
			}
		}
		out = SelectionReceipt{core.ID(), chosen.ID, sc, r.GroupID, r.Version, r.Model}
		return nil
	})
	return
}
func (s *Store) SetAccountCooldownVersion(ctx context.Context, l Lease, id, version, model string, until time.Time) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkLease(ctx, tx, l); e != nil {
			return e
		}
		sc := l.Principal.Scope
		if e := s.checkFlowTarget(ctx, tx, sc, "provider", id, version); e != nil {
			return e
		}
		var valid bool
		if e := tx.QueryRowContext(ctx, "SELECT $1::timestamptz<=clock_timestamp()+interval '32 days'", until).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return ErrConflict
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO account_cooldowns(org_id,user_id,id,version,model,until_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(org_id,user_id,id,model) DO UPDATE SET version=excluded.version,until_at=CASE WHEN account_cooldowns.version=excluded.version THEN GREATEST(account_cooldowns.until_at,excluded.until_at) ELSE excluded.until_at END", sc.OrganizationID, sc.UserID, id, version, model, until)
		return e
	})
}
func (s *Store) SetAccountCooldown(ctx context.Context, l Lease, id, model string, until time.Time) error {
	_, v, e := s.GetPrivateVersion(ctx, l.Principal.Scope, "provider", id)
	if e != nil {
		return e
	}
	return s.SetAccountCooldownVersion(ctx, l, id, v, model, until)
}
func (s *Store) AccountCooldown(ctx context.Context, sc core.Scope, id, model string) (until *time.Time, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if _, e := s.guard(ctx, tx, sc); e != nil {
			return e
		}
		return tx.QueryRowContext(ctx, "SELECT max(c.until_at) FROM account_cooldowns c JOIN private_data d ON d.org_id=c.org_id AND d.user_id=c.user_id AND d.kind=c.kind AND d.id=c.id AND d.version=c.version WHERE c.org_id=$1 AND c.user_id=$2 AND c.id=$3 AND c.model IN ('',$4) AND c.until_at>clock_timestamp()", sc.OrganizationID, sc.UserID, id, model).Scan(&until)
	})
	return
}

func (s *Store) SetAccountSoftDenial(ctx context.Context, l Lease, id, version, model string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkLease(ctx, tx, l); e != nil {
			return e
		}
		sc := l.Principal.Scope
		if e := s.checkFlowTarget(ctx, tx, sc, "provider", id, version); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO account_soft_denials(org_id,user_id,id,version,model,until_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()+interval '6 hours') ON CONFLICT(org_id,user_id,id,model) DO UPDATE SET version=excluded.version,until_at=excluded.until_at", sc.OrganizationID, sc.UserID, id, version, model)
		return e
	})
}

// AccountSoftDenied reads exact-model evidence before native priority narrowing.
// Callers restore the original eligible set if every candidate is softly denied.
func (s *Store) AccountSoftDenied(ctx context.Context, l Lease, id, version, model string) (denied bool, e error) {
	e = s.tx(ctx, func(tx *sql.Tx) error {
		if e := s.checkNativeAccount(ctx, tx, l, id, version); e != nil {
			return e
		}
		sc := l.Principal.Scope
		return tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM account_soft_denials WHERE org_id=$1 AND user_id=$2 AND id=$3 AND version=$4 AND model=$5 AND until_at>clock_timestamp())", sc.OrganizationID, sc.UserID, id, version, model).Scan(&denied)
	})
	return
}
