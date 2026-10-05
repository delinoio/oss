package domain

import "golang.org/x/mod/semver"

type NativeProtocol string

const (
	CodexAppServer   NativeProtocol = "codex-app-server-v2"
	ClaudeStreamJSON NativeProtocol = "claude-stream-json"
	OpenCodeHTTP     NativeProtocol = "opencode-http"
	GrokACP          NativeProtocol = "grok-acp"
)
const (
	CodexMinimumVersion = "0.151.0"
	// CodexProtocolVersion retains the historical fixture baseline. Runtime
	// admission uses CodexVersionAllowed and records the installed version.
	CodexProtocolVersion    = CodexMinimumVersion
	ClaudeProtocolVersion   = "2.1.236"
	GrokProtocolVersion     = "1.0.41"
	OpenCodeProtocolVersion = "1.18.32"
)

// CodexVersionAllowed permits an actual native attempt, not inferred protocol
// readiness. Every accepted installation still requires its native handshake.
func CodexVersionAllowed(version string) bool {
	return ValidInstallationVersion(version) && semver.IsValid("v"+version) && semver.Compare("v"+version, "v"+CodexMinimumVersion) >= 0
}

type ProtocolState string

const (
	ProtocolVerified    ProtocolState = "verified"
	ProtocolUnsupported ProtocolState = "unsupported"
	ProtocolFailed      ProtocolState = "failed"
)

type ProtocolObservation struct {
	Protocol   NativeProtocol   `json:"protocol"`
	State      ProtocolState    `json:"state"`
	Problem    *Error           `json:"problem,omitempty"`
	Diagnostic *CodexDiagnostic `json:"diagnostic,omitempty"`
}

func ProtocolFor(harness Harness) NativeProtocol {
	switch harness {
	case Codex:
		return CodexAppServer
	case ClaudeCode:
		return ClaudeStreamJSON
	case OpenCode:
		return OpenCodeHTTP
	case GrokBuild:
		return GrokACP
	default:
		return ""
	}
}
func ProtocolProblem(state ProtocolState) *Error {
	switch state {
	case ProtocolUnsupported:
		return Fail(Unsupported, "This installation has no validated native protocol profile.", "Select a supported harness version and refresh protocol discovery; execution is unavailable until native validation succeeds.")
	case ProtocolFailed:
		return Fail(Unavailable, "The installed harness did not complete native protocol validation.", "Inspect the selected Worker installation and refresh protocol discovery.")
	default:
		return nil
	}
}
func (i *Installation) validateProtocol(requested bool) error {
	if !requested || i.State != InstallationDetected {
		if i.Protocol != nil || i.ProtocolVerified {
			return Fail(InvalidArgument, "Unexpected native protocol observation.", "Report protocol validation only when requested for a detected installation.")
		}
		return nil
	}
	if i.Protocol == nil || i.Protocol.Protocol != ProtocolFor(i.Harness) {
		return Fail(InvalidArgument, "Missing or mismatched native protocol observation.", "Report the accepted harness's native protocol outcome.")
	}
	switch i.Protocol.State {
	case ProtocolVerified:
		supported := (i.Harness == Codex && CodexVersionAllowed(i.Version)) || (i.Harness == ClaudeCode && i.Version == ClaudeProtocolVersion) || (i.Harness == GrokBuild && i.Version == GrokProtocolVersion) || (i.Harness == OpenCode && i.Version == OpenCodeProtocolVersion)
		if !supported || !i.ProtocolVerified || i.Protocol.Problem != nil || i.Protocol.Diagnostic != nil {
			return Fail(InvalidArgument, "The reported native profile is not validated.", "Use a supported protocol profile; version detection alone cannot grant capabilities.")
		}
	case ProtocolFailed, ProtocolUnsupported:
		if i.ProtocolVerified {
			return Fail(InvalidArgument, "Failed native validation cannot claim readiness.", "Report the actual native protocol state.")
		}
	default:
		return Fail(InvalidArgument, "Unknown native protocol state.", "Report a supported protocol outcome.")
	}
	if d := i.Protocol.Diagnostic; d != nil {
		if i.Harness != Codex || d.Validate() != nil || d.DetectedVersion != i.Version || i.Protocol.State == ProtocolVerified {
			return Fail(InvalidArgument, "Invalid native failure attribution.", "Refresh discovery without raw error metadata.")
		}
	}
	i.Protocol.Problem = ProtocolProblem(i.Protocol.State)
	return nil
}
