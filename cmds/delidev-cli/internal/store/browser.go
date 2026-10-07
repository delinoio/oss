// SPDX-License-Identifier: Apache-2.0
package store

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"sort"
)

const MaxBrowserProfiles = 2000

func (t *Tx) browserDevice(id domain.ID) (Record, domain.Device, error) {
	r, err := t.Get(domain.DeviceKind, id)
	if err != nil {
		return r, domain.Device{}, err
	}
	d, err := Decode[domain.Device](r)
	if err != nil {
		return r, d, err
	}
	if len(d.BrowserProfiles) > MaxBrowserProfiles {
		return r, d, domain.Fail(domain.RecoveryRequired, "Browser profile inventory exceeds its bound.", "Preserve the original client metadata.")
	}
	accounts, ids := map[domain.ID]bool{}, map[domain.ID]bool{}
	for _, p := range d.BrowserProfiles {
		if err = p.Validate(); err != nil {
			return r, d, err
		}
		if accounts[p.Data.AccountID] || ids[p.ID] {
			return r, d, domain.Fail(domain.RecoveryRequired, "Browser profile ownership is inconsistent.", "Preserve the original client metadata.")
		}
		accounts[p.Data.AccountID] = true
		ids[p.ID] = true
	}
	return r, d, nil
}
func (t *Tx) BrowserProfile(device, id domain.ID) (domain.BrowserProfileRecord, error) {
	var after domain.ID
	for {
		rows, err := t.List(Filter{Kind: domain.DeviceKind, After: after, Limit: MaxPage})
		if err != nil {
			return domain.BrowserProfileRecord{}, err
		}
		for _, row := range rows {
			_, d, err := t.browserDevice(row.ID)
			if err != nil {
				return domain.BrowserProfileRecord{}, err
			}
			for _, profile := range d.BrowserProfiles {
				if profile.ID == id {
					return profile, nil
				}
			}
			after = row.ID
		}
		if len(rows) < MaxPage {
			break
		}
	}
	return domain.BrowserProfileRecord{}, domain.Fail(domain.NotFound, "The browser profile does not exist.", "Read the current browser profile inventory.")
}

func (t *Tx) FindBrowserProfile(device, account domain.ID) (domain.BrowserProfileRecord, error) {
	_, d, err := t.browserDevice(device)
	if err != nil {
		return domain.BrowserProfileRecord{}, err
	}
	for _, p := range d.BrowserProfiles {
		if p.Data.AccountID == account {
			return p, nil
		}
	}
	return domain.BrowserProfileRecord{}, domain.Fail(domain.NotFound, "No browser profile exists for this device and account.", "Register its current session profile.")
}
func (t *Tx) PutBrowserProfile(p domain.BrowserProfileRecord, expected uint64) (domain.BrowserProfileRecord, error) {
	if err := p.Validate(); err != nil {
		return p, err
	}
	r, d, err := t.browserDevice(p.Data.DeviceID)
	if err != nil {
		return p, err
	}
	found := -1
	for i, existing := range d.BrowserProfiles {
		if existing.ID == p.ID {
			found = i
			if existing.Revision != expected {
				return p, domain.Fail(domain.Conflict, "Browser profile ownership or revision changed.", "Read its original current state.")
			}
		} else if existing.Data.AccountID == p.Data.AccountID {
			return p, domain.Fail(domain.Conflict, "This account already has a device profile.", "Use its original identity.")
		}
	}
	if (found < 0 && expected != 0) || (found >= 0 && expected == 0) || expected == ^uint64(0) {
		return p, domain.Fail(domain.Conflict, "Browser profile revision changed.", "Read its original current state.")
	}
	p.Revision = expected + 1
	if found < 0 {
		if len(d.BrowserProfiles) >= MaxBrowserProfiles {
			return p, domain.Fail(domain.ResourceExhausted, "This device's profile inventory is full.", "Retain cleanup obligations and use existing profiles.")
		}
		d.BrowserProfiles = append(d.BrowserProfiles, p)
	} else {
		d.BrowserProfiles[found] = p
	}
	// The single-authority SQLite transaction serializes find/create and atomically
	// publishes the device event and original mutation receipt. No schema reservation
	// or shared EntityKind number is consumed by this additive document field.
	_, err = t.Put(domain.DeviceKind, r.ID, r.Revision, "", "", d)
	return p, err
}
func (t *Tx) BrowserProfiles(device, after domain.ID, limit int) ([]domain.BrowserProfileRecord, error) {
	var profiles []domain.BrowserProfileRecord
	var cursor domain.ID
	for {
		rows, err := t.List(Filter{Kind: domain.DeviceKind, After: cursor, Limit: MaxPage})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			_, d, err := t.browserDevice(row.ID)
			if err != nil {
				return nil, err
			}
			profiles = append(profiles, d.BrowserProfiles...)
			cursor = row.ID
		}
		if len(rows) < MaxPage {
			break
		}
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	out := make([]domain.BrowserProfileRecord, 0)
	for _, p := range profiles {
		if p.ID > after {
			out = append(out, p)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

// Account deletion freezes all original device obligations in its transaction.
// Revoked and offline devices are included; session lifecycles never call this.
func (t *Tx) RequireBrowserProfileRemoval(account domain.ID) error {
	var after domain.ID
	for {
		page, err := t.List(Filter{Kind: domain.DeviceKind, After: after, Limit: MaxPage})
		if err != nil {
			return err
		}
		for _, r := range page {
			_, d, err := t.browserDevice(r.ID)
			if err != nil {
				return err
			}
			changed := false
			for i, p := range d.BrowserProfiles {
				if p.Data.AccountID == account && p.Data.State == domain.BrowserProfileActive {
					if p.Revision == ^uint64(0) {
						return domain.Fail(domain.RecoveryRequired, "Browser profile revision cannot advance.", "Preserve the original cleanup metadata.")
					}
					p.Data.State = domain.BrowserProfileRemovalPending
					p.Data.DeletionRequestID = t.requestID
					p.Revision++
					d.BrowserProfiles[i] = p
					changed = true
				}
			}
			if changed {
				if _, err = t.Put(domain.DeviceKind, r.ID, r.Revision, "", "", d); err != nil {
					return err
				}
			}
			after = r.ID
		}
		if len(page) < MaxPage {
			return nil
		}
	}
}
func (t *Tx) BrowserCleanupCounts(account domain.ID) (active, pending, removed uint32, err error) {
	var after domain.ID
	for {
		page, e := t.List(Filter{Kind: domain.DeviceKind, After: after, Limit: MaxPage})
		if e != nil {
			return 0, 0, 0, e
		}
		for _, r := range page {
			_, d, e := t.browserDevice(r.ID)
			if e != nil {
				return 0, 0, 0, e
			}
			for _, p := range d.BrowserProfiles {
				if p.Data.AccountID != account {
					continue
				}
				switch p.Data.State {
				case domain.BrowserProfileActive:
					active++
				case domain.BrowserProfileRemovalPending:
					pending++
				case domain.BrowserProfileRemoved:
					removed++
				}
			}
			after = r.ID
		}
		if len(page) < MaxPage {
			return active, pending, removed, nil
		}
	}
}
