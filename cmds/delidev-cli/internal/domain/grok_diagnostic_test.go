// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

// sensitiveGrokText matches token-, path-, e-mail- and URL-shaped substrings
// that no Grok diagnostic may ever contain.
var sensitiveGrokText = regexp.MustCompile(`(?i)(sentinel|secret|token|refresh_token|https?://|file://|/users/|/home/|[a-z]:\\|@[a-z0-9-]+\.[a-z]|eyj[a-z0-9_-]{8,}|[a-z0-9]{32,})`)

func requireSafeGrokText(t *testing.T, d GrokDiagnostic) {
	t.Helper()
	for _, value := range []string{d.Message, d.Guidance, d.DetectedVersion, d.SupportedVersion, string(d.Code), string(d.Phase)} {
		if sensitiveGrokText.MatchString(value) {
			t.Fatalf("diagnostic field contains a sensitive shape: %q", value)
		}
	}
}

// Every phase maps to a stable code, the installed version and the supported
// baseline, with locally reconstructed message and guidance.
func TestGrokDiagnosticEveryPhase(t *testing.T) {
	steps := map[GrokPhase]string{GrokDiscovery: "executable discovery", GrokVersion: "version validation", GrokProfile: "profile validation", GrokRuntime: "runtime preparation", GrokLogin: "sign-in", GrokRefresh: "credential refresh", GrokLogout: "sign-out", GrokModels: "model discovery", GrokExecution: "execution", GrokHistory: "history verification", GrokCleanup: "cleanup"}
	if len(GrokPhases) != len(steps) {
		t.Fatalf("phase table drifted: %d phases", len(GrokPhases))
	}
	for _, phase := range GrokPhases {
		for _, code := range []Code{Unsupported, Unavailable, NotFound, Unauthenticated, RecoveryRequired} {
			t.Run(string(phase)+"/"+string(code), func(t *testing.T) {
				err := WithGrokDiagnostic("1.0.52", phase, Fail(code, "raw-native-sentinel https://auth.x.ai/oauth2/device?user_code=ABCD-EFGH", "secret-token /Users/private/.grok/auth.json"))
				d := GrokErrorDiagnostic(err)
				if d == nil || d.Phase != phase || d.Code != code || d.DetectedVersion != "1.0.52" || d.SupportedVersion != GrokSubscriptionBaselineVersion {
					t.Fatalf("diagnostic lost phase, code or versions: %#v", d)
				}
				if !strings.Contains(d.Message, "Grok Build 1.0.52 did not complete "+steps[phase]+".") || d.Guidance == "" {
					t.Fatalf("unexpected message or guidance: %#v", d)
				}
				if d.Validate() != nil {
					t.Fatalf("diagnostic is not valid: %v", d.Validate())
				}
				requireSafeGrokText(t, *d)
				safe := SafeError(err)
				if safe.Code != code || safe.Message != d.Message || safe.Guidance != d.Guidance || strings.Contains(err.Error(), "sentinel") {
					t.Fatalf("transport error reflected native text: %#v", safe)
				}
			})
		}
	}
}

func TestGrokDiagnosticPreservesFirstFailureAndRedactsUntrustedInput(t *testing.T) {
	err := WithGrokDiagnostic("1.0.52", GrokProfile, Fail(Unsupported, "raw-native-sentinel", "secret-login-url"))
	err = WithGrokDiagnostic("1.0.46", GrokLogin, err)
	d := GrokErrorDiagnostic(err)
	if d == nil || d.DetectedVersion != "1.0.52" || d.Phase != GrokProfile || d.Code != Unsupported {
		t.Fatalf("outer operation replaced the first failure: %#v", d)
	}
	redacted := WithGrokDiagnostic("secret/version", GrokRuntime, errors.New("token/path/raw-native-sentinel"))
	if strings.Contains(redacted.Error(), "sentinel") || strings.Contains(redacted.Error(), "secret/") {
		t.Fatal("untrusted native error or version was reflected")
	}
	if d := GrokErrorDiagnostic(redacted); d.DetectedVersion != "" || d.Code != Internal || d.Validate() != nil {
		t.Fatalf("untrusted version was retained: %#v", d)
	}
	if GrokErrorDiagnostic(Fail(Unsupported, "x", "y")) != nil || WithGrokDiagnostic("1.0.52", GrokLogin, nil) != nil {
		t.Fatal("diagnostic invented for a non-Grok error")
	}
	unknown := GrokErrorDiagnostic(WithGrokDiagnostic("1.0.52", GrokPhase("bogus"), context.Canceled))
	if unknown.Phase != GrokProfile || unknown.Code != Canceled {
		t.Fatalf("unknown phase was not normalized: %#v", unknown)
	}
}

