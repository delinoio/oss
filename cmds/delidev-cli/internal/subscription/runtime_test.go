// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestRuntimeCleanupPreservesInventoryBounds(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		files int
		bytes int64
		want  CleanupReason
	}{
		{"file-limit", RuntimeFileLimit, 0, ""},
		{"too-many-files", RuntimeFileLimit + 1, 0, CleanupFileLimit},
		{"byte-limit", 1, RuntimeByteLimit, ""},
		{"too-many-bytes", 1, RuntimeByteLimit + 1, CleanupByteLimit},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "private-runtime")
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
			original, err := security.StableStat(home)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < scenario.files; i++ {
				file, err := os.OpenFile(filepath.Join(home, fmt.Sprintf("file-%04d", i)), os.O_CREATE|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(scenario.bytes); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err = CleanupRuntime(home, original)
			if scenario.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(home); !os.IsNotExist(err) {
					t.Fatal("bounded runtime was not removed")
				}
				return
			}
			var failure *RuntimeCleanupError
			if !errors.As(err, &failure) || failure.Stage != CleanupInventory || failure.Reason != scenario.want || domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("cleanup limit lost its safe recovery classification")
			}
			if failure.FileCount != scenario.files || failure.Bytes != scenario.bytes || strings.Contains(err.Error(), home) {
				t.Fatal("cleanup diagnostic lost its bounded counters or exposed a path")
			}
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != scenario.files {
				t.Fatal("failed inventory removed unconfirmed files")
			}
		})
	}
}

func TestRuntimeCleanupRejectsSymlinkAndReplacedIdentity(t *testing.T) {
	for _, scenario := range []string{"symlink", "identity"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "runtime")
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
			original, err := security.StableStat(home)
			if err != nil {
				t.Fatal(err)
			}
			foreign := filepath.Join(root, "retained-foreign-file")
			if err := os.WriteFile(foreign, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			want := CleanupSymlink
			if scenario == "symlink" {
				if err := os.Symlink(foreign, filepath.Join(home, "link")); err != nil {
					t.Skip("platform cannot create the temporary symlink fixture")
				}
			} else {
				want = CleanupIdentity
				if err := os.Rename(home, filepath.Join(root, "original")); err != nil {
					t.Fatal(err)
				}
				if err := security.PrivateDir(home); err != nil {
					t.Fatal(err)
				}
			}
			var failure *RuntimeCleanupError
			err = CleanupRuntime(home, original)
			if !errors.As(err, &failure) {
				t.Fatalf("unowned runtime inventory accepted: %v", err)
			}
			if failure.Reason != want {
				t.Fatalf("cleanup returned reason %q, want %q", failure.Reason, want)
			}
			if _, err := os.Stat(home); err != nil {
				t.Fatal("unconfirmed runtime removed")
			}
			if raw, err := os.ReadFile(foreign); err != nil || string(raw) != "preserve" {
				t.Fatal("cleanup changed foreign content")
			}
		})
	}
}

