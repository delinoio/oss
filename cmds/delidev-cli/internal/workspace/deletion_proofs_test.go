// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func deletionProofWork() domain.SessionDeletionWork {
	return domain.SessionDeletionWork{Version: 1, ServerID: domain.NewID(), SessionID: domain.NewID(), DeletionID: domain.NewID(), MachineID: domain.NewID(), DeviceID: domain.NewID(), Copies: []domain.SessionDeletionCopy{{JobID: domain.NewID(), Type: domain.PrepareWorkspaceJob, Revision: 1, InstanceID: domain.NewID(), Digest: strings.Repeat("a", 64)}}}
}
func TestDeletionWorkspaceProofLegacyImportsOnlyExactEqualPairs(t *testing.T) {
	for _, stage := range []string{"original", "removed", "equal-pair", "changed-pair", "foreign-work"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			work := deletionProofWork()
			if err := security.PrivateDir(filepath.Join(root, "session-deletions")); err != nil {
				t.Fatal(err)
			}
			legacy := deletionWorkspaceProof{Version: 1, Digest: work.Digest(), Removed: stage == "removed"}
			expected := legacy
			expected.Version = 2
			expected.Identity = work.Identity()
			paths, _ := SessionDeletionProofPaths(root, work)
			if stage == "foreign-work" {
				legacy.Digest = deletionProofWork().Digest()
			}
			if err := writeDeletionWorkspaceProof(legacyDeletionWorkspacePath(root, work), legacy); err != nil {
				t.Fatal(err)
			}
			if stage == "equal-pair" || stage == "changed-pair" {
				pair := expected
				if stage == "changed-pair" {
					pair.Removed = true
				}
				if err := writeDeletionWorkspaceProof(paths.Workspace, pair); err != nil {
					t.Fatal(err)
				}
			}
			err := ImportLegacyDeletionWorkspaceProof(root, work)
			accepted := stage == "original" || stage == "removed" || stage == "equal-pair"
			if (err == nil) != accepted {
				t.Fatalf("workspace legacy %s: %v", stage, err)
			}
			if accepted {
				raw, err := security.ReadPrivate(paths.Workspace, 1<<20)
				var actual deletionWorkspaceProof
				if err != nil || domain.Decode(raw, &actual) != nil || actual.Identity != expected.Identity || actual.Digest != expected.Digest || actual.Removed != expected.Removed {
					t.Fatal("original workspace stage changed", err)
				}
				if _, err := os.Lstat(legacyDeletionWorkspacePath(root, work)); !os.IsNotExist(err) {
					t.Fatal("legacy proof not retired", err)
				}
			} else if _, err := os.Lstat(legacyDeletionWorkspacePath(root, work)); err != nil {
				t.Fatal("foreign legacy proof changed", err)
			}
		})
	}
}