func TestGrokVersionFailureReportsDetectedAndSupportedVersions(t *testing.T) {
	d := GrokErrorDiagnostic(GrokVersionFailure("1.0.43"))
	if d == nil || d.Phase != GrokVersion || d.Code != Unsupported || d.DetectedVersion != "1.0.43" || d.SupportedVersion != GrokSubscriptionBaselineVersion {
		t.Fatalf("unexpected version diagnostic: %#v", d)
	}
	if !strings.Contains(d.Message, "at or above "+GrokSubscriptionBaselineVersion) {
		t.Fatalf("message lost the supported version: %q", d.Message)
	}
	invalid := GrokErrorDiagnostic(GrokVersionFailure("unknown"))
	if invalid.DetectedVersion != "unknown" && invalid.DetectedVersion != "" || !strings.Contains(invalid.Message, "invalid") {
		t.Fatalf("invalid metadata was not distinguished: %#v", invalid)
	}
}

// The desktop reconstructs the same version reason from the closed metadata, so
// this table is mirrored by apps/delidev/src/grok-diagnostic.test.tsx. Every
// version below passes ValidInstallationVersion; only x/mod/semver decides
// whether the reason names the supported baseline or invalid metadata.
func TestGrokVersionReasonFollowsFullSemVerValidation(t *testing.T) {
	for _, version := range []string{"1.0.43", "1.0.0-rc.1", "1.0.0-0", "1.0.0-alpha-1", "1.0.0+build.01", "1.0.0-rc.1+20260101"} {
		t.Run("valid "+version, func(t *testing.T) {
			d := GrokErrorDiagnostic(GrokVersionFailure(version))
			if d == nil || d.DetectedVersion != version || d.Validate() != nil || !strings.Contains(d.Message, "valid SemVer at or above "+GrokSubscriptionBaselineVersion) {
				t.Fatalf("unexpected diagnostic: %#v", d)
			}
		})
	}
	for _, version := range []string{"01.0.0", "1.00.0", "1.0.00", "1.0.0-01", "1.0.0-rc.01", "1.0.0-a..b", "1.0.0-.a", "1.0.0-a.", "1.0.0+a..b", "1.0.0+.a"} {
		t.Run("invalid "+version, func(t *testing.T) {
			if !ValidInstallationVersion(version) {
				t.Fatalf("fixture %q must be an admitted installation version", version)
			}
			d := GrokErrorDiagnostic(GrokVersionFailure(version))
			if d == nil || d.DetectedVersion != version || d.Validate() != nil || !strings.Contains(d.Message, "The native version metadata is invalid.") {
				t.Fatalf("unexpected diagnostic: %#v", d)
			}
		})
	}
}

func TestGrokDiagnosticTimeoutAndValidation(t *testing.T) {
	d := GrokErrorDiagnostic(WithGrokDiagnostic("1.0.52", GrokLogin, context.DeadlineExceeded))
	if d.Code != Unavailable || !strings.HasSuffix(d.Message, "The native operation timed out.") || d.Validate() != nil {
		t.Fatalf("timeout was not reported distinctly: %#v", d)
	}
	valid := *GrokErrorDiagnostic(WithGrokDiagnostic("1.0.52", GrokRuntime, Fail(Unsupported, "x", "y")))
	for name, mutate := range map[string]func(*GrokDiagnostic){
		"raw message":    func(d *GrokDiagnostic) { d.Message = "raw-native/path/token" },
		"raw guidance":   func(d *GrokDiagnostic) { d.Guidance = "https://example.invalid" },
		"phase":          func(d *GrokDiagnostic) { d.Phase = "bogus" },
		"code":           func(d *GrokDiagnostic) { d.Code = "bogus" },
		"supported":      func(d *GrokDiagnostic) { d.SupportedVersion = "0.0.1" },
		"version":        func(d *GrokDiagnostic) { d.DetectedVersion = "/Users/private" },
		"correlation":    func(d *GrokDiagnostic) { d.CorrelationID = "not-an-id" },
		"missing detail": func(d *GrokDiagnostic) { d.SupportedVersion = "" },
	} {
		t.Run(name, func(t *testing.T) {
			d := valid
			mutate(&d)
			if d.Validate() == nil {
				t.Fatal("tampered diagnostic was admitted")
			}
		})
	}
	restored := RestoreGrokDiagnostic(valid)
	if got := GrokErrorDiagnostic(restored); got == nil || *got != valid || SafeError(restored).Code != Unsupported {
		t.Fatalf("restore changed the diagnostic: %#v", got)
	}
	invalid := valid
	invalid.Message = "raw"
	if got := GrokErrorDiagnostic(RestoreGrokDiagnostic(invalid)); got == nil || got.Validate() != nil || got.Message == "raw" {
		t.Fatal("invalid metadata was restored verbatim")
	}
}

