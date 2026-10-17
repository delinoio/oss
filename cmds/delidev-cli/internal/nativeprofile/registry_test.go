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
	// Mode bits do not install a private DACL on Windows. Create the fixture
	// owner root through the same primitive as the Worker.
	root = filepath.Join(root, "private")
	if err := security.PrivateDir(root); err != nil {
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

// The shared Codex owner purpose is account scoped and registers an existing
// per-account directory in place without allocating a managed profile.
func TestSharedOwnerPurposeIsAccountScopedAndRegistersInPlace(t *testing.T) {
	root := testRoot(t)
	r, _ := workerRegistry(t, root, nil)
	account := domain.NewID()
	if err := (Key{Owner: OwnerWorker, Machine: domain.NewID(), Harness: domain.Codex, Purpose: PurposeSharedOwner}).Validate(); err == nil {
		t.Fatal("a shared owner key without an account was accepted")
	}
	if err := (Key{Owner: OwnerWorker, Machine: domain.NewID(), Harness: domain.Codex, Purpose: PurposeSharedOwner, Account: account, Conversation: domain.NewID()}).Validate(); err == nil {
		t.Fatal("a shared owner key with a conversation was accepted")
	}
	relative := "runtimes/accounts/" + string(account) + "/codex"
	home := filepath.Join(root, filepath.FromSlash(relative))
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "history"), []byte("kept"), 0600); err != nil {
		t.Fatal(err)
	}
	key := Key{Harness: domain.Codex, Purpose: PurposeSharedOwner, Account: account}
	info, err := r.RegisterLegacy(context.Background(), key, relative)
	if err != nil || info.Origin != OriginLegacy || info.Root != home {
		t.Fatalf("shared owner home was not registered in place: %+v %v", info, err)
	}
	lease, err := r.Acquire(context.Background(), key)
	if err != nil || lease.ID != info.ID || lease.Root != home {
		t.Fatalf("acquire did not reuse the registered home: %v", err)
	}
	defer lease.Close()
	if _, err := r.Acquire(context.Background(), key); err == nil {
		t.Fatal("a held shared owner home was locked twice")
	}
	if raw, err := os.ReadFile(filepath.Join(home, "history")); err != nil || string(raw) != "kept" {
		t.Fatal("registration changed retained history")
	}
}

// keyFor builds a valid key of the purpose's scope for the harness.
func keyFor(purpose Purpose, harness domain.Harness, account, conversation domain.ID) Key {
	scope, _ := purpose.Scope()
	key := Key{Harness: harness, Purpose: purpose}
	if scope != ScopeSystem {
		key.Account = account
	}
	if scope == ScopeConversation {
		key.Conversation = conversation
	}
	return key
}

// Every purpose is checked against every harness through every registry entry
// point. The registered-only shared owner purpose is defined for Codex alone;
// it is never created by Acquire, but an entry registered in place is locked
// and looked up like any other profile.
func TestPurposeHarnessMatrixAcrossAcquireRegisterLegacyAndLookup(t *testing.T) {
	ctx := context.Background()
	harnesses := []domain.Harness{domain.Codex, domain.ClaudeCode, domain.OpenCode, domain.GrokBuild}
	for purpose := range purposeScopes {
		for _, harness := range harnesses {
			purpose, harness := purpose, harness
			t.Run(string(purpose)+"/"+string(harness), func(t *testing.T) {
				root := testRoot(t)
				r, _ := workerRegistry(t, root, nil)
				key := keyFor(purpose, harness, domain.NewID(), domain.NewID())
				defined := purpose != PurposeSharedOwner || harness == domain.Codex
				relative := "retained/" + string(purpose) + "-" + string(harness)
				home := filepath.Join(root, filepath.FromSlash(relative))
				if err := security.PrivateDir(home); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, "history"), []byte("kept"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := r.normalize(key); (err == nil) != defined {
					t.Fatalf("Validate defined=%v err=%v", defined, err)
				}
				if !defined {
					if _, err := r.Acquire(ctx, key); err == nil {
						t.Fatal("Acquire accepted an undefined purpose for the harness")
					}
					if _, err := r.RegisterLegacy(ctx, key, relative); err == nil {
						t.Fatal("RegisterLegacy accepted an undefined purpose for the harness")
					}
					if _, _, err := r.Lookup(ctx, key); err == nil {
						t.Fatal("Lookup accepted an undefined purpose for the harness")
					}
					return
				}
				if _, ok, err := r.Lookup(ctx, key); err != nil || ok {
					t.Fatalf("an unseen key was found: %v", err)
				}
				lease, err := r.Acquire(ctx, key)
				if purpose.RegisteredOnly() {
					if err == nil {
						_ = lease.Close()
						t.Fatal("Acquire created a registered-only profile")
					}
					if reason, ok := Reasoned(err); !ok || reason != ReasonLegacyUnverified {
						t.Fatalf("Acquire failed without the closed cause: %v", err)
					}
					if _, ok, _ := r.Lookup(ctx, key); ok {
						t.Fatal("a rejected Acquire registered the key")
					}
					if entries, _ := os.ReadDir(filepath.Join(root, "native-profiles", "p")); len(entries) != 0 {
						t.Fatalf("a rejected Acquire left %d profile directories", len(entries))
					}
				} else {
					if err != nil || lease.Origin != OriginManaged || !lease.Created {
						t.Fatalf("Acquire did not create a managed profile: %v", err)
					}
					_ = lease.Close()
					// A managed key keeps its single identity; a second registration
					// of retained history for it is a conflict.
					if _, err := r.RegisterLegacy(ctx, key, relative); err == nil {
						t.Fatal("RegisterLegacy replaced a managed profile")
					}
					return
				}
				info, err := r.RegisterLegacy(ctx, key, relative)
				if err != nil || info.Origin != OriginLegacy || info.Root != home {
					t.Fatalf("RegisterLegacy: %+v %v", info, err)
				}
				found, ok, err := r.Lookup(ctx, key)
				if err != nil || !ok || found.ID != info.ID {
					t.Fatalf("Lookup after registration: %v", err)
				}
				locked, err := r.Acquire(ctx, key)
				if err != nil || locked.ID != info.ID || locked.Root != home || locked.Created {
					t.Fatalf("Acquire of the registered entry: %v", err)
				}
				defer locked.Close()
				if raw, err := os.ReadFile(filepath.Join(home, "history")); err != nil || string(raw) != "kept" {
					t.Fatal("registration changed retained history")
				}
			})
		}
	}
}

