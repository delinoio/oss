// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestCodexForwardVersionAdmission(t *testing.T) {
	for _, version := range []string{"0.151.0", "0.159.2", "0.160.0-beta.1", "1.0.0", "99999999.0.0", "0.151.0+build.7", "0.160.0-beta.1+build.7"} {
		if !CodexVersionAllowed(version) {
			t.Errorf("allowed native attempt rejected for %s", version)
		}
	}
	for _, version := range []string{"0.150.9", "0.151.0-beta.1", "", "garbage", "0.0151.0", "0.159", "0.159.2-", "0.159.2\nsecret", "0.159.2+"} {
		if CodexVersionAllowed(version) {
			t.Errorf("invalid/lower version admitted: %q", version)
		}
	}
}

func TestCodexDiagnosticPreservesVersionAndFirstFailure(t *testing.T) {
	err := WithCodexDiagnostic("0.159.2", CodexInitialize, Fail(Unsupported, "The initialization response is incompatible.", "Inspect Connection & diagnostics."))
	err = WithCodexDiagnostic("1.0.0", CodexLogin, err)
	d := CodexErrorDiagnostic(err)
	if d == nil || d.DetectedVersion != "0.159.2" || d.MinimumVersion != "0.151.0" || d.Phase != CodexInitialize || d.Code != Unsupported {
		t.Fatalf("diagnostic lost original context: %#v", d)
	}
	if !strings.Contains(SafeError(err).Message, "0.159.2") {
		t.Fatal("public error lost installed version")
	}
	redacted := WithCodexDiagnostic("secret/version", CodexLaunch, errors.New("token/path/raw-native-sentinel"))
	if strings.Contains(redacted.Error(), "sentinel") || strings.Contains(redacted.Error(), "secret/") {
		t.Fatal("untrusted native error or version was reflected")
	}
}
