// SPDX-License-Identifier: Apache-2.0
// Package lifecycle publishes cache trees only after every clone succeeds.
// Callers must hold all source, destination and transaction leases throughout.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/cowcache/internal/clone"
	"github.com/delinoio/oss/cmds/cowcache/internal/state"
	"github.com/google/uuid"
)

type Pair struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}
type entry struct {
	Pair
	Stage    string `json:"stage"`
	Backup   string `json:"backup"`
	Existing bool   `json:"existing"`
}
type journal struct {
	SchemaVersion int     `json:"schemaVersion"`
	Committed     bool    `json:"committed"`
	Entries       []entry `json:"entries"`
}
type Result struct {
	Reused bool           `json:"reused"`
	Trees  []clone.Result `json:"trees,omitempty"`
}

func Exists(path string) (bool, error) {
	i, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("cache %s is not a directory; repair it before continuing", path)
	}
	return true, nil
}

// Prepare stages all requested trees before moving any existing cache. Recovery
// rolls back an unfinished publication, or removes backups of a committed one.
func Prepare(ctx context.Context, record string, pairs []Pair, replace, allowCopy bool) (result Result, err error) {
	if err = Recover(record, pairs); err != nil {
		return result, err
	}
	j := journal{SchemaVersion: 1}
	result.Reused = true
	id, e := uuid.NewV7()
	if e != nil {
		return result, e
	}
	for _, p := range pairs {
		exists, e := Exists(p.Destination)
		if e != nil {
			return result, e
		}
		if exists && !replace {
			continue
		}
		if p.Source == p.Destination {
			if replace {
				return result, errors.New("cannot replace a cache from itself; select another source")
			}
			continue
		}
		if ok, e := Exists(p.Source); e != nil || !ok {
			if e != nil {
				return result, e
			}
			return result, errors.New("source cache unavailable; select another source or use cowc run interactively")
		}
		if e = os.MkdirAll(filepath.Dir(p.Destination), 0755); e != nil {
			return result, e
		}
		suffix := ".cowc-" + id.String()
		j.Entries = append(j.Entries, entry{Pair: p, Stage: p.Destination + suffix + ".stage", Backup: p.Destination + suffix + ".backup", Existing: exists})
		result.Reused = false
	}
	if len(j.Entries) == 0 {
		return result, nil
	}
	if err = state.Write(record, j); err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, Recover(record, pairs))
		}
	}()
	for _, v := range j.Entries {
		r, e := clone.Tree(ctx, v.Source, v.Stage, allowCopy)
		if e != nil {
			return result, e
		}
		if e = syncTree(v.Stage); e != nil {
			return result, e
		}
		result.Trees = append(result.Trees, r)
	}
	for _, v := range j.Entries {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if v.Existing {
			if err = os.Rename(v.Destination, v.Backup); err != nil {
				return result, err
			}
		}
		if err = syncParent(v.Destination); err != nil {
			return result, err
		}
		if err = os.Rename(v.Stage, v.Destination); err != nil {
			return result, err
		}
	}
	for _, v := range j.Entries {
		if err = syncParent(v.Destination); err != nil {
			return result, err
		}
	}
	j.Committed = true
	if err = state.Write(record, j); err != nil {
		return result, err
	}
	err = Recover(record, pairs)
	return result, err
}
func Recover(record string, pairs []Pair) error {
	var j journal
	e := state.Read(record, &j)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if j.SchemaVersion != 1 {
		return errors.New("unsupported transaction schemaVersion; use a compatible cowc")
	}
	// A journal is not authority to delete arbitrary paths. Revalidate every entry
	// against the freshly discovered preset paths before making any change.
	for _, v := range j.Entries {
		found := false
		for _, p := range pairs {
			if p.Destination == v.Destination {
				found = true
			}
		}
		prefix := v.Destination + ".cowc-"
		if !found || filepath.Dir(v.Stage) != filepath.Dir(v.Destination) || filepath.Dir(v.Backup) != filepath.Dir(v.Destination) {
			return errors.New("transaction paths differ from verified cache paths; retain journal for recovery")
		}
		suffix := v.Stage[lenPrefix(v.Stage, prefix):]
		if lenPrefix(v.Stage, prefix) == 0 {
			return errors.New("invalid transaction stage")
		}
		if len(suffix) < 6 || suffix[len(suffix)-6:] != ".stage" {
			return errors.New("invalid transaction stage")
		}
		id := suffix[:len(suffix)-6]
		u, e := uuid.Parse(id)
		if e != nil || u.Version() != 7 || v.Backup != prefix+id+".backup" {
			return errors.New("invalid transaction backup")
		}
	}
	for i := len(j.Entries) - 1; i >= 0; i-- {
		v := j.Entries[i]
		backup, e := Exists(v.Backup)
		if e != nil {
			return e
		}
		stage, e := Exists(v.Stage)
		if e != nil {
			return e
		}
		if j.Committed {
			if e = removeTree(v.Backup); e != nil {
				return e
			}
		} else if backup {
			if e = removeTree(v.Destination); e != nil {
				return e
			}
			if e = os.Rename(v.Backup, v.Destination); e != nil {
				return e
			}
		} else if !v.Existing && !stage {
			if e = removeTree(v.Destination); e != nil {
				return e
			}
		}
		if e = removeTree(v.Stage); e != nil {
			return e
		}
	}
	for _, v := range j.Entries {
		if e = syncParent(v.Destination); e != nil {
			return e
		}
	}
	if e = os.Remove(record); e != nil {
		return e
	}
	return syncParent(record)
}
func lenPrefix(s, p string) int {
	if len(s) >= len(p) && s[:len(p)] == p {
		return len(p)
	}
	return 0
}

// Only transaction-owned trees use this cleanup. Restore owner write permission
// on read-only cloned directories so a failed publication cannot leak staging.
func removeTree(path string) error {
	if _, e := os.Lstat(path); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return e
	}
	e := filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return os.Chmod(p, 0700)
		}
		return nil
	})
	if e != nil {
		return e
	}
	return os.RemoveAll(path)
}
