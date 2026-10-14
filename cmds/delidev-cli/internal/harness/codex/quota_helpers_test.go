//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// Access-only quota fixture modes. They create the same native-style helper
// aliases as the managed Thread helper modes right after initialization.
const (
	quotaHelperPrefix       = "quota-helpers-"
	quotaHelperReady        = quotaHelperPrefix + "ready"
	quotaHelperReadFail     = quotaHelperPrefix + "readfail"
	quotaHelperForeignStore = quotaHelperPrefix + "foreign-store"
	quotaHelperStoreTamper  = quotaHelperForeignStore + "-tamper"
	quotaHelperEOFTamper    = quotaHelperPrefix + "eof-tamper"
	quotaHelperEOFRename    = quotaHelperPrefix + "eof-rename"
	quotaHelperEOFHardLink  = quotaHelperPrefix + "eof-hardlink"
	quotaHelperEOFForeign   = quotaHelperPrefix + "eof-foreign"
	quotaHelperReadyHang    = quotaHelperReady + "-hang"
)

func isHelperFixtureMode(mode string) bool {
	return strings.HasPrefix(mode, helperFixturePrefix) || strings.HasPrefix(mode, quotaHelperPrefix)
}

// mutateQuotaHelperFixtureAtEOF runs in the fixture native process after stdin
// EOF and before exit. It models a native tree that leaves, or alters, its
// original helper entries at shutdown.
func mutateQuotaHelperFixtureAtEOF(mode string) {
	directory := helperFixtureDirectoryPath()
	switch mode {
	case quotaHelperEOFTamper:
		tamperHelperFixtureAlias()
	case quotaHelperEOFRename:
		if os.Rename(directory, directory+".renamed") != nil {
			os.Exit(46)
		}
	case quotaHelperEOFHardLink:
		// Linkat without AT_SYMLINK_FOLLOW links the symlink itself on every Unix.
		if unix.Linkat(unix.AT_FDCWD, filepath.Join(directory, executionHelperNames[0]), unix.AT_FDCWD, filepath.Join(directory, "second-name"), 0) != nil {
			os.Exit(47)
		}
	case quotaHelperEOFForeign:
		if os.WriteFile(filepath.Join(directory, "foreign-alias"), []byte("foreign"), 0600) != nil {
			os.Exit(48)
		}
	}
}

func quotaHelperConfig(t *testing.T, mode string) (Config, *bytes.Buffer) {
	t.Helper()
	config, logs := helperStartupConfig(t, mode)
	config.Mode, config.ManagedAuthentication = QuotaProtocol, false
	return config, logs
}

func quotaHelperAuth() QuotaAuthentication {
	return QuotaAuthentication{Access: "synthetic-access-only", Account: "synthetic-account", Plan: "plus"}
}

func openQuotaHelperClient(t *testing.T, config Config) (*Client, error) {
	t.Helper()
	// The native process is bound to the Open context, so it must outlive the
	// quota read and Close performed by the caller.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return Open(ctx, config)
}