func TestInstallationProtocolCarriesOnlyGrokFailureDiagnostics(t *testing.T) {
	diagnostic := GrokErrorDiagnostic(WithGrokDiagnostic("1.0.52", GrokProfile, Fail(Unsupported, "x", "y")))
	installation := func(harness Harness, state ProtocolState) *Installation {
		i := &Installation{Harness: harness, State: InstallationDetected, Version: "1.0.52", ResolvedPath: "/bin/fixture", Protocol: &ProtocolObservation{Protocol: ProtocolFor(harness), State: state}}
		i.Protocol.GrokDiagnostic = diagnostic
		return i
	}
	if err := installation(GrokBuild, ProtocolUnsupported).validateProtocol(true); err != nil {
		t.Fatalf("Grok failure diagnostic rejected: %v", err)
	}
	if err := installation(GrokBuild, ProtocolFailed).validateProtocol(true); err != nil {
		t.Fatalf("Grok failure diagnostic rejected for failed state: %v", err)
	}
	verified := installation(GrokBuild, ProtocolVerified)
	verified.ProtocolVerified = true
	if verified.validateProtocol(true) == nil {
		t.Fatal("verified profile carried a failure diagnostic")
	}
	if installation(Codex, ProtocolUnsupported).validateProtocol(true) == nil || installation(ClaudeCode, ProtocolFailed).validateProtocol(true) == nil {
		t.Fatal("non-Grok harness carried a Grok diagnostic")
	}
	mismatch := installation(GrokBuild, ProtocolUnsupported)
	mismatch.Version = "1.0.46"
	if mismatch.validateProtocol(true) == nil {
		t.Fatal("diagnostic for another version was attributed to the installation")
	}
}

func grokOperationFixture() Account {
	now := time.Now().UTC()
	operation := NewID()
	state := &SubscriptionState{
		Pending:         &SubscriptionOperation{ID: operation, Action: SubscriptionLogin, Actor: Principal{Type: OwnerDevice}, Phase: SubscriptionQueued},
		ServerOperation: &ServerSubscriptionOperation{ID: operation, Epoch: NewID(), FinishID: NewID(), Action: SubscriptionLogin, Actor: Principal{Type: OwnerDevice}, State: SubscriptionUnsupported, StartedAt: now, ExpiresAt: now.Add(15 * time.Minute)},
	}
	state.ServerOperation.GrokDiagnostic = GrokErrorDiagnostic(WithGrokDiagnostic("1.0.52", GrokProfile, Fail(Unsupported, "x", "y")))
	state.ServerOperation.GrokDiagnostic.CorrelationID = string(operation)
	return Account{Type: SubscriptionAccount, SubscriptionService: SubscriptionGrok, Health: AccountDisconnected, Subscription: state}
}

func TestServerOperationGrokDiagnosticOwnership(t *testing.T) {
	valid := grokOperationFixture()
	if err := valid.Subscription.Validate(valid); err != nil {
		t.Fatalf("settled Grok operation with a diagnostic was rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Account){
		"codex account":    func(a *Account) { a.SubscriptionService = SubscriptionChatGPT },
		"claude account":   func(a *Account) { a.SubscriptionService = SubscriptionClaude },
		"both diagnostics": func(a *Account) { a.Subscription.ServerOperation.Diagnostic = &CodexDiagnostic{} },
		"other operation":  func(a *Account) { a.Subscription.ServerOperation.GrokDiagnostic.CorrelationID = string(NewID()) },
		"raw message":      func(a *Account) { a.Subscription.ServerOperation.GrokDiagnostic.Message = "raw-native-text" },
		"waiting":          func(a *Account) { a.Subscription.ServerOperation.State = SubscriptionWaiting },
		"succeeded":        func(a *Account) { a.Subscription.ServerOperation.State = SubscriptionSucceeded },
	} {
		t.Run(name, func(t *testing.T) {
			a := grokOperationFixture()
			mutate(&a)
			if a.Subscription.Validate(a) == nil {
				t.Fatal("invalid Grok diagnostic ownership was accepted")
			}
		})
	}
	raw, err := json.Marshal(ServerSubscriptionOperation{})
	if err != nil || strings.Contains(string(raw), "grok_diagnostic") {
		t.Fatalf("legacy server operation gained a Grok diagnostic key: %s %v", raw, err)
	}
}
