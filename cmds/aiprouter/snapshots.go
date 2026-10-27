// SPDX-License-Identifier: Apache-2.0
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
)

const MaxNativeCatalogBytes = 256 << 20
const snapshotThreshold = 64 << 10

var snapshotName = regexp.MustCompile(`^[a-f0-9]{64}\.blob$`)

func configSizeLimit(path string) int {
	switch filepath.Base(path) {
	case "aiprouter-catalog.json", "models_cache.json":
		return MaxNativeCatalogBytes
	}
	if filepath.Base(filepath.Dir(path)) == "snapshots" && snapshotName.MatchString(filepath.Base(path)) {
		return MaxNativeCatalogBytes
	}
	return MaxConfigBytes
}
func snapshotDigest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func (d Disk) snapshotPath(ref string) (string, error) {
	if !snapshotName.MatchString(ref + ".blob") {
		return "", errors.New("invalid protected snapshot reference")
	}
	return filepath.Join(d.Root, "snapshots", ref+".blob"), nil
}
func (d Disk) storeSnapshot(b []byte) (string, error) {
	if len(b) > MaxNativeCatalogBytes {
		return "", errors.New("protected snapshot exceeds native catalog limit")
	}
	ref := snapshotDigest(b)
	path, _ := d.snapshotPath(ref)
	existing, ok, e := readConfig(path)
	if e != nil {
		return "", e
	}
	if ok {
		if snapshotDigest(existing) != ref {
			return "", errors.New("protected snapshot integrity conflict")
		}
		if e = d.Protection.Protect(filepath.Dir(path), true); e != nil {
			return "", e
		}
		if e = d.Protection.Protect(path, false); e != nil {
			return "", e
		}
		return ref, nil
	}
	root := filepath.Dir(path)
	if e = os.MkdirAll(root, 0700); e != nil {
		return "", e
	}
	if e = d.Protection.Protect(root, true); e != nil {
		return "", e
	}
	if e = d.write(path, b, compareFile(path, nil, false)); e != nil {
		return "", e
	}
	return ref, nil
}
func (d Disk) encodeBackup(b Backup, threshold int) (Backup, error) {
	for _, field := range []struct {
		data *[]byte
		ref  *string
	}{{&b.Before, &b.BeforeSnapshot}, {&b.After, &b.AfterSnapshot}} {
		if len(*field.data) > threshold || *field.ref != "" {
			ref, e := d.storeSnapshot(*field.data)
			if e != nil {
				return Backup{}, e
			}
			*field.ref = ref
			*field.data = nil
		}
	}
	return b, nil
}
func (d Disk) encodeRecord(r Record, threshold int) (Record, error) {
	out := r
	out.Files = make([]Backup, len(r.Files))
	out.Originals = make(map[string]Backup, len(r.Originals))
	for i, b := range r.Files {
		encoded, e := d.encodeBackup(b, threshold)
		if e != nil {
			return Record{}, e
		}
		out.Files[i] = encoded
	}
	for path, b := range r.Originals {
		encoded, e := d.encodeBackup(b, threshold)
		if e != nil {
			return Record{}, e
		}
		out.Originals[path] = encoded
	}
	return out, nil
}
func (d Disk) encodeState(s State, threshold int) (State, error) {
	out := s
	out.Records = make(map[string]Record, len(s.Records))
	for key, r := range s.Records {
		encoded, e := d.encodeRecord(r, threshold)
		if e != nil {
			return State{}, e
		}
		out.Records[key] = encoded
	}
	return out, nil
}
func (d Disk) marshalState(s State) ([]byte, error) {
	for _, threshold := range []int{snapshotThreshold, 0} {
		encoded, e := d.encodeState(s, threshold)
		if e != nil {
			return nil, e
		}
		b, e := json.MarshalIndent(encoded, "", "  ")
		if e != nil {
			return nil, e
		}
		if len(b) <= MaxConfigBytes {
			return b, nil
		}
	}
	return nil, errors.New("protected state metadata exceeds limit")
}
func (d Disk) marshalPending(p Pending) ([]byte, error) {
	for _, threshold := range []int{snapshotThreshold, 0} {
		encoded := p
		var e error
		encoded.Record, e = d.encodeRecord(p.Record, threshold)
		if e != nil {
			return nil, e
		}
		encoded.Desired, e = d.encodeState(p.Desired, threshold)
		if e != nil {
			return nil, e
		}
		b, e := json.Marshal(encoded)
		if e != nil {
			return nil, e
		}
		if len(b) <= MaxConfigBytes {
			return b, nil
		}
	}
	return nil, errors.New("protected transaction metadata exceeds limit")
}
func (d Disk) hydrateBackup(b Backup, cache map[string][]byte) (Backup, error) {
	for _, field := range []struct {
		data *[]byte
		ref  *string
	}{{&b.Before, &b.BeforeSnapshot}, {&b.After, &b.AfterSnapshot}} {
		if *field.ref == "" {
			continue
		}
		if len(*field.data) != 0 {
			return Backup{}, errors.New("ambiguous protected snapshot")
		}
		data, cached := cache[*field.ref]
		if !cached {
			path, e := d.snapshotPath(*field.ref)
			if e != nil {
				return Backup{}, e
			}
			value, ok, e := readConfig(path)
			if e != nil {
				return Backup{}, e
			}
			if !ok || snapshotDigest(value) != *field.ref {
				return Backup{}, errors.New("protected snapshot missing or corrupt; preserve recovery records")
			}
			data = value
			cache[*field.ref] = data
		}
		*field.data = data
		// References belong to the disk encoding, not mutable in-memory snapshots.
		*field.ref = ""
	}
	return b, nil
}
func (d Disk) hydrateRecord(r Record, cache map[string][]byte) (Record, error) {
	for i, b := range r.Files {
		value, e := d.hydrateBackup(b, cache)
		if e != nil {
			return Record{}, e
		}
		r.Files[i] = value
	}
	for path, b := range r.Originals {
		value, e := d.hydrateBackup(b, cache)
		if e != nil {
			return Record{}, e
		}
		r.Originals[path] = value
	}
	return r, nil
}
func (d Disk) hydrateState(s State) (State, error) {
	return d.hydrateStateCached(s, map[string][]byte{})
}
func (d Disk) hydrateStateCached(s State, cache map[string][]byte) (State, error) {
	for key, r := range s.Records {
		value, e := d.hydrateRecord(r, cache)
		if e != nil {
			return State{}, e
		}
		s.Records[key] = value
	}
	return s, nil
}
func (d Disk) hydratePending(p Pending) (Pending, error) {
	var e error
	cache := map[string][]byte{}
	p.Record, e = d.hydrateRecord(p.Record, cache)
	if e != nil {
		return Pending{}, e
	}
	p.Desired, e = d.hydrateStateCached(p.Desired, cache)
	return p, e
}

