// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
)

func TestDesktopExecutableWaitsForEveryIncompleteUpdatePhase(t *testing.T) {
	for _, phase := range []UpdatePhase{UpdateClaiming, UpdateClaimed, UpdatePrepared, UpdateStarting, UpdateRollback, UpdateUnknown} {
		t.Run(string(phase), func(t *testing.T) {
			root, _ := lifecycleFixture(t)
			if selected, err := DesktopExecutable(root, "original"); err != nil || selected != "original" {
				t.Fatal("absent update changed executable", selected, err)
			}
			claim := domain.NewID()
			journal := UpdateJournal{Version: 1, ID: domain.NewID(), ClaimID: claim, ReportID: domain.NewID(), OldGeneration: domain.NewID(), ExpectedRevision: 1, Phase: phase, Operation: updates.Operation{ServerID: domain.NewID(), DeviceID: domain.NewID(), MachineID: domain.NewID(), Component: updates.Worker, Target: updates.Targets[0], CurrentVersion: "0.1.0", Version: "0.2.0", Manifest: []byte("{}"), ManifestSHA256: strings.Repeat("0", 64), ClaimRequestID: claim, ClaimedRevision: 1, ClaimedInstance: domain.NewID()}}
			if err := WriteUpdateJournal(root, journal); err != nil {
				t.Fatal(err)
			}
			if selected, err := DesktopExecutable(root, "original"); err == nil || selected != "" {
				t.Fatal("pending updater admitted desktop replacement", selected, err)
			}
		})
	}
}
