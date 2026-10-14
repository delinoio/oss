package nativeprofile

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
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
	owner := filepath.Join(lease.Root, "owner.json")
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
