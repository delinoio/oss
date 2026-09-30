// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strconv"
	"strings"
	"testing"
)

func deletionWorkWithCopies(count int) SessionDeletionWork {
	work := SessionDeletionWork{Version: 1, DeletionID: NewID(), ServerID: NewID(), SessionID: NewID(), MachineID: NewID(), DeviceID: NewID()}
	for range count {
		work.Copies = append(work.Copies, SessionDeletionCopy{JobID: NewID(), Type: PrepareWorkspaceJob, Revision: 1, Digest: strings.Repeat("a", 64), InstanceID: NewID()})
	}
	return work
}

func TestSessionDeletionWorkHonorsFullCopyCapacity(t *testing.T) {
	for _, count := range []int{1000, 1001, 4096} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			if err := deletionWorkWithCopies(count).Validate(); err != nil {
				t.Fatal("valid bounded deletion work rejected", err)
			}
		})
	}
	if err := deletionWorkWithCopies(4097).Validate(); err == nil {
		t.Fatal("oversized deletion work accepted")
	}
	for _, invalid := range []ID{"invalid", "duplicate"} {
		work := deletionWorkWithCopies(4096)
		work.Copies[4095].JobID = invalid
		if invalid == "duplicate" {
			work.Copies[4095].JobID = work.Copies[0].JobID
		}
		if err := work.Validate(); err == nil {
			t.Fatal("invalid large deletion work accepted", invalid)
		}
	}
}
