// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

var sensitiveProbeText = regexp.MustCompile(`(?i)(sentinel|https?://|/users/|/home/|[a-z]:\\|@[a-z0-9-]+\.[a-z]|eyj[a-z0-9_-]{8,})`)

func TestProbePhasesMapToDiagnosticPhases(t *testing.T) {
	for phase, want := range map[probePhase]domain.GrokPhase{
		versionPhase:          domain.GrokVersion,
		profilePhase:          domain.GrokProfile,
		runtimePhase:          domain.GrokRuntime,
		inspectPhase:          domain.GrokProfile,
		runtimeInspectPhase:   domain.GrokProfile,
		workspaceInspectPhase: domain.GrokProfile,
		authenticatePhase:     domain.GrokLogin,
		launchPhase:           domain.GrokRuntime,
		initializePhase:       domain.GrokRuntime,
		cleanupPhase:          domain.GrokCleanup,
		probePhase("unknown"): domain.GrokProfile,
	} {
		if got := phase.diagnosticPhase(); got != want {
			t.Errorf("probe phase %q mapped to %q, want %q", phase, got, want)
		}
	}
}

// Probe failures carry the original phase, a stable code and both versions.
// The 1.0.52 case is a fake native whose inspection reports a populated
// experiments inventory; no real native output is embedded.
func TestProbeFailuresCarryGrokDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name    string
		mode    string
		version string
		home    string
		phase   domain.GrokPhase
		code    domain.Code
	}{
		{name: "old version", mode: "valid", version: "1.0.40", phase: domain.GrokVersion, code: domain.Unsupported},
		{name: "invalid home", mode: "valid", home: "relative", phase: domain.GrokRuntime, code: ""},
		{name: "managed inspection", mode: "inspect-managed", phase: domain.GrokProfile, code: domain.Unsupported},
		{name: "1.0.52 experiments inspection", mode: "inspect-experiments", version: "1.0.52", phase: domain.GrokProfile, code: domain.Unsupported},
		{name: "inspection exit", mode: "inspect-exit", phase: domain.GrokProfile, code: domain.Unavailable},
		{name: "inspection overflow", mode: "inspect-overflow", phase: domain.GrokProfile, code: domain.ResourceExhausted},
		{name: "foreign initialize", mode: "foreign", phase: domain.GrokRuntime, code: domain.Unsupported},
		{name: "native exit", mode: "exit", phase: domain.GrokRuntime, code: domain.Unavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, logs := fixtureConfig(t, test.mode)
			if test.version != "" {
				config.Version = test.version
			}
			if test.home != "" {
				config.Home = test.home
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := Probe(ctx, config)
			d := domain.GrokErrorDiagnostic(err)
			if err == nil || d == nil || d.Phase != test.phase || d.Validate() != nil {
				t.Fatalf("probe diagnostic: err=%v diagnostic=%#v logs=%s", err, d, logs.String())
			}
			if test.code != "" && d.Code != test.code {
				t.Fatalf("diagnostic code %q, want %q", d.Code, test.code)
			}
			if d.DetectedVersion != config.Version || d.SupportedVersion != domain.GrokSubscriptionBaselineVersion || d.Guidance == "" {
				t.Fatalf("diagnostic lost versions or guidance: %#v", d)
			}
			if safe := domain.SafeError(err); safe.Code != d.Code || safe.Message != d.Message {
				t.Fatalf("transport error differs from diagnostic: %#v", safe)
			}
			for _, value := range []string{d.Message, d.Guidance, err.Error()} {
				if sensitiveProbeText.MatchString(value) {
					t.Fatalf("diagnostic text contains a sensitive shape: %q", value)
				}
			}
			if strings.Contains(d.Message+d.Guidance, config.Process.Cwd) {
				t.Fatal("diagnostic exposed a runtime path")
			}
			if !strings.Contains(logs.String(), "diagnostic_phase") {
				t.Fatalf("failure log lost the structured diagnostic phase: %s", logs.String())
			}
		})
	}
}

func TestProbeSuccessCarriesNoDiagnostic(t *testing.T) {
	config, _ := fixtureConfig(t, "valid")
	if err := Probe(context.Background(), config); err != nil || domain.GrokErrorDiagnostic(err) != nil {
		t.Fatalf("successful probe: %v", err)
	}
}
