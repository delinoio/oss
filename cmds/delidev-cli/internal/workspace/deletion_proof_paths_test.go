// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func proofWork() domain.SessionDeletionWork {
	return domain.SessionDeletionWork{Version: 1, DeletionID: domain.NewID(), ServerID: domain.NewID(), SessionID: domain.NewID(), MachineID: domain.NewID(), DeviceID: domain.NewID(), Copies: []domain.SessionDeletionCopy{{JobID: domain.NewID(), Type: domain.PrepareWorkspaceJob, Revision: 1, Digest: strings.Repeat("a", 64), InstanceID: domain.NewID()}}}
}
func TestDeletionProofMigrationRetainsExactOriginalBytes(t *testing.T) {
	for _, kind := range []DeletionProofKind{WorkerDeletionProof, WorkspaceDeletionProof} {
		t.Run(map[DeletionProofKind]string{WorkerDeletionProof: "worker", WorkspaceDeletionProof: "workspace"}[kind], func(t *testing.T) {
			root, w := t.TempDir(), proofWork()
			if err := security.PrivateDir(filepath.Join(root, "session-deletions")); err != nil {
				t.Fatal(err)
			}
			suffix := ".json"
			if kind == WorkspaceDeletionProof {
				suffix = "-workspace.json"
			}
			legacy := filepath.Join(root, "session-deletions", string(w.SessionID)+suffix)
			original := []byte(`{"version":1,"original_receipt":"retained","removal_started":true}`)
			if err := security.WriteAtomic(legacy, original); err != nil {
				t.Fatal(err)
			}
			valid := func(raw []byte) bool { return bytes.Equal(raw, original) }
			if err := MigrateSessionDeletionProof(root, w, kind, 4096, valid); err != nil {
				t.Fatal(err)
			}
			actual, err := security.ReadPrivate(SessionDeletionProofPath(root, w, kind), 4096)
			if err != nil || !bytes.Equal(actual, original) {
				t.Fatal("changed immutable bytes", err)
			}
			if _, err := os.Lstat(legacy); !os.IsNotExist(err) {
				t.Fatal("legacy retained", err)
			}
			if err := MigrateSessionDeletionProof(root, w, kind, 4096, valid); err != nil {
				t.Fatal("interrupted sync recovery failed", err)
			}
		})
	}
}
func TestDeletionProofMigrationRejectsForeignOrConflictingEvidence(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreign", true: "conflict"}[conflict], func(t *testing.T) {
			root, w := t.TempDir(), proofWork()
			if err := security.PrivateDir(SessionDeletionDirectory(root, w.SessionID)); err != nil {
				t.Fatal(err)
			}
			legacy := filepath.Join(root, "session-deletions", string(w.SessionID)+"-workspace.json")
			original := []byte("retained")
			if err := security.WriteAtomic(legacy, original); err != nil {
				t.Fatal(err)
			}
			destination := SessionDeletionProofPath(root, w, WorkspaceDeletionProof)
			if conflict {
				if err := security.WriteAtomic(destination, []byte("conflict")); err != nil {
					t.Fatal(err)
				}
			}
			if err := MigrateSessionDeletionProof(root, w, WorkspaceDeletionProof, 4096, func([]byte) bool { return conflict }); err == nil {
				t.Fatal("ambiguous evidence migrated")
			}
			actual, err := security.ReadPrivate(legacy, 4096)
			if err != nil || !bytes.Equal(actual, original) {
				t.Fatal("legacy modified", err)
			}
		})
	}
}
