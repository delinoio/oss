// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestCodexExternalImageViewRetainsLifecycleWithoutFileAuthority(t *testing.T) {
	for _, change := range []string{"none", "location", "manifest", "status", "duplicate", "legacy"} {
		t.Run(change, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			c.publisher.input.NativeDynamicTools = true
			input := &c.publisher.input
			preparation := workspace.PrepareRequest{SessionID: input.SessionID, MachineID: input.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
			input.Preparation, _ = json.Marshal(preparation)
			digest := sha256.Sum256(input.Preparation)
			root := filepath.Join(t.TempDir(), "workspaces", string(input.SessionID), "chat")
			manifest := workspace.Manifest{Version: 1, SessionID: input.SessionID, MachineID: input.MachineID, Type: domain.GeneralChat, State: workspace.Ready, InputDigest: hex.EncodeToString(digest[:]), PrimaryPath: root, Repositories: []workspace.PreparedRepository{}, CreatedAt: time.Now().UTC()}
			input.Manifest, _ = json.Marshal(manifest)
			location := filepath.Join(t.TempDir(), "private-external-image.png")
			tool := &codex.Tool{ID: "external-image", Kind: codex.ImageViewTool, Status: codex.ToolRunning, ImagePath: location}
			event := codex.Event{Kind: codex.ToolStartedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: tool.ID, Correlated: true, Tool: tool}
			if change == "legacy" {
				input.NativeDynamicTools = false
				if _, err := c.PublishCore(context.Background(), event); err == nil {
					t.Fatal("legacy peer gained external image support")
				}
				return
			}
			if _, err := c.PublishCore(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			event.Kind, tool.Status = codex.ToolCompletedEvent, codex.ToolCompleted
			switch change {
			case "location":
				tool.ImagePath = filepath.Join(filepath.Dir(location), "changed.png")
			case "manifest":
				input.Manifest = []byte(`{}`)
			case "status":
				tool.Status = codex.ToolFailed
			case "duplicate":
				if _, err := c.PublishCore(context.Background(), event); err != nil {
					t.Fatal(err)
				}
			}
			_, err := c.PublishCore(context.Background(), event)
			if (err == nil) != (change == "none") {
				t.Fatal("external image identity/lifecycle mismatch", err)
			}
			if change != "none" {
				return
			}
			if !c.dynamicToolsSettled() || len(c.tools) != 0 || len(c.dynamicTools) != 1 {
				t.Fatal("external image gained workspace tool ownership or did not settle")
			}
			raw, _ := json.Marshal(rpc.events)
			if bytes.Contains(raw, []byte(location)) || bytes.Contains(raw, []byte("image_view")) || bytes.Contains(raw, []byte("reference_id")) {
				t.Fatal("external file observation gained location or preview authority")
			}
		})
	}
}

func TestCodexImageViewWorkerRejectsChangedOwnerScopeAndCompletion(t *testing.T) {
	for _, bad := range []string{"native-location", "machine", "manifest", "thread", "turn", "failure-status", "duplicate-completion"} {
		t.Run(bad, func(t *testing.T) {
			c, rpc := codexTerminalStatusFixture(t)
			input := &c.publisher.input
			p := workspace.PrepareRequest{SessionID: input.SessionID, MachineID: input.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
			raw, _ := json.Marshal(p)
			digest := sha256.Sum256(raw)
			root := filepath.Join(t.TempDir(), "workspaces", string(input.SessionID), "chat")
			m := workspace.Manifest{Version: 1, SessionID: input.SessionID, MachineID: input.MachineID, Type: domain.GeneralChat, State: workspace.Ready, InputDigest: hex.EncodeToString(digest[:]), PrimaryPath: root, Repositories: []workspace.PreparedRepository{}, CreatedAt: time.Now().UTC()}
			input.Preparation = raw
			input.Manifest, _ = json.Marshal(m)
			native := &codex.Tool{ID: "image-original", Kind: codex.ImageViewTool, Status: codex.ToolRunning, ImagePath: filepath.Join(root, "never-open.png")}
			event := codex.Event{Kind: codex.ToolStartedEvent, ThreadID: c.thread, TurnID: c.turn, ItemID: native.ID, Correlated: true, Tool: native}
			if handled, err := c.PublishCore(context.Background(), event); !handled || err != nil {
				t.Fatal(err)
			}
			event.Kind = codex.ToolCompletedEvent
			native.Status = codex.ToolCompleted
			switch bad {
			case "native-location":
				native.ImagePath = filepath.Join(root, "another.png")
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
