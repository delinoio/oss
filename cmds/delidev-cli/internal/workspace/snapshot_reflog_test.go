// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSnapshotRetainsCommonBranchReflogOffline(t *testing.T) {
	m := manager(t)
	input, sources := snapshotRequest(t, m, false)
	source, selected := sources[0], input.Manifest.Repositories[0].Path
	if err := os.WriteFile(filepath.Join(source, "tracked.txt"), []byte("unpushed reset commit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, source, "add", "tracked.txt")
	gitTest(t, source, "commit", "-m", "unpushed reset commit")
	lost := gitTest(t, source, "rev-parse", "HEAD")
	gitTest(t, source, "reset", "--hard", "HEAD^")
	if strings.Contains(gitTest(t, source, "rev-list", "--all"), lost) || !strings.Contains(gitTest(t, source, "reflog", "show", "--format=%H", "main"), lost) {
		t.Fatal("fixture commit is not reachable exclusively through the common reflog")
	}
	selectedHeadLog := gitTest(t, selected, "reflog", "show", "--format=%H", "HEAD")
	commonBranchLog := gitTest(t, source, "reflog", "show", "--format=%H", "main")
	preview := storageDo(t, m, input)
	input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	cleaned := storageDo(t, m, input)
	if err := os.Rename(source, source+"-offline"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(source+"-offline", source)
	input.Action, input.OperationID, input.PreviousState, input.PreviousSnapshotID, input.SnapshotDigest = StorageRestore, domain.NewID(), domain.WorkspaceStored, input.SnapshotID, cleaned.Snapshot.SHA256
	storageDo(t, m, input)
	if actual := gitTest(t, selected, "reflog", "show", "--format=%H", "main"); actual != commonBranchLog {
		t.Fatal("restored common branch reflog was lost or changed", actual)
	}
	if actual := gitTest(t, selected, "reflog", "show", "--format=%H", "HEAD"); actual != selectedHeadLog {
		t.Fatal("restored HEAD log belongs to a different worktree", actual)
	}
	if actual := gitTest(t, selected, "show", lost+":tracked.txt"); actual != "unpushed reset commit" {
		t.Fatal("reset commit is not recoverable offline", actual)
	}
}
