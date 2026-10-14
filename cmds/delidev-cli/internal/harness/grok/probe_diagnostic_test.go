// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
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

// The probe result table: every probe outcome (success, one failure per phase
// and a real deadline) against a confirmed and an unconfirmed cleanup of the
// owned ACP process. The first failure keeps its phase and code; an unconfirmed
// cleanup alone is the cleanup phase, and after a failure it only turns the
// transport error into recovery_required so discovery still stops for
// reconciliation. Failures before the ACP process exists (version gate and
// configuration inspection) own no handle to clean up here.
func TestProbeCleanupFailureNeverReplacesTheFirstFailure(t *testing.T) {
	errInjected := errors.New("injected cleanup failure")
	for _, test := range []struct {
		name     string
		mode     string
		version  string
		deadline time.Duration
		handle   bool
		phases   []domain.GrokPhase
		code     domain.Code
	}{
		{name: "success", mode: "valid", handle: true},
		{name: "version failure", mode: "valid", version: "1.0.40", phases: []domain.GrokPhase{domain.GrokVersion}, code: domain.Unsupported},
		{name: "profile failure", mode: "inspect-managed", phases: []domain.GrokPhase{domain.GrokProfile}, code: domain.Unsupported},
		{name: "profile inspection exit", mode: "inspect-exit", phases: []domain.GrokPhase{domain.GrokProfile}, code: domain.Unavailable},
		{name: "runtime failure", mode: "foreign", handle: true, phases: []domain.GrokPhase{domain.GrokRuntime}, code: domain.Unsupported},
		{name: "runtime exit", mode: "exit", handle: true, phases: []domain.GrokPhase{domain.GrokRuntime}, code: domain.Unavailable},
		{name: "late protocol failure", mode: "duplicate", handle: true, phases: []domain.GrokPhase{domain.GrokRuntime}, code: domain.Unsupported},
		// The deadline can expire while the fake inspection still runs on a slow
		// machine, so either phase proves the first failure was kept.
		{name: "deadline", mode: "timeout", deadline: time.Second, handle: true, phases: []domain.GrokPhase{domain.GrokRuntime, domain.GrokProfile}, code: domain.Unavailable},
	} {
		for _, unconfirmed := range []bool{false, true} {
			name := test.name + " with confirmed cleanup"
			if unconfirmed {
				name = test.name + " with unconfirmed cleanup"
			}
			t.Run(name, func(t *testing.T) {
				previous := closeProbeHandle
				t.Cleanup(func() { closeProbeHandle = previous })
				if unconfirmed {
					closeProbeHandle = func(h *process.Handle) error { return errors.Join(h.Close(), errInjected) }
				}
				config, logs := fixtureConfig(t, test.mode)
				if test.version != "" {
					config.Version = test.version
				}
				timeout := test.deadline
				if timeout == 0 {
					timeout = 10 * time.Second
				}
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				err := Probe(ctx, config)
				cleanupFails := unconfirmed && test.handle
				if len(test.phases) == 0 && !cleanupFails {
					if err != nil {
						t.Fatalf("successful probe: %v", err)
					}
					return
				}
				d := domain.GrokErrorDiagnostic(err)
				if err == nil || d == nil || d.Validate() != nil || d.DetectedVersion != config.Version || d.SupportedVersion != domain.GrokSubscriptionBaselineVersion {
					t.Fatalf("diagnostic: err=%v diagnostic=%#v logs=%s", err, d, logs.String())
				}
				wantCode, wantTransport := test.code, test.code
				if len(test.phases) == 0 {
					// Cleanup alone failed after a successful probe.
					if d.Phase != domain.GrokCleanup {
						t.Fatalf("unconfirmed cleanup alone reported phase %q", d.Phase)
					}
					wantCode, wantTransport = domain.RecoveryRequired, domain.RecoveryRequired
				} else {
					if !slices.Contains(test.phases, d.Phase) {
						t.Fatalf("first failure phase = %q, want one of %v; logs: %s", d.Phase, test.phases, logs.String())
					}
					if cleanupFails {
						wantTransport = domain.RecoveryRequired
					}
				}
				if d.Code != wantCode {
					t.Fatalf("diagnostic code = %q, want %q", d.Code, wantCode)
				}
				if got := domain.SafeError(err).Code; got != wantTransport {
					t.Fatalf("transport code = %q, want %q", got, wantTransport)
				}
				if got := strings.Contains(logs.String(), `"cleanup_unconfirmed":true`); got != cleanupFails {
					t.Fatalf("cleanup_unconfirmed logged = %v, want %v: %s", got, cleanupFails, logs.String())
				}
				for _, value := range []string{d.Message, d.Guidance, err.Error()} {
					if sensitiveProbeText.MatchString(value) || strings.Contains(value, errInjected.Error()) {
						t.Fatalf("diagnostic text contains a sensitive shape: %q", value)
					}
				}
			})
		}
	}
}

// A recovery-required result of the configuration inspection means its owned
// process was not confirmed stopped, so it is reported as the cleanup phase.
func TestInspectionRecoveryRequiredIsTheCleanupPhase(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want domain.GrokPhase
	}{
		{"unconfirmed inspection cleanup", domain.Fail(domain.RecoveryRequired, "x", "y"), domain.GrokCleanup},
		{"wrapped unconfirmed inspection cleanup", errors.Join(errors.New("raw"), domain.Fail(domain.RecoveryRequired, "x", "y")), domain.GrokCleanup},
		{"inspection failure", domain.Fail(domain.Unsupported, "x", "y"), domain.GrokProfile},
		{"inspection overflow", domain.Fail(domain.ResourceExhausted, "x", "y"), domain.GrokProfile},
	} {
		if got := inspectFailurePhase(test.err).diagnosticPhase(); got != test.want {
			t.Errorf("%s: phase %q, want %q", test.name, got, test.want)
		}
	}
}
