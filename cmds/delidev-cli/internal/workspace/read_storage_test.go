// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestReadStorageNativeHelper(t *testing.T) {
	if os.Getenv("DELIDEV_READ_STORAGE_FIXTURE") != "1" {
		return
	}
	io.Copy(io.Discard, os.Stdin)
}

func TestWorkspaceObservationGateRetainsNativeOwnerBeforeStorage(t *testing.T) {
	m, prepare, manifest := chatExecutionFixture(t)
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	request := workspaceReadFixture(input.Preparation, input.Manifest, domain.WorkspaceFile, "keep")
	ready, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	go func() {
		finished <- m.observeWorkspace(context.Background(), request, input.Preparation.PrimaryRepository, true, func(ctx context.Context, git Git, _ Manifest, _ *os.Root, selected string) (returned error) {
			// Use an anchored General Chat read to isolate the shared lifetime
			// gate from the independent complete Git/PR matching profile. This
			// fixture child uses the same session-bound native owner namespace.
			for _, dir := range []string{filepath.Dir(git.ProcessRoot), git.ProcessRoot, filepath.Join(git.ProcessRoot, string(git.OwnerID))} {
				if err := security.PrivateDir(dir); err != nil {
					return err
				}
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				if err := m.quiesceWorkspaceReads(cleanup, input.Preparation.SessionID); err != nil {
					returned = err
				}
			}()
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			env := []string{"DELIDEV_READ_STORAGE_FIXTURE=1"}
			for _, key := range []string{"SystemRoot", "WINDIR"} {
				if value := os.Getenv(key); value != "" {
					env = append(env, key+"="+value)
				}
			}
			child, err := process.Start(ctx, process.Config{Directory: git.ProcessRoot, OwnerID: git.OwnerID, Executable: executable, Args: []string{"-test.run=^TestReadStorageNativeHelper$"}, Env: env, Cwd: selected, Stdout: io.Discard, Stderr: io.Discard, Logger: m.Logger})
			if err != nil {
				return err
			}
			defer child.Close()
			if err := child.Resume(); err != nil {
				return err
			}
			close(ready)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			if err := child.CloseInput(); err != nil {
				return err
			}
			return child.Wait()
		})
	}()
	joined := false
	defer func() {
		unblock()
		if !joined {
			<-finished
		}
	}()
	select {
	case <-ready:
	case err := <-finished:
		joined = true
		t.Fatal("read could not establish native ownership", err)
	case <-time.After(15 * time.Second):
		t.Fatal("read did not establish native ownership")
	}
	other := &Manager{Root: m.Root, Logger: m.Logger}
	result, err := other.Storage(context.Background(), input)
	if domain.SafeError(err).Code != domain.Conflict || result.CleanupVerified {
		t.Fatal("storage bypassed in-flight native read", result, err)
	}
	if _, err := os.Stat(input.Manifest.PrimaryPath); err != nil {
		t.Fatal("read workspace was removed", err)
	}
	if _, err := os.Lstat(m.snapshotPath(input.SnapshotID)); !os.IsNotExist(err) {
		t.Fatal("busy storage created output", err)
	}
	work := domain.SessionDeletionWork{Version: 1, DeletionID: domain.NewID(), ServerID: domain.NewID(), SessionID: input.Preparation.SessionID, MachineID: input.Preparation.MachineID, DeviceID: domain.NewID(), PreparationDigests: []string{input.Manifest.InputDigest}, Copies: []domain.SessionDeletionCopy{{JobID: domain.NewID(), Type: domain.PrepareWorkspaceJob, Revision: 1, InstanceID: domain.NewID(), Digest: strings.Repeat("a", 64)}}}
	removed := false
	if work.Validate() != nil {
		t.Fatal("invalid deletion fixture")
	}
	if err := other.DeleteOwnedWorkspace(context.Background(), work, false, func() error { removed = true; return nil }); err == nil || removed {
		t.Fatal("permanent deletion bypassed in-flight read", err)
	}
	independent := PrepareRequest{SessionID: domain.NewID(), MachineID: input.Preparation.MachineID, Type: domain.GeneralChat}
	independentManifest, err := other.Prepare(context.Background(), independent)
	if err != nil {
		t.Fatal("read blocked an unrelated session", err)
	}
	storageDo(t, other, StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: independent, Manifest: independentManifest})
	unblock()
	err = <-finished
	joined = true
	if err != nil {
		t.Fatal("fenced observation failed", err)
	}
	if _, err := os.Lstat(m.readProcessRoot(input.Preparation.SessionID)); !os.IsNotExist(err) {
		t.Fatal("native read scope was not joined and retired", err)
	}
	storageDo(t, other, input)
}

func TestWorkspaceObservationGateRejectsReadDuringStorage(t *testing.T) {
	m, prepare, manifest := chatExecutionFixture(t)
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	ready, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	m.storageAfterSnapshot = func() { close(ready); <-release }
	go func() { _, err := m.Storage(context.Background(), input); finished <- err }()
	joined := false
	defer func() {
		unblock()
		if !joined {
			<-finished
		}
	}()
	select {
	case <-ready:
	case err := <-finished:
		joined = true
		t.Fatal("storage could not reach publication", err)
	case <-time.After(15 * time.Second):
		t.Fatal("storage did not reach publication")
	}
	other := &Manager{Root: m.Root, Logger: m.Logger}
	if _, err := other.ReadWorkspace(context.Background(), workspaceReadFixture(prepare, manifest, domain.WorkspaceFile, "keep")); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("read entered destructive storage", err)
	}
	unblock()
	err := <-finished
	joined = true
	if err != nil {
		t.Fatal("storage did not finish after read rejection", err)
	}
}

func TestWorkspaceStoragePreservesUnassignedAndUnknownReadOwners(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "session-bound", true: "legacy-unassigned"}[legacy], func(t *testing.T) {
			m, prepare, manifest := chatExecutionFixture(t)
			root := m.readProcessRoot(prepare.SessionID)
			if legacy {
				root = filepath.Join(m.Root, "workspace-read-processes")
			}
			owner := filepath.Join(root, string(domain.NewID()))
			if err := security.PrivateDir(owner); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(owner, "unknown"), []byte("preserve native evidence"), 0600); err != nil {
				t.Fatal(err)
			}
			input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StorageCreate, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest, SnapshotID: domain.NewID()}
			if _, err := m.Storage(context.Background(), input); err != nil {
				t.Fatal("unknown read ownership blocked snapshot", err)
			}
			if _, err := os.Lstat(owner); err != nil {
				t.Fatal("unknown read ownership was erased", err)
			}
			if _, err := os.Lstat(manifest.PrimaryPath); err != nil {
				t.Fatal("source workspace was erased", err)
			}
		})
	}
}
