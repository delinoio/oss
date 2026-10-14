package domain

import "testing"

func protocolOutput() (HarnessDiscoveryInput, HarnessDiscoveryOutput) {
	input := HarnessDiscoveryInput{Revision: 1, VerifyProtocol: true, Selections: ExecutableSelections{Executables: []ExecutableSelection{}}}
	output := HarnessDiscoveryOutput{Installations: input.Selections.Installations()}
	for index := range output.Installations {
		i := &output.Installations[index]
		i.State = InstallationDetected
		i.ResolvedPath = "/worker/executable"
		i.Version = "1.2.3"
		i.Protocol = &ProtocolObservation{Protocol: ProtocolFor(i.Harness), State: ProtocolUnsupported, Problem: Fail(Internal, "raw-private-diagnostic", "raw-private-guidance")}
		if i.Harness == Codex {
			i.Version = CodexProtocolVersion
		}
		if i.Harness == ClaudeCode {
			i.Version = ClaudeProtocolVersion
		}
		if i.Harness == GrokBuild {
			i.Version = GrokProtocolVersion
		}
		if i.Harness == OpenCode {
			i.Version = OpenCodeProtocolVersion
		}
		if i.Harness == Codex || i.Harness == ClaudeCode || i.Harness == GrokBuild || i.Harness == OpenCode {
			i.Protocol.State = ProtocolVerified
			i.Protocol.Problem = nil
			i.ProtocolVerified = true
		}
	}
	return input, output
}
func TestNativeProtocolObservationIsSeparateFromExecutionReadiness(t *testing.T) {
	input, output := protocolOutput()
	if err := output.ValidateDiscovery(input); err != nil {
		t.Fatal(err)
	}
	for _, i := range output.Installations {
		if len(i.Capabilities) != 0 {
			t.Fatal("handshake granted execution capabilities")
		}
		if i.Protocol.Problem != nil && i.Protocol.Problem.Message == "raw-private-diagnostic" {
			t.Fatal("raw protocol error retained")
		}
	}
	for _, change := range []string{"unrequested", "foreign-protocol", "unverified-version", "unverified-claude-version", "unverified-grok-version", "unverified-opencode-version", "missing", "false-state", "execution-capability", "claude-execution-capability", "grok-execution-capability", "opencode-execution-capability"} {
		t.Run(change, func(t *testing.T) {
			input, output := protocolOutput()
			switch change {
			case "unrequested":
				input.VerifyProtocol = false
			case "foreign-protocol":
				output.Installations[0].Protocol.Protocol = GrokACP
			case "unverified-version":
				output.Installations[0].Version = "invalid/version"
			case "unverified-claude-version":
				output.Installations[1].Version = "invalid/version"
			case "unverified-grok-version":
				output.Installations[3].Version = "invalid/version"
			case "missing":
				output.Installations[0].Protocol = nil
			case "false-state":
				output.Installations[0].ProtocolVerified = false
			case "unverified-opencode-version":
				output.Installations[2].Version = "invalid/version"
			case "execution-capability":
				output.Installations[0].Capabilities = []Capability{CapabilityExecute}
			case "claude-execution-capability":
				output.Installations[1].Capabilities = []Capability{CapabilityExecute}
			case "grok-execution-capability":
				output.Installations[3].Capabilities = []Capability{CapabilityExecute}
			case "opencode-execution-capability":
				output.Installations[2].Capabilities = []Capability{CapabilityExecute}
			}
			if err := output.ValidateDiscovery(input); err == nil {
				t.Fatal("invalid observation accepted")
			}
		})
	}
}

func TestHarnessVersionAllowedUsesSemVerPrecedence(t *testing.T) {
	cases := []struct {
		harness Harness
		version string
		want    bool
	}{
		{ClaudeCode, "2.1.236", true},
		{ClaudeCode, "2.1.237", true},
		{ClaudeCode, "2.2.0", true},
		{ClaudeCode, "3.0.0-rc.1", true},
		{ClaudeCode, "2.1.236+build.5", true},
		{ClaudeCode, "2.1.237-beta.1", true},
		{ClaudeCode, "2.1.236-rc.1", false},
		{ClaudeCode, "2.1.235", false},
		{ClaudeCode, "1.99.99", false},
		{ClaudeCode, "", false},
		{ClaudeCode, "v2.1.237", false},
		{ClaudeCode, "2.1", false},
		{ClaudeCode, "02.1.237", false},
		{OpenCode, "1.18.32", true},
		{OpenCode, "1.18.33", true},
		{OpenCode, "1.18.31", false},
		{GrokBuild, "1.0.41", true},
		{GrokBuild, "1.0.42", true},
		{GrokBuild, "1.0.40", false},
		{Codex, "9.9.9", false},
	}
	for _, c := range cases {
		if got := HarnessVersionAllowed(c.harness, c.version); got != c.want {
			t.Errorf("HarnessVersionAllowed(%q, %q) = %v, want %v", c.harness, c.version, got, c.want)
		}
	}
}

func TestGrokSubscriptionVersionAllowedUsesSemVerPrecedence(t *testing.T) {
	if GrokProtocolVersion != "1.0.41" || GrokSubscriptionBaselineVersion != "1.0.46" {
		t.Fatalf("unexpected Grok baselines: api=%q subscription=%q", GrokProtocolVersion, GrokSubscriptionBaselineVersion)
	}
	cases := []struct {
		version string
		want    bool
	}{
		{"1.0.45", false},
		{"1.0.46", true},
		{"1.0.46+build.5", true},
		{"1.0.47", true},
		{"1.0.52", true},
		{"1.1.0", true},
		{"2.0.0", true},
		{"1.0.47-rc.1", true},
		{"1.0.46-rc.1", false},
		{"1.0.41", false},
		{"", false},
		{"v1.0.46", false},
		{"1.0", false},
		{"01.0.46", false},
		{"1.0.046", false},
		{"not-a-version", false},
	}
	for _, c := range cases {
		if got := GrokSubscriptionVersionAllowed(c.version); got != c.want {
			t.Errorf("GrokSubscriptionVersionAllowed(%q) = %v, want %v", c.version, got, c.want)
		}
	}
}
