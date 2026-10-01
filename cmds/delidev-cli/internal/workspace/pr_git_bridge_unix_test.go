//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestPRGitBridgeRetainsCommandOwnershipThroughLocalChild(t *testing.T) {
	f := newPRPreparationFixture(t)
	preparePRFixGitTransport(t, f)
	control := t.TempDir()
	ready, release := filepath.Join(control, "ready"), filepath.Join(control, "release")
	wrapper := filepath.Join(control, "git")
	script := fmt.Sprintf("#!/bin/sh\nfor arg do\n if [ \"$arg\" = commit ]; then\n  : > %s\n  while [ ! -f %s ]; do sleep 0.01; done\n fi\ndone\nexec %s \"$@\"\n", fixtureShellQuote(ready), fixtureShellQuote(release), fixtureShellQuote(f.manager.Git.Executable))
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	f.manager.Git.Executable = wrapper
	manifest, err := f.manager.Prepare(context.Background(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.manager.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), f.request, manifest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	root := manifest.Repositories[0].Path
	gitTest(t, root, "config", "user.name", "PR fixture")
	gitTest(t, root, "config", "user.email", "fixture@example.invalid")
	selection := domain.PRFixExecution{AttemptID: domain.NewID(), Target: *f.request.Repositories[0].PRTarget, Strategy: domain.MergeConflictStrategy}
	tool, err := lease.PreparePRGitTool(context.Background(), selection, f.request, manifest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tool.Close() })
	for _, entry := range tool.Environment(nil) {
		name, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, prGitEnvironmentPrefix) {
			t.Setenv(name, value)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "fix.txt"), []byte("isolated change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RunPRGit(context.Background(), tool.path, []string{"add", "fix.txt"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() {
		completed <- RunPRGit(context.Background(), tool.path, []string{"commit", "-m", "controlled commit"}, io.Discard)
	}()
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0600) })
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-completed:
			t.Fatal("local child exited before barrier", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("local child did not reach controlled barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A guessed completion cannot release the command's actual owner.
	raw, _ := json.Marshal(prGitLocalCompletion{Version: 1, Grant: strings.Repeat("x", 32)})
	request, _ := http.NewRequest(http.MethodPost, tool.bridge.endpoint+"/complete", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+tool.bridge.token)
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatal("foreign completion released command ownership")
	}
	push := tool.scope.commandPlan()["push_argv"].([]string)
	bounded, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = RunPRGit(bounded, tool.path, push, io.Discard)
	cancel()
	if err == nil {
		t.Fatal("parallel push crossed the live local child")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(tool.path), "push.json")); !os.IsNotExist(err) {
		t.Fatal("parallel push claimed an intermediate HEAD", err)
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("local completion was not acknowledged")
	}
	if err := RunPRGit(context.Background(), tool.path, push, io.Discard); err != nil {
		t.Fatal("completed local child retained command ownership", err)
	}
	if gitTest(t, f.fork, "rev-parse", "HEAD") != gitTest(t, root, "rev-parse", "HEAD") {
		t.Fatal("serialized push did not publish the completed local commit")
	}
	// Simulate a launcher disappearing after grant, without a completion.
	raw, _ = json.Marshal(prGitBridgeRequest{Version: 1, Args: []string{"status"}})
	request, _ = http.NewRequest(http.MethodPost, tool.bridge.endpoint, bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+tool.bridge.token)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var granted prGitBridgeResponse
	if err := json.NewDecoder(response.Body).Decode(&granted); err != nil || granted.Grant == "" {
		t.Fatal("local grant unavailable", err)
	}
	response.Body.Close()
	bounded, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	err = RunPRGit(bounded, tool.path, []string{"delidev-target"}, io.Discard)
	cancel()
	if err == nil {
		t.Fatal("lost local completion granted another command")
	}
	if err := tool.Close(); err == nil {
		t.Fatal("lost local completion became clean bridge closure")
	}
}