// Collect only after durable state/journal changes. Both documents retain their
// references until recovery commits, so interrupted writes never lose backups.
func (d Disk) collectSnapshots() error {
	keep := map[string]bool{}
	recordRefs := func(r Record) {
		for _, b := range r.Files {
			keep[b.BeforeSnapshot] = true
			keep[b.AfterSnapshot] = true
		}
		for _, b := range r.Originals {
			keep[b.BeforeSnapshot] = true
			keep[b.AfterSnapshot] = true
		}
	}
	stateRefs := func(s State) {
		for _, r := range s.Records {
			recordRefs(r)
		}
	}
	raw, ok, e := readConfig(filepath.Join(d.Root, "state.json"))
	if e != nil {
		return e
	}
	if ok {
		var s State
		if json.Unmarshal(raw, &s) != nil || s.Profiles == nil || s.Active == nil || s.Records == nil {
			return errors.New("cannot collect snapshots with invalid state")
		}
		stateRefs(s)
	}
	raw, ok, e = readConfig(filepath.Join(d.Root, "pending.json"))
	if e != nil {
		return e
	}
	if ok {
		var p Pending
		if json.Unmarshal(raw, &p) != nil || p.Record.Client == "" || p.Desired.Profiles == nil || p.Desired.Records == nil {
			return errors.New("cannot collect snapshots with invalid journal")
		}
		recordRefs(p.Record)
		stateRefs(p.Desired)
	}
	for ref := range keep {
		if ref == "" {
			continue
		}
		path, e := d.snapshotPath(ref)
		if e != nil {
			return e
		}
		data, ok, e := readConfig(path)
		if e != nil {
			return e
		}
		if !ok || snapshotDigest(data) != ref {
			return errors.New("cannot collect snapshots while a referenced backup is missing or corrupt")
		}
	}
	root := filepath.Join(d.Root, "snapshots")
	if e = safeParents(root); e != nil {
		return e
	}
	entries, e := os.ReadDir(root)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	for _, entry := range entries {
		name := entry.Name()
		if !snapshotName.MatchString(name) || keep[name[:64]] {
			continue
		}
		path := filepath.Join(root, name)
		data, ok, e := readConfig(path)
		if e != nil {
			return e
		}
		if !ok {
			continue
		}
		if snapshotDigest(data) != name[:64] {
			return errors.New("unreferenced protected snapshot is corrupt; preserve for inspection")
		}
		if e = compareFile(path, data, true)(); e != nil {
			return e
		}
		if e = os.Remove(path); e != nil {
			return e
		}
	}
	return nil
}
func (d Disk) finishJournal(path string) error {
	if e := os.Remove(path); e != nil {
		return e
	}
	return d.collectSnapshots()
}
