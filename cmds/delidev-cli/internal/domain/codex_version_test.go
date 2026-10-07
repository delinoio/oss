// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCodexVersionIsMetadata(t *testing.T) {
	for _, version := range []string{"0.150.9", "0.151.0-beta.1", "unknown", "0.0151.0", "0.159", "0.151.0", "0.159.2", "0.160.0-beta.1", "1.0.0", "99999999.0.0", "999999999999999999.0.0", "0.151.0+build.7", "0.160.0-beta.1+build.7"} {
		if !CodexVersionAllowed(version) {
			t.Errorf("allowed native attempt rejected for %s", version)
		}
	}
	for _, version := range []string{"", "invalid/version", "token with space", "https://invalid", strings.Repeat("a", 65)} {
		if CodexVersionAllowed(version) {
			t.Errorf("unsafe metadata admitted: %q", version)
		}
	}
}

func TestCodexDiagnosticPreservesVersionAndFirstFailure(t *testing.T) {
	err := WithCodexDiagnostic("0.159.2", CodexInitialize, Fail(Unsupported, "raw-native-sentinel", "secret-login-url"))
	err = WithCodexDiagnostic("1.0.0", CodexLogin, err)
	d := CodexErrorDiagnostic(err)
	if d == nil || d.DetectedVersion != "0.159.2" || d.MinimumVersion != "" || d.Phase != CodexInitialize || d.Code != Unsupported {
		t.Fatalf("diagnostic lost original context: %#v", d)
	}
	if strings.Contains(SafeError(err).Message, "sentinel") || strings.Contains(SafeError(err).Guidance, "secret") {
		t.Fatal("safe transport error reflected native contents")
	}
	if !strings.Contains(SafeError(err).Message, "0.159.2") {
		t.Fatal("public error lost installed version")
	}
	redacted := WithCodexDiagnostic("secret/version", CodexLaunch, errors.New("token/path/raw-native-sentinel"))
	if strings.Contains(redacted.Error(), "sentinel") || strings.Contains(redacted.Error(), "secret/") {
		t.Fatal("untrusted native error or version was reflected")
	}
}

func TestCodexRecoveryRetainsOriginalFailureAndCancellation(t *testing.T) {
	original := WithCodexDiagnostic("0.159.2", CodexLogin, context.Canceled)
	if !errors.Is(original, context.Canceled) {
		t.Fatal("cancellation classification was lost")
	}
	recovery := CodexRecoveryFailure("0.159.2", CodexCleanup, original, Fail(RecoveryRequired, "Cleanup failed.", "Inspect the original operation."))
	d := CodexErrorDiagnostic(recovery)
	if d == nil || d.Phase != CodexLogin || d.Code != Canceled || SafeError(recovery).Code != RecoveryRequired || d.Validate() != nil {
		t.Fatal("cleanup erased the original failure or recovery")
	}
	d.Message = "raw-native/path/token"
	if d.Validate() == nil {
		t.Fatal("remote raw text was admitted")
	}
}
