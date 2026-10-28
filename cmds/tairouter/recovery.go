// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func (a *Application) Recover(s *State) error {
	path := filepath.Join(a.Disk.Root, "pending.json")
	data, ok, e := readConfig(path)
	if e != nil {
		return e
	}
	if !ok {
		return errors.New("no unfinished settings transaction")
	}
	var pending Pending
	if json.Unmarshal(data, &pending) != nil || pending.Record.Client == "" || len(pending.Record.Files) == 0 || pending.Desired.Profiles == nil {
		return errors.New("invalid recovery record; preserve it for manual recovery")
	}
	pending, e = a.Disk.hydratePending(pending)
	if e != nil {
		return e
	}
	release, e := a.Disk.nativeLocks(pending.Locks)
	if e != nil {
		return e
	}
	defer release()
	if pending.Kind == TransactionRestore {
		return a.finishRestore(s, pending, path)
	}
	if pending.Kind != "" && pending.Kind != TransactionApply {
		return errors.New("unknown transaction kind; preserve journal")
	}
	record := pending.Record
	if statesEquivalent(*s, pending.Desired) {
		for _, b := range record.Files {
			current, exists, e := readConfig(b.Path)
			if e != nil || exists == pending.Absent[b.Path] || !bytes.Equal(current, b.After) {
				return ErrConflict
			}
		}
		return a.Disk.finishJournal(path)
	}

	for _, b := range record.Files {
		current, exists, e := readConfig(b.Path)
		if e != nil {
			return e
		}
		if exists == b.Existed && bytes.Equal(current, b.Before) {
			continue
		}
		if exists == pending.Absent[b.Path] || !bytes.Equal(current, b.After) {
			return ErrConflict
		}
	}
	if !a.Prompt.Confirm("Roll back the unfinished settings transaction using its protected backups?") {
		return errors.New("recovery declined")
	}
	for _, b := range record.Files {
		current, exists, e := readConfig(b.Path)
		if e != nil {
			return e
		}
		if exists == b.Existed && bytes.Equal(current, b.Before) {
			continue
		}
		if exists == pending.Absent[b.Path] || !bytes.Equal(current, b.After) {
			return ErrConflict
		}
		if b.Existed {
			e = a.Disk.write(b.Path, b.Before, compareFile(b.Path, b.After, !pending.Absent[b.Path]))
		} else if exists {
			e = os.Remove(b.Path)
		}
		if e != nil {
			return e
		}
	}
	return a.Disk.finishJournal(path)
}

// Restore recovery moves forward. Rolling backward could resurrect a revoked
// remote key in client settings after disconnect has already completed cleanup.
func (a *Application) finishRestore(s *State, p Pending, journal string) error {
	seen := map[string]bool{}
	for _, b := range p.Record.Files {
		if !filepath.IsAbs(b.Path) || seen[b.Path] {
			return errors.New("invalid restore journal")
		}
		seen[b.Path] = true
		current, exists, e := readConfig(b.Path)
		if e != nil {
			return e
		}
		before := exists == b.Existed && bytes.Equal(current, b.Before)
		after := (!p.Absent[b.Path] && exists && bytes.Equal(current, b.After)) || (p.Absent[b.Path] && !exists)
		if !before && !after {
			return ErrConflict
		}
	}
	for path := range p.Absent {
		if !seen[path] {
			return errors.New("invalid restore removal target")
		}
	}
	if !a.Prompt.Confirm("Complete the interrupted restoration? Unchanged pending files will be restored and completed files kept.") {
		return errors.New("recovery declined")
	}
	for _, b := range p.Record.Files {
		current, exists, e := readConfig(b.Path)
		if e != nil {
			return e
		}
		if p.Absent[b.Path] && !exists {
			continue
		}
		if !p.Absent[b.Path] && exists && bytes.Equal(current, b.After) {
			continue
		}
		if exists != b.Existed || !bytes.Equal(current, b.Before) {
			return ErrConflict
		}
		if p.Absent[b.Path] {
			if e = compareFile(b.Path, b.Before, b.Existed)(); e == nil {
				if exists {
					e = os.Remove(b.Path)
				}
			}
		} else {
			e = a.Disk.write(b.Path, b.After, compareFile(b.Path, b.Before, b.Existed))
		}
		if e != nil {
			return e
		}
	}
	// Disconnect may already have removed credentials/profile metadata while
	// this journal was pending. Merge only restoration ownership cleanup.
	desired := copyState(*s)
	delete(desired.Records, p.Record.Client)
	delete(desired.Active, p.Record.Client)
	if e := a.Disk.Save(desired); e != nil {
		return e
	}
	*s = desired
	return a.Disk.finishJournal(journal)
}

// Compare hydrated bytes directly instead of allocating a base64 encoding of
// potentially large native snapshots. Empty and nil collections are equivalent.
func statesEquivalent(a, b State) bool {
	if len(a.Profiles) != len(b.Profiles) || len(a.Active) != len(b.Active) || len(a.Records) != len(b.Records) {
		return false
	}
	for key, p := range a.Profiles {
		other, ok := b.Profiles[key]
		if !ok || p != other {
			return false
		}
	}
	for key, value := range a.Active {
		other, ok := b.Active[key]
		if !ok || value != other {
			return false
		}
	}
	sameBackup := func(x, y Backup) bool {
		return x.Path == y.Path && x.Existed == y.Existed && x.Removed == y.Removed && bytes.Equal(x.Before, y.Before) && bytes.Equal(x.After, y.After)
	}
	for key, r := range a.Records {
		other, ok := b.Records[key]
		if !ok || r.Profile != other.Profile || r.Client != other.Client || r.RuntimeMode != other.RuntimeMode || len(r.Files) != len(other.Files) || len(r.Originals) != len(other.Originals) {
			return false
		}
		for i, file := range r.Files {
			if !sameBackup(file, other.Files[i]) {
				return false
			}
		}
		for path, file := range r.Originals {
			value, ok := other.Originals[path]
			if !ok || !sameBackup(file, value) {
				return false
			}
		}
	}
	return true
}
