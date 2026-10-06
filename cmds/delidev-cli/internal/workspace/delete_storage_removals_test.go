// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func deletionRemovalFixture(t *testing.T, action StorageAction) (*Manager, StorageRequest, domain.SessionDeletionWork) {
	t.Helper()
	m := manager(t)
	prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := m.Prepare(context.Background(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "z"} {
		if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, name), []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.OperationID, input.SnapshotID, input.Action, input.PreviewDigest = domain.NewID(), domain.NewID(), action, preview.PreviewDigest
	work := domain.SessionDeletionWork{Version: 1, DeletionID: domain.NewID(), ServerID: domain.NewID(), SessionID: prepare.SessionID, MachineID: prepare.MachineID, DeviceID: domain.NewID(), PreparationDigests: []string{manifest.InputDigest}}
	copyFor := func() domain.SessionDeletionCopy {
		return domain.SessionDeletionCopy{JobID: input.OperationID, Type: domain.WorkspaceStorageJob, SnapshotID: input.SnapshotID, InstanceID: domain.NewID(), Revision: 2, Digest: strings.Repeat("a", 64)}
	}
	if action == StorageDelete {
		input.Action = StorageCreate
		created := storageDo(t, m, input)
		work.Copies = append(work.Copies, copyFor())
		input.Action, input.OperationID, input.SnapshotDigest, input.SnapshotMetadata = action, domain.NewID(), created.Snapshot.SHA256, created.Snapshot
	}
	work.Copies = append(work.Copies, copyFor())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	trigger := "chat/z"
	if action == StorageDelete {
		trigger = "workspace/" + trigger
	}
	interrupted := false
	m.storageAfterRemovalClaim = func(name string) {
		if name == trigger {
			interrupted = true
			cancel()
		}
	}
	if _, err := m.Storage(ctx, input); !interrupted || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("fixture did not interrupt real claimed removal", err)
	}
	if err := security.PrivateDir(filepath.Join(m.Root, "session-deletions")); err != nil {
		t.Fatal(err)
	}
	// Restart from durable proof only, with no original callbacks or native run.
	return &Manager{Root: m.Root, Logger: m.Logger}, input, work
}

