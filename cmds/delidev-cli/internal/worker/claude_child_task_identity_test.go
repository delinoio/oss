// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func TestSubagentClaudeTaskIdentityCommitsOnlyWithReceipt(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact-repeat", true: "altered-repeat"}[changed], func(t *testing.T) {
			c, rpc, o := claudeToolFixture(t, "", "Agent")
			ctx := context.Background()
			tool, kind, description := "tool_original_one", claude.LocalAgentTask, "Original description"
			o.Kind, o.Content, o.NativeID = claude.TaskObserved, nil, string(domain.NewID())
			o.Task = &claude.NativeTaskObservation{Kind: claude.TaskStarted, ID: "original_child", ToolID: &tool, Type: &kind, Description: &description}
			rpc.lose = true
			before := len(rpc.events)
			if _, err := c.PublishTaskObservation(ctx, o); err == nil || c.binding.progressSeen[o.NativeID] || len(c.children) != 0 {
				t.Fatal("unacknowledged child event acquired retained identity")
			}
			rpc.lose = false
			if err := c.ReplayPending(ctx); err != nil {
				t.Fatal(err)
			}
			if !c.binding.progressSeen[o.NativeID] || rpc.requests[before] != rpc.requests[before+1] || !bytes.Equal(rpc.events[before], rpc.events[before+1]) {
				t.Fatal("replayed child receipt did not retain the original event identity")
			}
			status := claude.TaskRunning
			o.NativeID = string(domain.NewID())
			o.Task = &claude.NativeTaskObservation{Kind: claude.TaskNotification, ID: "original_child", ToolID: &tool, Status: &status, Description: &description}
			if _, err := c.PublishTaskObservation(ctx, o); err != nil || !c.binding.progressSeen[o.NativeID] {
				t.Fatal("fresh child notification lost its acknowledged identity", err)
			}
			if changed {
				copy := *o.Task
				altered := "Altered duplicate description"
				copy.Description, o.Task = &altered, &copy
			}
			before = len(rpc.events)
			if _, err := c.PublishTaskObservation(ctx, o); err == nil || len(rpc.events) != before || *c.children["original_child"].Task.Description != description || c.binding.stage != claudeBindingBlocked {
				t.Fatal("acknowledged native task identity was reused or changed retained metadata")
			}
		})
	}
}
