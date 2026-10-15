package nativeprofile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func testRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func workerRegistry(t *testing.T, root string, logs *bytes.Buffer) (*Registry, domain.ID) {
	t.Helper()
	machine := domain.NewID()
	var logger *slog.Logger
	if logs != nil {
		logger = slog.New(slog.NewJSONHandler(logs, nil))
	}
	r, err := Open(Config{Root: root, Owner: OwnerWorker, Server: domain.NewID(), Machine: machine, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	return r, machine
}

func conversationKey(account, conversation domain.ID) Key {
	return Key{Harness: domain.Codex, Purpose: PurposeConversation, Account: account, Conversation: conversation}
}

func TestAcquireReusesTheSameProfileAfterRestart(t *testing.T) {
	root := testRoot(t)
	var logs bytes.Buffer
	r, _ := workerRegistry(t, root, &logs)
	key := conversationKey(domain.NewID(), domain.NewID())
	first, err := r.Acquire(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.ID.Validate() != nil {
		t.Fatalf("first=%+v", first.Info)
	}
	marker := filepath.Join(first.Root, "history.txt")
	if err := os.WriteFile(marker, []byte("kept"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// A different Registry value over the same root models an owner restart.
	restarted, err := Open(r.config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := restarted.Acquire(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.Created || second.ID != first.ID || second.Root != first.Root {
		t.Fatalf("restart changed profile: %+v %+v", first.Info, second.Info)
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "kept" {
		t.Fatalf("history was not retained: %v", err)
	}
	if !strings.Contains(logs.String(), `"reason":"created"`) || !strings.Contains(logs.String(), `"reason":"reused"`) || strings.Contains(logs.String(), root) {
		t.Fatalf("unexpected logs: %s", logs.String())
	}
}

func TestProfilesAreIsolatedByAccountPurposeAndConversation(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	ctx := context.Background()
	account, other, conversation := domain.NewID(), domain.NewID(), domain.NewID()
	keys := []Key{
		conversationKey(account, conversation),
		conversationKey(account, domain.NewID()),
		conversationKey(other, conversation),
		{Harness: domain.Codex, Purpose: PurposeQuota, Account: account},
		{Harness: domain.Codex, Purpose: PurposeCredit, Account: account},
		{Harness: domain.Codex, Purpose: PurposeAuth, Account: account},
		{Harness: domain.ClaudeCode, Purpose: PurposeConversation, Account: account, Conversation: conversation},
		{Harness: domain.Codex, Purpose: PurposeDiscovery},
		{Harness: domain.OpenCode, Purpose: PurposeDiscovery},
	}
	seen := map[string]bool{}
	for _, key := range keys {
		lease, err := r.Acquire(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Close()
		if seen[lease.Root] {
			t.Fatalf("profile shared: %+v", key)
		}
		seen[lease.Root] = true
	}
	other2, _ := workerRegistry(t, testRoot(t), nil)
	lease, err := other2.Acquire(ctx, keys[0])
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if seen[lease.Root] {
		t.Fatal("Workers share a profile")
	}
}

func TestConcurrentUseOfOneProfileBlocksWithSpecificCause(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	key := conversationKey(domain.NewID(), domain.NewID())
	held, err := r.Acquire(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Acquire(context.Background(), key)
	if reason, ok := Reasoned(err); !ok || reason != ReasonBusy {
		t.Fatalf("err=%v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := r.Acquire(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
}

func TestRetainedHelperLinksNeverBlockLaterAcquire(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	key := Key{Harness: domain.Codex, Purpose: PurposeQuota, Account: domain.NewID()}
	lease, err := r.Acquire(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(lease.Root, "helper")); err != nil {
		t.Skip("symlinks unavailable")
	}
	lease.Close()
	next, err := r.Acquire(context.Background(), key)
	if err != nil {
		t.Fatalf("helper symlink blocked reuse: %v", err)
	}
	next.Close()
	if _, err := os.Lstat(filepath.Join(next.Root, "helper")); err != nil {
		t.Fatal("helper link was removed")
	}
}

func TestInvalidOwnerRecordAndReplacedDirectoryBlock(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	key := conversationKey(domain.NewID(), domain.NewID())
	lease, err := r.Acquire(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	lease.Close()
	owner := filepath.Join(filepath.Dir(lease.Root), "owner.json")
	for name, body := range map[string]string{"garbage": "{", "future": `{"version":2}`, "other": `{"version":1,"id":"` + string(domain.NewID()) + `","key":{}}`} {
		if err := os.WriteFile(owner, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := r.Acquire(context.Background(), key)
		reason, ok := Reasoned(err)
		if !ok || (reason != ReasonOwnerInvalid && reason != ReasonIncompatible) {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
	if err := os.RemoveAll(lease.Root); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, lease.Root); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := r.Acquire(context.Background(), key); err == nil {
		t.Fatal("symlinked profile accepted")
	}
}

func TestIncompatibleRegistryVersionIsRejectedWithoutRewrite(t *testing.T) {
	root := testRoot(t)
	r, _ := workerRegistry(t, root, nil)
	key := conversationKey(domain.NewID(), domain.NewID())
	lease, err := r.Acquire(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	lease.Close()
	path := r.registryPath()
	future := []byte(`{"version":9,"profiles":[]}`)
	if err := os.WriteFile(path, future, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = r.Acquire(context.Background(), key)
	if reason, ok := Reasoned(err); !ok || reason != ReasonIncompatible {
		t.Fatalf("err=%v", err)
	}
	if raw, _ := os.ReadFile(path); !bytes.Equal(raw, future) {
		t.Fatal("incompatible registry was rewritten")
	}
}

func TestRegisterLegacyKeepsHistoryInPlaceAndResolvesHistory(t *testing.T) {
	root := testRoot(t)
	r, _ := workerRegistry(t, root, nil)
	execution := domain.NewID()
	legacy := filepath.Join(root, "runtimes", string(execution))
	if err := os.MkdirAll(filepath.Join(legacy, "codex"), 0700); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(legacy, "codex", "rollout.jsonl")
	if err := os.WriteFile(rollout, []byte("history"), 0600); err != nil {
		t.Fatal(err)
	}
	key := conversationKey(domain.NewID(), domain.NewID())
	rel := "runtimes/" + string(execution)
	info, err := r.RegisterLegacy(context.Background(), key, rel, execution)
	if err != nil {
		t.Fatal(err)
	}
	if info.Origin != OriginLegacy || info.Root != legacy {
		t.Fatalf("info=%+v", info)
	}
	again, err := r.RegisterLegacy(context.Background(), key, rel, execution)
	if err != nil || again.ID != info.ID {
		t.Fatalf("not idempotent: %v", err)
	}
	if _, err := r.RegisterLegacy(context.Background(), conversationKey(key.Account, domain.NewID()), rel); err == nil {
		t.Fatal("one directory registered for two conversations")
	}
	found, ok, err := r.History(context.Background(), execution)
	if err != nil || !ok || found.Root != legacy {
		t.Fatalf("history: %v %v", ok, err)
	}
	lease, err := r.Acquire(context.Background(), key)
	if err != nil || lease.Root != legacy {
		t.Fatalf("legacy lease: %v", err)
	}
	lease.Close()
	if raw, _ := os.ReadFile(rollout); string(raw) != "history" {
		t.Fatal("history was modified")
	}
	for _, bad := range []string{"", "../x", "/abs", "native-profiles/p/x", "runtimes/missing"} {
		if _, err := r.RegisterLegacy(context.Background(), conversationKey(key.Account, domain.NewID()), bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := os.Lstat(filepath.Join(legacy, "owner.json")); err == nil {
		t.Fatal("legacy directory gained a file")
	}
}

func TestDeleteRefusesReferencedAndActiveProfiles(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	ctx := context.Background()
	account, conversation := domain.NewID(), domain.NewID()
	key := conversationKey(account, conversation)
	lease, err := r.Acquire(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Delete(ctx, DeleteScope{Conversation: conversation}); err == nil {
		t.Fatal("active profile deleted")
	}
	lease.Close()
	ref := Reference{Kind: ReferenceCheckpoint, ID: domain.NewID()}
	if err := r.AddReference(ctx, key, ref); err != nil {
		t.Fatal(err)
	}
	_, err = r.Delete(ctx, DeleteScope{Conversation: conversation})
	if reason, ok := Reasoned(err); !ok || reason != ReasonReferenced {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(lease.Root); err != nil {
		t.Fatal("referenced profile removed")
	}
	if err := r.RemoveReference(ctx, key, ref); err != nil {
		t.Fatal(err)
	}
	if n, err := r.Delete(ctx, DeleteScope{Conversation: conversation}); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, err := os.Lstat(lease.Root); !os.IsNotExist(err) {
		t.Fatal("profile directory remains")
	}
	if _, err := r.Delete(ctx, DeleteScope{}); err == nil {
		t.Fatal("empty scope accepted")
	}
}

func TestKeyValidationRejectsMissingScopeFields(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	for _, key := range []Key{
		{Harness: domain.Codex, Purpose: PurposeQuota},
		{Harness: domain.Codex, Purpose: PurposeConversation, Account: domain.NewID()},
		{Harness: domain.Codex, Purpose: PurposeDiscovery, Account: domain.NewID()},
		{Harness: "other", Purpose: PurposeDiscovery},
		{Harness: domain.Codex, Purpose: "temp"},
		{Owner: OwnerServer, Harness: domain.Codex, Purpose: PurposeDiscovery},
	} {
		if _, err := r.Acquire(context.Background(), key); err == nil {
			t.Fatalf("accepted %+v", key)
		}
	}
}

func TestHistoryIdentifiersKeepTheOriginalAndEvictOldestLater(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	ctx := context.Background()
	key := conversationKey(domain.NewID(), domain.NewID())
	lease, err := r.Acquire(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	original := domain.NewID()
	if err := r.RecordHistory(ctx, key, original); err != nil {
		t.Fatal(err)
	}
	var latest domain.ID
	for i := 0; i < maxHistories+8; i++ {
		latest = domain.NewID()
		if err := r.RecordHistory(ctx, key, latest); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []domain.ID{original, latest} {
		info, ok, err := r.History(ctx, id)
		if err != nil || !ok || info.ID != lease.ID {
			t.Fatalf("history %s was not resolved: %v", id, err)
		}
	}
}

func TestHistoryOwnershipRejectsConflictingBindings(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	ctx := context.Background()
	account := domain.NewID()
	firstKey := conversationKey(account, domain.NewID())
	secondKey := conversationKey(account, domain.NewID())
	for _, key := range []Key{firstKey, secondKey} {
		lease, err := r.Acquire(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		lease.Close()
	}
	history := domain.NewID()
	if err := r.RecordHistory(ctx, firstKey, history); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(r.registryPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RecordHistory(ctx, secondKey, history); err == nil {
		t.Fatal("a second managed profile accepted the same history")
	}
	if err := r.RecordHistory(ctx, firstKey, history); err != nil {
		t.Fatal("same-owner binding was not idempotent", err)
	}
	if after, err := os.ReadFile(r.registryPath()); err != nil || !bytes.Equal(before, after) {
		t.Fatal("conflicting or repeated binding rewrote the registry", err)
	}
	if found, ok, err := r.History(ctx, history); err != nil || !ok || found.Key.Conversation != firstKey.Conversation {
		t.Fatal("history changed owner", err)
	}
	legacy := filepath.Join(r.config.Root, "runtimes", string(domain.NewID()))
	if err := os.MkdirAll(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	relative, _ := filepath.Rel(r.config.Root, legacy)
	if _, err := r.RegisterLegacy(ctx, conversationKey(account, domain.NewID()), filepath.ToSlash(relative), history); err == nil {
		t.Fatal("a legacy profile accepted managed history")
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("conflicting registration moved the legacy directory", err)
	}
	legacyHistory := domain.NewID()
	firstLegacy := filepath.Join(r.config.Root, "runtimes", string(domain.NewID()))
	secondLegacy := filepath.Join(r.config.Root, "runtimes", string(domain.NewID()))
	for _, path := range []string{firstLegacy, secondLegacy} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	firstRelative, _ := filepath.Rel(r.config.Root, firstLegacy)
	secondRelative, _ := filepath.Rel(r.config.Root, secondLegacy)
	if _, err := r.RegisterLegacy(ctx, conversationKey(account, domain.NewID()), filepath.ToSlash(firstRelative), legacyHistory); err != nil {
		t.Fatal(err)
	}
	before, _ = os.ReadFile(r.registryPath())
	if _, err := r.RegisterLegacy(ctx, conversationKey(account, domain.NewID()), filepath.ToSlash(secondRelative), legacyHistory); err == nil {
		t.Fatal("a second legacy profile accepted the same history")
	}
	if after, err := os.ReadFile(r.registryPath()); err != nil || !bytes.Equal(before, after) {
		t.Fatal("conflicting legacy registration rewrote the registry", err)
	}
	for _, path := range []string{firstLegacy, secondLegacy} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("conflicting legacy registration moved or removed a directory", err)
		}
	}
	if n, err := r.Delete(ctx, DeleteScope{History: history}); err != nil || n != 1 {
		t.Fatalf("unique history deletion changed scope: n=%d err=%v", n, err)
	}
	if _, ok, err := r.Lookup(ctx, secondKey); err != nil || !ok {
		t.Fatal("history deletion removed another conversation", err)
	}
}

func TestLoadRejectsDuplicateHistoryWithoutMutation(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		lease, err := r.Acquire(ctx, conversationKey(domain.NewID(), domain.NewID()))
		if err != nil {
			t.Fatal(err)
		}
		lease.Close()
	}
	file, err := r.load()
	if err != nil {
		t.Fatal(err)
	}
	history := domain.NewID()
	file.Profiles[0].Histories = []domain.ID{history}
	file.Profiles[1].Histories = []domain.ID{history}
	writeRawRegistry(t, r, file.Profiles)
	before, _ := os.ReadFile(r.registryPath())
	if _, _, err := r.History(ctx, history); err == nil {
		t.Fatal("ambiguous history was resolved")
	} else if reason, ok := Reasoned(err); !ok || reason != ReasonRegistryInvalid {
		t.Fatal("ambiguous history did not report registry invalid", err)
	}
	if _, err := r.Delete(ctx, DeleteScope{History: history}); err == nil {
		t.Fatal("ambiguous history was deleted")
	}
	if after, err := os.ReadFile(r.registryPath()); err != nil || !bytes.Equal(before, after) {
		t.Fatal("invalid registry was rewritten", err)
	}
	for _, e := range file.Profiles {
		if _, err := os.Stat(r.pathOf(e)); err != nil {
			t.Fatal("an ambiguous owner was removed", err)
		}
	}
}

func writeRawRegistry(t *testing.T, r *Registry, profiles []entry) {
	t.Helper()
	if err := r.prepare(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(registryFile{Version: Version, Profiles: profiles})
	if err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(r.registryPath(), raw); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteKeepsDeletionPendingWhenAnyMatchIsLocked(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	ctx := context.Background()
	account, conversation := domain.NewID(), domain.NewID()
	locked, err := r.Acquire(ctx, Key{Harness: domain.Codex, Purpose: PurposeTitle, Account: account, Conversation: conversation})
	if err != nil {
		t.Fatal(err)
	}
	for _, purpose := range []Purpose{PurposeConversation, PurposeReview, PurposeSidechat} {
		other, err := r.Acquire(ctx, Key{Harness: domain.Codex, Purpose: purpose, Account: account, Conversation: conversation})
		if err != nil {
			t.Fatal(err)
		}
		other.Close()
	}
	n, err := r.Delete(ctx, DeleteScope{Conversation: conversation})
	if reason, ok := Reasoned(err); !ok || reason != ReasonBusy {
		t.Fatalf("a locked match reported success: n=%d err=%v", n, err)
	}
	if n != 3 {
		t.Fatalf("unlocked matches were not committed: n=%d", n)
	}
	if found, err := r.Exists(ctx, DeleteScope{Conversation: conversation}); err != nil || !found {
		t.Fatalf("the locked profile left the registry: %v %v", found, err)
	}
	if _, err := os.Stat(locked.Root); err != nil {
		t.Fatal("locked profile directory removed")
	}
	locked.Close()
	if n, err := r.Delete(ctx, DeleteScope{Conversation: conversation}); err != nil || n != 1 {
		t.Fatalf("retry n=%d err=%v", n, err)
	}
	if found, err := r.Exists(ctx, DeleteScope{Conversation: conversation}); err != nil || found {
		t.Fatalf("profile remains: %v %v", found, err)
	}
}

func TestDeleteReportsAnUnremovableMatch(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	ctx := context.Background()
	key := conversationKey(domain.NewID(), domain.NewID())
	lease, err := r.Acquire(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(lease.Root)
	lease.Close()
	// Replace the retained directory with a regular file: it is not a real
	// directory, so removal is refused and must never be acknowledged.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	n, err := r.Delete(ctx, DeleteScope{Conversation: key.Conversation})
	if reason, ok := Reasoned(err); !ok || reason != ReasonDeleteBlocked || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if found, _ := r.Exists(ctx, DeleteScope{Conversation: key.Conversation}); !found {
		t.Fatal("the entry was dropped while its directory remained")
	}
}

func TestDeleteVerifiesManagedOwnerBeforeRemoval(t *testing.T) {
	for _, scenario := range []string{"missing-owner", "mismatched-owner", "incompatible-owner", "missing-home", "replacement"} {
		t.Run(scenario, func(t *testing.T) {
			r, _ := workerRegistry(t, testRoot(t), nil)
			ctx := context.Background()
			key := conversationKey(domain.NewID(), domain.NewID())
			lease, err := r.Acquire(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Dir(lease.Root)
			lease.Close()
			owner := filepath.Join(root, "owner.json")
			switch scenario {
			case "missing-owner":
				err = os.Remove(owner)
			case "mismatched-owner":
				raw, _ := json.Marshal(ownerRecord{Version: Version, ID: domain.NewID(), Key: key})
				err = os.WriteFile(owner, raw, 0600)
			case "incompatible-owner":
				raw, _ := json.Marshal(ownerRecord{Version: Version + 1, ID: lease.ID, Key: key})
				err = os.WriteFile(owner, raw, 0600)
			case "missing-home":
				err = os.RemoveAll(lease.Root)
			case "replacement":
				if err = os.RemoveAll(root); err == nil {
					err = os.Mkdir(root, 0700)
				}
				if err == nil {
					err = os.WriteFile(filepath.Join(root, "sentinel"), []byte("foreign"), 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.Delete(ctx, DeleteScope{Conversation: key.Conversation}); err == nil || n != 0 {
				t.Fatalf("unsafe deletion: n=%d err=%v", n, err)
			}
			if found, err := r.Exists(ctx, DeleteScope{Conversation: key.Conversation}); err != nil || !found {
				t.Fatal("failed deletion dropped its registry entry", err)
			}
			if scenario == "replacement" {
				if raw, err := os.ReadFile(filepath.Join(root, "sentinel")); err != nil || string(raw) != "foreign" {
					t.Fatal("replacement contents were removed", err)
				}
			}
		})
	}
}

func TestDeleteKeepsPathReplacementAfterOwnerVerification(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	ctx := context.Background()
	key := conversationKey(domain.NewID(), domain.NewID())
	lease, err := r.Acquire(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(lease.Root)
	lease.Close()
	moved := root + "-original"
	removeProfileAfterVerifyHook = func() {
		if err := os.Rename(root, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "sentinel"), []byte("foreign"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { removeProfileAfterVerifyHook = nil })
	if n, err := r.Delete(ctx, DeleteScope{Conversation: key.Conversation}); err == nil || n != 0 {
		t.Fatalf("path replacement was deleted: n=%d err=%v", n, err)
	}
	for _, path := range []string{filepath.Join(root, "sentinel"), filepath.Join(moved, "owner.json")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("the replacement or original object was removed", err)
		}
	}
	if found, err := r.Exists(ctx, DeleteScope{Conversation: key.Conversation}); err != nil || !found {
		t.Fatal("failed deletion dropped its registry entry", err)
	}
}

func TestDeleteRemovedClaudeProfileRetriesRegistryPublication(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	ctx := context.Background()
	account, profile := domain.NewID(), domain.NewID()
	key := Key{Harness: domain.ClaudeCode, Purpose: PurposeAuth, Account: account}
	relative := filepath.ToSlash(filepath.Join("native-claude", string(r.config.Server), string(account), string(profile)))
	root := filepath.Join(r.config.Root, filepath.FromSlash(relative))
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	registered, err := r.RegisterLegacy(ctx, key, relative)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := Key{Harness: domain.ClaudeCode, Purpose: PurposeQuota, Account: domain.NewID()}
	other, err := r.Acquire(ctx, unrelated)
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	held, err := security.TryLock(r.lockPath(registered.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteRemovedClaudeProfile(ctx, key, relative); err == nil {
		t.Fatal("an active profile was cleared")
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	originalWrite := writeRegistryFile
	writeRegistryFile = func(string, []byte) error { return errors.New("injected registry publication failure") }
	if err := r.DeleteRemovedClaudeProfile(ctx, key, relative); err == nil {
		t.Fatal("registry failure was reported as cleanup success")
	}
	writeRegistryFile = originalWrite
	t.Cleanup(func() { writeRegistryFile = originalWrite })
	if found, err := r.Exists(ctx, DeleteScope{Account: account, Purpose: PurposeAuth, Harness: domain.ClaudeCode}); err != nil || !found {
		t.Fatal("failed publication dropped the original entry", err)
	}
	if err := r.DeleteRemovedClaudeProfile(ctx, key, relative); err != nil {
		t.Fatal("original registry-only cleanup did not recover", err)
	}
	if err := r.DeleteRemovedClaudeProfile(ctx, key, relative); err != nil {
		t.Fatal("repeated cleanup was not idempotent", err)
	}
	if found, err := r.Exists(ctx, DeleteScope{Account: account, Purpose: PurposeAuth, Harness: domain.ClaudeCode}); err != nil || found {
		t.Fatal("original entry remains after retry", err)
	}
	if _, ok, err := r.Lookup(ctx, unrelated); err != nil || !ok {
		t.Fatal("unrelated profile was removed", err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sentinel"), []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteRemovedClaudeProfile(ctx, key, relative); err == nil {
		t.Fatal("a reappeared directory was accepted")
	}
	if raw, err := os.ReadFile(filepath.Join(root, "sentinel")); err != nil || string(raw) != "foreign" {
		t.Fatal("the reappeared directory was changed", err)
	}
}

func TestLoadRejectsEntriesOfAnotherOwnerIdentity(t *testing.T) {
	root := testRoot(t)
	r, _ := workerRegistry(t, root, nil)
	ctx := context.Background()
	lease, err := r.Acquire(ctx, conversationKey(domain.NewID(), domain.NewID()))
	if err != nil {
		t.Fatal(err)
	}
	lease.Close()
	for name, config := range map[string]Config{
		"other-server":  {Root: root, Owner: OwnerWorker, Server: domain.NewID(), Machine: r.config.Machine},
		"other-machine": {Root: root, Owner: OwnerWorker, Server: r.config.Server, Machine: domain.NewID()},
		"server-owner":  {Root: root, Owner: OwnerServer, Server: r.config.Server},
	} {
		t.Run(name, func(t *testing.T) {
			restored, err := Open(config)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = restored.Lookup(ctx, Key{Harness: domain.Codex, Purpose: PurposeDiscovery})
			if reason, ok := Reasoned(err); !ok || reason != ReasonRegistryInvalid {
				t.Fatalf("a foreign registry was accepted: %v", err)
			}
		})
	}
	if _, _, err := r.Lookup(ctx, Key{Harness: domain.Codex, Purpose: PurposeDiscovery}); err != nil {
		t.Fatalf("the owning identity was rejected: %v", err)
	}
}

func TestLoadRejectsUnsafeLegacyPaths(t *testing.T) {
	for _, bad := range []string{"/etc", "../outside", "a/../../outside", "native-profiles/p/x", "native-profiles", "a//b", "./a", "a/./b", "a/", `a\b`, ".", ".."} {
		t.Run(bad, func(t *testing.T) {
			r, _ := workerRegistry(t, testRoot(t), nil)
			key, err := r.normalize(conversationKey(domain.NewID(), domain.NewID()))
			if err != nil {
				t.Fatal(err)
			}
			writeRawRegistry(t, r, []entry{{ID: domain.NewID(), Key: key, Origin: OriginLegacy, Path: bad, CreatedAt: time.Now().UTC()}})
			_, _, err = r.Lookup(context.Background(), key)
			if reason, ok := Reasoned(err); !ok || reason != ReasonRegistryInvalid {
				t.Fatalf("unsafe legacy path %q was loaded: %v", bad, err)
			}
		})
	}
	r, _ := workerRegistry(t, testRoot(t), nil)
	key, _ := r.normalize(conversationKey(domain.NewID(), domain.NewID()))
	writeRawRegistry(t, r, []entry{{ID: domain.NewID(), Key: key, Origin: OriginLegacy, Path: "runtimes/abc", CreatedAt: time.Now().UTC()}})
	if _, _, err := r.Lookup(context.Background(), key); err != nil {
		t.Fatalf("a local normalized legacy path was rejected: %v", err)
	}
}

func lockHeld(r *Registry, id domain.ID) bool {
	lock, err := security.TryLock(r.lockPath(id))
	if err != nil {
		return true
	}
	_ = lock.Close()
	return false
}

func TestAcquireRollsBackAnUnpublishedProfileAndReleasesItsLease(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	ctx := context.Background()
	key := conversationKey(domain.NewID(), domain.NewID())
	original := writeRegistryFile
	defer func() { writeRegistryFile = original }()
	writeRegistryFile = func(string, []byte) error { return errors.New("disk full") }
	if _, err := r.Acquire(ctx, key); err == nil {
		t.Fatal("a failed publication was reported as success")
	}
	writeRegistryFile = original
	if entries, err := os.ReadDir(r.profilesDir()); err != nil || len(entries) != 0 {
		t.Fatalf("an unregistered profile directory remains: %v %v", entries, err)
	}
	if entries, err := os.ReadDir(r.locksDir()); err != nil {
		t.Fatal(err)
	} else {
		for _, entry := range entries {
			if lockHeld(r, domain.ID(strings.TrimSuffix(entry.Name(), ".lock"))) {
				t.Fatalf("lease %s leaked", entry.Name())
			}
		}
	}
	lease, err := r.Acquire(ctx, key)
	if err != nil || !lease.Created {
		t.Fatalf("retry after rollback: %v", err)
	}
	lease.Close()
}

func TestAcquireKeepsADurableProfileButReleasesItsLease(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	ctx := context.Background()
	key := conversationKey(domain.NewID(), domain.NewID())
	original := writeRegistryFile
	defer func() { writeRegistryFile = original }()
	// The rename succeeds and a later directory sync fails.
	writeRegistryFile = func(path string, raw []byte) error {
		if err := original(path, raw); err != nil {
			return err
		}
		return errors.New("directory sync failed")
	}
	if _, err := r.Acquire(ctx, key); err == nil {
		t.Fatal("a failed publication was reported as success")
	}
	writeRegistryFile = original
	info, ok, err := r.Lookup(ctx, key)
	if err != nil || !ok {
		t.Fatalf("durable entry lost: %v %v", ok, err)
	}
	if lockHeld(r, info.ID) {
		t.Fatal("lease leaked")
	}
	lease, err := r.Acquire(ctx, key)
	if err != nil || lease.Created || lease.ID != info.ID {
		t.Fatalf("durable profile was not reused: %v", err)
	}
	lease.Close()
}

func TestRepeatedPublicationFailuresLeaveNoProfileDirectories(t *testing.T) {
	r, _ := workerRegistry(t, testRoot(t), nil)
	ctx := context.Background()
	original := writeRegistryFile
	defer func() { writeRegistryFile = original }()
	writeRegistryFile = func(string, []byte) error { return errors.New("rename failed") }
	for i := 0; i < 3; i++ {
		if _, err := r.Acquire(ctx, conversationKey(domain.NewID(), domain.NewID())); err == nil {
			t.Fatal("accepted a failed publication")
		}
	}
	if entries, _ := os.ReadDir(r.profilesDir()); len(entries) != 0 {
		t.Fatalf("leftover directories: %d", len(entries))
	}
}
