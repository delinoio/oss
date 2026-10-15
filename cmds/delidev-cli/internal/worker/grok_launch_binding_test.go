// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// The launch binding table (grok_launch_binding.go). The credential reaches only
// the snapshot whose bytes were hashed while they were copied:
//
//	event                                          | bind                 | prepare (credential)  | launched bytes
//	admitted bytes at the path                     | snapshot created     | allowed               | the admitted bytes
//	installed path replaced after the bind         | unaffected           | allowed               | the admitted bytes (the snapshot)
//	installed path replaced before the bind        | refused, no snapshot | not reached           | none
//	snapshot rewritten in place after the bind     | created              | refused, nothing written | none
//	snapshot replaced by another file after bind   | created              | refused, nothing written | none
//	execution ends                                 | snapshot removed     |                       |

func launchDirectory(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "journal")
	if err := security.PrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGrokLaunchBindingLaunchesOnlyTheSnapshotOfTheAdmittedBytes(t *testing.T) {
	f := newGrokExecutionServer(t)
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	dir := launchDirectory(t)
	launch, err := run.bindLaunch(context.Background(), dir)
	if err != nil {
		t.Fatalf("the admitted bytes were refused: %v", err)
	}
	if launch == run.installation.ResolvedPath || filepath.Dir(filepath.Dir(launch)) != dir {
		t.Fatalf("the launch path is not a private snapshot: %s", launch)
	}
	info, err := os.Stat(launch)
	if err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the snapshot is reachable by others: %v %v", info, err)
	}
	// The installed path is replaced after the bind: the snapshot is what runs.
	if err := os.WriteFile(run.installation.ResolvedPath, []byte("#!/bin/sh\necho replaced\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := run.prepare(context.Background()); err != nil {
		t.Fatalf("the verified snapshot was refused after the installed path changed: %v", err)
	}
	if raw, err := os.ReadFile(launch); err != nil || string(raw) != "#!/bin/sh\nexit 0\n" {
		t.Fatalf("the snapshot does not hold the admitted bytes: %q %v", raw, err)
	}
	if err := run.finish(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(launch); !os.IsNotExist(err) {
		t.Fatal("the snapshot outlived the execution")
	}
}

func TestGrokLaunchBindingRefusesBytesReplacedBeforeTheBind(t *testing.T) {
	f := newGrokExecutionServer(t)
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(run.installation.ResolvedPath, []byte("#!/bin/sh\necho replaced\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	dir := launchDirectory(t)
	if _, err := run.bindLaunch(context.Background(), dir); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("replaced bytes were snapshotted: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(dir, grokLaunchName)); err != nil || len(entries) != 0 {
		t.Fatalf("a refused bind left a snapshot behind: %v %v", entries, err)
	}
	if run.launchPath != "" {
		t.Fatal("a refused bind recorded a launch path")
	}
	if _, err := os.Lstat(f.authPath()); !os.IsNotExist(err) {
		t.Fatal("a credential was written for replaced bytes")
	}
	if err := run.release(true); err != nil {
		t.Fatal(err)
	}
}

func TestGrokLaunchBindingRefusesASnapshotChangedBeforeTheCredential(t *testing.T) {
	for name, tamper := range map[string]func(t *testing.T, launch string){
		"rewritten in place": func(t *testing.T, launch string) {
			// The same size, different bytes: only the digest can tell.
			if err := os.WriteFile(launch, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"replaced by another file": func(t *testing.T, launch string) {
			other := launch + ".other"
			if err := os.WriteFile(other, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			// Identical bytes, different file: the identity evidence refuses it.
			if err := os.Rename(other, launch); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newGrokExecutionServer(t)
			run, err := f.begin(t)
			if err != nil {
				t.Fatal(err)
			}
			launch, err := run.bindLaunch(context.Background(), launchDirectory(t))
			if err != nil {
				t.Fatal(err)
			}
			tamper(t, launch)
			if _, err := run.prepare(context.Background()); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatalf("a changed snapshot was accepted: %v", err)
			}
			if _, err := os.Lstat(f.authPath()); !os.IsNotExist(err) {
				t.Fatal("a credential was written for a changed snapshot")
			}
			if err := run.release(true); err != nil {
				t.Fatal(err)
			}
			request := f.onlyFinish(t)
			if request.Succeeded || !request.CleanupConfirmed || len(request.Bundle) != 0 {
				t.Fatalf("the refused lease was not released with confirmed cleanup: %+v", request)
			}
		})
	}
}
