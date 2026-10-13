// SPDX-License-Identifier: Apache-2.0
package security

import (
	"errors"
	"os"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// ObjectKind is the stable file type recorded in a PathIdentityRecord.
type ObjectKind uint8

const (
	ObjectRegular ObjectKind = iota + 1
	ObjectDirectory
	ObjectSymlink
	ObjectOther
)

// PathIdentityRecord is the serializable identity of one filesystem object. It
// carries no content. Birth time is zero where the platform or filesystem does
// not report one; size and modification time are recorded for regular files only.
type PathIdentityRecord struct {
	Kind      ObjectKind `json:"kind"`
	Device    uint64     `json:"device"`
	Inode     uint64     `json:"inode"`
	Size      int64      `json:"size,omitempty"`
	ModTimeNS int64      `json:"mtime_ns,omitempty"`
	BirthNS   int64      `json:"birth_ns,omitempty"`
}

// sameObject reports whether c describes the object r was recorded from.
// Device and inode bind the object, birth time (when both sides report one)
// rejects a recreated object that reuses an inode, and regular files also bind
// size and modification time so in-place rewrites are foreign.
func (r PathIdentityRecord) sameObject(c PathIdentityRecord) bool {
	if r.Kind != c.Kind || r.Device != c.Device || r.Inode != c.Inode {
		return false
	}
	if r.BirthNS != 0 && c.BirthNS != 0 && r.BirthNS != c.BirthNS {
		return false
	}
	if r.Kind == ObjectRegular {
		return r.Size == c.Size && r.ModTimeNS == c.ModTimeNS
	}
	return true
}

// PathIdentity is the validated state of one managed path: either the exact
// filesystem object that ownership validation observed, or proven absence.
//
// A captured identity holds the object open. While the handle is open the
// operating system cannot reuse the object's inode (or NTFS file ID) for a new
// object, so a record match against the current path proves the same object
// within one attempt. A record restored from storage, or one whose handle was
// released, has no such guarantee across the interval: it is only as strong as
// its device, inode, birth time (where reported), size and modification time.
type PathIdentity struct {
	present bool
	record  PathIdentityRecord
	held    *os.File
}

// IdentityFromRecord restores a persisted identity without holding the object.
func IdentityFromRecord(record PathIdentityRecord) PathIdentity {
	return PathIdentity{present: true, record: record}
}

// CapturePathIdentity records what currently exists at path without following
// links and holds the object open. A missing path is captured as proven absence.
// The caller must Close the identity when removal is finished or abandoned.
func CapturePathIdentity(path string) (PathIdentity, error) {
	f, record, e := openPathIdentity(path)
	if errors.Is(e, os.ErrNotExist) {
		return PathIdentity{}, nil
	}
	if e != nil {
		return PathIdentity{}, domain.SessionDeletionPending()
	}
	return PathIdentity{present: true, record: record, held: f}, nil
}

// ReopenPathIdentity binds a persisted record to the object now at path and
// holds it. A different object at the path is pending cleanup; an object that
// has vanished since the record was taken counts as already removed.
func ReopenPathIdentity(path string, record PathIdentityRecord) (PathIdentity, error) {
	f, current, e := openPathIdentity(path)
	if errors.Is(e, os.ErrNotExist) {
		return PathIdentity{present: true, record: record}, nil
	}
	if e != nil {
		return PathIdentity{}, domain.SessionDeletionPending()
	}
	if !record.sameObject(current) {
		if f != nil {
			f.Close()
		}
		return PathIdentity{}, domain.SessionDeletionPending()
	}
	return PathIdentity{present: true, record: record, held: f}, nil
}

// Present reports whether the captured path existed.
func (i PathIdentity) Present() bool { return i.present }

// Record returns the serializable identity of a present object.
func (i PathIdentity) Record() PathIdentityRecord { return i.record }

// Held reports whether the identity still holds its object open.
func (i PathIdentity) Held() bool { return i.held != nil }

// Release closes the held handle but keeps the record, for callers that bound
// their descriptor budget. The remaining guarantee is the record alone.
func (i *PathIdentity) Release() {
	if i.held != nil {
		i.held.Close()
		i.held = nil
	}
}

// Close releases the handle. It is safe to call more than once.
func (i *PathIdentity) Close() { i.Release() }

// StillAt reports whether path currently holds the captured object (or is still
// absent for a proven-absent identity).
func (i PathIdentity) StillAt(path string) error {
	f, current, e := openPathIdentity(path)
	if errors.Is(e, os.ErrNotExist) {
		// Proven absence stays absent, and a vanished original is already removed.
		return nil
	}
	if e != nil {
		return domain.SessionDeletionPending()
	}
	if f != nil {
		f.Close()
	}
	if !i.present || !i.record.sameObject(current) {
		return domain.SessionDeletionPending()
	}
	return nil
}
