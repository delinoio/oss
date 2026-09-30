// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
)

func TestSessionForkRejectedInspectionRemovesUnusedRuntime(t *testing.T) {
	root := t.TempDir()
	source, unused := filepath.Join(root, "source"), filepath.Join(root, "unused")
	for _, home := range []string{source, unused} {
		if _, err := harness.PrivateRuntimeEnvironment(home); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(source, "codex", "original-history")
	if err := os.WriteFile(marker, []byte("source history"), 0600); err != nil {
		t.Fatal(err)
	}
	rejected := domain.Fail(domain.Unsupported, "Unsupported settled tool history.", "")
	if err := finishForkSourceInspection(unused, rejected, nil); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("confirmed cleanup changed the rejection", err)
	}
	if _, err := os.Lstat(unused); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected inspection leaked its fresh runtime", err)
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "source history" {
		t.Fatal("unused-runtime cleanup touched source history", err)
	}
}

func TestSessionForkInspectionRetainsUnprovedProcessAndAcceptedRuntime(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		home := filepath.Join(t.TempDir(), "runtime")
		if _, err := harness.PrivateRuntimeEnvironment(home); err != nil {
			t.Fatal(err)
		}
		var inspectErr, closeErr error
		if !accepted {
			inspectErr = domain.Fail(domain.Unsupported, "Unsupported history.", "")
			closeErr = errors.New("unconfirmed process cleanup")
		}
		err := finishForkSourceInspection(home, inspectErr, closeErr)
		if accepted && err != nil || !accepted && domain.SafeError(err).Code != domain.RecoveryRequired {
			t.Fatal("inspection lost its ownership outcome", err)
		}
		if _, err := os.Stat(home); err != nil {
			t.Fatal("runtime removed without rejected, joined inspection", err)
		}
	}
}

func TestSessionForkInspectionCannotClaimFailedRuntimeRemoval(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("retain this foreign entry"), 0600); err != nil {
		t.Fatal(err)
	}
	rejected := domain.Fail(domain.Unsupported, "Unsupported history.", "")
	if err := finishForkSourceInspection(filepath.Join(parent, "runtime"), rejected, nil); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("failed runtime cleanup permitted a definite retry", err)
	}
	if raw, err := os.ReadFile(parent); err != nil || string(raw) != "retain this foreign entry" {
		t.Fatal("cleanup failure replaced an unrelated entry", err)
	}
}

func TestSessionForkPreNativeFailureOwnership(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		phase    forkRuntimePhase
		code     domain.Code
		retained bool
	}{
		{"workspace-rejected", forkRuntimeUnused, domain.Conflict, false},
		{"unjoined-inspection", forkSourceInspectionUnproved, domain.RecoveryRequired, true},
		{"possible-native-child", forkChildNativePossible, domain.RecoveryRequired, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "runtime")
			if _, err := harness.PrivateRuntimeEnvironment(home); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(home, "codex", "retained-state")
			if err := os.WriteFile(marker, []byte("owned state"), 0600); err != nil {
				t.Fatal(err)
			}
			failure := domain.Fail(domain.Conflict, "Workspace preparation rejected.", "")
			if scenario.phase == forkChildNativePossible {
				failure = executionCheckpointUncertain()
			}
			if err := finishForkPreNativeFailure(home, scenario.phase, failure); domain.SafeError(err).Code != scenario.code {
				t.Fatal("failure lost its cleanup/uncertainty classification", err)
			}
			raw, err := os.ReadFile(marker)
			if scenario.retained && (err != nil || string(raw) != "owned state") {
				t.Fatal("unproved or possible native state was removed", err)
			}
			if !scenario.retained {
				if _, err := os.Lstat(home); !os.IsNotExist(err) {
					t.Fatal("definite pre-native workspace failure leaked its runtime", err)
				}
			}
		})
	}
}
