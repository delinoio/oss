// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func verifyNativeCLISidechat(t *testing.T, ctx context.Context, run func([]string, any) map[string]any, parentID string, original domain.Session, originalJob domain.Job, workerRoot string, calls *atomic.Int64, activeRequest, releaseActive chan struct{}) {
	t.Helper()
	var assignment domain.ExecutionJobInput
	var source workspace.Manifest
	if domain.Decode(originalJob.Input, &assignment) != nil || domain.Decode(assignment.Manifest, &source) != nil {
		t.Fatal("source authority absent")
	}
	sentinel := filepath.Join(source.PrimaryPath, "sidechat-current.txt")
	if err := os.WriteFile(sentinel, []byte("original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rev := func(row map[string]any) string { return strconv.FormatUint(uint64(row["revision"].(float64)), 10) }
	parent := run([]string{"session", "get", "--id", parentID}, nil)
	run([]string{"session", "stop", "--id", parentID, "--revision", rev(parent)}, nil)
	run([]string{"session", "enqueue", "--id", parentID}, domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Parent-owned pending input"})
	parent = run([]string{"session", "get", "--id", parentID}, nil)
	before, _ := json.Marshal(parent["data"])
	for {
		machine := run([]string{"machine", "get", "--id", string(original.MachineID)}, nil)["data"].(map[string]any)
		ready := false
		if values, ok := machine["worker_capabilities"].([]any); ok {
			for _, value := range values {
				if value == string(domain.CodexReadOnlySidechatWorkerV1) {
					ready = true
				}
			}
		}
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("original Worker did not negotiate Sidechat")
		case <-time.After(100 * time.Millisecond):
		}
	}
	args := []string{"session", "sidechat", "--id", parentID, "--revision", rev(parent), "--turn-id", original.Execution.NativeTurnID, "--name", "Native read-only Sidechat", "--request-id", string(domain.NewID()), "--wait"}
	result := run(args, nil)
	childRow, ok := result["session"].(map[string]any)
	if !ok {
		t.Fatal("native Sidechat was not published")
	}
	childID := childRow["id"].(string)
	var child domain.Session
	raw, _ := json.Marshal(childRow["data"])
	if domain.Decode(raw, &child) != nil || !child.IsSidechat() || child.Fork.Validate() != nil || child.Source != domain.SidechatSession || child.InitialExecution != nil || child.PendingInputs != 0 || calls.Load() != 1 {
		t.Fatal("Sidechat inferred during preparation or lost original authority")
	}
	if child.Fork.Snapshot.Configuration.SidechatPolicy != domain.CodexReadOnlySidechatV1 || child.Fork.Snapshot.Configuration.Options.Permission != domain.PermissionReadOnly || child.Fork.Snapshot.InitialAccountID != original.InitialExecution.InitialAccountID {
		t.Fatal("Sidechat acquired another account or write authority")
	}
	var j domain.Job
	var output domain.ForkJobResult
	var manifest workspace.Manifest
	raw, _ = json.Marshal(result["job"].(map[string]any)["data"])
	if domain.Decode(raw, &j) != nil || domain.Decode(j.Output, &output) != nil || domain.Decode(output.Manifest, &manifest) != nil || manifest.Reference == nil || manifest.PrimaryPath != source.PrimaryPath {
		t.Fatal("Sidechat copied or adopted its source workspace")
	}
	for _, repo := range manifest.Repositories {
		if repo.Owned {
			t.Fatal("Sidechat owns parent removal")
		}
	}
	replay := run(args, nil)
	if replay["replayed"] != true || replay["session"].(map[string]any)["id"] != childID || calls.Load() != 1 {
		t.Fatal("Sidechat replay repeated native fork or inference")
	}
	if err := os.WriteFile(sentinel, []byte("current parent file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	readArgs := []string{"session", "files", "read", "--id", childID, "--path", "sidechat-current.txt"}
	if len(manifest.Repositories) != 0 {
		for _, repo := range manifest.Repositories {
			if repo.Path == manifest.PrimaryPath {
				readArgs = append(readArgs, "--repository-id", string(repo.ID))
			}
		}
	}
	read := run(readArgs, nil)
	if read["text"] != "current parent file\n" {
		t.Fatal("Sidechat did not read current referenced parent files")
	}
	var lastExecution domain.ID
	for n, prompt := range []string{"Public second prompt", "Public third prompt"} {
		mode := domain.ExecuteMode
		if n == 1 {
			mode = domain.PlanMode
		}
		run([]string{"session", "enqueue", "--id", childID}, domain.SessionInput{Prompt: prompt, Mode: mode})
		if n == 0 {
			childRow = run([]string{"session", "get", "--id", childID}, nil)
			run([]string{"session", "resume", "--id", childID, "--revision", rev(childRow)}, nil)
		}
		for {
			childRow = run([]string{"session", "get", "--id", childID}, nil)
			raw, _ := json.Marshal(childRow["data"])
			child = domain.Session{}
			if domain.Decode(raw, &child) != nil || child.Recovery != domain.NoRecovery || child.Outcome == domain.ExecutionFailed || child.Dispatch == domain.DispatchBlocked {
				t.Fatal("Sidechat continuation failed")
			}
			if calls.Load() == int64(n+2) && child.Execution != nil && child.Execution.ExecutionID != lastExecution && child.Execution.CleanupVerified && child.PendingInputs == 0 && child.ActiveExecutionID == "" && child.Outcome == domain.ExecutionSucceeded {
				var settled domain.Job
				job := run([]string{"job", "get", "--id", string(child.Execution.JobID)}, nil)
				raw, _ := json.Marshal(job["data"])
				if domain.Decode(raw, &settled) == nil && settled.State == domain.JobSucceeded && settled.FinishedAt != nil {
					lastExecution = child.Execution.ExecutionID
					break
				}
			}
			select {
			case <-ctx.Done():
				t.Fatal("Sidechat continuation timed out")
			case <-time.After(30 * time.Millisecond):
			}
		}
		if child.Execution.NativeThreadID != string(child.Fork.NativeThreadID) || child.InitialExecution.Configuration.SidechatPolicy != domain.CodexReadOnlySidechatV1 || child.Execution.Observed.Permission != domain.PermissionReadOnly || child.Execution.Observed.ApprovalPolicy != "never" {
			t.Fatal("continuation lost immutable native enforcement")
		}
	}
	after, _ := json.Marshal(run([]string{"session", "get", "--id", parentID}, nil)["data"])
	if string(before) != string(after) {
		t.Fatal("Sidechat changed parent queue or execution")
	}
	messages := run([]string{"message", "list", "--session-id", childID}, nil)["resources"].([]any)
	var selected map[string]any
	for _, row := range messages {
		resource := row.(map[string]any)
		m := resource["data"].(map[string]any)
		if m["role"] == "assistant" && m["state"] == "complete" && m["inherited"] == nil {
			selected = resource
			break
		}
	}
	if selected == nil {
		t.Fatal("no complete Sidechat findings")
	}
	parent = run([]string{"session", "get", "--id", parentID}, nil)
	childRow = run([]string{"session", "get", "--id", childID}, nil)
	send := []string{"session", "sidechat", "send", "--id", childID, "--revision", rev(childRow), "--parent-id", parentID, "--parent-revision", rev(parent), "--messages", selected["id"].(string) + ":" + rev(selected), "--request-id", string(domain.NewID())}
	queued := run(send, nil)
	if queued["input"].(map[string]any)["data"].(map[string]any)["prompt"] != "Selected Sidechat findings:\n\n"+selected["data"].(map[string]any)["text"].(string) {
		t.Fatal("findings selection changed or transferred unselected content")
	}
	if run(send, nil)["replayed"] != true || calls.Load() != 3 {
		t.Fatal("findings replay created work or inferred")
	}
	// Race permanent parent deletion with an admitted live child HTTP request.
	// The original Worker must join that child without exiting its controller
	// or substituting successful completion for an interrupted native turn.
	run([]string{"session", "enqueue", "--id", childID}, domain.SessionInput{Prompt: "Public fourth prompt", Mode: domain.ExecuteMode})
	select {
	case <-activeRequest:
	case <-ctx.Done():
		t.Fatal("live child request did not reach original provider")
	}
	parent = run([]string{"session", "get", "--id", parentID}, nil)
	run([]string{"session", "delete", "--id", parentID, "--revision", rev(parent), "--confirm"}, nil)
	close(releaseActive)
	for {
		deleted := run([]string{"session", "deletion", "--id", parentID}, nil)
		if deleted["state"] == "SESSION_DELETION_STATE_SUCCEEDED" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("parent deletion did not join dependent retirement")
		case <-time.After(100 * time.Millisecond):
		}
	}
	for _, path := range []string{sentinel, filepath.Join(workerRoot, "workspaces", childID), filepath.Join(workerRoot, "runtimes", string(child.Fork.RuntimeID))} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("dependent native/workspace cleanup incomplete", err)
		}
	}
	t.Log("Pinned native CLI/Connect/Worker Sidechat: original snapshot, current parent references, native read-only Execute/Plan continuations, selected findings/replay and dependent deletion passed with scripted provider and temporary SQLite/Git")
}
