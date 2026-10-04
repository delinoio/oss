// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCodeForkInventoryBoundDoesNotNarrowCodex(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Empty entries still consume the complete inventory bound. Bytes alone
	// cannot constrain a pathological tree or prove a complete native copy.
	for n := 0; n <= maxOpenCodeForkEntries; n++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("entry-%05d", n)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := scanForkTreeBounded(context.Background(), root, "", false, maxOpenCodeForkEntries); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("OpenCode adopted an over-bound tree", err)
	}
	if _, err := scanForkTree(context.Background(), root, "", false); err != nil {
		t.Fatal("legacy Codex bound was narrowed", err)
	}
}