// A restored registry that binds the shared owner purpose to a non-Codex
// harness is invalid as a whole and is never rewritten.
func TestRegistryLoadRejectsSharedOwnerForOtherHarnesses(t *testing.T) {
	root := testRoot(t)
	r, _ := workerRegistry(t, root, nil)
	account := domain.NewID()
	relative := "runtimes/accounts/" + string(account) + "/codex"
	if err := security.PrivateDir(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RegisterLegacy(context.Background(), Key{Harness: domain.Codex, Purpose: PurposeSharedOwner, Account: account}, relative); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(r.registryPath())
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(raw, []byte(`"harness":"codex"`), []byte(`"harness":"claude-code"`), 1)
	if bytes.Equal(raw, tampered) {
		t.Fatalf("registry encoding changed: %s", raw)
	}
	if err := os.WriteFile(r.registryPath(), tampered, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Lookup(context.Background(), Key{Harness: domain.Codex, Purpose: PurposeSharedOwner, Account: account}); err == nil {
		t.Fatal("a shared owner entry for another harness was loaded")
	} else if reason, ok := Reasoned(err); !ok || reason != ReasonRegistryInvalid {
		t.Fatalf("restored registry was not rejected as invalid: %v", err)
	}
	if after, _ := os.ReadFile(r.registryPath()); !bytes.Equal(after, tampered) {
		t.Fatal("the invalid registry was rewritten")
	}
}

// A registered-only purpose exists only as a legacy directory registered in
// place that holds no conversation history. Registration enforces that, and a
// restored registry that breaks it is invalid as a whole at every entry point,
// even when its managed directory and owner record are intact: Acquire must
// never lock such an entry as an ordinary profile. Every cell is one entry
// point against one broken entry shape.
func TestRegistryLoadRejectsBrokenRegisteredOnlyEntries(t *testing.T) {
	ctx := context.Background()
	type shape struct {
		name   string
		tamper func(t *testing.T, r *Registry, root string, account domain.ID) Key
	}
	shapes := []shape{
		{"managed origin with its directory and owner record", func(t *testing.T, r *Registry, root string, account domain.ID) Key {
			auth := Key{Harness: domain.Codex, Purpose: PurposeAuth, Account: account}
			lease, err := r.Acquire(ctx, auth)
			if err != nil || lease.Origin != OriginManaged {
				t.Fatalf("managed profile: %v", err)
			}
			_ = lease.Close()
			owner := filepath.Join(r.managedPath(lease.ID), "owner.json")
			for _, path := range []string{r.registryPath(), owner} {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				tampered := bytes.Replace(raw, []byte(`"purpose":"auth"`), []byte(`"purpose":"shared_owner"`), 1)
				if bytes.Equal(raw, tampered) {
					t.Fatalf("encoding changed: %s", raw)
				}
				if err := os.WriteFile(path, tampered, 0600); err != nil {
					t.Fatal(err)
				}
			}
			return Key{Harness: domain.Codex, Purpose: PurposeSharedOwner, Account: account}
		}},
		{"legacy origin holding a conversation history", func(t *testing.T, r *Registry, root string, account domain.ID) Key {
			relative := "runtimes/accounts/" + string(account) + "/codex"
			if err := security.PrivateDir(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
				t.Fatal(err)
			}
			key := Key{Harness: domain.Codex, Purpose: PurposeSharedOwner, Account: account}
			if _, err := r.RegisterLegacy(ctx, key, relative); err != nil {
				t.Fatal(err)
			}
			file, err := r.load()
			if err != nil || len(file.Profiles) != 1 {
				t.Fatalf("registry: %v", err)
			}
			file.Profiles[0].Histories = []domain.ID{domain.NewID()}
			raw, err := json.Marshal(file)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(r.registryPath(), raw, 0600); err != nil {
				t.Fatal(err)
			}
			return key
		}},
	}
	entryPoints := map[string]func(r *Registry, key Key, relative string) error{
		"Lookup": func(r *Registry, key Key, _ string) error { _, _, err := r.Lookup(ctx, key); return err },
		"Peek":   func(r *Registry, key Key, _ string) error { _, _, err := r.Peek(key); return err },
		"Acquire": func(r *Registry, key Key, _ string) error {
			lease, err := r.Acquire(ctx, key)
			if err == nil {
				_ = lease.Close()
			}
			return err
		},
		"RegisterLegacy": func(r *Registry, key Key, relative string) error {
			_, err := r.RegisterLegacy(ctx, key, relative)
			return err
		},
	}
	for _, s := range shapes {
		for name, call := range entryPoints {
			s, name, call := s, name, call
			t.Run(s.name+"/"+name, func(t *testing.T) {
				root := testRoot(t)
				r, _ := workerRegistry(t, root, nil)
				account := domain.NewID()
				key := s.tamper(t, r, root, account)
				before, _ := os.ReadFile(r.registryPath())
				err := call(r, key, "runtimes/accounts/"+string(account)+"/codex")
				if err == nil {
					t.Fatal("a broken registered-only entry was accepted")
				}
				if reason, ok := Reasoned(err); !ok || reason != ReasonRegistryInvalid {
					t.Fatalf("restored registry was not rejected as invalid: %v", err)
				}
				if after, _ := os.ReadFile(r.registryPath()); !bytes.Equal(after, before) {
					t.Fatal("the invalid registry was rewritten")
				}
			})
		}
	}
}

// Registering a registered-only purpose with a conversation history is
// rejected before anything is written, so the invariant holds on both sides of
// a registry round trip.
func TestRegisterLegacyRejectsHistoriesForRegisteredOnlyPurpose(t *testing.T) {
	root := testRoot(t)
	r, _ := workerRegistry(t, root, nil)
	account := domain.NewID()
	relative := "runtimes/accounts/" + string(account) + "/codex"
	if err := security.PrivateDir(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
		t.Fatal(err)
	}
	key := Key{Harness: domain.Codex, Purpose: PurposeSharedOwner, Account: account}
	if _, err := r.RegisterLegacy(context.Background(), key, relative, domain.NewID()); err == nil {
		t.Fatal("a registered-only purpose accepted a conversation history")
	}
	if _, ok, err := r.Lookup(context.Background(), key); err != nil || ok {
		t.Fatalf("a rejected registration left an entry: ok=%v err=%v", ok, err)
	}
	// An entry registered without histories cannot gain one later either, or the
	// next registry load would reject the whole registry.
	if _, err := r.RegisterLegacy(context.Background(), key, relative); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordHistory(context.Background(), key, domain.NewID()); err == nil {
		t.Fatal("a registered-only purpose recorded a conversation history")
	}
	if _, ok, err := r.Lookup(context.Background(), key); err != nil || !ok {
		t.Fatalf("a rejected history left the registry unusable: ok=%v err=%v", ok, err)
	}
}

// Closing a lease is final only once the lock close succeeded. A failed close
// leaves the lease open, so a later call closes the handle again and reports
// that result: it never turns a close the lock did not confirm into a success.
// Each cell is the state of the lease against one Close call.
func TestLeaseCloseIsFinalOnlyAfterASuccessfulLockClose(t *testing.T) {
	ctx := context.Background()
	key := Key{Harness: domain.Codex, Purpose: PurposeAuth, Account: domain.NewID()}
	t.Run("open then closed twice", func(t *testing.T) {
		r, _ := workerRegistry(t, testRoot(t), nil)
		lease, err := r.Acquire(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if err := lease.Close(); err != nil {
			t.Fatalf("first close: %v", err)
		}
		if err := lease.Close(); err != nil {
			t.Fatalf("a close after a successful close must stay nil: %v", err)
		}
		again, err := r.Acquire(ctx, key)
		if err != nil {
			t.Fatalf("a closed lease still held the lock: %v", err)
		}
		_ = again.Close()
	})
	t.Run("failed close stays failed and open", func(t *testing.T) {
		r, _ := workerRegistry(t, testRoot(t), nil)
		lease, err := r.Acquire(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		// Closing the lock handle behind the lease makes every lease close fail
		// the way an unconfirmed release does.
		if err := lease.lock.Close(); err != nil {
			t.Fatal(err)
		}
		for call := 1; call <= 3; call++ {
			if err := lease.Close(); err == nil {
				t.Fatalf("close %d reported success for a lock close that never succeeded", call)
			}
		}
		if lease.closed {
			t.Fatal("a failed close marked the lease closed")
		}
	})
}

// Peek is the read-only lookup: it answers from the registry on disk and never
// creates the registry directories, their lock file or any profile, so it works
// on a read-only root. Every cell below is one state of the registry.
func TestPeekIsReadOnlyForEveryRegistryState(t *testing.T) {
	ctx := context.Background()
	key := func(account domain.ID) Key {
		return Key{Harness: domain.Codex, Purpose: PurposeSharedOwner, Account: account}
	}
	snapshot := func(t *testing.T, root string) map[string]string {
		t.Helper()
		seen := map[string]string{}
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || path == root {
				return nil
			}
			content := ""
			if info.Mode().IsRegular() {
				raw, _ := os.ReadFile(path)
				content = string(raw)
			}
			seen[path] = info.Mode().String() + ":" + content
			return nil
		})
		return seen
	}
	same := func(t *testing.T, before, after map[string]string) {
		t.Helper()
		if len(before) != len(after) {
			t.Fatalf("peek changed the owner root: %v -> %v", before, after)
		}
		for path, state := range before {
			if after[path] != state {
				t.Fatalf("peek changed %s", path)
			}
		}
	}
	t.Run("registry absent", func(t *testing.T) {
		root := testRoot(t)
		r, _ := workerRegistry(t, root, nil)
		before := snapshot(t, root)
		if _, ok, err := r.Peek(key(domain.NewID())); err != nil || ok {
			t.Fatalf("absent registry: ok=%v err=%v", ok, err)
		}
		same(t, before, snapshot(t, root))
		if _, err := os.Lstat(r.Base()); !os.IsNotExist(err) {
			t.Fatal("peek created the registry directory")
		}
	})
	t.Run("registry directory without a registry file", func(t *testing.T) {
		root := testRoot(t)
		r, _ := workerRegistry(t, root, nil)
		if err := security.PrivateDir(r.Base()); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, root)
		if _, ok, err := r.Peek(key(domain.NewID())); err != nil || ok {
			t.Fatalf("empty registry directory: ok=%v err=%v", ok, err)
		}
		same(t, before, snapshot(t, root))
	})
	t.Run("registered and unregistered keys on a read-only registry", func(t *testing.T) {
		root := testRoot(t)
		r, _ := workerRegistry(t, root, nil)
		account := domain.NewID()
		relative := "runtimes/accounts/" + string(account) + "/codex"
		if err := security.PrivateDir(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			t.Fatal(err)
		}
		registered, err := r.RegisterLegacy(ctx, key(account), relative)
		if err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, root)
		found, ok, err := r.Peek(key(account))
		if err != nil || !ok || found.ID != registered.ID || found.Root != registered.Root {
			t.Fatalf("registered key: %+v ok=%v err=%v", found, ok, err)
		}
		if _, ok, err := r.Peek(key(domain.NewID())); err != nil || ok {
			t.Fatalf("unregistered key: ok=%v err=%v", ok, err)
		}
		same(t, before, snapshot(t, root))
	})
	t.Run("invalid registry file", func(t *testing.T) {
		root := testRoot(t)
		r, _ := workerRegistry(t, root, nil)
		if err := security.PrivateDir(r.Base()); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(r.registryPath(), []byte("{not json"), 0600); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, root)
		if _, _, err := r.Peek(key(domain.NewID())); err == nil {
			t.Fatal("an invalid registry was accepted")
		} else if reason, ok := Reasoned(err); !ok || reason != ReasonRegistryInvalid {
			t.Fatalf("invalid registry reason: %v", err)
		}
		same(t, before, snapshot(t, root))
	})
	t.Run("registry directory is a link", func(t *testing.T) {
		root := testRoot(t)
		r, _ := workerRegistry(t, root, nil)
		elsewhere := filepath.Join(root, "elsewhere")
		if err := security.PrivateDir(elsewhere); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, r.Base()); err != nil {
			t.Skip("symlinks unavailable")
		}
		if _, _, err := r.Peek(key(domain.NewID())); err == nil {
			t.Fatal("a linked registry directory was read")
		} else if reason, ok := Reasoned(err); !ok || reason != ReasonRegistryInvalid {
			t.Fatalf("linked registry reason: %v", err)
		}
	})
}
