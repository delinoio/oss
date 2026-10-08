// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSkillReadRejectsExpiredOrUnboundedObservationBeforeFilesystemAccess(t *testing.T) {
	machine := domain.NewID()
	for _, deadline := range []time.Time{{}, time.Now().Add(-time.Minute), time.Now().Add(time.Minute)} {
		root := filepath.Join(t.TempDir(), "untouched")
		manager := Manager{Root: root}
		request := ReadRequest{Deadline: deadline, Preparation: PrepareRequest{MachineID: machine}, Skills: &domain.SkillReadRequest{MachineID: machine, ActorID: domain.NewID()}}
		if _, err := manager.ReadSkills(context.Background(), request); err == nil {
			t.Fatal("invalid observation deadline accepted")
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("invalid observation touched filesystem", err)
		}
	}
}