func requireNoHelperSymlinks(t *testing.T, home string) {
	t.Helper()
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			t.Fatal("runtime still holds a symlink", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func requireNoManagedAuthFile(t *testing.T, config Config) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(config.Home, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("access-only quota created a managed auth file")
	}
}

func TestQuotaOpenCapturesHelpersBeforeAnyAuthenticationOrRead(t *testing.T) {
	config, logs := quotaHelperConfig(t, quotaHelperReady)
	client, err := openQuotaHelperClient(t, config)
	if err != nil {
		t.Fatal(err)
	}
	if client.executionHelpers == nil || !client.executionHelpers.hasCapturedProof() {
		t.Fatal("quota Open did not hold captured startup proof")
	}
	if !strings.Contains(logs.String(), `"stage":"startup"`) || retirementLogs(logs) != 0 {
		t.Fatal("quota startup capture was not logged exactly before any retirement")
	}
	if _, err := os.Lstat(filepath.Join(config.Home, "quota-read")); !os.IsNotExist(err) {
		t.Fatal("quota read happened before helper capture")
	}
	requireNoManagedAuthFile(t, config)
	requireHelperAliases(t, config, true)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaCloseRetiresHelpersOnceAfterSuccessfulRead(t *testing.T) {
	config, logs := quotaHelperConfig(t, quotaHelperReady)
	client, err := openQuotaHelperClient(t, config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	observation, err := client.ReadExternalQuota(ctx, domain.NewID(), quotaHelperAuth())
	if err != nil || len(observation.Windows) != 1 {
		t.Fatal("quota read failed", err, logs.String())
	}
	requireNoManagedAuthFile(t, config)
	requireHelperAliases(t, config, true)
	if retirementLogs(logs) != 0 {
		t.Fatal("helpers retired before Close")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	requireHelperAliases(t, config, false)
	requireNoHelperSymlinks(t, config.Home)
	requireNoManagedAuthFile(t, config)
	if retirementLogs(logs) != 1 || !strings.Contains(logs.String(), `"reason":"verified"`) {
		t.Fatal("Close must retire the captured quota helpers exactly once")
	}
	if strings.Contains(logs.String(), "synthetic-access-only") {
		t.Fatal("helper diagnostics leaked a credential")
	}
	if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaCloseRetiresHelpersAfterFailedRead(t *testing.T) {
	config, logs := quotaHelperConfig(t, quotaHelperReadFail)
	client, err := openQuotaHelperClient(t, config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := client.ReadExternalQuota(ctx, domain.NewID(), quotaHelperAuth()); err == nil {
		t.Fatal("failed quota read succeeded")
	}
	if err := client.Close(); err != nil {
		t.Fatal("joined cleanup after a failed read must be confirmed", err)
	}
	requireHelperAliases(t, config, false)
	requireNoHelperSymlinks(t, config.Home)
	if retirementLogs(logs) != 1 {
		t.Fatal("failed read did not retire helpers exactly once")
	}
}

func TestQuotaCloseRetiresHelpersAfterForcedJoin(t *testing.T) {
	// The fixture ignores stdin EOF, so graceful close expires and forces the
	// original process. A confirmed forced join still permits captured retirement.
	config, logs := quotaHelperConfig(t, quotaHelperReadyHang)
	client, err := openQuotaHelperClient(t, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	requireHelperAliases(t, config, false)
	if retirementLogs(logs) != 1 {
		t.Fatal("forced joined close did not retire the captured proof once")
	}
}

func TestQuotaCloseWithMismatchedProcessRetiresNothing(t *testing.T) {
	config, logs := quotaHelperConfig(t, quotaHelperReady)
	client, err := openQuotaHelperClient(t, config)
	if err != nil {
		t.Fatal(err)
	}
	foreign := client.wire.Identity()
	foreign.PID++
	if err := client.executionHelpers.retire(client.home, client.ownerID, foreign); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("retirement accepted another process identity", err)
	}
	requireHelperAliases(t, config, true)
	if retirementLogs(logs) != 0 {
		t.Fatal("mismatched identity reached retirement")
	}
	// The rejected attempt spends no authority: the joined original still retires.
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	requireHelperAliases(t, config, false)
}

func TestQuotaCloseKeepsCleanupUncertainWithoutProofOrOnChangedEntries(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		reason  string
		present []string
	}{
		{"replaced-target", quotaHelperEOFTamper, `"reason":"alias-changed"`, nil},
		{"hard-link", quotaHelperEOFHardLink, `"reason":"alias-changed"`, []string{"second-name"}},
		{"foreign-entry", quotaHelperEOFForeign, ``, []string{"foreign-alias"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config, logs := quotaHelperConfig(t, tc.mode)
			client, err := openQuotaHelperClient(t, config)
			if err != nil {
				t.Fatal(err)
			}
			err = client.Close()
			directory := filepath.Join(config.Home, "tmp", "arg0", helperFixtureDirectory)
			if tc.mode == quotaHelperEOFHardLink {
				linked, linkErr := os.Lstat(filepath.Join(directory, "second-name"))
				if linkErr != nil || linked.Mode()&os.ModeSymlink == 0 {
					// Some platforms follow the symlink when linking.
					t.Skip("platform did not hard-link the symlink itself")
				}
			}
			if tc.name == "foreign-entry" {
				// A foreign sibling does not alter the three captured originals; only
				// those originals are retired and the foreign entry is untouched.
				if err != nil {
					t.Fatal(err)
				}
				requireHelperAliases(t, config, false)
			} else {
				if domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatalf("changed helper entry did not stay recovery-required: %v", err)
				}
				if !strings.Contains(logs.String(), tc.reason) {
					t.Fatal("missing closed retirement diagnostic", logs.String())
				}
				// Validation precedes the first unlink, so every original remains.
				for _, name := range executionHelperNames {
					if _, err := os.Lstat(filepath.Join(directory, name)); err != nil {
						t.Fatal("unproved alias was removed", name, err)
					}
				}
			}
			for _, name := range tc.present {
				if _, err := os.Lstat(filepath.Join(directory, name)); err != nil {
					t.Fatal("unrelated entry was removed", name, err)
				}
			}
			if retirementLogs(logs) != 1 {
				t.Fatal("retirement must settle exactly once")
			}
			if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQuotaCloseRejectsRenamedHelperDirectory(t *testing.T) {
	config, logs := quotaHelperConfig(t, quotaHelperEOFRename)
	client, err := openQuotaHelperClient(t, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("renamed helper directory was accepted as native removal: %v", err)
	}
	directory := filepath.Join(config.Home, "tmp", "arg0", helperFixtureDirectory)
	if _, err := os.Lstat(directory); !os.IsNotExist(err) {
		t.Fatal("cleanup adopted the original directory basename", err)
	}
	for _, name := range executionHelperNames {
		if _, err := os.Lstat(filepath.Join(directory+".renamed", name)); err != nil {
			t.Fatal("renamed directory lost an alias", name, err)
		}
	}
	if retirementLogs(logs) != 1 || !strings.Contains(logs.String(), `"reason":"identity-changed"`) {
		t.Fatal("renamed directory did not produce one closed retirement failure")
	}
}

func TestQuotaFailedOpenAfterCaptureRetiresAfterJoin(t *testing.T) {
	config, logs := quotaHelperConfig(t, quotaHelperForeignStore)
	client, err := openQuotaHelperClient(t, config)
	if client != nil {
		_ = client.Close()
		t.Fatal("foreign credential store opened a quota client")
	}
	if code := domain.SafeError(err).Code; code == domain.RecoveryRequired || NativeJoinedHelperRetirementFailed(err) {
		t.Fatalf("confirmed retirement must leave the original startup error: %v", err)
	}
	requireHelperAliases(t, config, false)
	if retirementLogs(logs) != 1 || !strings.Contains(logs.String(), `"reason":"verified"`) {
		t.Fatal("failed quota startup must retire the captured proof exactly once")
	}
	if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaFailedOpenRetirementFailureStaysRecoveryRequired(t *testing.T) {
	config, logs := quotaHelperConfig(t, quotaHelperStoreTamper)
	_, err := openQuotaHelperClient(t, config)
	if domain.SafeError(err).Code != domain.RecoveryRequired || !NativeJoinedHelperRetirementFailed(err) {
		t.Fatalf("retirement failure did not retain recovery ownership: %v", err)
	}
	if retirementLogs(logs) != 1 || !strings.Contains(logs.String(), `"reason":"alias-changed"`) {
		t.Fatal("changed alias did not produce one retirement failure")
	}
	directory := filepath.Join(config.Home, "tmp", "arg0", helperFixtureDirectory)
	for _, name := range executionHelperNames {
		if _, err := os.Lstat(filepath.Join(directory, name)); err != nil {
			t.Fatal("retirement removed an entry despite an unconfirmed proof", name, err)
		}
	}
}
