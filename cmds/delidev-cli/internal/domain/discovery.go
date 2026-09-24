package domain

import (
	"regexp"
)

// Detected proves a successful version probe only. Native protocol capability
// and selected-account readiness require their own subsequent validation.
type InstallationState string

const (
	InstallationUnchecked    InstallationState = "unchecked"
	InstallationDetected     InstallationState = "detected"
	InstallationMissing      InstallationState = "missing"
	InstallationDenied       InstallationState = "permission-denied"
	InstallationIncompatible InstallationState = "incompatible"
	InstallationFailed       InstallationState = "failed"
)

func Harnesses() []Harness { return []Harness{Codex, ClaudeCode, OpenCode, GrokBuild} }

type ExecutableSelection struct {
	Harness Harness `json:"harness"`
	Path    string  `json:"path,omitempty"`
}

// A supplied list replaces all selections; omitted harnesses use Worker PATH.
type ExecutableSelections struct {
	Executables []ExecutableSelection `json:"executables"`
}

func (s ExecutableSelections) Validate() error {
	if s.Executables == nil {
		return Fail(InvalidArgument, "Executable selections require an explicit list.", "Use an empty executables array to reset all harnesses to Worker PATH.")
	}
	if len(s.Executables) > 4 {
		return Fail(InvalidArgument, "Too many executable selections.", "Configure each of the four harnesses once.")
	}
	seen := map[Harness]bool{}
	for _, selection := range s.Executables {
		if !selection.Harness.Valid() || seen[selection.Harness] {
			return Fail(InvalidArgument, "Invalid executable selection.", "Use distinct supported harness identifiers.")
		}
		seen[selection.Harness] = true
		if err := Text(selection.Path, "executable path", 4096, false); err != nil {
			return err
		}
	}
	return nil
}

func (s ExecutableSelections) Installations() []Installation {
	result := make([]Installation, 0, 4)
	for _, harness := range Harnesses() {
		i := Installation{Harness: harness, State: InstallationUnchecked, Capabilities: []Capability{}}
		for _, selection := range s.Executables {
			if selection.Harness == harness {
				i.ExplicitPath = selection.Path
			}
		}
		result = append(result, i)
	}
	return result
}

type HarnessDiscoveryInput struct {
	Revision   uint64               `json:"revision"`
	Selections ExecutableSelections `json:"selections"`
}
type HarnessDiscoveryOutput struct {
	Installations []Installation `json:"installations"`
}

var installationVersion = regexp.MustCompile(`^[0-9]{1,8}\.[0-9]{1,8}\.[0-9]{1,8}([+-][a-zA-Z0-9.-]{1,64})?$`)

func ValidInstallationVersion(version string) bool { return installationVersion.MatchString(version) }

func InstallationProblem(state InstallationState) *Error {
	switch state {
	case InstallationMissing:
		return Fail(NotFound, "The selected harness executable was not found.", "Install the harness yourself on this Worker or correct its explicit path, then refresh discovery.")
	case InstallationDenied:
		return Fail(PermissionDenied, "The Worker cannot execute the selected harness.", "Check executable permissions and access on the selected Worker, then refresh discovery.")
	case InstallationIncompatible:
		return Fail(Unsupported, "The selected executable did not provide a supported version response.", "Check the native harness installation and selected path; install a supported version yourself.")
	case InstallationFailed:
		return Fail(Unavailable, "The harness version probe failed or exceeded its bound.", "Check the selected Worker installation and refresh discovery; no execution readiness was established.")
	default:
		return nil
	}
}

// ValidateDiscovery rejects claimed execution capabilities and reconstructs
// diagnostics locally instead of persisting arbitrary Worker error strings.
func (o *HarnessDiscoveryOutput) ValidateDiscovery(input HarnessDiscoveryInput) error {
	if len(o.Installations) != 4 {
		return Fail(InvalidArgument, "Discovery must report all four harnesses.", "Refresh using a compatible Worker.")
	}
	expected := input.Selections.Installations()
	for index := range o.Installations {
		i := &o.Installations[index]
		if i.Harness != expected[index].Harness || i.ExplicitPath != expected[index].ExplicitPath || i.ProtocolVerified || len(i.Capabilities) != 0 || i.ObservedAt != nil {
			return Fail(InvalidArgument, "Discovery reported unrequested selection or unverified capabilities.", "Report only the version probe for the accepted selections.")
		}
		if err := Text(i.ResolvedPath, "resolved executable path", 4096, false); err != nil {
			return err
		}
		switch i.State {
		case InstallationDetected:
			if i.ResolvedPath == "" || !ValidInstallationVersion(i.Version) || i.Problem != nil {
				return Fail(InvalidArgument, "Detected installation metadata is incomplete.", "Report a resolved executable and bounded version.")
			}
		case InstallationMissing, InstallationDenied, InstallationIncompatible, InstallationFailed:
			if i.Version != "" {
				return Fail(InvalidArgument, "Failed discovery cannot claim a version.", "Clear unverified version metadata.")
			}
		default:
			return Fail(InvalidArgument, "Unknown discovery outcome.", "Report a supported terminal discovery state.")
		}
		i.Problem = InstallationProblem(i.State)
		i.Capabilities = []Capability{}
	}
	return nil
}
