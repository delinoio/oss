//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestForkCheckpointCapacityIncludesSourceFilesBeforeNativeClaims(t *testing.T) {
	ctx := context.Background()
	api, home := completedCheckpointFixture(t)
	raw, ref, err := api.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source, err := decodeCheckpoint(raw, ref, home)
	if err != nil {
		t.Fatal(err)
	}
	source.NativeRoot, source.Project = "/", "global"
	if forkSourceProfile(source, false) != nil {
		t.Fatal("fixture is not plain General Chat")
	}
	directory := home
	for range 6 {
		directory = filepath.Join(directory, strings.Repeat("&", 100))
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for index := range 1750 {
		name := fmt.Sprintf("%04d-%s", index, strings.Repeat("&", 155))
		if err := os.WriteFile(filepath.Join(directory, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source.Files, err = checkpointFiles(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(source)
	ref.SHA256 = mutationDigest(raw)
	if err != nil || len(raw) <= 7<<20 || len(raw) >= maxCheckpointBytes || InspectCheckpoint(ctx, home, raw, ref) != nil {
		t.Fatal("large original private checkpoint must remain independently valid", len(raw), err)
	}
	if domain.SafeError(InspectForkSourceCheckpoint(ctx, home, raw, ref, false)).Code != domain.ResourceExhausted {
		t.Fatal("complete child envelope did not reject capacity before copying")
	}
	config := fixtureOwnedAPIConfig(t)
	config.Workspace, config.Settings.Agent, config.Settings.Permission = source.Workspace, BuildAgent, []PermissionRule{}
	before, err := os.ReadDir(filepath.Dir(config.Probe.Home))
	if err != nil {
		t.Fatal(err)
	}
	claims := 0
	config.Claim = func(context.Context, SessionClaim) error { claims++; return nil }
	target, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	requests := ForkRequests{domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), false}
	if _, _, err := PrepareForkAPI(ctx, config, home, raw, ref, target, requests); domain.SafeError(err).Code != domain.ResourceExhausted || claims != 0 {
		t.Fatal("oversized child claimed or launched a native replacement", claims, err)
	}
	if entries, err := os.ReadDir(filepath.Dir(config.Probe.Home)); err != nil || len(entries) != len(before) {
		t.Fatal("capacity rejection created runtime state", err)
	}
	// A smaller file inventory can still overflow only after duplicated
	// histories and the complete identity map are included in the child proof.
	for index := 1250; index < 1750; index++ {
		name := fmt.Sprintf("%04d-%s", index, strings.Repeat("&", 155))
		if err := os.Remove(filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
	}
	source.Files, err = checkpointFiles(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	source.History.Messages = nil
	for index := range 1000 {
		message := HistoryMessage{ID: fmt.Sprintf("msg_%012xabcdefghijklmn", index), Role: AssistantMessageRole, Digest: strings.Repeat("e", 64), Parts: []HistoryPart{}}
		if index == 0 {
			message.Role = UserMessageRole
		}
		parts := 4
		if index == 0 {
			parts = 1
		}
		for part := range parts {
			message.Parts = append(message.Parts, HistoryPart{ID: fmt.Sprintf("prt_%012xabcdefghijklmn", index*4+part), Kind: TextPartKind, Digest: strings.Repeat("d", 64)})
		}
		source.History.Messages = append(source.History.Messages, message)
	}
	source.History.InputID, source.History.AssistantID, source.History.Digest = source.History.Messages[0].ID, source.History.Messages[999].ID, ""
	historyBytes, _ := json.Marshal(source.History)
	source.History.Digest = mutationDigest(historyBytes)
	source.Reference.InputID, source.Reference.PartID, source.Reference.HistorySHA256 = source.History.InputID, source.History.Messages[0].Parts[0].ID, source.History.Digest
	ref = source.Reference
	raw, _ = json.Marshal(source)
	ref.SHA256 = mutationDigest(raw)
	if len(raw) >= maxCheckpointBytes || InspectCheckpoint(ctx, home, raw, ref) != nil || forkSourceProfile(source, false) != nil {
		t.Fatal("complete large history source must fit", len(raw), validCheckpointHistory(source.History), validCheckpointLineage(source), InspectCheckpoint(ctx, home, raw, ref), forkSourceProfile(source, false))
	}
	if domain.SafeError(InspectForkSourceCheckpoint(ctx, home, raw, ref, false)).Code != domain.ResourceExhausted {
		t.Fatal("duplicated child histories were omitted from reservation")
	}
	if err := os.RemoveAll(filepath.Join(home, strings.Repeat("&", 100))); err != nil {
		t.Fatal(err)
	}
	source.Files, err = checkpointFiles(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(source)
	ref.SHA256 = mutationDigest(raw)
	if InspectForkSourceCheckpoint(ctx, home, raw, ref, false) != nil {
		t.Fatal("small complete child envelope rejected")
	}
}

// Reasoning parts are ordinary retained history entries: profile v1 still
// rejects the source, profile v2 admits it under the same capacity reservation.
func TestForkSourceProfileAdmitsReasoningOnlyUnderV2(t *testing.T) {
	ctx := context.Background()
	api, home := completedCheckpointFixture(t)
	raw, ref, err := api.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source, err := decodeCheckpoint(raw, ref, home)
	if err != nil {
		t.Fatal(err)
	}
	source.NativeRoot, source.Project = "/", "global"
	last := &source.History.Messages[len(source.History.Messages)-1]
	last.Parts = append([]HistoryPart{{ID: "prt_000000000000abcdefghijklmn", Kind: ReasoningPartKind, Digest: strings.Repeat("d", 64)}}, last.Parts...)
	source.History.Digest = ""
	historyBytes, _ := json.Marshal(source.History)
	source.History.Digest = mutationDigest(historyBytes)
	source.Reference.HistorySHA256 = source.History.Digest
	if domain.SafeError(forkSourceProfile(source, false)).Code != domain.Unsupported {
		t.Fatal("profile v1 admitted a reasoning source")
	}
	if err := forkSourceProfile(source, true); err != nil {
		t.Fatal("profile v2 rejected a plain reasoning source", err)
	}
	if err := checkForkCheckpointCapacity(source, true); err != nil {
		t.Fatal("one reasoning part must fit the shared capacity reservation", err)
	}
}