// An interrupted atomic credential write leaves its temporary name beside the
// target. That remnant holds a plaintext bundle, so it must block reuse and be
// retired with the credential files.
func credentialRemnant(t *testing.T, profile, name string) string {
	t.Helper()
	target := filepath.Join(profile, filepath.FromSlash(name))
	if err := security.PrivateDir(filepath.Dir(target)); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(filepath.Dir(target), security.OwnedTemporaryPrefix(target))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"tokens":{"access_token":"secret"}}`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return file.Name()
}

func TestInterruptedCredentialWriteBlocksReuseAndIsRetired(t *testing.T) {
	for _, name := range CredentialFiles {
		t.Run(name, func(t *testing.T) {
			profile := t.TempDir()
			helper := filepath.Join(profile, "history.jsonl")
			if err := os.WriteFile(helper, []byte("kept"), 0600); err != nil {
				t.Fatal(err)
			}
			remnant := credentialRemnant(t, profile, name)
			present, err := CredentialFilePresent(profile)
			if err != nil || !present {
				t.Fatalf("a credential temporary was considered clean: %v %v", present, err)
			}
			if err := RetireCredentialFiles(profile); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(remnant); !os.IsNotExist(err) {
				t.Fatal("the credential temporary survived retirement")
			}
			if present, err := CredentialFilePresent(profile); err != nil || present {
				t.Fatalf("profile still blocked: %v %v", present, err)
			}
			if raw, err := os.ReadFile(helper); err != nil || string(raw) != "kept" {
				t.Fatal("retirement changed retained data")
			}
		})
	}
}

func TestCredentialWriteRetiresEarlierInterruptedTemporaries(t *testing.T) {
	profile := t.TempDir()
	name := CredentialFiles[0]
	remnant := credentialRemnant(t, profile, name)
	target := filepath.Join(profile, filepath.FromSlash(name))
	if err := WriteCredentialFile(target, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(remnant); !os.IsNotExist(err) {
		t.Fatal("an earlier credential temporary survived the next write")
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "new" {
		t.Fatalf("credential file not written: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files remain: %v %v", entries, err)
	}
}

func TestCredentialTemporaryThatIsNotRegularBlocksWithoutRemoval(t *testing.T) {
	profile := t.TempDir()
	name := CredentialFiles[0]
	target := filepath.Join(profile, filepath.FromSlash(name))
	if err := security.PrivateDir(filepath.Dir(target)); err != nil {
		t.Fatal(err)
	}
	special := filepath.Join(filepath.Dir(target), security.OwnedTemporaryPrefix(target)+"dir")
	if err := os.Mkdir(special, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := CredentialFilePresent(profile); err == nil {
		t.Fatal("a non-regular credential temporary was accepted")
	}
	if err := RetireCredentialFiles(profile); err == nil {
		t.Fatal("a non-regular credential temporary was retired")
	}
	if _, err := os.Lstat(special); err != nil {
		t.Fatal("the unexpected entry was removed")
	}
}

func requireCredentialSyncFailure(t *testing.T, err error) {
	t.Helper()
	var failure *RuntimeCleanupError
	if !errors.As(err, &failure) || failure.Stage != CleanupSync || failure.Reason != CleanupFilesystem || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("cleanup did not retain its typed recovery error: %v", err)
	}
}

func TestCredentialCleanupSyncsAbsentTargetAfterFailedRemovalSync(t *testing.T) {
	profile := t.TempDir()
	name := CredentialFiles[0]
	target := filepath.Join(profile, filepath.FromSlash(name))
	if err := security.PrivateDir(filepath.Dir(target)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("fixture credential"), 0600); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(filepath.Dir(target), "history.jsonl")
	if err := os.WriteFile(unrelated, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	calls, failOnCall := 0, 2
	syncParent := func(path string) error {
		if path != target {
			t.Fatalf("cleanup synchronized an unexpected path: %q", path)
		}
		calls++
		if calls == failOnCall {
			return errors.New("injected parent sync failure")
		}
		return security.SyncParent(path)
	}
	requireCredentialSyncFailure(t, retireCredentialFilesWithSync(profile, syncParent))
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("credential removal did not precede the failed sync: %v", err)
	}
	if calls != 2 {
		t.Fatalf("unexpected first cleanup sync count: %d", calls)
	}
	// The empty temporary scan succeeds, then the absent target's parent sync fails.
	failOnCall = calls + 2
	requireCredentialSyncFailure(t, retireCredentialFilesWithSync(profile, syncParent))
	if calls != 4 {
		t.Fatalf("retry did not sync the absent target's parent: %d", calls)
	}
	failOnCall = 0
	if err := retireCredentialFilesWithSync(profile, syncParent); err != nil {
		t.Fatal(err)
	}
	if calls != 6 {
		t.Fatalf("successful retry did not confirm the parent: %d", calls)
	}
	if raw, err := os.ReadFile(unrelated); err != nil || string(raw) != "retained" {
		t.Fatal("cleanup changed unrelated profile content")
	}
}

func TestCredentialCleanupSyncsEmptyTemporaryRetry(t *testing.T) {
	profile := t.TempDir()
	name := CredentialFiles[0]
	target := filepath.Join(profile, filepath.FromSlash(name))
	remnant := credentialRemnant(t, profile, name)
	calls, failOnCall := 0, 1
	syncParent := func(path string) error {
		if path != remnant && path != target {
			t.Fatalf("cleanup synchronized an unexpected path: %q", path)
		}
		calls++
		if calls == failOnCall {
			return errors.New("injected parent sync failure")
		}
		return security.SyncParent(path)
	}
	requireCredentialSyncFailure(t, retireCredentialTemporariesWithSync(profile, name, syncParent))
	if _, err := os.Lstat(remnant); !os.IsNotExist(err) {
		t.Fatalf("temporary removal did not precede the failed sync: %v", err)
	}
	failOnCall = 2
	requireCredentialSyncFailure(t, retireCredentialTemporariesWithSync(profile, name, syncParent))
	if calls != 2 {
		t.Fatalf("empty retry did not synchronize the parent: %d", calls)
	}
	failOnCall = 0
	if err := retireCredentialTemporariesWithSync(profile, name, syncParent); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("successful empty retry did not synchronize the parent: %d", calls)
	}
}

func TestCredentialCleanupSyncsInitiallyAbsentTarget(t *testing.T) {
	profile := t.TempDir()
	target := filepath.Join(profile, filepath.FromSlash(CredentialFiles[0]))
	if err := security.PrivateDir(filepath.Dir(target)); err != nil {
		t.Fatal(err)
	}
	calls, failOnCall := 0, 1
	syncParent := func(path string) error {
		if path != target {
			t.Fatalf("cleanup synchronized an unexpected path: %q", path)
		}
		calls++
		if calls == failOnCall {
			return errors.New("injected parent sync failure")
		}
		return security.SyncParent(path)
	}
	requireCredentialSyncFailure(t, retireCredentialFilesWithSync(profile, syncParent))
	if calls != 1 {
		t.Fatalf("empty temporary scan did not synchronize the parent: %d", calls)
	}
	failOnCall = 0
	if err := retireCredentialFilesWithSync(profile, syncParent); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("absent temporary and target did not both confirm the parent: %d", calls)
	}
}

func TestCredentialCleanupRejectsNonRegularTarget(t *testing.T) {
	profile := t.TempDir()
	target := filepath.Join(profile, filepath.FromSlash(CredentialFiles[0]))
	if err := security.PrivateDir(filepath.Dir(target)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	var failure *RuntimeCleanupError
	err := RetireCredentialFiles(profile)
	if !errors.As(err, &failure) || failure.Stage != CleanupAuthFile || failure.Reason != CleanupNotRegular {
		t.Fatalf("non-regular credential target lost its validation error: %v", err)
	}
	if info, err := os.Lstat(target); err != nil || !info.IsDir() {
		t.Fatal("cleanup removed the unexpected credential target")
	}
}

func TestCredentialCleanupValidatesParentBeforeAbsentSync(t *testing.T) {
	name := CredentialFiles[0]
	t.Run("missing parent", func(t *testing.T) {
		profile := t.TempDir()
		if err := retireCredentialFilesWithSync(profile, func(string) error {
			t.Fatal("missing parent was synchronized")
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("symlink parent", func(t *testing.T) {
		profile := t.TempDir()
		foreign := t.TempDir()
		parent := filepath.Dir(filepath.Join(profile, filepath.FromSlash(name)))
		if err := os.Symlink(foreign, parent); err != nil {
			t.Skip("platform cannot create the temporary symlink fixture")
		}
		var failure *RuntimeCleanupError
		err := retireCredentialFilesWithSync(profile, func(string) error {
			t.Fatal("symlink parent was synchronized")
			return nil
		})
		if !errors.As(err, &failure) || failure.Stage != CleanupValidation || failure.Reason != CleanupSymlink {
			t.Fatalf("symlink parent lost its validation error: %v", err)
		}
	})
	t.Run("non-directory parent", func(t *testing.T) {
		profile := t.TempDir()
		parent := filepath.Dir(filepath.Join(profile, filepath.FromSlash(name)))
		if err := os.WriteFile(parent, []byte("retained"), 0600); err != nil {
			t.Fatal(err)
		}
		var failure *RuntimeCleanupError
		err := retireCredentialFilesWithSync(profile, func(string) error {
			t.Fatal("non-directory parent was synchronized")
			return nil
		})
		if !errors.As(err, &failure) || failure.Stage != CleanupValidation || failure.Reason != CleanupNotRegular {
			t.Fatalf("non-directory parent lost its validation error: %v", err)
		}
	})
}

func TestInspectRetainedCredentialsAdmitsOnlySafeRegularFiles(t *testing.T) {
	for _, name := range CredentialFiles {
		t.Run(name, func(t *testing.T) {
			profile := t.TempDir()
			if present, err := InspectRetainedCredentials(profile); err != nil || present {
				t.Fatalf("an empty profile was not clean: %v %v", present, err)
			}
			target := filepath.Join(profile, filepath.FromSlash(name))
			if err := security.PrivateDir(filepath.Dir(target)); err != nil {
				t.Fatal(err)
			}
			if err := WriteCredentialFile(target, []byte(`{"tokens":{"access_token":"secret"}}`)); err != nil {
				t.Fatal(err)
			}
			// A retained regular private file is admitted and left untouched.
			if present, err := InspectRetainedCredentials(profile); err != nil || !present {
				t.Fatalf("a retained private file was rejected: %v %v", present, err)
			}
			if raw, err := os.ReadFile(target); err != nil || !strings.Contains(string(raw), "secret") {
				t.Fatal("inspection changed the retained file", err)
			}
			// The target-attributed temporary of an interrupted write is rejected.
			remnant := credentialRemnant(t, profile, name)
			if _, err := InspectRetainedCredentials(profile); !hasReason(err, CleanupPendingTemporary) {
				t.Fatalf("a pending temporary was admitted: %v", err)
			}
			if err := os.Remove(remnant); err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" {
				if err := os.Chmod(target, 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := InspectRetainedCredentials(profile); !hasReason(err, CleanupUnsafe) {
					t.Fatalf("a world-readable file was admitted: %v", err)
				}
				if err := os.Chmod(target, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), target); err != nil {
					t.Skip("symlinks unavailable")
				}
				if _, err := InspectRetainedCredentials(profile); !hasReason(err, CleanupSymlink) {
					t.Fatalf("a symlinked credential file was admitted: %v", err)
				}
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := InspectRetainedCredentials(profile); !hasReason(err, CleanupNotRegular) {
				t.Fatalf("a directory in place of the credential file was admitted: %v", err)
			}
		})
	}
}

func hasReason(err error, reason CleanupReason) bool {
	var failure *RuntimeCleanupError
	return errors.As(err, &failure) && failure.Reason == reason
}
