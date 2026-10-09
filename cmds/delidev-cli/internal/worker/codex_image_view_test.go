// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestCodexImageViewWorkerRejectsChangedOwnerScopeAndCompletion(t *testing.T) {
	for _, bad := range []string{"native-location", "machine", "manifest", "thread", "turn", "failure-status", "duplicate-completion"} {
		t.Run(bad, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			input := &c.publisher.input
			p := workspace.PrepareRequest{SessionID: input.SessionID, MachineID: input.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
			raw, _ := json.Marshal(p)
			digest := sha256.Sum256(raw)
			root := "/original/workspaces/" + string(input.SessionID) + "/chat"
			m := workspace.Manifest{Version: 1, SessionID: input.SessionID, MachineID: input.MachineID, Type: domain.GeneralChat, State: workspace.Ready, InputDigest: hex.EncodeToString(digest[:]), PrimaryPath: root, Repositories: []workspace.PreparedRepository{}, CreatedAt: time.Now().UTC()}
			input.Preparation = raw
			input.Manifest, _ = json.Marshal(m)
			native := &codex.Tool{ID: "image-original", Kind: codex.ImageViewTool, Status: codex.ToolRunning, ImagePath: root + "/never-open.png"}
			event := codex.Event{Kind: codex.ToolStartedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: native.ID, Correlated: true, Tool: native}
			if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil {
				t.Fatal(err)
			}
			event.Kind = codex.ToolCompletedEvent
			native.Status = codex.ToolCompleted
			switch bad {
			case "native-location":
				native.ImagePath = root + "/another.png"
			case "machine":
				input.MachineID = domain.NewID()
			case "manifest":
				input.Manifest = append(input.Manifest, ' ')
			case "thread":
				event.ThreadID = domain.NewID()
			case "turn":
				event.TurnID = domain.NewID()
			case "failure-status":
				native.Status = codex.ToolFailed
			case "duplicate-completion":
				if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil {
					t.Fatal(err)
				}
			}
			before := len(rpc.events)
			if _, err := c.PublishCore(context.Background(), event); err == nil || !c.blocked || len(rpc.events) != before {
				t.Fatal("changed image observation was published")
			}
		})
	}
}