func deletionRemovalEntry(t *testing.T, m *Manager, input StorageRequest, logical string) string {
	t.Helper()
	raw, err := os.ReadFile(m.removalIntentPath(input.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	_, pending, err := m.readRemovalClaimPending(input, raw)
	if err != nil {
		t.Fatal(err)
	}
	removal := filepath.Join(m.Root, "workspace-removals", string(input.OperationID))
	physical, longest := logical, 0
	for _, rename := range pending {
		if (logical == rename.Original || strings.HasPrefix(logical, rename.Original+"/")) && len(rename.Original) > longest {
			candidate := rename.Private + strings.TrimPrefix(logical, rename.Original)
			// Cancellation can restore a name before its journal-clear append.
			// Prefer the deepest mapping that still names the original entry.
			if _, err := os.Lstat(filepath.Join(removal, filepath.FromSlash(candidate))); err == nil {
				physical, longest = candidate, len(rename.Original)
			}
		}
	}
	result := filepath.Join(removal, filepath.FromSlash(physical))
	if _, err := os.Lstat(result); err != nil {
		t.Fatal("fixture lost its retained pinned entry", err)
	}
	return result
}

func deletionRemovalProof(t *testing.T, m *Manager, input StorageRequest) map[string][]byte {
	t.Helper()
	proof := map[string][]byte{}
	for _, path := range []string{m.removalIntentPath(input.OperationID), m.removalClaimPath(input.OperationID), m.removalClaimJournalPath(input.OperationID)} {
		raw, err := os.ReadFile(path)
		if err == nil {
			proof[path] = raw
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	return proof
}

func assertDeletionRemovalProof(t *testing.T, proof map[string][]byte) {
	t.Helper()
	for path, original := range proof {
		if raw, err := os.ReadFile(path); err != nil || !bytes.Equal(raw, original) {
			t.Fatal("permanent deletion changed still-needed original proof", err)
		}
	}
}

func TestPermanentDeletionPreservesChangedRemovalNamespaces(t *testing.T) {
	for _, action := range []StorageAction{StorageCleanup, StorageDelete} {
		for _, change := range []string{"root", "new-entry", "changed-entry"} {
			t.Run(string(action)+"/"+change, func(t *testing.T) {
				m, input, work := deletionRemovalFixture(t, action)
				removal := filepath.Join(m.Root, "workspace-removals", string(input.OperationID))
				foreign := filepath.Join(removal, "foreign")
				switch change {
				case "root":
					if err := os.Rename(removal, removal+"-original"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(removal, 0700); err != nil {
						t.Fatal(err)
					}
				case "changed-entry":
					logical := "chat/z"
					if action == StorageDelete {
						logical = "workspace/" + logical
					}
					foreign = deletionRemovalEntry(t, m, input, logical)
				}
				if err := os.WriteFile(foreign, []byte("preserve replacement bytes"), 0600); err != nil {
					t.Fatal(err)
				}
				proof := deletionRemovalProof(t, m, input)
				called := false
				if err := m.DeleteOwnedWorkspace(context.Background(), work, false, func() error { called = true; return nil }); domain.SafeError(err).Code != domain.RecoveryRequired || called {
					t.Fatal("changed removal namespace acquired permanent deletion authority", err)
				}
				if raw, err := os.ReadFile(foreign); err != nil || string(raw) != "preserve replacement bytes" {
					t.Fatal("foreign removal bytes changed", err)
				}
				assertDeletionRemovalProof(t, proof)
			})
		}
	}
}

func TestPermanentDeletionRequiresOriginalRemovalProof(t *testing.T) {
	for _, change := range []string{"missing-intent", "malformed-intent", "missing-claim", "malformed-claim", "legacy-claim", "intent-digest", "root-identity", "session", "operation", "snapshot", "malformed-journal"} {
		t.Run(change, func(t *testing.T) {
			m, input, work := deletionRemovalFixture(t, StorageCleanup)
			entryPath := deletionRemovalEntry(t, m, input, "chat/z")
			claimPath, intentPath := m.removalClaimPath(input.OperationID), m.removalIntentPath(input.OperationID)
			switch change {
			case "missing-intent", "missing-claim":
				path := intentPath
				if change == "missing-claim" {
					path = claimPath
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "malformed-intent", "malformed-claim", "malformed-journal":
				path := intentPath
				if change == "malformed-claim" {
					path = claimPath
				} else if change == "malformed-journal" {
					path = m.removalClaimJournalPath(input.OperationID)
				}
				if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			default:
				raw, err := os.ReadFile(claimPath)
				var claim storageRemovalClaim
				if err != nil || json.Unmarshal(raw, &claim) != nil {
					t.Fatal(err)
				}
				switch change {
				case "legacy-claim":
					claim.Version = 1
				case "intent-digest":
					claim.IntentDigest = strings.Repeat("b", 64)
				case "root-identity":
					claim.RootIdentity = strings.Repeat("b", 64)
				case "session":
					claim.Reference.SessionID = domain.NewID()
				case "operation":
					claim.Reference.OperationID = domain.NewID()
				case "snapshot":
					claim.Reference.SnapshotID = domain.NewID()
				}
				raw, err = json.Marshal(claim)
				if err != nil || os.WriteFile(claimPath, raw, 0600) != nil {
					t.Fatal("could not replace fixture claim", err)
				}
			}
			proof := deletionRemovalProof(t, m, input)
			if err := m.DeleteOwnedWorkspace(context.Background(), work, false, func() error { t.Fatal("invalid proof reached copy cleanup"); return nil }); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("missing or mismatched proof authorized deletion", err)
			}
			assertDeletionRemovalProof(t, proof)
			if _, err := os.Stat(filepath.Join(m.Root, "workspace-removals", string(input.OperationID))); err != nil {
				t.Fatal("invalid proof namespace removed", err)
			}
			if raw, err := os.ReadFile(entryPath); err != nil || string(raw) != "original" {
				t.Fatal("invalid proof unlinked a pinned entry", err)
			}
		})
	}
}

func TestPermanentDeletionResumesOriginalPartialRemoval(t *testing.T) {
	for _, action := range []StorageAction{StorageCleanup, StorageDelete} {
		for _, replay := range []string{"renamed", "prepared", "compacted"} {
			t.Run(string(action)+"/"+replay, func(t *testing.T) {
				m, input, work := deletionRemovalFixture(t, action)
				switch replay {
				case "prepared":
					path := m.removalClaimJournalPath(input.OperationID)
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					// Native rename committed just before its durable committed record.
					raw = raw[:bytes.LastIndexByte(raw[:len(raw)-1], '\n')+1]
					if err := os.WriteFile(path, raw, 0600); err != nil {
						t.Fatal(err)
					}
				case "compacted":
					if err := m.compactRemovalClaimJournal(context.Background(), input); err != nil {
						t.Fatal(err)
					}
				}
				removal := filepath.Join(m.Root, "workspace-removals", string(input.OperationID))
				called := false
				if err := m.DeleteOwnedWorkspace(context.Background(), work, false, func() error {
					called = true
					if _, err := os.Lstat(removal); !os.IsNotExist(err) {
						t.Fatal("copy callback preceded validated removal", err)
					}
					if len(deletionRemovalProof(t, m, input)) != 3 {
						t.Fatal("proof retired before its last use")
					}
					return nil
				}); err != nil || !called {
					t.Fatal("original partial removal could not finish", err)
				}
			})
		}
	}
}
