// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func appsTestBool(value bool) *bool { return &value }

func TestAppsSnapshotKeepsNativeFactsSeparateFromSelection(t *testing.T) {
	selection := domain.CodexAppConfiguration{Version: 1, SessionID: domain.NewID(), AccountID: domain.NewID(), Generation: domain.NewID(), AppIDs: []string{"available", "callable", "missing"}}
	directory := []domain.CodexApp{{ID: "available", Name: "Available", Discovered: true, Accessible: true}, {ID: "callable", Name: "Callable", Discovered: true, Accessible: true}}
	installed := []installedAppRow{{ID: "callable", Enabled: appsTestBool(true), Callable: appsTestBool(true)}}
	rows, err := joinAppsSnapshot(selection, directory, installed, map[string]appReadRow{})
	if err != nil || len(rows) != 3 {
		t.Fatalf("snapshot: %#v, %v", rows, err)
	}
	if !rows[0].Selected || !rows[0].Discovered || rows[0].Installed || rows[0].Callable || !rows[1].Callable || !rows[2].Selected || rows[2].Discovered || rows[2].Installed || rows[2].Callable {
		t.Fatal("selection or discovery fabricated native authority")
	}
	if _, err := joinAppsSnapshot(selection, directory, installed, map[string]appReadRow{"foreign": {ID: "foreign", Name: "Foreign"}}); err == nil {
		t.Fatal("foreign read metadata accepted")
	}
}

func TestAppsRemovalRequiresNewOriginalGenerationAndFreshNegativeFacts(t *testing.T) {
	previous := domain.CodexAppConfiguration{Version: 1, SessionID: domain.NewID(), AccountID: domain.NewID(), Generation: domain.NewID(), AppIDs: []string{"retained", "removed"}}
	next := previous.Clone()
	next.Generation, next.AppIDs = domain.NewID(), []string{"retained"}
	fresh := []installedAppRow{{ID: "retained", Enabled: appsTestBool(true), Callable: appsTestBool(true)}, {ID: "removed", Enabled: appsTestBool(false), Callable: appsTestBool(false)}}
	if !appsRemovalObserved(previous, next, fresh) {
		t.Fatal("original refreshed removal rejected")
	}
	fresh[1].Enabled = appsTestBool(true)
	if appsRemovalObserved(previous, next, fresh) || appsRemovalObserved(previous, next, nil) {
		t.Fatal("stale or missing catalog accepted as revocation")
	}
	fresh[1].Enabled = appsTestBool(false)
	foreign := next.Clone()
	foreign.AccountID = domain.NewID()
	if appsRemovalObserved(previous, foreign, fresh) || appsRemovalObserved(previous, previous, fresh) {
		t.Fatal("foreign account or reused generation accepted")
	}
	foreign = next.Clone()
	foreign.AppIDs = append(foreign.AppIDs, "new")
	if appsRemovalObserved(previous, foreign, fresh) {
		t.Fatal("live positive authority expansion accepted")
	}
}
