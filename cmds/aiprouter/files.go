// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Protector interface{ Protect(string, bool) error }
type NativeProtector struct{}
type Disk struct {
	Root       string
	Protection Protector
}
type State struct {
	Profiles map[string]Profile `json:"profiles"`
	Active   map[string]string  `json:"active"`
	Records  map[string]Record  `json:"records"`
}
type Record struct {
	RuntimeMode     NativeCompatibilityMode `json:"runtimeMode,omitempty"`
	Profile, Client string
	Files           []Backup
	Originals       map[string]Backup `json:"originals,omitempty"`
}
type Backup struct {
	BeforeSnapshot string `json:"beforeSnapshot,omitempty"`
	AfterSnapshot  string `json:"afterSnapshot,omitempty"`
	Removed        bool   `json:"removed,omitempty"`
	Path           string
	Before, After  []byte
	Existed        bool
}
type Change struct {
	RuntimeMode   NativeCompatibilityMode
	Remove        bool
	Check         func() error
	Locks         []string
	Path          string
	Before, After []byte
	Existed       bool
	Fields        []string
	RemoteKey     bool
}

func emptyState() State {
	return State{Profiles: map[string]Profile{}, Active: map[string]string{}, Records: map[string]Record{}}
}
func (d Disk) init() error {
	if !filepath.IsAbs(d.Root) {
		return errors.New("state directory must be absolute")
	}
	if err := safeParents(d.Root); err != nil {
		return err
	}
	if err := os.MkdirAll(d.Root, 0700); err != nil {
		return err
	}
	return d.Protection.Protect(d.Root, true)
}
func safeParents(path string) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		s, e := os.Lstat(p)
		if e == nil && s.Mode()&os.ModeSymlink != 0 {
			return errors.New("symbolic links are not accepted in managed paths")
		}
		if e != nil && !os.IsNotExist(e) {
			return errors.New("cannot inspect managed path")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
func readConfig(path string) ([]byte, bool, error) {
	return readConfigLimit(path, configSizeLimit(path))
}
func readNativeCatalog(path string) ([]byte, bool, error) {
	return readConfigLimit(path, MaxNativeCatalogBytes)
}
func readConfigLimit(path string, limit int) ([]byte, bool, error) {
	if err := safeParents(path); err != nil {
		return nil, false, err
	}
	s, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil || !s.Mode().IsRegular() || s.Size() > int64(limit) {
		return nil, false, errors.New("configuration must be a readable regular file within its size limit")
	}
	f, err := openConfig(path)
	if err != nil {
		return nil, false, errors.New("cannot open configuration")
	}
	defer f.Close()
	if e := validateLinks(f); e != nil {
		return nil, false, e
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(s, opened) {
		return nil, false, ErrConflict
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil || len(b) > limit {
		return nil, false, errors.New("cannot read bounded configuration")
	}
	current, e := os.Lstat(path)
	if e != nil || !os.SameFile(s, current) {
		return nil, false, ErrConflict
	}
	return b, true, nil
}
func (d Disk) write(path string, b []byte, validators ...func() error) error {
	if len(b) > configSizeLimit(path) {
		return errors.New("configuration exceeds its size limit")
	}
	if err := safeParents(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".aiprouter-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = d.Protection.Protect(tmp, false); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = safeParents(path); err != nil {
		return err
	}
	for _, validate := range validators {
		if e := validate(); e != nil {
			return e
		}
	}
	return os.Rename(tmp, path)
}
func (d Disk) Load() (State, error) {
	if err := d.init(); err != nil {
		return State{}, err
	}
	b, exists, err := readConfig(filepath.Join(d.Root, "state.json"))
	if err != nil {
		return State{}, err
	}
	s := emptyState()
	if !exists {
		return s, nil
	}
	if json.Unmarshal(b, &s) != nil || s.Profiles == nil || s.Active == nil || s.Records == nil {
		return State{}, errors.New("state is invalid; preserve it for recovery")
	}
	return d.hydrateState(s)
}
func (d Disk) Save(s State) error {
	b, e := d.marshalState(s)
	if e != nil {
		return e
	}
	return d.write(filepath.Join(d.Root, "state.json"), b)
}
func (d Disk) Lock() (func(), error) {
	if err := d.init(); err != nil {
		return nil, err
	}
	path := filepath.Join(d.Root, "write.lock")
	if e := safeParents(path); e != nil {
		return nil, e
	}
	lock, e := acquireProcessLock(path)
	if e != nil {
		return nil, errors.New("another CLI operation is active or the process lock is unavailable")
	}
	if e = d.Protection.Protect(path, false); e != nil {
		lock.Close()
		return nil, e
	}
	// The OS releases this advisory lock when the process exits, including a
	// crash. Keep its directory entry stable so competing openers share it.
	return func() { _ = lock.Close() }, nil
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Preview lists field names and fingerprints, never source values. Existing
// settings and backups can contain arbitrary secrets we cannot enumerate.
func Preview(out io.Writer, changes []Change) {
	for _, c := range changes {
		fmt.Fprintf(out, "%s\n  fields: %v\n  before: %s\n  after: %s\n  values: [redacted]\n", c.Path, c.Fields, digest(c.Before), digest(c.After))
	}
}

type TransactionKind string

const (
	TransactionApply   TransactionKind = "apply"
	TransactionRestore TransactionKind = "restore"
)

type Pending struct {
	Kind    TransactionKind `json:"kind,omitempty"`
	Absent  map[string]bool `json:"absent,omitempty"`
	Record  Record          `json:"record"`
	Desired State           `json:"desired"`
	Locks   []string        `json:"locks,omitempty"`
}

func copyState(s State) State {
	out := emptyState()
	for key, p := range s.Profiles {
		out.Profiles[key] = p
	}
	for key, value := range s.Active {
		out.Active[key] = value
	}
	for key, r := range s.Records {
		r.Files = append([]Backup(nil), r.Files...)
		originals := make(map[string]Backup, len(r.Originals))
		for path, b := range r.Originals {
			originals[path] = b
		}
		r.Originals = originals
		out.Records[key] = r
	}
	// Snapshot bytes are immutable; copy metadata without re-encoding large blobs.
	return out
}
func updatedRecord(s State, client, profile string, changes []Change) (Record, error) {
	record := Record{Profile: profile, Client: client, Originals: map[string]Backup{}}
	if prior, ok := s.Records[client]; ok {
		for path, original := range prior.Originals {
			record.Originals[path] = original
		}
		if len(prior.Originals) == 0 {
			for _, old := range prior.Files {
				record.Originals[old.Path] = old
			}
		}
	}
	for _, c := range changes {
		if c.RuntimeMode != "" {
			if record.RuntimeMode != "" && record.RuntimeMode != c.RuntimeMode {
				return Record{}, errors.New("conflicting runtime modes")
			}
			record.RuntimeMode = c.RuntimeMode
		}
		b := Backup{Path: c.Path, Before: c.Before, After: c.After, Existed: c.Existed, Removed: c.Remove}
		if _, ok := record.Originals[c.Path]; !ok {
			record.Originals[c.Path] = b
		}
		if prior, ok := s.Records[client]; ok {
			for _, old := range prior.Files {
				if old.Path == c.Path {
					restored, remove, e := restoreOwned(client, old, c.Before)
					if e != nil {
						return Record{}, e
					}
					b.Before = restored
					b.Existed = !remove
					break
				}
			}
		}
		record.Files = append(record.Files, b)
	}
	return record, nil
}

func (d Disk) Apply(s *State, client, profile string, changes []Change, prompt Prompter) error {
	if len(changes) == 0 {
		return errors.New("no configuration changes")
	}
	remote := false
	for _, c := range changes {
		remote = remote || c.RemoteKey
	}
	if remote && !prompt.Confirm("Store the remote API key in this client's protected settings (separate from the OS store)?") {
		return errors.New("remote-key storage declined")
	}
	if !prompt.Confirm("Apply the displayed settings changes and save protected recovery backups?") {
		return errors.New("settings changes declined")
	}
	desired := copyState(*s)
	record, e := updatedRecord(*s, client, profile, changes)
	if e != nil {
		return e
	}
	desired.Records[client] = record
	desired.Active[client] = profile
	return d.transaction(s, desired, changes, client, profile)
}
func (d Disk) ChangePort(s *State, next Profile, plans map[string][]Change) error {
	desired := copyState(*s)
	desired.Profiles[next.Name] = next
	var all []Change
	for client, changes := range plans {
		record, e := updatedRecord(*s, client, next.Name, changes)
		if e != nil {
			return e
		}
		desired.Records[client] = record
		all = append(all, changes...)
	}
	if len(all) == 0 {
		if e := d.Save(desired); e != nil {
			return e
		}
		*s = desired
		return nil
	}
	return d.transaction(s, desired, all, "port-change", next.Name)
}

// The journal contains the complete intended state and every before/after file.
// A crash cannot commit a port or profile switch for only some affected clients.
func (d Disk) transaction(s *State, desired State, changes []Change, client, profile string) error {
	return d.transact(s, desired, changes, client, profile, TransactionApply)
}
func (d Disk) transact(s *State, desired State, changes []Change, client, profile string, kind TransactionKind) error {
	var lockPaths []string
	for _, c := range changes {
		lockPaths = append(lockPaths, c.Locks...)
	}
	release, e := d.nativeLocks(lockPaths)
	if e != nil {
		return e
	}
	defer release()
	record := Record{Client: client, Profile: profile}
	seen := map[string]bool{}
	for _, c := range changes {
		if c.Check != nil {
			if e := c.Check(); e != nil {
				return e
			}
		}
		if seen[c.Path] {
			return errors.New("transaction contains competing destinations")
		}
		seen[c.Path] = true
		b, exists, e := readConfig(c.Path)
		if e != nil {
			return e
		}
		if exists != c.Existed || !bytes.Equal(b, c.Before) {
			return ErrConflict
		}
		record.Files = append(record.Files, Backup{Path: c.Path, Before: c.Before, After: c.After, Existed: c.Existed, Removed: c.Remove})
	}
	pending := filepath.Join(d.Root, "pending.json")
	if exists(pending) {
		return errors.New("unfinished transaction requires recovery")
	}
	absent := map[string]bool{}
	for _, c := range changes {
		if c.Remove {
			absent[c.Path] = true
		}
	}
	data, e := d.marshalPending(Pending{Kind: kind, Absent: absent, Record: record, Desired: desired, Locks: lockPaths})
	if e != nil {
		return e
	}
	if e := d.write(pending, data); e != nil {
		return e
	}
	for _, c := range changes {
		current, exists, e := readConfig(c.Path)
		if e != nil {
			return e
		}
		if exists != c.Existed || !bytes.Equal(current, c.Before) {
			return ErrConflict
		}
		validate := func() error {
			if c.Check != nil {
				if e := c.Check(); e != nil {
					return e
				}
			}
			return compareFile(c.Path, c.Before, c.Existed)()
		}
		if c.Remove {
			if e = validate(); e == nil {
				if c.Existed {
					e = os.Remove(c.Path)
				}
			}
		} else {
			e = d.write(c.Path, c.After, validate)
		}
		if e != nil {
			return errors.New("settings transaction interrupted; run recover with its protected journal")
		}

	}
	if e := d.Save(desired); e != nil {
		return e
	}
	*s = desired
	return d.finishJournal(pending)
}
func restoreBytes(b Backup, current []byte) ([]byte, error) {
	if bytes.Equal(current, b.After) {
		return b.Before, nil
	}
	// A minimal source span permits unrelated edits before/after the owned
	// contribution. A changed or ambiguous owned span refuses restoration.
	prefix := 0
	for prefix < len(b.Before) && prefix < len(b.After) && b.Before[prefix] == b.After[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(b.Before)-prefix && suffix < len(b.After)-prefix && b.Before[len(b.Before)-1-suffix] == b.After[len(b.After)-1-suffix] {
		suffix++
	}
	old := b.Before[prefix : len(b.Before)-suffix]
	owned := b.After[prefix : len(b.After)-suffix]
	if len(owned) == 0 || bytes.Count(current, owned) != 1 {
		return nil, ErrConflict
	}
	return bytes.Replace(current, owned, old, 1), nil
}
func (d Disk) Restore(s *State, client string) error {
	r, ok := s.Records[client]
	if !ok {
		return nil
	}
	var changes []Change
	for _, b := range r.Files {
		current, exists, e := readConfig(b.Path)
		if e != nil {
			return e
		}
		if b.Removed {
			if exists {
				return ErrConflict
			}
			changes = append(changes, Change{Path: b.Path, After: b.Before, Existed: false, Remove: !b.Existed, Locks: nativeLockPaths(r.Client, b.Path)})
			continue
		}
		if !exists {
			return ErrConflict
		}
		result, remove, e := restoreOwned(client, b, current)
		if e != nil {
			return e
		}
		changes = append(changes, Change{Path: b.Path, Before: current, After: result, Existed: true, Remove: remove, Locks: nativeLockPaths(r.Client, b.Path)})
	}
	desired := copyState(*s)
	delete(desired.Records, client)
	delete(desired.Active, client)
	return d.transact(s, desired, changes, client, r.Profile, TransactionRestore)
}

func compareFile(path string, expected []byte, expectedExists bool) func() error {
	return func() error {
		current, exists, e := readConfig(path)
		if e != nil {
			return e
		}
		if exists != expectedExists || !bytes.Equal(current, expected) {
			return ErrConflict
		}
		return nil
	}
}
