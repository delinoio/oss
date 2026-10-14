package server

import (
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// TestLegacyExecutionRelayAdmitsBaselineSemVerOnly proves that legacy (version
// 1) Claude, Grok and OpenCode assignments use the shared baseline predicate
// and that direct version-4 admission stays independent of version metadata.
func TestLegacyExecutionRelayAdmitsBaselineSemVerOnly(t *testing.T) {
	for _, test := range []struct {
		harness  domain.Harness
		protocol domain.APIProtocol
		baseline string
		newer    string
		lower    string
	}{
		{domain.ClaudeCode, domain.AnthropicMessages, domain.ClaudeProtocolVersion, "2.1.237", "2.1.235"},
		{domain.GrokBuild, domain.OpenAIChat, domain.GrokProtocolVersion, "1.0.42", "1.0.40"},
		{domain.OpenCode, domain.OpenAIChat, domain.OpenCodeProtocolVersion, "1.18.33", "1.18.31"},
	} {
		t.Run(string(test.harness), func(t *testing.T) {
			f := newProfileAuthorityFixture(t, "http://127.0.0.1:1", test.harness, test.protocol, nil, false)
			input := f.input
			for version, want := range map[string]bool{
				test.baseline:         true,
				test.newer:            true,
				"9.9.9-rc.1":          true,
				test.baseline + "+b1": true,
				test.lower:            false,
				test.baseline + "-rc": false,
				"not-a-version":       false,
				"":                    false,
			} {
				input.Installation.Version = version
				if got := len(executionAPIOperations(input, test.protocol)) == 1; got != want {
					t.Errorf("legacy version %q admitted=%v, want %v", version, got, want)
				}
			}
		})
	}
}

// TestFirstDispatchFixtureReportsObservedExecutableVersion proves that the
// dispatch fixture records the version a real native binary reported instead of
// the harness baseline, so the Worker probes that binary with its true version.
func TestFirstDispatchFixtureReportsObservedExecutableVersion(t *testing.T) {
	for _, test := range []struct {
		name, observed, want string
	}{
		{"baseline-by-default", "", domain.OpenCodeProtocolVersion},
		{"newer-observed", "1.18.33", "1.18.33"},
		{"prerelease-observed", "2.0.0-rc.1", "2.0.0-rc.1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFirstDispatchFixtureObserved(t, domain.OpenCode, domain.ExecuteMode, "/fixture/opencode", test.observed, "", "fixture-model", domain.GeneralChat)
			var machine domain.Machine
			if err := domain.Decode(f.machine.DocumentJson, &machine); err != nil {
				t.Fatal(err)
			}
			for _, installation := range machine.Installations {
				if installation.Harness == domain.OpenCode {
					if installation.Version != test.want || installation.ResolvedPath != "/fixture/opencode" {
						t.Fatalf("installation version=%q path=%q, want %q", installation.Version, installation.ResolvedPath, test.want)
					}
					return
				}
			}
			t.Fatal("OpenCode installation was not reported")
		})
	}
}
