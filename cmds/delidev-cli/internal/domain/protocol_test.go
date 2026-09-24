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
	for _, change := range []string{"unrequested", "foreign-protocol", "unverified-version", "missing", "false-state", "unimplemented-profile", "execution-capability"} {
		t.Run(change, func(t *testing.T) {
			input, output := protocolOutput()
			switch change {
			case "unrequested":
				input.VerifyProtocol = false
			case "foreign-protocol":
				output.Installations[0].Protocol.Protocol = GrokACP
			case "unverified-version":
				output.Installations[0].Version = "0.152.0"
			case "missing":
				output.Installations[0].Protocol = nil
			case "false-state":
				output.Installations[0].ProtocolVerified = false
			case "unimplemented-profile":
				output.Installations[1].Protocol.State = ProtocolVerified
				output.Installations[1].ProtocolVerified = true
				output.Installations[1].Protocol.Problem = nil
			case "execution-capability":
				output.Installations[0].Capabilities = []Capability{CapabilityExecute}
			}
			if err := output.ValidateDiscovery(input); err == nil {
				t.Fatal("invalid observation accepted")
			}
		})
	}
}
