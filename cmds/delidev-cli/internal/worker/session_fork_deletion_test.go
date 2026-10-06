// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestSessionForkDeletionRemovesOnlyBoundChildRuntime(t *testing.T) {
	config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	work.Copies = nil
	runtimeID, sourceJob := domain.NewID(), domain.NewID()
	checkpoint := ForkCheckpoint{Version: 1, JobID: sourceJob, RuntimeID: runtimeID, SessionID: work.SessionID, MachineID: work.MachineID, JobInputDigest: strings.Repeat("ab", 32)}
	raw := mustForkJSON(checkpoint)
	home := filepath.Join(config.Root, "runtimes", string(runtimeID))
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(home, "fork-completion.json"), raw); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(config.Root, "runtimes", string(domain.NewID()))
	if err := security.PrivateDir(parent); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(parent, "keep.json"), []byte("parent history")); err != nil {
		t.Fatal(err)
	}
	work.Fork = &domain.SessionDeletionFork{JobID: sourceJob, RuntimeID: runtimeID, JobInputDigest: checkpoint.JobInputDigest, CheckpointDigest: executionInputDigest(raw)}
	proof, err := deleteSessionCopies(context.Background(), config, work)
	if err != nil || !proof.Complete {
		t.Fatal("child cleanup failed", err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("unstarted child runtime leaked", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "keep.json")); err != nil {
		t.Fatal("child cleanup deleted parent history", err)
	}
	if _, err := deleteSessionCopies(context.Background(), config, work); err != nil {
		t.Fatal("cleanup replay changed ownership", err)
	}
}

func TestSessionOpenCodeForkDeletionRetainsCheckpointSizedDecoding(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "original", true: "replaced"}[changed], func(t *testing.T) {
			config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
			work.Copies = nil
			runtimeID, sourceJob := domain.NewID(), domain.NewID()
			checkpoint := openCodeForkCheckpoint{Version: 2, JobID: sourceJob, RuntimeID: runtimeID, SessionID: work.SessionID, MachineID: work.MachineID, JobInputDigest: strings.Repeat("ab", 32), Native: json.RawMessage(`{"retained":"` + strings.Repeat("x", (1<<20)+1) + `"}`)}
			raw := mustForkJSON(checkpoint)
			if len(raw) <= 1<<20 || len(raw) > maxOpenCodeExecutionCheckpointBytes {
				t.Fatal("fixture missed declared checkpoint range")
			}
			home := filepath.Join(config.Root, "runtimes", string(runtimeID))
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
			work.Fork = &domain.SessionDeletionFork{JobID: sourceJob, RuntimeID: runtimeID, JobInputDigest: checkpoint.JobInputDigest, CheckpointDigest: executionInputDigest(raw)}
			if changed {
				checkpoint.SessionID = domain.NewID()
				raw = mustForkJSON(checkpoint)
			}
			if err := security.WriteAtomic(filepath.Join(home, "fork-completion.json"), raw); err != nil {
				t.Fatal(err)
			}
			proof, err := deleteSessionCopies(context.Background(), config, work)
			if changed {
				if err == nil || proof.Complete {
					t.Fatal("replacement checkpoint granted cleanup")
				}
				if _, err := os.Stat(home); err != nil {
					t.Fatal("replacement runtime removed", err)
				}
			} else if err != nil || !proof.Complete {
				t.Fatal("bounded original fork could not be deleted", err)
			}
		})
	}
}
