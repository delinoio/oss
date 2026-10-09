// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestDesktopOriginalOutcomeOfflineSettlement(t *testing.T) {
	for _, phase := range []desktopUpdatePhase{desktopInstalled, desktopFailed, desktopUnknown} {
		t.Run(string(phase), func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "desktop-updates")
			if err := security.PrivateDir(directory); err != nil {
				t.Fatal(err)
			}
			original := desktopUpdateJournal{Version: 1, ID: domain.NewID(), ServerID: domain.NewID(), Revision: 7, Generation: domain.NewID(), SavedConnection: domain.NewID(), Phase: desktopInstalling}
			path := filepath.Join(directory, string(original.ID)+".json")
			raw, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			if err := security.WriteAtomic(path, raw); err != nil {
				t.Fatal(err)
			}
			args := []string{"native-outcome", "--id", string(original.ID), "--server-id", string(original.ServerID), "--revision", strconv.FormatUint(original.Revision, 10), "--native-generation", string(original.Generation), "--saved-connection", string(original.SavedConnection), "--outcome", string(phase)}
			lock, err := security.TryLock(filepath.Join(directory, "control.lock"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := nativeDesktopUpdate(context.Background(), options{dataDir: root}, args); err == nil {
				t.Fatal("held original journal gate admitted a write")
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				if _, err := nativeDesktopUpdate(context.Background(), options{dataDir: root}, args); err != nil {
					t.Fatal("offline original/same-phase settlement", err)
				}
			}
			read, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var actual desktopUpdateJournal
			if err := json.Unmarshal(read, &actual); err != nil {
				t.Fatal(err)
			}
			original.Phase = phase
			want, _ := json.Marshal(original)
			got, _ := json.Marshal(actual)
			if string(got) != string(want) {
				t.Fatal("settlement changed original journal ownership")
			}
			for _, index := range []int{2, 4, 6, 8, 10, 12} {
				changed := append([]string(nil), args...)
				if index == 6 {
					changed[index] = "8"
				} else if index == 12 {
					if phase == desktopFailed {
						changed[index] = string(desktopInstalled)
					} else {
						changed[index] = string(desktopFailed)
					}
				} else {
					changed[index] = string(domain.NewID())
				}
				if _, err := nativeDesktopUpdate(context.Background(), options{dataDir: root}, changed); err == nil {
					t.Fatal("changed original authority or nonterminal outcome accepted", index)
				}
			}
			unchanged, err := os.ReadFile(path)
			if err != nil || string(unchanged) != string(read) {
				t.Fatal("rejected settlement changed original journal", err)
			}
		})
	}
}
