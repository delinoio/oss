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

// Profile v2 only widens the native history families. It keeps the closed
// General Chat shape and the independent 8,192-entry copy bound.
func TestOpenCodeForkProfileV2KeepsGeneralChatShapeAndBound(t *testing.T) {
	machine := domain.NewID()
	for _, profile := range []ForkProfile{OpenCodeGeneralChatForkV1, OpenCodeGeneralChatForkV2} {
		request := PrepareRequest{SessionID: domain.NewID(), MachineID: machine, OriginMachineID: machine, Type: domain.GeneralChat, ForkSourceID: domain.NewID(), ForkSourcePath: "/source", ForkProfile: profile}
		if err := request.validateStructure(); err != nil {
			t.Fatalf("%s rejected a valid General Chat preparation: %v", profile, err)
		}
		if request.forkEntryLimit() != maxOpenCodeForkEntries {
			t.Fatalf("%s changed the copy bound", profile)
		}
		request.Type = domain.Worktree
		if request.validateStructure() == nil {
			t.Fatalf("%s accepted a non General Chat preparation", profile)
		}
	}
	unknown := PrepareRequest{SessionID: domain.NewID(), MachineID: machine, OriginMachineID: machine, Type: domain.GeneralChat, ForkSourceID: domain.NewID(), ForkSourcePath: "/source", ForkProfile: "opencode-general-chat-v3"}
	if unknown.validateStructure() == nil {
		t.Fatal("unknown profile accepted")
	}
}
